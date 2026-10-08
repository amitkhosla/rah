package steps

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/amitkhosla/rah/internal/config"
)

// ExecuteLLMDirect performs a single LLM call outside the flow engine.
// No retries, no slots, no rctx — for management-plane use.
func ExecuteLLMDirect(
	modelCfg config.LLMModelConfig,
	apiKey string,
	req LLMRequest,
	timeoutMs int,
) (LLMResponse, error) {
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}
	params, err := resolveCallParams(modelCfg, apiKey)
	if err != nil {
		return LLMResponse{}, err
	}
	body, err := params.adapter.Marshal(req)
	if err != nil {
		return LLMResponse{}, err
	}
	client := getLLMClient(modelCfg.BaseURL, timeoutMs)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, params.endpoint, bytes.NewReader(body))
	if err != nil {
		return LLMResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if params.authName != "" {
		httpReq.Header.Set(params.authName, params.authValue)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return LLMResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return LLMResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return LLMResponse{}, fmt.Errorf("provider returned %d: %s", resp.StatusCode, respBody)
	}
	return params.adapter.Unmarshal(respBody)
}
