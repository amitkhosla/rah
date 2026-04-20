package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ── Anthropic wire types (request) ────────────────────────────────────────────

type anthropicAdapterReq struct {
	Model     string               `json:"model"`
	Messages  []anthropicAdapterMsg `json:"messages"`
	MaxTokens int                   `json:"max_tokens,omitempty"`
	System    string                `json:"system,omitempty"`
	Stream    bool                  `json:"stream,omitempty"`
}

type anthropicAdapterMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []contentBlock
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ── Anthropic wire types (response) ──────────────────────────────────────────

type anthropicAdapterResp struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Role         string                 `json:"role"`
	Content      []anthropicContentBlock `json:"content"`
	Model        string                 `json:"model"`
	StopReason   string                 `json:"stop_reason"`
	StopSequence *string                `json:"stop_sequence"`
	Usage        anthropicAdapterUsage  `json:"usage"`
}

type anthropicAdapterUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ── OpenAI response types (for format normalization) ─────────────────────────

type openAIAdapterResp struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Model   string              `json:"model"`
	Choices []openAIAdapterChoice `json:"choices"`
	Usage   openAIAdapterUsage  `json:"usage"`
}

type openAIAdapterChoice struct {
	Index        int                `json:"index"`
	Message      openAIAdapterMsg   `json:"message"`
	FinishReason string             `json:"finish_reason"`
}

type openAIAdapterMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIAdapterUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// RegisterAnthropicAdapter registers POST /ai/v1/messages on mux.
//
// Setting ANTHROPIC_BASE_URL=http://localhost:<mport>/ai (or any prefix) lets
// the Anthropic SDK and Claude Code transparently route API calls through the
// RAH smart-router or classifier gateway.
//
// Target flow endpoint selection (first match wins):
//  1. X-Gateway-Endpoint request header
//  2. RAH_ADAPTER_ENDPOINT env var
//  3. Default: /ai/smart-chat
//
// Input field name (first match wins):
//  1. X-Input-Field request header
//  2. Default: message
//
// gatewayBase is the hot-path server URL, e.g. http://localhost:8080.
func RegisterAnthropicAdapter(mux *http.ServeMux, gatewayBase string) {
	client := &http.Client{Timeout: 120 * time.Second}

	mux.HandleFunc("/ai/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			anthropicWriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
			return
		}

		var req anthropicAdapterReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			anthropicWriteError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
			return
		}

		userMsg := anthropicExtractLastUserMsg(req.Messages)
		if userMsg == "" {
			anthropicWriteError(w, http.StatusBadRequest, "invalid_request_error", "no user message content found")
			return
		}

		targetEndpoint := r.Header.Get("X-Gateway-Endpoint")
		if targetEndpoint == "" {
			targetEndpoint = os.Getenv("RAH_ADAPTER_ENDPOINT")
		}
		if targetEndpoint == "" {
			targetEndpoint = "/ai/smart-chat"
		}

		inputField := r.Header.Get("X-Input-Field")
		if inputField == "" {
			inputField = "message"
		}

		gwBody, _ := json.Marshal(map[string]string{inputField: userMsg})

		gwReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, gatewayBase+targetEndpoint, bytes.NewReader(gwBody))
		if err != nil {
			anthropicWriteError(w, http.StatusInternalServerError, "api_error", "failed to build gateway request")
			return
		}
		gwReq.Header.Set("Content-Type", "application/json")
		// Forward session / tenant routing headers
		for _, h := range []string{"X-Session-Id", "X-Tenant", "X-Tenant-Id", "X-Correlation-Id"} {
			if v := r.Header.Get(h); v != "" {
				gwReq.Header.Set(h, v)
			}
		}

		gwResp, err := client.Do(gwReq)
		if err != nil {
			anthropicWriteError(w, http.StatusBadGateway, "api_error", "gateway unreachable: "+err.Error())
			return
		}
		defer gwResp.Body.Close()

		respBytes, err := io.ReadAll(gwResp.Body)
		if err != nil {
			anthropicWriteError(w, http.StatusInternalServerError, "api_error", "failed to read gateway response")
			return
		}

		if gwResp.StatusCode != http.StatusOK {
			anthropicWriteError(w, gwResp.StatusCode, "api_error", strings.TrimSpace(string(respBytes)))
			return
		}

		out, err := anthropicNormalize(respBytes, req.Model)
		if err != nil {
			// Could not parse/normalize — pass through raw (best effort)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(respBytes)
			return
		}

		if req.Stream {
			anthropicWriteStream(w, out)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		}
	})
}

// anthropicExtractLastUserMsg extracts plain text from the last user-role message.
// content may be a JSON string or an array of content blocks.
func anthropicExtractLastUserMsg(msgs []anthropicAdapterMsg) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "user" {
			continue
		}
		raw := m.Content
		if len(raw) == 0 {
			continue
		}
		if raw[0] == '"' {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}
		if raw[0] == '[' {
			var blocks []anthropicContentBlock
			if json.Unmarshal(raw, &blocks) == nil {
				var parts []string
				for _, b := range blocks {
					if b.Type == "text" && b.Text != "" {
						parts = append(parts, b.Text)
					}
				}
				if len(parts) > 0 {
					return strings.Join(parts, "\n")
				}
			}
		}
	}
	return ""
}

// anthropicNormalize detects Anthropic vs OpenAI response format and converts
// to Anthropic format. Unknown formats are wrapped as a text block.
func anthropicNormalize(data []byte, requestModel string) (anthropicAdapterResp, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return anthropicAdapterResp{}, err
	}

	// Already Anthropic format: top-level "content" array + "stop_reason"
	if _, hasContent := raw["content"]; hasContent {
		if _, hasStop := raw["stop_reason"]; hasStop {
			var resp anthropicAdapterResp
			if err := json.Unmarshal(data, &resp); err == nil {
				if resp.Model == "" {
					resp.Model = requestModel
				}
				return resp, nil
			}
		}
	}

	// OpenAI format: "choices" array
	if _, hasChoices := raw["choices"]; hasChoices {
		var oai openAIAdapterResp
		if err := json.Unmarshal(data, &oai); err != nil {
			return anthropicAdapterResp{}, err
		}
		text := ""
		if len(oai.Choices) > 0 {
			text = oai.Choices[0].Message.Content
		}
		model := oai.Model
		if model == "" {
			model = requestModel
		}
		stopReason := "end_turn"
		if len(oai.Choices) > 0 {
			switch oai.Choices[0].FinishReason {
			case "length":
				stopReason = "max_tokens"
			case "tool_calls", "function_call":
				stopReason = "tool_use"
			}
		}
		msgID := "msg_gw_" + oai.ID
		if oai.ID == "" {
			msgID = fmt.Sprintf("msg_gw_%d", time.Now().UnixNano())
		}
		return anthropicAdapterResp{
			ID:         msgID,
			Type:       "message",
			Role:       "assistant",
			Content:    []anthropicContentBlock{{Type: "text", Text: text}},
			Model:      model,
			StopReason: stopReason,
			Usage: anthropicAdapterUsage{
				InputTokens:  oai.Usage.PromptTokens,
				OutputTokens: oai.Usage.CompletionTokens,
			},
		}, nil
	}

	// Unknown format — wrap raw bytes as plain text (last resort)
	return anthropicAdapterResp{
		ID:         fmt.Sprintf("msg_gw_%d", time.Now().UnixNano()),
		Type:       "message",
		Role:       "assistant",
		Content:    []anthropicContentBlock{{Type: "text", Text: string(data)}},
		Model:      requestModel,
		StopReason: "end_turn",
		Usage:      anthropicAdapterUsage{},
	}, nil
}

// anthropicWriteStream emits a minimal Anthropic SSE stream containing all
// content in a single delta event, then closes with message_stop.
// This lets streaming-only SDK clients work correctly, albeit without
// token-by-token streaming (all tokens arrive at once).
func anthropicWriteStream(w http.ResponseWriter, resp anthropicAdapterResp) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, canFlush := w.(http.Flusher)

	text := ""
	if len(resp.Content) > 0 {
		text = resp.Content[0].Text
	}

	writeSSE := func(event string, payload interface{}) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if canFlush {
			flusher.Flush()
		}
	}

	writeSSE("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            resp.ID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         resp.Model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]int{"input_tokens": resp.Usage.InputTokens, "output_tokens": 0},
		},
	})

	writeSSE("content_block_start", map[string]interface{}{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]string{"type": "text", "text": ""},
	})

	writeSSE("ping", map[string]string{"type": "ping"})

	writeSSE("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]string{"type": "text_delta", "text": text},
	})

	writeSSE("content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": 0,
	})

	writeSSE("message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": resp.StopReason, "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": resp.Usage.OutputTokens},
	})

	writeSSE("message_stop", map[string]string{"type": "message_stop"})
}

func anthropicWriteError(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]string{
			"type":    errType,
			"message": msg,
		},
	})
	_, _ = w.Write(b)
}
