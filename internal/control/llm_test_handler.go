package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"rah/internal/config"
	"rah/internal/engine/steps"
)

// llmTestRequest is the optional JSON body for the test endpoint.
type llmTestRequest struct {
	Prompt    string `json:"prompt"`
	MaxTokens int    `json:"max_tokens"`
	APIKey    string `json:"api_key"`
}

// findLLMModel returns a pointer to the named model within a snapshot, or nil.
func findLLMModel(cfgMgr *config.Manager, alias string) *config.LLMModelConfig {
	models := cfgMgr.LLM().Models
	for i := range models {
		if models[i].Alias == alias {
			return &models[i]
		}
	}
	return nil
}

// llmTestModelHandler sends a test prompt to the LLM model identified by alias
// and returns the response along with latency and token counts.
func llmTestModelHandler(w http.ResponseWriter, r *http.Request, cfgMgr *config.Manager, alias string, sm steps.SecretLoader) {
	if r.Method != http.MethodPost {
		writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	model := findLLMModel(cfgMgr, alias)
	if model == nil {
		writeAIError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", alias))
		return
	}

	// Parse optional request body; use defaults if absent or unparseable.
	prompt := "Say OK"
	maxTokens := 10
	apiKey := ""

	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err == nil && len(bytes.TrimSpace(body)) > 0 {
			var req llmTestRequest
			if err := json.Unmarshal(body, &req); err == nil {
				if req.Prompt != "" {
					prompt = req.Prompt
				}
				if req.MaxTokens > 0 {
					maxTokens = req.MaxTokens
				}
				apiKey = req.APIKey
			}
		}
	}

	// Resolve API key: body field takes precedence, else resolve model's APIKeyRef through
	// the secrets manager (supports "env:VAR", "file:///path", etc.).
	if apiKey == "" {
		ref := model.APIKeyRef
		if sm != nil && ref != "" {
			val, resolveErr := sm.Resolve(context.Background(), ref)
			if resolveErr != nil {
				writeAIError(w, http.StatusBadRequest, fmt.Sprintf("api_key_ref %q: %s", ref, resolveErr.Error()))
				return
			}
			apiKey = string(val)
		} else {
			apiKey = ref
		}
	}

	// Build adapter.
	adapter, err := steps.NewAdapter(*model)
	if err != nil {
		writeAIError(w, http.StatusBadRequest, fmt.Sprintf("unsupported adapter %q: %s", model.Adapter, err.Error()))
		return
	}

	// Resolve wire model ID.
	wireModelID := model.ModelID
	if wireModelID == "" {
		wireModelID = model.Alias
	}

	// EndpointOverride bypasses adapter URL logic entirely.
	// {model} in the override is replaced with the resolved model ID.
	endpoint := model.EndpointOverride
	if endpoint != "" {
		endpoint = strings.ReplaceAll(endpoint, "{model}", wireModelID)
	} else {
		endpoint = adapter.Endpoint(model.BaseURL, wireModelID)
	}
	authName, authValue := adapter.AuthHeader(apiKey)

	llmReq := steps.LLMRequest{
		Messages:            []steps.CanonicalMessage{{Role: steps.RoleUser, Content: prompt}},
		Model:               wireModelID,
		MaxTokens:           maxTokens,
		UseCompletionTokens: model.UseCompletionTokens,
	}

	// Marshal to provider wire format.
	reqBody, err := adapter.Marshal(llmReq)
	if err != nil {
		writeAIError(w, http.StatusInternalServerError, "marshal failed: "+err.Error())
		return
	}

	// Merge provider params if present.
	if len(model.ProviderParams) > 0 {
		var m map[string]any
		if jsonErr := json.Unmarshal(reqBody, &m); jsonErr == nil {
			for k, v := range model.ProviderParams {
				m[k] = v
			}
			if merged, jsonErr := json.Marshal(m); jsonErr == nil {
				reqBody = merged
			}
		}
	}

	log.Printf("[llm-test] alias=%q model_id=%q endpoint=%s prompt=%q max_tokens=%d request_body=%s",
		alias, wireModelID, endpoint, prompt, maxTokens, reqBody)

	// POST to provider with 30s timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		writeAIError(w, http.StatusInternalServerError, "failed to build request: "+err.Error())
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if authName != "" {
		httpReq.Header.Set(authName, authValue)
	}
	if model.Adapter == config.AdapterAnthropic || model.Adapter == config.AdapterBedrock {
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}
	for k, v := range model.ExtraHeaders {
		httpReq.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(httpReq)
	latencyMs := time.Since(start).Milliseconds()

	if err != nil {
		writeAIError(w, http.StatusBadGateway, "provider unreachable: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		writeAIError(w, http.StatusBadGateway, "failed to read provider response: "+err.Error())
		return
	}

	if resp.StatusCode != http.StatusOK {
		body := strings.TrimSpace(string(respBody))
		if body == "" {
			body = "(empty response body)"
		}
		log.Printf("[llm-test] FAIL alias=%q model_id=%q endpoint=%s http_status=%d latency_ms=%d body=%s",
			alias, wireModelID, endpoint, resp.StatusCode, latencyMs, body)
		// Return structured debug info so the UI can surface each field clearly.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // envelope is always 200; ok=false signals failure
		json.NewEncoder(w).Encode(map[string]any{
			"ok": false,
			"debug": map[string]any{
				"endpoint":       endpoint,
				"model_id_sent":  wireModelID,
				"http_status":    resp.StatusCode,
				"request_body":   string(reqBody),
				"response_body":  body,
				"latency_ms":     latencyMs,
			},
			"error": fmt.Sprintf("provider returned HTTP %d — see debug for details", resp.StatusCode),
		})
		return
	}

	llmResp, err := adapter.Unmarshal(respBody)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"ok": false,
			"debug": map[string]any{
				"endpoint":      endpoint,
				"model_id_sent": wireModelID,
				"http_status":   resp.StatusCode,
				"response_body": strings.TrimSpace(string(respBody)),
			},
			"error": "response parse failed: " + err.Error(),
		})
		return
	}

	log.Printf("[llm-test] OK alias=%q model_id=%q latency_ms=%d in_tokens=%d out_tokens=%d",
		alias, wireModelID, latencyMs, llmResp.InputTokens, llmResp.OutputTokens)
	writeAIOK(w, map[string]any{
		"response":      llmResp.Content,
		"latency_ms":    latencyMs,
		"input_tokens":  llmResp.InputTokens,
		"output_tokens": llmResp.OutputTokens,
	})
}
