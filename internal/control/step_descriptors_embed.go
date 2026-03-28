package control

// EmbedStepDescriptors returns StepDescriptors for the embed_text step.
// These are merged into the full palette via AllStepDescriptors.
func EmbedStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "embed_text",
			Title:       "Embed Text",
			Category:    "ai",
			Capability:  "embeddings",
			Description: "Calls an embedding API (OpenAI, Ollama, Gemini) to convert text in a slot into a float vector. The vector is written as a JSON array to the result slot, enabling semantic search and RAG pipelines.",
			Fields: []StepField{
				sf("key_identifier", "Input text slot", "Slot containing the text to embed", "var.query"),
				sf("as", "Vector output slot", "Slot to write the embedding vector JSON array", "var.query_vector"),
				sf("input", "Config (JSON)", `{"provider":"openai","model":"text-embedding-3-small","api_key_ref":"secret:openai_key","dim_slot":"0"}`, ""),
			},
		},
	}
}
