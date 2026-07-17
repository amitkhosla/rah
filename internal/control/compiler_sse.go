package control

import (
	"fmt"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

func (c *Compiler) compileSSEEvent(step StepConfig) error {
	dataSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("send_sse_event: data slot: %w", err)
	}

	eventSlot := -1
	if v, ok := step.Input["event"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err == nil {
			eventSlot = s
		}
	}

	idSlot := -1
	if v, ok := step.Input["id_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err == nil {
			idSlot = s
		}
	}

	cfg := steps.SSEEventConfig{
		EventSlot: eventSlot,
		DataSlot:  dataSlot,
		IDSlot:    idSlot,
	}
	c.GlobalTable = append(c.GlobalTable, steps.SendSSEEvent(cfg))
	return nil
}
