package control

func GuardrailStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "llm_call",
			Title:       "LLM Call with Guardrail",
			Category:    "ai",
			Capability:  "safety",
			Description: "Add content guardrails to an llm_call step via its input keys. Set guardrail.* fields on the llm_call step — this is not a separate step type. Supports regex, OpenAI Moderation, AWS Bedrock Guardrails, Google Model Armor, and webhooks.",
			Fields: []StepField{
				sf("guardrail.on_violation", "Default action", `"block" | "redact" | "flag"`, "block"),
				sf("guardrail.regex_rules", "Regex rules (JSON)", `[{"pattern":"<pattern>","action":"block","label":"name"}]`, ""),
				sf("guardrail.providers", "External providers (JSON)", `[{"provider":"openai_moderation","api_key":"env:KEY","action":"block","timeout_ms":3000}]`, ""),
				sf("guardrail.flag_slot", "Flag slot name", "Bool slot set true when action=flag", "guardrail_flagged"),
			},
		},
	}
}
