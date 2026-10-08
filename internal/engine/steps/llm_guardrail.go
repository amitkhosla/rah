package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

type GuardrailAction string

const (
	GuardrailBlock  GuardrailAction = "block"
	GuardrailRedact GuardrailAction = "redact"
	GuardrailFlag   GuardrailAction = "flag"
)

type GuardrailRegexRule struct {
	Pattern     string          `json:"pattern"               yaml:"pattern"`
	Replacement string          `json:"replacement,omitempty" yaml:"replacement,omitempty"`
	Action      GuardrailAction `json:"action"                yaml:"action"`
	Label       string          `json:"label,omitempty"       yaml:"label,omitempty"`
}

type GuardrailProviderConfig struct {
	Provider        string          `json:"provider"                    yaml:"provider"`
	Endpoint        string          `json:"endpoint,omitempty"          yaml:"endpoint,omitempty"`
	APIKey          string          `json:"api_key,omitempty"           yaml:"api_key,omitempty"`
	TimeoutMs       int             `json:"timeout_ms,omitempty"        yaml:"timeout_ms,omitempty"`
	Action          GuardrailAction `json:"action"                      yaml:"action"`
	ResourceID      string          `json:"resource_id,omitempty"       yaml:"resource_id,omitempty"`
	ResourceVersion string          `json:"resource_version,omitempty"  yaml:"resource_version,omitempty"`
}

// GuardrailConfig holds all guardrail rules for one llm_call step.
// Zero value = no guardrails (disabled). Embedded as a value type in LLMCallConfig.
type GuardrailConfig struct {
	RegexRules []GuardrailRegexRule      `json:"regex_rules,omitempty" yaml:"regex_rules,omitempty"`
	Providers  []GuardrailProviderConfig `json:"providers,omitempty"   yaml:"providers,omitempty"`
	ResultSlot int                       `json:"-" yaml:"-"`
	FlagSlot   int                       `json:"-" yaml:"-"` // -1 = disabled
}

type compiledRegexRule struct {
	re          *regexp.Regexp
	replacement []byte
	action      GuardrailAction
	label       string
}

// GuardrailCheck returns an engine.Instruction that applies guardrail rules to
// ctx.ByteSlots[cfg.ResultSlot] after a successful LLM call. All regex patterns
// are compiled once at call time (bake time). Returns a no-op when cfg is empty.
func GuardrailCheck(cfg GuardrailConfig) engine.Instruction {
	if len(cfg.RegexRules) == 0 && len(cfg.Providers) == 0 {
		return engine.Instruction{
			Name: "guardrail_check[noop]",
			Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
				return state.PC + 1
			},
		}
	}

	compiled := make([]compiledRegexRule, 0, len(cfg.RegexRules))
	for _, r := range cfg.RegexRules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			msg := fmt.Sprintf("guardrail_check: invalid regex %q: %v", r.Pattern, err)
			return engine.Instruction{
				Name: "guardrail_check[bad_regex]",
				Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
					ctx.ResponseStatus = 500
					ctx.Failed = true
					ctx.ErrorCode = 500
					errMsg := ctx.Alloc(len(msg))
					copy(errMsg, msg)
					ctx.ErrorMsg = errMsg
					return engine.StopPlan
				},
			}
		}
		cr := compiledRegexRule{re: re, action: r.Action, label: r.Label}
		if r.Action == GuardrailRedact {
			cr.replacement = []byte(r.Replacement)
		}
		compiled = append(compiled, cr)
	}

	providers := make([]GuardrailProviderConfig, len(cfg.Providers))
	copy(providers, cfg.Providers)
	resultSlot := cfg.ResultSlot
	flagSlot := cfg.FlagSlot

	return engine.Instruction{
		Name: "guardrail_check",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if resultSlot < 0 || resultSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			content := ctx.ByteSlots[resultSlot]
			if len(content) == 0 {
				return state.PC + 1
			}

			for _, cr := range compiled {
				if !cr.re.Match(content) {
					continue
				}
				switch cr.action {
				case GuardrailBlock:
					label := "guardrail_check: content blocked"
					if cr.label != "" {
						label = "guardrail_check: content blocked by rule [" + cr.label + "]"
					}
					ctx.ResponseStatus = 400
					ctx.Failed = true
					ctx.ErrorCode = 400
					errMsg := ctx.Alloc(len(label))
					copy(errMsg, label)
					ctx.ErrorMsg = errMsg
					return engine.StopPlan
				case GuardrailRedact:
					redacted := cr.re.ReplaceAll(content, cr.replacement)
					slot := ctx.Alloc(len(redacted))
					copy(slot, redacted)
					ctx.ByteSlots[resultSlot] = slot
					content = ctx.ByteSlots[resultSlot]
				case GuardrailFlag:
					if flagSlot >= 0 && flagSlot < len(ctx.BoolSlots) {
						ctx.BoolSlots[flagSlot] = true
					}
				}
			}

			for i := range providers {
				p := &providers[i]
				timeoutMs := p.TimeoutMs
				if timeoutMs <= 0 {
					timeoutMs = 5_000
				}
				var violated bool
				var callErr string
				switch p.Provider {
				case "openai_moderation":
					violated, callErr = callOpenAIModeration(ctx, string(content), p.Endpoint, p.APIKey, timeoutMs)
				case "bedrock":
					violated, callErr = callBedrockGuardrail(ctx, string(content), p.Endpoint, p.APIKey, p.ResourceID, p.ResourceVersion, timeoutMs)
				case "model_armor":
					violated, callErr = callModelArmor(ctx, string(content), p.Endpoint, p.APIKey, p.ResourceID, timeoutMs)
				case "webhook":
					violated, callErr = callWebhookGuardrail(ctx, string(content), p.Endpoint, p.APIKey, timeoutMs)
				default:
					continue
				}
				if callErr != "" {
					continue // fail open
				}
				if !violated {
					continue
				}
				switch p.Action {
				case GuardrailBlock:
					msg := "guardrail_check: content blocked by provider [" + p.Provider + "]"
					ctx.ResponseStatus = 400
					ctx.Failed = true
					ctx.ErrorCode = 400
					errMsg := ctx.Alloc(len(msg))
					copy(errMsg, msg)
					ctx.ErrorMsg = errMsg
					return engine.StopPlan
				case GuardrailRedact:
					redactMsg := "[CONTENT REMOVED BY GUARDRAIL]"
					slot := ctx.Alloc(len(redactMsg))
					copy(slot, redactMsg)
					ctx.ByteSlots[resultSlot] = slot
					content = ctx.ByteSlots[resultSlot]
				case GuardrailFlag:
					if flagSlot >= 0 && flagSlot < len(ctx.BoolSlots) {
						ctx.BoolSlots[flagSlot] = true
					}
				}
			}
			return state.PC + 1
		},
	}
}

func callOpenAIModeration(ctx *rctx.Context, content, endpoint, apiKey string, timeoutMs int) (bool, string) {
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/moderations"
	}
	payload, _ := json.Marshal(map[string]string{"input": content})
	client := getLLMClient(endpoint, timeoutMs)
	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return false, "request build failed"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	start := time.Now()
	resp, doErr := client.Do(req)
	cancel()
	atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, time.Since(start).Nanoseconds())
	atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)
	if doErr != nil {
		return false, "upstream error"
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "status error"
	}
	var result struct {
		Results []struct {
			Flagged bool `json:"flagged"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, "parse error"
	}
	for _, r := range result.Results {
		if r.Flagged {
			return true, ""
		}
	}
	return false, ""
}

func callBedrockGuardrail(ctx *rctx.Context, content, region, apiKey, resourceID, resourceVersion string, timeoutMs int) (bool, string) {
	if resourceVersion == "" {
		resourceVersion = "DRAFT"
	}
	if !strings.HasPrefix(region, "http") {
		region = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", region)
	}
	endpoint := fmt.Sprintf("%s/guardrail/%s/version/%s/apply", region, resourceID, resourceVersion)
	type bedrockContent struct {
		Text struct {
			Text string `json:"text"`
		} `json:"text"`
	}
	payload, _ := json.Marshal(map[string]any{
		"source":  "OUTPUT",
		"content": []bedrockContent{{Text: struct{ Text string `json:"text"` }{Text: content}}},
	})
	client := getLLMClient(region, timeoutMs)
	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return false, "request build failed"
	}
	req.Header.Set("Content-Type", "application/json")
	if err := signBedrockGuardrailRequest(req, apiKey, region, payload); err != nil {
		cancel()
		return false, "signing failed"
	}
	start := time.Now()
	resp, doErr := client.Do(req)
	cancel()
	atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, time.Since(start).Nanoseconds())
	atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)
	if doErr != nil {
		return false, "upstream error"
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "status error"
	}
	var result struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, "parse error"
	}
	return result.Action == "GUARDRAIL_INTERVENED", ""
}

func callModelArmor(ctx *rctx.Context, content, endpoint, apiKey, resourceID string, timeoutMs int) (bool, string) {
	url := endpoint
	if resourceID != "" && !strings.Contains(endpoint, resourceID) {
		url = strings.TrimSuffix(endpoint, "/") + "/" + resourceID + ":sanitizeModelResponse"
	}
	payload, _ := json.Marshal(map[string]any{
		"model_response_data": map[string]string{"text": content},
	})
	client := getLLMClient(url, timeoutMs)
	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return false, "request build failed"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	start := time.Now()
	resp, doErr := client.Do(req)
	cancel()
	atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, time.Since(start).Nanoseconds())
	atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)
	if doErr != nil {
		return false, "upstream error"
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "status error"
	}
	var result struct {
		SanitizationResult struct {
			FilterMatchState string `json:"filterMatchState"`
		} `json:"sanitizationResult"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, "parse error"
	}
	return result.SanitizationResult.FilterMatchState == "MATCH_FOUND", ""
}

func callWebhookGuardrail(ctx *rctx.Context, content, endpoint, apiKey string, timeoutMs int) (bool, string) {
	payload, _ := json.Marshal(map[string]string{"content": content})
	client := getLLMClient(endpoint, timeoutMs)
	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		cancel()
		return false, "request build failed"
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	start := time.Now()
	resp, doErr := client.Do(req)
	cancel()
	atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, time.Since(start).Nanoseconds())
	atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)
	if doErr != nil {
		return false, "upstream error"
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, "status error"
	}
	var result struct {
		Violated bool `json:"violated"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, "parse error"
	}
	return result.Violated, ""
}
