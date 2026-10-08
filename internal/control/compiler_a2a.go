package control

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileA2ACall compiles the a2a_call step.
//
// step.Input fields:
//
//	url         — base URL of the remote A2A agent (required)
//	skill_id    — optional skill ID hint passed to the agent
//	input_var   — slot name holding the JSON message to send (required)
//	output_var  — slot name to write the result JSON into (required)
//	timeout_sec — HTTP timeout in seconds (default 30)
func (c *Compiler) compileA2ACall(step StepConfig) error {
	url := strings.TrimSpace(step.Input["url"])
	if url == "" {
		return fmt.Errorf("a2a_call: input.url is required")
	}

	inputVar := strings.TrimSpace(step.Input["input_var"])
	if inputVar == "" {
		return fmt.Errorf("a2a_call: input.input_var is required")
	}
	inputSlot, err := c.getSlot(inputVar)
	if err != nil {
		return fmt.Errorf("a2a_call: input_var slot %q: %w", inputVar, err)
	}

	outputVar := strings.TrimSpace(step.Input["output_var"])
	if outputVar == "" {
		return fmt.Errorf("a2a_call: input.output_var is required")
	}
	outputSlot, err := c.getSlot(outputVar)
	if err != nil {
		return fmt.Errorf("a2a_call: output_var slot %q: %w", outputVar, err)
	}

	timeoutSec := 30
	if t := strings.TrimSpace(step.Input["timeout_sec"]); t != "" {
		if n, convErr := strconv.Atoi(t); convErr == nil && n > 0 {
			timeoutSec = n
		}
	}

	cfg := steps.A2ACallConfig{
		URL:        url,
		SkillID:    strings.TrimSpace(step.Input["skill_id"]),
		InputSlot:  inputSlot,
		OutputSlot: outputSlot,
		TimeoutSec: timeoutSec,
	}

	c.GlobalTable = append(c.GlobalTable, steps.A2ACall(cfg))
	return nil
}
