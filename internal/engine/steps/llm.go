package steps

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/observability"
	"github.com/amitkhosla/rah/internal/rctx"
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
	CatalogKeys  map[string]string                // pre-resolved API keys for catalog entries (alias â†’ literal key)

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

	// StopReasonSlot: if >= 0, write response.StopReason to ctx.ByteSlots[StopReasonSlot]
	// after a successful call. Used by format_response to map stop reason to caller's format.
	// Set to -1 (default) to skip.
	StopReasonSlot int

	// FallbackChain is an ordered list of fallback models tried in sequence when
	// the primary model exhausts all retries. Each entry is resolved at bake time.
	// An empty chain means no fallback.
	FallbackChain []FallbackEntry

	// FallbackByModel maps a model alias to its pre-baked fallback entry chain.
	// When a model is selected at runtime via ModelSlot, its chain from this map
	// overrides FallbackChain for that specific routing decision. Resolved at bake time.
	FallbackByModel map[string][]FallbackEntry

	// ModelConfigSlot: if >= 0, read a JSON-encoded LLMModelConfig from
	// ctx.ByteSlots[ModelConfigSlot] at runtime and use it instead of the
	// baked ModelConfig. Allows per-request model config injection from payload.
	// Set to -1 (default) to use the baked ModelConfig.
	ModelConfigSlot int

	// MessagesSlot: if >= 0, read JSON-encoded []CanonicalMessage from
	// ctx.ByteSlots[MessagesSlot] instead of building a single-turn request
	// from PromptSlot. Use with parse_message_format to support multi-turn
	// conversation history and content blocks.
	// Set to -1 (default) to use PromptSlot.
	MessagesSlot int

	// â"€â"€ Tool / thinking slots (runtime, read from ctx.ByteSlots) â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	// All slot fields default to -1 (disabled). Callers (compiler) must set them
	// explicitly; zero would silently conflict with a real slot.

	// ToolsSlot: if >= 0, read JSON []ToolDefinition from ctx.ByteSlots[ToolsSlot].
	// Populated by parse_message_format from the incoming request body.
	// Set to -1 (default) to use no tools (or the baked ToolChoice static value).
	ToolsSlot int

	// ToolChoiceSlot: if >= 0, read a ToolChoice override from ctx.ByteSlots[ToolChoiceSlot].
	// Value may be a JSON string ("auto") or a JSON ToolChoice object.
	// Set to -1 (default) to use the static ToolChoice field below.
	ToolChoiceSlot int

	// ThinkingSlot: if >= 0, read a JSON ThinkingConfig from ctx.ByteSlots[ThinkingSlot].
	// Set to -1 (default) to use the static Thinking field below.
	ThinkingSlot int

	// â"€â"€ Static bake-time values (used when corresponding slot == -1) â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

	// ToolChoice is the static tool choice strategy applied when ToolChoiceSlot == -1.
	// Empty string means use the adapter default.
	ToolChoice ToolChoiceType

	// Thinking is the static thinking config applied when ThinkingSlot == -1.
	// nil means thinking is disabled.
	Thinking *ThinkingConfig

	// â"€â"€ Output slots (write after successful model call) â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

	// ToolUseSlot: if >= 0, write JSON []ContentBlock (BlockToolUse only) to
	// ctx.ByteSlots[ToolUseSlot] after a successful call.
	// Set to -1 (default) to skip.
	ToolUseSlot int

	// ThinkingOutSlot: if >= 0, write the text of the first BlockThinking block
	// to ctx.ByteSlots[ThinkingOutSlot] after a successful call.
	// Set to -1 (default) to skip.
	ThinkingOutSlot int

	// Anthropic provider-side prompt caching.
	PromptCacheEnabled bool // bake-time opt-in; adds cache_control to the request
	PromptCacheUpTo    int  // which message index to mark (-1 = last)
	CacheReadSlot      int  // IntSlot to write cache_read_input_tokens into (-1 = skip)
	CacheCreationSlot  int  // IntSlot to write cache_creation_input_tokens into (-1 = skip)

	// StreamToClient enables true SSE streaming from the LLM provider to the HTTP client.
	// When true and ctx.GetWriter() is non-nil, the response body is streamed token-by-token
	// instead of buffered. The result slot is NOT populated in streaming mode.
	// Must be false when ToolsSlot >= 0 (tool calling requires the full response buffered).
	StreamToClient bool
}

// FallbackEntry holds one step in the fallback chain, resolved at bake time.
type FallbackEntry struct {
	ModelConfig config.LLMModelConfig
	APIKey      string
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
	adapter, err := NewAdapter(modelCfg)
	if err != nil {
		return llmCallParams{}, err
	}
	// EndpointOverride bypasses all adapter URL logic.
	// {model} in the override is replaced with the resolved model ID.
	wireModelID := modelCfg.ModelID
	if wireModelID == "" {
		wireModelID = modelCfg.Alias
	}
	endpoint := modelCfg.EndpointOverride
	if endpoint != "" {
		endpoint = strings.ReplaceAll(endpoint, "{model}", wireModelID)
	} else {
		endpoint = adapter.Endpoint(modelCfg.BaseURL, wireModelID)
	}
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
	llmClientCache sync.Map // key: baseURL string â†’ *http.Client
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

	// Bake-time validation.
	if cfg.StreamToClient && cfg.ToolsSlot >= 0 {
		bakeErr := fmt.Errorf("llm_call: stream_to_client cannot be used with tools_slot (tool calling requires buffered response)")
		return engine.Instruction{
			Name: "llm_call[bad_config]",
			Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := bakeErr.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			},
		}
	}

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
		Name: "llm_call[" + cfg.ModelConfig.Alias + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read prompt.
			// promptContent is the single-turn text (used as fallback for single-turn
			// mode and as the trace "prompt" label when MessagesSlot is not set).
			// When MessagesSlot is set, reqMessages is built from the slot and
			// promptContent is used only for the trace snippet.
			var promptContent string
			if cfg.PromptSlot >= 0 && cfg.PromptSlot < len(ctx.ByteSlots) {
				promptContent = string(ctx.ByteSlots[cfg.PromptSlot])
			}
			// When MessagesSlot is configured, allow empty promptContent — the messages
			// array will supply the actual content. Only skip if both are empty.
			hasMessages := cfg.MessagesSlot >= 0 && cfg.MessagesSlot < len(ctx.ByteSlots) &&
				len(ctx.ByteSlots[cfg.MessagesSlot]) > 0
			if promptContent == "" && !hasMessages {
				// Nothing to send — skip silently, leave result slot empty
				return state.PC + 1
			}

			// Runtime API key resolution with fallback chain:
			// 1. X-API-Key header (highest priority)
			// 2. APIKeySlot (if configured)
			// 3. Baked-in APIKey (lowest priority)
			apiKey := cfg.APIKey

			// 1. Check X-API-Key header
			if ctx.Request != nil {
				if headerKey := ctx.Request.Header.Get("X-API-Key"); headerKey != "" {
					apiKey = headerKey
				}
			}

			// 2. Check APIKeySlot (if X-API-Key not provided and key not already set)
			if apiKey == cfg.APIKey && cfg.APIKeySlot >= 0 && cfg.APIKeySlot < len(ctx.ByteSlots) {
				if k := ctx.ByteSlots[cfg.APIKeySlot]; len(k) > 0 {
					apiKey = string(k)
				}
			}

			// activeFallback is the fallback chain for this request. Defaults to the baked
			// chain; overridden per-model by FallbackByModel when model_slot is used.
			activeFallback := cfg.FallbackChain

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
					// Resolve API key: prefer runtime slot key, then pre-resolved catalog key,
					// then baked key. CatalogKeys holds keys resolved at bake time so we never
					// pass a raw "env:FOO" ref as a literal key to the provider.
					dynKey := apiKey // already set to runtime slot key or baked key above
					if resolved, ok2 := cfg.CatalogKeys[slug]; ok2 && resolved != "" {
						dynKey = resolved
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
					// Per-model fallback chain: overrides the baked FallbackChain.
					if len(cfg.FallbackByModel) > 0 {
						if chain, ok := cfg.FallbackByModel[slug]; ok {
							activeFallback = chain
						}
					}
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

			// Runtime model config override from slot (payload injection).
			// If ModelConfigSlot >= 0 and the slot contains a JSON-encoded
			// LLMModelConfig, use it instead of the baked model config.
			if cfg.ModelConfigSlot >= 0 && cfg.ModelConfigSlot < len(ctx.ByteSlots) {
				if raw := ctx.ByteSlots[cfg.ModelConfigSlot]; len(raw) > 0 {
					var runtimeCfg config.LLMModelConfig
					if jsonErr := json.Unmarshal(raw, &runtimeCfg); jsonErr == nil && runtimeCfg.Alias != "" {
						runtimeKey := runtimeCfg.APIKeyRef
						if runtimeKey == "" {
							runtimeKey = apiKey // fall back to baked/slot key
						}
						rp, rpErr := resolveCallParams(runtimeCfg, runtimeKey)
						if rpErr == nil {
							activeCfg = runtimeCfg
							activeParams = rp
							activeEndpointHost = extractUpstreamHost(rp.endpoint)
						}
					}
				}
			}

			// 2. Read system prompt
			var systemContent string
			if cfg.SystemSlot >= 0 && cfg.SystemSlot < len(ctx.ByteSlots) {
				systemContent = string(ctx.ByteSlots[cfg.SystemSlot])
			}

			// 3. Build canonical request
			wireModel := activeCfg.ModelID
			if wireModel == "" {
				wireModel = activeCfg.Alias
			}
			// Build message list: use MessagesSlot (multi-turn) if configured,
			// otherwise fall back to single-turn from PromptSlot.
			var reqMessages []CanonicalMessage
			if cfg.MessagesSlot >= 0 && cfg.MessagesSlot < len(ctx.ByteSlots) {
				if raw := ctx.ByteSlots[cfg.MessagesSlot]; len(raw) > 0 {
					if jsonErr := json.Unmarshal(raw, &reqMessages); jsonErr != nil {
						reqMessages = nil // fall through to single-turn
					}
				}
			}
			if len(reqMessages) == 0 {
				reqMessages = []CanonicalMessage{{Role: RoleUser, Content: promptContent}}
			}
			req := LLMRequest{
				Messages:            reqMessages,
				System:              systemContent,
				Model:               wireModel,
				MaxTokens:           maxTokens,
				Temperature:         cfg.Temperature,
				ProviderParams:      activeCfg.ProviderParams,
				UseCompletionTokens: activeCfg.UseCompletionTokens,
				PromptCacheEnabled:  cfg.PromptCacheEnabled,
				PromptCacheUpTo:     cfg.PromptCacheUpTo,
				StreamMode:          cfg.StreamToClient,
			}

			// 3b. Populate tools, tool_choice, and thinking from slots / baked config.

			// Read tools from slot (populated by parse_message_format from incoming request body).
			if cfg.ToolsSlot >= 0 && cfg.ToolsSlot < len(ctx.ByteSlots) {
				if raw := ctx.ByteSlots[cfg.ToolsSlot]; len(raw) > 0 {
					var tools []ToolDefinition
					if jsonErr := json.Unmarshal(raw, &tools); jsonErr == nil && len(tools) > 0 {
						req.Tools = tools
					}
				}
			}

			// Resolve tool choice: slot â†’ baked default.
			if cfg.ToolChoiceSlot >= 0 && cfg.ToolChoiceSlot < len(ctx.ByteSlots) {
				if raw := ctx.ByteSlots[cfg.ToolChoiceSlot]; len(raw) > 0 {
					// Try to parse as ToolChoice struct first, then as plain string.
					var tc ToolChoice
					if jsonErr := json.Unmarshal(raw, &tc); jsonErr == nil && tc.Type != "" {
						req.ToolChoice = &tc
					} else {
						var s string
						if jsonErr2 := json.Unmarshal(raw, &s); jsonErr2 == nil && s != "" {
							req.ToolChoice = &ToolChoice{Type: ToolChoiceType(s)}
						}
					}
				}
			}
			if req.ToolChoice == nil && cfg.ToolChoice != "" {
				req.ToolChoice = &ToolChoice{Type: cfg.ToolChoice}
			}

			// Resolve thinking: slot â†’ baked default.
			if cfg.ThinkingSlot >= 0 && cfg.ThinkingSlot < len(ctx.ByteSlots) {
				if raw := ctx.ByteSlots[cfg.ThinkingSlot]; len(raw) > 0 {
					var tc ThinkingConfig
					if jsonErr := json.Unmarshal(raw, &tc); jsonErr == nil {
						req.Thinking = &tc
					}
				}
			}
			if req.Thinking == nil && cfg.Thinking != nil {
				req.Thinking = cfg.Thinking
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

			// 5. Marshal to provider wire format, then merge any provider-specific params
			body, marshalErr := activeParams.adapter.Marshal(req)
			if marshalErr == nil && len(req.ProviderParams) > 0 {
				body, marshalErr = mergeProviderParams(body, req.ProviderParams)
			}
			if marshalErr != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "llm_call: marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 6. Proactive provider rate limit check.
			// Checks local atomic counters — ~20ns, no network call.
			// If any configured window is exhausted we skip the HTTP loop and fall
			// through to the fallback chain exactly as a provider-returned 429 would.
			var lastStatus int
			var lastErrBody []byte // last provider error response body, for final exhaustion response
			var llmSeq uint8      // incremented each time AppendLLMCall is called
			if detail, allowed := engine.CheckUpstreamLimitWithDetail(activeCfg.Alias); !allowed {
				state.AddTraceAttr("rate_limited", activeCfg.Alias)
				state.AddTraceAttr("rate_limit_detail", detail) // e.g. "minute:60/60"
				lastStatus = 429
			} else {
				_ = detail // allowed path — no detail needed

				// 7. HTTP call with retry
				client := getLLMClient(activeCfg.BaseURL, timeoutMs)
				attempts := maxRetries + 1

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
					// Anthropic and Bedrock (Anthropic models) require the API version header.
					if activeCfg.Adapter == config.AdapterAnthropic || activeCfg.Adapter == config.AdapterBedrock {
						httpReq.Header.Set("anthropic-version", "2023-06-01")
					}
					// Extra headers defined on the model config — applied last so they can override defaults.
					for k, v := range activeCfg.ExtraHeaders {
						httpReq.Header.Set(k, v)
					}
					// SigV4 signing for adapters that require request-level signing (e.g. AWS Bedrock).
					if signer, ok := activeParams.adapter.(RequestSigner); ok {
						if signErr := signer.SignRequest(httpReq, body, apiKey); signErr != nil {
							cancel()
							ctx.ResponseStatus = 500
							ctx.Failed = true
							ctx.ErrorCode = 500
							msg := "llm_call: request signing failed: " + signErr.Error()
							ctx.ErrorMsg = ctx.Alloc(len(msg))
							copy(ctx.ErrorMsg, msg)
							return engine.StopPlan
						}
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
						// All retries exhausted — break to try fallback chain below.
						lastStatus = 502
						break
					}

					// Streaming path: pipe SSE directly to the client.
					if cfg.StreamToClient && ctx.GetWriter() != nil && resp.StatusCode == http.StatusOK {
						streamProvider := "openai" // default (OpenAI-compatible)
						if activeCfg.Adapter == config.AdapterAnthropic || activeCfg.Adapter == config.AdapterBedrock {
							streamProvider = "anthropic"
						}
						// Tee the SSE body to accumulate raw bytes for verbatim capture.
						var sseAccumBuf bytes.Buffer
						teedRC := io.NopCloser(io.TeeReader(resp.Body, &sseAccumBuf))
						inTok, outTok, streamErr := streamLLMToClient(ctx, teedRC, streamProvider)
						// Capture raw bytes (even on partial error).
						observability.AppendLLMCall(&ctx.LLMCalls, observability.LLMCallEntry{
							PC:           state.PC,
							Seq:          llmSeq,
							ModelName:    activeCfg.Alias,
							Status:       uint16(resp.StatusCode),
							InputTokens:  uint32(inTok),
							OutputTokens: uint32(outTok),
							DurationNs:   elapsed.Nanoseconds(),
							ReqBytes:     body,
							ResBytes:     sseAccumBuf.Bytes(),
						})
						// Write token counts to slots even in streaming mode.
						if cfg.InputTokensSlot >= 0 && cfg.InputTokensSlot < len(ctx.IntSlots) {
							ctx.IntSlots[cfg.InputTokensSlot] = int64(inTok)
						}
						if cfg.OutputTokensSlot >= 0 && cfg.OutputTokensSlot < len(ctx.IntSlots) {
							ctx.IntSlots[cfg.OutputTokensSlot] = int64(outTok)
						}
						if streamErr != nil {
							// Streaming already started — cannot retry/fallback. Signal done.
							ctx.Failed = true
							return engine.StopPlan
						}
						// Streaming complete — skip the buffered response path entirely.
						return state.PC + 1
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
							Model: activeCfg.Alias,
						})
					}

					if readErr != nil {
						if attempt < attempts {
							time.Sleep(llmBackoff(attempt))
							continue
						}
						// All retries exhausted — break to try fallback chain below.
						lastStatus = 502
						break
					}

					// Retry on rate-limit or transient server errors
					if resp.StatusCode == 429 || resp.StatusCode >= 500 {
						if attempt < attempts {
							time.Sleep(llmBackoff(attempt))
							continue
						}
						// All retries exhausted — break to try fallback chain below.
						lastStatus = resp.StatusCode
						break
					}

					if resp.StatusCode != http.StatusOK {
						// Capture provider error in trace regardless of what happens next.
						errSnip := string(respBody)
						if len(errSnip) > 500 {
							errSnip = errSnip[:500] + "â€¦"
						}
						state.AddTraceAttr("provider_error", errSnip)
						state.AddTraceAttr("provider_status", fmt.Sprintf("%d", resp.StatusCode))
						// Break to fallback chain — a 4xx may be model-specific (bad key,
						// unsupported param, quota) and a different model may succeed.
						lastStatus = resp.StatusCode
						lastErrBody = respBody
						break
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
					// Write cache token counts to IntSlots (if configured).
					if cfg.CacheReadSlot >= 0 && cfg.CacheReadSlot < len(ctx.IntSlots) {
						ctx.IntSlots[cfg.CacheReadSlot] = int64(llmResp.CacheReadTokens)
					}
					if cfg.CacheCreationSlot >= 0 && cfg.CacheCreationSlot < len(ctx.IntSlots) {
						ctx.IntSlots[cfg.CacheCreationSlot] = int64(llmResp.CacheCreationTokens)
					}
					// 10. Write stop reason to ByteSlot (used by format_response).
					if cfg.StopReasonSlot >= 0 && cfg.StopReasonSlot < len(ctx.ByteSlots) {
						sr := ctx.Alloc(len(llmResp.StopReason))
						copy(sr, llmResp.StopReason)
						ctx.ByteSlots[cfg.StopReasonSlot] = sr
					}

					// 11. Write tool use blocks to ToolUseSlot.
					if cfg.ToolUseSlot >= 0 && cfg.ToolUseSlot < len(ctx.ByteSlots) && len(llmResp.ContentBlocks) > 0 {
						var toolUseBlocks []ContentBlock
						for _, b := range llmResp.ContentBlocks {
							if b.Type == BlockToolUse {
								toolUseBlocks = append(toolUseBlocks, b)
							}
						}
						if len(toolUseBlocks) > 0 {
							if encoded, encErr := json.Marshal(toolUseBlocks); encErr == nil {
								slot := ctx.Alloc(len(encoded))
								copy(slot, encoded)
								ctx.ByteSlots[cfg.ToolUseSlot] = slot
							}
						}
					}

					// 12. Write thinking text to ThinkingOutSlot (first BlockThinking block).
					if cfg.ThinkingOutSlot >= 0 && cfg.ThinkingOutSlot < len(ctx.ByteSlots) {
						for _, b := range llmResp.ContentBlocks {
							if b.Type == BlockThinking && b.Text != "" {
								slot := ctx.Alloc(len(b.Text))
								copy(slot, b.Text)
								ctx.ByteSlots[cfg.ThinkingOutSlot] = slot
								break // only first thinking block
							}
						}
					}

					// Emit LLM trace attributes
					{
						alias := activeCfg.Alias
						state.AddTraceAttr("model", alias)
						if activeCfg.ModelID != alias {
							state.AddTraceAttr("model_id", activeCfg.ModelID)
						}
						state.AddTraceAttr("input_tokens", strconv.Itoa(llmResp.InputTokens))
						state.AddTraceAttr("output_tokens", strconv.Itoa(llmResp.OutputTokens))
						var computedCostMicro uint32
						if activeCfg.CostPerInputToken > 0 || activeCfg.CostPerOutputToken > 0 {
							cost := float64(llmResp.InputTokens)/1e6*activeCfg.CostPerInputToken +
								float64(llmResp.OutputTokens)/1e6*activeCfg.CostPerOutputToken
							state.AddTraceAttr("cost_usd", fmt.Sprintf("%.6f", cost))
							computedCostMicro = uint32(cost * 1e6)
						}

						// Optional full-detail JSONL file logging for incident/debug use.
						observability.WriteDetailLog(map[string]any{
							"type":          "llm_call",
							"api_id":        ctx.ApiId,
							"tenant_id":     ctx.TenantID,
							"model":         alias,
							"model_id":      activeCfg.ModelID,
							"input_tokens":  llmResp.InputTokens,
							"output_tokens": llmResp.OutputTokens,
						})

						// Capture verbatim raw bytes for the non-streaming response.
						observability.AppendLLMCall(&ctx.LLMCalls, observability.LLMCallEntry{
							PC:           state.PC,
							Seq:          llmSeq,
							ModelName:    activeCfg.Alias,
							Status:       uint16(lastStatus),
							InputTokens:  uint32(llmResp.InputTokens),
							OutputTokens: uint32(llmResp.OutputTokens),
							CostMicro:    computedCostMicro,
							DurationNs:   elapsed.Nanoseconds(),
							ReqBytes:     body,
							ResBytes:     respBody,
						})
					}

					return state.PC + 1
				}

			} // end else (rate limit not exceeded)

			// --- fallback chain: walk each entry until one succeeds ---
			for chainIdx, fb := range activeFallback {
				if ctx.Obs != nil {
					ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, observability.UpstreamEvent{
						Host:    "llm_fallback",
						URL:     "fallback:" + fb.ModelConfig.Alias,
						Attempt: chainIdx + 1,
						Err:     "primary exhausted; trying fallback chain entry",
					})
				}

				fbParams, fbParamErr := resolveCallParams(fb.ModelConfig, fb.APIKey)
				if fbParamErr != nil {
					continue // bad adapter config — skip to next
				}

				fbEndpointHost := extractUpstreamHost(fbParams.endpoint)
				fbWireModel := fb.ModelConfig.ModelID
				if fbWireModel == "" {
					fbWireModel = fb.ModelConfig.Alias
				}
				fbReq := LLMRequest{
					Messages:            reqMessages, // reuse same messages (single or multi-turn)
					System:              systemContent,
					Model:               fbWireModel,
					MaxTokens:           maxTokens,
					Temperature:         cfg.Temperature,
					ProviderParams:      activeCfg.ProviderParams,
					UseCompletionTokens: activeCfg.UseCompletionTokens,
					PromptCacheEnabled:  cfg.PromptCacheEnabled,
					PromptCacheUpTo:     cfg.PromptCacheUpTo,
					// Preserve tools, tool choice, and thinking from primary request
					Tools:      req.Tools,
					ToolChoice: req.ToolChoice,
					Thinking:   req.Thinking,
				}

				// Skip if prompt exceeds this fallback model's context limit.
				if fb.ModelConfig.Capabilities.MaxContextTokens > 0 {
					if estimateRequestTokens(fbReq)+maxTokens > fb.ModelConfig.Capabilities.MaxContextTokens {
						continue
					}
				}

				fbBody, fbMarshalErr := fbParams.adapter.Marshal(fbReq)
				if fbMarshalErr != nil {
					continue
				}

				fbClient := getLLMClient(fb.ModelConfig.BaseURL, timeoutMs)
				fbStart := time.Now()
				fbReqCtx, fbCancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
				fbHttpReq, fbReqErr := http.NewRequestWithContext(fbReqCtx, http.MethodPost, fbParams.endpoint, bytes.NewReader(fbBody))
				if fbReqErr != nil {
					fbCancel()
					continue
				}
				fbHttpReq.Header.Set("Content-Type", "application/json")
				if fbParams.authName != "" {
					fbHttpReq.Header.Set(fbParams.authName, fbParams.authValue)
				}
				if fb.ModelConfig.Adapter == config.AdapterAnthropic {
					fbHttpReq.Header.Set("anthropic-version", "2023-06-01")
				}
				for k, v := range fb.ModelConfig.ExtraHeaders {
					fbHttpReq.Header.Set(k, v)
				}

				fbResp, fbDoErr := fbClient.Do(fbHttpReq)
				fbCancel()
				fbElapsed := time.Since(fbStart)

				atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, fbElapsed.Nanoseconds())
				atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

				if fbDoErr != nil {
					if ctx.Obs != nil {
						ctx.Obs.RecordUpstream(fbEndpointHost, fbElapsed, int64(len(fbBody)), 0)
						ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, observability.UpstreamEvent{
							Host: fbEndpointHost, URL: fbParams.endpoint, Attempt: chainIdx + 1,
							Err: fbDoErr.Error(), TotalNs: fbElapsed.Nanoseconds(),
						})
					}
					continue // network error — try next
				}

				fbRespBody, fbReadErr := io.ReadAll(fbResp.Body)
				_ = fbResp.Body.Close()
				atomic.AddInt64(&ctx.Timing.UpstreamBytesRx, int64(len(fbRespBody)))
				atomic.AddInt64(&ctx.Timing.UpstreamBytesTx, int64(len(fbBody)))

				if ctx.Obs != nil {
					ctx.Obs.RecordUpstream(fbEndpointHost, fbElapsed, int64(len(fbBody)), int64(len(fbRespBody)))
					ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, observability.UpstreamEvent{
						Host: fbEndpointHost, URL: fbParams.endpoint, Attempt: chainIdx + 1,
						Status: fbResp.StatusCode, TotalNs: fbElapsed.Nanoseconds(),
						BytesSent: int64(len(fbBody)), BytesReceived: int64(len(fbRespBody)),
					})
				}

				// Transient errors â†’ try next entry.
				if fbReadErr != nil || fbResp.StatusCode == 429 || fbResp.StatusCode >= 500 {
					continue
				}
				if fbResp.StatusCode != http.StatusOK {
					continue
				}

				fbLlmResp, fbUnmarshalErr := fbParams.adapter.Unmarshal(fbRespBody)
				if fbUnmarshalErr != nil {
					continue
				}

				// Success — write result and return.
				if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
					result := []byte(fbLlmResp.Content)
					ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(result))
					copy(ctx.ByteSlots[cfg.ResultSlot], result)
				}
				if cfg.InputTokensSlot >= 0 && cfg.InputTokensSlot < len(ctx.IntSlots) {
					ctx.IntSlots[cfg.InputTokensSlot] = int64(fbLlmResp.InputTokens)
				}
				if cfg.OutputTokensSlot >= 0 && cfg.OutputTokensSlot < len(ctx.IntSlots) {
					ctx.IntSlots[cfg.OutputTokensSlot] = int64(fbLlmResp.OutputTokens)
				}
				if cfg.CacheReadSlot >= 0 && cfg.CacheReadSlot < len(ctx.IntSlots) {
					ctx.IntSlots[cfg.CacheReadSlot] = int64(fbLlmResp.CacheReadTokens)
				}
				if cfg.CacheCreationSlot >= 0 && cfg.CacheCreationSlot < len(ctx.IntSlots) {
					ctx.IntSlots[cfg.CacheCreationSlot] = int64(fbLlmResp.CacheCreationTokens)
				}
				if cfg.StopReasonSlot >= 0 && cfg.StopReasonSlot < len(ctx.ByteSlots) {
					sr := ctx.Alloc(len(fbLlmResp.StopReason))
					copy(sr, fbLlmResp.StopReason)
					ctx.ByteSlots[cfg.StopReasonSlot] = sr
				}

				// Write tool use blocks to ToolUseSlot (fallback path).
				if cfg.ToolUseSlot >= 0 && cfg.ToolUseSlot < len(ctx.ByteSlots) && len(fbLlmResp.ContentBlocks) > 0 {
					var toolUseBlocks []ContentBlock
					for _, b := range fbLlmResp.ContentBlocks {
						if b.Type == BlockToolUse {
							toolUseBlocks = append(toolUseBlocks, b)
						}
					}
					if len(toolUseBlocks) > 0 {
						if encoded, encErr := json.Marshal(toolUseBlocks); encErr == nil {
							slot := ctx.Alloc(len(encoded))
							copy(slot, encoded)
							ctx.ByteSlots[cfg.ToolUseSlot] = slot
						}
					}
				}

				// Write thinking text to ThinkingOutSlot (fallback path, first BlockThinking block).
				if cfg.ThinkingOutSlot >= 0 && cfg.ThinkingOutSlot < len(ctx.ByteSlots) {
					for _, b := range fbLlmResp.ContentBlocks {
						if b.Type == BlockThinking && b.Text != "" {
							slot := ctx.Alloc(len(b.Text))
							copy(slot, b.Text)
							ctx.ByteSlots[cfg.ThinkingOutSlot] = slot
							break // only first thinking block
						}
					}
				}

				return state.PC + 1
			}

			// All retries and fallback chain exhausted — emit diagnosis.
			if len(activeFallback) == 0 {
				state.AddTraceAttr("fallback_status", "none_configured")
			} else {
				state.AddTraceAttr("fallback_status", fmt.Sprintf("all_%d_exhausted", len(activeFallback)))
			}
			state.AddTraceAttr("primary_status", fmt.Sprintf("%d", lastStatus))
			ctx.ResponseStatus = lastStatus
			if ctx.ResponseStatus == 0 {
				ctx.ResponseStatus = 502
			}
			ctx.Failed = true
			ctx.ErrorCode = int16(ctx.ResponseStatus)
			msg := "llm_call: all retries exhausted"
			ctx.ErrorMsg = ctx.Alloc(len(msg))
			copy(ctx.ErrorMsg, msg)
			// Return the last provider error body to the caller so they see what failed.
			if len(lastErrBody) > 0 {
				ctx.ResponseBuffer = append(ctx.ResponseBuffer[:0], lastErrBody...)
				ctx.IsBuffered = true
			}
			return engine.StopPlan
		},
	}
}

// llmBackoff returns the delay before retry attempt n (1-based).
// Uses simple exponential backoff: 500ms, 1s, 2s, â€¦
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

// mergeProviderParams merges extra provider-specific fields into an already-marshalled
// JSON body. The base JSON is decoded into a map, params are overlaid (overriding any
// existing key), and the result is re-marshalled. This is the single merge point for
// all adapters — no per-adapter changes needed when new params are added.
func mergeProviderParams(base []byte, params map[string]any) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(base, &m); err != nil {
		return base, err
	}
	for k, v := range params {
		m[k] = v
	}
	return json.Marshal(m)
}

// streamLLMToClient streams an LLM SSE response body directly to the HTTP client.
// It reads the upstream body line-by-line, parses provider SSE chunks, writes
// data frames to ctx.Writer, and accumulates token counts into totalIn/totalOut.
// provider must be "anthropic" or "openai" (anything else is treated as openai-compatible).
// Returns (inputTokens, outputTokens, error).
func streamLLMToClient(ctx *rctx.Context, body io.ReadCloser, provider string) (int, int, error) {
	defer func() { _ = body.Close() }()

	w := ctx.GetWriter()
	if w == nil {
		return 0, 0, fmt.Errorf("streamLLMToClient: no writer available")
	}

	type flusher interface{ Flush() }
	fl, canFlush := w.(flusher)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	var totalIn, totalOut int

	for scanner.Scan() {
		line := scanner.Bytes()
		// SSE lines starting with "data: "
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))

		var (
			delta    string
			inTok    int
			outTok   int
			done     bool
			parseErr error
		)

		switch provider {
		case "anthropic":
			delta, inTok, outTok, done, parseErr = ParseAnthropicStreamChunk(payload)
		default: // openai-compatible
			delta, inTok, outTok, done, parseErr = ParseOpenAIStreamChunk(payload)
		}

		if parseErr != nil {
			continue // skip malformed chunks
		}

		if inTok > 0 {
			totalIn += inTok
		}
		if outTok > 0 {
			totalOut += outTok
		}

		if len(delta) > 0 {
			frame := "data: " + delta + "\n\n"
			_, _ = io.WriteString(w, frame)
			if canFlush {
				fl.Flush()
			}
		}

		if done {
			break
		}
	}

	return totalIn, totalOut, scanner.Err()
}
