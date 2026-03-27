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
	ModelConfig config.LLMModelConfig
	APIKey      string  // literal key resolved at bake time
	TimeoutMs   int     // per-request timeout; default 30 000 ms
	MaxRetries  int     // default 2
	PromptSlot  int     // ByteSlots index for the user prompt input
	ResultSlot  int     // ByteSlots index for the LLM text response output
	SystemSlot  int     // ByteSlots index for the system prompt; -1 = not used
	MaxTokens   int     // max output tokens; falls back to ModelConfig.MaxTokens
	Temperature float64 // 0.0 = use model default
	OnExceed    string  // "reject" (default) — return 413 when prompt exceeds context
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
func LLMCall(cfg LLMCallConfig) engine.Instruction {
	adapter, err := NewAdapter(cfg.ModelConfig.Adapter)
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

	endpoint := adapter.Endpoint(cfg.ModelConfig.BaseURL, cfg.ModelConfig.Slug)
	authName, authValue := adapter.AuthHeader(cfg.APIKey)
	endpointHost := extractUpstreamHost(endpoint)

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

			// 2. Read system prompt
			var systemContent string
			if cfg.SystemSlot >= 0 && cfg.SystemSlot < len(ctx.ByteSlots) {
				systemContent = string(ctx.ByteSlots[cfg.SystemSlot])
			}

			// 3. Build canonical request
			req := LLMRequest{
				Messages:    []CanonicalMessage{{Role: RoleUser, Content: promptContent}},
				System:      systemContent,
				Model:       cfg.ModelConfig.Slug,
				MaxTokens:   maxTokens,
				Temperature: cfg.Temperature,
			}

			// 4. Token limit check (reject — no truncation)
			if cfg.ModelConfig.Capabilities.MaxContextTokens > 0 {
				est := estimateRequestTokens(req) + maxTokens
				if est > cfg.ModelConfig.Capabilities.MaxContextTokens {
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
			body, marshalErr := adapter.Marshal(req)
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
			client := getLLMClient(cfg.ModelConfig.BaseURL, timeoutMs)
			attempts := maxRetries + 1

			var lastStatus int
			for attempt := 1; attempt <= attempts; attempt++ {
				start := time.Now()

				reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
				httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
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
				if authName != "" {
					httpReq.Header.Set(authName, authValue)
				}
				// Anthropic requires API version header
				if cfg.ModelConfig.Adapter == config.AdapterAnthropic {
					httpReq.Header.Set("anthropic-version", "2023-06-01")
				}

				resp, doErr := client.Do(httpReq)
				cancel()
				elapsed := time.Since(start)

				atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, elapsed.Nanoseconds())
				atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

				if doErr != nil {
					if ctx.Obs != nil {
						ctx.Obs.RecordUpstream(endpointHost, elapsed, int64(len(body)), 0)
						ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, observability.UpstreamEvent{
							Host: endpointHost, URL: endpoint, Attempt: attempt,
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
					ctx.Obs.RecordUpstream(endpointHost, elapsed, int64(len(body)), int64(len(respBody)))
					ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, observability.UpstreamEvent{
						Host: endpointHost, URL: endpoint, Attempt: attempt,
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
				llmResp, unmarshalErr := adapter.Unmarshal(respBody)
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
