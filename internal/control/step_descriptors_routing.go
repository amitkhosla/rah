package control

// RoutingStepDescriptors returns the StepDescriptors for routing steps.
// These are merged into AllStepDescriptors via BuildStepCatalog.
func RoutingStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:       "route_llm",
			Title:      "Route LLM",
			Category:   "ai",
			Capability: "routing",
			Description: "Evaluate routing rules to select a model slug at runtime. " +
				"Writes the selected model slug to the result slot. " +
				"Use before an llm_call step that references the same slot via model_slot. " +
				"Rules are evaluated in order; first match wins. " +
				"Supported conditions: token_count > N, meta == VALUE, true (catch-all).",
			Defaults: map[string]string{
				"as":      "selected_model",
				"default": "claude-haiku-4-5",
				"rules":   `[{"condition":"token_count > 8000","model":"gemini-pro"},{"condition":"true","model":"claude-haiku-4-5"}]`,
			},
			Fields: []StepField{
				sf("as", "Result slot", "Slot name to write the selected model slug into; consumed by a following llm_call via model_slot", "selected_model"),
				sf("rules", "Routing rules (JSON)", `Array of {condition, model} objects evaluated in order. Conditions: "true", "token_count > N", "meta == VALUE"`, `[{"condition":"token_count > 8000","model":"gemini-pro"},{"condition":"true","model":"claude-haiku-4-5"}]`),
				sf("default", "Default model", "Model slug used when no rule matches (fallback)", "claude-haiku-4-5"),
				sf("token_slot", "Token count slot", "Slot name (IntSlots) holding an estimated token count for token_count conditions; leave empty if not used", ""),
				sf("meta_slot", "Meta value slot", "Slot name (ByteSlots) holding a tenant metadata string for meta == conditions; leave empty if not used", ""),
			},
		},
	}
}
