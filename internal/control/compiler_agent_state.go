package control

import (
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileAgentStateGet compiles the agent_state_get step.
//
// step.Input fields:
//
//	tenant_alias  — tenant identifier (required)
//	agent_name    — agent identifier (required)
//	session_id    — slot name or literal session ID (required)
//	key           — top-level key to read; empty = whole state
//	output_var    — slot name to write result into (required)
func (c *Compiler) compileAgentStateGet(step StepConfig) error {
	cfg, err := buildAgentStateConfig(c, step, true)
	if err != nil {
		return fmt.Errorf("agent_state_get: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.AgentStateGet(cfg))
	return nil
}

// compileAgentStateSet compiles the agent_state_set step.
//
// step.Input fields:
//
//	tenant_alias  — tenant identifier (required)
//	agent_name    — agent identifier (required)
//	session_id    — slot name or literal session ID (required)
//	key           — top-level key to set; empty = replace whole state
//	value         — literal JSON value or slot name holding the value
func (c *Compiler) compileAgentStateSet(step StepConfig) error {
	cfg, err := buildAgentStateConfig(c, step, false)
	if err != nil {
		return fmt.Errorf("agent_state_set: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.AgentStateSet(cfg))
	return nil
}

// compileAgentStateMerge compiles the agent_state_merge step.
//
// step.Input fields:
//
//	tenant_alias  — tenant identifier (required)
//	agent_name    — agent identifier (required)
//	session_id    — slot name or literal session ID (required)
//	value         — literal JSON object or slot name holding the patch
func (c *Compiler) compileAgentStateMerge(step StepConfig) error {
	cfg, err := buildAgentStateConfig(c, step, false)
	if err != nil {
		return fmt.Errorf("agent_state_merge: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.AgentStateMerge(cfg))
	return nil
}

// compileAgentStateClear compiles the agent_state_clear step.
//
// step.Input fields:
//
//	tenant_alias  — tenant identifier (required)
//	agent_name    — agent identifier (required)
//	session_id    — slot name or literal session ID (required)
//	key           — top-level key to clear; empty = delete whole state
func (c *Compiler) compileAgentStateClear(step StepConfig) error {
	cfg, err := buildAgentStateConfig(c, step, false)
	if err != nil {
		return fmt.Errorf("agent_state_clear: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, steps.AgentStateClear(cfg))
	return nil
}

// buildAgentStateConfig reads common agent state step fields and resolves slot indices.
// requireOutput gates the output_var check for steps that need it (get).
func buildAgentStateConfig(c *Compiler, step StepConfig, requireOutput bool) (steps.AgentStateConfig, error) {
	tenantAlias := strings.TrimSpace(step.Input["tenant_alias"])
	if tenantAlias == "" {
		return steps.AgentStateConfig{}, fmt.Errorf("input.tenant_alias is required")
	}
	agentName := strings.TrimSpace(step.Input["agent_name"])
	if agentName == "" {
		return steps.AgentStateConfig{}, fmt.Errorf("input.agent_name is required")
	}
	sessionRef := strings.TrimSpace(step.Input["session_id"])
	if sessionRef == "" {
		return steps.AgentStateConfig{}, fmt.Errorf("input.session_id is required")
	}

	cfg := steps.AgentStateConfig{
		TenantAlias: tenantAlias,
		AgentName:   agentName,
		SessionSlot: -1,
		ValueSlot:   -1,
		OutputSlot:  -1,
	}

	// Resolve store from DSM.
	if c.DSM != nil {
		cfg.Store = c.DSM.StoreFor(string(config.DomainAgentState))
	}

	// session_id: if it looks like a slot variable (contains a dot or matches an existing slot),
	// allocate as slot; otherwise treat as a literal.
	if isSlotRef(sessionRef) {
		slot, err := c.getSlot(sessionRef)
		if err != nil {
			return steps.AgentStateConfig{}, fmt.Errorf("session_id slot %q: %w", sessionRef, err)
		}
		cfg.SessionSlot = slot
	} else {
		cfg.SessionLit = sessionRef
	}

	cfg.Key = strings.TrimSpace(step.Input["key"])

	// value: may be a slot ref or a literal
	if valueRef := strings.TrimSpace(step.Input["value"]); valueRef != "" {
		if isSlotRef(valueRef) {
			slot, err := c.getSlot(valueRef)
			if err != nil {
				return steps.AgentStateConfig{}, fmt.Errorf("value slot %q: %w", valueRef, err)
			}
			cfg.ValueSlot = slot
		} else {
			cfg.ValueLit = valueRef
		}
	}

	// output_var
	outputVar := strings.TrimSpace(step.Input["output_var"])
	if requireOutput && outputVar == "" {
		return steps.AgentStateConfig{}, fmt.Errorf("input.output_var is required")
	}
	if outputVar != "" {
		slot, err := c.getSlot(outputVar)
		if err != nil {
			return steps.AgentStateConfig{}, fmt.Errorf("output_var slot %q: %w", outputVar, err)
		}
		cfg.OutputSlot = slot
	}

	return cfg, nil
}

// isSlotRef returns true when the string looks like a variable reference
// (contains a dot separator like "header.X-Foo" or "var.name") rather than a literal value.
func isSlotRef(s string) bool {
	return strings.Contains(s, ".")
}
