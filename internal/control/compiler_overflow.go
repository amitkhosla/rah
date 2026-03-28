package control

import (
	"fmt"
	"strconv"

	"rah/internal/datastore"
	"rah/internal/engine/steps"
)

// compileOverflowHistory resolves and appends the overflow_history instruction.
//
// step.KeyIdentifier            → historySlot (ByteSlots, trimmed in place)
// step.As                       → keySlot (ByteSlots, conversation key)
// step.Input["overflow_slot"]   → IntSlots index for token overflow budget (optional; default -1)
// step.Input["max_tokens"]      → int fallback token budget (default 0)
// step.Input["max_turns"]       → int fallback turn limit (default 0)
// step.Input["domain"]          → datastore domain (required when a store is needed)
// step.Input["ttl_secs"]        → int TTL in seconds (default 0)
func (c *Compiler) compileOverflowHistory(step StepConfig) error {
	historySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("overflow_history: history slot: %w", err)
	}
	keySlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("overflow_history: key slot: %w", err)
	}

	overflowSlot := -1
	if raw := step.Input["overflow_slot"]; raw != "" {
		if n, convErr := c.getSlot(raw); convErr == nil {
			overflowSlot = n
		}
	}

	maxTokens := 0
	if raw := step.Input["max_tokens"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			maxTokens = n
		}
	}

	maxTurns := 0
	if raw := step.Input["max_turns"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			maxTurns = n
		}
	}

	ttlSecs := 0
	if raw := step.Input["ttl_secs"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			ttlSecs = n
		}
	}

	domain := step.Input["domain"]

	var store datastore.KeyValueStore
	if domain != "" && c.DSM != nil {
		store = c.DSM.StoreFor(domain)
	}

	cfg := steps.OverflowHistoryConfig{
		HistorySlot:  historySlot,
		KeySlot:      keySlot,
		OverflowSlot: overflowSlot,
		MaxTokens:    maxTokens,
		MaxTurns:     maxTurns,
		Domain:       domain,
		Store:        store,
		TTLSecs:      ttlSecs,
	}

	c.GlobalTable = append(c.GlobalTable, steps.OverflowHistory(cfg))
	return nil
}

// compileLoadOverflowHistory resolves and appends the load_overflow_history instruction.
//
// step.KeyIdentifier          → historySlot (ByteSlots, overflow prepended here)
// step.As                     → keySlot (ByteSlots, conversation key)
// step.Input["domain"]        → datastore domain (required)
// step.Input["max_turns"]     → int: limit overflow turns prepended (default 0 = unlimited)
func (c *Compiler) compileLoadOverflowHistory(step StepConfig) error {
	historySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("load_overflow_history: history slot: %w", err)
	}
	keySlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("load_overflow_history: key slot: %w", err)
	}

	domain := step.Input["domain"]
	if domain == "" {
		return fmt.Errorf("load_overflow_history: input.domain is required")
	}

	maxTurns := 0
	if raw := step.Input["max_turns"]; raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			maxTurns = n
		}
	}

	var store datastore.KeyValueStore
	if c.DSM != nil {
		store = c.DSM.StoreFor(domain)
	}

	cfg := steps.LoadOverflowHistoryConfig{
		HistorySlot: historySlot,
		KeySlot:     keySlot,
		MaxTurns:    maxTurns,
		Domain:      domain,
		Store:       store,
	}

	c.GlobalTable = append(c.GlobalTable, steps.LoadOverflowHistory(cfg))
	return nil
}
