package steps

import (
	"fmt"
	"unsafe"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/registry"
)

// RegistryMutator is the subset of RegistryManager used by the set_* and delete_* steps.
// Defined here so the steps package does not import the full registry manager.
type RegistryMutator interface {
	AddServiceURL(alias string, name string, value []byte)
	AddIdentifier(alias string, name string, value []byte)
	AddMeta(alias string, name string, value []byte)
	DeleteServiceURL(alias string, name string)
	DeleteIdentifier(alias string, name string)
	DeleteMeta(alias string, name string)
}

// RegistryLookup resolves the alias stored in keySlot to a TenantID.
//
// onHitJumpPC controls behaviour when a miss-handler block is inlined immediately
// after this instruction:
//   - onHitJumpPC < 0  â†’ no miss block; miss halts with 401 (default behaviour).
//   - onHitJumpPC >= 0 â†’ miss block follows inline; on HIT jump to onHitJumpPC
//     (skipping the block), on MISS fall through to s.PC+1 (entering the block).
//
// On no registry: always sets 503 and halts.
//
// Flow config:
//
//	{"action": "registry_lookup", "key_identifier": "header.X-Tenant"}
//	{"action": "registry_lookup", "key_identifier": "header.X-Tenant", "on_miss": "hydrate-flow"}
func RegistryLookup(keySlot int, onHitJumpPC int16) engine.Instruction {
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
				if onHitJumpPC >= 0 {
					// Fall through into the inlined miss block.
					return s.PC + 1
				}
				ctx.ResponseStatus = 401
				return -1
			}
			ctx.TenantID = tID
			ctx.TenantKey = alias
			s.AddTraceAttr("tenant_id", fmt.Sprintf("%d", tID))
			if onHitJumpPC >= 0 {
				return onHitJumpPC // skip the inlined miss block
			}
			return s.PC + 1
		},
	}
}

// LoadServiceURL loads the upstream URL for the current tenant into destSlot.
// keyID is the pre-resolved KeyID in the URLs store, obtained via
// RegistryManager.EnsureURLKeyID at bake time.
// Hot-path cost: 1 atomic load â‰ˆ 2—5 ns.
//
// Example: {"action": "load_service_url", "key": "primary", "as": "upstream_url"}
func LoadServiceURL(keyID uint16, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "LOAD_SERVICE_URL",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if val, ok := registry.GetURLByKeyID(ctx.TenantID, keyID); ok {
				ctx.ByteSlots[destSlot] = val
				s.AddTraceAttr("url", string(val))
			} else {
				s.AddTraceAttr("url", "")
				s.AddTraceAttr("key_id", fmt.Sprintf("%d", keyID))
			}
			return s.PC + 1
		},
	}
}

// LoadIdentifier loads the identifier for the current tenant into destSlot.
// keyID is the pre-resolved KeyID in the IDs store, obtained via
// RegistryManager.EnsureIDKeyID at bake time.
// Hot-path cost: 1 atomic load â‰ˆ 2—5 ns.
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

// LoadMeta loads a metadata value for the current tenant into destSlot.
// keyID is the pre-resolved KeyID in the Meta store, obtained via
// RegistryManager.EnsureMetaKeyID at bake time.
// Hot-path cost: 1 atomic load â‰ˆ 2—5 ns.
//
// Example: {"action": "load_meta", "key": "tier", "as": "tier_slot"}
func LoadMeta(keyID uint16, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "LOAD_META",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if val, ok := registry.GetMetaByKeyID(ctx.TenantID, keyID); ok {
				ctx.ByteSlots[destSlot] = val
			}
			return s.PC + 1
		},
	}
}

// LoadServiceURLVar loads a service URL using a runtime key name from keySlot.
// The key name (e.g. "payments_url") must already be in ctx.ByteSlots[keySlot].
// Hot-path cost: ~50—100 ns (radix walk on immutable snapshot). Zero allocations.
// Use only when the key is not known at compile time. Prefer LoadServiceURL (~2—5 ns)
// when the key is static.
func LoadServiceURLVar(keySlot int, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "LOAD_SERVICE_URL_VAR",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if keySlot >= len(ctx.ByteSlots) || len(ctx.ByteSlots[keySlot]) == 0 {
				return s.PC + 1
			}
			// Zero-copy string view — no heap allocation.
			keyName := *(*string)(unsafe.Pointer(&ctx.ByteSlots[keySlot]))
			reg := registry.State.Active.Load()
			if val, ok := registry.GetURLByKeyName(reg, ctx.TenantID, keyName); ok {
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
			mgr.AddMeta(ctx.TenantKey, name, val)
			return s.PC + 1
		},
	}
}

// DeleteServiceURL removes a service URL from the registry for the current tenant.
// name is the URL key (e.g. "primary"). The entry is deleted immediately.
// This is a management-plane write — involves a mutex lock — use in admin flows.
//
// Example: {"action": "delete_service_url", "key": "fallback"}
func DeleteServiceURL(mgr RegistryMutator, name string) engine.Instruction {
	return engine.Instruction{
		Name: "DELETE_SERVICE_URL",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			mgr.DeleteServiceURL(ctx.TenantKey, name)
			return s.PC + 1
		},
	}
}

// DeleteIdentifier removes an identifier from the registry for the current tenant.
// name is the identifier key (e.g. "api_key"). The entry is deleted immediately.
// This is a management-plane write — involves a mutex lock — use in admin flows.
//
// Example: {"action": "delete_identifier", "key": "api_key"}
func DeleteIdentifier(mgr RegistryMutator, name string) engine.Instruction {
	return engine.Instruction{
		Name: "DELETE_IDENTIFIER",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			mgr.DeleteIdentifier(ctx.TenantKey, name)
			return s.PC + 1
		},
	}
}

// DeleteMeta removes a metadata value from the registry for the current tenant.
// name is the metadata key (e.g. "tier"). The entry is deleted immediately.
// This is a management-plane write — involves a mutex lock — use in admin flows.
//
// Example: {"action": "delete_meta", "key": "tier"}
func DeleteMeta(mgr RegistryMutator, name string) engine.Instruction {
	return engine.Instruction{
		Name: "DELETE_META",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			mgr.DeleteMeta(ctx.TenantKey, name)
			return s.PC + 1
		},
	}
}
