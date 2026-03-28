package control

import (
	"fmt"
	"strconv"

	"rah/internal/engine/steps"
)

// compileRerank resolves and appends the rerank instruction.
// Separated from compiler.go to keep all rerank compilation logic in one place.
func (c *Compiler) compileRerank(step StepConfig) error {
	querySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("rerank: query slot: %w", err)
	}

	docsSlot, err := c.getSlotReadOnly(step.Input["docs"])
	if err != nil {
		return fmt.Errorf("rerank: docs slot: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("rerank: result slot: %w", err)
	}

	// Determine provider (default: Cohere)
	provider := steps.RerankCohere
	if p, ok := step.Input["provider"]; ok && p == "jina" {
		provider = steps.RerankJina
	}

	// API key (resolved at compile time)
	apiKey := step.Input["api_key"]
	if apiKey == "" {
		// Try to resolve from api_key_ref (e.g., "secret:cohere_key")
		if ref, ok := step.Input["api_key_ref"]; ok && ref != "" {
			// For now, just use the ref as-is; a real implementation would resolve
			// from a secret store. Keeping it simple for now.
			apiKey = ref
		}
	}

	// Model slug
	model := step.Input["model"]
	if model == "" {
		return fmt.Errorf("rerank: no model specified")
	}

	// Top-N (optional, default 0 = all)
	topN := 0
	if v, ok := step.Input["top_n"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topN = n
		}
	}

	// Timeout in milliseconds (optional, default 10000)
	timeoutMs := 10000
	if v, ok := step.Input["timeout_ms"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeoutMs = n
		}
	}

	// BaseURL override (optional)
	baseURL := step.Input["base_url"]

	cfg := steps.RerankConfig{
		Provider:   provider,
		BaseURL:    baseURL,
		APIKey:     apiKey,
		Model:      model,
		QuerySlot:  querySlot,
		DocsSlot:   docsSlot,
		ResultSlot: resultSlot,
		TopN:       topN,
		TimeoutMs:  timeoutMs,
	}

	c.GlobalTable = append(c.GlobalTable, steps.Rerank(cfg))
	return nil
}
