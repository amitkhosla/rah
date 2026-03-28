package control

import (
	"fmt"

	"rah/internal/engine/steps"
)

// compileParseToolCalls resolves slots and appends the parse_tool_calls instruction.
//
// Slot mapping:
//   - key_identifier → responseSlot (input: raw LLM response content to parse)
//   - as             → toolCallsSlot (output: extracted []CanonicalToolCall JSON)
//
// Optional input fields:
//   - input["count_slot"]            → IntSlot name for writing tool call count (-1 if absent)
//   - input["has_tool_calls_slot"]   → BoolSlot name for writing presence flag (-1 if absent)
//   - input["source_format"]         → "anthropic", "openai", or "" (auto-detect)
func (c *Compiler) compileParseToolCalls(step StepConfig) error {
	responseSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("parse_tool_calls: response slot: %w", err)
	}

	toolCallsSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("parse_tool_calls: tool calls slot: %w", err)
	}

	countSlot := -1
	if raw := step.Input["count_slot"]; raw != "" {
		idx, slotErr := c.getSlot(raw)
		if slotErr != nil {
			return fmt.Errorf("parse_tool_calls: count_slot: %w", slotErr)
		}
		countSlot = idx
	}

	hasToolCallsSlot := -1
	if raw := step.Input["has_tool_calls_slot"]; raw != "" {
		idx, slotErr := c.getSlot(raw)
		if slotErr != nil {
			return fmt.Errorf("parse_tool_calls: has_tool_calls_slot: %w", slotErr)
		}
		hasToolCallsSlot = idx
	}

	sourceFormat := step.Input["source_format"] // "" = auto-detect

	cfg := steps.ParseToolCallsConfig{
		ResponseSlot:     responseSlot,
		ToolCallsSlot:    toolCallsSlot,
		CountSlot:        countSlot,
		HasToolCallsSlot: hasToolCallsSlot,
		SourceFormat:     sourceFormat,
	}
	c.GlobalTable = append(c.GlobalTable, steps.ParseToolCalls(cfg))
	return nil
}

// compileAppendToolResult resolves slots and appends the append_tool_result instruction.
//
// Slot mapping:
//   - key_identifier → historySlot (ByteSlots, in-place modification)
//
// Required input fields:
//   - input["tool_call_id_slot"] → ByteSlot name holding the tool call ID string
//   - input["result_slot"]       → ByteSlot name holding the tool result content
//
// Optional input fields:
//   - input["target_format"] → "anthropic" or "openai" (default "openai")
func (c *Compiler) compileAppendToolResult(step StepConfig) error {
	historySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("append_tool_result: history slot: %w", err)
	}

	toolCallIDSlotName := step.Input["tool_call_id_slot"]
	if toolCallIDSlotName == "" {
		return fmt.Errorf("append_tool_result: input.tool_call_id_slot is required")
	}
	toolCallIDSlot, err := c.getSlot(toolCallIDSlotName)
	if err != nil {
		return fmt.Errorf("append_tool_result: tool_call_id_slot: %w", err)
	}

	resultSlotName := step.Input["result_slot"]
	if resultSlotName == "" {
		return fmt.Errorf("append_tool_result: input.result_slot is required")
	}
	resultSlot, err := c.getSlot(resultSlotName)
	if err != nil {
		return fmt.Errorf("append_tool_result: result_slot: %w", err)
	}

	targetFormat := step.Input["target_format"]
	if targetFormat == "" {
		targetFormat = "openai"
	}

	cfg := steps.AppendToolResultConfig{
		HistorySlot:    historySlot,
		ToolCallIDSlot: toolCallIDSlot,
		ResultSlot:     resultSlot,
		TargetFormat:   targetFormat,
	}
	c.GlobalTable = append(c.GlobalTable, steps.AppendToolResult(cfg))
	return nil
}
