package control

import (
	"fmt"
	"strconv"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileMCPListTools resolves and appends the mcp_list_tools instruction.
//
// Step fields:
//   - step.Input["server"]     â†’ MCP server alias (required)
//   - step.Input["mode"]       â†’ MCPLoadMode: "names", "brief", or "full" (default "full")
//   - step.Input["timeout_ms"] â†’ int milliseconds (default 5000)
//   - step.As                  â†’ result slot name (required)
func (c *Compiler) compileMCPListTools(step StepConfig) error {
	alias := step.Input["server"]
	if alias == "" {
		return fmt.Errorf("mcp_list_tools: input.server is required")
	}

	serverCfg, err := c.findMCPServer(alias)
	if err != nil {
		return fmt.Errorf("mcp_list_tools: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("mcp_list_tools: result slot: %w", err)
	}

	mode := steps.MCPLoadMode(step.Input["mode"])
	if mode == "" {
		mode = steps.MCPLoadFull
	}

	timeoutMs := 5000
	if t := step.Input["timeout_ms"]; t != "" {
		if n, convErr := strconv.Atoi(t); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	cfg := steps.MCPListToolsConfig{
		ServerConfig: serverCfg,
		APIKey:       serverCfg.APIKeyRef,
		Mode:         mode,
		ResultSlot:   resultSlot,
		TimeoutMs:    timeoutMs,
	}
	c.GlobalTable = append(c.GlobalTable, steps.MCPListTools(cfg))
	return nil
}

// compileMCPFetchSchemas resolves and appends the mcp_fetch_schemas instruction.
//
// Step fields:
//   - step.Input["server"]     â†’ MCP server alias (required)
//   - step.Input["timeout_ms"] â†’ int milliseconds (default 5000)
//   - step.KeyIdentifier       â†’ slot name holding the JSON array of selected tool names
//   - step.As                  â†’ result slot name (output: full ToolDefinition array)
func (c *Compiler) compileMCPFetchSchemas(step StepConfig) error {
	alias := step.Input["server"]
	if alias == "" {
		return fmt.Errorf("mcp_fetch_schemas: input.server is required")
	}

	serverCfg, err := c.findMCPServer(alias)
	if err != nil {
		return fmt.Errorf("mcp_fetch_schemas: %w", err)
	}

	selectedSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("mcp_fetch_schemas: selected slot: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("mcp_fetch_schemas: result slot: %w", err)
	}

	timeoutMs := 5000
	if t := step.Input["timeout_ms"]; t != "" {
		if n, convErr := strconv.Atoi(t); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	cfg := steps.MCPFetchSchemasConfig{
		ServerConfig: serverCfg,
		APIKey:       serverCfg.APIKeyRef,
		SelectedSlot: selectedSlot,
		ResultSlot:   resultSlot,
		TimeoutMs:    timeoutMs,
	}
	c.GlobalTable = append(c.GlobalTable, steps.MCPFetchSchemas(cfg))
	return nil
}

// findMCPServer looks up a registered MCP server by alias in the LLM config.
func (c *Compiler) findMCPServer(alias string) (config.MCPServerConfig, error) {
	for _, s := range c.LLMCfg.MCPServers {
		if s.Alias == alias {
			return s, nil
		}
	}
	return config.MCPServerConfig{}, fmt.Errorf("MCP server %q not found in llm.mcp_servers", alias)
}

// compileMCPToolCall resolves and appends the call_mcp_tool instruction.
//
// Step fields:
//   - step.Input["server"]     â†’ MCP server alias (required)
//   - step.Input["tool"]       â†’ tool name to call (required, baked at compile time)
//   - step.Input["timeout_ms"] â†’ int milliseconds (default 10000)
//   - step.KeyIdentifier       â†’ slot name holding the JSON arguments object (optional)
//   - step.As                  â†’ result slot name (required)
func (c *Compiler) compileMCPToolCall(step StepConfig) error {
	// Resolve server alias
	alias := step.Input["server"]
	if alias == "" {
		return fmt.Errorf("call_mcp_tool: input.server is required")
	}

	serverCfg, err := c.findMCPServer(alias)
	if err != nil {
		return fmt.Errorf("call_mcp_tool: %w", err)
	}

	// Resolve tool name (baked at compile time)
	toolName := step.Input["tool"]
	if toolName == "" {
		return fmt.Errorf("call_mcp_tool: input.tool is required")
	}

	// Resolve input slot (optional)
	inputSlot := -1
	if inputSlotName := step.KeyIdentifier; inputSlotName != "" {
		s, slotErr := c.getSlot(inputSlotName)
		if slotErr != nil {
			return fmt.Errorf("call_mcp_tool: input slot: %w", slotErr)
		}
		inputSlot = s
	}

	// Resolve result slot
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("call_mcp_tool: result slot: %w", err)
	}

	// Resolve API key from server config
	apiKey := serverCfg.APIKeyRef

	// Resolve timeout
	timeoutMs := 10_000
	if t := step.Input["timeout_ms"]; t != "" {
		if n, convErr := strconv.Atoi(t); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	cfg := steps.MCPCallConfig{
		Server:     serverCfg,
		APIKey:     apiKey,
		ToolName:   toolName,
		InputSlot:  inputSlot,
		ResultSlot: resultSlot,
		TimeoutMs:  timeoutMs,
	}
	c.GlobalTable = append(c.GlobalTable, steps.MCPToolCall(cfg))
	return nil
}
