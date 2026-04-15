package control

import (
	"fmt"

	"rah/internal/config"
	"rah/internal/engine/steps"
)

// compileClassifyLLM resolves slots and appends the classify_llm instruction.
// Expects:
//   - input["model"]: classifier model slug
//   - input["prompt_slot"]: slot holding the classification prompt
//   - input["mapping"]: JSON map of "response_key" -> "target_slot_name"
func (c *Compiler) compileClassifyLLM(step StepConfig) error {
	// 1. Resolve model
	modelSlug := step.Input["model"]
	if modelSlug == "" {
		modelSlug = c.LLMCfg.Default
	}
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
		return fmt.Errorf("classify_llm: model %q not found", modelSlug)
	}

	apiKey, err := c.resolveAPIKey(modelCfg.APIKeyRef)
	if err != nil {
		return fmt.Errorf("classify_llm: %w", err)
	}

	// 2. Resolve slots
	promptSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("classify_llm: prompt slot: %w", err)
	}
	
	// Temporary slot for raw JSON response
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("classify_llm: result slot: %w", err)
	}

	// 3. Resolve mapping
	mapping := make(map[string]int)
	for jsonKey, slotName := range step.Input {
		if jsonKey == "model" || jsonKey == "prompt_slot" || jsonKey == "result_slot" {
			continue
		}
		// Treat any other key as a mapping entry
		if s, slotErr := c.getSlot(slotName); slotErr == nil {
			mapping[jsonKey] = s
		}
	}

	classifyCfg := steps.ClassifyLLMConfig{
		CallConfig: steps.LLMCallConfig{
			ModelConfig: modelCfg,
			APIKey:      apiKey,
			PromptSlot:  promptSlot,
			ResultSlot:  resultSlot,
			MaxTokens:   500, // Small limit for classification
			Temperature: 0.0, // Strict for classification
		},
		Mapping: mapping,
	}

	c.GlobalTable = append(c.GlobalTable, steps.ClassifyLLM(classifyCfg))
	return nil
}
