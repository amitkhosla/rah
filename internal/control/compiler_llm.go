package control

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileLLMCall resolves and appends the llm_call instruction.
// Separated from compiler.go to keep all LLM compilation logic in one place
// as the AI group (sanitization, routing, proxy, MCP) grows.
func (c *Compiler) compileLLMCall(step StepConfig) error {
	promptSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("llm_call: prompt slot: %w", err)
	}
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("llm_call: result slot: %w", err)
	}

	// Resolve model slug: step.Input["model"] â†’ catalog default
	modelSlug := step.Input["model"]
	if modelSlug == "" {
		modelSlug = c.LLMCfg.Default
	}
	if modelSlug == "" {
		return fmt.Errorf("llm_call: no model specified and no default model configured")
	}

	// Look up model in catalog
	var modelCfg config.LLMModelConfig
	found := false
	for _, m := range c.LLMCfg.Models {
		if m.Alias == modelSlug {
			modelCfg = m
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("llm_call: model %q not found in LLM catalog", modelSlug)
	}

	// API key: step.Input["api_key"] overrides catalog APIKeyRef.
	// The resolved value is passed through the secrets manager so that
	// references like "env:OPENAI_KEY" or "file:///run/secrets/key" are
	// resolved to their plaintext value at bake time.
	rawKey := step.Input["api_key"]
	if rawKey == "" {
		rawKey = modelCfg.APIKeyRef
	}
	apiKey, err := c.resolveAPIKey(rawKey)
	if err != nil {
		return fmt.Errorf("llm_call: %w", err)
	}

	// System prompt slot (optional)
	systemSlot := -1
	if sysSlotName := step.Input["system_slot"]; sysSlotName != "" {
		if s, slotErr := c.getSlot(sysSlotName); slotErr == nil {
			systemSlot = s
		}
	}

	// max_tokens: step.Input â†’ model default â†’ 2000
	maxTokens := modelCfg.MaxTokens
	if mt := step.Input["max_tokens"]; mt != "" {
		if n, convErr := strconv.Atoi(mt); convErr == nil && n > 0 {
			maxTokens = n
		}
	}
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	// temperature: step.Input â†’ 0.7
	temperature := 0.7
	if t := step.Input["temperature"]; t != "" {
		if f, convErr := strconv.ParseFloat(t, 64); convErr == nil {
			temperature = f
		}
	}

	// timeout_ms: step.Input â†’ 30 000
	timeoutMs := 30_000
	if t := step.Input["timeout_ms"]; t != "" {
		if n, convErr := strconv.Atoi(t); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	llmCfg := steps.LLMCallConfig{
		ModelConfig:      modelCfg,
		APIKey:           apiKey,
		TimeoutMs:        timeoutMs,
		MaxRetries:       2,
		PromptSlot:       promptSlot,
		ResultSlot:       resultSlot,
		SystemSlot:       systemSlot,
		MaxTokens:        maxTokens,
		Temperature:      temperature,
		OnExceed:         "reject",
		ModelSlot:        -1, // disabled by default
		APIKeySlot:       -1, // disabled by default
		InputTokensSlot:  -1, // disabled by default
		OutputTokensSlot: -1, // disabled by default
		MessagesSlot:     -1, // disabled by default
		// Tool / thinking slots — disabled by default
		ToolsSlot:       -1,
		ToolChoiceSlot:  -1,
		ThinkingSlot:    -1,
		ToolUseSlot:     -1,
		ThinkingOutSlot: -1,
		// Prompt caching — disabled by default
		PromptCacheEnabled: false,
		PromptCacheUpTo:    -1,
		CacheReadSlot:      -1,
		CacheCreationSlot:  -1,
	}

	// Dynamic model slot (optional)
	if modelSlotName := step.Input["model_slot"]; modelSlotName != "" {
		if s, slotErr := c.getSlot(modelSlotName); slotErr == nil {
			llmCfg.ModelSlot = s
			// Build catalog map for runtime lookup; also pre-resolve API keys so that
			// the runtime never passes a raw "env:FOO" ref as a literal key to a provider.
			llmCfg.ModelCatalog = make(map[string]config.LLMModelConfig, len(c.LLMCfg.Models))
			llmCfg.CatalogKeys = make(map[string]string, len(c.LLMCfg.Models))
			for _, m := range c.LLMCfg.Models {
				llmCfg.ModelCatalog[m.Alias] = m
				if resolvedKey, keyErr := c.resolveAPIKey(m.APIKeyRef); keyErr == nil {
					llmCfg.CatalogKeys[m.Alias] = resolvedKey
				}
			}
		}
	}

	// api_key_slot: optional slot name holding a runtime API key (per-tenant injection).
	// The slot must have been populated before this instruction executes (e.g. by
	// a preceding load_credential or load_llm_key step).
	if v, ok := step.Input["api_key_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: api_key_slot: %w", err)
		}
		llmCfg.APIKeySlot = s
	}

	// input_tokens_slot / output_tokens_slot: optional IntSlot indices for token
	// usage accounting. These are IntSlots (not ByteSlots), so we accept a literal
	// integer index from config rather than a named slot (the compiler's getSlot only
	// tracks ByteSlots).
	if v, ok := step.Input["input_tokens_slot"]; ok && v != "" {
		if idx, convErr := strconv.Atoi(v); convErr == nil && idx >= 0 {
			llmCfg.InputTokensSlot = idx
		}
	}
	if v, ok := step.Input["output_tokens_slot"]; ok && v != "" {
		if idx, convErr := strconv.Atoi(v); convErr == nil && idx >= 0 {
			llmCfg.OutputTokensSlot = idx
		}
	}

	// Resolve fallback chain.
	// Supports two formats:
	//   fallback_model: "alias"               â†’ single-entry chain (backward compat)
	//   fallback_chain: "alias1,alias2,..."   â†’ ordered multi-hop chain
	// Both can be combined; fallback_model is appended after fallback_chain entries.
	var fallbackChain []steps.FallbackEntry

	if chainStr, ok := step.Input["fallback_chain"]; ok && chainStr != "" {
		for _, alias := range splitTrimmed(chainStr, ',') {
			if alias == "" {
				continue
			}
			var mc config.LLMModelConfig
			found := false
			for _, m := range c.LLMCfg.Models {
				if m.Alias == alias {
					mc = m
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("llm_call: fallback_chain entry %q not found in LLM catalog", alias)
			}
			fbKey, err := c.resolveAPIKey(mc.APIKeyRef)
			if err != nil {
				return fmt.Errorf("llm_call: fallback_chain %q: %w", alias, err)
			}
			fallbackChain = append(fallbackChain, steps.FallbackEntry{ModelConfig: mc, APIKey: fbKey, CircuitName: "llm:" + mc.Alias})
		}
	}

	if singleFallback, ok := step.Input["fallback_model"]; ok && singleFallback != "" {
		var mc config.LLMModelConfig
		found := false
		for _, m := range c.LLMCfg.Models {
			if m.Alias == singleFallback {
				mc = m
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("llm_call: fallback_model %q not found in LLM catalog", singleFallback)
		}
		rawFbKey := step.Input["fallback_api_key"]
		if rawFbKey == "" {
			rawFbKey = mc.APIKeyRef
		}
		fbKey, err := c.resolveAPIKey(rawFbKey)
		if err != nil {
			return fmt.Errorf("llm_call: fallback_model %q: %w", singleFallback, err)
		}
		fallbackChain = append(fallbackChain, steps.FallbackEntry{ModelConfig: mc, APIKey: fbKey, CircuitName: "llm:" + mc.Alias})
	}

	llmCfg.FallbackChain = fallbackChain

	// fallback_by_model: optional JSON map of alias â†’ []alias that provides per-model
	// fallback chains when model_slot is used. Resolved at bake time so all API keys
	// are available. At runtime, the selected model's chain overrides FallbackChain.
	// Format: {"gpt-4o-mini":["gemini-flash","claude-haiku"],"claude-opus":["gpt-4o"]}
	if fbmJSON, ok := step.Input["fallback_by_model"]; ok && fbmJSON != "" {
		var rawMap map[string][]string
		if jsonErr := json.Unmarshal([]byte(fbmJSON), &rawMap); jsonErr != nil {
			return fmt.Errorf("llm_call: fallback_by_model: invalid JSON: %w", jsonErr)
		}
		fbm := make(map[string][]steps.FallbackEntry, len(rawMap))
		for modelAlias, fallbacks := range rawMap {
			var entries []steps.FallbackEntry
			for _, fbAlias := range fallbacks {
				var mc config.LLMModelConfig
				found := false
				for _, m := range c.LLMCfg.Models {
					if m.Alias == fbAlias {
						mc = m
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("llm_call: fallback_by_model[%q]: %q not found in LLM catalog", modelAlias, fbAlias)
				}
				fbKey, fbKeyErr := c.resolveAPIKey(mc.APIKeyRef)
				if fbKeyErr != nil {
					return fmt.Errorf("llm_call: fallback_by_model[%q][%q]: %w", modelAlias, fbAlias, fbKeyErr)
				}
				entries = append(entries, steps.FallbackEntry{ModelConfig: mc, APIKey: fbKey, CircuitName: "llm:" + mc.Alias})
			}
			fbm[modelAlias] = entries
		}
		llmCfg.FallbackByModel = fbm
	}

	// model_config_slot: optional slot holding a runtime JSON-encoded LLMModelConfig
	// that overrides the baked model config per request (payload injection).
	llmCfg.ModelConfigSlot = -1
	if v, ok := step.Input["model_config_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: model_config_slot: %w", err)
		}
		llmCfg.ModelConfigSlot = s
	}

	// stop_reason_slot: optional ByteSlot to write the LLM stop reason after a successful call.
	// Used by format_response to map the stop reason to the caller's expected wire format.
	llmCfg.StopReasonSlot = -1
	if v, ok := step.Input["stop_reason_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: stop_reason_slot: %w", err)
		}
		llmCfg.StopReasonSlot = s
	}

	// messages_slot: optional ByteSlot holding JSON-encoded []CanonicalMessage
	// produced by parse_message_format. When set, the full conversation history
	// (multi-turn, content blocks) is sent to the model instead of a single
	// user turn from prompt_slot. Set alongside system_slot from parse_message_format.
	if v, ok := step.Input["messages_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: messages_slot: %w", err)
		}
		llmCfg.MessagesSlot = s
	}

	// Mutual exclusivity: prompt_slot and messages_slot are alternatives.
	if llmCfg.MessagesSlot >= 0 && llmCfg.PromptSlot >= 0 {
		return fmt.Errorf("llm_call: specify either prompt_slot or messages_slot, not both")
	}

	// tools_slot: ByteSlot populated by parse_message_format containing JSON []ToolDefinition.
	if v, ok := step.Input["tools_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: tools_slot: %w", err)
		}
		llmCfg.ToolsSlot = s
	}

	// tool_choice_slot: ByteSlot containing a JSON-encoded ToolChoice override.
	if v, ok := step.Input["tool_choice_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: tool_choice_slot: %w", err)
		}
		llmCfg.ToolChoiceSlot = s
	}

	// tool_choice: static tool choice strategy ("auto", "required", "none", "tool").
	if v, ok := step.Input["tool_choice"]; ok && v != "" {
		llmCfg.ToolChoice = steps.ToolChoiceType(v)
	}

	// thinking_slot: ByteSlot containing a JSON-encoded ThinkingConfig override.
	if v, ok := step.Input["thinking_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: thinking_slot: %w", err)
		}
		llmCfg.ThinkingSlot = s
	}

	// thinking_enabled / thinking_budget: bake-time static ThinkingConfig.
	if v, ok := step.Input["thinking_enabled"]; ok && v == "true" {
		if llmCfg.Thinking == nil {
			llmCfg.Thinking = &steps.ThinkingConfig{}
		}
		llmCfg.Thinking.Enabled = true
	}
	if v, ok := step.Input["thinking_budget"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
			if llmCfg.Thinking == nil {
				llmCfg.Thinking = &steps.ThinkingConfig{}
			}
			llmCfg.Thinking.BudgetTokens = n
		}
	}

	// tool_use_slot: ByteSlot to write JSON []ContentBlock (tool_use type) after a
	// successful call. Read by format_response to assemble tool_use response blocks.
	if v, ok := step.Input["tool_use_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: tool_use_slot: %w", err)
		}
		llmCfg.ToolUseSlot = s
	}

	// thinking_out_slot: ByteSlot to write the first thinking block text after a
	// successful call.
	if v, ok := step.Input["thinking_out_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: thinking_out_slot: %w", err)
		}
		llmCfg.ThinkingOutSlot = s
	}

	// prompt_cache: "true"/"false" to enable Anthropic prompt caching.
	if v, ok := step.Input["prompt_cache"]; ok && v != "" {
		llmCfg.PromptCacheEnabled = v == "true"
	}

	// prompt_cache_up_to: which message index to mark for caching (default -1 = last).
	if v, ok := step.Input["prompt_cache_up_to"]; ok && v != "" {
		if idx, convErr := strconv.Atoi(v); convErr == nil {
			llmCfg.PromptCacheUpTo = idx
		}
	}

	// cache_read_var: IntSlot to write cache_read_input_tokens into.
	if v, ok := step.Input["cache_read_var"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: cache_read_var: %w", err)
		}
		llmCfg.CacheReadSlot = s
	}

	// cache_creation_var: IntSlot to write cache_creation_input_tokens into.
	if v, ok := step.Input["cache_creation_var"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("llm_call: cache_creation_var: %w", err)
		}
		llmCfg.CacheCreationSlot = s
	}

	// stream_to_client: enables streaming response output to the client.
	if v, ok := step.Input["stream_to_client"]; ok && v == "true" {
		llmCfg.StreamToClient = true
	}

	// Wire CostLimiter, CircuitName, CircuitEventPipeline into llmCfg
	llmCfg.CostLimiter = engine.GetModelCostLimiter(modelSlug)
	if llmCfg.CostLimiter != nil {
		llmCfg.CircuitName = "llm:" + modelSlug
		llmCfg.CircuitEventPipeline = c.IngestPipeline
	}

	c.GlobalTable = append(c.GlobalTable, steps.LLMCall(llmCfg))

	// Register per-model upstream rate limits at bake time for all catalog models.
	// This covers both the baked model and any model reachable via model_slot.
	for _, m := range c.LLMCfg.Models {
		if len(m.RateLimits) > 0 {
			windows := make([]*engine.UpstreamRateWindow, 0, len(m.RateLimits))
			for _, rl := range m.RateLimits {
				windows = append(windows, engine.UpstreamWindowFromWindow(rl.Window, uint32(rl.Limit)))
			}
			engine.RegisterUpstreamLimit(m.Alias, windows)
		}
	}

	// Register per-model cost limiters and pre-create named circuits.
	for i := range c.LLMCfg.Models {
		m := &c.LLMCfg.Models[i]
		if len(m.CostLimits) > 0 {
			windows := make([]*engine.UpstreamCostWindow, 0, len(m.CostLimits))
			for _, cl := range m.CostLimits {
				w := engine.UpstreamCostWindowFromWindow(cl.Window, cl.LimitUSD)
				if w != nil {
					windows = append(windows, w)
				}
			}
			if len(windows) > 0 {
				engine.RegisterModelCostLimit(m.Alias, windows)
			}
		}
		// Pre-create circuit so IsNamedCircuitOpen is a hot Load-only path after startup.
		engine.GetOrCreateNamedCircuit("llm:"+m.Alias, 5, 2, 60_000)
	}

	return nil
}

// splitTrimmed splits s by sep and trims whitespace from each piece.
func splitTrimmed(s string, sep rune) []string {
	var out []string
	for _, p := range splitRune(s, sep) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitRune splits s by sep rune (avoids importing strings just for Split).
func splitRune(s string, sep rune) []string {
	var parts []string
	start := 0
	for i, r := range s {
		if r == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
