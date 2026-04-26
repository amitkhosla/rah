package control

import (
	"bytes"
	"encoding/json"
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
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ── Anthropic wire types (response) ──────────────────────────────────────────

type anthropicAdapterResp struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"` // Preserve raw to handle all block types
	Model        string          `json:"model"`
	StopReason   string          `json:"stop_reason"`
	StopSequence *string         `json:"stop_sequence"`
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

		// Read and parse the full Anthropic request
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			anthropicWriteError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
			return
		}

		var req anthropicAdapterReq
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			anthropicWriteError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
			return
		}

		// Validate that there's at least a message
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

		// Forward the full Anthropic request to the gateway (preserving tools, system, etc.)
		gwReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, gatewayBase+targetEndpoint, bytes.NewReader(bodyBytes))
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

		// Return the response as-is if it's already in Anthropic format
		// This preserves tool_use blocks and other content types
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(respBytes)
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

// Note: anthropicNormalize and anthropicWriteStream are no longer used.
// The adapter now forwards responses as-is from the gateway to preserve
// all content types including tool_use blocks.

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
