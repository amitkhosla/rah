package router

import (
	"fmt"
	"testing"
)

func TestGemRouter_EmptyLookup(t *testing.T) {
	r := New()
	if got := r.Lookup("/v1/any"); got != 0 {
		t.Fatalf("expected 0 for empty router lookup, got %d", got)
	}
}

// TestSubpathMatching ensures that basepaths correctly "claim" sub-paths
// unless a more specific route exists.
func TestGemRouter_SubpathMatching(t *testing.T) {
	r := New()

	basePath := "/v1/commons/xyz"
	apiName := uint32(1)
	r.Add(basePath, 1)

	// Test 1: Exact match
	if res := r.Lookup("/v1/commons/xyz"); res != apiName {
		t.Errorf("Exact match failed: expected %d, got %d", apiName, res)
	}

	// Test 2: Sub-path match
	if res := r.Lookup("/v1/commons/xyz/abc"); res != apiName {
		t.Errorf("Sub-path match failed: expected %d, got %d", apiName, res)
	}

	// Test 3: Deeper sub-path
	if res := r.Lookup("/v1/commons/xyz/abc/def/ghi"); res != apiName {
		t.Errorf("Deep sub-path match failed: expected %d, got %d", apiName, res)
	}

	// Test 4: Partial mismatch (Should NOT match)
	// "/v1/commons/xy" is not a sub-path of "/v1/commons/xyz"
	if res := r.Lookup("/v1/commons/xy"); res != 0 {
		t.Errorf("Partial mismatch should return empty string, got %d", res)
	}
}

// TestAPINames verifies the greedy hierarchy (most specific match wins).
func TestGemRouter_APINames(t *testing.T) {
	r := New()

	r.Add("/v1/commons", uint32(1))
	r.Add("/v1/commons/xyz", uint32(2))

	tests := []struct {
		input    string
		expected uint32
	}{
		{"/v1/commons", uint32(1)},
		{"/v1/commons/anything", uint32(1)}, // Falls back to base
		{"/v1/commons/xyz", uint32(2)},      // Matches more specific branch
		{"/v1/commons/xyz/123", uint32(2)},  // Sub-path of specific branch
		{"/v1/other", 0},                    // No match at all
	}

	for _, tc := range tests {
		res := r.Lookup(tc.input)
		if res != tc.expected {
			t.Errorf("Path %s: expected %d, got %d", tc.input, tc.expected, res)
		}
	}
}

// TestFunctional verifies that adding routes that share prefixes works correctly.
func TestGemRouter_Functional(t *testing.T) {
	r := New()

	routes := map[string]uint32{
		"/v1/commons":        uint32(1),
		"/v1/commons/xyz":    uint32(2),
		"/v1/commons/xyzabc": uint32(3),
	}

	for path, name := range routes {
		r.Add(path, name)
	}

	// Verify each route
	for path, expected := range routes {
		res := r.Lookup(path)
		if res != expected {
			t.Errorf("Path %s: expected %d, got %d", path, expected, res)
		}
	}

	// Verify that a partial match prefix doesn't return a child's name
	// "/v1/commons/xy" is a sub-path of "/v1/commons", so it should return "COMMONS"
	if res := r.Lookup("/v1/commons/xy"); res != uint32(1) {
		t.Errorf("Expected COMMONS for path /v1/commons/xy, got %d", res)
	}
}

// TestFunctional verifies that adding routes that share prefixes works correctly.
func TestGemRouterFewEntries_Functional(t *testing.T) {
	r := New()

	routes := map[string]uint32{
		"/v1/common":     uint32(1),
		"/v1/scheduling": uint32(2),
	}

	for path, name := range routes {
		r.Add(path, name)
	}

	// Verify each route
	for path, expected := range routes {
		res := r.Lookup(path)
		if res != expected {
			t.Errorf("Path %s: expected %d, got %d", path, expected, res)
		}
	}

	// Verify that a partial match prefix doesn't return a child's name
	// "/v1/commons/xy" is a sub-path of "/v1/commons", so it should return "COMMONS"
	if res := r.Lookup("/v1/commons"); res != uint32(0) {
		t.Errorf("Why COMMONS comming for /v1/commons, got %d", res)
	}

	if res := r.Lookup("/v1/commons/xy"); res != uint32(0) {
		t.Errorf("Why COMMONS comming for /v1/commons/xy, got %d", res)
	}
}

// TestScale ensures performance and correctness with 1000 routes.
/*func TestGemRouter_1000Routes(t *testing.T) {
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
*/
// BenchmarkLookup_1000Routes tests the nanosecond latency of the hot path.
func BenchmarkLookup_1000Routes(b *testing.B) {
	print("Starting benchmark")
	r := New()
	urls := Generate3000Routes(r)
	print("added Urls: ", urls)

	// Search for all paths

	b.ResetTimer()
	// The benchmark must run b.N times for Go to calculate average speed
	for i := 0; i < b.N; i++ {
		// We use a nested loop to test all 3000 URLs in every iteration
		for _, url := range urls {
			_ = r.Lookup(url)
		}
	}
}

func BenchmarkLookup_1000RoutesSmall(b *testing.B) {
	print("Starting benchmark")
	r := NewSmall()
	urls := Generate3000RoutesSmall(r)
	print("added Urls: ", urls)

	// Search for all paths

	b.ResetTimer()
	// The benchmark must run b.N times for Go to calculate average speed
	for i := 0; i < b.N; i++ {
		// We use a nested loop to test all 3000 URLs in every iteration
		for _, url := range urls {
			_ = r.Lookup(url)
		}
	}
}

// BenchmarkLookup_1000Routes tests the nanosecond latency of the hot path.
func BenchmarkLookup_1000RoutesWoutPadding(b *testing.B) {
	print("Starting benchmark")
	r := NewWoutPadding()
	urls := Generate3000RoutesWPadding(r)
	print("added Urls: ", urls)

	// Search for all paths

	b.ResetTimer()
	// The benchmark must run b.N times for Go to calculate average speed
	for i := 0; i < b.N; i++ {
		// We use a nested loop to test all 3000 URLs in every iteration
		for _, url := range urls {
			_ = r.Lookup(url)
		}
	}
}

// BenchmarkLookup_1000Routes tests the nanosecond latency of the hot path.
func BenchmarkLookup_1000RoutesString(b *testing.B) {
	print("Starting benchmark")
	r := NewStringRouter()
	urls := Generate3000RoutesString(r)
	print("added Urls: ", urls)

	// Search for all paths

	b.ResetTimer()
	// The benchmark must run b.N times for Go to calculate average speed
	for i := 0; i < b.N; i++ {
		// We use a nested loop to test all 3000 URLs in every iteration
		for _, url := range urls {
			_ = r.Lookup(url)
		}
	}
}

// BenchmarkLookup_1000Routes tests the nanosecond latency of the hot path.
func BenchmarkLookup_SingleRoutes(b *testing.B) {
	print("Starting benchmark")
	r := New()
	urls := Generate3000Routes(r)
	print("added Urls: ", urls)

	// Search for all paths

	b.ResetTimer()
	// The benchmark must run b.N times for Go to calculate average speed
	for i := 0; i < b.N; i++ {
		_ = r.Lookup("/api/v1/procurement/vulnerability/archived")
	}
}

// BenchmarkLookup_1000Routes tests the nanosecond latency of the hot path.
func BenchmarkLookup_SingleRoutesSmall(b *testing.B) {
	print("Starting benchmark")
	r := NewSmall()
	urls := Generate3000RoutesSmall(r)
	print("added Urls: ", urls)

	// Search for all paths

	b.ResetTimer()
	// The benchmark must run b.N times for Go to calculate average speed
	for i := 0; i < b.N; i++ {
		_ = r.Lookup("/api/v1/procurement/vulnerability/archived")
	}
}

// BenchmarkLookup_1000Routes tests the nanosecond latency of the hot path.
func BenchmarkLookup_SingleRoutesWoutPadding(b *testing.B) {
	print("Starting benchmark")
	r := NewWoutPadding()
	urls := Generate3000RoutesWPadding(r)
	print("added Urls: ", urls)

	// Search for all paths

	b.ResetTimer()
	// The benchmark must run b.N times for Go to calculate average speed
	for i := 0; i < b.N; i++ {
		_ = r.Lookup("/api/v1/procurement/vulnerability/archived")
	}
}

// BenchmarkLookup_1000Routes tests the nanosecond latency of the hot path.
func BenchmarkLookup_SingleRoutesString(b *testing.B) {
	print("Starting benchmark")
	r := NewStringRouter()
	urls := Generate3000RoutesString(r)
	print("added Urls: ", urls)

	// Search for all paths

	b.ResetTimer()
	// The benchmark must run b.N times for Go to calculate average speed
	for i := 0; i < b.N; i++ {
		_ = r.Lookup("/api/v1/procurement/vulnerability/archived")
	}
}

func Generate3000Routes(r *RahRouter) []string {
	output := make([]string, 0, 3000)
	// 15 Departments/Sectors
	sectors := []string{
		"finance", "hr", "ops", "marketing", "dev", "legal",
		"sales", "it", "support", "product", "audit", "security",
		"logistics", "warehouse", "procurement",
	}

	// 20 Core Domains
	domains := []string{
		"users", "accounts", "billing", "reports", "assets",
		"tickets", "campaigns", "leads", "contracts", "invoices",
		"inventory", "shipments", "policies", "vulnerability", "backups",
		"sprints", "backlogs", "metrics", "alerts", "audits",
	}

	// 10 Specific Resources or Actions per Domain
	subResources := []string{
		"all", "active", "pending", "archived", "deleted",
		"summary", "details", "history", "config", "logs",
	}

	count := 0
	for _, s := range sectors {
		for _, d := range domains {
			for _, sub := range subResources {
				// This loop generates 15 * 20 * 10 = 3,000 unique paths
				path := fmt.Sprintf("/api/v1/%s/%s/%s", s, d, sub)
				r.Add(path, uint32(count+1))
				output = append(output, path)
				count++

				path = fmt.Sprintf("/api/v2/%s/%s/%s", s, d, sub)
				r.Add(path, uint32(count+1))
				output = append(output, path)
				count++

			}
		}
	}
	return output
}

func Generate3000RoutesWPadding(r *RahRouterWoutPAdding) []string {
	output := make([]string, 0, 3000)
	// 15 Departments/Sectors
	sectors := []string{
		"finance", "hr", "ops", "marketing", "dev", "legal",
		"sales", "it", "support", "product", "audit", "security",
		"logistics", "warehouse", "procurement",
	}

	// 20 Core Domains
	domains := []string{
		"users", "accounts", "billing", "reports", "assets",
		"tickets", "campaigns", "leads", "contracts", "invoices",
		"inventory", "shipments", "policies", "vulnerability", "backups",
		"sprints", "backlogs", "metrics", "alerts", "audits",
	}

	// 10 Specific Resources or Actions per Domain
	subResources := []string{
		"all", "active", "pending", "archived", "deleted",
		"summary", "details", "history", "config", "logs",
	}

	count := 0
	for _, s := range sectors {
		for _, d := range domains {
			for _, sub := range subResources {
				// This loop generates 15 * 20 * 10 = 3,000 unique paths
				path := fmt.Sprintf("/api/v1/%s/%s/%s", s, d, sub)
				r.Add(path, uint32(count+1))
				output = append(output, path)
				count++

				path = fmt.Sprintf("/api/v2/%s/%s/%s", s, d, sub)
				r.Add(path, uint32(count+1))
				output = append(output, path)
				count++
			}
		}
	}
	return output
}

func Generate3000RoutesSmall(r *RahRouterSmall) []string {
	output := make([]string, 0, 3000)
	// 15 Departments/Sectors
	sectors := []string{
		"finance", "hr", "ops", "marketing", "dev", "legal",
		"sales", "it", "support", "product", "audit", "security",
		"logistics", "warehouse", "procurement",
	}

	// 20 Core Domains
	domains := []string{
		"users", "accounts", "billing", "reports", "assets",
		"tickets", "campaigns", "leads", "contracts", "invoices",
		"inventory", "shipments", "policies", "vulnerability", "backups",
		"sprints", "backlogs", "metrics", "alerts", "audits",
	}

	// 10 Specific Resources or Actions per Domain
	subResources := []string{
		"all", "active", "pending", "archived", "deleted",
		"summary", "details", "history", "config", "logs",
	}

	count := 0
	for _, s := range sectors {
		for _, d := range domains {
			for _, sub := range subResources {
				// This loop generates 15 * 20 * 10 = 3,000 unique paths
				path := fmt.Sprintf("/api/v1/%s/%s/%s", s, d, sub)
				r.Add(path, uint16(count+1))
				output = append(output, path)
				count++

				path = fmt.Sprintf("/api/v2/%s/%s/%s", s, d, sub)
				r.Add(path, uint16(count+1))
				output = append(output, path)
				count++
			}
		}
	}
	return output
}

func Generate3000RoutesString(r *RahStringRouter) []string {
	output := make([]string, 0, 3000)
	// 15 Departments/Sectors
	sectors := []string{
		"finance", "hr", "ops", "marketing", "dev", "legal",
		"sales", "it", "support", "product", "audit", "security",
		"logistics", "warehouse", "procurement",
	}

	// 20 Core Domains
	domains := []string{
		"users", "accounts", "billing", "reports", "assets",
		"tickets", "campaigns", "leads", "contracts", "invoices",
		"inventory", "shipments", "policies", "vulnerability", "backups",
		"sprints", "backlogs", "metrics", "alerts", "audits",
	}

	// 10 Specific Resources or Actions per Domain
	subResources := []string{
		"all", "active", "pending", "archived", "deleted",
		"summary", "details", "history", "config", "logs",
	}
	// /api/v1/procurement/vulnerability/archived
	count := 0
	for _, s := range sectors {
		for _, d := range domains {
			for _, sub := range subResources {
				// This loop generates 15 * 20 * 10 = 3,000 unique paths
				path := fmt.Sprintf("/api/v1/%s/%s/%s", s, d, sub)
				r.Add(path, path+"-")
				output = append(output, path)
				count++

				path = fmt.Sprintf("/api/v2/%s/%s/%s", s, d, sub)
				r.Add(path, path+"-")
				output = append(output, path)
				count++
			}
		}
	}
	return output
}
