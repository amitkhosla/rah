package steps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/amitkhosla/rah/internal/datastore"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// AgentStateConfig holds bake-time resolved parameters for agent state steps.
type AgentStateConfig struct {
	Store       datastore.KeyValueStore
	TenantAlias string
	AgentName   string
	SessionSlot int    // >= 0: read session ID from ByteSlots at runtime; -1: use SessionLit
	SessionLit  string // literal session ID when SessionSlot == -1
	Key         string // for get/set/clear; empty = whole state
	ValueSlot   int    // >= 0: read value from ByteSlots; -1: use ValueLit
	ValueLit    string // literal value for set when ValueSlot == -1
	OutputSlot  int    // >= 0: write result into ByteSlots[OutputSlot]; -1: skip
}

// agentStateKey builds the storage key for the given session ID.
func agentStateKey(tenantAlias, agentName, sessionID string) string {
	return fmt.Sprintf("agent_state:%s:%s:%s", tenantAlias, agentName, sessionID)
}

// globalTenant is the datastore tenant used for all agent state entries.
// Tenant isolation is encoded in the storage key itself.
const agentGlobalTenant datastore.Tenant = "__global__"

// resolveSession reads the session ID from a slot or falls back to the literal.
func resolveSession(ctx *rctx.Context, slot int, lit string) string {
	if slot >= 0 && slot < len(ctx.ByteSlots) && len(ctx.ByteSlots[slot]) > 0 {
		return string(ctx.ByteSlots[slot])
	}
	return lit
}

// AgentStateGet reads agent state from the store.
// If cfg.Key is empty, the entire state JSON is written to OutputSlot.
// If cfg.Key is set, only that top-level key's value is written.
func AgentStateGet(cfg AgentStateConfig) engine.Instruction {
	return engine.Instruction{
		Name: "agent_state_get",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Store == nil || cfg.OutputSlot < 0 {
				return state.PC + 1
			}
			sessionID := resolveSession(ctx, cfg.SessionSlot, cfg.SessionLit)
			if sessionID == "" {
				return state.PC + 1
			}
			key := agentStateKey(cfg.TenantAlias, cfg.AgentName, sessionID)
			raw, found, err := cfg.Store.Get(context.Background(), agentGlobalTenant, key)
			if err != nil || !found {
				return state.PC + 1
			}
			if cfg.Key == "" {
				state.WriteSlot(ctx, cfg.OutputSlot, raw)
				return state.PC + 1
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				return state.PC + 1
			}
			if v, ok := m[cfg.Key]; ok {
				state.WriteSlot(ctx, cfg.OutputSlot, v)
			}
			return state.PC + 1
		},
	}
}

// AgentStateSet writes a value into the agent state.
// If cfg.Key is empty, the entire state is replaced with the value.
// If cfg.Key is set, only that top-level key is updated.
func AgentStateSet(cfg AgentStateConfig) engine.Instruction {
	return engine.Instruction{
		Name: "agent_state_set",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Store == nil {
				return state.PC + 1
			}
			sessionID := resolveSession(ctx, cfg.SessionSlot, cfg.SessionLit)
			if sessionID == "" {
				return state.PC + 1
			}
			var valueBytes []byte
			if cfg.ValueSlot >= 0 && cfg.ValueSlot < len(ctx.ByteSlots) {
				valueBytes = ctx.ByteSlots[cfg.ValueSlot]
			} else {
				valueBytes = []byte(cfg.ValueLit)
			}
			storeKey := agentStateKey(cfg.TenantAlias, cfg.AgentName, sessionID)
			if cfg.Key == "" {
				_ = cfg.Store.Put(context.Background(), agentGlobalTenant, storeKey, valueBytes)
				return state.PC + 1
			}
			existing, _, _ := cfg.Store.Get(context.Background(), agentGlobalTenant, storeKey)
			var m map[string]json.RawMessage
			if len(existing) > 0 {
				_ = json.Unmarshal(existing, &m)
			}
			if m == nil {
				m = make(map[string]json.RawMessage)
			}
			m[cfg.Key] = json.RawMessage(valueBytes)
			updated, err := json.Marshal(m)
			if err != nil {
				return state.PC + 1
			}
			_ = cfg.Store.Put(context.Background(), agentGlobalTenant, storeKey, updated)
			return state.PC + 1
		},
	}
}

// AgentStateMerge merges top-level keys from the patch value into the stored state.
// Both the existing state and the patch must be JSON objects.
func AgentStateMerge(cfg AgentStateConfig) engine.Instruction {
	return engine.Instruction{
		Name: "agent_state_merge",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Store == nil {
				return state.PC + 1
			}
			sessionID := resolveSession(ctx, cfg.SessionSlot, cfg.SessionLit)
			if sessionID == "" {
				return state.PC + 1
			}
			var patch []byte
			if cfg.ValueSlot >= 0 && cfg.ValueSlot < len(ctx.ByteSlots) {
				patch = ctx.ByteSlots[cfg.ValueSlot]
			} else {
				patch = []byte(cfg.ValueLit)
			}
			if len(patch) == 0 {
				return state.PC + 1
			}
			storeKey := agentStateKey(cfg.TenantAlias, cfg.AgentName, sessionID)
			existing, _, _ := cfg.Store.Get(context.Background(), agentGlobalTenant, storeKey)
			merged, err := shallowMergeJSON(existing, patch)
			if err != nil {
				return state.PC + 1
			}
			_ = cfg.Store.Put(context.Background(), agentGlobalTenant, storeKey, merged)
			return state.PC + 1
		},
	}
}

// AgentStateClear removes agent state from the store.
// If cfg.Key is empty, the entire state record is deleted.
// If cfg.Key is set, only that top-level key is removed.
func AgentStateClear(cfg AgentStateConfig) engine.Instruction {
	return engine.Instruction{
		Name: "agent_state_clear",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Store == nil {
				return state.PC + 1
			}
			sessionID := resolveSession(ctx, cfg.SessionSlot, cfg.SessionLit)
			if sessionID == "" {
				return state.PC + 1
			}
			storeKey := agentStateKey(cfg.TenantAlias, cfg.AgentName, sessionID)
			if cfg.Key == "" {
				_ = cfg.Store.Delete(context.Background(), agentGlobalTenant, storeKey)
				return state.PC + 1
			}
			existing, found, _ := cfg.Store.Get(context.Background(), agentGlobalTenant, storeKey)
			if !found {
				return state.PC + 1
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(existing, &m); err != nil {
				return state.PC + 1
			}
			delete(m, cfg.Key)
			updated, err := json.Marshal(m)
			if err != nil {
				return state.PC + 1
			}
			_ = cfg.Store.Put(context.Background(), agentGlobalTenant, storeKey, updated)
			return state.PC + 1
		},
	}
}

// shallowMergeJSON merges top-level keys from patch into base.
// Both must be JSON objects. If base is nil or empty, returns patch.
func shallowMergeJSON(base, patch []byte) ([]byte, error) {
	if len(base) == 0 {
		return patch, nil
	}
	var bm map[string]json.RawMessage
	if err := json.Unmarshal(base, &bm); err != nil {
		return nil, fmt.Errorf("shallowMergeJSON: base is not a JSON object: %w", err)
	}
	var pm map[string]json.RawMessage
	if err := json.Unmarshal(patch, &pm); err != nil {
		return nil, fmt.Errorf("shallowMergeJSON: patch is not a JSON object: %w", err)
	}
	for k, v := range pm {
		bm[k] = v
	}
	return json.Marshal(bm)
}
