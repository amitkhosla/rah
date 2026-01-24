package router

import (
	"fmt"
	"testing"
)

// TestSubpathMatching ensures that basepaths correctly "claim" sub-paths
// unless a more specific route exists.
func TestGemRouter_SubpathMatching(t *testing.T) {
	r := New()

	basePath := "/v1/commons/xyz"
	apiName := "XYZ_SERVICE"
	r.Add(basePath, apiName)

	// Test 1: Exact match
	if res := r.Lookup("/v1/commons/xyz"); res != apiName {
		t.Errorf("Exact match failed: expected %s, got %s", apiName, res)
	}

	// Test 2: Sub-path match
	if res := r.Lookup("/v1/commons/xyz/abc"); res != apiName {
		t.Errorf("Sub-path match failed: expected %s, got %s", apiName, res)
	}

	// Test 3: Deeper sub-path
	if res := r.Lookup("/v1/commons/xyz/abc/def/ghi"); res != apiName {
		t.Errorf("Deep sub-path match failed: expected %s, got %s", apiName, res)
	}

	// Test 4: Partial mismatch (Should NOT match)
	// "/v1/commons/xy" is not a sub-path of "/v1/commons/xyz"
	if res := r.Lookup("/v1/commons/xy"); res != "" {
		t.Errorf("Partial mismatch should return empty string, got %s", res)
	}
}

// TestAPINames verifies the greedy hierarchy (most specific match wins).
func TestGemRouter_APINames(t *testing.T) {
	r := New()

	r.Add("/v1/commons", "API_COMMONS_BASE")
	r.Add("/v1/commons/xyz", "API_XYZ_SERVICE")

	tests := []struct {
		input    string
		expected string
	}{
		{"/v1/commons", "API_COMMONS_BASE"},
		{"/v1/commons/anything", "API_COMMONS_BASE"}, // Falls back to base
		{"/v1/commons/xyz", "API_XYZ_SERVICE"},       // Matches more specific branch
		{"/v1/commons/xyz/123", "API_XYZ_SERVICE"},   // Sub-path of specific branch
		{"/v1/other", ""},                            // No match at all
	}

	for _, tc := range tests {
		res := r.Lookup(tc.input)
		if res != tc.expected {
			t.Errorf("Path %s: expected %s, got %s", tc.input, tc.expected, res)
		}
	}
}

// TestFunctional verifies that adding routes that share prefixes works correctly.
func TestGemRouter_Functional(t *testing.T) {
	r := New()

	routes := map[string]string{
		"/v1/commons":        "COMMONS",
		"/v1/commons/xyz":    "XYZ",
		"/v1/commons/xyzabc": "XYZABC",
	}

	for path, name := range routes {
		r.Add(path, name)
	}

	// Verify each route
	for path, expected := range routes {
		res := r.Lookup(path)
		if res != expected {
			t.Errorf("Path %s: expected %s, got %s", path, expected, res)
		}
	}

	// Verify that a partial match prefix doesn't return a child's name
	// "/v1/commons/xy" is a sub-path of "/v1/commons", so it should return "COMMONS"
	if res := r.Lookup("/v1/commons/xy"); res != "COMMONS" {
		t.Errorf("Expected COMMONS for path /v1/commons/xy, got %s", res)
	}
}

// TestScale ensures performance and correctness with 1000 routes.
func TestGemRouter_1000Routes(t *testing.T) {
	r := New()

	numServices := 1000
	numEndpoints := 100

	for s := 0; s < numServices; s++ {
		for e := 0; e < numEndpoints; e++ {
			path := fmt.Sprintf("/api/v1/service-%d/endpoint-%d", s, e)
			name := fmt.Sprintf("API_%d_%d", s, e)
			r.Add(path, name)
		}
	}

	// Verify a sample deep in the tree
	testPath := "/api/v1/service-5/endpoint-42"
	expected := "API_5_42"
	if res := r.Lookup(testPath); res != expected {
		t.Errorf("Failed to find %s: expected %s, got %s", testPath, expected, res)
	}
}

// BenchmarkLookup_1000Routes tests the nanosecond latency of the hot path.
func BenchmarkLookup_1000Routes(b *testing.B) {
	r := New()
	for s := 0; s < 10; s++ {
		for e := 0; e < 100; e++ {
			path := fmt.Sprintf("/api/v1/service-%d/endpoint-%d", s, e)
			name := fmt.Sprintf("API_%d_%d", s, e)
			r.Add(path, name)
		}
	}

	// Search for a path that is likely the "last" in the search order to ensure worst-case
	path := "/api/v1/service-9/endpoint-99"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Lookup(path)
	}
}
