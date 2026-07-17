package control

import (
	"fmt"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileDetectMessageFormat resolves slots and appends the detect_message_format instruction.
//
// Slot mapping:
//   - key_identifier â†’ bodySlot (input: raw JSON request body)
//   - as             â†’ formatSlot (output: detected format string)
func (c *Compiler) compileDetectMessageFormat(step StepConfig) error {
	bodySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("detect_message_format: body slot: %w", err)
	}

	formatSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("detect_message_format: format slot: %w", err)
	}

	cfg := steps.DetectMessageFormatConfig{
		BodySlot:   bodySlot,
		FormatSlot: formatSlot,
	}
	c.GlobalTable = append(c.GlobalTable, steps.DetectMessageFormat(cfg))
	return nil
}
