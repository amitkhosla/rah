package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
)

// CacheStore is the subset of cache.CacheManager used by cache instruction steps.
// Defined locally to avoid a circular import between steps and cache packages.
// cache.CacheManager satisfies this interface directly (SmartPointer = uint64).
type CacheStore interface {
	Get(tenantID uint16, key []byte) ([]byte, bool)
	Put(tenantID uint16, key []byte, value []byte, ttl uint32) (uint64, bool)
}

// globalCacheTenantID is the tenant namespace for tenant-agnostic cache entries.
// tenantID 0 is reserved and never assigned to a real tenant.
const globalCacheTenantID uint16 = 0

// CacheGet looks up ByteSlots[keySlot] in the cache under ctx.TenantID.
// On hit:  writes the cached value into ByteSlots[destSlot].
// On miss: ByteSlots[destSlot] is left unchanged (empty if not previously set).
// Execution always continues to the next instruction — use an `if` step checking
// whether the dest slot is non-empty to branch on hit vs miss.
func CacheGet(store CacheStore, keySlot, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_get",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			if len(key) == 0 {
				return s.PC + 1
			}
			if val, ok := store.Get(ctx.TenantID, key); ok {
				ctx.ByteSlots[destSlot] = val
			}
			return s.PC + 1
		},
	}
}

// CachePut queues a cache PUT into the per-request op buffer (async, fire-and-forget).
// The actual write is dispatched in a later batch_flush instruction or at request end.
// Skipped silently if key or value slot is empty.
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
func CacheGetGlobal(store CacheStore, keySlot, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "cache_get_global",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			if len(key) == 0 {
				return s.PC + 1
			}
			if val, ok := store.Get(globalCacheTenantID, key); ok {
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
