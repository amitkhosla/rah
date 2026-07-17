package steps

import (
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// CacheStore is the subset of cache.CacheManager used by cache instruction steps.
// Defined locally to avoid a circular import between steps and cache packages.
// cache.CacheManager satisfies this interface directly (SmartPointer = uint64).
type CacheStore interface {
	Get(tenantID uint16, key []byte) ([]byte, bool)
	Put(tenantID uint16, key []byte, value []byte, ttl uint32) (uint64, bool)
	Invalidate(tenantID uint16, key []byte) error
	// Advanced operations.
	Exists(tenantID uint16, key []byte) bool
	Incr(tenantID uint16, key []byte, delta int64, ttl uint32) (int64, bool)
	Touch(tenantID uint16, key []byte, ttl uint32) bool
}

// globalCacheTenantID is the tenant namespace for tenant-agnostic cache entries.
// tenantID 0 is reserved and never assigned to a real tenant.
const globalCacheTenantID uint16 = 0

// CacheGet looks up ByteSlots[keySlot] in the cache under ctx.TenantID.
// On hit:  writes the cached value into ByteSlots[destSlot].
// On miss: ByteSlots[destSlot] is left unchanged (empty if not previously set).
// Execution always continues to the next instruction â€” use an `if` step checking
// whether the dest slot is non-empty to branch on hit vs miss.
// In test mode ctx.TenantID is the ephemeral test tenant, so no special key
// namespacing is needed â€” isolation is handled by the tenant ID itself.
func CacheGet(store CacheStore, keySlot, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_get",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if pc, stop := StopIfCancelled(ctx); stop {
				return pc
			}
			key := ctx.ByteSlots[keySlot]
			if len(key) == 0 {
				return s.PC + 1
			}
			t0 := time.Now()
			val, ok := store.Get(ctx.TenantID, key)
			if ctx.Obs != nil {
				ctx.Obs.RecordCacheOp("cache_get", ok, time.Since(t0).Nanoseconds())
			}
			if ok {
				ctx.ByteSlots[destSlot] = val
			}
			return s.PC + 1
		},
	}
}

// CachePut queues a cache PUT into the per-request op buffer (async, fire-and-forget).
// The actual write is dispatched in a later batch_flush instruction or at request end.
// Skipped silently if key or value slot is empty.
// In test mode ctx.TenantID is the ephemeral test tenant, so writes are isolated
// by tenant ID and no key namespacing is required.
func CachePut(keySlot, valueSlot int, ttl uint32) engine.Instruction {
	return engine.Instruction{
		Name: "cache_put",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			val := ctx.ByteSlots[valueSlot]
			if len(key) > 0 && len(val) > 0 {
				rctx.EmitPut(ctx, key, val, rctx.TargetCache, true, ttl)
			}
			return s.PC + 1
		},
	}
}

// CacheGetGlobal looks up ByteSlots[keySlot] in the shared (tenant-agnostic) namespace.
// Behaviour is identical to CacheGet except tenantID=0 is used for all lookups,
// making the entry visible to all tenants.
// Test mode does not require special handling here â€” global cache reads are read-only
// against the shared namespace, and no test isolation is needed for reads.
func CacheGetGlobal(store CacheStore, keySlot, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_get_global",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if pc, stop := StopIfCancelled(ctx); stop {
				return pc
			}
			key := ctx.ByteSlots[keySlot]
			if len(key) == 0 {
				return s.PC + 1
			}
			t0 := time.Now()
			val, ok := store.Get(globalCacheTenantID, key)
			if ctx.Obs != nil {
				ctx.Obs.RecordCacheOp("cache_get_global", ok, time.Since(t0).Nanoseconds())
			}
			if ok {
				ctx.ByteSlots[destSlot] = val
			}
			return s.PC + 1
		},
	}
}

// CachePutGlobal queues a cache PUT into the shared (tenant-agnostic) namespace via the op buffer.
// The op is emitted with async=true (fire-and-forget); the flusher is responsible for dispatching.
// The TenantID captured in the StorageOp will be ctx.TenantID at emit time; the flusher must
// override it to globalCacheTenantID (0) when routing this op to the cache.
// Skipped silently if key or value slot is empty.
func CachePutGlobal(keySlot, valueSlot int, ttl uint32) engine.Instruction {
	return engine.Instruction{
		Name: "cache_put_global",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			val := ctx.ByteSlots[valueSlot]
			if len(key) > 0 && len(val) > 0 {
				// Temporarily override TenantID so the buffered op targets the global namespace.
				saved := ctx.TenantID
				ctx.TenantID = globalCacheTenantID
				rctx.EmitPut(ctx, key, val, rctx.TargetCache, true, ttl)
				ctx.TenantID = saved
			}
			return s.PC + 1
		},
	}
}

// CacheDelete removes ByteSlots[keySlot] from the cache under ctx.TenantID.
// Skipped silently if keySlot is empty. Calls Invalidate which removes the
// entry from the L1 index and the backend, and propagates the deletion to
// other gateway instances via OnInvalidate.
func CacheDelete(store CacheStore, keySlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_delete",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			if len(key) > 0 {
				_ = store.Invalidate(ctx.TenantID, key)
			}
			return s.PC + 1
		},
	}
}

// CacheDeleteGlobal removes ByteSlots[keySlot] from the shared (tenant-agnostic) namespace.
// Skipped silently if keySlot is empty. Affects all tenants that read from
// the shared cache using the same key.
func CacheDeleteGlobal(store CacheStore, keySlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_delete_global",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			if len(key) > 0 {
				_ = store.Invalidate(globalCacheTenantID, key)
			}
			return s.PC + 1
		},
	}
}

// CacheGetBatched queues a cache GET into the op buffer.
// The result is written to ByteSlots[destSlot] only after a batch_flush instruction executes.
// Use this when multiple cache lookups can be batched before their results are needed.
func CacheGetBatched(keySlot, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_get_batched",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			if len(key) == 0 {
				return s.PC + 1
			}
			rctx.EmitGet(ctx, key, rctx.TargetCache, destSlot)
			return s.PC + 1
		},
	}
}

// CacheGetGlobalBatched queues a cache GET in the shared (tenant-agnostic) namespace into the op buffer.
// The result is written to ByteSlots[destSlot] only after a batch_flush instruction executes.
// Use this when multiple cache lookups can be batched before their results are needed.
func CacheGetGlobalBatched(keySlot, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_get_global_batched",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			if len(key) == 0 {
				return s.PC + 1
			}
			// Temporarily override TenantID so the buffered op targets the global namespace.
			saved := ctx.TenantID
			ctx.TenantID = globalCacheTenantID
			rctx.EmitGet(ctx, key, rctx.TargetCache, destSlot)
			ctx.TenantID = saved
			return s.PC + 1
		},
	}
}

// CacheExists writes true to BoolSlots[resultSlot] if the key exists and is not expired.
// Does NOT check the backend â€” L1 only for speed.
func CacheExists(store CacheStore, keySlot, resultSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_exists",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if pc, stop := StopIfCancelled(ctx); stop {
				return pc
			}
			key := ctx.ByteSlots[keySlot]
			if len(key) == 0 {
				ctx.BoolSlots[resultSlot] = false
				return s.PC + 1
			}
			ctx.BoolSlots[resultSlot] = store.Exists(ctx.TenantID, key)
			return s.PC + 1
		},
	}
}

// CacheIncr atomically increments the int64 counter at keySlot by staticDelta,
// storing the updated value in IntSlots[resultSlot].
// If the key is absent or expired, it is initialised to staticDelta with the given TTL.
func CacheIncr(store CacheStore, keySlot, resultSlot int, staticDelta int64, ttl uint32) engine.Instruction {
	return engine.Instruction{
		Name: "cache_incr",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if pc, stop := StopIfCancelled(ctx); stop {
				return pc
			}
			key := ctx.ByteSlots[keySlot]
			if len(key) == 0 {
				return s.PC + 1
			}
			newVal, ok := store.Incr(ctx.TenantID, key, staticDelta, ttl)
			if ok {
				ctx.IntSlots[resultSlot] = newVal
			}
			return s.PC + 1
		},
	}
}

// CacheTouch refreshes the TTL of an existing cache entry without reading or writing its value.
// No-op if the key does not exist or is already expired.
func CacheTouch(store CacheStore, keySlot int, ttl uint32) engine.Instruction {
	return engine.Instruction{
		Name: "cache_touch",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			if len(key) > 0 {
				store.Touch(ctx.TenantID, key, ttl)
			}
			return s.PC + 1
		},
	}
}
