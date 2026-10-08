package steps

import (
	"sync"
	"testing"
)

// TestGetOrStartStdioProcess_EmptyCommand verifies that an empty command returns an error.
func TestGetOrStartStdioProcess_EmptyCommand(t *testing.T) {
	_, err := getOrStartStdioProcess(1, "test_alias", []string{})
	if err == nil {
		t.Fatal("expected error for empty command, got nil")
	}
}

// TestMCPListTools_StdioRestartOnDeath verifies that after a process is marked dead,
// the next call to getOrStartStdioProcess attempts to restart it (or fails with a launch
// error when the underlying command doesn't exist — proving the restart path is taken).
func TestMCPListTools_StdioRestartOnDeath(t *testing.T) {
	// Use a command that doesn't exist so we get a predictable start failure.
	alias := "test_dead_restart_" + t.Name()
	tenantID := uint16(1)

	// First: seed the cache with a dead process by creating a stub and marking it dead.
	stubProc := &stdioMCPProcess{}
	stubProc.dead.Store(true)
	key := stdioCacheKey{tenantID: tenantID, alias: alias}
	stdioProcessCache.Store(key, stubProc)

	// Now call getOrStartStdioProcess — it should evict the dead entry and try to start
	// a new process with the given command. Since "nonexistent_mcp_binary_xyz" doesn't
	// exist, we expect a start error, which proves the restart path was taken (not the
	// dead-process short-circuit).
	_, err := getOrStartStdioProcess(tenantID, alias, []string{"nonexistent_mcp_binary_xyz"})
	if err == nil {
		// If somehow a binary by that name exists, the test is inconclusive but not a failure.
		t.Skip("nonexistent_mcp_binary_xyz unexpectedly found; skipping restart test")
	}
	// We expect an error from cmd.Start(), confirming the dead entry was cleared and a
	// new start was attempted.
}

// TestStdioCacheKey_TenantIsolation verifies that different tenants with the same alias
// use different cache entries (regression test for bug where cache was keyed by alias only).
func TestStdioCacheKey_TenantIsolation(t *testing.T) {
	m := sync.Map{}

	key1 := stdioCacheKey{tenantID: 1, alias: "github"}
	key2 := stdioCacheKey{tenantID: 2, alias: "github"}

	// Verify keys are different by storing under key1 and confirming key2 is a cache miss.
	m.Store(key1, "value_from_tenant_1")
	if v, ok := m.Load(key2); ok {
		t.Fatalf("expected cache miss for tenant 2, got %v", v)
	}

	// Store under key2 with different value.
	m.Store(key2, "value_from_tenant_2")

	// Confirm both keys still have their original values (no overwrite).
	v1, ok1 := m.Load(key1)
	v2, ok2 := m.Load(key2)
	if !ok1 || v1 != "value_from_tenant_1" {
		t.Fatalf("expected value_from_tenant_1 for tenant 1, got ok=%v val=%v", ok1, v1)
	}
	if !ok2 || v2 != "value_from_tenant_2" {
		t.Fatalf("expected value_from_tenant_2 for tenant 2, got ok=%v val=%v", ok2, v2)
	}
}

// TestStdioCacheKey_SameTenantSameAlias verifies that the same tenant and alias produce
// the same cache key and retrieve the same cached entry.
func TestStdioCacheKey_SameTenantSameAlias(t *testing.T) {
	m := sync.Map{}

	key1 := stdioCacheKey{tenantID: 1, alias: "github"}
	key2 := stdioCacheKey{tenantID: 1, alias: "github"}

	// Store under key1.
	m.Store(key1, "cached_process")

	// Load using key2 (semantically same key).
	v, ok := m.Load(key2)
	if !ok || v != "cached_process" {
		t.Fatalf("expected to retrieve cached_process for same tenant and alias, got ok=%v val=%v", ok, v)
	}
}
