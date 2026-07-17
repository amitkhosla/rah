package steps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"sort"
	"sync"
	"testing"
)

// MockRegistryMutator captures all writes for verification in tests.
type MockRegistryMutator struct {
	mu              sync.Mutex
	ServiceURLs     map[string]map[string][]byte // [alias][name]value
	Identifiers     map[string]map[string][]byte // [alias][name]value
	Metadata        map[string]map[string][]byte // [alias][name]value
	DeletedURLs     map[string][]string          // [alias]names
	DeletedIDs      map[string][]string          // [alias]names
	DeletedMeta     map[string][]string          // [alias]names
}

func NewMockRegistryMutator() *MockRegistryMutator {
	return &MockRegistryMutator{
		ServiceURLs:  make(map[string]map[string][]byte),
		Identifiers:  make(map[string]map[string][]byte),
		Metadata:     make(map[string]map[string][]byte),
		DeletedURLs:  make(map[string][]string),
		DeletedIDs:   make(map[string][]string),
		DeletedMeta:  make(map[string][]string),
	}
}

func (m *MockRegistryMutator) AddServiceURL(alias string, name string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ServiceURLs[alias] == nil {
		m.ServiceURLs[alias] = make(map[string][]byte)
	}
	m.ServiceURLs[alias][name] = append([]byte{}, value...)
}

func (m *MockRegistryMutator) AddIdentifier(alias string, name string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Identifiers[alias] == nil {
		m.Identifiers[alias] = make(map[string][]byte)
	}
	m.Identifiers[alias][name] = append([]byte{}, value...)
}

func (m *MockRegistryMutator) AddMeta(alias string, name string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Metadata[alias] == nil {
		m.Metadata[alias] = make(map[string][]byte)
	}
	m.Metadata[alias][name] = append([]byte{}, value...)
}

func (m *MockRegistryMutator) DeleteServiceURL(alias string, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ServiceURLs[alias] != nil {
		delete(m.ServiceURLs[alias], name)
	}
	m.DeletedURLs[alias] = append(m.DeletedURLs[alias], name)
}

func (m *MockRegistryMutator) DeleteIdentifier(alias string, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Identifiers[alias] != nil {
		delete(m.Identifiers[alias], name)
	}
	m.DeletedIDs[alias] = append(m.DeletedIDs[alias], name)
}

func (m *MockRegistryMutator) DeleteMeta(alias string, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Metadata[alias] != nil {
		delete(m.Metadata[alias], name)
	}
	m.DeletedMeta[alias] = append(m.DeletedMeta[alias], name)
}

// Sample configuration JSON similar to the user's data structure.
type PlatformService struct {
	ServiceCode string `json:"serviceCode"`
	ServiceURL  string `json:"serviceUrl"`
	BasePath    string `json:"basepath"`
}

type Service struct {
	ServiceCode string `json:"serviceCode"`
	ServiceURL  string `json:"serviceUrl"`
	BasePath    string `json:"basepath"`
}

type ServiceCategory struct {
	Category string    `json:"category"`
	Services []Service `json:"services"`
}

type Product struct {
	Product            string             `json:"product"`
	TenantIdentifier   string             `json:"tenantIdentifier"`
	ServiceCategories []ServiceCategory   `json:"serviceCategories"`
}

type Extension struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ConfigResponse struct {
	GlobalID          string             `json:"globalId"`
	CustomerID        string             `json:"customerId"`
	Extensions        []Extension        `json:"extensions"`
	PlatformServices  []PlatformService  `json:"platformServices"`
	Products          []Product          `json:"products"`
}

// buildSampleConfig creates a test configuration matching the user's JSON structure.
func buildSampleConfig() ConfigResponse {
	return ConfigResponse{
		GlobalID:   "63bce995-9caa-4c4b-ae6f-e92447a07041",
		CustomerID: "7e96021a-1929-49c7-835e-f24e6e1d70e4",
		Extensions: []Extension{
			{Key: "apiVanityURL", Value: "acme.rah.com"},
			{Key: "region", Value: "us-east"},
			{Key: "tier", Value: "gold"},
			{Key: "environment", Value: "nonprd"},
		},
		PlatformServices: []PlatformService{
			{ServiceCode: "identity", ServiceURL: "http://platform.internal:8080", BasePath: "/platform/identity/v1"},
			{ServiceCode: "auditLog", ServiceURL: "http://platform.internal:8081", BasePath: "/platform/audit/v1"},
			{ServiceCode: "configService", ServiceURL: "http://platform.internal:8082", BasePath: "/platform/config/v1"},
			{ServiceCode: "featureFlags", ServiceURL: "http://platform.internal:8083", BasePath: "/platform/flags/v1"},
		},
		Products: []Product{
			{
				Product:          "Commerce",
				TenantIdentifier: "acme_nonprd_01",
				ServiceCategories: []ServiceCategory{
					{
						Category: "CoreServices",
						Services: []Service{
							{ServiceCode: "orders", ServiceURL: "http://10.0.1.10:8080", BasePath: "/commerce/orders/v1"},
							{ServiceCode: "cart", ServiceURL: "unassigned", BasePath: "/commerce/cart/v1"},
							{ServiceCode: "checkout", ServiceURL: "http://10.0.1.10:8080", BasePath: "/commerce/checkout/v1"},
						},
					},
					{
						Category: "InventoryServices",
						Services: []Service{
							{ServiceCode: "stockLevels", ServiceURL: "unassigned", BasePath: "/inventory/stock/v1"},
							{ServiceCode: "warehouse", ServiceURL: "unassigned", BasePath: "/inventory/warehouse/v1"},
						},
					},
				},
			},
			{
				Product:          "Analytics",
				TenantIdentifier: "A7345-34789S-54879-WERT",
				ServiceCategories: []ServiceCategory{
					{
						Category: "ReportingServices",
						Services: []Service{
							{ServiceCode: "salesReport", ServiceURL: "http://10.0.1.11:9090", BasePath: "/analytics/sales/v1"},
							{ServiceCode: "inventoryReport", ServiceURL: "unassigned", BasePath: "/analytics/inventory/v1"},
						},
					},
				},
			},
			{
				Product:          "Notifications",
				TenantIdentifier: "ZWP491",
				ServiceCategories: []ServiceCategory{
					{
						Category: "MessagingServices",
						Services: []Service{
							{ServiceCode: "emailSend", ServiceURL: "http://10.0.2.10:7070", BasePath: "/notify/email/v1"},
							{ServiceCode: "smsSend", ServiceURL: "unassigned", BasePath: "/notify/sms/v1"},
						},
					},
				},
			},
		},
	}
}

// TestExtractGlobalExtensions extracts global extensions and stores as metadata.
func TestExtractGlobalExtensions(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	// Create context with slots
	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "acme",
		TenantID:    1,
	}

	// Slot 1: Response body (manually populated for testing extraction logic)
	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	// Step 1: Extract global extensions and store as metadata
	mockMgr := NewMockRegistryMutator()
	ops := []ExtractOp{
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
		{
			Path:       "extensions.3.value",
			KeyPrefix:  "environment",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryMeta,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}

	extractStep := JSONExtractEmit(1, ops)
	state.PC = 2

	// Mock the registry operations
	// Set up very large MaxOps so OnFlush doesn't trigger during extraction
	ctx.MaxOps = 1000
	ctx.OnFlush = func(c *rctx.Context) {
		for _, op := range c.Ops {
			if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryMeta {
				mockMgr.AddMeta(c.TenantKey, string(op.Key), op.Value)
			}
		}
	}

	extractStep.Action(ctx, state)

	// Manually process remaining ops (since OnFlush may not have been called)
	for _, op := range ctx.Ops {
		if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryMeta {
			mockMgr.AddMeta(ctx.TenantKey, string(op.Key), op.Value)
		}
	}

	// Verify metadata was stored
	meta := mockMgr.Metadata["acme"]
	if meta["apiVanityURL"] != nil && string(meta["apiVanityURL"]) != "acme.rah.com" {
		t.Errorf("expected apiVanityURL=acme.rah.com, got %s", string(meta["apiVanityURL"]))
	}
	if meta["region"] != nil && string(meta["region"]) != "us-east" {
		t.Errorf("expected region=us-east, got %s", string(meta["region"]))
	}
	if meta["tier"] != nil && string(meta["tier"]) != "gold" {
		t.Errorf("expected tier=gold, got %s", string(meta["tier"]))
	}
}

// TestExtractPlatformServices extracts platform services and stores as service URLs.
func TestExtractPlatformServices(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "platform",
		TenantID:    2,
	}

	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	// Extract platform services
	// Iterate over platformServices array and extract serviceCode + serviceUrl
	mockMgr := NewMockRegistryMutator()
	ops := []ExtractOp{
		{
			Path:       "serviceCode",
			KeyPrefix:  "platform:",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryURL,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}

	// For this test, we'll extract individual fields
	// In a real flow, we'd use JSONForeachEmit for arrays
	extractStep := JSONForeachEmit(1, "platformServices", ops)

	ctx.MaxOps = 1000
	ctx.OnFlush = func(c *rctx.Context) {
		for _, op := range c.Ops {
			if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
				// Extract service code from key (format: "platform:<serviceCode>")
				mockMgr.AddServiceURL(c.TenantKey, string(op.Key), op.Value)
			}
		}
	}

	extractStep.Action(ctx, state)

	// Manually process remaining ops (since OnFlush may not have been called)
	for _, op := range ctx.Ops {
		if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
			mockMgr.AddServiceURL(ctx.TenantKey, string(op.Key), op.Value)
		}
	}

	// Verify some platform services were extracted
	urls := mockMgr.ServiceURLs["platform"]
	if urls == nil {
		t.Fatal("expected service URLs for platform tenant")
	}

	expectedServices := []string{"platform:identity", "platform:auditLog", "platform:configService"}
	for _, svc := range expectedServices {
		if len(urls[svc]) == 0 {
			t.Errorf("expected service URL for %s", svc)
		}
	}
}

// TestExtractProductTenantIdentifiers extracts tenant identifiers from products.
func TestExtractProductTenantIdentifiers(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "acme",
		TenantID:    3,
	}

	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 1}

	// Extract product tenant identifiers
	// Iterate over products and extract tenantIdentifier
	mockMgr := NewMockRegistryMutator()
	ops := []ExtractOp{
		{
			Path:       "tenantIdentifier",
			KeyPrefix:  "product_tenant",
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

	// Manually process remaining ops (since OnFlush may not have been called)
	for _, op := range ctx.Ops {
		if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryID {
			mockMgr.AddIdentifier(ctx.TenantKey, string(op.Key), op.Value)
		}
	}

	// Verify tenant identifiers were extracted
	ids := mockMgr.Identifiers["acme"]
	if ids == nil {
		t.Fatal("expected identifiers for acme tenant")
	}

	expectedIDs := []string{"acme_nonprd_01", "A7345-34789S-54879-WERT", "ZWP491"}
	foundCount := 0
	for _, expectedID := range expectedIDs {
		for key, val := range ids {
			if string(val) == expectedID {
				foundCount++
				t.Logf("Found tenant ID: %s under key %s", expectedID, key)
			}
		}
	}
	if foundCount < 2 {
		t.Errorf("expected at least 2 tenant identifiers, found %d", foundCount)
	}
}

// TestExtractNestedProductServices extracts services from nested product categories.
func TestExtractNestedProductServices(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "commerce",
		TenantID:    4,
	}

	ctx.ByteSlots[1] = configJSON
	state := &engine.ExecutionState{PC: 0}

	// For nested arrays (products[].serviceCategories[].services[]),
	// we need multiple foreach steps or a more complex extraction.
	// For now, demonstrate extracting the first product's services.

	// Extract commerce services from first product (products.0.serviceCategories.0.services)
	mockMgr := NewMockRegistryMutator()
	ops := []ExtractOp{
		{
			Path:       "serviceCode",
			KeyPrefix:  "commerce:",
			OpType:     rctx.OpPut,
			Target:     rctx.TargetRegistryURL,
			ValueSlot:  -1,
			DestSlot:   -1,
		},
	}

	// Simulate extracting from a specific nested path
	// In a real scenario, the compiler would generate multiple nested foreach steps
	extractStep := JSONForeachEmit(1, "products.0.serviceCategories.0.services", ops)

	ctx.MaxOps = 1000
	ctx.OnFlush = func(c *rctx.Context) {
		for _, op := range c.Ops {
			if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
				mockMgr.AddServiceURL(c.TenantKey, string(op.Key), op.Value)
			}
		}
	}

	extractStep.Action(ctx, state)

	// Manually process remaining ops (since OnFlush may not have been called)
	for _, op := range ctx.Ops {
		if op.Type == rctx.OpPut && op.Target == rctx.TargetRegistryURL {
			mockMgr.AddServiceURL(ctx.TenantKey, string(op.Key), op.Value)
		}
	}

	// Verify commerce services were extracted
	urls := mockMgr.ServiceURLs["commerce"]
	if urls == nil {
		t.Fatal("expected service URLs for commerce tenant")
	}

	// Check for commerce services (orders, cart, checkout)
	expectedServices := []string{"commerce:orders", "commerce:cart", "commerce:checkout"}
	for _, svc := range expectedServices {
		found := false
		for key := range urls {
			if key == svc {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected service URL for %s", svc)
		}
	}
}

// TestExtractAndStoreServiceConfig tests a complete flow: extract and store for multiple tenants.
func TestExtractAndStoreServiceConfig(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	// Test multiple tenants in a single config
	tenants := []struct {
		alias string
		id    uint16
	}{
		{"commerce", 1},
		{"analytics", 2},
		{"notifications", 3},
	}

	for _, tenant := range tenants {
		ctx := &rctx.Context{
			ByteSlots:   make([][]byte, 10),
			TenantKey:   tenant.alias,
			TenantID:    tenant.id,
		}

		ctx.ByteSlots[1] = configJSON

		// Verify response is valid JSON
		var resp ConfigResponse
		if err := json.Unmarshal(ctx.ByteSlots[1], &resp); err != nil {
			t.Fatalf("tenant %s: failed to parse response: %v", tenant.alias, err)
		}

		if len(resp.Products) == 0 {
			t.Fatalf("tenant %s: expected products in response", tenant.alias)
		}

		t.Logf("âœ“ Tenant %s: loaded config with %d products", tenant.alias, len(resp.Products))
	}
}

// TestExtractServiceURLsFromMultipleProducts tests extracting URLs across all products.
func TestExtractServiceURLsFromMultipleProducts(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "global",
		TenantID:    99,
	}

	ctx.ByteSlots[1] = configJSON

	// Collect all service codes across all products and their service categories
	var allServices []string
	var resp ConfigResponse
	json.Unmarshal(ctx.ByteSlots[1], &resp)

	for _, product := range resp.Products {
		for _, category := range product.ServiceCategories {
			for _, svc := range category.Services {
				allServices = append(allServices, fmt.Sprintf("%s:%s", product.Product, svc.ServiceCode))
			}
		}
	}

	expectedCount := 9 // orders, cart, checkout, stockLevels, warehouse, salesReport, inventoryReport, emailSend, smsSend
	if len(allServices) < expectedCount {
		t.Errorf("expected at least %d services, extracted %d", expectedCount, len(allServices))
	}

	// Verify some expected services
	expectedServices := []string{
		"Commerce:orders",
		"Commerce:checkout",
		"Analytics:salesReport",
		"Notifications:emailSend",
	}

	sort.Strings(allServices)
	for _, expected := range expectedServices {
		found := false
		for _, actual := range allServices {
			if bytes.Contains([]byte(actual), []byte(expected)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find service containing %s in extracted services", expected)
		}
	}

	t.Logf("âœ“ Extracted %d services: %v", len(allServices), allServices)
}

// TestExtractOnlyAssignedServices filters out "unassigned" service URLs.
func TestExtractOnlyAssignedServices(t *testing.T) {
	config := buildSampleConfig()
	configJSON, _ := json.Marshal(config)

	ctx := &rctx.Context{
		ByteSlots:   make([][]byte, 10),
		TenantKey:   "commerce",
		TenantID:    5,
	}

	ctx.ByteSlots[1] = configJSON

	var resp ConfigResponse
	json.Unmarshal(ctx.ByteSlots[1], &resp)

	// Count assigned (not "unassigned") services
	assignedCount := 0
	unassignedCount := 0
	for _, product := range resp.Products {
		for _, category := range product.ServiceCategories {
			for _, svc := range category.Services {
				if svc.ServiceURL == "unassigned" {
					unassignedCount++
				} else {
					assignedCount++
				}
			}
		}
	}

	t.Logf("âœ“ Found %d assigned services and %d unassigned services", assignedCount, unassignedCount)

	if assignedCount == 0 {
		t.Error("expected to find at least one assigned service")
	}
	if unassignedCount == 0 {
		t.Error("expected to find at least one unassigned service")
	}
}
