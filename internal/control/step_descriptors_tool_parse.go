package control

// ToolParseStepDescriptors returns StepDescriptors for the parse_tool_calls and
// append_tool_result steps.
func ToolParseStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "parse_tool_calls",
			Title:       "Parse Tool Calls",
			Category:    "ai",
			Capability:  "tools",
			Description: "Extracts tool call requests from an LLM response into a canonical slot. Supports Anthropic and OpenAI formats. For Anthropic, also updates the response slot to contain only the concatenated text blocks.",
			Fields: []StepField{
				sf("key_identifier", "Response slot", "Slot containing the LLM response content", "var.llm_response"),
				sf("as", "Tool calls slot", "Slot to write extracted tool calls JSON array", "var.tool_calls"),
				sf("input", "Config (JSON)", `{"count_slot":"var.tool_count","has_tool_calls_slot":"var.has_tools","source_format":"anthropic"}`, ""),
			},
		},
		{
			Type:        "append_tool_result",
			Title:       "Append Tool Result",
			Category:    "ai",
			Capability:  "tools",
			Description: "Appends a tool execution result back into the conversation history in the correct provider format.",
			Fields: []StepField{
				sf("key_identifier", "History slot", "Slot containing conversation history", "var.history"),
				sf("input", "Config (JSON)", `{"tool_call_id_slot":"var.tool_call_id","result_slot":"var.tool_result","target_format":"anthropic"}`, ""),
			},
		},
	}
}
