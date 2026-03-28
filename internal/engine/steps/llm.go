package steps

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/observability"
	"rah/internal/rctx"
)

// LLMCallConfig is resolved once at bake time and captured in the instruction closure.
type LLMCallConfig struct {
	ModelConfig  config.LLMModelConfig            // bake-time resolved model
	APIKey       string                           // literal key resolved at bake time
	TimeoutMs    int                              // per-request timeout; default 30 000 ms
	MaxRetries   int                              // default 2
	PromptSlot   int                              // ByteSlots index for the user prompt input
	ResultSlot   int                              // ByteSlots index for the LLM text response output
	SystemSlot   int                              // ByteSlots index for the system prompt; -1 = not used
	MaxTokens    int                              // max output tokens; falls back to ModelConfig.MaxTokens
	Temperature  float64                          // 0.0 = use model default
	OnExceed     string                           // "reject" (default) — return 413 when prompt exceeds context
	ModelSlot    int                              // >= 0: read model slug from ByteSlots at runtime
	ModelCatalog map[string]config.LLMModelConfig // full catalog for runtime lookup

	// APIKeySlot: if >= 0, read the API key at request time from ctx.ByteSlots[APIKeySlot]
	// instead of using the baked-in APIKey string. Allows per-tenant key injection.
	// Set to -1 (default) to use the baked APIKey.
	APIKeySlot int

	// InputTokensSlot: if >= 0, write response.InputTokens to ctx.IntSlots[InputTokensSlot]
	// after a successful call. Set to -1 (default) to skip.
	InputTokensSlot int

	// OutputTokensSlot: if >= 0, write response.OutputTokens to ctx.IntSlots[OutputTokensSlot]
	// after a successful call. Set to -1 (default) to skip.
	OutputTokensSlot int
}

// llmCallParams holds the runtime-resolved call parameters (adapter, endpoint, auth).
type llmCallParams struct {
	adapter   ProviderAdapter
	endpoint  string
	authName  string
	authValue string
}

// resolveCallParams derives call parameters from a model config and API key.
// Called both at bake time (pre-resolve) and at runtime (dynamic model override).
func resolveCallParams(modelCfg config.LLMModelConfig, apiKey string) (llmCallParams, error) {
	adapter, err := NewAdapter(modelCfg.Adapter)
	if err != nil {
		return llmCallParams{}, err
	}
	endpoint := adapter.Endpoint(modelCfg.BaseURL, modelCfg.Slug)
	authName, authValue := adapter.AuthHeader(apiKey)
	return llmCallParams{
		adapter:   adapter,
		endpoint:  endpoint,
		authName:  authName,
		authValue: authValue,
	}, nil
}

var (
	llmClientOnce  sync.Once
	llmClientCache sync.Map // key: baseURL string → *http.Client
	llmClientCount atomic.Int64
)

func getLLMClient(baseURL string, timeoutMs int) *http.Client {
	if c, ok := llmClientCache.Load(baseURL); ok {
		return c.(*http.Client)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
		Timeout: timeout,
	}
	if llmClientCount.Load() < 256 {
		actual, loaded := llmClientCache.LoadOrStore(baseURL, client)
		if loaded {
			return actual.(*http.Client)
		}
		llmClientCount.Add(1)
	}
	return client
}

// LLMCall returns an engine.Instruction that sends a prompt to an LLM provider
// and writes the text response into ResultSlot.
//
// At runtime:
//  1. Reads prompt from ctx.ByteSlots[cfg.PromptSlot]
//  2. Reads system prompt from ctx.ByteSlots[cfg.SystemSlot] (if SystemSlot >= 0)
//  3. Estimates token count; returns 413 if over model context limit (when OnExceed == "reject")
//  4. Marshals canonical request to provider wire format via adapter
//  5. POSTs to provider endpoint with retry on 429 / 5xx (up to cfg.MaxRetries)
//  6. Writes response text to ctx.ByteSlots[cfg.ResultSlot]
//  7. Writes token counts to ctx.IntSlots[cfg.InputTokensSlot / cfg.OutputTokensSlot] (if >= 0)
func LLMCall(cfg LLMCallConfig) engine.Instruction {
	// Ensure ModelSlot default: 0 would conflict with a real slot, so callers
	// that don't want dynamic routing must explicitly set ModelSlot = -1.
	// For backward compatibility, treat ModelSlot == 0 with nil catalog as "disabled".

	// Resolve baked params using the baked-in key; runtime slot key applied per-request below.
	bakedParams, err := resolveCallParams(cfg.ModelConfig, cfg.APIKey)
	if err != nil {
		// Misconfiguration caught at bake time — return a poisoned instruction
		// that immediately fails every request with 500.
		return engine.Instruction{
			Name: "llm_call[bad_adapter]",
			Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "llm_call: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			},
		}
	}

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = cfg.ModelConfig.MaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}

	bakedEndpointHost := extractUpstreamHost(bakedParams.endpoint)

	return engine.Instruction{
		Name: "llm_call[" + cfg.ModelConfig.Slug + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read prompt
			var promptContent string
			if cfg.PromptSlot >= 0 && cfg.PromptSlot < len(ctx.ByteSlots) {
				promptContent = string(ctx.ByteSlots[cfg.PromptSlot])
			}
			if promptContent == "" {
				// Empty prompt — skip silently, leave result slot empty
				return state.PC + 1
			}

			// Runtime API key override: read from slot if configured.
			// This replaces cfg.APIKey (the baked-in key) for this request.
			apiKey := cfg.APIKey
			if cfg.APIKeySlot >= 0 && cfg.APIKeySlot < len(ctx.ByteSlots) {
				if k := ctx.ByteSlots[cfg.APIKeySlot]; len(k) > 0 {
					apiKey = string(k)
				}
			}

			// Dynamic model override from slot
			activeCfg := cfg.ModelConfig // local copy
			// Re-resolve params with the (possibly overridden) API key.
			activeParams := bakedParams
			if apiKey != cfg.APIKey {
				rp, rpErr := resolveCallParams(cfg.ModelConfig, apiKey)
				if rpErr == nil {
					activeParams = rp
				}
			}
			activeEndpointHost := bakedEndpointHost
			if cfg.ModelSlot >= 0 && len(cfg.ModelCatalog) > 0 &&
				cfg.ModelSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[cfg.ModelSlot]) > 0 {
				slug := string(ctx.ByteSlots[cfg.ModelSlot])
				if mc, ok := cfg.ModelCatalog[slug]; ok {
					activeCfg = mc
					// Resolve API key: prefer runtime slot key, then catalog ref, then baked key.
					dynKey := apiKey // already set to runtime slot key or baked key above
					if mc.APIKeyRef != "" {
						dynKey = mc.APIKeyRef
					}
					rp, rpErr := resolveCallParams(mc, dynKey)
					if rpErr != nil {
						ctx.ResponseStatus = 500
						ctx.Failed = true
						ctx.ErrorCode = 500
						msg := "llm_call: dynamic model adapter error: " + rpErr.Error()
						ctx.ErrorMsg = ctx.Alloc(len(msg))
						copy(ctx.ErrorMsg, msg)
						return engine.StopPlan
					}
					activeParams = rp
					activeEndpointHost = extractUpstreamHost(rp.endpoint)
				} else {
					ctx.ResponseStatus = 500
					ctx.Failed = true
					ctx.ErrorCode = 500
					msg := "llm_call: dynamic model not found: " + slug
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
			}

			// 2. Read system prompt
			var systemContent string
			if cfg.SystemSlot >= 0 && cfg.SystemSlot < len(ctx.ByteSlots) {
				systemContent = string(ctx.ByteSlots[cfg.SystemSlot])
			}

			// 3. Build canonical request
			req := LLMRequest{
				Messages:    []CanonicalMessage{{Role: RoleUser, Content: promptContent}},
				System:      systemContent,
				Model:       activeCfg.Slug,
				MaxTokens:   maxTokens,
				Temperature: cfg.Temperature,
			}

			// 4. Token limit check (reject — no truncation)
			if activeCfg.Capabilities.MaxContextTokens > 0 {
				est := estimateRequestTokens(req) + maxTokens
				if est > activeCfg.Capabilities.MaxContextTokens {
					ctx.ResponseStatus = 413
					ctx.Failed = true
					ctx.ErrorCode = 413
					msg := "llm_call: prompt exceeds model context limit"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
			}

			// 5. Marshal to provider wire format
			body, marshalErr := activeParams.adapter.Marshal(req)
			if marshalErr != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "llm_call: marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 6. HTTP call with retry
			client := getLLMClient(activeCfg.BaseURL, timeoutMs)
			attempts := maxRetries + 1

			var lastStatus int
			for attempt := 1; attempt <= attempts; attempt++ {
				start := time.Now()

				reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
				httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, activeParams.endpoint, bytes.NewReader(body))
				if reqErr != nil {
					cancel()
					ctx.ResponseStatus = 500
					ctx.Failed = true
					ctx.ErrorCode = 500
					msg := "llm_call: request build failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				httpReq.Header.Set("Content-Type", "application/json")
				if activeParams.authName != "" {
					httpReq.Header.Set(activeParams.authName, activeParams.authValue)
				}
				// Anthropic requires API version header
				if activeCfg.Adapter == config.AdapterAnthropic {
					httpReq.Header.Set("anthropic-version", "2023-06-01")
				}

				resp, doErr := client.Do(httpReq)
				cancel()
				elapsed := time.Since(start)

				atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, elapsed.Nanoseconds())
				atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

				if doErr != nil {
					if ctx.Obs != nil {
						ctx.Obs.RecordUpstream(activeEndpointHost, elapsed, int64(len(body)), 0)
						ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, observability.UpstreamEvent{
							Host: activeEndpointHost, URL: activeParams.endpoint, Attempt: attempt,
							Err: doErr.Error(), TotalNs: elapsed.Nanoseconds(),
						})
					}
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "llm_call: upstream error"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				respBody, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastStatus = resp.StatusCode

				atomic.AddInt64(&ctx.Timing.UpstreamBytesRx, int64(len(respBody)))
				atomic.AddInt64(&ctx.Timing.UpstreamBytesTx, int64(len(body)))

				if ctx.Obs != nil {
					ctx.Obs.RecordUpstream(activeEndpointHost, elapsed, int64(len(body)), int64(len(respBody)))
					ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, observability.UpstreamEvent{
						Host: activeEndpointHost, URL: activeParams.endpoint, Attempt: attempt,
						Status: resp.StatusCode, TotalNs: elapsed.Nanoseconds(),
						BytesSent: int64(len(body)), BytesReceived: int64(len(respBody)),
					})
				}

				if readErr != nil {
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "llm_call: response read failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// Retry on rate-limit or transient server errors
				if resp.StatusCode == 429 || resp.StatusCode >= 500 {
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					ctx.ResponseStatus = resp.StatusCode
					ctx.Failed = true
					ctx.ErrorCode = int16(resp.StatusCode)
					msg := "llm_call: provider error"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				if resp.StatusCode != http.StatusOK {
					ctx.ResponseStatus = resp.StatusCode
					ctx.Failed = true
					ctx.ErrorCode = int16(resp.StatusCode)
					msg := "llm_call: unexpected status"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// 7. Unmarshal response
				llmResp, unmarshalErr := activeParams.adapter.Unmarshal(respBody)
				if unmarshalErr != nil {
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "llm_call: response parse failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// 8. Write result to slot
				if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
					result := []byte(llmResp.Content)
					ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(result))
					copy(ctx.ByteSlots[cfg.ResultSlot], result)
				}

				// 9. Write token usage to IntSlots (if configured).
				if cfg.InputTokensSlot >= 0 && cfg.InputTokensSlot < len(ctx.IntSlots) {
					ctx.IntSlots[cfg.InputTokensSlot] = int64(llmResp.InputTokens)
				}
				if cfg.OutputTokensSlot >= 0 && cfg.OutputTokensSlot < len(ctx.IntSlots) {
					ctx.IntSlots[cfg.OutputTokensSlot] = int64(llmResp.OutputTokens)
				}

				return state.PC + 1
			}

			// All retries exhausted
			ctx.ResponseStatus = lastStatus
			if ctx.ResponseStatus == 0 {
				ctx.ResponseStatus = 502
			}
			ctx.Failed = true
			ctx.ErrorCode = int16(ctx.ResponseStatus)
			msg := "llm_call: all retries exhausted"
			ctx.ErrorMsg = ctx.Alloc(len(msg))
			copy(ctx.ErrorMsg, msg)
			return engine.StopPlan
		},
	}
}

// llmBackoff returns the delay before retry attempt n (1-based).
// Uses simple exponential backoff: 500ms, 1s, 2s, …
func llmBackoff(attempt int) time.Duration {
	d := 500 * time.Millisecond
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > 4*time.Second {
			d = 4 * time.Second
			break
		}
	}
	return d
}
