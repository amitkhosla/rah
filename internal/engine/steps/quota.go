package steps

// quota.go — QuotaGroup assignment step.
//
// A QuotaGroup maps a runtime string value (e.g. a tenant's "tier" metadata:
// "free", "pro", "enterprise") to a specific RateLimitConfigId at bake time.
// This lets one gateway flow serve different SLA tiers without branching.
//
// Usage in a flow:
//
//	{"action": "load_meta",  "key": "tier", "slot": "tier_slot"}
//	{"action": "assign_quota_group", "src_slot": "tier_slot",
//	 "group_map": "free:0,pro:1,enterprise:2"}
//	{"action": "check_rate_limit"}
//
// The group_map is compiled once at bake time into a map[string]uint8.
// At runtime: ByteSlots[srcSlot] is read, looked up in the map, and
// ctx.QuotaGroupID is set. CheckRateLimit then uses QuotaGroupID to
// select the appropriate per-group rate limit config.

import (
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/registry"
)

// AssignQuotaGroup reads a string value from srcSlot, maps it to a quota
// group ID using the bake-time groupMap, and sets ctx.QuotaGroupID.
//
// groupMap: string label â†’ group ID (uint8, 1—255; 0 is reserved for "no group").
// If the slot value is not in the map, QuotaGroupID is left unchanged (stays 0).
//
// The groupMap is built by the compiler from the flow's "group_map" field:
//
//	"free:1,pro:2,enterprise:3"
func AssignQuotaGroup(srcSlot int, groupMap map[string]uint8) engine.Instruction {
	return engine.Instruction{
		Name: "ASSIGN_QUOTA_GROUP",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if srcSlot < 0 || srcSlot >= len(ctx.ByteSlots) {
				return s.PC + 1
			}
			val := ctx.ByteSlots[srcSlot]
			if len(val) == 0 {
				return s.PC + 1
			}
			if gid, ok := groupMap[string(val)]; ok {
				ctx.QuotaGroupID = gid
			}
			return s.PC + 1
		},
	}
}

// AssignTierGroup reads the tenant's tier name from the registry at request time
// and maps it to a quota group ID using the bake-time groupMap.
// Unlike AssignQuotaGroup, this bypasses slot reads — the registry is authoritative.
// groupMap: tier name â†’ group ID (uint8, 1—255; 0 is reserved for "no group").
func AssignTierGroup(groupMap map[string]uint8) engine.Instruction {
	return engine.Instruction{
		Name: "ASSIGN_TIER_GROUP",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			reg := registry.State.Active.Load()
			tierName := registry.TenantTierName(reg, ctx.TenantID)
			if tierName != "" {
				if gid, ok := groupMap[tierName]; ok {
					ctx.QuotaGroupID = gid
				}
			}
			return s.PC + 1
		},
	}
}
