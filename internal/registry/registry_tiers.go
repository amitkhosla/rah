package registry

// ─── TierStore ────────────────────────────────────────────────────────────────

// TierStore holds all named tier definitions.
// Written at bake time, read at hot path.
// Access is safe as long as writes happen only before hot-path reads begin
// (bake-time protocol — no concurrent mutation after bake).
type TierStore struct {
	tiers map[string]*TierDef // name → def; nil-safe
}

// NewTierStore creates an empty TierStore.
func NewTierStore() *TierStore { return &TierStore{tiers: make(map[string]*TierDef)} }

// Upsert adds or replaces a tier definition.
func (ts *TierStore) Upsert(def TierDef) {
	cp := def // copy
	ts.tiers[def.Name] = &cp
}

// Get returns the TierDef for the given name, or nil if not found.
func (ts *TierStore) Get(name string) *TierDef { return ts.tiers[name] }

// All returns all tier definitions (for listing).
func (ts *TierStore) All() []TierDef {
	out := make([]TierDef, 0, len(ts.tiers))
	for _, v := range ts.tiers {
		out = append(out, *v)
	}
	return out
}

// ─── Global Tier Store ────────────────────────────────────────────────────────

var globalTierStore = NewTierStore()

// ActiveTierStore returns the global tier store. Never nil.
func ActiveTierStore() *TierStore { return globalTierStore }

// UpsertTier adds or replaces a tier definition in the global store.
func UpsertTier(def TierDef) { globalTierStore.Upsert(def) }

// GetTier returns a tier definition by name, or nil if not found.
func GetTier(name string) *TierDef { return globalTierStore.Get(name) }

// ListTiers returns all tier definitions.
func ListTiers() []TierDef { return globalTierStore.All() }

// Delete removes a tier definition by name. No-op if the name is not found.
func (ts *TierStore) Delete(name string) { delete(ts.tiers, name) }

// DeleteTier removes a tier definition from the global store by name.
func DeleteTier(name string) { globalTierStore.Delete(name) }

// ─── Rate Limit Resolution — tier-based ──────────────────────────────────────

// RateLimitResolution is the result of resolving which rate limit config applies
// to a given (tenant, API) pair taking the tenant's tier into account.
type RateLimitResolution struct {
	ConfigName  string // rate-limit config name to use (empty = no tier-based limiting)
	OverallName string // cross-API overall quota config name (empty = none)
	Allowed     bool   // false = API is blocked for this tenant's tier
	Multiplier  uint32 // fixed-point ×100 (100 = 1.0×); 0 treated as 100 by callers
}

// ResolveTierRateLimit resolves which rate-limit config applies to (tenantID, apiName)
// based on the tenant's assigned tier.
//
// Resolution order:
//  1. Get tenant's tier name from meta["tier"].
//  2. If tier exists:
//     a. If apiName is in tier.BlockedAPIs → return Allowed=false.
//     b. If tier.AllowedAPIs is non-empty and apiName is NOT in it → return Allowed=false.
//     c. ConfigName = tier.ConfigName, OverallName = tier.OverallName.
//  3. Get tenant's multiplier from meta["rl_multiplier"] → fixed-point ×100 uint32.
//  4. Return resolution with Allowed=true.
//
// If no tier is assigned the function returns Allowed=true with empty config names,
// meaning the caller should fall back to its own policy.
func ResolveTierRateLimit(reg *TenantRegistry, tenantID uint16, apiName string) RateLimitResolution {
	tierName := TenantTierName(reg, tenantID)
	multiplier := TenantRLMultiplier(reg, tenantID)

	if tierName == "" {
		// No tier assigned — allow with default multiplier, no config names.
		return RateLimitResolution{Allowed: true, Multiplier: multiplier}
	}

	tier := globalTierStore.Get(tierName)
	if tier == nil {
		// Tier name is set but definition is missing — allow, no config override.
		return RateLimitResolution{Allowed: true, Multiplier: multiplier}
	}

	// Check blocked list first (explicit deny takes precedence).
	for _, blocked := range tier.BlockedAPIs {
		if blocked == apiName {
			return RateLimitResolution{Allowed: false, Multiplier: multiplier}
		}
	}

	// Check allow-list: if non-empty and apiName is not in it → deny.
	if len(tier.AllowedAPIs) > 0 {
		found := false
		for _, allowed := range tier.AllowedAPIs {
			if allowed == apiName {
				found = true
				break
			}
		}
		if !found {
			return RateLimitResolution{Allowed: false, Multiplier: multiplier}
		}
	}

	return RateLimitResolution{
		ConfigName:  tier.ConfigName,
		OverallName: tier.OverallName,
		Allowed:     true,
		Multiplier:  multiplier,
	}
}
