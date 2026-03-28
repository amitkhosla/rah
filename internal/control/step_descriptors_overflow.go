package control

// OverflowStepDescriptors returns step descriptors for sliding-window overflow
// history management steps.
func OverflowStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "overflow_history",
			Title:       "Overflow History",
			Category:    "ai",
			Capability:  "history",
			Description: "Move the oldest turns that exceed the configured token/turn budget into persistent datastore overflow storage. Keeps only the fitting tail in the history slot. Use load_overflow_history to retrieve archived turns.",
			Fields: []StepField{
				sf("key_identifier", "History slot", "Slot holding the JSON conversation history (trimmed in place)", "var.history"),
				sf("as", "Key slot", "Slot holding the conversation key (e.g. session ID)", "var.session_id"),
				sf("input", "Config (JSON)", `{"domain":"conversations","overflow_slot":"var.overflow_tokens","max_tokens":"4000","max_turns":"20","ttl_secs":"86400"}`, ""),
			},
		},
		{
			Type:        "load_overflow_history",
			Title:       "Load Overflow History",
			Category:    "ai",
			Capability:  "history",
			Description: "Retrieve overflow history from persistent storage and prepend it to the current history slot. Optionally limit how many overflow turn-pairs are restored.",
			Fields: []StepField{
				sf("key_identifier", "History slot", "Slot holding the current JSON conversation history — overflow is prepended", "var.history"),
				sf("as", "Key slot", "Slot holding the conversation key (e.g. session ID)", "var.session_id"),
				sf("input", "Config (JSON)", `{"domain":"conversations","max_turns":"10"}`, ""),
			},
		},
	}
}
