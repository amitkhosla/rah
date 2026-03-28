package control

// SanitizeStepDescriptors returns the step descriptors for the LLM prompt
// sanitization group: estimate_tokens, sanitize_prompt, compress_prompt.
//
// These are merged into the Studio palette automatically by AllStepDescriptors
// when callers append the result of this function.
func SanitizeStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "estimate_tokens",
			Title:       "Estimate Tokens",
			Description: "Estimate the token count of a prompt slot using the 1 token ≈ 4 characters heuristic and store the integer result in an int slot. Useful for conditional branching before llm_call or sanitize_prompt.",
			Category:    "ai",
			Capability:  "token-accounting",
			Defaults: map[string]string{
				"key_identifier": "var.prompt",
				"as":             "token_count",
			},
			Fields: []StepField{
				sf("key_identifier", "Prompt slot", "Slot containing the text whose tokens you want to estimate", "var.prompt"),
				sf("as", "Store as (int slot)", "Int slot name to write the estimated token count into", "token_count"),
			},
		},
		{
			Type:        "sanitize_prompt",
			Title:       "Sanitize Prompt",
			Description: "Scan and sanitize a prompt slot for PII, prompt-injection patterns, and/or token-count limits. On violation: reject (4xx + stop), strip (redact/remove), or flag (set a bool slot and continue).",
			Category:    "ai",
			Capability:  "safety",
			Defaults: map[string]string{
				"key_identifier": "var.prompt",
				"input":          `{"rules":"pii,injection","on_violation":"reject"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Prompt slot", "Slot whose value is scanned and modified in-place", "var.prompt"),
				sf("input", "Config (JSON)", `Keys: rules (comma-separated: "pii", "injection", "max_tokens:N"), on_violation ("reject"|"strip"|"flag"), flag_slot (slot name for flag mode)`, `{"rules":"pii,injection","on_violation":"reject"}`),
			},
		},
		{
			Type:        "compress_prompt",
			Title:       "Compress Prompt",
			Description: "If the prompt slot exceeds target_tokens, call an LLM to produce a concise summary and replace the slot value in-place. Skips the LLM call when already under the limit. Returns 413 when the compressed result still exceeds the limit and on_exceed is \"reject\".",
			Category:    "ai",
			Capability:  "token-management",
			Defaults: map[string]string{
				"key_identifier": "var.prompt",
				"input":          `{"model":"claude-sonnet-4-6","target_tokens":"2000","timeout_ms":"30000","on_exceed":"reject"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Prompt slot", "Slot whose value is compressed in-place when over the token limit", "var.prompt"),
				sf("input", "Config (JSON)", `Keys: model (catalog slug), target_tokens (default 2000), timeout_ms (default 30000), on_exceed ("reject"), api_key (overrides catalog)`, `{"model":"claude-sonnet-4-6","target_tokens":"2000"}`),
			},
		},
	}
}
