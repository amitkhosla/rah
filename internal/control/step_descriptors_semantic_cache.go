package control

// SemanticCacheStepDescriptors returns step descriptors for semantic cache operations.
func SemanticCacheStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "semantic_cache_get",
			Title:       "Semantic Cache Get",
			Category:    "ai",
			Description: "Look up a semantically similar query in the vector store. Sets hit_slot=true and writes cached response if similarity >= min_score.",
			Fields: []StepField{
				sf("key_identifier", "Query slot", "Slot containing the query text to look up", "var.query"),
				sf("as", "Result slot", "Slot to write the cached response on hit", "var.cached_response"),
				sf("input", "Config (JSON)", `{"store":"my_store","collection":"semantic_cache","hit_slot":"var.cache_hit","min_score":"0.92","provider":"openai","model":"text-embedding-3-small","api_key_ref":"secret:openai_key"}`, ""),
			},
		},
		{
			Type:        "semantic_cache_put",
			Title:       "Semantic Cache Put",
			Category:    "ai",
			Description: "Store a query-response pair in the vector store for future semantic cache lookups.",
			Fields: []StepField{
				sf("key_identifier", "Query slot", "Slot containing the query text (will be embedded as cache key)", "var.query"),
				sf("input", "Config (JSON)", `{"store":"my_store","collection":"semantic_cache","response":"var.llm_response","provider":"openai","model":"text-embedding-3-small","api_key_ref":"secret:openai_key"}`, ""),
			},
		},
	}
}
