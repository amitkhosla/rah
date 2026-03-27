package control

import (
	"fmt"
	"strconv"

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
		if m.Slug == modelSlug {
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
		ModelConfig: modelCfg,
		APIKey:      apiKey,
		TimeoutMs:   timeoutMs,
		MaxRetries:  2,
		PromptSlot:  promptSlot,
		ResultSlot:  resultSlot,
		SystemSlot:  systemSlot,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		OnExceed:    "reject",
	}
	c.GlobalTable = append(c.GlobalTable, steps.LLMCall(llmCfg))
	return nil
}
