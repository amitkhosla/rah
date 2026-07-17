package steps

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ============================================================================
// CROSS-VALIDATION TESTS
// These tests verify that all extracted identifiers match their service codes
// and service URLs are correctly associated with the right tenants/services.
// ============================================================================

// ServiceMapping represents a single service with its URL and tenant
type ServiceMapping struct {
	TenantID     string
	TenantName   string
	ServiceCode  string
	ServiceURL   string
	Category     string
	ExpectedURL  string // from config
}

// CrossValidationResult holds comprehensive validation results
type CrossValidationResult struct {
	AllServices        []ServiceMapping
	MissingURLs        []ServiceMapping
	MismatchedURLs     []ServiceMapping
	UnassignedServices []ServiceMapping
	OrphanServiceCodes []string
	OrphanIdentifiers  []string
	TotalServices      int
	AssignedCount      int
	UnassignedCount    int
	ValidatedCount     int
}

// TestCrossValidateServiceCodesAndURLs verifies that every extracted service code
// has a corresponding valid URL and is associated with the correct tenant identifier.
func TestCrossValidateServiceCodesAndURLs(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	// Build expected mappings from config
	expectedMappings := make(map[string]map[string]string) // tenant -> (serviceCode -> URL)

	for _, product := range config.Products {
		tenant := product.Product

		if expectedMappings[tenant] == nil {
			expectedMappings[tenant] = make(map[string]string)
		}

		for _, category := range product.ServiceCategories {
			for _, svc := range category.Services {
				key := fmt.Sprintf("%s:%s", tenant, svc.ServiceCode)
				expectedMappings[tenant][key] = svc.ServiceURL
			}
		}
	}

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "global",
		TenantID:    99,
	}

	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	mockMgr := NewMockRegistryMutator()
	ctx.MaxOps = 1000

	// Extract from all products' services (nested structure)
	processAllServices := func(tenant string, pathToServices string) {
		ctx.Ops = ctx.Ops[:0]
		// Adjust keyPrefix based on tenant
		tenantOps := []ExtractOp{
			{
				Path:      "serviceCode", // key suffix = serviceCode
				ValuePath: "serviceUrl",  // value = serviceUrl from same element
				KeyPrefix: tenant + ":",
				OpType:    rctx.OpPut,
				Target:    rctx.TargetRegistryURL,
				ValueSlot: -1,
				DestSlot:  -1,
			},
		}

		extractStep := JSONForeachEmit(1, pathToServices, tenantOps)
		extractStep.Action(ctx, state)

		for _, op := range ctx.Ops {
			if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
				mockMgr.AddServiceURL("global", string(op.Key), op.Value)
			}
		}
	}

	// JSONForeachEmit iterates one flat array level. serviceCategories contains nested
	// service arrays, so we call once per product+category with the explicit index path.
	for pIdx, product := range config.Products {
		for cIdx := range product.ServiceCategories {
			path := fmt.Sprintf("products.%d.serviceCategories.%d.services", pIdx, cIdx)
			processAllServices(product.Product, path)
		}
	}

	// Step 2: Extract tenant identifiers for cross-validation
	ctx.Ops = ctx.Ops[:0]
	tenantIDOps := []ExtractOp{
		{
			Path:       "tenantIdentifier",
			KeyPrefix:  "",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryID,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}
	tenantIDStep := JSONForeachEmit(1, "products", tenantIDOps)
	tenantIDStep.Action(ctx, state)

	extractedTenantIDs := make(map[string]string) // tenant name -> ID
	for _, op := range ctx.Ops {
		if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryID {
			mockMgr.AddIdentifier("global", string(op.Key), op.Value)
			// Extract tenant name from config by value matching
			val := string(op.Value)
			for _, product := range config.Products {
				if product.TenantIdentifier == val {
					extractedTenantIDs[product.Product] = val
					break
				}
			}
		}
	}

	t.Log("\n=== CROSS-VALIDATION: Service Codes vs Identifiers vs URLs ===\n")

	// VALIDATION 1: Check all extracted services against expected
	result := &CrossValidationResult{
		AllServices:        []ServiceMapping{},
		MissingURLs:        []ServiceMapping{},
		MismatchedURLs:     []ServiceMapping{},
		UnassignedServices: []ServiceMapping{},
	}

	urls := mockMgr.ServiceURLs["global"]

	if urls == nil {
		t.Fatal("No service URLs found in registry")
	}

	// Collect all extracted services
	for serviceKey, _ := range urls {
		result.AllServices = append(result.AllServices, ServiceMapping{
			ServiceCode: serviceKey,
		})
	}

	// Validate each service
	for serviceKey, extractedURL := range urls {
		// Parse the key: format is "Tenant:serviceCode"
		parts := splitServiceKey(serviceKey)
		if len(parts) != 2 {
			t.Logf("âš ï¸  Invalid service key format: %s", serviceKey)
			continue
		}

		tenant := parts[0]
		serviceCode := parts[1]

		// Check if URL matches expected
		expectedURL := ""
		if expectedMap, exists := expectedMappings[tenant]; exists {
			if exp, found := expectedMap[serviceKey]; found {
				expectedURL = exp
			}
		}

		actualURL := string(extractedURL)
		tenantID := extractedTenantIDs[tenant]

		mapping := ServiceMapping{
			TenantID:    tenantID,
			TenantName:  tenant,
			ServiceCode: serviceCode,
			ServiceURL:  actualURL,
			ExpectedURL: expectedURL,
		}

		// Check for unassigned services
		if actualURL == "unassigned" {
			result.UnassignedServices = append(result.UnassignedServices, mapping)
			t.Logf("âš ï¸  [UNASSIGNED] %s:%s (Tenant: %s, ID: %s)", tenant, serviceCode, tenant, tenantID)
		} else if expectedURL == "" {
			result.MissingURLs = append(result.MissingURLs, mapping)
			t.Errorf("âŒ [MISSING] %s:%s - No expected URL found", tenant, serviceCode)
		} else if actualURL != expectedURL {
			result.MismatchedURLs = append(result.MismatchedURLs, mapping)
			t.Errorf("âŒ [MISMATCH] %s:%s\n   Expected: %s\n   Got: %s", tenant, serviceCode, expectedURL, actualURL)
		} else {
			result.ValidatedCount++
			t.Logf("âœ“ [VALID] %s:%s â†’ %s (Tenant ID: %s)", tenant, serviceCode, actualURL, tenantID)
		}
	}

	result.TotalServices = len(urls)
	result.AssignedCount = result.ValidatedCount + len(result.MismatchedURLs)
	result.UnassignedCount = len(result.UnassignedServices)

	// Print summary
	t.Log("\n=== SUMMARY ===")
	t.Logf("Total Services: %d", result.TotalServices)
	t.Logf("âœ“ Validated: %d", result.ValidatedCount)
	t.Logf("âš ï¸  Unassigned: %d", result.UnassignedCount)
	t.Logf("âŒ Mismatches: %d", len(result.MismatchedURLs))
	t.Logf("âŒ Missing URLs: %d", len(result.MissingURLs))

	// Fail if there are mismatches
	if len(result.MismatchedURLs) > 0 {
		t.Errorf("Found %d URL mismatches", len(result.MismatchedURLs))
	}

	if result.ValidatedCount == 0 {
		t.Error("No services were successfully validated")
	}
}

// TestVerifyEachTenantHasIdentifier validates that every tenant has at least one identifier
func TestVerifyEachTenantHasIdentifier(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "global",
		TenantID:    99,
	}

	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	mockMgr := NewMockRegistryMutator()
	ctx.MaxOps = 1000

	// Extract tenant identifiers
	ops := []ExtractOp{
		{
			Path:       "tenantIdentifier",
			KeyPrefix:  "tenant_",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryID,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}

	extractStep := JSONForeachEmit(1, "products", ops)
	extractStep.Action(ctx, state)

	for _, op := range ctx.Ops {
		if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryID {
			mockMgr.AddIdentifier("global", string(op.Key), op.Value)
		}
	}

	// VALIDATION: Each product has a unique tenant identifier
	t.Log("\n=== TENANT IDENTIFIER VERIFICATION ===\n")

	expectedTenants := map[string]bool{
		"acme_nonprd_01":            false,
		"A7345-34789S-54879-WERT":   false,
		"ZWP491":                    false,
	}

	ids := mockMgr.Identifiers["global"]
	if ids == nil {
		t.Fatal("No tenant identifiers found in registry")
	}

	extractedIDs := make([]string, 0)
	for key, val := range ids {
		idVal := string(val)
		extractedIDs = append(extractedIDs, idVal)

		if _, exists := expectedTenants[idVal]; exists {
			expectedTenants[idVal] = true
			t.Logf("âœ“ Tenant ID found: %s (stored as key: %s)", idVal, key)
		} else {
			t.Logf("âš ï¸  Unexpected tenant ID: %s", idVal)
		}
	}

	// Verify all expected tenants were found
	allFound := true
	for expected, found := range expectedTenants {
		if !found {
			t.Errorf("âŒ Expected tenant ID not found: %s", expected)
			allFound = false
		}
	}

	if allFound {
		t.Logf("\nâœ“ All %d tenant identifiers correctly extracted and stored", len(expectedTenants))
	}

	sort.Strings(extractedIDs)
	t.Logf("\nExtracted Tenant IDs: %v", extractedIDs)
}

// TestVerifyServiceCodeConsistency checks that each service code is consistent
// across extraction and storage (no duplicates, no loss)
func TestVerifyServiceCodeConsistency(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	// Build expected service codes from config
	expectedServiceCodes := make(map[string]int) // serviceCode -> count
	for _, product := range config.Products {
		for _, category := range product.ServiceCategories {
			for _, svc := range category.Services {
				key := fmt.Sprintf("%s:%s", product.Product, svc.ServiceCode)
				expectedServiceCodes[key]++
			}
		}
	}

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "global",
		TenantID:    99,
	}

	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	mockMgr := NewMockRegistryMutator()
	ctx.MaxOps = 1000

	// Extract all service codes
	extractServices := func(tenant string, pathToServices string) {
		ctx.Ops = ctx.Ops[:0]
		ops := []ExtractOp{
			{
				Path:       "serviceCode",
				KeyPrefix:  tenant + ":",
				OpType:     rctx.OpPut,
				Target:     rctx.TargetRegistryURL,
				ValueSlot:  -1,
				DestSlot:   -1,
			},
		}

		extractStep := JSONForeachEmit(1, pathToServices, ops)
		extractStep.Action(ctx, state)

		for _, op := range ctx.Ops {
			if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
				mockMgr.AddServiceURL("global", string(op.Key), op.Value)
			}
		}
	}

	// Call once per product+category so JSONForeachEmit iterates a flat service array.
	for pIdx, product := range config.Products {
		for cIdx := range product.ServiceCategories {
			path := fmt.Sprintf("products.%d.serviceCategories.%d.services", pIdx, cIdx)
			extractServices(product.Product, path)
		}
	}

	t.Log("\n=== SERVICE CODE CONSISTENCY VERIFICATION ===\n")

	// Get extracted services
	urls := mockMgr.ServiceURLs["global"]
	extractedServiceCodes := make(map[string]int)

	for serviceKey := range urls {
		extractedServiceCodes[serviceKey]++
	}

	// Compare extracted vs expected
	duplicates := 0
	missing := 0
	extra := 0

	// Check for duplicates or extra services
	for code, extractedCount := range extractedServiceCodes {
		expectedCount, exists := expectedServiceCodes[code]
		if !exists {
			t.Logf("âš ï¸  Extra service code: %s (count: %d)", code, extractedCount)
			extra++
		} else if extractedCount > expectedCount {
			t.Logf("âš ï¸  Duplicate service code: %s (expected: %d, got: %d)", code, expectedCount, extractedCount)
			duplicates++
		} else if extractedCount == expectedCount {
			t.Logf("âœ“ Service code consistent: %s (count: %d)", code, extractedCount)
		}
	}

	// Check for missing services
	for code, expectedCount := range expectedServiceCodes {
		extractedCount, exists := extractedServiceCodes[code]
		if !exists {
			t.Logf("âŒ Missing service code: %s", code)
			missing++
		} else if extractedCount < expectedCount {
			t.Logf("âŒ Incomplete extraction: %s (expected: %d, got: %d)", code, expectedCount, extractedCount)
			missing++
		}
	}

	// Summary
	t.Log("\n=== CONSISTENCY SUMMARY ===")
	t.Logf("Expected service codes: %d", len(expectedServiceCodes))
	t.Logf("Extracted service codes: %d", len(extractedServiceCodes))
	t.Logf("âœ“ Consistent: %d", len(extractedServiceCodes)-duplicates-extra)
	t.Logf("âš ï¸  Duplicates: %d", duplicates)
	t.Logf("âš ï¸  Extra: %d", extra)
	t.Logf("âŒ Missing: %d", missing)

	if missing > 0 || duplicates > 0 || extra > 0 {
		t.Errorf("Service code consistency check failed: %d missing, %d duplicates, %d extra", missing, duplicates, extra)
	}

	if len(extractedServiceCodes) == len(expectedServiceCodes) {
		t.Logf("\nâœ“ All service codes extracted correctly (total: %d)", len(extractedServiceCodes))
	}
}

// TestDetailedServiceMapping provides a detailed report of all extracted data
// showing service code -> tenant -> URL mappings
func TestDetailedServiceMapping(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "global",
		TenantID:    99,
	}

	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	mockMgr := NewMockRegistryMutator()
	ctx.MaxOps = 1000

	// Extract all data
	extractAllData := func() {
		// Extract services
		for _, product := range config.Products {
			ctx.Ops = ctx.Ops[:0]
			pathToServices := fmt.Sprintf("products.%d.serviceCategories",
				findProductIndex(config, product.Product))

			ops := []ExtractOp{
				{
					Path:       "serviceCode",
					KeyPrefix:  product.Product + ":",
					OpType:     rctx.OpPut,
					Target:     rctx.TargetRegistryURL,
					ValueSlot:  -1,
					DestSlot:   -1,
				},
			}

			extractStep := JSONForeachEmit(1, pathToServices, ops)
			extractStep.Action(ctx, state)

			for _, op := range ctx.Ops {
				if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
					mockMgr.AddServiceURL("global", string(op.Key), op.Value)
				}
			}
		}

		// Extract tenant IDs
		ctx.Ops = ctx.Ops[:0]
		ops := []ExtractOp{
			{
				Path:       "tenantIdentifier",
				KeyPrefix:  "id_",
				OpType:     rctx.OpPut,
				Target:     rctx.TargetRegistryID,
				ValueSlot:  -1,
				DestSlot:   -1,
			},
		}
		extractStep := JSONForeachEmit(1, "products", ops)
		extractStep.Action(ctx, state)

		for _, op := range ctx.Ops {
			if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryID {
				mockMgr.AddIdentifier("global", string(op.Key), op.Value)
			}
		}
	}

	extractAllData()

	// Build detailed mapping report
	separator := strings.Repeat("=", 80)
	t.Log("\n" + separator)
	t.Log("DETAILED SERVICE MAPPING REPORT")
	t.Log(separator + "\n")

	type DetailedMapping struct {
		TenantName string
		TenantID   string
		Services   []struct {
			Code    string
			URL     string
			Status  string
		}
	}

	mappings := make(map[string]*DetailedMapping)

	// Organize data by tenant
	urls := mockMgr.ServiceURLs["global"]

	// Create tenant entries
	for _, product := range config.Products {
		if _, exists := mappings[product.Product]; !exists {
			mappings[product.Product] = &DetailedMapping{
				TenantName: product.Product,
				TenantID:   product.TenantIdentifier,
			}
		}
	}

	// Add services to each tenant
	for serviceKey, urlVal := range urls {
		parts := splitServiceKey(serviceKey)
		if len(parts) != 2 {
			continue
		}

		tenantName := parts[0]
		serviceCode := parts[1]

		if mapping, exists := mappings[tenantName]; exists {
			status := "âœ“ ASSIGNED"
			if string(urlVal) == "unassigned" {
				status = "âš ï¸  UNASSIGNED"
			}

			mapping.Services = append(mapping.Services, struct {
				Code   string
				URL    string
				Status string
			}{
				Code:   serviceCode,
				URL:    string(urlVal),
				Status: status,
			})
		}
	}

	// Print report
	for _, tenantName := range []string{"Commerce", "Analytics", "Notifications"} {
		if mapping, exists := mappings[tenantName]; exists {
			t.Logf("Tenant: %s", mapping.TenantName)
			t.Logf("  Tenant ID: %s", mapping.TenantID)
			t.Logf("  Services (%d):", len(mapping.Services))

			// Sort services by code
			sort.Slice(mapping.Services, func(i, j int) bool {
				return mapping.Services[i].Code < mapping.Services[j].Code
			})

			for _, svc := range mapping.Services {
				t.Logf("    [%s] %s â†’ %s", svc.Status, svc.Code, svc.URL)
			}
			t.Log("")
		}
	}

	// Final statistics
	totalServices := 0
	assignedServices := 0
	unassignedServices := 0

	for _, mapping := range mappings {
		for _, svc := range mapping.Services {
			totalServices++
			if svc.URL != "unassigned" {
				assignedServices++
			} else {
				unassignedServices++
			}
		}
	}

	t.Log(separator)
	t.Log("SUMMARY STATISTICS")
	t.Log(separator)
	t.Logf("Total Services: %d", totalServices)
	t.Logf("âœ“ Assigned: %d", assignedServices)
	t.Logf("âš ï¸  Unassigned: %d", unassignedServices)
	t.Logf("Tenants with Data: %d", len(mappings))
	t.Log(separator)
}

// Helper function to split service key into tenant and code
func splitServiceKey(key string) []string {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return []string{key}
}

// Helper function to find product index in config
func findProductIndex(config ConfigResponse, productName string) int {
	for i, p := range config.Products {
		if p.Product == productName {
			return i
		}
	}
	return 0
}
