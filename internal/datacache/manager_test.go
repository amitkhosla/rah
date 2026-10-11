package datacache

import (
	"encoding/json"
	"testing"
)

// TestManager_CreateGet tests Create and Get operations.
// Create("test", opts with 1 float64 field) → Get("test") returns non-nil meta
func TestManager_CreateGet(t *testing.T) {
	pool := NewArenaPool(4, 2048, 20)
	quotas := NewQuotaRegistry(10000, 10)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	opts := CreateOpts{
		TenantID: 1,
		App:      "test-app",
		Fields: []FieldDef{
			{Name: "price", Kind: FieldFloat64},
		},
		Capacity: 10,
		MaxCap:   100,
	}

	// Create collection
	err := manager.Create("prices", opts)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Get collection
	meta, err := manager.Get("prices")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}

	if meta.Schema.BufSize <= 0 {
		t.Errorf("expected positive BufSize, got %d", meta.Schema.BufSize)
	}

	if len(meta.Schema.Fields) != 1 {
		t.Errorf("expected 1 field, got %d", len(meta.Schema.Fields))
	}
}

// TestManager_CreateDuplicate tests that creating duplicate names fails.
// Create twice same name → ErrCollectionExists
func TestManager_CreateDuplicate(t *testing.T) {
	pool := NewArenaPool(4, 2048, 20)
	quotas := NewQuotaRegistry(10000, 10)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	opts := CreateOpts{
		TenantID: 1,
		App:      "test-app",
		Fields: []FieldDef{
			{Name: "value", Kind: FieldFloat64},
		},
		Capacity: 10,
	}

	// Create first time
	err := manager.Create("data", opts)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	// Create again should fail
	err = manager.Create("data", opts)
	if err != ErrCollectionExists {
		t.Errorf("expected ErrCollectionExists, got %v", err)
	}
}

// TestManager_Drop tests dropping a collection.
// Create then Drop → Get returns ErrCollectionNotFound
func TestManager_Drop(t *testing.T) {
	pool := NewArenaPool(4, 2048, 20)
	quotas := NewQuotaRegistry(10000, 10)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	opts := CreateOpts{
		TenantID: 1,
		App:      "test-app",
		Fields: []FieldDef{
			{Name: "value", Kind: FieldFloat64},
		},
		Capacity: 10,
	}

	// Create
	err := manager.Create("data", opts)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify it exists
	_, err = manager.Get("data")
	if err != nil {
		t.Fatalf("Get before Drop failed: %v", err)
	}

	// Drop
	err = manager.Drop("data", 1, "test-app")
	if err != nil {
		t.Fatalf("Drop failed: %v", err)
	}

	// Get should now fail
	_, err = manager.Get("data")
	if err != ErrCollectionNotFound {
		t.Errorf("expected ErrCollectionNotFound, got %v", err)
	}
}

// TestManager_AppendQuery tests appending and querying data.
// Create with float64 field "price", AppendJSON with {"price": 42.0}, QueryAll → JSON contains "price"
func TestManager_AppendQuery(t *testing.T) {
	pool := NewArenaPool(4, 2048, 20)
	quotas := NewQuotaRegistry(10000, 10)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	opts := CreateOpts{
		TenantID: 1,
		App:      "test-app",
		Fields: []FieldDef{
			{Name: "price", Kind: FieldFloat64},
		},
		Capacity: 10,
	}

	// Create
	err := manager.Create("prices", opts)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append JSON
	jsonData := []byte(`{"price": 42.0}`)
	err = manager.AppendJSON("prices", jsonData)
	if err != nil {
		t.Fatalf("AppendJSON failed: %v", err)
	}

	// Query all
	result, err := manager.QueryAll("prices", func(size int) []byte {
		return make([]byte, size)
	})
	if err != nil {
		t.Fatalf("QueryAll failed: %v", err)
	}

	// Verify result is valid JSON
	var rows []map[string]interface{}
	err = json.Unmarshal(result, &rows)
	if err != nil {
		t.Fatalf("QueryAll result not valid JSON: %v", err)
	}

	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}

	if price, ok := rows[0]["price"]; ok {
		if priceVal, ok := price.(float64); ok && priceVal != 42.0 {
			t.Errorf("expected price 42.0, got %v", priceVal)
		} else if !ok {
			t.Errorf("price is not float64: %v", price)
		}
	} else {
		t.Error("price field not found in result")
	}
}

// TestManager_AppendVals tests appending via AppendVals.
func TestManager_AppendVals(t *testing.T) {
	pool := NewArenaPool(4, 2048, 20)
	quotas := NewQuotaRegistry(10000, 10)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	opts := CreateOpts{
		TenantID: 1,
		App:      "test-app",
		Fields: []FieldDef{
			{Name: "price", Kind: FieldFloat64},
			{Name: "count", Kind: FieldInt64},
		},
		Capacity: 10,
	}

	// Create
	err := manager.Create("items", opts)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append values
	err = manager.AppendVals("items", []interface{}{99.99, int64(5)})
	if err != nil {
		t.Fatalf("AppendVals failed: %v", err)
	}

	// Query and verify
	result, err := manager.QueryAll("items", func(size int) []byte {
		return make([]byte, size)
	})
	if err != nil {
		t.Fatalf("QueryAll failed: %v", err)
	}

	var rows []map[string]interface{}
	json.Unmarshal(result, &rows)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if rows[0]["price"] != 99.99 {
		t.Errorf("expected price 99.99, got %v", rows[0]["price"])
	}
	if rows[0]["count"] != float64(5) { // JSON unmarshal converts to float64
		t.Errorf("expected count 5, got %v", rows[0]["count"])
	}
}

// TestManager_GetNotFound tests Get on non-existent collection.
func TestManager_GetNotFound(t *testing.T) {
	pool := NewArenaPool(4, 2048, 20)
	quotas := NewQuotaRegistry(10000, 10)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	// Try to get non-existent collection
	_, err := manager.Get("nonexistent")
	if err != ErrCollectionNotFound {
		t.Errorf("expected ErrCollectionNotFound, got %v", err)
	}
}

// TestManager_DropNotFound tests Drop on non-existent collection.
func TestManager_DropNotFound(t *testing.T) {
	pool := NewArenaPool(4, 2048, 20)
	quotas := NewQuotaRegistry(10000, 10)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	// Try to drop non-existent collection
	err := manager.Drop("nonexistent", 1, "test-app")
	if err != ErrCollectionNotFound {
		t.Errorf("expected ErrCollectionNotFound, got %v", err)
	}
}

// TestManager_QuotaEnforcement tests that quota is enforced on Create.
func TestManager_QuotaEnforcement(t *testing.T) {
	pool := NewArenaPool(4, 2048, 100) // maxRecords must be >= capacity
	quotas := NewQuotaRegistry(100, 10) // very small quota

	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	opts := CreateOpts{
		TenantID: 1,
		App:      "test-app",
		Fields: []FieldDef{
			{Name: "value", Kind: FieldFloat64},
		},
		Capacity: 100, // large capacity = large buffer
	}

	// Create should fail due to quota
	err := manager.Create("data", opts)
	if err != ErrQuotaExceeded {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}
}

// TestManager_AppendJSONMissingFields tests that missing JSON fields get zero values.
func TestManager_AppendJSONMissingFields(t *testing.T) {
	pool := NewArenaPool(4, 2048, 20)
	quotas := NewQuotaRegistry(10000, 10)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	opts := CreateOpts{
		TenantID: 1,
		App:      "test-app",
		Fields: []FieldDef{
			{Name: "price", Kind: FieldFloat64},
			{Name: "label", Kind: FieldStringN, Width: 16},
		},
		Capacity: 10,
	}

	// Create
	err := manager.Create("items", opts)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Append JSON with only one field
	jsonData := []byte(`{"price": 50.0}`)
	err = manager.AppendJSON("items", jsonData)
	if err != nil {
		t.Fatalf("AppendJSON failed: %v", err)
	}

	// Query and verify missing fields are zero-valued
	result, err := manager.QueryAll("items", func(size int) []byte {
		return make([]byte, size)
	})
	if err != nil {
		t.Fatalf("QueryAll failed: %v", err)
	}

	var rows []map[string]interface{}
	json.Unmarshal(result, &rows)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if rows[0]["price"] != 50.0 {
		t.Errorf("price should be 50.0, got %v", rows[0]["price"])
	}
	// Missing label field should be zero-valued (empty string after trim)
	if label, ok := rows[0]["label"]; ok {
		if label != "" {
			t.Errorf("label should be empty string, got %v", label)
		}
	}
}

// TestManager_MultipleCollections tests creating multiple collections.
func TestManager_MultipleCollections(t *testing.T) {
	pool := NewArenaPool(10, 4096, 20)
	quotas := NewQuotaRegistry(100000, 20)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	// Create first collection
	err := manager.Create("col1", CreateOpts{
		TenantID: 1,
		App:      "app1",
		Fields:   []FieldDef{{Name: "val", Kind: FieldFloat64}},
		Capacity: 5,
	})
	if err != nil {
		t.Fatalf("Create col1 failed: %v", err)
	}

	// Create second collection
	err = manager.Create("col2", CreateOpts{
		TenantID: 1,
		App:      "app1",
		Fields:   []FieldDef{{Name: "val", Kind: FieldInt64}},
		Capacity: 5,
	})
	if err != nil {
		t.Fatalf("Create col2 failed: %v", err)
	}

	// Both should exist
	_, err = manager.Get("col1")
	if err != nil {
		t.Errorf("Get col1 failed: %v", err)
	}

	_, err = manager.Get("col2")
	if err != nil {
		t.Errorf("Get col2 failed: %v", err)
	}

	// Drop one and verify the other still exists
	manager.Drop("col1", 1, "app1")

	_, err = manager.Get("col1")
	if err != ErrCollectionNotFound {
		t.Error("col1 should not exist after drop")
	}

	_, err = manager.Get("col2")
	if err != nil {
		t.Error("col2 should still exist")
	}
}

// TestManager_DropAndRecreate tests that dropping and recreating a collection works.
func TestManager_DropAndRecreate(t *testing.T) {
	pool := NewArenaPool(4, 64<<10, 100)
	quotas := NewQuotaRegistry(1<<30, 10)
	mgr := NewDataCacheManager(pool, quotas)
	defer mgr.Close()

	fields := []FieldDef{{Name: "x", Kind: FieldFloat64, Width: 8}}
	opts := CreateOpts{Fields: fields, Capacity: 10}

	if err := mgr.Create("mycoll", opts); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mgr.Drop("mycoll", 0, ""); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	// Re-create with same name must succeed
	if err := mgr.Create("mycoll", opts); err != nil {
		t.Errorf("re-Create after Drop: %v", err)
	}
}
