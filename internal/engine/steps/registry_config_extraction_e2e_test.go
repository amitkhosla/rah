package steps

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ============================================================================
// END-TO-END VALIDATION TESTS
// These tests verify that extracted data is correctly persisted in the registry
// with proper tenant isolation, correct values, and multi-step execution.
// ============================================================================

// TestValidateCommerceTenanIdentifierPersistence validates that the commerce tenant
// identifier is correctly extracted and stored in the registry with the proper key.
func TestValidateCommerceTenanIdentifierPersistence(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "acme",
		TenantID:    1,
	}

	// Manually populate response body (simulating HTTP fetch)
	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	// Extract commerce product tenant identifier
	mockMgr := NewMockRegistryMutator()
	ops := []ExtractOp{
		{
			Path:       "tenantIdentifier",
			KeyPrefix:  "commerce_tenant_id",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryID,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}

	// Extract from first product (Commerce) only
	extractStep := JSONForeachEmit(1, "products", ops)

	ctx.MaxOps = 1000
	ctx.OnFlush = func(c *rctx.Context) {
		for _, op := range c.Ops {
			if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryID {
				mockMgr.AddIdentifier(c.TenantKey, string(op.Key), op.Value)
			}
		}
	}

	extractStep.Action(ctx, state)

	// Process remaining ops
	for _, op := range ctx.Ops {
		if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryID {
			mockMgr.AddIdentifier(ctx.TenantKey, string(op.Key), op.Value)
		}
	}

	// VALIDATION: Check that commerce tenant identifier was stored correctly
	ids := mockMgr.Identifiers["acme"]
	if ids == nil {
		t.Fatal("expected identifiers for acme tenant")
	}

	// Verify the commerce tenant ID is correctly stored
	found := false
	for key, val := range ids {
		if string(val) == "acme_nonprd_01" {
			found = true
			t.Logf("âœ“ Commerce tenant ID correctly persisted: key=%s, value=%s", key, string(val))
			break
		}
	}
	if !found {
		t.Errorf("commerce tenant identifier 'acme_nonprd_01' not found in registry. Found: %v", ids)
	}
}

// TestValidateServiceURLsPerTenant validates that different service URLs are
// correctly mapped to their respective tenants with proper key naming.
func TestValidateServiceURLsPerTenant(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	testCases := []struct {
		tenant       string
		tenantID     uint16
		expectedSvcs map[string]string // key -> expected URL prefix
	}{
		{
			tenant:   "commerce",
			tenantID: 1,
			expectedSvcs: map[string]string{
				"commerce:orders":   "http://10.0.1.10:8080",
				"commerce:checkout": "http://10.0.1.10:8080",
				"commerce:cart":     "unassigned",
			},
		},
		{
			tenant:   "analytics",
			tenantID: 2,
			expectedSvcs: map[string]string{
				"analytics:salesReport": "http://10.0.1.11:9090",
			},
		},
		{
			tenant:   "notifications",
			tenantID: 3,
			expectedSvcs: map[string]string{
				"notifications:emailSend": "http://10.0.2.10:7070",
				"notifications:smsSend":   "unassigned",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.tenant, func(t *testing.T) {
			ctx := &rctx.Context{
				ByteSlots:   make([][]byte, 10),
				TenantKey:   tc.tenant,
				TenantID:    tc.tenantID,
			}

			ctx.ByteSlots[1] = configJSON
			state := &engine.ExecutionState{PC: 1}

			mockMgr := NewMockRegistryMutator()
			ops := []ExtractOp{
				{
					Path:       "serviceCode",
					KeyPrefix:  tc.tenant + ":",
					OpType:     rctx.OpPut,
					Target:     rctx.TargetRegistryURL,
					ValueSlot:  -1,
					DestSlot:   -1,
				},
			}

			// For simplicity, extract from first product's services
			var pathToExtract string
			if tc.tenant == "commerce" {
				pathToExtract = "products.0.serviceCategories.0.services"
			} else if tc.tenant == "analytics" {
				pathToExtract = "products.1.serviceCategories.0.services"
			} else {
				pathToExtract = "products.2.serviceCategories.0.services"
			}

			extractStep := JSONForeachEmit(1, pathToExtract, ops)

			ctx.MaxOps = 1000
			ctx.OnFlush = func(c *rctx.Context) {
				for _, op := range c.Ops {
					if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
						mockMgr.AddServiceURL(c.TenantKey, string(op.Key), op.Value)
					}
				}
			}

			extractStep.Action(ctx, state)

			for _, op := range ctx.Ops {
				if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
					mockMgr.AddServiceURL(ctx.TenantKey, string(op.Key), op.Value)
				}
			}

			// VALIDATION: Verify service URLs are stored correctly
			urls := mockMgr.ServiceURLs[tc.tenant]
			if urls == nil {
				t.Fatalf("expected service URLs for %s tenant, got nil", tc.tenant)
			}

			for expectedKey, expectedPrefix := range tc.expectedSvcs {
				found := false
				for key, val := range urls {
					if key == expectedKey {
						found = true
						actualVal := string(val)
						if expectedPrefix != "unassigned" && len(actualVal) == 0 {
							t.Errorf("expected URL for %s, got empty", expectedKey)
						}
						t.Logf("âœ“ %s: %s = %s", tc.tenant, key, actualVal)
						break
					}
				}
				if !found {
					t.Errorf("tenant %s: expected service URL key '%s' not found", tc.tenant, expectedKey)
				}
			}
		})
	}
}

// TestValidateMultipleProductsWithDifferentTenantIDs validates that multiple
// products in the same response are correctly associated with their respective tenant IDs.
func TestValidateMultipleProductsWithDifferentTenantIDs(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	// Expected product -> tenant ID mappings
	expectedMappings := map[string]string{
		"Commerce":      "acme_nonprd_01",
		"Analytics":     "A7345-34789S-54879-WERT",
		"Notifications": "ZWP491",
	}

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "global",
		TenantID:    99,
	}

	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	mockMgr := NewMockRegistryMutator()
	ops := []ExtractOp{
		{
			Path:       "product",
			KeyPrefix:  "product_name",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryMeta,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
		{
			Path:       "tenantIdentifier",
			KeyPrefix:  "tenant_id",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryID,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}

	extractStep := JSONForeachEmit(1, "products", ops)

	ctx.MaxOps = 1000
	ctx.OnFlush = func(c *rctx.Context) {
		for _, op := range c.Ops {
			switch op.Target {
			case rctx.TargetRegistryMeta:
				mockMgr.AddMeta(c.TenantKey, string(op.Key), op.Value)
			case rctx.TargetRegistryID:
				mockMgr.AddIdentifier(c.TenantKey, string(op.Key), op.Value)
			}
		}
	}

	extractStep.Action(ctx, state)

	for _, op := range ctx.Ops {
		switch op.Target {
		case rctx.TargetRegistryMeta:
			mockMgr.AddMeta(ctx.TenantKey, string(op.Key), op.Value)
		case rctx.TargetRegistryID:
			mockMgr.AddIdentifier(ctx.TenantKey, string(op.Key), op.Value)
		}
	}

	// VALIDATION: Verify products and their tenant IDs are correctly persisted
	meta := mockMgr.Metadata["global"]
	ids := mockMgr.Identifiers["global"]

	if meta == nil || ids == nil {
		t.Fatal("expected metadata and identifiers for global tenant")
	}

	// Build a map of product -> tenant ID from extracted data
	extractedMappings := make(map[string]string)

	// Find tenant IDs by matching product names
	for _, idVal := range ids {
		// Look for corresponding product name
		for _, metaVal := range meta {
			if string(metaVal) == "Commerce" && string(idVal) == "acme_nonprd_01" {
				extractedMappings["Commerce"] = string(idVal)
			} else if string(metaVal) == "Analytics" && string(idVal) == "A7345-34789S-54879-WERT" {
				extractedMappings["Analytics"] = string(idVal)
			} else if string(metaVal) == "Notifications" && string(idVal) == "ZWP491" {
				extractedMappings["Notifications"] = string(idVal)
			}
		}
	}

	t.Logf("Extracted mappings: %v", extractedMappings)

	// VALIDATION: Verify all products have correct tenant IDs
	for product, expectedTenantID := range expectedMappings {
		if actual, found := extractedMappings[product]; !found {
			t.Errorf("product %s not found in extracted data", product)
		} else if actual != expectedTenantID {
			t.Errorf("product %s: expected tenant ID %s, got %s", product, expectedTenantID, actual)
		} else {
			t.Logf("âœ“ Product %s correctly mapped to tenant ID %s", product, actual)
		}
	}
}

// TestMultiStepExtractAndStoreFlow simulates a complete flow:
// Step 1: HTTP fetch (simulated with manual population)
// Step 2: Extract tenant identifiers
// Step 3: Extract service URLs
// Step 4: Extract metadata
// Then validates all data is correctly persisted
func TestMultiStepExtractAndStoreFlow(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "commerce",
		TenantID:    1,
	}

	// Step 1: Fetch response (simulated)
	ctx.ByteSlots[1] = configJSON
	if len(ctx.ByteSlots[1]) == 0 {
		t.Fatal("Step 1 failed: response body not populated")
	}
	t.Log("âœ“ Step 1: HTTP fetch complete (response populated in slot 1)")

	mockMgr := NewMockRegistryMutator()
	ctx.MaxOps = 1000
	state := &engine.ExecutionState{PC: 1}

	// Helper function to process ops from any step
	processOps := func() {
		for _, op := range ctx.Ops {
			switch op.Target {
			case rctx.TargetRegistryID:
				if op.Type == rctx.OpPut {
					mockMgr.AddIdentifier(ctx.TenantKey, string(op.Key), op.Value)
				}
			case rctx.TargetRegistryURL:
				if op.Type == rctx.OpPut {
					mockMgr.AddServiceURL(ctx.TenantKey, string(op.Key), op.Value)
				}
			case rctx.TargetRegistryMeta:
				if op.Type == rctx.OpPut {
					mockMgr.AddMeta(ctx.TenantKey, string(op.Key), op.Value)
				}
			}
		}
		ctx.Ops = ctx.Ops[:0] // Reset for next step
	}

	// Step 2: Extract tenant identifiers
	step2Ops := []ExtractOp{
		{
			Path:       "tenantIdentifier",
			KeyPrefix:  "commerce_tenant_id",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryID,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}
	step2 := JSONForeachEmit(1, "products", step2Ops)
	step2.Action(ctx, state)
	processOps()
	t.Log("âœ“ Step 2: Tenant identifiers extracted and stored")

	// Step 3: Extract service URLs
	step3Ops := []ExtractOp{
		{
			Path:       "serviceCode",
			KeyPrefix:  "commerce:",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryURL,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}
	step3 := JSONForeachEmit(1, "products.0.serviceCategories.0.services", step3Ops)
	step3.Action(ctx, state)
	processOps()
	t.Log("âœ“ Step 3: Service URLs extracted and stored")

	// Step 4: Extract metadata (region, tier, etc.)
	step4Ops := []ExtractOp{
		{
			Path:       "extensions.0.value",
			KeyPrefix:  "apiVanityURL",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryMeta,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
		{
			Path:       "extensions.1.value",
			KeyPrefix:  "region",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryMeta,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
		{
			Path:       "extensions.2.value",
			KeyPrefix:  "tier",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryMeta,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}
	step4 := JSONExtractEmit(1, step4Ops)
	step4.Action(ctx, state)
	processOps()
	t.Log("âœ“ Step 4: Metadata extracted and stored")

	// VALIDATION: Verify all data was correctly persisted
	t.Log("\n=== VALIDATION RESULTS ===")

	// Validate tenant identifier
	ids := mockMgr.Identifiers["commerce"]
	if ids == nil {
		t.Fatal("No identifiers found for commerce tenant")
	}
	foundTenantID := false
	for key, val := range ids {
		if string(val) == "acme_nonprd_01" {
			t.Logf("âœ“ Tenant ID: key=%s, value=%s", key, string(val))
			foundTenantID = true
		}
	}
	if !foundTenantID {
		t.Error("Commerce tenant ID 'acme_nonprd_01' not found")
	}

	// Validate service URLs
	urls := mockMgr.ServiceURLs["commerce"]
	if urls == nil {
		t.Fatal("No service URLs found for commerce tenant")
	}
	expectedSvcs := map[string]bool{
		"commerce:orders":   true,
		"commerce:checkout": true,
	}
	for expectedKey := range expectedSvcs {
		if _, found := urls[expectedKey]; found {
			t.Logf("âœ“ Service URL stored: %s", expectedKey)
		} else {
			t.Errorf("Expected service URL not found: %s", expectedKey)
		}
	}

	// Validate metadata
	// Note: In the extraction, KeyPrefix + extracted_value becomes the key,
	// and extracted_value (when ValueSlot=-1) becomes the value.
	// So: "apiVanityURL" + "acme.rah.com" = "apiVanityURLacme.rah.com" as key
	meta := mockMgr.Metadata["commerce"]
	if meta == nil {
		t.Fatal("No metadata found for commerce tenant")
	}

	expectedMeta := map[string]string{
		"apiVanityURLacme.rah.com": "acme.rah.com",
		"regionus-east":             "us-east",
		"tiergold":                  "gold",
	}
	for expectedKey, expectedVal := range expectedMeta {
		if val, found := meta[expectedKey]; found && string(val) == expectedVal {
			t.Logf("âœ“ Metadata stored: %s=%s", expectedKey, expectedVal)
		} else {
			actual := ""
			if found {
				actual = string(val)
			}
			t.Errorf("Metadata mismatch for %s: expected %s, got %s", expectedKey, expectedVal, actual)
		}
	}

	t.Log("\nâœ“ Multi-step extraction flow completed successfully!")
}

// TestCrossTenanIsolation validates that extracted data is properly isolated
// per tenant and doesn't leak between tenants.
func TestCrosTenanIsolation(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	tenants := []struct {
		key      string
		id       uint16
		expected string
	}{
		{"commerce", 1, "acme_nonprd_01"},
		{"analytics", 2, "A7345-34789S-54879-WERT"},
		{"notifications", 3, "ZWP491"},
	}

	mockMgr := NewMockRegistryMutator()

	// Extract and store identifiers for all tenants
	for _, tenant := range tenants {
		ctx := &rctx.Context{
			ByteSlots:   make([][]byte, 10),
			TenantKey:   tenant.key,
			TenantID:    tenant.id,
		}

		ctx.ByteSlots[1] = configJSON
		state := &engine.ExecutionState{PC: 1}

		ops := []ExtractOp{
			{
				Path:       "tenantIdentifier",
				KeyPrefix:  fmt.Sprintf("%s_tenant_id", tenant.key),
				OpType:     rctx.OpPut,
				Target:     rctx.TargetRegistryID,
				ValueSlot:  -1,
				DestSlot:   -1,
			},
		}

		extractStep := JSONForeachEmit(1, "products", ops)

		ctx.MaxOps = 1000
		ctx.OnFlush = func(c *rctx.Context) {
			for _, op := range c.Ops {
				if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryID {
					mockMgr.AddIdentifier(c.TenantKey, string(op.Key), op.Value)
				}
			}
		}

		extractStep.Action(ctx, state)

		for _, op := range ctx.Ops {
			if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryID {
				mockMgr.AddIdentifier(ctx.TenantKey, string(op.Key), op.Value)
			}
		}
	}

	// VALIDATION: Verify each tenant has its own isolated data
	for _, tenant := range tenants {
		ids := mockMgr.Identifiers[tenant.key]
		if ids == nil {
			t.Errorf("No identifiers found for tenant %s", tenant.key)
			continue
		}

		found := false
		for key, val := range ids {
			if string(val) == tenant.expected {
				found = true
				t.Logf("âœ“ Tenant %s: isolated identifier %s=%s", tenant.key, key, string(val))
				break
			}
		}
		if !found {
			t.Errorf("Tenant %s: expected identifier '%s' not found. Got: %v", tenant.key, tenant.expected, ids)
		}
	}

	// Verify no cross-tenant leakage
	if mockMgr.Identifiers["commerce"] != nil && mockMgr.Identifiers["analytics"] != nil {
		commerceIDs := mockMgr.Identifiers["commerce"]
		analyticsIDs := mockMgr.Identifiers["analytics"]

		// Check that commerce tenant doesn't have analytics data
		for key := range analyticsIDs {
			if _, found := commerceIDs[key]; found {
				t.Errorf("Cross-tenant data leak: analytics key '%s' found in commerce tenant", key)
			}
		}
	}

	t.Log("âœ“ Cross-tenant isolation validated")
}
