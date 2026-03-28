package control

// ContextFitStepDescriptors returns the StepDescriptor for the check_context_fit step.
// Merged into AllStepDescriptors via the append block at the bottom of step_descriptors.go.
func ContextFitStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:       "check_context_fit",
			Title:      "Check Context Fit",
			Category:   "ai",
			Capability: "routing",
			Description: "Estimate total token usage (prompt + system + history + tools) and compare against " +
				"the target model's context limit. Writes whether the request fits and how many tokens overflow. " +
				"Never stops the flow — use the fits slot in a following if/switch step to gate routing decisions " +
				"without making any LLM call.",
			Defaults: map[string]string{
				"key_identifier": "var.prompt",
				"as":             "context_fits",
				"input": `{"model":"claude-sonnet-4-6","system_slot":"var.system","history_slot":"var.history",` +
					`"overflow_slot":"ctx_overflow","total_slot":"ctx_total_tokens"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Prompt slot", "Slot containing the user message text or a JSON []CanonicalMessage array", "var.prompt"),
				sf("as", "Fits slot (bool)", "BoolSlots slot name written true when total tokens <= budget, false otherwise", "context_fits"),
				sf("input", "Config (JSON)", `Keys: model (catalog slug for context limits), system_slot, history_slot, tools_slot, overflow_slot, total_slot, max_output_tokens`,
					`{"model":"claude-sonnet-4-6","overflow_slot":"ctx_overflow","total_slot":"ctx_total_tokens"}`),
			},
		},
	}
}
