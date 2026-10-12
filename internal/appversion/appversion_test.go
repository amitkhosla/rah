package appversion

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// Functional (Happy Path) Tests
// ============================================================================

// TestMemoryStore_UpsertAndGetVersion tests upserting and retrieving a version.
func TestMemoryStore_UpsertAndGetVersion(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	v := AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "1.0.0",
		Status:     "active",
		BundleHash: "abc123def456",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	err := store.UpsertVersion(ctx, v)
	if err != nil {
		t.Fatalf("UpsertVersion failed: %v", err)
	}

	retrieved, err := store.GetVersion(ctx, "orders-v1")
	if err != nil {
		t.Fatalf("GetVersion failed: %v", err)
	}

	if retrieved.AppName != v.AppName {
		t.Errorf("AppName mismatch: got %s, want %s", retrieved.AppName, v.AppName)
	}
	if retrieved.LogicalApp != v.LogicalApp {
		t.Errorf("LogicalApp mismatch: got %s, want %s", retrieved.LogicalApp, v.LogicalApp)
	}
	if retrieved.Version != v.Version {
		t.Errorf("Version mismatch: got %s, want %s", retrieved.Version, v.Version)
	}
	if retrieved.Status != v.Status {
		t.Errorf("Status mismatch: got %s, want %s", retrieved.Status, v.Status)
	}
	if retrieved.BundleHash != v.BundleHash {
		t.Errorf("BundleHash mismatch: got %s, want %s", retrieved.BundleHash, v.BundleHash)
	}
}

// TestMemoryStore_ListVersions tests listing versions by logical app.
func TestMemoryStore_ListVersions(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Insert 2 versions for "orders"
	ordersV1 := AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "1.0.0",
		Status:     "active",
	}
	ordersV2 := AppVersion{
		AppName:    "orders-v2",
		LogicalApp: "orders",
		Version:    "2.0.0",
		Status:     "active",
	}

	// Insert 1 version for "payments"
	paymentsV1 := AppVersion{
		AppName:    "payments-v1",
		LogicalApp: "payments",
		Version:    "1.0.0",
		Status:     "active",
	}

	if err := store.UpsertVersion(ctx, ordersV1); err != nil {
		t.Fatalf("UpsertVersion ordersV1 failed: %v", err)
	}
	if err := store.UpsertVersion(ctx, ordersV2); err != nil {
		t.Fatalf("UpsertVersion ordersV2 failed: %v", err)
	}
	if err := store.UpsertVersion(ctx, paymentsV1); err != nil {
		t.Fatalf("UpsertVersion paymentsV1 failed: %v", err)
	}

	// List versions for "orders"
	ordersVersions, err := store.ListVersions(ctx, "orders")
	if err != nil {
		t.Fatalf("ListVersions(orders) failed: %v", err)
	}

	if len(ordersVersions) != 2 {
		t.Errorf("Expected 2 orders versions, got %d", len(ordersVersions))
	}

	// List versions for "payments"
	paymentsVersions, err := store.ListVersions(ctx, "payments")
	if err != nil {
		t.Fatalf("ListVersions(payments) failed: %v", err)
	}

	if len(paymentsVersions) != 1 {
		t.Errorf("Expected 1 payments version, got %d", len(paymentsVersions))
	}
}

// TestMemoryStore_ResolveRoute_TenantSpecific tests tenant-specific routing rule priority.
func TestMemoryStore_ResolveRoute_TenantSpecific(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Set tenant-5 specific rule
	tenant5 := uint16(5)
	rule1 := RoutingRule{
		TenantID:    &tenant5,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v2",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(ctx, rule1); err != nil {
		t.Fatalf("UpsertRoutingRule tenant-5 failed: %v", err)
	}

	// Set default rule
	rule2 := RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(ctx, rule2); err != nil {
		t.Fatalf("UpsertRoutingRule default failed: %v", err)
	}

	// Resolve for tenant 5 should return the tenant-specific rule
	resolved, err := store.ResolveRoute(ctx, 5, "orders", "production")
	if err != nil {
		t.Fatalf("ResolveRoute failed: %v", err)
	}
	if resolved != "orders-v2" {
		t.Errorf("Expected orders-v2, got %s", resolved)
	}
}

// TestMemoryStore_ResolveRoute_DefaultFallback tests default rule fallback.
func TestMemoryStore_ResolveRoute_DefaultFallback(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Set only default rule
	rule := RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(ctx, rule); err != nil {
		t.Fatalf("UpsertRoutingRule failed: %v", err)
	}

	// Resolve for tenant 7 should return the default rule
	resolved, err := store.ResolveRoute(ctx, 7, "orders", "production")
	if err != nil {
		t.Fatalf("ResolveRoute failed: %v", err)
	}
	if resolved != "orders-v1" {
		t.Errorf("Expected orders-v1, got %s", resolved)
	}
}

// TestMemoryStore_UpsertRoutingRule_Updates tests that upserting the same rule updates it.
func TestMemoryStore_UpsertRoutingRule_Updates(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	tenant5 := uint16(5)

	// Upsert with orders-v1
	rule1 := RoutingRule{
		TenantID:    &tenant5,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(ctx, rule1); err != nil {
		t.Fatalf("First UpsertRoutingRule failed: %v", err)
	}

	// Upsert same key with orders-v2
	rule2 := RoutingRule{
		TenantID:    &tenant5,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v2",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(ctx, rule2); err != nil {
		t.Fatalf("Second UpsertRoutingRule failed: %v", err)
	}

	// ListRoutingRules should return exactly 1 rule with the latest value
	rules, err := store.ListRoutingRules(ctx, "orders")
	if err != nil {
		t.Fatalf("ListRoutingRules failed: %v", err)
	}

	if len(rules) != 1 {
		t.Errorf("Expected 1 rule, got %d", len(rules))
	}
	if rules[0].ActiveApp != "orders-v2" {
		t.Errorf("Expected orders-v2, got %s", rules[0].ActiveApp)
	}
}

// TestVersionIndex_TenantSpecific tests VersionIndex tenant-specific override.
func TestVersionIndex_TenantSpecific(t *testing.T) {
	vi := &VersionIndex{}

	tenant5 := uint16(5)
	// Set tenant override
	vi.Set(&tenant5, "orders", "production", "orders-v2")
	// Set default
	vi.Set(nil, "orders", "production", "orders-v1")

	// Resolve for tenant 5 should return override
	resolved := vi.Resolve(5, "orders", "production")
	if resolved != "orders-v2" {
		t.Errorf("Expected orders-v2, got %s", resolved)
	}

	// Resolve for tenant 7 should return default
	resolved = vi.Resolve(7, "orders", "production")
	if resolved != "orders-v1" {
		t.Errorf("Expected orders-v1, got %s", resolved)
	}
}

// TestVersionIndex_DefaultFallback tests VersionIndex default-only fallback.
func TestVersionIndex_DefaultFallback(t *testing.T) {
	vi := &VersionIndex{}

	// Set only default
	vi.Set(nil, "orders", "production", "orders-v1")

	// Resolve for any tenant should return default
	resolved := vi.Resolve(7, "orders", "production")
	if resolved != "orders-v1" {
		t.Errorf("Expected orders-v1, got %s", resolved)
	}
}

// TestVersionIndex_NoRule tests VersionIndex with no rules set.
func TestVersionIndex_NoRule(t *testing.T) {
	vi := &VersionIndex{}

	// Resolve with no rules should return logicalApp unchanged
	resolved := vi.Resolve(5, "orders", "production")
	if resolved != "orders" {
		t.Errorf("Expected orders, got %s", resolved)
	}
}

// TestVersionIndex_LoadAll tests VersionIndex LoadAll with multiple rules.
func TestVersionIndex_LoadAll(t *testing.T) {
	vi := &VersionIndex{}

	tenant5 := uint16(5)
	tenant7 := uint16(7)

	rules := []RoutingRule{
		{
			TenantID:    &tenant5,
			LogicalApp:  "orders",
			ActiveApp:   "orders-v2",
			Environment: "production",
		},
		{
			TenantID:    &tenant7,
			LogicalApp:  "orders",
			ActiveApp:   "orders-v3",
			Environment: "production",
		},
		{
			TenantID:    nil,
			LogicalApp:  "orders",
			ActiveApp:   "orders-v1",
			Environment: "production",
		},
	}

	vi.LoadAll(rules)

	// Verify tenant-5 override
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v2" {
		t.Errorf("Tenant 5: expected orders-v2, got %s", resolved)
	}
	// Verify tenant-7 override
	if resolved := vi.Resolve(7, "orders", "production"); resolved != "orders-v3" {
		t.Errorf("Tenant 7: expected orders-v3, got %s", resolved)
	}
	// Verify default fallback
	if resolved := vi.Resolve(99, "orders", "production"); resolved != "orders-v1" {
		t.Errorf("Tenant 99: expected orders-v1, got %s", resolved)
	}
}

// ============================================================================
// Negative / Error Path Tests
// ============================================================================

// TestMemoryStore_GetVersion_NotFound tests GetVersion with non-existent app.
func TestMemoryStore_GetVersion_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.GetVersion(ctx, "nonexistent")
	if err == nil {
		t.Errorf("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected error to contain 'not found', got: %v", err)
	}
}

// TestMemoryStore_RetireVersion_WithActiveRule tests retiring a version with active routing rule.
func TestMemoryStore_RetireVersion_WithActiveRule(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create a version
	v := AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "1.0.0",
		Status:     "active",
	}
	if err := store.UpsertVersion(ctx, v); err != nil {
		t.Fatalf("UpsertVersion failed: %v", err)
	}

	// Create a routing rule pointing to it
	rule := RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(ctx, rule); err != nil {
		t.Fatalf("UpsertRoutingRule failed: %v", err)
	}

	// Attempt to retire should fail
	err := store.RetireVersion(ctx, "orders-v1")
	if err == nil {
		t.Errorf("Expected error when retiring version with active rule")
	}
	if !strings.Contains(err.Error(), "routing rule") {
		t.Errorf("Expected error to contain 'routing rule', got: %v", err)
	}
}

// TestMemoryStore_RetireVersion_NotFound tests retiring a non-existent version.
func TestMemoryStore_RetireVersion_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.RetireVersion(ctx, "nonexistent")
	if err == nil {
		t.Errorf("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected error to contain 'not found', got: %v", err)
	}
}

// TestMemoryStore_ResolveRoute_NoRule tests ResolveRoute with no rules set.
func TestMemoryStore_ResolveRoute_NoRule(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Resolve with no rules should return the logicalApp string itself
	resolved, err := store.ResolveRoute(ctx, 5, "orders", "production")
	if err != nil {
		t.Fatalf("ResolveRoute failed: %v", err)
	}
	if resolved != "orders" {
		t.Errorf("Expected orders, got %s", resolved)
	}
}

// ============================================================================
// Non-Functional Tests
// ============================================================================

// TestVersionIndex_ConcurrentAccess tests concurrent access with race detection.
func TestVersionIndex_ConcurrentAccess(t *testing.T) {
	vi := &VersionIndex{}
	ctx := context.Background()

	var wg sync.WaitGroup
	var resolveCount, setCount atomic.Int64

	// 200 goroutines doing Resolve()
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(tenantID uint16) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = vi.Resolve(tenantID, "orders", "production")
				resolveCount.Add(1)
			}
		}(uint16(i % 10))
	}

	// 10 goroutines doing Set()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(tenantID uint16) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				appName := fmt.Sprintf("orders-v%d", j%3)
				vi.Set(&tenantID, "orders", "production", appName)
				setCount.Add(1)
			}
		}(uint16(i))
	}

	wg.Wait()

	if resolveCount.Load() != 10000 {
		t.Errorf("Expected 10000 resolves, got %d", resolveCount.Load())
	}
	if setCount.Load() != 1000 {
		t.Errorf("Expected 1000 sets, got %d", setCount.Load())
	}

	_ = ctx // suppress unused warning
}

// TestVersionIndex_BlastRadius tests that changing one tenant doesn't affect another.
func TestVersionIndex_BlastRadius(t *testing.T) {
	vi := &VersionIndex{}

	tenant5 := uint16(5)
	tenant7 := uint16(7)

	// Set different versions for tenant-5 and tenant-7
	vi.Set(&tenant5, "orders", "production", "orders-v2")
	vi.Set(&tenant7, "orders", "production", "orders-v1")

	// Verify initial state
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v2" {
		t.Errorf("Initial tenant-5: expected orders-v2, got %s", resolved)
	}
	if resolved := vi.Resolve(7, "orders", "production"); resolved != "orders-v1" {
		t.Errorf("Initial tenant-7: expected orders-v1, got %s", resolved)
	}

	// Change tenant-5
	vi.Set(&tenant5, "orders", "production", "orders-v3")

	// Tenant-7 should be unaffected
	if resolved := vi.Resolve(7, "orders", "production"); resolved != "orders-v1" {
		t.Errorf("After change: tenant-7 should still be orders-v1, got %s", resolved)
	}

	// Tenant-5 should be updated
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v3" {
		t.Errorf("After change: tenant-5 should be orders-v3, got %s", resolved)
	}
}

// TestVersionIndex_Delete tests Set + Delete a rule with fallback to default.
func TestVersionIndex_Delete(t *testing.T) {
	vi := &VersionIndex{}

	tenant5 := uint16(5)

	// Set tenant-5 override
	vi.Set(&tenant5, "orders", "production", "orders-v2")
	// Set default
	vi.Set(nil, "orders", "production", "orders-v1")

	// Verify tenant-5 override works
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v2" {
		t.Errorf("Before delete: expected orders-v2, got %s", resolved)
	}

	// Delete the tenant-5 rule
	vi.Delete(&tenant5, "orders", "production")

	// Should now fall back to default
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v1" {
		t.Errorf("After delete: expected orders-v1, got %s", resolved)
	}
}

// TestMemoryStore_DeleteRoutingRule tests deleting a routing rule.
func TestMemoryStore_DeleteRoutingRule(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	tenant5 := uint16(5)

	// Upsert a rule
	rule := RoutingRule{
		TenantID:    &tenant5,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v2",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(ctx, rule); err != nil {
		t.Fatalf("UpsertRoutingRule failed: %v", err)
	}

	// Verify it exists
	rules, err := store.ListRoutingRules(ctx, "orders")
	if err != nil {
		t.Fatalf("ListRoutingRules failed: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("Expected 1 rule before delete, got %d", len(rules))
	}

	// Delete it
	if err := store.DeleteRoutingRule(ctx, &tenant5, "orders", "production"); err != nil {
		t.Fatalf("DeleteRoutingRule failed: %v", err)
	}

	// Verify it's gone
	rules, err = store.ListRoutingRules(ctx, "orders")
	if err != nil {
		t.Fatalf("ListRoutingRules failed: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("Expected 0 rules after delete, got %d", len(rules))
	}
}

// TestMemoryStore_RetireVersion_Success tests successfully retiring a version.
func TestMemoryStore_RetireVersion_Success(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create a version
	v := AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "1.0.0",
		Status:     "active",
	}
	if err := store.UpsertVersion(ctx, v); err != nil {
		t.Fatalf("UpsertVersion failed: %v", err)
	}

	// Retire it (no routing rules point to it)
	if err := store.RetireVersion(ctx, "orders-v1"); err != nil {
		t.Fatalf("RetireVersion failed: %v", err)
	}

	// Verify status changed to "retired"
	retired, err := store.GetVersion(ctx, "orders-v1")
	if err != nil {
		t.Fatalf("GetVersion failed: %v", err)
	}
	if retired.Status != "retired" {
		t.Errorf("Expected status retired, got %s", retired.Status)
	}
}

// TestVersionIndex_LoadAll_ReplacesPrevious tests that LoadAll clears previous entries.
func TestVersionIndex_LoadAll_ReplacesPrevious(t *testing.T) {
	vi := &VersionIndex{}

	tenant5 := uint16(5)

	// Set initial entry
	vi.Set(&tenant5, "orders", "production", "orders-v1")

	// Verify it's there
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v1" {
		t.Errorf("Before LoadAll: expected orders-v1, got %s", resolved)
	}

	// LoadAll with different rules
	rules := []RoutingRule{
		{
			TenantID:    nil,
			LogicalApp:  "orders",
			ActiveApp:   "orders-v2",
			Environment: "production",
		},
	}
	vi.LoadAll(rules)

	// Old tenant-5 entry should be gone, tenant 5 should resolve to default
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v2" {
		t.Errorf("After LoadAll: expected orders-v2, got %s", resolved)
	}
}

// TestMemoryStore_UpsertVersion_TimestampAutofill tests CreatedAt and UpdatedAt autofill.
func TestMemoryStore_UpsertVersion_TimestampAutofill(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Upsert without timestamps
	v := AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "1.0.0",
		Status:     "active",
	}

	before := time.Now()
	if err := store.UpsertVersion(ctx, v); err != nil {
		t.Fatalf("UpsertVersion failed: %v", err)
	}
	after := time.Now()

	retrieved, err := store.GetVersion(ctx, "orders-v1")
	if err != nil {
		t.Fatalf("GetVersion failed: %v", err)
	}

	// CreatedAt should be set
	if retrieved.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should not be zero")
	}
	if retrieved.CreatedAt.Before(before) || retrieved.CreatedAt.After(after) {
		t.Errorf("CreatedAt not within expected range")
	}

	// UpdatedAt should be set
	if retrieved.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt should not be zero")
	}
	if retrieved.UpdatedAt.Before(before) || retrieved.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt not within expected range")
	}
}

// TestMemoryStore_UpsertVersion_PreservesCreatedAt tests that CreatedAt is preserved on update.
func TestMemoryStore_UpsertVersion_PreservesCreatedAt(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	originalTime := time.Now().Add(-1 * time.Hour)

	// First upsert
	v1 := AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "1.0.0",
		Status:     "active",
		CreatedAt:  originalTime,
		UpdatedAt:  originalTime,
	}

	if err := store.UpsertVersion(ctx, v1); err != nil {
		t.Fatalf("First UpsertVersion failed: %v", err)
	}

	// Second upsert (update) with different time
	newTime := time.Now()
	v2 := AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "1.0.0",
		Status:     "active",
		UpdatedAt:  newTime,
		// CreatedAt intentionally left zero to test preservation
	}

	if err := store.UpsertVersion(ctx, v2); err != nil {
		t.Fatalf("Second UpsertVersion failed: %v", err)
	}

	retrieved, err := store.GetVersion(ctx, "orders-v1")
	if err != nil {
		t.Fatalf("GetVersion failed: %v", err)
	}

	// CreatedAt should still be the original time
	if !retrieved.CreatedAt.Equal(originalTime) {
		t.Errorf("CreatedAt should be preserved, expected %v, got %v", originalTime, retrieved.CreatedAt)
	}
}

// TestMemoryStore_ListVersions_Empty tests listing versions when none exist.
func TestMemoryStore_ListVersions_Empty(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	versions, err := store.ListVersions(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("ListVersions failed: %v", err)
	}

	if len(versions) != 0 {
		t.Errorf("Expected 0 versions, got %d", len(versions))
	}
}

// TestMemoryStore_ListRoutingRules_Empty tests listing rules when none exist.
func TestMemoryStore_ListRoutingRules_Empty(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	rules, err := store.ListRoutingRules(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("ListRoutingRules failed: %v", err)
	}

	if len(rules) != 0 {
		t.Errorf("Expected 0 rules, got %d", len(rules))
	}
}

// TestMemoryStore_DeleteRoutingRule_NoMatch tests deleting a non-existent rule (should not error).
func TestMemoryStore_DeleteRoutingRule_NoMatch(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	tenant5 := uint16(5)

	// Try to delete a rule that doesn't exist - should not error
	err := store.DeleteRoutingRule(ctx, &tenant5, "orders", "production")
	if err != nil {
		t.Fatalf("DeleteRoutingRule returned unexpected error: %v", err)
	}
}

// TestVersionIndex_Resolve_DifferentEnvironments tests resolution with different environments.
func TestVersionIndex_Resolve_DifferentEnvironments(t *testing.T) {
	vi := &VersionIndex{}

	tenant5 := uint16(5)

	// Set different versions for different environments
	vi.Set(&tenant5, "orders", "production", "orders-v2")
	vi.Set(&tenant5, "orders", "staging", "orders-v1")

	// Verify production
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v2" {
		t.Errorf("Production: expected orders-v2, got %s", resolved)
	}

	// Verify staging
	if resolved := vi.Resolve(5, "orders", "staging"); resolved != "orders-v1" {
		t.Errorf("Staging: expected orders-v1, got %s", resolved)
	}
}

// TestVersionIndex_Resolve_DifferentLogicalApps tests resolution with different logical apps.
func TestVersionIndex_Resolve_DifferentLogicalApps(t *testing.T) {
	vi := &VersionIndex{}

	tenant5 := uint16(5)

	// Set different versions for different logical apps
	vi.Set(&tenant5, "orders", "production", "orders-v2")
	vi.Set(&tenant5, "payments", "production", "payments-v1")

	// Verify orders
	if resolved := vi.Resolve(5, "orders", "production"); resolved != "orders-v2" {
		t.Errorf("Orders: expected orders-v2, got %s", resolved)
	}

	// Verify payments
	if resolved := vi.Resolve(5, "payments", "production"); resolved != "payments-v1" {
		t.Errorf("Payments: expected payments-v1, got %s", resolved)
	}
}
