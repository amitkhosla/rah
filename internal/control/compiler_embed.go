package control

import (
	"fmt"
	"strconv"

	"rah/internal/engine/steps"
)

// compileEmbedText handles the "embed_text" step type.
//
// StepConfig fields:
//   - key_identifier       → inputSlot (text to embed)
//   - as                   → resultSlot (vector JSON output)
//   - input["provider"]    → "openai" | "ollama" | "gemini" (required)
//   - input["model"]       → model name (required)
//   - input["base_url"]    → optional endpoint override
//   - input["api_key_ref"] → literal API key or secret reference (resolved at bake time)
//   - input["api_key_slot"]→ optional slot name for runtime key injection
//   - input["dim_slot"]    → optional IntSlot index for dimension count output
//   - input["timeout_ms"]  → optional timeout in milliseconds (default 10000)
//   - input["max_retries"] → optional max retry count (default 2)
func (c *Compiler) compileEmbedText(step StepConfig) error {
	inputSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("embed_text: input slot: %w", err)
	}
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("embed_text: result slot: %w", err)
	}

	// provider is required
	providerStr := step.Input["provider"]
	if providerStr == "" {
		return fmt.Errorf("embed_text: input[\"provider\"] is required (openai, ollama, gemini)")
	}
	provider := steps.EmbedProvider(providerStr)
	switch provider {
	case steps.EmbedProviderOpenAI, steps.EmbedProviderOllama, steps.EmbedProviderGemini:
		// valid
	default:
		return fmt.Errorf("embed_text: unsupported provider %q; must be openai, ollama, or gemini", providerStr)
	}

	// model is required
	model := step.Input["model"]
	if model == "" {
		return fmt.Errorf("embed_text: input[\"model\"] is required")
	}

	// base_url: optional override
	baseURL := step.Input["base_url"]

	// api_key_ref: literal key or resolved the same way compiler_llm.go resolves APIKeyRef.
	// The SecretsMgr-based resolution happens at bake time when a "secret:" prefix is present.
	apiKey := step.Input["api_key_ref"]

	// api_key_slot: optional slot name for runtime key injection
	apiKeySlot := -1
	if v, ok := step.Input["api_key_slot"]; ok && v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("embed_text: api_key_slot: %w", slotErr)
		}
		apiKeySlot = s
	}

	// dim_slot: optional IntSlot index (literal integer, not a named ByteSlot)
	// Follows the same pattern as input_tokens_slot in compiler_llm.go.
	dimSlot := -1
	if v, ok := step.Input["dim_slot"]; ok && v != "" {
		if idx, convErr := strconv.Atoi(v); convErr == nil && idx >= 0 {
			dimSlot = idx
		}
	}

	// timeout_ms: optional, default 10000
	timeoutMs := 10_000
	if v, ok := step.Input["timeout_ms"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	// max_retries: optional, default 2
	maxRetries := 2
	if v, ok := step.Input["max_retries"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n >= 0 {
			maxRetries = n
		}
	}

	cfg := steps.EmbedTextConfig{
		InputSlot:  inputSlot,
		ResultSlot: resultSlot,
		DimSlot:    dimSlot,
		Provider:   provider,
		Model:      model,
		BaseURL:    baseURL,
		APIKey:     apiKey,
		APIKeySlot: apiKeySlot,
		TimeoutMs:  timeoutMs,
		MaxRetries: maxRetries,
	}

	c.GlobalTable = append(c.GlobalTable, steps.EmbedText(cfg))
	return nil
}
