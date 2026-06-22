package registry

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// resetGlobalState clears package-level globals between tests.
// nextAvailableTenantID must be reset because it is a global counter; without
// a reset successive tests get different TenantIDs, making uniqueness assertions
// fragile across orderings.
func resetGlobalState() {
	State.Active.Store(nil)
	State.FreeSlots = nil
	atomic.StoreUint32(&nextAvailableTenantID, 1)
}

// TestUpsertTenantStateCreatesAndRetrieves verifies that a single upsert with
// all three property maps populates the registry correctly and that the
// hot-path getters return the expected values.
func TestUpsertTenantStateCreatesAndRetrieves(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	m.UpsertTenantState(
		[]string{"acme"},
		map[string]string{"primary": "https://acme.com"},
		map[string]string{"api_key": "secret"},
		map[string]string{"tier": "pro"},
	)

	reg := State.Active.Load()
	if reg == nil {
		t.Fatal("State.Active is nil after UpsertTenantState")
	}

	tID, ok := reg.Aliases.Lookup("acme")
	if !ok || tID == 0 {
		t.Fatalf("alias 'acme' not found or tID==0 (got %d, ok=%v)", tID, ok)
	}

	urlKeyID := m.EnsureURLKeyID("primary")
	val, found := GetURLByKeyID(tID, urlKeyID)
	if !found || string(val) != "https://acme.com" {
		t.Fatalf("GetURLByKeyID: got %q found=%v, want 'https://acme.com'", val, found)
	}

	idKeyID := m.EnsureIDKeyID("api_key")
	val, found = GetIDByKeyID(tID, idKeyID)
	if !found || string(val) != "secret" {
		t.Fatalf("GetIDByKeyID: got %q found=%v, want 'secret'", val, found)
	}

	metaKeyID := m.EnsureMetaKeyID("tier")
	val, found = GetMetaByKeyID(tID, metaKeyID)
	if !found || string(val) != "pro" {
		t.Fatalf("GetMetaByKeyID: got %q found=%v, want 'pro'", val, found)
	}
}

// TestUpsertTenantStateMergesKeys verifies that a second upsert on the same
// alias accumulates keys rather than replacing the prior set.
func TestUpsertTenantStateMergesKeys(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	m.UpsertTenantState([]string{"acme"}, map[string]string{"primary": "url1"}, nil, nil)
	m.UpsertTenantState([]string{"acme"}, map[string]string{"secondary": "url2"}, nil, nil)

	reg := State.Active.Load()
	tID, ok := reg.Aliases.Lookup("acme")
	if !ok || tID == 0 {
		t.Fatal("alias 'acme' not found after second upsert")
	}

	primaryKeyID := m.EnsureURLKeyID("primary")
	val, found := GetURLByKeyID(tID, primaryKeyID)
	if !found || string(val) != "url1" {
		t.Fatalf("primary URL: got %q found=%v, want 'url1'", val, found)
	}

	secondaryKeyID := m.EnsureURLKeyID("secondary")
	val, found = GetURLByKeyID(tID, secondaryKeyID)
	if !found || string(val) != "url2" {
		t.Fatalf("secondary URL: got %q found=%v, want 'url2'", val, found)
	}
}

// TestUpsertTenantStateMultipleAliases verifies that all aliases in the batch
// resolve to the same TenantID.
func TestUpsertTenantStateMultipleAliases(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	aliases := []string{"acme", "acme-corp", "acme.com"}
	m.UpsertTenantState(aliases, nil, nil, nil)

	reg := State.Active.Load()
	if reg == nil {
		t.Fatal("State.Active is nil after UpsertTenantState")
	}

	first, ok := reg.Aliases.Lookup("acme")
	if !ok || first == 0 {
		t.Fatal("alias 'acme' not found")
	}
	for _, a := range aliases[1:] {
		tID, ok := reg.Aliases.Lookup(a)
		if !ok {
			t.Fatalf("alias %q not found", a)
		}
		if tID != first {
			t.Fatalf("alias %q maps to tID=%d, want %d (same as 'acme')", a, tID, first)
		}
	}
}

// TestUpsertTenantStateEmptyAliasesIsNoop verifies that calling UpsertTenantState
// with an empty aliases slice is a safe no-op.
func TestUpsertTenantStateEmptyAliasesIsNoop(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	m.UpsertTenantState([]string{}, map[string]string{"primary": "https://example.com"}, nil, nil)

	reg := State.Active.Load()
	if reg != nil {
		t.Fatal("State.Active should remain nil after noop upsert")
	}
	_ = m
}

// TestAddAliasToExistingTenant verifies that AddAlias links the new alias to the
// same TenantID as the existing one.
func TestAddAliasToExistingTenant(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	m.UpsertTenantState([]string{"acme"}, nil, nil, nil)

	reg := State.Active.Load()
	original, ok := reg.Aliases.Lookup("acme")
	if !ok || original == 0 {
		t.Fatal("alias 'acme' not found before AddAlias")
	}

	m.AddAlias("acme", "acme-new")

	reg = State.Active.Load()
	newID, ok := reg.Aliases.Lookup("acme-new")
	if !ok {
		t.Fatal("alias 'acme-new' not found after AddAlias")
	}
	if newID != original {
		t.Fatalf("AddAlias: 'acme-new' → tID=%d, want %d", newID, original)
	}
	existingID, ok := reg.Aliases.Lookup("acme")
	if !ok || existingID != original {
		t.Fatalf("original alias 'acme' should still resolve to %d, got %d ok=%v", original, existingID, ok)
	}
}

// TestAddAliasCreatesNewTenant verifies that AddAlias creates a new tenant when
// the existingAlias is not found, and that the new alias resolves successfully.
func TestAddAliasCreatesNewTenant(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	m.AddAlias("unknown-original", "brand-new")

	reg := State.Active.Load()
	if reg == nil {
		t.Fatal("State.Active is nil after AddAlias on empty manager")
	}
	tID, ok := reg.Aliases.Lookup("brand-new")
	if !ok || tID == 0 {
		t.Fatalf("'brand-new' not found or tID==0 (got %d, ok=%v)", tID, ok)
	}
}

// TestListTenantsCursorPagination verifies stable cursor-based pagination over
// a set of tenants, ensuring the union of all pages equals the full set.
func TestListTenantsCursorPagination(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	for i := 1; i <= 5; i++ {
		m.UpsertTenantState([]string{fmt.Sprintf("t%d", i)}, nil, nil, nil)
	}

	seen := make(map[uint16]bool)
	var cursor uint16

	page1, next1 := m.ListTenants(0, 2)
	if len(page1) != 2 {
		t.Fatalf("page1: want 2 summaries, got %d", len(page1))
	}
	if next1 == 0 {
		t.Fatal("page1: nextCursor should be non-zero when more pages exist")
	}
	for _, s := range page1 {
		seen[s.TenantID] = true
	}
	cursor = next1

	page2, next2 := m.ListTenants(cursor, 2)
	if len(page2) != 2 {
		t.Fatalf("page2: want 2 summaries, got %d", len(page2))
	}
	if next2 == 0 {
		t.Fatal("page2: nextCursor should be non-zero when more pages exist")
	}
	for _, s := range page2 {
		seen[s.TenantID] = true
	}
	cursor = next2

	page3, next3 := m.ListTenants(cursor, 10)
	if len(page3) != 1 {
		t.Fatalf("page3: want 1 summary, got %d", len(page3))
	}
	if next3 != 0 {
		t.Fatalf("page3: nextCursor should be 0 (last page), got %d", next3)
	}
	for _, s := range page3 {
		seen[s.TenantID] = true
	}

	if len(seen) != 5 {
		t.Fatalf("total unique tenants across pages: want 5, got %d", len(seen))
	}
}

// TestListTenantsEmptyManager verifies that listing an empty manager returns
// an empty slice and cursor=0.
func TestListTenantsEmptyManager(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	summaries, cursor := m.ListTenants(0, 10)
	if len(summaries) != 0 {
		t.Fatalf("want 0 summaries, got %d", len(summaries))
	}
	if cursor != 0 {
		t.Fatalf("want cursor=0, got %d", cursor)
	}
}

// TestDeleteTenantRemovesFromLookup verifies that after deletion the alias no
// longer resolves and the tenant is absent from ListTenants.
func TestDeleteTenantRemovesFromLookup(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	m.UpsertTenantState([]string{"acme"}, nil, nil, nil)
	m.DeleteTenant("acme")

	reg := State.Active.Load()
	if reg != nil {
		_, ok := reg.Aliases.Lookup("acme")
		if ok {
			t.Fatal("alias 'acme' still resolves after DeleteTenant")
		}
	}

	summaries, _ := m.ListTenants(0, 100)
	for _, s := range summaries {
		for _, a := range s.Aliases {
			if a == "acme" {
				t.Fatal("deleted tenant 'acme' still appears in ListTenants")
			}
		}
	}
}

// TestDeleteServiceURLRemovesKey verifies that after DeleteServiceURL the key
// is no longer accessible via GetURLByKeyID.
func TestDeleteServiceURLRemovesKey(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	m.UpsertTenantState([]string{"acme"}, map[string]string{"primary": "url1"}, nil, nil)

	reg := State.Active.Load()
	tID, ok := reg.Aliases.Lookup("acme")
	if !ok {
		t.Fatal("alias 'acme' not found before DeleteServiceURL")
	}
	urlKeyID := m.EnsureURLKeyID("primary")

	m.DeleteServiceURL("acme", "primary")

	val, found := GetURLByKeyID(tID, urlKeyID)
	if found {
		t.Fatalf("GetURLByKeyID: key should be absent after DeleteServiceURL, got %q", val)
	}
}

// TestEnsureURLKeyIDIsIdempotent verifies that calling EnsureURLKeyID twice with
// the same key returns the same KeyID both times.
func TestEnsureURLKeyIDIsIdempotent(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	id1 := m.EnsureURLKeyID("primary")
	id2 := m.EnsureURLKeyID("primary")
	if id1 != id2 {
		t.Fatalf("EnsureURLKeyID not idempotent: first call returned %d, second returned %d", id1, id2)
	}
}

// TestUpsertNamedRateLimitConfigRoundTrip verifies that a named rate limit
// config can be created, its ID retrieved, and its values read back.
func TestUpsertNamedRateLimitConfigRoundTrip(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	m.UpsertNamedRateLimitConfig("enterprise", RateLimitConfig{PerSec: 500, BurstFactor: 150})

	id, ok := m.GetRateLimitConfigId("enterprise")
	if !ok || id == 0 {
		t.Fatalf("GetRateLimitConfigId: got id=%d ok=%v, want non-zero id and ok=true", id, ok)
	}

	rec, ok := m.GetRateLimitConfig("enterprise")
	if !ok {
		t.Fatal("GetRateLimitConfig returned ok=false")
	}
	if rec.Config.PerSec != 500 {
		t.Fatalf("PerSec: got %d, want 500", rec.Config.PerSec)
	}
	if rec.Config.BurstFactor != 150 {
		t.Fatalf("BurstFactor: got %d, want 150", rec.Config.BurstFactor)
	}
}

// TestFullBakeAndLookupRoundTrip verifies end-to-end alias → tID → keyID →
// value resolution for multiple tenants, and confirms there is no cross-tenant
// leakage when one tenant has a key that another does not.
func TestFullBakeAndLookupRoundTrip(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)

	m := NewRegistryManager()
	tenants := []struct {
		alias string
		url   string
	}{
		{"tenant-a", "https://a.example.com"},
		{"tenant-b", "https://b.example.com"},
		{"tenant-c", "https://c.example.com"},
	}

	for _, tc := range tenants {
		m.UpsertTenantState([]string{tc.alias}, map[string]string{"endpoint": tc.url}, nil, nil)
	}

	keyID := m.EnsureURLKeyID("endpoint")

	reg := State.Active.Load()
	if reg == nil {
		t.Fatal("State.Active is nil after upserting tenants")
	}

	for _, tc := range tenants {
		tID, ok := reg.Aliases.Lookup(tc.alias)
		if !ok || tID == 0 {
			t.Fatalf("alias %q not found", tc.alias)
		}
		val, found := GetURLByKeyID(tID, keyID)
		if !found {
			t.Fatalf("tenant %q: GetURLByKeyID returned not found", tc.alias)
		}
		if string(val) != tc.url {
			t.Fatalf("tenant %q: got URL %q, want %q", tc.alias, val, tc.url)
		}
	}

	// Verify no cross-tenant leakage: a key unique to tenant-a should not
	// appear in the slot row for tenant-b (which has never set "exclusive-key").
	m.UpsertTenantState([]string{"tenant-a"}, map[string]string{"exclusive-key": "private"}, nil, nil)
	exclusiveKeyID := m.EnsureURLKeyID("exclusive-key")

	regAfter := State.Active.Load()
	tIDb, _ := regAfter.Aliases.Lookup("tenant-b")
	val, found := GetURLByKeyID(tIDb, exclusiveKeyID)
	if found {
		t.Fatalf("cross-tenant leakage: tenant-b got value %q for exclusive-key that belongs to tenant-a", val)
	}
}
