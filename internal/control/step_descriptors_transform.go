package control

// TransformStepDescriptors returns step descriptors for the transform_messages step.
// These are merged into the full palette via AllStepDescriptors.
func TransformStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "transform_messages",
			Title:       "Transform Messages",
			Category:    "ai",
			Capability:  "format",
			Description: "Apply structural wire-format transformations and history truncation to canonical messages when routing between LLM providers. Pure Go — no LLM calls. Each transform is independently togglable.",
			Defaults: map[string]string{
				"key_identifier": "canonical_messages",
				"input":          `{"target_format":"openai","adapt_roles":"true","extract_system":"true","inject_system":"true"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "History slot", "Slot containing the JSON-encoded []CanonicalMessage to transform (in-place)", "canonical_messages"),
				sf("input", "Options (JSON)", `Keys: target_format (required: "anthropic"/"openai"/"gemini"), source_format (optional static), format_slot (slot holding detected format), system_slot (slot for system prompt), thinking_slot (slot for extracted thinking), strip_thinking (true/false), extract_thinking (true/false), extract_system (true/false), inject_system (true/false), flatten_content (true/false), adapt_roles (true/false), normalize_tools (true/false), max_messages (int: hard cap on turn count; 0=no cap), max_tokens (int: token budget; 0=no cap), remove_orphans (true/false: remove tool_result blocks with no preceding tool_use), ensure_start_user (true/false: drop leading non-user turns after truncation)`, `{"target_format":"openai","adapt_roles":"true","max_messages":"50","remove_orphans":"true"}`),
			},
		},
	}
}
