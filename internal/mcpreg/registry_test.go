package mcpreg

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

// === Functional Tests ===

// TestRegistry_UpsertGetServer_TenantScoped verifies that a server upserted
// with a non-zero tenantID is only visible to that tenant.
func TestRegistry_UpsertGetServer_TenantScoped(t *testing.T) {
	reg := NewRegistry()
	def := VirtualMCPServerDef{
		Name:        "myserver",
		Description: "Test server",
		TenantID:    5,
		Sources: []ToolSource{
			{Kind: ToolSourceAPI},
		},
	}

	reg.UpsertServer(def)

	// Verify tenant 5 can get it
	retrieved, ok := reg.GetServer(5, "myserver")
	if !ok {
		t.Errorf("expected GetServer(5, 'myserver') to return true, got false")
	}
	if retrieved.Name != "myserver" {
		t.Errorf("expected Name='myserver', got '%s'", retrieved.Name)
	}
	if retrieved.TenantID != 5 {
		t.Errorf("expected TenantID=5, got %d", retrieved.TenantID)
	}

	// Verify tenant 6 cannot get it
	_, ok = reg.GetServer(6, "myserver")
	if ok {
		t.Errorf("expected GetServer(6, 'myserver') to return false, got true")
	}

	// Verify global (tenantID=0) cannot get it
	_, ok = reg.GetServer(0, "myserver")
	if ok {
		t.Errorf("expected GetServer(0, 'myserver') to return false, got true")
	}
}

// TestRegistry_UpsertGetServer_Global verifies that a server upserted with
// tenantID=0 is visible to all tenants.
func TestRegistry_UpsertGetServer_Global(t *testing.T) {
	reg := NewRegistry()
	def := VirtualMCPServerDef{
		Name:        "globalserver",
		Description: "Global test server",
		TenantID:    0,
		Sources: []ToolSource{
			{Kind: ToolSourceAPI},
		},
	}

	reg.UpsertServer(def)

	// Verify any tenant can get it
	for _, tenantID := range []uint16{0, 1, 5, 100} {
		retrieved, ok := reg.GetServer(tenantID, "globalserver")
		if !ok {
			t.Errorf("expected GetServer(%d, 'globalserver') to return true, got false", tenantID)
		}
		if retrieved.Name != "globalserver" {
			t.Errorf("expected Name='globalserver', got '%s'", retrieved.Name)
		}
		if retrieved.TenantID != 0 {
			t.Errorf("expected TenantID=0, got %d", retrieved.TenantID)
		}
	}
}

// TestRegistry_TenantScopedTakesPriorityOverGlobal verifies that when both
// a global and tenant-scoped server exist with the same name, the tenant-scoped
// version is returned for that tenant.
func TestRegistry_TenantScopedTakesPriorityOverGlobal(t *testing.T) {
	reg := NewRegistry()

	// Upsert global version
	globalDef := VirtualMCPServerDef{
		Name:        "myserver",
		Description: "Global version",
		TenantID:    0,
		Sources: []ToolSource{
			{Kind: ToolSourceAPI},
		},
	}
	reg.UpsertServer(globalDef)

	// Upsert tenant-scoped version
	tenantDef := VirtualMCPServerDef{
		Name:        "myserver",
		Description: "Tenant 5 version",
		TenantID:    5,
		Sources: []ToolSource{
			{Kind: ToolSourceMCPAll},
		},
	}
	reg.UpsertServer(tenantDef)

	// Tenant 5 should get the tenant-scoped version
	retrieved, ok := reg.GetServer(5, "myserver")
	if !ok {
		t.Errorf("expected GetServer(5, 'myserver') to return true, got false")
	}
	if retrieved.Description != "Tenant 5 version" {
		t.Errorf("expected Description='Tenant 5 version', got '%s'", retrieved.Description)
	}

	// Tenant 6 should get the global version
	retrieved, ok = reg.GetServer(6, "myserver")
	if !ok {
		t.Errorf("expected GetServer(6, 'myserver') to return true, got false")
	}
	if retrieved.Description != "Global version" {
		t.Errorf("expected Description='Global version', got '%s'", retrieved.Description)
	}

	// Global (tenantID=0) should get the global version
	retrieved, ok = reg.GetServer(0, "myserver")
	if !ok {
		t.Errorf("expected GetServer(0, 'myserver') to return true, got false")
	}
	if retrieved.Description != "Global version" {
		t.Errorf("expected Description='Global version', got '%s'", retrieved.Description)
	}
}

// TestRegistry_ListServers_FiltersByTenant verifies that ListServers returns
// the correct combination of global and tenant-scoped servers for a given tenant.
func TestRegistry_ListServers_FiltersByTenant(t *testing.T) {
	reg := NewRegistry()

	// Upsert global servers
	globalServer1 := VirtualMCPServerDef{
		Name:     "global1",
		TenantID: 0,
	}
	globalServer2 := VirtualMCPServerDef{
		Name:     "global2",
		TenantID: 0,
	}
	reg.UpsertServer(globalServer1)
	reg.UpsertServer(globalServer2)

	// Upsert tenant 5 servers
	tenant5Server1 := VirtualMCPServerDef{
		Name:     "tenant5_1",
		TenantID: 5,
	}
	tenant5Server2 := VirtualMCPServerDef{
		Name:     "tenant5_2",
		TenantID: 5,
	}
	reg.UpsertServer(tenant5Server1)
	reg.UpsertServer(tenant5Server2)

	// Upsert tenant 10 server
	tenant10Server := VirtualMCPServerDef{
		Name:     "tenant10_1",
		TenantID: 10,
	}
	reg.UpsertServer(tenant10Server)

	// List for tenant 5 should return global + tenant 5 servers
	results := reg.ListServers(5)
	if len(results) != 4 {
		t.Errorf("expected 4 servers for tenant 5, got %d", len(results))
	}

	names := make(map[string]bool)
	for _, s := range results {
		names[s.Name] = true
	}
	expectedNames := map[string]bool{
		"global1":    true,
		"global2":    true,
		"tenant5_1":  true,
		"tenant5_2":  true,
	}
	if len(names) != len(expectedNames) {
		t.Errorf("expected names %v, got %v", expectedNames, names)
	}
	for name := range expectedNames {
		if !names[name] {
			t.Errorf("expected server '%s' in results, not found", name)
		}
	}

	// List for tenant 10 should return global + tenant 10 servers
	results = reg.ListServers(10)
	if len(results) != 3 {
		t.Errorf("expected 3 servers for tenant 10, got %d", len(results))
	}

	names = make(map[string]bool)
	for _, s := range results {
		names[s.Name] = true
	}
	expectedNames = map[string]bool{
		"global1":    true,
		"global2":    true,
		"tenant10_1": true,
	}
	if len(names) != len(expectedNames) {
		t.Errorf("expected names %v, got %v", expectedNames, names)
	}
	for name := range expectedNames {
		if !names[name] {
			t.Errorf("expected server '%s' in results, not found", name)
		}
	}

	// List all (tenantID=0) should return all servers
	results = reg.ListServers(0)
	if len(results) != 5 {
		t.Errorf("expected 5 servers total, got %d", len(results))
	}
}

// TestRegistry_DeleteServer verifies that deleting a server removes it and
// subsequent GetServer calls return false.
func TestRegistry_DeleteServer(t *testing.T) {
	reg := NewRegistry()
	def := VirtualMCPServerDef{
		Name:     "toDelete",
		TenantID: 5,
	}

	reg.UpsertServer(def)

	// Verify it exists
	_, ok := reg.GetServer(5, "toDelete")
	if !ok {
		t.Fatalf("expected server to exist after upsert")
	}

	// Delete it
	deleted := reg.DeleteServer(5, "toDelete")
	if !deleted {
		t.Errorf("expected DeleteServer(5, 'toDelete') to return true, got false")
	}

	// Verify it's gone
	_, ok = reg.GetServer(5, "toDelete")
	if ok {
		t.Errorf("expected GetServer to return false after delete, got true")
	}
}

// TestRegistry_UpsertGetAPITool verifies the round-trip of UpsertAPITool and GetAPITool.
func TestRegistry_UpsertGetAPITool(t *testing.T) {
	reg := NewRegistry()

	// Create a test APIToolDef
	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	tool := APIToolDef{
		Name:        "searchTool",
		Description: "Search endpoint",
		InputSchema: schema,
		Path:        "/v1/search",
		Method:      "POST",
		AuthKind:    "bearer",
		AuthKeyRef:  "secret:api-key",
	}

	reg.UpsertAPITool(tool)

	// Retrieve it
	retrieved, ok := reg.GetAPITool("searchTool")
	if !ok {
		t.Errorf("expected GetAPITool to return true, got false")
	}

	if retrieved.Name != "searchTool" {
		t.Errorf("expected Name='searchTool', got '%s'", retrieved.Name)
	}
	if retrieved.Description != "Search endpoint" {
		t.Errorf("expected Description='Search endpoint', got '%s'", retrieved.Description)
	}
	if retrieved.Path != "/v1/search" {
		t.Errorf("expected Path='/v1/search', got '%s'", retrieved.Path)
	}
	if retrieved.Method != "POST" {
		t.Errorf("expected Method='POST', got '%s'", retrieved.Method)
	}
	if string(retrieved.InputSchema) != string(schema) {
		t.Errorf("expected InputSchema=%s, got %s", schema, retrieved.InputSchema)
	}
}

// TestRegistry_ListAPITools verifies that ListAPITools returns all upserted API tools.
func TestRegistry_ListAPITools(t *testing.T) {
	reg := NewRegistry()

	tools := []APIToolDef{
		{
			Name:        "tool1",
			Description: "First tool",
			Path:        "/v1/tool1",
			Method:      "GET",
		},
		{
			Name:        "tool2",
			Description: "Second tool",
			Path:        "/v1/tool2",
			Method:      "POST",
		},
		{
			Name:        "tool3",
			Description: "Third tool",
			Path:        "/v1/tool3",
			Method:      "PUT",
		},
	}

	for _, tool := range tools {
		reg.UpsertAPITool(tool)
	}

	results := reg.ListAPITools()
	if len(results) != 3 {
		t.Errorf("expected 3 tools, got %d", len(results))
	}

	names := make(map[string]bool)
	for _, tool := range results {
		names[tool.Name] = true
	}

	for _, tool := range tools {
		if !names[tool.Name] {
			t.Errorf("expected tool '%s' in results, not found", tool.Name)
		}
	}
}

// === Negative Tests ===

// TestRegistry_GetServer_NotFound verifies that GetServer returns false
// for a server that was never upserted.
func TestRegistry_GetServer_NotFound(t *testing.T) {
	reg := NewRegistry()

	// Try to get a non-existent server
	_, ok := reg.GetServer(5, "nonexistent")
	if ok {
		t.Errorf("expected GetServer to return false for non-existent server, got true")
	}

	// Try global non-existent
	_, ok = reg.GetServer(0, "nonexistent")
	if ok {
		t.Errorf("expected GetServer to return false for global non-existent, got true")
	}
}

// TestRegistry_DeleteServer_NotFound verifies that DeleteServer returns false
// when trying to delete a server that doesn't exist.
func TestRegistry_DeleteServer_NotFound(t *testing.T) {
	reg := NewRegistry()

	// Try to delete a non-existent server
	deleted := reg.DeleteServer(5, "nonexistent")
	if deleted {
		t.Errorf("expected DeleteServer to return false for non-existent server, got true")
	}

	// Try to delete from global
	deleted = reg.DeleteServer(0, "nonexistent")
	if deleted {
		t.Errorf("expected DeleteServer to return false for global non-existent, got true")
	}
}

// TestRegistry_GetAPITool_NotFound verifies that GetAPITool returns false
// for a tool that was never upserted.
func TestRegistry_GetAPITool_NotFound(t *testing.T) {
	reg := NewRegistry()

	// Try to get a non-existent tool
	_, ok := reg.GetAPITool("nonexistent")
	if ok {
		t.Errorf("expected GetAPITool to return false for non-existent tool, got true")
	}
}

// TestRegistry_DeleteAPITool_NotFound verifies that DeleteAPITool returns false
// when trying to delete a tool that doesn't exist.
func TestRegistry_DeleteAPITool_NotFound(t *testing.T) {
	reg := NewRegistry()

	// Try to delete a non-existent tool
	deleted := reg.DeleteAPITool("nonexistent")
	if deleted {
		t.Errorf("expected DeleteAPITool to return false for non-existent tool, got true")
	}
}

// === Non-Functional Tests ===

// TestRegistry_TenantIsolation verifies that servers for one tenant are
// not visible to another tenant (non-zero tenantIDs).
func TestRegistry_TenantIsolation(t *testing.T) {
	reg := NewRegistry()

	// Create servers for different tenants
	tenantAServer := VirtualMCPServerDef{
		Name:     "tenant_server",
		TenantID: 100,
	}
	tenantBServer := VirtualMCPServerDef{
		Name:     "tenant_server",
		TenantID: 200,
	}

	reg.UpsertServer(tenantAServer)
	reg.UpsertServer(tenantBServer)

	// Tenant 100 should only see its own server
	retrieved, ok := reg.GetServer(100, "tenant_server")
	if !ok {
		t.Errorf("tenant 100 should see its own server")
	}
	if retrieved.TenantID != 100 {
		t.Errorf("expected TenantID=100, got %d", retrieved.TenantID)
	}

	// Tenant 200 should only see its own server
	retrieved, ok = reg.GetServer(200, "tenant_server")
	if !ok {
		t.Errorf("tenant 200 should see its own server")
	}
	if retrieved.TenantID != 200 {
		t.Errorf("expected TenantID=200, got %d", retrieved.TenantID)
	}

	// Tenant 300 should see neither
	_, ok = reg.GetServer(300, "tenant_server")
	if ok {
		t.Errorf("tenant 300 should not see servers from other tenants")
	}

	// Global (tenantID=0) should also see neither
	_, ok = reg.GetServer(0, "tenant_server")
	if ok {
		t.Errorf("global query should not see tenant-scoped servers")
	}
}

// TestRegistry_ConcurrentReadWrite verifies that the registry is safe for
// concurrent read and write operations with no data races.
func TestRegistry_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()

	// Pre-populate with some servers
	for i := range 10 {
		def := VirtualMCPServerDef{
			Name:     string(rune(i)),
			TenantID: uint16(i % 5),
		}
		reg.UpsertServer(def)
	}

	// Pre-populate with some API tools
	for i := range 10 {
		tool := APIToolDef{
			Name: "tool_" + string(rune(i)),
			Path: "/path/" + string(rune(i)),
		}
		reg.UpsertAPITool(tool)
	}

	var wg sync.WaitGroup
	var readCount atomic.Int64
	var writeCount atomic.Int64

	// 50 reader goroutines
	for i := range 50 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tenantID := uint16(idx % 5)

			// Read server
			_, _ = reg.GetServer(tenantID, "0")
			readCount.Add(1)

			// List servers
			_ = reg.ListServers(tenantID)
			readCount.Add(1)

			// Read API tool
			_, _ = reg.GetAPITool("tool_1")
			readCount.Add(1)

			// List API tools
			_ = reg.ListAPITools()
			readCount.Add(1)
		}(i)
	}

	// 5 writer goroutines
	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Upsert servers
			for j := range 10 {
				def := VirtualMCPServerDef{
					Name:     "writer_" + string(rune(idx)) + "_" + string(rune(j)),
					TenantID: uint16((idx + j) % 5),
				}
				reg.UpsertServer(def)
				writeCount.Add(1)
			}

			// Upsert API tools
			for j := range 10 {
				tool := APIToolDef{
					Name: "writer_tool_" + string(rune(idx)) + "_" + string(rune(j)),
				}
				reg.UpsertAPITool(tool)
				writeCount.Add(1)
			}

			// Delete a server if it exists
			if idx < 3 {
				_ = reg.DeleteServer(uint16(idx), string(rune(idx)))
			}

			// Delete an API tool if it exists
			if idx < 2 {
				_ = reg.DeleteAPITool("tool_" + string(rune(idx)))
			}
		}(i)
	}

	wg.Wait()

	// Verify operations completed
	if readCount.Load() == 0 {
		t.Errorf("expected read operations to complete, got 0")
	}
	if writeCount.Load() == 0 {
		t.Errorf("expected write operations to complete, got 0")
	}

	// Verify registry is in a consistent state
	allServers := reg.ListServers(0)
	if len(allServers) < 10 {
		t.Errorf("expected at least 10 servers after concurrent operations, got %d", len(allServers))
	}

	allTools := reg.ListAPITools()
	if len(allTools) < 10 {
		t.Errorf("expected at least 10 tools after concurrent operations, got %d", len(allTools))
	}
}
