package control

// VectorStepDescriptors returns StepDescriptors for vector_search and vector_upsert.
// These are merged into the full palette via AllStepDescriptors.
func VectorStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "vector_search",
			Title:       "Vector Search",
			Category:    "ai",
			Capability:  "rag",
			Description: "Queries a vector store with a float embedding vector and returns the top-K most similar chunks. Use after embed_text to enable semantic search, RAG context retrieval, or semantic cache lookups.",
			Fields: []StepField{
				sf("key_identifier", "Vector slot", "Slot containing the query embedding (JSON float array from embed_text)", "var.query_vector"),
				sf("as", "Results slot", "Slot to write matched chunks as JSON []SearchResult", "var.search_results"),
				sf("input", "Config (JSON)", `{"store":"default","collection":"docs","top_k":"5","min_score":"0.7","count_slot":"0"}`, ""),
			},
		},
		{
			Type:        "vector_upsert",
			Title:       "Vector Upsert",
			Category:    "ai",
			Capability:  "rag",
			Description: "Inserts or updates a vector with its content and metadata in a vector store. Use during RAG ingestion: embed a chunk then upsert it.",
			Fields: []StepField{
				sf("key_identifier", "Vector slot", "Slot containing the embedding vector (JSON float array)", "var.chunk_vector"),
				sf("input", "Config (JSON)", `{"store":"default","collection":"docs","content_slot":"var.chunk_text","id_slot":"var.chunk_id","metadata_slot":"var.chunk_meta"}`, ""),
			},
		},
	}
}
