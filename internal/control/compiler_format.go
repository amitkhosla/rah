package control

import (
	"fmt"
	"strconv"

	"rah/internal/engine/steps"
)

// compileParseMessageFormat resolves slots and appends the parse_message_format instruction.
//
// Slot mapping:
//   - key_identifier → bodySlot (input: raw JSON request body)
//   - as             → messagesSlot (output: JSON-encoded []CanonicalMessage)
//   - input["system_slot"]       → systemSlot (optional; default -1)
//   - input["detected_fmt_slot"] → detectedFmtSlot (optional; default -1)
func (c *Compiler) compileParseMessageFormat(step StepConfig) error {
	bodySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("parse_message_format: body slot: %w", err)
	}

	messagesSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("parse_message_format: messages slot: %w", err)
	}

	systemSlot := -1
	if sysSlotName := step.Input["system_slot"]; sysSlotName != "" {
		if s, slotErr := c.getSlot(sysSlotName); slotErr == nil {
			systemSlot = s
		}
	}

	detectedFmtSlot := -1
	if fmtSlotName := step.Input["detected_fmt_slot"]; fmtSlotName != "" {
		if s, slotErr := c.getSlot(fmtSlotName); slotErr == nil {
			detectedFmtSlot = s
		}
	}

	toolsSlot := -1
	if name := step.Input["tools_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			toolsSlot = s
		}
	}

	toolChoiceSlot := -1
	if name := step.Input["tool_choice_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			toolChoiceSlot = s
		}
	}

	streamSlot := -1
	if name := step.Input["stream_slot"]; name != "" {
		if s, slotErr := c.getSlot(name); slotErr == nil {
			streamSlot = s
		}
	}

	cfg := steps.ParseMessageFormatConfig{
		BodySlot:        bodySlot,
		MessagesSlot:    messagesSlot,
		SystemSlot:      systemSlot,
		DetectedFmtSlot: detectedFmtSlot,
		ToolsSlot:       toolsSlot,
		ToolChoiceSlot:  toolChoiceSlot,
		StreamSlot:      streamSlot,
	}
	c.GlobalTable = append(c.GlobalTable, steps.ParseMessageFormat(cfg))
	return nil
}

// compileFormatResponse resolves slots and appends the format_response instruction.
//
// Slot mapping:
//   - key_identifier            → contentSlot (input: LLM response content text)
//   - as                        → resultSlot  (output: formatted JSON response body)
//
// Optional input keys:
//
//	format_slot        → ByteSlot: caller's detected format (from detect_message_format / parse_message_format)
//	format             → static format string "anthropic"|"openai"|"gemini" (bake time; overridden by format_slot)
//	stop_reason_slot   → ByteSlot: stop reason written by llm_call
//	model              → static model slug (bake time)
//	model_slot         → ByteSlot: runtime model slug override
//	input_tokens_slot  → IntSlot index (integer string, e.g. "0")
//	output_tokens_slot → IntSlot index (integer string, e.g. "1")
func (c *Compiler) compileFormatResponse(step StepConfig) error {
	contentSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("format_response: content slot: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("format_response: result slot: %w", err)
	}

	cfg := steps.FormatResponseConfig{
		ContentSlot:      contentSlot,
		ResultSlot:       resultSlot,
		StopReasonSlot:   -1,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		FormatSlot:       -1,
		ModelSlot:        -1,
		StreamSlot:       -1,
		Format:           step.Input["format"],
		Model:            step.Input["model"],
	}

	if v := step.Input["format_slot"]; v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("format_response: format_slot: %w", slotErr)
		}
		cfg.FormatSlot = s
	}

	if v := step.Input["stop_reason_slot"]; v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("format_response: stop_reason_slot: %w", slotErr)
		}
		cfg.StopReasonSlot = s
	}

	if v := step.Input["model_slot"]; v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("format_response: model_slot: %w", slotErr)
		}
		cfg.ModelSlot = s
	}

	if v := step.Input["input_tokens_slot"]; v != "" {
		if idx, convErr := strconv.Atoi(v); convErr == nil && idx >= 0 {
			cfg.InputTokensSlot = idx
		}
	}
	if v := step.Input["output_tokens_slot"]; v != "" {
		if idx, convErr := strconv.Atoi(v); convErr == nil && idx >= 0 {
			cfg.OutputTokensSlot = idx
		}
	}

	if v := step.Input["stream_slot"]; v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("format_response: stream_slot: %w", slotErr)
		}
		cfg.StreamSlot = s
	}

	if v := step.Input["tool_use_slot"]; v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("format_response: tool_use_slot: %w", slotErr)
		}
		cfg.ToolUseSlot = s
	}

	if v := step.Input["thinking_slot"]; v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("format_response: thinking_slot: %w", slotErr)
		}
		cfg.ThinkingSlot = s
	}

	c.GlobalTable = append(c.GlobalTable, steps.FormatResponse(cfg))
	return nil
}
