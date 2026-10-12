package appversion_test

import (
	"context"
	"testing"

	"github.com/amitkhosla/rah/internal/appversion"
	"github.com/stretchr/testify/require"
)

// TestVersionIndex_FullLifecycle tests the complete lifecycle of version routing:
// set default rule, set tenant rule, resolve with priority, delete rules, verify passthrough.
func TestVersionIndex_FullLifecycle(t *testing.T) {
	idx := &appversion.VersionIndex{}

	// Step 1: Set default rule: (nil, "orders", "production") → "orders-v1"
	idx.Set(nil, "orders", "production", "orders-v1")

	// Step 2: Set tenant-5 rule: (&5, "orders", "production") → "orders-v2"
	tenantID := uint16(5)
	idx.Set(&tenantID, "orders", "production", "orders-v2")

	// Step 3: Assert tenant-specific rule takes priority
	resolved := idx.Resolve(5, "orders", "production")
	if resolved != "orders-v2" {
		t.Errorf("Step 3: expected orders-v2, got %q", resolved)
	}

	// Step 4: Assert different tenant hits default rule
	resolved = idx.Resolve(7, "orders", "production")
	if resolved != "orders-v1" {
		t.Errorf("Step 4: expected orders-v1 (default), got %q", resolved)
	}

	// Step 5: Assert app without rule returns logicalApp unchanged (passthrough)
	resolved = idx.Resolve(99, "payments", "production")
	if resolved != "payments" {
		t.Errorf("Step 5: expected payments (passthrough), got %q", resolved)
	}

	// Step 6: Delete tenant-5 rule
	idx.Delete(&tenantID, "orders", "production")

	// Step 7: Assert tenant-5 now hits default rule
	resolved = idx.Resolve(5, "orders", "production")
	if resolved != "orders-v1" {
		t.Errorf("Step 7: expected orders-v1 (default), got %q", resolved)
	}

	// Step 8: Delete default rule
	idx.Delete(nil, "orders", "production")

	// Step 9: Assert no rules → passthrough to logicalApp
	resolved = idx.Resolve(5, "orders", "production")
	if resolved != "orders" {
		t.Errorf("Step 9: expected orders (passthrough), got %q", resolved)
	}
}

// TestVersionIndex_PromoteTenantToDefault tests promoting a tenant's version to be the default.
func TestVersionIndex_PromoteTenantToDefault(t *testing.T) {
	idx := &appversion.VersionIndex{}

	// Step 1: Set tenant-5 rule → "orders-v2"
	tenantID := uint16(5)
	idx.Set(&tenantID, "orders", "production", "orders-v2")

	// Step 2: Set default rule → "orders-v1"
	idx.Set(nil, "orders", "production", "orders-v1")

	// Step 3: Promote tenant-5's version to default by setting default to "orders-v2"
	idx.Set(nil, "orders", "production", "orders-v2")

	// Step 4: Assert tenant-5 still resolves to orders-v2 (tenant rule still exists)
	resolved := idx.Resolve(5, "orders", "production")
	if resolved != "orders-v2" {
		t.Errorf("Step 4: expected orders-v2 (tenant rule), got %q", resolved)
	}

	// Step 5: Assert new tenant (tenant-7) resolves to orders-v2 (new default)
	resolved = idx.Resolve(7, "orders", "production")
	if resolved != "orders-v2" {
		t.Errorf("Step 5: expected orders-v2 (new default), got %q", resolved)
	}
}

// TestVersionIndex_MultipleApps tests that routing rules for different apps don't cross-pollinate.
func TestVersionIndex_MultipleApps(t *testing.T) {
	idx := &appversion.VersionIndex{}

	// Set rules for "orders" and "payments" independently
	idx.Set(nil, "orders", "production", "orders-v1")
	idx.Set(nil, "payments", "production", "payments-v1")

	tenantID := uint16(5)
	idx.Set(&tenantID, "orders", "production", "orders-v2")
	idx.Set(&tenantID, "payments", "production", "payments-v2")

	// Verify orders resolution
	if resolved := idx.Resolve(5, "orders", "production"); resolved != "orders-v2" {
		t.Errorf("tenant-5 orders: expected orders-v2, got %q", resolved)
	}
	if resolved := idx.Resolve(7, "orders", "production"); resolved != "orders-v1" {
		t.Errorf("tenant-7 orders: expected orders-v1, got %q", resolved)
	}

	// Verify payments resolution (different app, should not be affected by orders rules)
	if resolved := idx.Resolve(5, "payments", "production"); resolved != "payments-v2" {
		t.Errorf("tenant-5 payments: expected payments-v2, got %q", resolved)
	}
	if resolved := idx.Resolve(7, "payments", "production"); resolved != "payments-v1" {
		t.Errorf("tenant-7 payments: expected payments-v1, got %q", resolved)
	}
}

// TestVersionIndex_EnvironmentIsolation tests that routing rules are isolated by environment.
func TestVersionIndex_EnvironmentIsolation(t *testing.T) {
	idx := &appversion.VersionIndex{}

	// Set rule for env="production" → "orders-v1"
	idx.Set(nil, "orders", "production", "orders-v1")

	// Set rule for env="staging" → "orders-v2"
	idx.Set(nil, "orders", "staging", "orders-v2")

	// Verify production resolves to orders-v1
	if resolved := idx.Resolve(1, "orders", "production"); resolved != "orders-v1" {
		t.Errorf("production: expected orders-v1, got %q", resolved)
	}

	// Verify staging resolves to orders-v2
	if resolved := idx.Resolve(1, "orders", "staging"); resolved != "orders-v2" {
		t.Errorf("staging: expected orders-v2, got %q", resolved)
	}

	// Verify a non-existent environment returns passthrough
	if resolved := idx.Resolve(1, "orders", "development"); resolved != "orders" {
		t.Errorf("development (no rule): expected orders (passthrough), got %q", resolved)
	}
}

// TestMemoryStore_FullLifecycle tests the complete MemoryStore lifecycle:
// upsert versions, set routing rules, resolve routes, retire versions with validation.
func TestMemoryStore_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	store := appversion.NewMemoryStore()

	// Step 1: UpsertVersion: orders-v1 (active)
	err := store.UpsertVersion(ctx, appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "active",
	})
	if err != nil {
		t.Fatalf("Step 1: failed to upsert orders-v1: %v", err)
	}

	// Step 2: UpsertVersion: orders-v2 (draft)
	err = store.UpsertVersion(ctx, appversion.AppVersion{
		AppName:    "orders-v2",
		LogicalApp: "orders",
		Version:    "v2",
		Status:     "draft",
	})
	if err != nil {
		t.Fatalf("Step 2: failed to upsert orders-v2: %v", err)
	}

	// Step 3: UpsertRoutingRule: tenant-5 → "orders-v2"
	tenantID := uint16(5)
	err = store.UpsertRoutingRule(ctx, appversion.RoutingRule{
		TenantID:    &tenantID,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v2",
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("Step 3: failed to upsert routing rule: %v", err)
	}

	// Step 4: ResolveRoute(5, "orders", "production") == "orders-v2"
	resolved, err := store.ResolveRoute(ctx, 5, "orders", "production")
	if err != nil {
		t.Fatalf("Step 4: ResolveRoute failed: %v", err)
	}
	if resolved != "orders-v2" {
		t.Errorf("Step 4: expected orders-v2, got %q", resolved)
	}

	// Step 5: Try to retire orders-v2 → error (has routing rule)
	err = store.RetireVersion(ctx, "orders-v2")
	if err == nil {
		t.Error("Step 5: expected error when retiring version with active routing rule, got nil")
	}

	// Step 6: DeleteRoutingRule(&5, "orders", "production")
	err = store.DeleteRoutingRule(ctx, &tenantID, "orders", "production")
	if err != nil {
		t.Fatalf("Step 6: failed to delete routing rule: %v", err)
	}

	// Step 7: RetireVersion("orders-v2") → nil (now succeeds)
	err = store.RetireVersion(ctx, "orders-v2")
	if err != nil {
		t.Fatalf("Step 7: failed to retire orders-v2: %v", err)
	}

	// Step 8: ListVersions("orders") → both versions present; orders-v2 has status "retired"
	versions, err := store.ListVersions(ctx, "orders")
	if err != nil {
		t.Fatalf("Step 8: failed to list versions: %v", err)
	}

	if len(versions) != 2 {
		t.Errorf("Step 8: expected 2 versions, got %d", len(versions))
	}

	// Find orders-v2 and verify status
	var ordersV2 *appversion.AppVersion
	for i := range versions {
		if versions[i].AppName == "orders-v2" {
			ordersV2 = &versions[i]
			break
		}
	}

	if ordersV2 == nil {
		t.Fatal("Step 8: orders-v2 not found in ListVersions result")
	}

	if ordersV2.Status != "retired" {
		t.Errorf("Step 8: expected orders-v2 status=retired, got %q", ordersV2.Status)
	}
}

// TestMemoryStore_ResolveRoute_WithDefault tests ResolveRoute with default rule fallback.
func TestMemoryStore_ResolveRoute_WithDefault(t *testing.T) {
	ctx := context.Background()
	store := appversion.NewMemoryStore()

	// Upsert versions
	require.NoError(t, store.UpsertVersion(ctx, appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "active",
	}))

	// Set default rule (nil tenant)
	err := store.UpsertRoutingRule(ctx, appversion.RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("failed to set default rule: %v", err)
	}

	// Resolve for tenant with no specific rule → should use default
	resolved, err := store.ResolveRoute(ctx, 99, "orders", "production")
	if err != nil {
		t.Fatalf("ResolveRoute failed: %v", err)
	}
	if resolved != "orders-v1" {
		t.Errorf("expected orders-v1 (default), got %q", resolved)
	}

	// Resolve for app with no rules at all → should return logicalApp
	resolved, err = store.ResolveRoute(ctx, 99, "unknown-app", "production")
	if err != nil {
		t.Fatalf("ResolveRoute for unknown app failed: %v", err)
	}
	if resolved != "unknown-app" {
		t.Errorf("expected unknown-app (passthrough), got %q", resolved)
	}
}

// TestMemoryStore_TenantOverridesDefault verifies tenant rules override default rules.
func TestMemoryStore_TenantOverridesDefault(t *testing.T) {
	ctx := context.Background()
	store := appversion.NewMemoryStore()

	// Upsert versions
	require.NoError(t, store.UpsertVersion(ctx, appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "active",
	}))
	require.NoError(t, store.UpsertVersion(ctx, appversion.AppVersion{
		AppName:    "orders-v2",
		LogicalApp: "orders",
		Version:    "v2",
		Status:     "active",
	}))

	// Set default rule
	require.NoError(t, store.UpsertRoutingRule(ctx, appversion.RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}))

	// Set tenant-5 rule
	tenantID := uint16(5)
	require.NoError(t, store.UpsertRoutingRule(ctx, appversion.RoutingRule{
		TenantID:    &tenantID,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v2",
		Environment: "production",
	}))

	// Tenant-5 should resolve to orders-v2
	resolved, _ := store.ResolveRoute(ctx, 5, "orders", "production")
	if resolved != "orders-v2" {
		t.Errorf("tenant-5: expected orders-v2, got %q", resolved)
	}

	// Other tenants should resolve to orders-v1
	resolved, _ = store.ResolveRoute(ctx, 7, "orders", "production")
	if resolved != "orders-v1" {
		t.Errorf("tenant-7: expected orders-v1, got %q", resolved)
	}
}

// TestMemoryStore_ListRoutingRules verifies listing rules by logical app.
func TestMemoryStore_ListRoutingRules(t *testing.T) {
	ctx := context.Background()
	store := appversion.NewMemoryStore()

	// Create rules for multiple apps and tenants
	tenantID1 := uint16(1)
	tenantID2 := uint16(2)

	require.NoError(t, store.UpsertRoutingRule(ctx, appversion.RoutingRule{
		TenantID:    &tenantID1,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v2",
		Environment: "production",
	}))

	store.UpsertRoutingRule(ctx, appversion.RoutingRule{
		TenantID:    &tenantID2,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "staging",
	})

	store.UpsertRoutingRule(ctx, appversion.RoutingRule{
		TenantID:    nil,
		LogicalApp:  "payments",
		ActiveApp:   "payments-v1",
		Environment: "production",
	})

	// List rules for "orders" — should get 2
	ordersRules, err := store.ListRoutingRules(ctx, "orders")
	if err != nil {
		t.Fatalf("failed to list orders rules: %v", err)
	}
	if len(ordersRules) != 2 {
		t.Errorf("expected 2 orders rules, got %d", len(ordersRules))
	}

	// List rules for "payments" — should get 1
	paymentRules, err := store.ListRoutingRules(ctx, "payments")
	if err != nil {
		t.Fatalf("failed to list payment rules: %v", err)
	}
	if len(paymentRules) != 1 {
		t.Errorf("expected 1 payment rule, got %d", len(paymentRules))
	}
}

// TestVersionIndex_LoadAll tests atomic replacement of all rules at startup.
func TestVersionIndex_LoadAll(t *testing.T) {
	idx := &appversion.VersionIndex{}

	// Pre-populate with some rules
	idx.Set(nil, "orders", "production", "orders-v1")
	tenantID := uint16(5)
	idx.Set(&tenantID, "orders", "production", "orders-v2")

	// Verify pre-loaded state
	if resolved := idx.Resolve(5, "orders", "production"); resolved != "orders-v2" {
		t.Errorf("before LoadAll: expected orders-v2, got %q", resolved)
	}

	// LoadAll replaces everything with new rules
	newRules := []appversion.RoutingRule{
		{
			TenantID:    nil,
			LogicalApp:  "payments",
			ActiveApp:   "payments-v1",
			Environment: "production",
		},
		{
			TenantID:    &tenantID,
			LogicalApp:  "payments",
			ActiveApp:   "payments-v2",
			Environment: "production",
		},
	}
	idx.LoadAll(newRules)

	// Old rules should be gone
	if resolved := idx.Resolve(5, "orders", "production"); resolved != "orders" {
		t.Errorf("after LoadAll: orders should be passthrough, got %q", resolved)
	}

	// New rules should be in place
	if resolved := idx.Resolve(5, "payments", "production"); resolved != "payments-v2" {
		t.Errorf("after LoadAll: expected payments-v2, got %q", resolved)
	}
	if resolved := idx.Resolve(7, "payments", "production"); resolved != "payments-v1" {
		t.Errorf("after LoadAll: expected payments-v1, got %q", resolved)
	}
}
