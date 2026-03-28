package control

// ExecutePlanStepDescriptors returns the StepDescriptor for the execute_plan step.
// This is appended to AllStepDescriptors so the Studio palette includes it.
func ExecutePlanStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "execute_plan",
			Title:       "Execute Plan (DAG)",
			Category:    "ai",
			Capability:  "orchestration",
			Description: "Takes a JSON execution plan produced by an LLM (with steps, dependencies, and tool names) and executes it as a DAG. Steps run in dependency order; fan-out occurs when a parent step returns a list. Avoids repeated LLM calls — one LLM call produces the full plan, then all tools execute directly.",
			Fields: []StepField{
				sf("key_identifier", "Plan slot", "Slot containing the LLM-generated JSON execution plan", "var.llm_plan"),
				sf("as", "Results slot", "Slot to write execution results map {step_id: result}", "var.plan_results"),
				sf("input", "Config (JSON)", `{"mcp_server":"default","timeout_ms":"5000","skip_new_tool_required":"true","error_slot":"var.plan_error"}`, ""),
			},
		},
	}
}
