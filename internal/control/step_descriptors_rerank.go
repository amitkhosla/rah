package control

// RerankStepDescriptors returns the step descriptors for the rerank instruction.
func RerankStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "rerank",
			Title:       "Rerank Results",
			Category:    "ai",
			Description: "Rerank a list of documents by relevance to a query using Cohere or Jina reranking APIs. Input is JSON []string, output is reranked JSON []string.",
			Defaults: map[string]string{
				"key_identifier": "var.query",
				"as":             "var.reranked",
				"input":          `{"provider":"cohere","model":"rerank-english-v3.0","docs":"var.docs","api_key":"","top_n":"0","timeout_ms":"10000"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Query slot", "Slot containing the query text", "var.query"),
				sf("as", "Result slot", "Slot to write reranked document texts (JSON []string)", "var.reranked"),
				sf("input", "Config (JSON)", `Keys: provider ("cohere" or "jina"), model (e.g. "rerank-english-v3.0"), docs (slot name for input documents), api_key (provider API key), api_key_ref (secret reference, optional), top_n (max results, 0=all), timeout_ms (default 10000), base_url (override, optional)`, `{"provider":"cohere","model":"rerank-english-v3.0","docs":"var.docs","api_key":"","top_n":"5","timeout_ms":"10000"}`),
			},
		},
	}
}
