package control

// AgentStateStepDescriptors returns StepDescriptor entries for all agent state steps.
func AgentStateStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "agent_state_get",
			Title:       "Agent State Get",
			Description: "Read agent session state from the KV store. Use key to fetch a single top-level field, or leave key empty to read the entire state object.",
			Category:    "agent",
			Capability:  "state",
			Defaults:    map[string]string{"tenant_alias": "", "agent_name": "", "session_id": "", "output_var": "agent_state"},
			Fields: []StepField{
				sf("tenant_alias", "Tenant alias", "Tenant identifier for state isolation", "acme"),
				sf("agent_name", "Agent name", "Logical agent identifier", "my-agent"),
				sf("session_id", "Session ID", "Session identifier; supports slot variable references", "var.session_id"),
				sf("key", "Key", "Top-level key to read; leave empty to read the whole state", ""),
				sf("output_var", "Output variable", "Slot name to write the result into", "agent_state"),
			},
		},
		{
			Type:        "agent_state_set",
			Title:       "Agent State Set",
			Description: "Write a value into agent session state. If key is set, only that field is updated; otherwise the whole state is replaced.",
			Category:    "agent",
			Capability:  "state",
			Defaults:    map[string]string{"tenant_alias": "", "agent_name": "", "session_id": ""},
			Fields: []StepField{
				sf("tenant_alias", "Tenant alias", "Tenant identifier for state isolation", "acme"),
				sf("agent_name", "Agent name", "Logical agent identifier", "my-agent"),
				sf("session_id", "Session ID", "Session identifier; supports slot variable references", "var.session_id"),
				sf("key", "Key", "Top-level key to set; leave empty to replace the whole state", ""),
				sf("value", "Value", "JSON value to store, or slot variable name holding the value", `{"status":"active"}`),
			},
		},
		{
			Type:        "agent_state_merge",
			Title:       "Agent State Merge",
			Description: "Shallow-merge top-level keys from a patch object into the stored agent session state.",
			Category:    "agent",
			Capability:  "state",
			Defaults:    map[string]string{"tenant_alias": "", "agent_name": "", "session_id": ""},
			Fields: []StepField{
				sf("tenant_alias", "Tenant alias", "Tenant identifier for state isolation", "acme"),
				sf("agent_name", "Agent name", "Logical agent identifier", "my-agent"),
				sf("session_id", "Session ID", "Session identifier; supports slot variable references", "var.session_id"),
				sf("value", "Patch value", "JSON object with keys to merge, or slot variable name holding the patch", "var.patch"),
			},
		},
		{
			Type:        "agent_state_clear",
			Title:       "Agent State Clear",
			Description: "Remove agent session state. If key is set, only that field is removed; otherwise the whole state record is deleted.",
			Category:    "agent",
			Capability:  "state",
			Defaults:    map[string]string{"tenant_alias": "", "agent_name": "", "session_id": ""},
			Fields: []StepField{
				sf("tenant_alias", "Tenant alias", "Tenant identifier for state isolation", "acme"),
				sf("agent_name", "Agent name", "Logical agent identifier", "my-agent"),
				sf("session_id", "Session ID", "Session identifier; supports slot variable references", "var.session_id"),
				sf("key", "Key", "Top-level key to remove; leave empty to delete the whole state", ""),
			},
		},
	}
}
