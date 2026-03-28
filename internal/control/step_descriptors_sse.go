package control

// SSEStepDescriptors returns step descriptors for SSE streaming steps.
func SSEStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "send_sse_event",
			Title:       "Send SSE Event",
			Category:    "streaming",
			Description: "Write a Server-Sent Events frame to the response stream and flush immediately. Use inside a foreach or while loop for streaming responses.",
			Fields: []StepField{
				sf("key_identifier", "Data slot", "Slot containing the event data payload", "var.chunk"),
				sf("input", "Options (JSON)", `{"event":"var.event_name","id_slot":"var.event_id"}`, ""),
			},
		},
	}
}
