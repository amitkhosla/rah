package control

// MCPCallToolDescriptors returns the StepDescriptor for the mcp_call_tool step.
// This is appended to AllStepDescriptors so the Studio palette includes it.
func MCPCallToolDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "mcp_call_tool",
			Title:       "MCP Call Tool",
			Category:    "ai",
			Capability:  "mcp",
			Description: "Call a specific tool on an MCP server and write the result content array (JSON) to a slot. The tool name is read at runtime from a slot. Retries on 429/5xx up to 2 times.",
			Defaults: map[string]string{
				"key_identifier": "selected_tool",
				"as":             "tool_result",
				"input":          `{"server":"tools_server","args_slot":"tool_args"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Tool name slot", "Slot containing the name of the MCP tool to invoke", "selected_tool"),
				sf("as", "Store as", "Slot to write the tool result content array (JSON) into", "tool_result"),
				sf("input", "Config (JSON)", `Keys: server (MCP server alias), args_slot (slot with JSON args object), timeout_ms`, `{"server":"tools_server","args_slot":"tool_args"}`),
			},
		},
	}
}
