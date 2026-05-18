package registry

// ─── Bake-time wiring ─────────────────────────────────────────────────────────

// BakeMultipliers reads the rl_multiplier meta from every known tenant and
// calls setMultiplierFn(tenantID, multiplierX100) for each one.
//
// The function parameter decouples this package from the engine package and
// avoids an import cycle (engine already imports registry). Pass
// engine.SetTenantMultiplier as the callback at call sites:
//
//	registry.BakeMultipliers(reg, mgr, engine.SetTenantMultiplier)
//
// This must be called once at bake/startup time after UpsertTenantState has
// been called for all tenants. It is NOT safe for concurrent use — call it
// only from the single-goroutine bake path before the gateway starts serving
// requests.
func BakeMultipliers(reg *TenantRegistry, mgr *RegistryManager, setMultiplierFn func(tenantID uint16, multiplierX100 uint32)) {
	if reg == nil || mgr == nil || setMultiplierFn == nil {
		return
	}
	for _, tenantID := range mgr.TenantIDs() {
		m := TenantRLMultiplier(reg, tenantID)
		setMultiplierFn(tenantID, m)
	}
}
