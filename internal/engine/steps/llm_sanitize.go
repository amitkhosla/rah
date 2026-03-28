package steps

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// EstimateTokens reads ByteSlots[sourceSlot], estimates its token count via
// estimateTokens, and writes the result as int64 to IntSlots[destIntSlot].
func EstimateTokens(sourceSlot int, destIntSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "estimate_tokens",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			var val []byte
			if sourceSlot >= 0 && sourceSlot < len(ctx.ByteSlots) {
				val = ctx.ByteSlots[sourceSlot]
			}
			est := estimateTokens(string(val))
			if destIntSlot >= 0 && destIntSlot < len(ctx.IntSlots) {
				ctx.IntSlots[destIntSlot] = int64(est)
			}
			return state.PC + 1
		},
	}
}

// SanitizePromptConfig is resolved at bake time and captured in the closure.
type SanitizePromptConfig struct {
	PromptSlot  int      // ByteSlots index: input and output (modified in-place)
	Rules       []string // "pii", "injection", "max_tokens:N"
	OnViolation string   // "reject", "strip", "flag"
	FlagSlot    int      // BoolSlots index for "flag" mode (-1 if unused)
}

// piiPattern groups all compiled PII regexes.
type piiPattern struct {
	re   *regexp.Regexp
	name string
}

// SanitizePrompt returns an instruction that applies the configured rules to
// the prompt in ByteSlots[cfg.PromptSlot], modifying it in-place via ctx.Alloc.
//
// Rules are applied in order. All regex patterns are compiled once at bake time
// (when SanitizePrompt is called) and captured in the closure.
func SanitizePrompt(cfg SanitizePromptConfig) engine.Instruction {
	if cfg.OnViolation == "" {
		cfg.OnViolation = "reject"
	}

	// Compile PII regexes once at bake time.
	piiPatterns := []*piiPattern{
		{regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`), "email"},
		{regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), "ssn"},
		{regexp.MustCompile(`\b\d{4}[\s\-]?\d{4}[\s\-]?\d{4}[\s\-]?\d{4}\b`), "credit_card"},
		{regexp.MustCompile(`\b(\+?1[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b`), "phone"},
	}

	// Injection patterns — case-insensitive substring matching.
	injectionPatterns := []string{
		"ignore previous instructions",
		"ignore all instructions",
		"disregard previous",
		"you are now",
		"act as if",
		"jailbreak",
		"do anything now",
		"dan ",
		"pretend you are",
	}

	// Pre-lowercase the injection patterns so we can compare against a
	// lower-cased copy of the prompt without allocating on every check.
	injectionLower := make([]string, len(injectionPatterns))
	for i, p := range injectionPatterns {
		injectionLower[i] = strings.ToLower(p)
	}

	// Parse max_tokens value from any "max_tokens:N" rule.
	maxTokensLimit := 0
	for _, rule := range cfg.Rules {
		if strings.HasPrefix(rule, "max_tokens:") {
			n, err := strconv.Atoi(strings.TrimPrefix(rule, "max_tokens:"))
			if err == nil && n > 0 {
				maxTokensLimit = n
			}
		}
	}

	return engine.Instruction{
		Name: "sanitize_prompt",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.PromptSlot < 0 || cfg.PromptSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}

			prompt := string(ctx.ByteSlots[cfg.PromptSlot])
			if prompt == "" {
				return state.PC + 1
			}

			for _, rule := range cfg.Rules {
				switch {
				case rule == "pii":
					violated := false
					for _, pp := range piiPatterns {
						if pp.re.MatchString(prompt) {
							violated = true
							switch cfg.OnViolation {
							case "strip":
								prompt = pp.re.ReplaceAllString(prompt, "[REDACTED]")
							case "flag":
								if cfg.FlagSlot >= 0 && cfg.FlagSlot < len(ctx.BoolSlots) {
									ctx.BoolSlots[cfg.FlagSlot] = true
								}
								// continue processing
							default: // "reject"
								ctx.ResponseStatus = 400
								ctx.Failed = true
								ctx.ErrorCode = 400
								msg := "sanitize_prompt: PII detected in prompt"
								ctx.ErrorMsg = ctx.Alloc(len(msg))
								copy(ctx.ErrorMsg, msg)
								return engine.StopPlan
							}
						}
					}
					_ = violated

				case rule == "injection":
					promptLower := strings.ToLower(prompt)
					for _, pattern := range injectionLower {
						if strings.Contains(promptLower, pattern) {
							switch cfg.OnViolation {
							case "strip":
								prompt = stripInjectionSentence(prompt, pattern)
								promptLower = strings.ToLower(prompt)
							case "flag":
								if cfg.FlagSlot >= 0 && cfg.FlagSlot < len(ctx.BoolSlots) {
									ctx.BoolSlots[cfg.FlagSlot] = true
								}
								// continue — check remaining patterns
							default: // "reject"
								ctx.ResponseStatus = 400
								ctx.Failed = true
								ctx.ErrorCode = 400
								msg := "sanitize_prompt: injection pattern detected"
								ctx.ErrorMsg = ctx.Alloc(len(msg))
								copy(ctx.ErrorMsg, msg)
								return engine.StopPlan
							}
						}
					}

				case strings.HasPrefix(rule, "max_tokens:"):
					if maxTokensLimit > 0 {
						est := estimateTokens(prompt)
						if est > maxTokensLimit {
							switch cfg.OnViolation {
							case "flag":
								if cfg.FlagSlot >= 0 && cfg.FlagSlot < len(ctx.BoolSlots) {
									ctx.BoolSlots[cfg.FlagSlot] = true
								}
								// strip is no-op for max_tokens; fall through
							default: // "reject" (and "strip" is no-op per spec)
								ctx.ResponseStatus = 413
								ctx.Failed = true
								ctx.ErrorCode = 413
								msg := "sanitize_prompt: prompt exceeds token limit"
								ctx.ErrorMsg = ctx.Alloc(len(msg))
								copy(ctx.ErrorMsg, msg)
								return engine.StopPlan
							}
						}
					}
				}
			}

			// Write modified prompt back to the slot via ctx.Alloc.
			result := ctx.Alloc(len(prompt))
			copy(result, prompt)
			ctx.ByteSlots[cfg.PromptSlot] = result
			return state.PC + 1
		},
	}
}

// stripInjectionSentence removes any sentence that contains the given
// (already-lower-cased) injection pattern from the prompt.
// Sentences are delimited by '.', '!', or '?'.
func stripInjectionSentence(prompt, patternLower string) string {
	sentences := splitSentences(prompt)
	var kept []string
	for _, s := range sentences {
		if !strings.Contains(strings.ToLower(s), patternLower) {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, " ")
}

// splitSentences splits text on sentence-ending punctuation (.!?) while
// preserving the delimiter at the end of each sentence.
func splitSentences(text string) []string {
	var sentences []string
	var buf strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		buf.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			// Peek ahead: if next char is whitespace or end of string, treat as sentence end.
			if i+1 >= len(runes) || unicode.IsSpace(runes[i+1]) {
				s := strings.TrimSpace(buf.String())
				if s != "" {
					sentences = append(sentences, s)
				}
				buf.Reset()
			}
		}
	}
	// Remainder without terminal punctuation.
	if s := strings.TrimSpace(buf.String()); s != "" {
		sentences = append(sentences, s)
	}
	return sentences
}

// CompressPromptConfig is resolved at bake time and captured in the closure.
type CompressPromptConfig struct {
	PromptSlot   int                   // ByteSlots index: input and output (modified in-place)
	ModelConfig  config.LLMModelConfig
	APIKey       string
	TargetTokens int    // compress to fit under this; default 2000
	TimeoutMs    int    // default 30000
	OnExceed     string // "reject" (413) if still over after compression; default "reject"
}

// CompressPrompt returns an instruction that calls an LLM to summarise the
// prompt when it exceeds TargetTokens, writing the compressed text back in-place.
func CompressPrompt(cfg CompressPromptConfig) engine.Instruction {
	adapter, err := NewAdapter(cfg.ModelConfig.Adapter)
	if err != nil {
		return engine.Instruction{
			Name: "compress_prompt[bad_adapter]",
			Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "compress_prompt: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			},
		}
	}

	targetTokens := cfg.TargetTokens
	if targetTokens <= 0 {
		targetTokens = 2000
	}
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}
	onExceed := cfg.OnExceed
	if onExceed == "" {
		onExceed = "reject"
	}

	maxTokens := cfg.ModelConfig.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	endpoint := adapter.Endpoint(cfg.ModelConfig.BaseURL, cfg.ModelConfig.Slug)
	authName, authValue := adapter.AuthHeader(cfg.APIKey)

	return engine.Instruction{
		Name: "compress_prompt[" + cfg.ModelConfig.Slug + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.PromptSlot < 0 || cfg.PromptSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}

			prompt := string(ctx.ByteSlots[cfg.PromptSlot])
			if prompt == "" {
				return state.PC + 1
			}

			// 1. Check if already under limit — skip LLM call entirely.
			if estimateTokens(prompt) <= targetTokens {
				return state.PC + 1
			}

			// 2. Build the compression meta-prompt.
			charLimit := targetTokens * 4
			metaPrompt := fmt.Sprintf(
				"Summarize the following text concisely, preserving all key information and intent, in under %d characters:\n\n%s",
				charLimit, prompt,
			)

			// 3. Build canonical request.
			req := LLMRequest{
				Messages:    []CanonicalMessage{{Role: RoleUser, Content: metaPrompt}},
				Model:       cfg.ModelConfig.Slug,
				MaxTokens:   maxTokens,
				Temperature: 0.3, // low temperature for faithful summarisation
			}

			// 4. Marshal.
			body, marshalErr := adapter.Marshal(req)
			if marshalErr != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "compress_prompt: marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 5. HTTP call — single attempt, no retry for compression.
			client := getLLMClient(cfg.ModelConfig.BaseURL, timeoutMs)

			reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
			httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
			if reqErr != nil {
				cancel()
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "compress_prompt: request build failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}
			httpReq.Header.Set("Content-Type", "application/json")
			if authName != "" {
				httpReq.Header.Set(authName, authValue)
			}
			if cfg.ModelConfig.Adapter == config.AdapterAnthropic {
				httpReq.Header.Set("anthropic-version", "2023-06-01")
			}

			start := time.Now()
			resp, doErr := client.Do(httpReq)
			cancel()
			elapsed := time.Since(start)

			atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, elapsed.Nanoseconds())
			atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

			if doErr != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "compress_prompt: upstream error"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()

			atomic.AddInt64(&ctx.Timing.UpstreamBytesRx, int64(len(respBody)))
			atomic.AddInt64(&ctx.Timing.UpstreamBytesTx, int64(len(body)))

			if readErr != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "compress_prompt: response read failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			if resp.StatusCode != http.StatusOK {
				ctx.ResponseStatus = resp.StatusCode
				ctx.Failed = true
				ctx.ErrorCode = int16(resp.StatusCode)
				msg := "compress_prompt: provider error"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 6. Unmarshal.
			llmResp, unmarshalErr := adapter.Unmarshal(respBody)
			if unmarshalErr != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "compress_prompt: response parse failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			compressed := llmResp.Content

			// 7. Check if result still exceeds limit.
			if estimateTokens(compressed) > targetTokens && onExceed == "reject" {
				ctx.ResponseStatus = 413
				ctx.Failed = true
				ctx.ErrorCode = 413
				msg := "compress_prompt: compressed prompt still exceeds token limit"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 8. Write compressed text back to slot.
			result := ctx.Alloc(len(compressed))
			copy(result, compressed)
			ctx.ByteSlots[cfg.PromptSlot] = result
			return state.PC + 1
		},
	}
}
