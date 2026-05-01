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
			Description: "Parse an incoming LLM request body (Anthropic/OpenAI/Gemini JSON) into canonical message slots. Extracts tools, tool_choice, and stream flag alongside messages. Use in APIs that accept provider-specific payloads.",
			Defaults: map[string]string{
				"key_identifier": "var.request_body",
				"as":             "canonical_messages",
				"input":          `{"system_slot":"system_prompt","detected_fmt_slot":"detected_format","tools_slot":"var.tools","tool_choice_slot":"var.tool_choice","stream_slot":"var.stream"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Body slot", "Slot containing the raw JSON request body to parse", "var.request_body"),
				sf("as", "Messages slot", "Slot to write the JSON-encoded canonical messages array into", "canonical_messages"),
				sf("input", "Options (JSON)", `Keys: system_slot (slot for system prompt), detected_fmt_slot (slot for detected format: "anthropic"/"openai"/"gemini"), tools_slot (slot for JSON []ToolDefinition extracted from request), tool_choice_slot (slot for JSON ToolChoice), stream_slot (slot for "true"/"false" stream flag)`, `{"system_slot":"system_prompt","detected_fmt_slot":"detected_format","tools_slot":"var.tools"}`),
			},
		},
		{
			Type:        "format_response",
			Title:       "Format Response",
			Category:    "ai",
			Capability:  "format",
			Description: "Encode a canonical LLM response (text + tool_use blocks + thinking) into the caller's expected provider-specific JSON envelope (Anthropic/OpenAI/Gemini). Supports runtime format detection via format_slot.",
			Defaults: map[string]string{
				"key_identifier": "var.llm_content",
				"as":             "var.formatted_response",
				"input":          `{"format":"anthropic","model":"claude-sonnet-4-6","stop_reason_slot":"var.stop_reason","format_slot":"var.detected_format","input_tokens_slot":"0","output_tokens_slot":"1","tool_use_slot":"var.tool_use","thinking_slot":"var.thinking"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Content slot", "Slot containing the plain-text LLM response content", "var.llm_content"),
				sf("as", "Output slot", "Slot to write the formatted JSON response body into", "var.formatted_response"),
				sf("input", "Config (JSON)", `Keys: format ("anthropic"|"openai"|"gemini", bake-time default), format_slot (runtime format override from parse_message_format), model (bake-time model slug), model_slot (runtime model slug), stop_reason_slot (stop reason from llm_call), input_tokens_slot (IntSlot index), output_tokens_slot (IntSlot index), tool_use_slot (slot with JSON []ContentBlock of tool_use type — from llm_call tool_use_slot), thinking_slot (slot with thinking text — from llm_call thinking_out_slot), stream_slot (slot with "true"/"false" stream flag)`, `{"format":"anthropic","model":"claude-sonnet-4-6","tool_use_slot":"var.tool_use"}`),
			},
		},
	}
}
