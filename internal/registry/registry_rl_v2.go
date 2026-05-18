package registry

import "sync"

// ─── V2 Rate Limit Config Store ───────────────────────────────────────────────

// globalRateLimitConfigsV2 stores all RateLimitConfigV2 objects by name.
// Written by the management server on POST /api/rate-limit-configs-v2;
// read by GetRateLimitConfigV2 at compile/bake time.
// Concurrent-safe via sync.Map.
var globalRateLimitConfigsV2 sync.Map // key: string (name) → value: RateLimitConfigV2

// UpsertRateLimitConfigV2 stores a V2 rate limit config by name.
func UpsertRateLimitConfigV2(cfg RateLimitConfigV2) {
	globalRateLimitConfigsV2.Store(cfg.Name, cfg)
}

// GetRateLimitConfigV2 returns the V2 rate limit config by name, or nil if not found.
func GetRateLimitConfigV2(name string) *RateLimitConfigV2 {
	v, ok := globalRateLimitConfigsV2.Load(name)
	if !ok {
		return nil
	}
	cfg := v.(RateLimitConfigV2)
	return &cfg
}

// DeleteRateLimitConfigV2 removes a V2 rate limit config by name.
func DeleteRateLimitConfigV2(name string) {
	globalRateLimitConfigsV2.Delete(name)
}

// ListRateLimitConfigsV2 returns all stored V2 rate limit configs.
func ListRateLimitConfigsV2() []RateLimitConfigV2 {
	var out []RateLimitConfigV2
	globalRateLimitConfigsV2.Range(func(_, v any) bool {
		out = append(out, v.(RateLimitConfigV2))
		return true
	})
	return out
}

// GetRateLimitConfigV2 returns the V2 rate limit config by name, or nil.
// This method is on RegistryManager so it can be called from the compiler
// via c.RegMgr.GetRateLimitConfigV2(name).
func (m *RegistryManager) GetRateLimitConfigV2(name string) *RateLimitConfigV2 {
	return GetRateLimitConfigV2(name)
}

// ─── Upstream Service Store ───────────────────────────────────────────────────

// globalUpstreamServices stores all UpstreamServiceDef objects by name.
// Concurrent-safe via sync.Map.
var globalUpstreamServices sync.Map // key: string (name) → value: UpstreamServiceDef

// UpsertUpstreamService stores an upstream service definition by name.
func UpsertUpstreamService(def UpstreamServiceDef) {
	globalUpstreamServices.Store(def.Name, def)
}

// GetUpstreamService returns an upstream service definition by name, or nil if not found.
func GetUpstreamService(name string) *UpstreamServiceDef {
	v, ok := globalUpstreamServices.Load(name)
	if !ok {
		return nil
	}
	def := v.(UpstreamServiceDef)
	return &def
}

// DeleteUpstreamService removes an upstream service definition by name.
func DeleteUpstreamService(name string) {
	globalUpstreamServices.Delete(name)
}

// ListUpstreamServices returns all stored upstream service definitions.
func ListUpstreamServices() []UpstreamServiceDef {
	var out []UpstreamServiceDef
	globalUpstreamServices.Range(func(_, v any) bool {
		out = append(out, v.(UpstreamServiceDef))
		return true
	})
	return out
}
