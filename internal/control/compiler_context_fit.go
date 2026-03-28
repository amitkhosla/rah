package control

import (
	"fmt"
	"strconv"

	"rah/internal/engine/steps"
)

// compileCheckContextFit resolves and appends the check_context_fit instruction.
//
// Step config mapping:
//   - step.KeyIdentifier      → promptSlot  (ByteSlots)
//   - step.As                 → fitsSlot    (BoolSlots — written via unified slot index)
//   - step.Input["system_slot"]   → systemSlot  (optional, default -1)
//   - step.Input["history_slot"]  → historySlot (optional, default -1)
//   - step.Input["tools_slot"]    → toolsSlot   (optional, default -1)
//   - step.Input["overflow_slot"] → overflowSlot (optional, default -1; IntSlots)
//   - step.Input["total_slot"]    → totalSlot    (optional, default -1; IntSlots)
//   - step.Input["model"]         → look up in c.LLMCfg.Models; read MaxContextTokens + MaxTokens
//   - step.Input["max_output_tokens"] → override for output reservation
func (c *Compiler) compileCheckContextFit(step StepConfig) error {
	// ── PromptSlot (required) ──────────────────────────────────────────────────
	promptSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("check_context_fit: prompt slot: %w", err)
	}

	// ── FitsSlot (required) ───────────────────────────────────────────────────
	fitsSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("check_context_fit: fits slot: %w", err)
	}

	// ── Optional input slots ──────────────────────────────────────────────────
	systemSlot := -1
	if name := step.Input["system_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			systemSlot = s
		}
	}

	historySlot := -1
	if name := step.Input["history_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			historySlot = s
		}
	}

	toolsSlot := -1
	if name := step.Input["tools_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			toolsSlot = s
		}
	}

	// ── Optional output slots ─────────────────────────────────────────────────
	overflowSlot := -1
	if name := step.Input["overflow_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			overflowSlot = s
		}
	}

	totalSlot := -1
	if name := step.Input["total_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			totalSlot = s
		}
	}

	// ── Model limits (baked at compile time) ──────────────────────────────────
	maxContextTokens := 0
	maxOutputTokens := 2000 // default output reservation

	if modelSlug := step.Input["model"]; modelSlug != "" {
		for _, m := range c.LLMCfg.Models {
			if m.Slug == modelSlug {
				maxContextTokens = m.Capabilities.MaxContextTokens
				if m.MaxTokens > 0 {
					maxOutputTokens = m.MaxTokens
				}
				break
			}
		}
	}

	// Explicit override for output token reservation.
	if v := step.Input["max_output_tokens"]; v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n >= 0 {
			maxOutputTokens = n
		}
	}

	cfg := steps.CheckContextFitConfig{
		PromptSlot:       promptSlot,
		SystemSlot:       systemSlot,
		HistorySlot:      historySlot,
		ToolsSlot:        toolsSlot,
		MaxContextTokens: maxContextTokens,
		MaxOutputTokens:  maxOutputTokens,
		FitsSlot:         fitsSlot,
		OverflowSlot:     overflowSlot,
		TotalSlot:        totalSlot,
	}

	c.GlobalTable = append(c.GlobalTable, steps.CheckContextFit(cfg))
	return nil
}
