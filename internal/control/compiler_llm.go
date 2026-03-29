package control

import (
	"fmt"
	"strconv"
	"strings"

	"rah/internal/config"
	"rah/internal/engine/steps"
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

	// Resolve model slug: step.Input["model"] → catalog default
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

	// API key: step.Input["api_key"] overrides catalog APIKeyRef
	apiKey := step.Input["api_key"]
	if apiKey == "" {
		apiKey = modelCfg.APIKeyRef
	}

	// System prompt slot (optional)
	systemSlot := -1
	if sysSlotName := step.Input["system_slot"]; sysSlotName != "" {
		if s, slotErr := c.getSlot(sysSlotName); slotErr == nil {
			systemSlot = s
		}
	}

	// max_tokens: step.Input → model default → 2000
	maxTokens := modelCfg.MaxTokens
	if mt := step.Input["max_tokens"]; mt != "" {
		if n, convErr := strconv.Atoi(mt); convErr == nil && n > 0 {
			maxTokens = n
		}
	}
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	// temperature: step.Input → 0.7
	temperature := 0.7
	if t := step.Input["temperature"]; t != "" {
		if f, convErr := strconv.ParseFloat(t, 64); convErr == nil {
			temperature = f
		}
	}

	// timeout_ms: step.Input → 30 000
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
	}

	// Dynamic model slot (optional)
	if modelSlotName := step.Input["model_slot"]; modelSlotName != "" {
		if s, slotErr := c.getSlot(modelSlotName); slotErr == nil {
			llmCfg.ModelSlot = s
			// Build catalog map for runtime lookup
			llmCfg.ModelCatalog = make(map[string]config.LLMModelConfig, len(c.LLMCfg.Models))
			for _, m := range c.LLMCfg.Models {
				llmCfg.ModelCatalog[m.Alias] = m
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
	//   fallback_model: "alias"               → single-entry chain (backward compat)
	//   fallback_chain: "alias1,alias2,..."   → ordered multi-hop chain
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
			fbKey := mc.APIKeyRef
			fallbackChain = append(fallbackChain, steps.FallbackEntry{ModelConfig: mc, APIKey: fbKey})
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
		fbKey := step.Input["fallback_api_key"]
		if fbKey == "" {
			fbKey = mc.APIKeyRef
		}
		fallbackChain = append(fallbackChain, steps.FallbackEntry{ModelConfig: mc, APIKey: fbKey})
	}

	llmCfg.FallbackChain = fallbackChain

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

	c.GlobalTable = append(c.GlobalTable, steps.LLMCall(llmCfg))
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
