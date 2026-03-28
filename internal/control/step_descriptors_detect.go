package control

// DetectStepDescriptors returns StepDescriptors for the detect_message_format step.
// These are merged into the full palette via AllStepDescriptors.
func DetectStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "detect_message_format",
			Title:       "Detect Message Format",
			Category:    "ai",
			Capability:  "format",
			Description: "Read a raw JSON request body and auto-detect which LLM wire format it uses (Anthropic / OpenAI / Gemini). Writes the format name string (\"anthropic\", \"openai\", \"gemini\", or \"unknown\") to a slot so downstream steps can branch without the operator hardcoding the format.",
			Defaults: map[string]string{
				"key_identifier": "var.request_body",
				"as":             "detected_format",
			},
			Fields: []StepField{
				sf("key_identifier", "Body slot", "Slot containing the raw JSON request body to inspect", "var.request_body"),
				sf("as", "Format slot", "Slot to write the detected format string into (\"anthropic\", \"openai\", \"gemini\", or \"unknown\")", "detected_format"),
			},
		},
	}
}
