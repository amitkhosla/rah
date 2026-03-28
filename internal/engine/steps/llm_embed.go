package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rah/internal/engine"
	"rah/internal/observability"
	"rah/internal/rctx"
)

// EmbedTextConfig is resolved at bake time and captured in the instruction closure.
type EmbedTextConfig struct {
	// InputSlot: ByteSlot index holding the text to embed
	InputSlot int
	// ResultSlot: ByteSlot index to write the embedding vector as JSON float array
	// e.g. [0.0123, -0.4521, 0.9812, ...]
	ResultSlot int
	// DimSlot: IntSlot index to write the embedding dimension count (-1 = skip)
	DimSlot int
	// Provider: which adapter to use ("openai", "ollama", "gemini"; "anthropic" not supported)
	Provider EmbedProvider
	// Model: embedding model name, e.g. "text-embedding-3-small", "nomic-embed-text"
	Model string
	// BaseURL: override endpoint (for Ollama or self-hosted)
	BaseURL string
	// APIKey: resolved at bake time
	APIKey string
	// APIKeySlot: if >= 0, read API key from ctx.ByteSlots[APIKeySlot] at request time
	APIKeySlot int
	// TimeoutMs: per-request timeout in milliseconds (default 10000)
	TimeoutMs int
	// MaxRetries: max retry attempts on 429/503 (default 2)
	MaxRetries int
}

// EmbedProvider is the provider selector for embedding calls.
type EmbedProvider string

const (
	EmbedProviderOpenAI EmbedProvider = "openai"
	EmbedProviderOllama EmbedProvider = "ollama"
	EmbedProviderGemini EmbedProvider = "gemini"
)

var (
	embedClientCache sync.Map // key: endpoint string → *http.Client
	embedClientCount atomic.Int64
)

func getEmbedClient(endpoint string, timeoutMs int) *http.Client {
	if c, ok := embedClientCache.Load(endpoint); ok {
		return c.(*http.Client)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
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
	if embedClientCount.Load() < 256 {
		actual, loaded := embedClientCache.LoadOrStore(endpoint, client)
		if loaded {
			return actual.(*http.Client)
		}
		embedClientCount.Add(1)
	}
	return client
}

// embedEndpoint returns the full URL for the embedding call.
func embedEndpoint(provider EmbedProvider, baseURL, model string) string {
	switch provider {
	case EmbedProviderOpenAI:
		base := baseURL
		if base == "" {
			base = "https://api.openai.com"
		}
		return strings.TrimRight(base, "/") + "/v1/embeddings"
	case EmbedProviderOllama:
		base := baseURL
		if base == "" {
			base = "http://localhost:11434"
		}
		return strings.TrimRight(base, "/") + "/api/embeddings"
	case EmbedProviderGemini:
		base := baseURL
		if base == "" {
			base = "https://generativelanguage.googleapis.com"
		}
		return strings.TrimRight(base, "/") + "/v1beta/models/" + model + ":embedContent"
	default:
		return ""
	}
}

// embedBuildRequest marshals the provider-specific request body.
func embedBuildRequest(provider EmbedProvider, model, text string) ([]byte, error) {
	switch provider {
	case EmbedProviderOpenAI:
		return json.Marshal(map[string]interface{}{
			"model": model,
			"input": text,
		})
	case EmbedProviderOllama:
		return json.Marshal(map[string]interface{}{
			"model":  model,
			"prompt": text,
		})
	case EmbedProviderGemini:
		return json.Marshal(map[string]interface{}{
			"model": "models/" + model,
			"content": map[string]interface{}{
				"parts": []map[string]interface{}{
					{"text": text},
				},
			},
		})
	default:
		return nil, fmt.Errorf("embed_text: unsupported provider %q", provider)
	}
}

// embedParseResponse extracts the float64 vector from the provider response body.
func embedParseResponse(provider EmbedProvider, body []byte) ([]float64, error) {
	switch provider {
	case EmbedProviderOpenAI:
		var resp struct {
			Data []struct {
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("openai embed: unmarshal: %w", err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("openai embed: api error: %s", resp.Error.Message)
		}
		if len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
			return nil, fmt.Errorf("openai embed: empty embedding in response")
		}
		return resp.Data[0].Embedding, nil

	case EmbedProviderOllama:
		var resp struct {
			Embedding []float64 `json:"embedding"`
			Error     string    `json:"error,omitempty"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("ollama embed: unmarshal: %w", err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("ollama embed: api error: %s", resp.Error)
		}
		if len(resp.Embedding) == 0 {
			return nil, fmt.Errorf("ollama embed: empty embedding in response")
		}
		return resp.Embedding, nil

	case EmbedProviderGemini:
		var resp struct {
			Embedding struct {
				Values []float64 `json:"values"`
			} `json:"embedding"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("gemini embed: unmarshal: %w", err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("gemini embed: api error: %s", resp.Error.Message)
		}
		if len(resp.Embedding.Values) == 0 {
			return nil, fmt.Errorf("gemini embed: empty embedding in response")
		}
		return resp.Embedding.Values, nil

	default:
		return nil, fmt.Errorf("embed_text: unsupported provider %q", provider)
	}
}

// EmbedText returns an engine.Instruction that calls an embedding API and writes
// the resulting float vector as a JSON array to the result slot.
//
// At runtime:
//  1. Reads text from ctx.ByteSlots[cfg.InputSlot]; if empty, returns PC+1 (no-op)
//  2. Resolves API key: slot override if APIKeySlot >= 0 and non-empty, else cfg.APIKey
//  3. Builds provider-specific request body
//  4. POSTs with retry on 429/5xx (up to cfg.MaxRetries)
//  5. Parses response to extract []float64 vector
//  6. Marshals vector as JSON float array → ctx.ByteSlots[cfg.ResultSlot]
//  7. If DimSlot >= 0 and in range: writes int64(len(vector)) to ctx.IntSlots[cfg.DimSlot]
//  8. Returns state.PC + 1
//
// On any error: sets ctx.ResponseStatus = 502, ctx.Failed = true, returns engine.StopPlan.
func EmbedText(cfg EmbedTextConfig) engine.Instruction {
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 10_000
	}
	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	endpoint := embedEndpoint(cfg.Provider, cfg.BaseURL, cfg.Model)
	if endpoint == "" {
		// Misconfiguration at bake time — return poisoned instruction.
		return engine.Instruction{
			Name: "embed_text[bad_provider]",
			Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "embed_text: unsupported provider: " + string(cfg.Provider)
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			},
		}
	}

	endpointHost := extractUpstreamHost(endpoint)

	return engine.Instruction{
		Name: "embed_text[" + string(cfg.Provider) + "/" + cfg.Model + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read input text
			if cfg.InputSlot < 0 || cfg.InputSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			text := string(ctx.ByteSlots[cfg.InputSlot])
			if text == "" {
				return state.PC + 1
			}

			// 2. Resolve API key
			apiKey := cfg.APIKey
			if cfg.APIKeySlot >= 0 && cfg.APIKeySlot < len(ctx.ByteSlots) {
				if k := ctx.ByteSlots[cfg.APIKeySlot]; len(k) > 0 {
					apiKey = string(k)
				}
			}

			// Rebuild endpoint with potentially updated model (static for now)
			activeEndpoint := endpoint

			// 3. Build request body
			body, buildErr := embedBuildRequest(cfg.Provider, cfg.Model, text)
			if buildErr != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "embed_text: build request: " + buildErr.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 4. HTTP call with retry
			client := getEmbedClient(activeEndpoint, timeoutMs)
			attempts := maxRetries + 1

			var lastStatus int
			for attempt := 1; attempt <= attempts; attempt++ {
				start := time.Now()

				reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
				httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, activeEndpoint, bytes.NewReader(body))
				if reqErr != nil {
					cancel()
					ctx.ResponseStatus = 500
					ctx.Failed = true
					ctx.ErrorCode = 500
					msg := "embed_text: request build failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				httpReq.Header.Set("Content-Type", "application/json")

				// Set auth header per provider
				switch cfg.Provider {
				case EmbedProviderOpenAI:
					if apiKey != "" {
						httpReq.Header.Set("Authorization", "Bearer "+apiKey)
					}
				case EmbedProviderGemini:
					if apiKey != "" {
						httpReq.Header.Set("x-goog-api-key", apiKey)
					}
				// Ollama: no auth
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
							Host: endpointHost, URL: activeEndpoint, Attempt: attempt,
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
					msg := "embed_text: upstream error"
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
						Host: endpointHost, URL: activeEndpoint, Attempt: attempt,
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
					msg := "embed_text: response read failed"
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
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "embed_text: provider error"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				if resp.StatusCode != http.StatusOK {
					ctx.ResponseStatus = resp.StatusCode
					ctx.Failed = true
					ctx.ErrorCode = int16(resp.StatusCode)
					msg := "embed_text: unexpected status"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// 5. Parse vector from response
				vector, parseErr := embedParseResponse(cfg.Provider, respBody)
				if parseErr != nil {
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "embed_text: response parse failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// 6. Marshal vector as JSON float array
				vectorJSON, marshalErr := json.Marshal(vector)
				if marshalErr != nil {
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "embed_text: marshal vector failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// Write to result slot
				if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
					dst := ctx.Alloc(len(vectorJSON))
					copy(dst, vectorJSON)
					ctx.ByteSlots[cfg.ResultSlot] = dst
				}

				// 7. Write dimension count to IntSlot
				if cfg.DimSlot >= 0 && cfg.DimSlot < len(ctx.IntSlots) {
					ctx.IntSlots[cfg.DimSlot] = int64(len(vector))
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
			msg := "embed_text: all retries exhausted"
			ctx.ErrorMsg = ctx.Alloc(len(msg))
			copy(ctx.ErrorMsg, msg)
			return engine.StopPlan
		},
	}
}
