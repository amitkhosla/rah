package control

// CostStepDescriptors returns step descriptors for the cost accounting group:
// calculate_cost, enforce_cost_budget, record_cost.
//
// These steps form the full cost lifecycle in a flow:
//
//	estimate_tokens  →  calculate_cost  →  enforce_cost_budget  →  llm_call  →  record_cost
func CostStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "calculate_cost",
			Title:       "Calculate Cost",
			Description: "Compute the USD cost for an LLM call from input/output token counts and the pricing catalog. Stores the result as a fixed-point int64 (value = USD × 1e9) for downstream enforce_cost_budget or record_cost steps. Zero-cost fallback when the model is not in the catalog.",
			Category:    "cost",
			Capability:  "cost-accounting",
			Defaults: map[string]string{
				"key_identifier":    "var.cost",
				"input_tokens_slot": "var.input_tokens",
				"output_tokens_slot": "var.output_tokens",
				"model_slot":        "var.model",
			},
			Fields: []StepField{
				sf("key_identifier", "Cost slot (int)", "IntSlot to write the result into. Value = USD cost × 1e9.", "var.cost"),
				sf("input_tokens_slot", "Input tokens slot", "IntSlot holding the input token count (from estimate_tokens or llm_call response)", "var.input_tokens"),
				sf("output_tokens_slot", "Output tokens slot", "IntSlot holding the output token count (from llm_call response)", "var.output_tokens"),
				sf("model_slot", "Model slot (optional)", "ByteSlot holding the model ID string used for pricing lookup (e.g. 'gpt-4o', 'claude-sonnet'). Leave blank to use zero-cost.", "var.model"),
			},
		},
		{
			Type:        "enforce_cost_budget",
			Title:       "Enforce Cost Budget",
			Description: "Pre-call budget gate: reads the estimated cost from an int slot and checks it against the tenant's quota. Returns HTTP 429 if the budget is exceeded, stopping flow execution. No-op when no quota is configured for the tenant.",
			Category:    "cost",
			Capability:  "cost-enforcement",
			Defaults: map[string]string{
				"key_identifier": "var.cost",
			},
			Fields: []StepField{
				sf("key_identifier", "Cost slot (int)", "IntSlot containing the estimated cost (value = USD × 1e9, written by calculate_cost)", "var.cost"),
			},
		},
		{
			Type:        "record_cost",
			Title:       "Record Cost",
			Description: "Post-call cost recorder: reads the actual cost from an int slot, updates the tenant's in-memory quota, and emits a cost event to the ingest pipeline (deferred, after the response is sent). Best-effort — never fails the request.",
			Category:    "cost",
			Capability:  "cost-accounting",
			Defaults: map[string]string{
				"key_identifier": "var.cost",
				"model_slot":     "var.model",
			},
			Fields: []StepField{
				sf("key_identifier", "Cost slot (int)", "IntSlot containing the actual cost (value = USD × 1e9)", "var.cost"),
				sf("model_slot", "Model slot (optional)", "ByteSlot with model ID string — stamped on the ingest cost event for analytics", "var.model"),
			},
		},
	}
}
