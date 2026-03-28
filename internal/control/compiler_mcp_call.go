package control

import (
	"fmt"
	"strconv"

	"rah/internal/engine/steps"
)

// compileMCPCallTool resolves and appends the mcp_call_tool instruction.
//
// Step fields:
//   - step.KeyIdentifier       → slot name holding the tool name to call
//   - step.As                  → result slot name (output: JSON content array)
//   - step.Input["server"]     → MCP server alias (required)
//   - step.Input["args_slot"]  → slot name holding the JSON-encoded arguments object
//   - step.Input["timeout_ms"] → int milliseconds (default 30000)
func (c *Compiler) compileMCPCallTool(step StepConfig) error {
	alias := step.Input["server"]
	if alias == "" {
		return fmt.Errorf("mcp_call_tool: input.server is required")
	}

	serverCfg, err := c.findMCPServer(alias)
	if err != nil {
		return fmt.Errorf("mcp_call_tool: %w", err)
	}

	toolNameSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("mcp_call_tool: tool name slot: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("mcp_call_tool: result slot: %w", err)
	}

	argsSlot := -1
	if argsSlotName := step.Input["args_slot"]; argsSlotName != "" {
		s, slotErr := c.getSlot(argsSlotName)
		if slotErr != nil {
			return fmt.Errorf("mcp_call_tool: args slot: %w", slotErr)
		}
		argsSlot = s
	}

	timeoutMs := 30_000
	if t := step.Input["timeout_ms"]; t != "" {
		if n, convErr := strconv.Atoi(t); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	cfg := steps.MCPCallToolConfig{
		ServerURL:    serverCfg.URL,
		APIKey:       serverCfg.APIKeyRef,
		TimeoutMs:    timeoutMs,
		ToolNameSlot: toolNameSlot,
		ArgsSlot:     argsSlot,
		ResultSlot:   resultSlot,
	}
	c.GlobalTable = append(c.GlobalTable, steps.MCPCallTool(cfg))
	return nil
}
