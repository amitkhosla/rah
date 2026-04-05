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
			Description: "Encode a canonical LLM text response into the caller's expected provider-specific JSON envelope (Anthropic/OpenAI/Gemini). Supports runtime format detection via format_slot.",
			Defaults: map[string]string{
				"key_identifier": "var.llm_content",
				"as":             "var.formatted_response",
				"input":          `{"format":"anthropic","model":"claude-sonnet-4-6","stop_reason_slot":"var.stop_reason","format_slot":"var.detected_format","input_tokens_slot":"0","output_tokens_slot":"1"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Content slot", "Slot containing the plain-text LLM response content", "var.llm_content"),
				sf("as", "Output slot", "Slot to write the formatted JSON response body into", "var.formatted_response"),
				sf("input", "Config (JSON)", `Keys: format ("anthropic"|"openai"|"gemini", bake-time default), format_slot (slot name for runtime format override — from parse_message_format), model (bake-time model slug), model_slot (slot for runtime model slug), stop_reason_slot (slot written by llm_call stop_reason_slot), input_tokens_slot (IntSlot index as string), output_tokens_slot (IntSlot index as string)`, `{"format":"anthropic","model":"claude-sonnet-4-6"}`),
			},
		},
	}
}
