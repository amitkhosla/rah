package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// llmChatRequest is the body for POST /ai/llm/chat.
// Follows the same alias-based model selection as the rest of the AI management API.
type llmChatRequest struct {
	Model     string           `json:"model"`    // registered model alias
	System    string           `json:"system"`   // system prompt
	Messages  []llmChatMessage `json:"messages"` // conversation history + current user message
	MaxTokens int              `json:"max_tokens"`
}

type llmChatMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}

// llmChatResponse mirrors the envelope returned by the other AI management endpoints.
type llmChatResponse struct {
	Content      string `json:"content"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// llmChatModelHandler handles POST /ai/llm/chat.
// It follows the same pattern as llmTestModelHandler: look up the model alias,
// resolve its API key through the secrets manager, pick the right adapter,
// and call the provider — no provider credentials are needed in Studio.
func llmChatModelHandler(w http.ResponseWriter, r *http.Request, cfgMgr *config.Manager, sm steps.SecretLoader) {
	if r.Method != http.MethodPost {
		writeAIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeAIError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	var req llmChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAIError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Model == "" {
		writeAIError(w, http.StatusBadRequest, "model alias required")
		return
	}
	if len(req.Messages) == 0 {
		writeAIError(w, http.StatusBadRequest, "messages required")
		return
	}

	model := findLLMModel(cfgMgr, req.Model)
	if model == nil {
		writeAIError(w, http.StatusNotFound, fmt.Sprintf("model %q not registered — add it in AI â†’ Models", req.Model))
		return
	}

	// Resolve API key through the secrets manager (same as llmTestModelHandler).
	apiKey := model.APIKeyRef
	if apiKey != "" && sm != nil {
		val, resolveErr := sm.Resolve(context.Background(), apiKey)
		if resolveErr != nil {
			writeAIError(w, http.StatusBadRequest, fmt.Sprintf("api_key_ref %q: %s", apiKey, resolveErr.Error()))
			return
		}
		apiKey = string(val)
	}

	adapter, err := steps.NewAdapter(*model)
	if err != nil {
		writeAIError(w, http.StatusBadRequest, fmt.Sprintf("unsupported adapter %q", model.Adapter))
		return
	}

	wireModelID := model.ModelID
	if wireModelID == "" {
		wireModelID = model.Alias
	}

	endpoint := model.EndpointOverride
	if endpoint != "" {
		endpoint = strings.ReplaceAll(endpoint, "{model}", wireModelID)
	} else {
		endpoint = adapter.Endpoint(model.BaseURL, wireModelID)
	}
	authName, authValue := adapter.AuthHeader(apiKey)

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = model.MaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	msgs := make([]steps.CanonicalMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = steps.CanonicalMessage{
			Role:    steps.MessageRole(m.Role),
			Content: m.Content,
		}
	}

	llmReq := steps.LLMRequest{
		Messages:            msgs,
		System:              req.System,
		Model:               wireModelID,
		MaxTokens:           maxTokens,
		UseCompletionTokens: model.UseCompletionTokens,
	}

	reqBody, err := adapter.Marshal(llmReq)
	if err != nil {
		writeAIError(w, http.StatusInternalServerError, "marshal failed: "+err.Error())
		return
	}

	// Merge provider-specific params (same as llmTestModelHandler).
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

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
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

	resp, err := http.DefaultClient.Do(httpReq)
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
		writeAIError(w, http.StatusBadGateway,
			fmt.Sprintf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody))))
		return
	}

	llmResp, err := adapter.Unmarshal(respBody)
	if err != nil {
		writeAIError(w, http.StatusBadGateway, "response parse failed: "+err.Error())
		return
	}

	writeAIOK(w, llmChatResponse{
		Content:      llmResp.Content,
		InputTokens:  llmResp.InputTokens,
		OutputTokens: llmResp.OutputTokens,
	})
}
