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

// CachePut stores ByteSlots[valueSlot] in the cache under key ByteSlots[keySlot]
// for ctx.TenantID with the given TTL in seconds.
// Skipped silently if key or value slot is empty.
func CachePut(store CacheStore, keySlot, valueSlot int, ttl uint32) engine.Instruction {
	return engine.Instruction{
		Name: "cache_put",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			val := ctx.ByteSlots[valueSlot]
			if len(key) > 0 && len(val) > 0 {
				store.Put(ctx.TenantID, key, val, ttl)
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

// CachePutGlobal stores ByteSlots[valueSlot] in the shared (tenant-agnostic) namespace.
// Behaviour is identical to CachePut except tenantID=0 is used, making the entry
// readable by all tenants via CacheGetGlobal.
func CachePutGlobal(store CacheStore, keySlot, valueSlot int, ttl uint32) engine.Instruction {
	return engine.Instruction{
		Name: "cache_put_global",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			key := ctx.ByteSlots[keySlot]
			val := ctx.ByteSlots[valueSlot]
			if len(key) > 0 && len(val) > 0 {
				store.Put(globalCacheTenantID, key, val, ttl)
			}
			return s.PC + 1
		},
	}
}
