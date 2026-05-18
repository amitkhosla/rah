package steps

import (
	"fmt"
	"strconv"
	"time"

	"rah/internal/engine"
	"rah/internal/rctx"
	"rah/internal/registry"
)

// Standard rate-limit response header names (pre-allocated, never mutated).
var (
	hdrRLLimitSecond     = []byte("X-RateLimit-Limit-Second")
	hdrRLRemainingSecond = []byte("X-RateLimit-Remaining-Second")
	hdrRLLimitMinute     = []byte("X-RateLimit-Limit-Minute")
	hdrRLRemainingMinute = []byte("X-RateLimit-Remaining-Minute")
	hdrRLReset           = []byte("X-RateLimit-Reset")
	hdrRetryAfterRL      = []byte("Retry-After")
)

// fmtUint32 formats v as decimal bytes using the arena so no heap allocation
// occurs on the header-emit path.
func fmtUint32(ctx *rctx.Context, v uint32) []byte {
	buf := ctx.Alloc(10)
	b := strconv.AppendUint(buf[:0], uint64(v), 10)
	return b
}

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
// remoteRL / syncPolicy control distributed enforcement:
//   - syncPolicy 0 (LOCAL):  use only in-process counters (default, fastest).
//   - syncPolicy 1 (ASYNC):  local decision first; if allowed, fire-and-forget
//     INCR to Redis in background for cross-pod visibility.
//   - syncPolicy 2 (STRICT): call Redis before allowing; each request gets its
//     own count — NOT all-or-none. Fails-open when Redis is unavailable.
//
// Returns 403 if the tenant is blocked; 429 if either window is exceeded.
// No-ops (passes through) if both limits are 0 (unlimited).
// When emitQuotaHeaders is true, X-RateLimit-* headers are written to the
// response for every request (allowed or denied). Retry-After is added on 429.
func CheckRateLimit(store *engine.CounterStore, remoteRL engine.ExternalRateLimitProvider, syncPolicy uint8, quotaGroupRLIds []uint16, emitQuotaHeaders bool) engine.Instruction {
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

			// ── STRICT distributed enforcement (syncPolicy >= 2) ─────────────
			// Each request increments its own Redis counter independently — no
			// all-or-none: some concurrent requests may pass, others may fail
			// depending on the order Redis processes the pipeline entries.
			if syncPolicy >= 2 && remoteRL != nil {
				if resolved.PerSec > 0 {
					bf := resolved.BurstFactor
					if bf == 0 {
						bf = 100
					}
					effectiveSec := uint32(uint64(resolved.PerSec) * uint64(bf) / 100)
					if effectiveSec == 0 {
						effectiveSec = 1
					}
					secKey := fmt.Sprintf("rl:%d:%d:%d:s", ctx.TenantID, apiRLId, now)
					ok, remSec := remoteRL.Check(secKey, effectiveSec, 2)
					if emitQuotaHeaders {
						ctx.SetResponseHeader(hdrRLLimitSecond, fmtUint32(ctx, effectiveSec))
						ctx.SetResponseHeader(hdrRLRemainingSecond, fmtUint32(ctx, remSec))
						ctx.SetResponseHeader(hdrRLReset, fmtUint32(ctx, now+1))
					}
					if !ok {
						ctx.ResponseStatus = 429
						if emitQuotaHeaders {
							ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, 1))
						}
						return -1
					}
				}

				if resolved.PerMin > 0 {
					epochMin := now / 60
					minKey := fmt.Sprintf("rl:%d:%d:%d:m", ctx.TenantID, apiRLId, epochMin)
					ok, remMin := remoteRL.Check(minKey, resolved.PerMin, 61)
					if emitQuotaHeaders {
						ctx.SetResponseHeader(hdrRLLimitMinute, fmtUint32(ctx, resolved.PerMin))
						ctx.SetResponseHeader(hdrRLRemainingMinute, fmtUint32(ctx, remMin))
					}
					if !ok {
						ctx.ResponseStatus = 429
						if emitQuotaHeaders {
							secsUntilReset := 60 - (now % 60)
							ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, secsUntilReset))
						}
						return -1
					}
				}

				return s.PC + 1
			}

			// ── Local fixed-window counters (LOCAL and ASYNC) ─────────────────

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
				ok, remSec := store.FixedWindowEpoch(idxSec, now, effectiveSec)
				if emitQuotaHeaders {
					ctx.SetResponseHeader(hdrRLLimitSecond, fmtUint32(ctx, effectiveSec))
					ctx.SetResponseHeader(hdrRLRemainingSecond, fmtUint32(ctx, remSec))
					// Reset = start of next second
					ctx.SetResponseHeader(hdrRLReset, fmtUint32(ctx, now+1))
				}
				if !ok {
					ctx.ResponseStatus = 429
					if emitQuotaHeaders {
						ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, 1))
					}
					return -1
				}

				// ASYNC: fire-and-forget background INCR for cross-pod visibility.
				if syncPolicy == 1 && remoteRL != nil {
					secKey := fmt.Sprintf("rl:%d:%d:%d:s", ctx.TenantID, apiRLId, now)
					capSec := effectiveSec
					go func() { remoteRL.Check(secKey, capSec, 2) }()
				}
			}

			// ── Per-minute window ─────────────────────────────────────────────
			if resolved.PerMin > 0 {
				epochMin := now / 60
				idxMin := rateLimitIndex(store, ctx.TenantID, apiRLId, ctx.EndpointRateLimitId, ctx.QuotaGroupID, epochMin, 1)
				ok, remMin := store.FixedWindowEpoch(idxMin, epochMin, resolved.PerMin)
				if emitQuotaHeaders {
					ctx.SetResponseHeader(hdrRLLimitMinute, fmtUint32(ctx, resolved.PerMin))
					ctx.SetResponseHeader(hdrRLRemainingMinute, fmtUint32(ctx, remMin))
				}
				if !ok {
					ctx.ResponseStatus = 429
					if emitQuotaHeaders {
						// Retry-After = seconds until end of current minute window
						secsUntilReset := 60 - (now % 60)
						ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, secsUntilReset))
					}
					return -1
				}

				// ASYNC: fire-and-forget background INCR for cross-pod visibility.
				if syncPolicy == 1 && remoteRL != nil {
					minKey := fmt.Sprintf("rl:%d:%d:%d:m", ctx.TenantID, apiRLId, epochMin)
					capMin := resolved.PerMin
					go func() { remoteRL.Check(minKey, capMin, 61) }()
				}
			}

			return s.PC + 1
		},
	}
}

// CheckRateLimitIP enforces a rate limit keyed by source IP rather than TenantID.
// ipSlot: ByteSlots index holding the client IP (set by bind_client_ip).
// The IP bytes are hashed directly — no string conversion, no allocation.
// Returns 429 when the limit is exceeded.
func CheckRateLimitIP(store *engine.CounterStore, ipSlot int, syncPolicy uint8,
	remoteRL engine.ExternalRateLimitProvider, emitQuotaHeaders bool) engine.Instruction {
	return engine.Instruction{
		Name: "CHECK_RATE_LIMIT_IP",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			reg := registry.State.Active.Load()
			resolved, blocked := registry.ResolveRateLimit(reg, ctx.CallerID,
				ctx.TenantID, ctx.APIRateLimitId, ctx.EndpointRateLimitId)
			if blocked {
				ctx.ResponseStatus = 403
				return -1
			}
			if resolved.PerSec == 0 && resolved.PerMin == 0 {
				return s.PC + 1
			}

			var ipBytes []byte
			if ipSlot >= 0 && ipSlot < len(ctx.ByteSlots) {
				ipBytes = ctx.ByteSlots[ipSlot]
			}
			h := ipHash32(ipBytes)

			now := uint32(time.Now().Unix())

			if resolved.PerSec > 0 {
				bf := resolved.BurstFactor
				if bf == 0 {
					bf = 100
				}
				effectiveSec := uint32(uint64(resolved.PerSec) * uint64(bf) / 100)
				if effectiveSec == 0 {
					effectiveSec = 1
				}
				idx := rateLimitIndexIP(store, h, uint32(ctx.APIRateLimitId), now, 0)
				ok, remSec := store.FixedWindowEpoch(idx, now, effectiveSec)
				if emitQuotaHeaders {
					ctx.SetResponseHeader(hdrRLLimitSecond, fmtUint32(ctx, effectiveSec))
					ctx.SetResponseHeader(hdrRLRemainingSecond, fmtUint32(ctx, remSec))
					ctx.SetResponseHeader(hdrRLReset, fmtUint32(ctx, now+1))
				}
				if !ok {
					ctx.ResponseStatus = 429
					if emitQuotaHeaders {
						ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, 1))
					}
					return -1
				}
			}
			if resolved.PerMin > 0 {
				epochMin := now / 60
				idx := rateLimitIndexIP(store, h, uint32(ctx.APIRateLimitId), epochMin, 1)
				ok, remMin := store.FixedWindowEpoch(idx, epochMin, resolved.PerMin)
				if emitQuotaHeaders {
					ctx.SetResponseHeader(hdrRLLimitMinute, fmtUint32(ctx, resolved.PerMin))
					ctx.SetResponseHeader(hdrRLRemainingMinute, fmtUint32(ctx, remMin))
				}
				if !ok {
					ctx.ResponseStatus = 429
					if emitQuotaHeaders {
						secsUntilReset := 60 - (now % 60)
						ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, secsUntilReset))
					}
					return -1
				}
			}
			return s.PC + 1
		},
	}
}

// ipHash32 hashes raw IP bytes using FNV-1a. Zero alloc — operates on the
// []byte directly without converting to string. ~5 ns for an IPv4 address.
func ipHash32(ip []byte) uint32 {
	h := uint32(2166136261)
	for _, b := range ip {
		h ^= uint32(b)
		h *= 16777619
	}
	return h
}

// rateLimitIndexIP hashes (ipHash, apiRLId, epoch, windowType) to a CounterStore
// slot. Uses Knuth multiplicative hashing — same pattern as rateLimitIndex.
func rateLimitIndexIP(store *engine.CounterStore, ipHash, apiRLId, epoch uint32, windowType uint8) uint32 {
	h := uint64(ipHash)*2654435761 ^
		uint64(apiRLId)*2246822519 ^
		uint64(epoch)*1000003 ^
		uint64(windowType)*2166136261
	return uint32(h) % uint32(len(store.Arena))
}

// CheckRateLimitSlot enforces a rate limit keyed by any runtime value from a slot.
// slotKey: ByteSlots index holding the rate limit key (e.g., user ID, client ID, device ID).
// The slot bytes are hashed directly — no string conversion, no allocation.
// Returns 429 when the limit is exceeded.
func CheckRateLimitSlot(store *engine.CounterStore, slotKey int, syncPolicy uint8,
	remoteRL engine.ExternalRateLimitProvider, emitQuotaHeaders bool) engine.Instruction {
	return engine.Instruction{
		Name: "CHECK_RATE_LIMIT_SLOT",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			reg := registry.State.Active.Load()
			resolved, blocked := registry.ResolveRateLimit(reg, ctx.CallerID,
				ctx.TenantID, ctx.APIRateLimitId, ctx.EndpointRateLimitId)
			if blocked {
				ctx.ResponseStatus = 403
				return -1
			}
			if resolved.PerSec == 0 && resolved.PerMin == 0 {
				return s.PC + 1
			}

			var keyBytes []byte
			if slotKey >= 0 && slotKey < len(ctx.ByteSlots) {
				keyBytes = ctx.ByteSlots[slotKey]
			}
			h := ipHash32(keyBytes) // reuse same hash function for any []byte

			now := uint32(time.Now().Unix())

			if resolved.PerSec > 0 {
				bf := resolved.BurstFactor
				if bf == 0 {
					bf = 100
				}
				effectiveSec := uint32(uint64(resolved.PerSec) * uint64(bf) / 100)
				if effectiveSec == 0 {
					effectiveSec = 1
				}
				idx := rateLimitIndexIP(store, h, uint32(ctx.APIRateLimitId), now, 0)
				ok, remSec := store.FixedWindowEpoch(idx, now, effectiveSec)
				if emitQuotaHeaders {
					ctx.SetResponseHeader(hdrRLLimitSecond, fmtUint32(ctx, effectiveSec))
					ctx.SetResponseHeader(hdrRLRemainingSecond, fmtUint32(ctx, remSec))
					ctx.SetResponseHeader(hdrRLReset, fmtUint32(ctx, now+1))
				}
				if !ok {
					ctx.ResponseStatus = 429
					if emitQuotaHeaders {
						ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, 1))
					}
					return -1
				}
			}
			if resolved.PerMin > 0 {
				epochMin := now / 60
				idx := rateLimitIndexIP(store, h, uint32(ctx.APIRateLimitId), epochMin, 1)
				ok, remMin := store.FixedWindowEpoch(idx, epochMin, resolved.PerMin)
				if emitQuotaHeaders {
					ctx.SetResponseHeader(hdrRLLimitMinute, fmtUint32(ctx, resolved.PerMin))
					ctx.SetResponseHeader(hdrRLRemainingMinute, fmtUint32(ctx, remMin))
				}
				if !ok {
					ctx.ResponseStatus = 429
					if emitQuotaHeaders {
						secsUntilReset := 60 - (now % 60)
						ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, secsUntilReset))
					}
					return -1
				}
			}
			return s.PC + 1
		},
	}
}

// CheckRateLimitGlobal enforces a rate limit with a tenant-agnostic counter key.
// Unlike CheckRateLimit (which keys counters on TenantID), this instruction uses a
// counter shared across ALL tenants hitting the same API/endpoint. Two different tenants
// consuming the same endpoint will deplete the same counter bucket.
//
// Counter key: hash(apiRLId, endpointRLId, epoch, windowType) — no TenantID component.
//
// Usage: add {"action": "check_rate_limit_global"} to a flow, or set
// rate_limit_mode: "global" on the API/endpoint config in the Studio.
//
// Returns 403 if the tenant is blocked; 429 if the shared window is exceeded.
// Quota headers and sync policies behave identically to CheckRateLimit.
func CheckRateLimitGlobal(store *engine.CounterStore, remoteRL engine.ExternalRateLimitProvider, syncPolicy uint8, emitQuotaHeaders bool) engine.Instruction {
	return engine.Instruction{
		Name: "CHECK_RATE_LIMIT_GLOBAL",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			reg := registry.State.Active.Load()

			// Still resolve the rate limit config (limits/burst) from registry, but
			// block check uses TenantID for the "is this tenant blocked" decision only.
			resolved, blocked := registry.ResolveRateLimit(
				reg,
				ctx.CallerID,
				ctx.TenantID,
				ctx.APIRateLimitId,
				ctx.EndpointRateLimitId,
			)

			if blocked {
				ctx.ResponseStatus = 403
				return -1
			}

			now := uint32(time.Now().Unix())

			// ── STRICT distributed enforcement ───────────────────────────────────
			if syncPolicy >= 2 && remoteRL != nil {
				if resolved.PerSec > 0 {
					bf := resolved.BurstFactor
					if bf == 0 {
						bf = 100
					}
					effectiveSec := uint32(uint64(resolved.PerSec) * uint64(bf) / 100)
					if effectiveSec == 0 {
						effectiveSec = 1
					}
					// Global key: no TenantID
					secKey := fmt.Sprintf("rlg:%d:%d:%d:s", ctx.APIRateLimitId, ctx.EndpointRateLimitId, now)
					ok, remSec := remoteRL.Check(secKey, effectiveSec, 2)
					if emitQuotaHeaders {
						ctx.SetResponseHeader(hdrRLLimitSecond, fmtUint32(ctx, effectiveSec))
						ctx.SetResponseHeader(hdrRLRemainingSecond, fmtUint32(ctx, remSec))
						ctx.SetResponseHeader(hdrRLReset, fmtUint32(ctx, now+1))
					}
					if !ok {
						ctx.ResponseStatus = 429
						if emitQuotaHeaders {
							ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, 1))
						}
						return -1
					}
				}

				if resolved.PerMin > 0 {
					epochMin := now / 60
					minKey := fmt.Sprintf("rlg:%d:%d:%d:m", ctx.APIRateLimitId, ctx.EndpointRateLimitId, epochMin)
					ok, remMin := remoteRL.Check(minKey, resolved.PerMin, 61)
					if emitQuotaHeaders {
						ctx.SetResponseHeader(hdrRLLimitMinute, fmtUint32(ctx, resolved.PerMin))
						ctx.SetResponseHeader(hdrRLRemainingMinute, fmtUint32(ctx, remMin))
					}
					if !ok {
						ctx.ResponseStatus = 429
						if emitQuotaHeaders {
							secsUntilReset := 60 - (now % 60)
							ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, secsUntilReset))
						}
						return -1
					}
				}

				return s.PC + 1
			}

			// ── Local fixed-window counters ───────────────────────────────────────

			// ── Per-second window ─────────────────────────────────────────────────
			if resolved.PerSec > 0 {
				bf := resolved.BurstFactor
				if bf == 0 {
					bf = 100
				}
				effectiveSec := uint32(uint64(resolved.PerSec) * uint64(bf) / 100)
				if effectiveSec == 0 {
					effectiveSec = 1
				}
				// Global index: no TenantID, no QuotaGroupID
				idxSec := rateLimitIndexGlobal(store, uint32(ctx.APIRateLimitId), uint32(ctx.EndpointRateLimitId), now, 0)
				ok, remSec := store.FixedWindowEpoch(idxSec, now, effectiveSec)
				if emitQuotaHeaders {
					ctx.SetResponseHeader(hdrRLLimitSecond, fmtUint32(ctx, effectiveSec))
					ctx.SetResponseHeader(hdrRLRemainingSecond, fmtUint32(ctx, remSec))
					ctx.SetResponseHeader(hdrRLReset, fmtUint32(ctx, now+1))
				}
				if !ok {
					ctx.ResponseStatus = 429
					if emitQuotaHeaders {
						ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, 1))
					}
					return -1
				}

				// ASYNC: fire-and-forget background INCR
				if syncPolicy == 1 && remoteRL != nil {
					secKey := fmt.Sprintf("rlg:%d:%d:%d:s", ctx.APIRateLimitId, ctx.EndpointRateLimitId, now)
					capSec := effectiveSec
					go func() { remoteRL.Check(secKey, capSec, 2) }()
				}
			}

			// ── Per-minute window ─────────────────────────────────────────────────
			if resolved.PerMin > 0 {
				epochMin := now / 60
				idxMin := rateLimitIndexGlobal(store, uint32(ctx.APIRateLimitId), uint32(ctx.EndpointRateLimitId), epochMin, 1)
				ok, remMin := store.FixedWindowEpoch(idxMin, epochMin, resolved.PerMin)
				if emitQuotaHeaders {
					ctx.SetResponseHeader(hdrRLLimitMinute, fmtUint32(ctx, resolved.PerMin))
					ctx.SetResponseHeader(hdrRLRemainingMinute, fmtUint32(ctx, remMin))
				}
				if !ok {
					ctx.ResponseStatus = 429
					if emitQuotaHeaders {
						secsUntilReset := 60 - (now % 60)
						ctx.SetResponseHeader(hdrRetryAfterRL, fmtUint32(ctx, secsUntilReset))
					}
					return -1
				}

				// ASYNC: fire-and-forget background INCR
				if syncPolicy == 1 && remoteRL != nil {
					epochMin := now / 60
					minKey := fmt.Sprintf("rlg:%d:%d:%d:m", ctx.APIRateLimitId, ctx.EndpointRateLimitId, epochMin)
					capMin := resolved.PerMin
					go func() { remoteRL.Check(minKey, capMin, 61) }()
				}
			}

			return s.PC + 1
		},
	}
}

// rateLimitIndexGlobal hashes (apiRLId, endpointRLId, epoch, windowType) to a CounterStore
// slot. TenantID is intentionally omitted — all tenants share the same counter bucket for
// a given API/endpoint configuration. Uses Knuth multiplicative hashing.
func rateLimitIndexGlobal(store *engine.CounterStore, apiRLId, endpointRLId, epoch uint32, windowType uint8) uint32 {
	h := uint64(apiRLId)*2654435761 ^
		uint64(endpointRLId)*3266489917 ^
		uint64(epoch)*1000003 ^
		uint64(windowType)*2166136261
	return uint32(h) % uint32(len(store.Arena))
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
