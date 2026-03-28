package control

// ChunkStepDescriptors returns StepDescriptors for the chunk_text step.
// These are merged into the full palette via AllStepDescriptors.
func ChunkStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "chunk_text",
			Title:       "Chunk Text",
			Category:    "ai",
			Capability:  "rag",
			Description: "Splits text into overlapping chunks for RAG ingestion. Feed each chunk to embed_text then vector_upsert.",
			Defaults: map[string]string{
				"key_identifier": "var.document",
				"as":             "var.chunks",
				"input":          `{"chunk_size":"512","overlap":"64","count_slot":"-1"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Input text slot", "Slot with text to chunk", "var.document"),
				sf("as", "Chunks output slot", "Slot for JSON string array of chunks", "var.chunks"),
				sf("input", "Config (JSON)", `{"chunk_size":"512","overlap":"64","count_slot":"-1"}`, ""),
			},
		},
	}
}
