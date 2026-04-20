package control

import (
	"encoding/json"
	"fmt"
	"strings"

	"rah/internal/config"
	"rah/internal/engine/steps"
)

// compileRouteLLM resolves and appends the route_llm instruction.
// It evaluates rules at runtime to select the best model slug and writes it to
// the result slot, which a subsequent llm_call (with model_slot) will consume.
//
// step.As         → result slot (ByteSlots) for the selected model slug
// step.Input["rules"]       → JSON array of {condition, model} objects
// step.Input["default"]     → fallback model slug if no rule matches
// step.Input["token_slot"]  → slot name holding token estimate (IntSlots); optional
// step.Input["meta_slot"]   → slot name holding tenant meta value (ByteSlots); optional
func (c *Compiler) compileRouteLLM(step StepConfig) error {
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("route_llm: result slot: %w", err)
	}

	// Parse rules from JSON
	var rules []steps.RoutingRule
	if rulesJSON := step.Input["rules"]; rulesJSON != "" {
		if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
			return fmt.Errorf("route_llm: invalid rules JSON: %w", err)
		}
	}

	// Default model slug
	defaultModel := step.Input["default"]

	// Token slot (optional)
	tokenSlot := -1
	if tsName := step.Input["token_slot"]; tsName != "" {
		if s, slotErr := c.getSlot(tsName); slotErr == nil {
			tokenSlot = s
		}
	}

	// Meta slot (optional)
	metaSlot := -1
	if msName := step.Input["meta_slot"]; msName != "" {
		if s, slotErr := c.getSlot(msName); slotErr == nil {
			metaSlot = s
		}
	}

	// Build ModelCatalog from LLM config for validation reference
	catalog := make(map[string]config.LLMModelConfig, len(c.LLMCfg.Models))
	for _, m := range c.LLMCfg.Models {
		catalog[m.Alias] = m
	}

	// Build ByteSlotMap: for any condition whose LHS is not a built-in keyword,
	// treat it as a named slot and resolve it. This enables slot-value conditions
	// such as "var.complexity == high" emitted by classify_llm upstream.
	byteSlotMap := make(map[string]int)
	for _, rule := range rules {
		lhs := conditionLHS(rule.Condition)
		if lhs == "" || lhs == "token_count" || lhs == "meta" || lhs == "true" {
			continue
		}
		if _, already := byteSlotMap[lhs]; already {
			continue
		}
		if s, slotErr := c.getSlot(lhs); slotErr == nil {
			byteSlotMap[lhs] = s
		}
	}

	cfg := steps.RouteLLMConfig{
		Rules:        rules,
		Default:      defaultModel,
		ResultSlot:   resultSlot,
		TokenSlot:    tokenSlot,
		MetaSlot:     metaSlot,
		ByteSlotMap:  byteSlotMap,
		ModelCatalog: catalog,
	}

	c.GlobalTable = append(c.GlobalTable, steps.RouteLLM(cfg))
	return nil
}

// conditionLHS returns the left-hand side token of a routing condition string
// (the part before the first space), or "" if the condition has no operator.
func conditionLHS(cond string) string {
	cond = strings.TrimSpace(cond)
	if idx := strings.IndexByte(cond, ' '); idx >= 0 {
		return cond[:idx]
	}
	return cond
}
