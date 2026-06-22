package registry

import (
	"testing"
)

func TestAliasTableEmptyReturnsNotFound(t *testing.T) {
	table := &AliasTable{}
	id, found := table.Lookup("any")
	if found || id != 0 {
		t.Errorf("Lookup on empty table: got (%d, %v), want (0, false)", id, found)
	}
}

func TestAliasTableLookupKnownAlias(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.UpsertTenantState([]string{"alias1"}, nil, nil, nil)
	mgr.UpsertTenantState([]string{"alias2"}, nil, nil, nil)
	mgr.UpsertTenantState([]string{"alias3"}, nil, nil, nil)
	t.Cleanup(func() { State.Active.Store(nil) })

	reg := State.Active.Load()
	if reg == nil {
		t.Fatal("registry not populated")
	}

	id1, found1 := reg.Aliases.Lookup("alias1")
	id2, found2 := reg.Aliases.Lookup("alias2")
	id3, found3 := reg.Aliases.Lookup("alias3")

	if !found1 || id1 == 0 {
		t.Errorf("alias1: got (%d, %v), want non-zero TenantID and true", id1, found1)
	}
	if !found2 || id2 == 0 {
		t.Errorf("alias2: got (%d, %v), want non-zero TenantID and true", id2, found2)
	}
	if !found3 || id3 == 0 {
		t.Errorf("alias3: got (%d, %v), want non-zero TenantID and true", id3, found3)
	}

	if id1 == id2 || id2 == id3 || id1 == id3 {
		t.Errorf("TenantIDs not unique: alias1=%d, alias2=%d, alias3=%d", id1, id2, id3)
	}
}

func TestAliasTableUnknownAliasReturnsNotFound(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.UpsertTenantState([]string{"known"}, nil, nil, nil)
	t.Cleanup(func() { State.Active.Store(nil) })

	reg := State.Active.Load()
	if reg == nil {
		t.Fatal("registry not populated")
	}

	id, found := reg.Aliases.Lookup("unknown")
	if found || id != 0 {
		t.Errorf("Lookup unknown: got (%d, %v), want (0, false)", id, found)
	}
}

func TestGetURLByKeyIDReturnsValue(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.UpsertTenantState(
		[]string{"tenant1"},
		map[string]string{"primary": "https://api.example.com"},
		nil,
		nil,
	)
	t.Cleanup(func() { State.Active.Store(nil) })

	keyID := mgr.EnsureURLKeyID("primary")
	reg := State.Active.Load()
	tenantID, _ := reg.Aliases.Lookup("tenant1")

	val, found := GetURLByKeyID(tenantID, keyID)
	if !found {
		t.Errorf("GetURLByKeyID: not found")
	}
	if string(val) != "https://api.example.com" {
		t.Errorf("GetURLByKeyID: got %q, want %q", string(val), "https://api.example.com")
	}
}

func TestGetURLByKeyIDMissTenantReturnsNotFound(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.UpsertTenantState(
		[]string{"tenant1"},
		map[string]string{"primary": "https://api.example.com"},
		nil,
		nil,
	)
	t.Cleanup(func() { State.Active.Store(nil) })

	keyID := mgr.EnsureURLKeyID("primary")

	nonExistentTenantID := uint16(9999)
	val, found := GetURLByKeyID(nonExistentTenantID, keyID)
	if found {
		t.Errorf("GetURLByKeyID for non-existent tenant: got (%v, true), want (nil, false)", val)
	}
}

func TestGetIDByKeyIDAndMetaByKeyIDRoundTrip(t *testing.T) {
	mgr := NewRegistryManager()
	mgr.UpsertTenantState(
		[]string{"tenant1"},
		nil,
		map[string]string{"api_key": "secret123"},
		map[string]string{"tier": "premium"},
	)
	t.Cleanup(func() { State.Active.Store(nil) })

	idKeyID := mgr.EnsureIDKeyID("api_key")
	metaKeyID := mgr.EnsureMetaKeyID("tier")

	reg := State.Active.Load()
	tenantID, _ := reg.Aliases.Lookup("tenant1")

	idVal, idFound := GetIDByKeyID(tenantID, idKeyID)
	if !idFound || string(idVal) != "secret123" {
		t.Errorf("GetIDByKeyID: got (%q, %v), want (secret123, true)", string(idVal), idFound)
	}

	metaVal, metaFound := GetMetaByKeyID(tenantID, metaKeyID)
	if !metaFound || string(metaVal) != "premium" {
		t.Errorf("GetMetaByKeyID: got (%q, %v), want (premium, true)", string(metaVal), metaFound)
	}
}

func TestResolveRateLimitNilRegReturnsZero(t *testing.T) {
	limit, blocked := ResolveRateLimit(nil, 0, 1, 1, 0)
	if limit.PerSec != 0 || limit.PerMin != 0 || blocked {
		t.Errorf("ResolveRateLimit(nil): got (%+v, %v), want (zero, false)", limit, blocked)
	}
}

func TestResolveRateLimitGlobalDefault(t *testing.T) {
	reg := &TenantRegistry{
		RateLimitConfigs: make([]RateLimitConfig, 10),
		TenantModifiers:  make([]TenantRateLimitModifier, 10),
		TenantRateLimits: make([]*TenantRateLimitTable, 10),
	}
	reg.RateLimitConfigs[1] = RateLimitConfig{PerSec: 100, BurstFactor: 150}

	limit, blocked := ResolveRateLimit(reg, 0, 1, 1, 0)
	if blocked {
		t.Errorf("ResolveRateLimit: got blocked=true, want false")
	}
	if limit.PerSec != 100 {
		t.Errorf("ResolveRateLimit: got PerSec=%d, want 100", limit.PerSec)
	}
	if limit.BurstFactor != 150 {
		t.Errorf("ResolveRateLimit: got BurstFactor=%d, want 150", limit.BurstFactor)
	}
}

func TestResolveRateLimitTenantBlocked(t *testing.T) {
	reg := &TenantRegistry{
		RateLimitConfigs: make([]RateLimitConfig, 10),
		TenantModifiers:  make([]TenantRateLimitModifier, 10),
		TenantRateLimits: make([]*TenantRateLimitTable, 10),
	}
	reg.RateLimitConfigs[1] = RateLimitConfig{PerSec: 100, BurstFactor: 150}
	reg.TenantModifiers[1].Flags |= TenantBlocked

	_, blocked := ResolveRateLimit(reg, 0, 1, 1, 0)
	if !blocked {
		t.Errorf("ResolveRateLimit with TenantBlocked: got blocked=false, want true")
	}
}

func TestResolveRateLimitTenantRLDisabled(t *testing.T) {
	reg := &TenantRegistry{
		RateLimitConfigs: make([]RateLimitConfig, 10),
		TenantModifiers:  make([]TenantRateLimitModifier, 10),
		TenantRateLimits: make([]*TenantRateLimitTable, 10),
	}
	reg.RateLimitConfigs[1] = RateLimitConfig{PerSec: 100, BurstFactor: 150}
	reg.TenantModifiers[1].Flags |= TenantRLDisabled

	limit, blocked := ResolveRateLimit(reg, 0, 1, 1, 0)
	if blocked {
		t.Errorf("ResolveRateLimit with TenantRLDisabled: got blocked=true, want false")
	}
	if limit.PerSec != 0 || limit.PerMin != 0 {
		t.Errorf("ResolveRateLimit with TenantRLDisabled: got (%+v), want zero", limit)
	}
}

func TestResolveRateLimitPerTenantCustomValue(t *testing.T) {
	reg := &TenantRegistry{
		RateLimitConfigs: make([]RateLimitConfig, 10),
		TenantModifiers:  make([]TenantRateLimitModifier, 10),
		TenantRateLimits: make([]*TenantRateLimitTable, 10),
	}
	reg.RateLimitConfigs[1] = RateLimitConfig{PerSec: 100, BurstFactor: 150}

	tbl := &TenantRateLimitTable{
		PolicyIDs: []uint16{1},
		Entries: []TenantRateLimitEntry{
			{PerSec: 999, PerMin: 0, Flags: RLCustomValue},
		},
	}
	reg.TenantRateLimits[1] = tbl

	limit, blocked := ResolveRateLimit(reg, 0, 1, 1, 0)
	if blocked {
		t.Errorf("ResolveRateLimit with RLCustomValue: got blocked=true, want false")
	}
	if limit.PerSec != 999 {
		t.Errorf("ResolveRateLimit with RLCustomValue: got PerSec=%d, want 999", limit.PerSec)
	}
}

func TestResolveRateLimitScalePct(t *testing.T) {
	reg := &TenantRegistry{
		RateLimitConfigs: make([]RateLimitConfig, 10),
		TenantModifiers:  make([]TenantRateLimitModifier, 10),
		TenantRateLimits: make([]*TenantRateLimitTable, 10),
	}
	reg.RateLimitConfigs[1] = RateLimitConfig{PerSec: 100, BurstFactor: 100}
	reg.TenantModifiers[1].ScalePct = 50

	limit, blocked := ResolveRateLimit(reg, 0, 1, 1, 0)
	if blocked {
		t.Errorf("ResolveRateLimit with ScalePct: got blocked=true, want false")
	}
	expected := uint32(150)
	if limit.PerSec != expected {
		t.Errorf("ResolveRateLimit with ScalePct=50: got PerSec=%d, want %d", limit.PerSec, expected)
	}
}

func TestResolveRateLimitEndpointBeatsAPI(t *testing.T) {
	reg := &TenantRegistry{
		RateLimitConfigs: make([]RateLimitConfig, 10),
		TenantModifiers:  make([]TenantRateLimitModifier, 10),
		TenantRateLimits: make([]*TenantRateLimitTable, 10),
	}
	reg.RateLimitConfigs[1] = RateLimitConfig{PerSec: 100, BurstFactor: 100}
	reg.RateLimitConfigs[2] = RateLimitConfig{PerSec: 200, BurstFactor: 100}

	limit, blocked := ResolveRateLimit(reg, 0, 1, 1, 2)
	if blocked {
		t.Errorf("ResolveRateLimit: got blocked=true, want false")
	}
	if limit.PerSec != 200 {
		t.Errorf("ResolveRateLimit with endpoint and API: got PerSec=%d, want 200 (endpoint)", limit.PerSec)
	}
}
