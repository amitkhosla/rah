package registry

import (
	"strconv"
	"sync/atomic"
)

// ─── Tenant meta helpers ──────────────────────────────────────────────────────

// TenantTierName returns the tier name assigned to the tenant by reading
// meta["tier"] from the registry snapshot.
// Returns "" if the tenant has no tier assigned or the key is not found.
func TenantTierName(reg *TenantRegistry, tenantID uint16) string {
	if reg == nil || reg.Meta.Stride == 0 {
		return ""
	}
	keyID, found := findKeyID(reg.Meta.Keys, reg.Meta.StringPool, "tier")
	if !found {
		return ""
	}
	val, ok := getPropByKeyID(&reg.Meta, reg.ValuePool, tenantID, keyID)
	if !ok || len(val) == 0 {
		return ""
	}
	return string(val)
}

// TenantRLMultiplier returns the rate-limit multiplier for the tenant as a
// fixed-point ×100 uint32.  It reads meta["rl_multiplier"], parses it as a
// float64, and converts to ×100 (e.g. "1.5" → 150, "2.0" → 200).
// Returns 100 (= 1.0×) if the key is not set, empty, or cannot be parsed.
func TenantRLMultiplier(reg *TenantRegistry, tenantID uint16) uint32 {
	if reg == nil || reg.Meta.Stride == 0 {
		return 100
	}
	keyID, found := findKeyID(reg.Meta.Keys, reg.Meta.StringPool, "rl_multiplier")
	if !found {
		return 100
	}
	matrixIdx := uint32(tenantID)*reg.Meta.Stride + uint32(keyID)
	if matrixIdx >= uint32(len(reg.Meta.Matrix)) {
		return 100
	}
	vID := atomic.LoadUint32(&reg.Meta.Matrix[matrixIdx])
	if vID == 0 || int(vID) >= len(reg.ValuePool) {
		return 100
	}
	raw := reg.ValuePool[vID]
	if len(raw) == 0 {
		return 100
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || f <= 0 {
		return 100
	}
	m := uint32(f * 100)
	if m == 0 {
		return 100
	}
	return m
}

// TenantIDs returns the sorted slice of active tenant IDs from the manager.
// This is exposed so bake-time helpers (e.g. BakeMultipliers) can iterate over
// all known tenants without holding the manager mutex.
func (m *RegistryManager) TenantIDs() []uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]uint16, len(m.tenantIDs))
	copy(cp, m.tenantIDs)
	return cp
}
