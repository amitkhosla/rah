package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
	"rah/internal/registry"
)

// RegistryMutator is the subset of RegistryManager used by the set_* steps.
// Defined here so the steps package does not import the full registry manager.
type RegistryMutator interface {
	AddServiceURL(alias string, name string, value []byte)
	AddIdentifier(alias string, name string, value []byte)
	AddMeta(alias string, name string, value []byte)
}

// RegistryLookup resolves the alias stored in keySlot to a TenantID.
//
// On success: sets ctx.TenantID and ctx.TenantKey, advances PC.
// On miss:    sets ctx.ResponseStatus = 401, halts execution.
// On no registry: sets 503, halts.
//
// This is the typical first step in a tenant-aware flow:
//
//	{"action": "registry_lookup", "key_identifier": "header.X-Tenant"}
func RegistryLookup(keySlot int) engine.Instruction {
	return engine.Instruction{
		Name: "REG_LOOKUP",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if keySlot >= len(ctx.ByteSlots) || len(ctx.ByteSlots[keySlot]) == 0 {
				ctx.ResponseStatus = 401
				return -1
			}
			// string() is one allocation; acceptable here — not the inner hot loop.
			alias := string(ctx.ByteSlots[keySlot])
			reg := registry.State.Active.Load()
			if reg == nil {
				ctx.ResponseStatus = 503
				return -1
			}
			tID, found := reg.Aliases.Lookup(alias)
			if !found {
				ctx.ResponseStatus = 401
				return -1
			}
			ctx.TenantID = tID
			ctx.TenantKey = alias
			return s.PC + 1
		},
	}
}

// LoadServiceURL loads the upstream URL for the current tenant into destSlot.
// keyID is the pre-resolved KeyID in the URLs store, obtained via
// RegistryManager.EnsureURLKeyID at bake time.
// Hot-path cost: 1 atomic load ≈ 2–5 ns.
//
// Example: {"action": "load_service_url", "key": "primary", "as": "upstream_url"}
func LoadServiceURL(keyID uint16, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "LOAD_SERVICE_URL",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if val, ok := registry.GetURLByKeyID(ctx.TenantID, keyID); ok {
				ctx.ByteSlots[destSlot] = val
			}
			return s.PC + 1
		},
	}
}

// LoadIdentifier loads the identifier for the current tenant into destSlot.
// keyID is the pre-resolved KeyID in the IDs store, obtained via
// RegistryManager.EnsureIDKeyID at bake time.
// Hot-path cost: 1 atomic load ≈ 2–5 ns.
//
// Example: {"action": "load_identifier", "key": "api_key", "as": "tenant_api_key"}
func LoadIdentifier(keyID uint16, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "LOAD_IDENTIFIER",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if val, ok := registry.GetIDByKeyID(ctx.TenantID, keyID); ok {
				ctx.ByteSlots[destSlot] = val
			}
			return s.PC + 1
		},
	}
}

// SetServiceURL writes a service URL to the registry for the current tenant.
// name is the URL key (e.g. "primary"). The value is read from srcSlot.
// This is a management-plane write — involves a mutex lock — so use it in
// admin/onboarding flows, not high-frequency paths.
//
// Example: {"action": "set_service_url", "key": "primary", "source": "var.new_url"}
func SetServiceURL(mgr RegistryMutator, name string, srcSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "SET_SERVICE_URL",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if srcSlot >= len(ctx.ByteSlots) {
				return s.PC + 1
			}
			val := ctx.ByteSlots[srcSlot]
			if len(val) == 0 {
				return s.PC + 1
			}
			if ctx.TestMode {
				// Suppress registry write in test mode — live registry is read-only.
				// The side_effects counter in the test runner tracks this.
				return s.PC + 1
			}
			mgr.AddServiceURL(ctx.TenantKey, name, val)
			return s.PC + 1
		},
	}
}

// SetIdentifier writes an identifier to the registry for the current tenant.
// name is the identifier key (e.g. "api_key"). The value is read from srcSlot.
// This is a management-plane write — involves a mutex lock — use in admin flows.
//
// Example: {"action": "set_identifier", "key": "api_key", "source": "var.new_key"}
func SetIdentifier(mgr RegistryMutator, name string, srcSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "SET_IDENTIFIER",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if srcSlot >= len(ctx.ByteSlots) {
				return s.PC + 1
			}
			val := ctx.ByteSlots[srcSlot]
			if len(val) == 0 {
				return s.PC + 1
			}
			if ctx.TestMode {
				// Suppress registry write in test mode — live registry is read-only.
				// The side_effects counter in the test runner tracks this.
				return s.PC + 1
			}
			mgr.AddIdentifier(ctx.TenantKey, name, val)
			return s.PC + 1
		},
	}
}

// SetMeta writes a metadata value to the registry for the current tenant.
// name is the metadata key (e.g. "tier"). The value is read from srcSlot.
// This is a management-plane write — involves a mutex lock — use in admin flows.
//
// Example: {"action": "set_meta", "key": "tier", "source": "var.new_tier"}
func SetMeta(mgr RegistryMutator, name string, srcSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "SET_META",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if srcSlot >= len(ctx.ByteSlots) {
				return s.PC + 1
			}
			val := ctx.ByteSlots[srcSlot]
			if len(val) == 0 {
				return s.PC + 1
			}
			if ctx.TestMode {
				// Suppress registry write in test mode — live registry is read-only.
				// The side_effects counter in the test runner tracks this.
				return s.PC + 1
			}
			mgr.AddMeta(ctx.TenantKey, name, val)
			return s.PC + 1
		},
	}
}
