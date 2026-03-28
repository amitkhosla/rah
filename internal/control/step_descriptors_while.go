package control

// WhileStepDescriptors returns the step palette entry for the while loop.
func WhileStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:       "while",
			Title:      "While Loop",
			Category:   "logic",
			Capability: "control",
			Description: "Repeats a sub-flow while a BoolSlot condition is true. " +
				"Useful for agentic tool-call loops: set the condition slot true when tool calls are present, " +
				"and the loop re-invokes the LLM until no more tool calls are returned. " +
				"A safety limit (default 100) prevents infinite loops.",
			Fields: []StepField{
				sf("source", "Condition slot", "BoolSlot name that controls the loop (loop runs while true)", "var.has_tool_calls"),
				sf("input", "Options (JSON)", `{"max_iter":"10"}`, ""),
			},
		},
	}
}
