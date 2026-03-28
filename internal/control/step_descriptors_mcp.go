package control

// MCPStepDescriptors returns the StepDescriptors for all MCP integration steps.
// These are appended to AllStepDescriptors so the Studio palette includes them.
func MCPStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "mcp_list_tools",
			Title:       "MCP List Tools",
			Category:    "ai",
			Capability:  "mcp",
			Description: "Fetch available tool names/schemas from a configured MCP server. Use mode 'names' for routing decisions, 'full' for passing to llm_call.",
			Defaults: map[string]string{
				"as":    "available_tools",
				"input": `{"server":"tools_server","mode":"full"}`,
			},
			Fields: []StepField{
				sf("as", "Store as", "Slot to write the tool list into", "available_tools"),
				sf("input", "Config (JSON)", `Keys: server (MCP server alias), mode (names/brief/full), timeout_ms`, `{"server":"tools_server","mode":"full"}`),
			},
		},
		{
			Type:        "mcp_fetch_schemas",
			Title:       "MCP Fetch Schemas",
			Category:    "ai",
			Capability:  "mcp",
			Description: "Fetch full schemas for a selected subset of tools from an MCP server. Pass selected tool names as a JSON array in the key_identifier slot.",
			Defaults: map[string]string{
				"key_identifier": "selected_tools",
				"as":             "tool_schemas",
				"input":          `{"server":"tools_server"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Selected tools slot", "Slot with JSON array of tool names to fetch schemas for", "selected_tools"),
				sf("as", "Store as", "Slot to write full tool schemas into", "tool_schemas"),
				sf("input", "Config (JSON)", `Keys: server (MCP server alias), timeout_ms`, `{"server":"tools_server"}`),
			},
		},
	}
}
