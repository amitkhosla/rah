package steps

import (
	"time"

	"rah/internal/engine"
	"rah/internal/rctx"
	"rah/internal/registry"
)

// CheckRateLimit is an opt-in step that enforces the rate limit configured
// for the matched API and endpoint.
//
// Rate limiting only activates when the customer explicitly adds this step:
//
//	{"action": "check_rate_limit"}
//
// Enforcement uses two self-resetting fixed-window counters per request:
//   - Per-second: epoch = unix seconds, limit = resolved.PerSec × BurstFactor/100
//   - Per-minute: epoch = unix/60,     limit = resolved.PerMin (when > 0)
//
// QuotaGroup override: if ctx.QuotaGroupID != 0 and quotaGroupRLIds has a
// non-zero entry at that index, the group's RateLimitConfigId overrides the
// API-level config for the rate limit resolution. This lets a single flow serve
// multiple SLA tiers (set via assign_quota_group before this step).
//
// quotaGroupRLIds is baked at compile time: index = QuotaGroupID (uint8),
// value = RateLimitConfigId (uint16). Pass nil if quota groups are not used.
//
// Returns 403 if the tenant is blocked; 429 if either window is exceeded.
// No-ops (passes through) if both limits are 0 (unlimited).
func CheckRateLimit(store *engine.CounterStore, quotaGroupRLIds []uint16) engine.Instruction {
	return engine.Instruction{
		Name: "CHECK_RATE_LIMIT",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			reg := registry.State.Active.Load()

			// QuotaGroup override: if a group was assigned and maps to a config,
			// use it instead of the static API-level rate limit config.
			apiRLId := ctx.APIRateLimitId
			if ctx.QuotaGroupID != 0 && int(ctx.QuotaGroupID) < len(quotaGroupRLIds) {
				if gid := quotaGroupRLIds[ctx.QuotaGroupID]; gid != 0 {
					apiRLId = gid
				}
			}

			resolved, blocked := registry.ResolveRateLimit(
				reg,
				ctx.CallerID,
				ctx.TenantID,
				apiRLId,
				ctx.EndpointRateLimitId,
			)

			if blocked {
				ctx.ResponseStatus = 403
				return -1
			}

			now := uint32(time.Now().Unix())

			// ── Per-second window ─────────────────────────────────────────────
			if resolved.PerSec > 0 {
				bf := resolved.BurstFactor
				if bf == 0 {
					bf = 100
				}
				// effectiveLimit = PerSec * BurstFactor / 100 (integer, no float)
				effectiveSec := uint32(uint64(resolved.PerSec) * uint64(bf) / 100)
				if effectiveSec == 0 {
					effectiveSec = 1
				}
				idxSec := rateLimitIndex(store, ctx.TenantID, apiRLId, ctx.EndpointRateLimitId, ctx.QuotaGroupID, now, 0)
				if !store.FixedWindowEpoch(idxSec, now, effectiveSec) {
					ctx.ResponseStatus = 429
					return -1
				}
			}

			// ── Per-minute window ─────────────────────────────────────────────
			if resolved.PerMin > 0 {
				epochMin := now / 60
				idxMin := rateLimitIndex(store, ctx.TenantID, apiRLId, ctx.EndpointRateLimitId, ctx.QuotaGroupID, epochMin, 1)
				if !store.FixedWindowEpoch(idxMin, epochMin, resolved.PerMin) {
					ctx.ResponseStatus = 429
					return -1
				}
			}

			return s.PC + 1
		},
	}
}

// rateLimitIndex hashes (tenantID, apiRLId, endpointRLId, epoch, windowType)
// to a CounterStore slot. The windowType byte (0=sec, 1=min) ensures per-second
// and per-minute counters for the same request land on different slots.
//
// Uses Knuth multiplicative hashing — fast, well-distributed, no divisions
// except the final modulo against the store size.
func rateLimitIndex(
	store *engine.CounterStore,
	tenantID, apiRLId, endpointRLId uint16,
	quotaGroupID uint8,
	epoch uint32,
	windowType uint8,
) uint32 {
	h := uint64(tenantID)*2654435761 ^
		uint64(apiRLId)*2246822519 ^
		uint64(endpointRLId)*3266489917 ^
		uint64(quotaGroupID)*2654435923 ^
		uint64(epoch)*1000003 ^
		uint64(windowType)*2166136261
	return uint32(h) % uint32(len(store.Arena))
}
