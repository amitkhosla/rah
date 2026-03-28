package control

import (
	"fmt"

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

	cfg := steps.ParseMessageFormatConfig{
		BodySlot:        bodySlot,
		MessagesSlot:    messagesSlot,
		SystemSlot:      systemSlot,
		DetectedFmtSlot: detectedFmtSlot,
	}
	c.GlobalTable = append(c.GlobalTable, steps.ParseMessageFormat(cfg))
	return nil
}

// compileFormatResponse resolves slots and appends the format_response instruction.
//
// Slot mapping:
//   - key_identifier     → responseSlot (input: response text string)
//   - as                 → outputSlot (output: formatted JSON)
//   - input["format"]    → format string (required: "anthropic", "openai", or "gemini")
//   - input["model"]     → model name string (optional)
func (c *Compiler) compileFormatResponse(step StepConfig) error {
	responseSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("format_response: response slot: %w", err)
	}

	outputSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("format_response: output slot: %w", err)
	}

	format := step.Input["format"]
	if format == "" {
		return fmt.Errorf("format_response: input[\"format\"] is required (\"anthropic\", \"openai\", or \"gemini\")")
	}
	switch format {
	case "anthropic", "openai", "gemini":
		// valid
	default:
		return fmt.Errorf("format_response: unrecognized format %q (must be \"anthropic\", \"openai\", or \"gemini\")", format)
	}

	model := step.Input["model"]

	cfg := steps.FormatResponseConfig{
		ResponseSlot: responseSlot,
		OutputSlot:   outputSlot,
		Format:       format,
		Model:        model,
	}
	c.GlobalTable = append(c.GlobalTable, steps.FormatResponse(cfg))
	return nil
}
