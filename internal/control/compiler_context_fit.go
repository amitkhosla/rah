package control

import (
	"fmt"
	"strconv"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileCheckContextFit resolves and appends the check_context_fit instruction.
//
// Step config mapping:
//   - step.KeyIdentifier      â†’ promptSlot  (ByteSlots)
//   - step.As                 â†’ fitsSlot    (BoolSlots — written via unified slot index)
//   - step.Input["system_slot"]   â†’ systemSlot  (optional, default -1)
//   - step.Input["history_slot"]  â†’ historySlot (optional, default -1)
//   - step.Input["tools_slot"]    â†’ toolsSlot   (optional, default -1)
//   - step.Input["overflow_slot"] â†’ overflowSlot (optional, default -1; IntSlots)
//   - step.Input["total_slot"]    â†’ totalSlot    (optional, default -1; IntSlots)
//   - step.Input["model"]         â†’ look up in c.LLMCfg.Models; read MaxContextTokens + MaxTokens
//   - step.Input["max_output_tokens"] â†’ override for output reservation
func (c *Compiler) compileCheckContextFit(step StepConfig) error {
	// â"€â"€ PromptSlot (required) â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	promptSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("check_context_fit: prompt slot: %w", err)
	}

	// â"€â"€ FitsSlot (required) â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	fitsSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("check_context_fit: fits slot: %w", err)
	}

	// â"€â"€ Optional input slots â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
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

	// â"€â"€ Optional output slots â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
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

	// â"€â"€ Model limits (baked at compile time) â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	maxContextTokens := 0
	maxOutputTokens := 2000 // default output reservation

	if modelSlug := step.Input["model"]; modelSlug != "" {
		for _, m := range c.LLMCfg.Models {
			if m.Alias == modelSlug {
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
