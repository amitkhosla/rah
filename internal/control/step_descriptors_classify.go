package control

// ClassifyStepDescriptors returns step descriptors for the classify_llm step.
func ClassifyStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "classify_llm",
			Title:       "Classify LLM Request",
			Category:    "ai",
			Capability:  "inference",
			Description: "Call a small/cheap LLM to classify an incoming request and store results in metadata slots.",
			Defaults: map[string]string{
				"key_identifier": "var.prompt",
				"as":             "raw_classification",
				"input":          `{"model":"gemini-flash","complexity":"complexity_slot","intent":"intent_slot"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Prompt slot", "Slot containing the classification prompt", "var.prompt"),
				sf("as", "Raw result slot", "Slot to write the raw JSON response into", "raw_classification"),
				sf("input", "Mapping (JSON)", `Keys: model (classifier model), others are JSON key -> Slot mapping`, `{"model":"gemini-flash","complexity":"var.complexity"}`),
			},
		},
	}
}
