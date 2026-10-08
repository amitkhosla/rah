package steps

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestMCPServCacheKey_TenantIsolation verifies that different tenants with the same server alias
// use different cache entries (regression test for bug where cache was keyed by alias only).
func TestMCPServCacheKey_TenantIsolation(t *testing.T) {
	m := sync.Map{}

	key1 := mcpServCacheKey{tenantID: 1, alias: "tools"}
	key2 := mcpServCacheKey{tenantID: 2, alias: "tools"}

	// Verify keys are different by storing under key1 and confirming key2 is a cache miss.
	m.Store(key1, &mcpServCachedTools{tools: []mcpServRawTool{{Name: "tool_from_tenant_1"}}})
	if v, ok := m.Load(key2); ok {
		t.Fatalf("expected cache miss for tenant 2, got %v", v)
	}

	// Store under key2 with different value.
	m.Store(key2, &mcpServCachedTools{tools: []mcpServRawTool{{Name: "tool_from_tenant_2"}}})

	// Confirm both keys still have their original values (no overwrite).
	v1, ok1 := m.Load(key1)
	v2, ok2 := m.Load(key2)
	if !ok1 {
		t.Fatalf("expected to load value for tenant 1")
	}
	if !ok2 {
		t.Fatalf("expected to load value for tenant 2")
	}

	cached1 := v1.(*mcpServCachedTools)
	cached2 := v2.(*mcpServCachedTools)

	if len(cached1.tools) != 1 || cached1.tools[0].Name != "tool_from_tenant_1" {
		t.Fatalf("expected tool_from_tenant_1 for tenant 1, got %v", cached1.tools)
	}
	if len(cached2.tools) != 1 || cached2.tools[0].Name != "tool_from_tenant_2" {
		t.Fatalf("expected tool_from_tenant_2 for tenant 2, got %v", cached2.tools)
	}
}

// TestMCPServCacheKey_SameTenantSameEntry verifies that the same tenant and server alias
// produce the same cache key and retrieve the same cached entry.
func TestMCPServCacheKey_SameTenantSameEntry(t *testing.T) {
	m := sync.Map{}

	key1 := mcpServCacheKey{tenantID: 1, alias: "tools"}
	key2 := mcpServCacheKey{tenantID: 1, alias: "tools"}

	// Store under key1.
	expectedTools := []mcpServRawTool{
		{Name: "github", Description: "GitHub integration"},
		{Name: "slack", Description: "Slack integration"},
	}
	m.Store(key1, &mcpServCachedTools{tools: expectedTools})

	// Load using key2 (semantically same key).
	v, ok := m.Load(key2)
	if !ok {
		t.Fatalf("expected to retrieve cached tools for same tenant and alias")
	}

	cached := v.(*mcpServCachedTools)
	if len(cached.tools) != len(expectedTools) {
		t.Fatalf("expected %d tools, got %d", len(expectedTools), len(cached.tools))
	}
	for i, tool := range cached.tools {
		if tool.Name != expectedTools[i].Name || tool.Description != expectedTools[i].Description {
			t.Fatalf("tool mismatch at index %d: expected %v, got %v", i, expectedTools[i], tool)
		}
	}
}

// TestFetchExternalTools_TenantScopedCache verifies that fetchExternalTools respects tenant boundaries:
// - Two calls with the same alias but different tenants hit different cache entries
// - Subsequent calls within the same tenant hit the cache (only one HTTP request per tenant)
func TestFetchExternalTools_TenantScopedCache(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Parse the JSON-RPC request.
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Method != "tools/list" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Return a mock tools/list response.
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"tools": []map[string]string{
					{"name": "test_tool_1", "description": "A test tool"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Clear cache before and after test
	mcpServeToolsCache.Range(func(k, v any) bool {
		mcpServeToolsCache.Delete(k)
		return true
	})
	t.Cleanup(func() {
		mcpServeToolsCache.Range(func(k, v any) bool {
			mcpServeToolsCache.Delete(k)
			return true
		})
	})

	// Call fetchExternalTools for tenant 1, alias "mcp_service".
	tools1, err := fetchExternalTools(1, "mcp_service", server.URL, "", 5000)
	if err != nil {
		t.Fatalf("first fetch for tenant 1 failed: %v", err)
	}
	if len(tools1) != 1 || tools1[0].Name != "test_tool_1" {
		t.Fatalf("unexpected tools for tenant 1: %v", tools1)
	}
	count1 := requestCount
	if count1 != 1 {
		t.Fatalf("expected 1 request for first fetch, got %d", count1)
	}

	// Call fetchExternalTools for tenant 1, alias "mcp_service" again (should hit cache).
	tools1b, err := fetchExternalTools(1, "mcp_service", server.URL, "", 5000)
	if err != nil {
		t.Fatalf("second fetch for tenant 1 failed: %v", err)
	}
	if len(tools1b) != 1 || tools1b[0].Name != "test_tool_1" {
		t.Fatalf("unexpected tools for tenant 1 (cached): %v", tools1b)
	}
	count2 := requestCount
	if count2 != count1 {
		t.Fatalf("expected cache hit for second fetch (same tenant), but got %d total requests (should still be 1)", count2)
	}

	// Call fetchExternalTools for tenant 2, alias "mcp_service" (different tenant, should miss cache).
	tools2, err := fetchExternalTools(2, "mcp_service", server.URL, "", 5000)
	if err != nil {
		t.Fatalf("fetch for tenant 2 failed: %v", err)
	}
	if len(tools2) != 1 || tools2[0].Name != "test_tool_1" {
		t.Fatalf("unexpected tools for tenant 2: %v", tools2)
	}
	count3 := requestCount
	if count3 != 2 {
		t.Fatalf("expected cache miss for tenant 2 (different tenant), so 2 total requests, got %d", count3)
	}

	// Call fetchExternalTools for tenant 2 again (should hit cache).
	tools2b, err := fetchExternalTools(2, "mcp_service", server.URL, "", 5000)
	if err != nil {
		t.Fatalf("second fetch for tenant 2 failed: %v", err)
	}
	if len(tools2b) != 1 || tools2b[0].Name != "test_tool_1" {
		t.Fatalf("unexpected tools for tenant 2 (cached): %v", tools2b)
	}
	count4 := requestCount
	if count4 != count3 {
		t.Fatalf("expected cache hit for second fetch of tenant 2, but got %d total requests (should still be 2)", count4)
	}
}

// TestFetchExternalTools_ErrorHandling verifies that fetchExternalTools handles various error conditions.
func TestFetchExternalTools_ErrorHandling(t *testing.T) {
	// Test with a server that returns JSON-RPC error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32000,
				"message": "server error",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Clear cache for this test.
	mcpServeToolsCache.Range(func(k, v any) bool {
		mcpServeToolsCache.Delete(k)
		return true
	})
	t.Cleanup(func() {
		mcpServeToolsCache.Range(func(k, v any) bool {
			mcpServeToolsCache.Delete(k)
			return true
		})
	})

	tools, err := fetchExternalTools(1, "bad_server", server.URL, "", 5000)
	if err != nil {
		// According to the code, JSON-RPC errors return nil without error.
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected empty tools on JSON-RPC error, got %v", tools)
	}
}

// TestFetchExternalTools_InvalidJSON verifies that fetchExternalTools handles malformed JSON responses.
func TestFetchExternalTools_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "invalid json {{{")
	}))
	defer server.Close()

	// Clear cache for this test.
	mcpServeToolsCache.Range(func(k, v any) bool {
		mcpServeToolsCache.Delete(k)
		return true
	})
	t.Cleanup(func() {
		mcpServeToolsCache.Range(func(k, v any) bool {
			mcpServeToolsCache.Delete(k)
			return true
		})
	})

	tools, err := fetchExternalTools(1, "bad_json", server.URL, "", 5000)
	if err == nil {
		t.Fatalf("expected error for malformed JSON, got nil")
	}
	if tools != nil {
		t.Fatalf("expected nil tools on error, got %v", tools)
	}
}

// TestFetchExternalTools_WithAPIKey verifies that fetchExternalTools correctly passes Authorization header.
func TestFetchExternalTools_WithAPIKey(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"tools": []map[string]string{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Clear cache for this test.
	mcpServeToolsCache.Range(func(k, v any) bool {
		mcpServeToolsCache.Delete(k)
		return true
	})
	t.Cleanup(func() {
		mcpServeToolsCache.Range(func(k, v any) bool {
			mcpServeToolsCache.Delete(k)
			return true
		})
	})

	_, err := fetchExternalTools(1, "with_key", server.URL, "my_secret_key", 5000)
	if err != nil {
		t.Fatalf("fetch with API key failed: %v", err)
	}
	if authHeader != "Bearer my_secret_key" {
		t.Fatalf("expected Authorization header 'Bearer my_secret_key', got %q", authHeader)
	}
}
