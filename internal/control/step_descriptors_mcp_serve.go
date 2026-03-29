package control

// ServeMCPStepDescriptors returns the StepDescriptor for the serve_mcp action.
// Appended to AllStepDescriptors so the Studio palette includes it automatically.
func ServeMCPStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:     "serve_mcp",
			Title:    "Serve Virtual MCP",
			Category: "ai",
			Capability: "mcp",
			Description: "Handle MCP JSON-RPC 2.0 protocol for a named virtual MCP server. " +
				"Reads the server name from a slot or literal, dispatches tools/list and tools/call " +
				"across API tools and external MCP server tools. Always terminates the flow (StopPlan).",
			Defaults: map[string]string{
				"key_identifier": "var.body",
				"input":          `{"name":"my-tools","timeout_ms":"30000"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Body slot", "Slot containing the raw JSON-RPC request body", "var.body"),
				sf("input", "Config (JSON)", `Keys: name (literal virtual server name), name_slot (slot holding the name at runtime), timeout_ms`, `{"name":"my-tools","timeout_ms":"30000"}`),
			},
		},
	}
}
