package control

// A2AStepDescriptors returns StepDescriptor entries for A2A protocol steps.
func A2AStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "a2a_call",
			Title:       "A2A Call",
			Description: "Call a remote Agent-to-Agent (A2A) agent via JSON-RPC 2.0 tasks/send. Writes the result JSON into output_var. Fail-open: on remote error sets output_var to null and logs a warning.",
			Category:    "agent",
			Capability:  "a2a",
			Defaults:    map[string]string{"timeout_sec": "30"},
			Fields: []StepField{
				sf("url", "Agent URL", "Base URL of the remote A2A agent", "https://agent.example.com"),
				sf("skill_id", "Skill ID", "Optional skill ID hint to send with the request", ""),
				sf("input_var", "Input variable", "Slot name holding the JSON message to send", "agent_input"),
				sf("output_var", "Output variable", "Slot name to write the result JSON into", "agent_output"),
				sf("timeout_sec", "Timeout (seconds)", "HTTP timeout for the remote call (default 30)", "30"),
			},
		},
	}
}
