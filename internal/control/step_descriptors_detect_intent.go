package control

// DetectIntentStepDescriptors returns step descriptors for the detect_intent step.
func DetectIntentStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "detect_intent",
			Title:       "Detect Intent",
			Category:    "ai",
			Capability:  "identity",
			Description: "Tag the request with an intent label based on header patterns (e.g. identify Claude CLI).",
			Defaults: map[string]string{
				"as": "var.intent",
				"input": `{"header:User-Agent":"claude-code/* -> claude-cli"}`,
			},
			Fields: []StepField{
				sf("as", "Store as", "Slot name to write the detected intent tag into", "var.intent"),
				sf("input", "Pattern Rules (JSON)", `Keys starting with "header:" mapped to "pattern -> tag"`, `{"header:User-Agent":"claude-code/* -> claude-cli"}`),
			},
		},
	}
}
