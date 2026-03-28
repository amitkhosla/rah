package control

// HistoryStepDescriptors returns step descriptors for multi-turn conversation
// history management steps.
func HistoryStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "append_message",
			Title:       "Append Message",
			Category:    "ai",
			Capability:  "history",
			Description: "Append a user or assistant message to a conversation history slot (JSON-encoded canonical messages). Optionally trims to a max turn pair count.",
			Fields: []StepField{
				sf("key_identifier", "History slot", "Slot holding the JSON conversation history", "var.history"),
				sf("as", "Content slot", "Slot holding the message text to append", "var.user_message"),
				sf("input", "Config (JSON)", `{"role":"user","max_turns":"20"}`, ""),
			},
		},
		{
			Type:        "trim_history",
			Title:       "Trim History",
			Category:    "ai",
			Capability:  "history",
			Description: "Trim a conversation history slot to a max number of turn pairs and/or a token budget. Oldest messages are removed first.",
			Fields: []StepField{
				sf("key_identifier", "History slot", "Slot holding the JSON conversation history", "var.history"),
				sf("input", "Config (JSON)", `{"max_turns":"20","max_tokens":"4000"}`, ""),
			},
		},
		{
			Type:        "load_history",
			Title:       "Load History",
			Category:    "ai",
			Capability:  "history",
			Description: "Load conversation history from a datastore domain into a slot. Writes [] if the key is not found.",
			Fields: []StepField{
				sf("key_identifier", "History slot", "Slot to write the loaded history into", "var.history"),
				sf("as", "Key slot", "Slot holding the conversation key (e.g. session ID)", "var.session_id"),
				sf("input", "Config (JSON)", `{"domain":"conversations"}`, ""),
			},
		},
		{
			Type:        "save_history",
			Title:       "Save History",
			Category:    "ai",
			Capability:  "history",
			Description: "Persist a conversation history slot to a datastore domain. Supports optional TTL.",
			Fields: []StepField{
				sf("key_identifier", "History slot", "Slot holding the JSON conversation history to save", "var.history"),
				sf("as", "Key slot", "Slot holding the conversation key (e.g. session ID)", "var.session_id"),
				sf("input", "Config (JSON)", `{"domain":"conversations","ttl_secs":"3600"}`, ""),
			},
		},
	}
}
