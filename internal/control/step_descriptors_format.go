package control

// FormatStepDescriptors returns StepDescriptors for the AI message format group
// (parse_message_format and format_response). These are merged into the full
// palette via AllStepDescriptors.
func FormatStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "parse_message_format",
			Title:       "Parse Message Format",
			Category:    "ai",
			Capability:  "format",
			Description: "Parse an incoming LLM request body (Anthropic/OpenAI/Gemini JSON) into canonical message slots. Use in APIs that accept provider-specific payloads.",
			Defaults: map[string]string{
				"key_identifier": "var.request_body",
				"as":             "canonical_messages",
				"input":          `{"system_slot":"system_prompt","detected_fmt_slot":"detected_format"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Body slot", "Slot containing the raw JSON request body to parse", "var.request_body"),
				sf("as", "Messages slot", "Slot to write the JSON-encoded canonical messages array into", "canonical_messages"),
				sf("input", "Options (JSON)", `Keys: system_slot (slot name to write extracted system prompt into), detected_fmt_slot (slot name to write detected format string into — "anthropic", "openai", or "gemini")`, `{"system_slot":"system_prompt","detected_fmt_slot":"detected_format"}`),
			},
		},
		{
			Type:        "format_response",
			Title:       "Format Response",
			Category:    "ai",
			Capability:  "format",
			Description: "Encode a text response into a provider-specific JSON response structure (Anthropic/OpenAI/Gemini).",
			Defaults: map[string]string{
				"key_identifier": "var.response_text",
				"as":             "formatted_response",
				"input":          `{"format":"openai","model":"gpt-4"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Response text slot", "Slot containing the plain-text response to format", "var.response_text"),
				sf("as", "Output slot", "Slot to write the provider-specific JSON response into", "formatted_response"),
				sf("input", "Config (JSON)", `Keys: format (required — "anthropic", "openai", or "gemini"), model (optional — model name included in response)`, `{"format":"openai","model":"gpt-4"}`),
			},
		},
	}
}
