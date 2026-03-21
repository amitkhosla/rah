package registry

import (
	"hash/maphash"
	"sync/atomic"
	"unsafe"
)

// ─── Alias Lookup (Hot Path) ──────────────────────────────────────────────────

// Lookup resolves an alias string to a TenantID.
// Zero allocation, lock-free, ~40–70 ns at 75% load factor.
//
// Uses a 64-bit maphash for fast slot selection and rejection, then falls back
// to a full string compare on hash match to guarantee collision safety.
func (t *AliasTable) Lookup(alias string) (uint16, bool) {
	if len(t.Slots) == 0 {
		return 0, false
	}
	h := maphash.String(t.Seed, alias)
	idx := uint32(h) & t.Mask
	for {
		slot := t.Slots[idx]
		if slot.TenantID == 0 {
			return 0, false // empty slot — alias is not in the table
		}
		if slot.Hash == h {
			// Full string compare guards against hash collisions.
			// AliasTable.StringArena holds no GC pointers, so no GC pressure here.
			stored := t.StringArena[slot.Offset : slot.Offset+uint32(slot.Len)]
			if string(stored) == alias {
				return slot.TenantID, true
			}
		}
		idx = (idx + 1) & t.Mask // linear probe to next slot
	}
}

// ─── General Config Lookup ────────────────────────────────────────────────────

// GetURLByKeyID retrieves a service URL using a pre-resolved KeyID — zero radix walk.
// keyID must have been obtained via RegistryManager.EnsureURLKeyID at bake time.
// Hot-path cost: 1 atomic load + 2 array bounds checks ≈ 2–5 ns.
func GetURLByKeyID(tenantID uint16, keyID uint16) ([]byte, bool) {
	reg := State.Active.Load()
	if reg == nil || reg.URLs.Stride == 0 {
		return nil, false
	}
	return getPropByKeyID(&reg.URLs, reg.ValuePool, tenantID, keyID)
}

// GetIDByKeyID retrieves an identifier using a pre-resolved KeyID — zero radix walk.
// keyID must have been obtained via RegistryManager.EnsureIDKeyID at bake time.
// Hot-path cost: 1 atomic load + 2 array bounds checks ≈ 2–5 ns.
func GetIDByKeyID(tenantID uint16, keyID uint16) ([]byte, bool) {
	reg := State.Active.Load()
	if reg == nil || reg.IDs.Stride == 0 {
		return nil, false
	}
	return getPropByKeyID(&reg.IDs, reg.ValuePool, tenantID, keyID)
}

// GetMetaByKeyID retrieves a metadata value using a pre-resolved KeyID — zero radix walk.
// keyID must have been obtained via RegistryManager.EnsureMetaKeyID at bake time.
// Hot-path cost: 1 atomic load + 2 array bounds checks ≈ 2–5 ns.
func GetMetaByKeyID(tenantID uint16, keyID uint16) ([]byte, bool) {
	reg := State.Active.Load()
	if reg == nil || reg.Meta.Stride == 0 {
		return nil, false
	}
	return getPropByKeyID(&reg.Meta, reg.ValuePool, tenantID, keyID)
}

// getPropByKeyID is the shared inner implementation for typed KeyID lookups.
// Lock-free: reads the store's Matrix with atomic.LoadUint32.
func getPropByKeyID(store *PropStore, valuePool [][]byte, tenantID uint16, keyID uint16) ([]byte, bool) {
	matrixIdx := uint32(tenantID)*store.Stride + uint32(keyID)
	if matrixIdx >= uint32(len(store.Matrix)) {
		return nil, false
	}
	vID := atomic.LoadUint32(&store.Matrix[matrixIdx])
	if vID == 0 || int(vID) >= len(valuePool) {
		return nil, false
	}
	return valuePool[vID], true
}

// GetStoreKeyID resolves a property key name to a KeyID within the given store.
// Management plane only — not called on the request hot path.
func GetStoreKeyID(store *PropStore, key string) (uint16, bool) {
	return findKeyID(store.Keys, store.StringPool, key)
}

// GetTenantID resolves an alias to a TenantID in the given registry snapshot.
func GetTenantID(reg *TenantRegistry, alias string) (uint16, bool) {
	return reg.Aliases.Lookup(alias)
}

// ─── Rate Limit Resolution (Hot Path) ─────────────────────────────────────────

// ResolveRateLimit returns the effective rate limit for a request.
//
// Resolution order (most-specific wins):
//  1. TenantModifiers[tenantID].Flags & TenantBlocked  → block all traffic
//  2. TenantRateLimits[tenantID] override for endpointRateLimitId (most specific)
//  3. TenantRateLimits[tenantID] override for apiRateLimitId
//  4. TenantModifiers[tenantID].ScalePct applied to RateLimitConfigs baseline
//  5. RateLimitConfigs[endpointRateLimitId].PerSec  (prefer endpoint over API)
//  6. RateLimitConfigs[apiRateLimitId].PerSec
//
// callerID is reserved for future AppKey-level resolution; pass 0 today.
// blocked=true means the request must be rejected with 403.
func ResolveRateLimit(reg *TenantRegistry, callerID uint32, tenantID, apiRateLimitId, endpointRateLimitId uint16) (limit ResolvedLimit, blocked bool) {
	if reg == nil {
		return ResolvedLimit{}, false
	}

	// ── Layer 2: tenant-wide modifier ────────────────────────────────────────
	var scalePct int16
	if int(tenantID) < len(reg.TenantModifiers) {
		mod := reg.TenantModifiers[tenantID]
		if mod.Flags&TenantBlocked != 0 {
			return ResolvedLimit{}, true
		}
		if mod.Flags&TenantRLDisabled != 0 {
			return ResolvedLimit{}, false // rate limiting off globally for this tenant
		}
		scalePct = mod.ScalePct
	}

	// ── Layer 3: sparse per-tenant overrides ─────────────────────────────────
	if int(tenantID) < len(reg.TenantRateLimits) {
		if tbl := reg.TenantRateLimits[tenantID]; tbl != nil {
			if endpointRateLimitId != 0 {
				if e, ok := tbl.find(endpointRateLimitId); ok {
					if e.Flags&RLBlocked != 0 {
						return ResolvedLimit{}, true
					}
					if e.Flags&RLDisabled != 0 {
						return ResolvedLimit{}, false
					}
					if e.Flags&RLCustomValue != 0 {
						bf := uint16(100)
						if int(endpointRateLimitId) < len(reg.RateLimitConfigs) {
							if cfg := reg.RateLimitConfigs[endpointRateLimitId]; cfg.BurstFactor > 0 {
								bf = cfg.BurstFactor
							}
						}
						return ResolvedLimit{
							PerSec:      applyScale(e.PerSec, scalePct),
							PerMin:      applyScale(e.PerMin, scalePct),
							BurstFactor: bf,
						}, false
					}
				}
			}
			if apiRateLimitId != 0 {
				if e, ok := tbl.find(apiRateLimitId); ok {
					if e.Flags&RLBlocked != 0 {
						return ResolvedLimit{}, true
					}
					if e.Flags&RLDisabled != 0 {
						return ResolvedLimit{}, false
					}
					if e.Flags&RLCustomValue != 0 {
						bf := uint16(100)
						if int(apiRateLimitId) < len(reg.RateLimitConfigs) {
							if cfg := reg.RateLimitConfigs[apiRateLimitId]; cfg.BurstFactor > 0 {
								bf = cfg.BurstFactor
							}
						}
						return ResolvedLimit{
							PerSec:      applyScale(e.PerSec, scalePct),
							PerMin:      applyScale(e.PerMin, scalePct),
							BurstFactor: bf,
						}, false
					}
				}
			}
		}
	}

	// ── Layer 1: global defaults ──────────────────────────────────────────────
	var baseCfg RateLimitConfig
	if endpointRateLimitId != 0 && int(endpointRateLimitId) < len(reg.RateLimitConfigs) {
		baseCfg = reg.RateLimitConfigs[endpointRateLimitId]
	}
	if baseCfg.PerSec == 0 && baseCfg.PerMin == 0 && apiRateLimitId != 0 && int(apiRateLimitId) < len(reg.RateLimitConfigs) {
		baseCfg = reg.RateLimitConfigs[apiRateLimitId]
	}
	bf := baseCfg.BurstFactor
	if bf == 0 {
		bf = 100
	}
	return ResolvedLimit{
		PerSec:      applyScale(baseCfg.PerSec, scalePct),
		PerMin:      applyScale(baseCfg.PerMin, scalePct),
		BurstFactor: bf,
	}, false
}

// find performs a binary search for policyID in the sorted PolicyIDs slice.
// ~3–6 comparisons for tables with 5–50 entries.
func (t *TenantRateLimitTable) find(policyID uint16) (TenantRateLimitEntry, bool) {
	lo, hi := 0, len(t.PolicyIDs)-1
	for lo <= hi {
		mid := (lo + hi) >> 1
		switch {
		case t.PolicyIDs[mid] == policyID:
			return t.Entries[mid], true
		case t.PolicyIDs[mid] < policyID:
			lo = mid + 1
		default:
			hi = mid - 1
		}
	}
	return TenantRateLimitEntry{}, false
}

// applyScale multiplies base by (100+scalePct)/100 using integer arithmetic only.
// scalePct==0 is the common case — fast single branch, no multiply.
func applyScale(base uint32, scalePct int16) uint32 {
	if scalePct == 0 {
		return base
	}
	return uint32(int64(base) * int64(100+scalePct) / 100)
}

// ─── Properties Radix (Management Plane) ─────────────────────────────────────

// findKeyID walks the Properties radix tree for an exact key match.
// Zero allocation: uses unsafe.Slice to view the string as []byte without copying.
func findKeyID(nodes []RegistryNode, pool []byte, input string) (uint16, bool) {
	if len(nodes) == 0 || len(input) == 0 {
		return 0, false
	}

	// View string as []byte with no allocation.
	inputBytes := unsafe.Slice(unsafe.StringData(input), len(input))
	currIdx := uint32(0)
	inputPos := 0
	inputLen := len(inputBytes)

	for {
		node := nodes[currIdx]
		prefix := pool[node.PrefixOffset : node.PrefixOffset+uint32(node.PrefixLen)]
		prefixLen := len(prefix)

		if inputPos+prefixLen > inputLen {
			return 0, false
		}
		for i := 0; i < prefixLen; i++ {
			if inputBytes[inputPos+i] != prefix[i] {
				return 0, false
			}
		}
		inputPos += prefixLen

		if inputPos == inputLen {
			return node.Value, true
		}

		nextChar := inputBytes[inputPos]
		foundChild := false

		for i := uint32(0); i < uint32(node.ChildCount); i++ {
			childIdx := node.ChildBase + i
			childNode := nodes[childIdx]
			if pool[childNode.PrefixOffset] == nextChar {
				currIdx = childIdx
				foundChild = true
				break
			}
		}

		if !foundChild {
			return 0, false
		}
	}
}
