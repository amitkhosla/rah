package control

// LLMKeyStepDescriptors returns step descriptors for LLM API key management steps.
func LLMKeyStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "load_llm_key",
			Title:       "Load LLM API Key",
			Category:    "ai",
			Capability:  "secrets",
			Description: "Fetches and decrypts a per-tenant LLM API key from the credential store into a slot, enabling per-tenant model access. Use before llm_call with api_key_slot pointing to the output slot.",
			Fields: []StepField{
				sf("key_identifier", "Credential name slot", "Slot containing the credential name to look up (e.g. a slot holding 'anthropic_key' or 'openai_key')", "var.cred_name"),
				sf("as", "Output slot", "Slot to write the decrypted plaintext API key into; reference this slot in a subsequent llm_call via api_key_slot", "var.api_key"),
			},
		},
	}
}
