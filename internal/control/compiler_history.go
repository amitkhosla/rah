package control

import (
	"fmt"
	"strconv"

	"rah/internal/datastore"
	"rah/internal/engine/steps"
)

// compileAppendMessage resolves and appends the append_message instruction.
//
// step.KeyIdentifier         → historySlot (ByteSlots, in-place modification)
// step.As                    → contentSlot (ByteSlots)
// step.Input["role"]         → "user" | "assistant" (default "user")
// step.Input["max_turns"]    → int (default 0 = unlimited)
func (c *Compiler) compileAppendMessage(step StepConfig) error {
	historySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("append_message: history slot: %w", err)
	}
	contentSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("append_message: content slot: %w", err)
	}

	role := steps.MessageRole(step.Input["role"])
	if role == "" {
		role = steps.RoleUser
	}
	if role != steps.RoleUser && role != steps.RoleAssistant {
		return fmt.Errorf("append_message: role must be %q or %q, got %q", steps.RoleUser, steps.RoleAssistant, role)
	}

	maxTurns := 0
	if raw := step.Input["max_turns"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			maxTurns = n
		}
	}

	c.GlobalTable = append(c.GlobalTable, steps.AppendMessage(historySlot, contentSlot, role, maxTurns))
	return nil
}

// compileTrimHistory resolves and appends the trim_history instruction.
//
// step.KeyIdentifier         → historySlot (ByteSlots, in-place modification)
// step.Input["max_turns"]    → int (default 0 = unlimited)
// step.Input["max_tokens"]   → int (default 0 = unlimited)
func (c *Compiler) compileTrimHistory(step StepConfig) error {
	historySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("trim_history: history slot: %w", err)
	}

	maxTurns := 0
	if raw := step.Input["max_turns"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			maxTurns = n
		}
	}

	maxTokens := 0
	if raw := step.Input["max_tokens"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			maxTokens = n
		}
	}

	c.GlobalTable = append(c.GlobalTable, steps.TrimHistory(historySlot, maxTurns, maxTokens))
	return nil
}

// compileLoadHistory resolves and appends the load_history instruction.
//
// step.KeyIdentifier         → historySlot (ByteSlots, write target)
// step.As                    → keySlot (ByteSlots, conversation key)
// step.Input["domain"]       → datastore domain name (required)
func (c *Compiler) compileLoadHistory(step StepConfig) error {
	historySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("load_history: history slot: %w", err)
	}
	keySlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("load_history: key slot: %w", err)
	}

	domain := step.Input["domain"]
	if domain == "" {
		return fmt.Errorf("load_history: input.domain is required")
	}

	var store datastore.KeyValueStore
	if c.DSM != nil {
		store = c.DSM.StoreFor(domain)
	}

	c.GlobalTable = append(c.GlobalTable, steps.LoadHistory(store, domain, historySlot, keySlot))
	return nil
}

// compileSaveHistory resolves and appends the save_history instruction.
//
// step.KeyIdentifier         → historySlot (ByteSlots, read source)
// step.As                    → keySlot (ByteSlots, conversation key)
// step.Input["domain"]       → datastore domain name (required)
// step.Input["ttl_secs"]     → int (default 0 = no TTL)
func (c *Compiler) compileSaveHistory(step StepConfig) error {
	historySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("save_history: history slot: %w", err)
	}
	keySlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("save_history: key slot: %w", err)
	}

	domain := step.Input["domain"]
	if domain == "" {
		return fmt.Errorf("save_history: input.domain is required")
	}

	ttlSecs := 0
	if raw := step.Input["ttl_secs"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			ttlSecs = n
		}
	}

	var store datastore.KeyValueStore
	if c.DSM != nil {
		store = c.DSM.StoreFor(domain)
	}

	c.GlobalTable = append(c.GlobalTable, steps.SaveHistory(store, domain, historySlot, keySlot, ttlSecs))
	return nil
}
