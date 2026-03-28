package control

import (
	"fmt"

	"rah/internal/engine/steps"
)

// compileTransformMessages resolves slots and appends the transform_messages instruction.
//
// Slot mapping:
//   - key_identifier             → historySlot (ByteSlots, in-place transform)
//   - input["system_slot"]       → systemSlot (optional; default -1)
//   - input["thinking_slot"]     → thinkingSlot (optional; default -1)
//   - input["format_slot"]       → formatSlot (optional; default -1)
//   - input["source_format"]     → sourceFormat string (used if format_slot not set)
//   - input["target_format"]     → targetFormat string (required if format_slot not set)
//   - input["strip_thinking"]    → bool "true"/"false"
//   - input["extract_thinking"]  → bool "true"/"false"
//   - input["extract_system"]    → bool "true"/"false"
//   - input["inject_system"]     → bool "true"/"false"
//   - input["flatten_content"]   → bool "true"/"false"
//   - input["adapt_roles"]       → bool "true"/"false"
//   - input["normalize_tools"]   → bool "true"/"false"
func (c *Compiler) compileTransformMessages(step StepConfig) error {
	historySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("transform_messages: history slot: %w", err)
	}

	systemSlot := -1
	if name := step.Input["system_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			systemSlot = s
		}
	}

	thinkingSlot := -1
	if name := step.Input["thinking_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			thinkingSlot = s
		}
	}

	formatSlot := -1
	if name := step.Input["format_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			formatSlot = s
		}
	}

	parseBool := func(key string) bool {
		return step.Input[key] == "true"
	}

	cfg := steps.TransformMessagesConfig{
		HistorySlot:     historySlot,
		SystemSlot:      systemSlot,
		ThinkingSlot:    thinkingSlot,
		FormatSlot:      formatSlot,
		SourceFormat:    step.Input["source_format"],
		TargetFormat:    step.Input["target_format"],
		StripThinking:   parseBool("strip_thinking"),
		ExtractThinking: parseBool("extract_thinking"),
		ExtractSystem:   parseBool("extract_system"),
		InjectSystem:    parseBool("inject_system"),
		FlattenContent:  parseBool("flatten_content"),
		AdaptRoles:      parseBool("adapt_roles"),
		NormalizeTools:  parseBool("normalize_tools"),
	}

	c.GlobalTable = append(c.GlobalTable, steps.TransformMessages(cfg))
	return nil
}
