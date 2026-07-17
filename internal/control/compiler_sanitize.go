package control

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileEstimateTokens resolves and appends the estimate_tokens instruction.
//
// step.KeyIdentifier â†’ sourceSlot (ByteSlots)
// step.As            â†’ destIntSlot (IntSlots â€” uses unified getSlot index)
func (c *Compiler) compileEstimateTokens(step StepConfig) error {
	sourceSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("estimate_tokens: source slot: %w", err)
	}
	destIntSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("estimate_tokens: dest int slot: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.EstimateTokens(sourceSlot, destIntSlot))
	return nil
}

// compileSanitizePrompt resolves and appends the sanitize_prompt instruction.
//
// step.KeyIdentifier        â†’ promptSlot (ByteSlots, in-place modification)
// step.Input["rules"]       â†’ comma-separated rule list (e.g. "pii,injection,max_tokens:4000")
// step.Input["on_violation"] â†’ "reject" | "strip" | "flag" (default "reject")
// step.Input["flag_slot"]   â†’ slot name for BoolSlots flag (-1 if not provided)
func (c *Compiler) compileSanitizePrompt(step StepConfig) error {
	promptSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("sanitize_prompt: prompt slot: %w", err)
	}

	// Parse rules from comma-separated input string.
	var rules []string
	if raw := strings.TrimSpace(step.Input["rules"]); raw != "" {
		for part := range strings.SplitSeq(raw, ",") {
			if r := strings.TrimSpace(part); r != "" {
				rules = append(rules, r)
			}
		}
	}

	onViolation := strings.TrimSpace(step.Input["on_violation"])
	if onViolation == "" {
		onViolation = "reject"
	}

	flagSlot := -1
	if flagSlotName := strings.TrimSpace(step.Input["flag_slot"]); flagSlotName != "" {
		fs, slotErr := c.getSlot(flagSlotName)
		if slotErr == nil {
			flagSlot = fs
		}
	}

	cfg := steps.SanitizePromptConfig{
		PromptSlot:  promptSlot,
		Rules:       rules,
		OnViolation: onViolation,
		FlagSlot:    flagSlot,
	}
	c.GlobalTable = append(c.GlobalTable, steps.SanitizePrompt(cfg))
	return nil
}

// compileCompressPrompt resolves and appends the compress_prompt instruction.
//
// step.KeyIdentifier        â†’ promptSlot (ByteSlots, in-place modification)
// step.Input["model"]       â†’ model slug (looked up in c.LLMCfg.Models)
// step.Input["target_tokens"] â†’ int (default 2000)
// step.Input["timeout_ms"]  â†’ int (default 30000)
// step.Input["on_exceed"]   â†’ "reject" (default)
// API key resolved from step.Input["api_key"] or model catalog APIKeyRef.
func (c *Compiler) compileCompressPrompt(step StepConfig) error {
	promptSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("compress_prompt: prompt slot: %w", err)
	}

	// Resolve model slug.
	modelSlug := step.Input["model"]
	if modelSlug == "" {
		modelSlug = c.LLMCfg.Default
	}
	if modelSlug == "" {
		return fmt.Errorf("compress_prompt: no model specified and no default model configured")
	}

	// Look up model in catalog.
	var resolvedModel config.LLMModelConfig
	foundModel := false
	for _, m := range c.LLMCfg.Models {
		if m.Alias == modelSlug {
			resolvedModel = m
			foundModel = true
			break
		}
	}
	if !foundModel {
		return fmt.Errorf("compress_prompt: model %q not found in LLM catalog", modelSlug)
	}

	// API key: step.Input["api_key"] overrides catalog APIKeyRef.
	apiKey := step.Input["api_key"]
	if apiKey == "" {
		apiKey = resolvedModel.APIKeyRef
	}

	// target_tokens.
	targetTokens := 2000
	if raw := step.Input["target_tokens"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			targetTokens = n
		}
	}

	// timeout_ms.
	timeoutMs := 30_000
	if raw := step.Input["timeout_ms"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	// on_exceed.
	onExceed := step.Input["on_exceed"]
	if onExceed == "" {
		onExceed = "reject"
	}

	cfg := steps.CompressPromptConfig{
		PromptSlot:   promptSlot,
		ModelConfig:  resolvedModel,
		APIKey:       apiKey,
		TargetTokens: targetTokens,
		TimeoutMs:    timeoutMs,
		OnExceed:     onExceed,
	}
	c.GlobalTable = append(c.GlobalTable, steps.CompressPrompt(cfg))
	return nil
}
