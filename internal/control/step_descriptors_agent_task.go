package control

// AgentTaskStepDescriptors returns StepDescriptor entries for all agent task steps.
func AgentTaskStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "task_create",
			Title:       "Task Create",
			Description: "Create a new agent task with pending status. Generates a UUID for the task ID and writes it into output_var.",
			Category:    "agent",
			Capability:  "tasks",
			Defaults:    map[string]string{"tenant_alias": "", "agent_name": "", "output_var": "task_id"},
			Fields: []StepField{
				sf("tenant_alias", "Tenant alias", "Tenant identifier for task isolation", "acme"),
				sf("agent_name", "Agent name", "Logical agent identifier", "my-agent"),
				sf("session_id", "Session ID", "Optional session identifier; supports slot variable references", "var.session_id"),
				sf("output_var", "Output variable", "Slot name to write the generated task ID into", "task_id"),
			},
		},
		{
			Type:        "task_update",
			Title:       "Task Update",
			Description: "Update the status of an existing agent task.",
			Category:    "agent",
			Capability:  "tasks",
			Defaults:    map[string]string{"tenant_alias": "", "status": "done"},
			Fields: []StepField{
				sf("tenant_alias", "Tenant alias", "Tenant identifier for task isolation", "acme"),
				sf("task_id", "Task ID", "Task identifier; supports slot variable references", "var.task_id"),
				sf("status", "Status", "New task status: pending | running | done | failed", "done"),
			},
		},
		{
			Type:        "task_get",
			Title:       "Task Get",
			Description: "Read a task record by ID and write the JSON into output_var.",
			Category:    "agent",
			Capability:  "tasks",
			Defaults:    map[string]string{"tenant_alias": "", "output_var": "task_data"},
			Fields: []StepField{
				sf("tenant_alias", "Tenant alias", "Tenant identifier for task isolation", "acme"),
				sf("task_id", "Task ID", "Task identifier; supports slot variable references", "var.task_id"),
				sf("output_var", "Output variable", "Slot name to write the task JSON into", "task_data"),
			},
		},
	}
}
