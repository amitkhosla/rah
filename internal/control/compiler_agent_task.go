package control

import (
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileTaskCreate compiles the task_create step.
//
// step.Input fields:
//
//	tenant_alias  — tenant identifier (required)
//	agent_name    — agent identifier (required)
//	session_id    — slot name or literal session ID
//	output_var    — slot name to write the generated task ID into
func (c *Compiler) compileTaskCreate(step StepConfig) error {
	cfg, err := buildAgentTaskConfig(c, step, false)
	if err != nil {
		return fmt.Errorf("task_create: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.TaskCreate(cfg))
	return nil
}

// compileTaskUpdate compiles the task_update step.
//
// step.Input fields:
//
//	tenant_alias  — tenant identifier (required)
//	agent_name    — agent identifier (optional, stored on the task)
//	task_id       — slot name or literal task ID (required)
//	status        — new status string (pending|running|done|failed)
func (c *Compiler) compileTaskUpdate(step StepConfig) error {
	cfg, err := buildAgentTaskConfig(c, step, false)
	if err != nil {
		return fmt.Errorf("task_update: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.TaskUpdate(cfg))
	return nil
}

// compileTaskGet compiles the task_get step.
//
// step.Input fields:
//
//	tenant_alias  — tenant identifier (required)
//	task_id       — slot name or literal task ID (required)
//	output_var    — slot name to write the task JSON into (required)
func (c *Compiler) compileTaskGet(step StepConfig) error {
	cfg, err := buildAgentTaskConfig(c, step, true)
	if err != nil {
		return fmt.Errorf("task_get: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.TaskGet(cfg))
	return nil
}

// buildAgentTaskConfig reads common task step fields and resolves slot indices.
func buildAgentTaskConfig(c *Compiler, step StepConfig, requireOutput bool) (steps.AgentTaskConfig, error) {
	tenantAlias := strings.TrimSpace(step.Input["tenant_alias"])
	if tenantAlias == "" {
		return steps.AgentTaskConfig{}, fmt.Errorf("input.tenant_alias is required")
	}

	cfg := steps.AgentTaskConfig{
		TenantAlias: tenantAlias,
		AgentName:   strings.TrimSpace(step.Input["agent_name"]),
		SessionSlot: -1,
		TaskIDSlot:  -1,
		OutputSlot:  -1,
	}

	if c.DSM != nil {
		cfg.Store = c.DSM.StoreFor(string(config.DomainAgentTasks))
	}

	// session_id
	if sessionRef := strings.TrimSpace(step.Input["session_id"]); sessionRef != "" {
		if isSlotRef(sessionRef) {
			slot, err := c.getSlot(sessionRef)
			if err != nil {
				return steps.AgentTaskConfig{}, fmt.Errorf("session_id slot %q: %w", sessionRef, err)
			}
			cfg.SessionSlot = slot
		} else {
			cfg.SessionLit = sessionRef
		}
	}

	// task_id
	if taskIDRef := strings.TrimSpace(step.Input["task_id"]); taskIDRef != "" {
		if isSlotRef(taskIDRef) {
			slot, err := c.getSlot(taskIDRef)
			if err != nil {
				return steps.AgentTaskConfig{}, fmt.Errorf("task_id slot %q: %w", taskIDRef, err)
			}
			cfg.TaskIDSlot = slot
		} else {
			cfg.TaskIDLit = taskIDRef
		}
	}

	cfg.StatusLit = strings.TrimSpace(step.Input["status"])

	// output_var
	outputVar := strings.TrimSpace(step.Input["output_var"])
	if requireOutput && outputVar == "" {
		return steps.AgentTaskConfig{}, fmt.Errorf("input.output_var is required")
	}
	if outputVar != "" {
		slot, err := c.getSlot(outputVar)
		if err != nil {
			return steps.AgentTaskConfig{}, fmt.Errorf("output_var slot %q: %w", outputVar, err)
		}
		cfg.OutputSlot = slot
	}

	return cfg, nil
}
