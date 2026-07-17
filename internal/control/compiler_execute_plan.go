package control

import (
	"fmt"
	"strconv"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileExecutePlan resolves and appends the execute_plan instruction.
//
// Step fields:
//   - step.KeyIdentifier             â†’ slot name holding the JSON execution plan from the LLM
//   - step.As                        â†’ result slot name (output: JSON map {step_id: result})
//   - step.Input["error_slot"]       â†’ slot name to write error messages into (-1 if absent)
//   - step.Input["mcp_server"]       â†’ MCP server alias (required)
//   - step.Input["mcp_api_key_ref"]  â†’ literal API key or key reference (optional, overrides server config)
//   - step.Input["timeout_ms"]       â†’ int milliseconds (default 30000)
//   - step.Input["max_concurrent"]   â†’ int (0 = sequential; default 0)
//   - step.Input["skip_new_tool_required"] â†’ "true"/"false" (default "false")
//   - step.Input["messages_slot"]    â†’ slot name holding []CanonicalMessage history; result appended as assistant turn (-1 if absent)
func (c *Compiler) compileExecutePlan(step StepConfig) error {
	// Resolve MCP server.
	alias := step.Input["mcp_server"]
	if alias == "" {
		return fmt.Errorf("execute_plan: input.mcp_server is required")
	}
	serverCfg, err := c.findMCPServer(alias)
	if err != nil {
		return fmt.Errorf("execute_plan: %w", err)
	}

	// Plan slot (input).
	planSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("execute_plan: plan slot: %w", err)
	}

	// Result slot (output).
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("execute_plan: result slot: %w", err)
	}

	// Optional error slot.
	errorSlot := -1
	if errSlotName := step.Input["error_slot"]; errSlotName != "" {
		s, slotErr := c.getSlot(errSlotName)
		if slotErr != nil {
			return fmt.Errorf("execute_plan: error slot: %w", slotErr)
		}
		errorSlot = s
	}

	// Optional API key override.
	mcpAPIKey := step.Input["mcp_api_key_ref"]

	// Timeout.
	timeoutMs := 30_000
	if t := step.Input["timeout_ms"]; t != "" {
		if n, convErr := strconv.Atoi(t); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	// Max concurrent (sequential only for now; placeholder for future use).
	maxConcurrent := 0
	if mc := step.Input["max_concurrent"]; mc != "" {
		if n, convErr := strconv.Atoi(mc); convErr == nil && n >= 0 {
			maxConcurrent = n
		}
	}

	// skip_new_tool_required.
	skipNewTool := false
	if s := step.Input["skip_new_tool_required"]; s == "true" || s == "1" {
		skipNewTool = true
	}

	// Optional messages slot for conversation history append.
	messagesSlot := -1
	if msName := step.Input["messages_slot"]; msName != "" {
		s, slotErr := c.getSlot(msName)
		if slotErr != nil {
			return fmt.Errorf("execute_plan: messages slot: %w", slotErr)
		}
		messagesSlot = s
	}

	cfg := steps.ExecutePlanConfig{
		PlanSlot:            planSlot,
		ResultSlot:          resultSlot,
		ErrorSlot:           errorSlot,
		MCPConfig:           serverCfg,
		MCPAPIKey:           mcpAPIKey,
		MaxConcurrent:       maxConcurrent,
		TimeoutMs:           timeoutMs,
		SkipNewToolRequired: skipNewTool,
		MessagesSlot:        messagesSlot,
	}
	c.GlobalTable = append(c.GlobalTable, steps.ExecutePlan(cfg))
	return nil
}
