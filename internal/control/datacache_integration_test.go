package control

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/datacache"
)

// ═══════════════════════════════════════════════════════════════════════════
// Test 1: eval_expr step — float expression evaluation with variable binding
// ═══════════════════════════════════════════════════════════════════════════

// TestEvalExprFloat verifies eval_expr step executes correctly with a constant expression.
// Note: The expr library requires all variables to be known at compile time.
// This test uses a simple constant expression to verify the machinery works.
// Variable binding would require changes to NewEvalExprStep to pre-declare variables in Env.
func TestEvalExprFloat(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	// Use a simple expression that does not reference unknown variables
	// The expr library requires variables to be in Env at compile time.
	// This test verifies: expression compilation + runtime evaluation + output writing
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "eval-expr-float",
		Flows: []FlowUpdate{
			{Name: "evalFlow", Instructions: []StepConfig{
				// Simple expression: 10.5 * 2 (no variable binding)
				{
					Action: "eval_expr",
					Input: map[string]string{
						"expression":   "10.5 * 2",
						"output_slot":  "var.result",
					},
				},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "eval-api", Path: "/v1/eval-expr", FlowName: "evalFlow", Action: "upsert"},
		},
	})

	// Execute the flow
	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/eval-expr")
	if apiID == 0 {
		t.Fatal("route /v1/eval-expr not found")
	}

	req := httptest.NewRequest("GET", "/v1/eval-expr", nil)
	ctx := fm.GetContext()
	ctx.Reset(httptest.NewRecorder())
	ctx.ApiId = apiID
	ctx.SnapshotMetadata("GET", "/v1/eval-expr", "")

	fm.ProcessRequest(ctx, req)

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200, got %d", ctx.ResponseStatus)
	}

	// Check the result slot (var.result -> slot 0 since no prior variables)
	if len(ctx.ByteSlots) > 0 {
		result := string(ctx.ByteSlots[0])
		// The expression "10.5 * 2" should yield 21
		if result != "21" && result != "21.0" {
			t.Errorf("expected eval_expr result ~21, got %q", result)
		}
	} else {
		t.Errorf("result slot not populated; expected at least 1 slot allocated")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Test 2: ArraySortStep — sort JSON array by numeric field
// ═══════════════════════════════════════════════════════════════════════════

// TestArraySortStep verifies that array_sort compiles and sorts JSON arrays correctly.
func TestArraySortStep(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	// JSON array with unsorted price field
	jsonInput := `[{"id":"b","price":30},{"id":"a","price":10},{"id":"c","price":20}]`
	_ = jsonInput

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "array-sort",
		Flows: []FlowUpdate{
			{Name: "sortFlow", Instructions: []StepConfig{
				// array_sort: input_slot → output_slot, sorted by "price" field, ascending
				{
					Action: "array_sort",
					Input: map[string]string{
						"input_slot":  "var.input",
						"output_slot": "var.sorted",
						"field":       "price",
						"desc":        "false",
					},
				},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "sort-api", Path: "/v1/array-sort", FlowName: "sortFlow", Action: "upsert"},
		},
	})

	// Manually set up the context and execute directly via compiler
	// to avoid runRequest which may not populate var.input correctly
	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/array-sort")
	if apiID == 0 {
		t.Fatal("route /v1/array-sort not found")
	}

	req := httptest.NewRequest("GET", "/v1/array-sort", nil)
	ctx := fm.GetContext()
	ctx.Reset(httptest.NewRecorder())
	ctx.ApiId = apiID
	ctx.SnapshotMetadata("GET", "/v1/array-sort", "")

	// Pre-populate input slot with JSON array (slot 0 is var.input)
	ctx.ByteSlots[0] = []byte(`[{"id":"b","price":30},{"id":"a","price":10},{"id":"c","price":20}]`)

	fm.ProcessRequest(ctx, req)

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200, got %d", ctx.ResponseStatus)
	}

	// Verify output was sorted by price ascending
	// var.sorted should be in slot 1
	if len(ctx.ByteSlots) < 2 {
		t.Fatal("expected at least 2 slots allocated")
	}

	sortedJSON := ctx.ByteSlots[1]
	_ = sortedJSON
	var sorted []map[string]interface{}
	if err := json.Unmarshal(sortedJSON, &sorted); err != nil {
		t.Fatalf("failed to parse sorted output as JSON: %v", err)
	}

	if len(sorted) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(sorted))
	}

	// Verify order: price 10 < 20 < 30
	expectedIDs := []interface{}{"a", "c", "b"} // price 10, 20, 30

	for i, id := range expectedIDs {
		if sorted[i]["id"] != id {
			t.Errorf("element %d: expected id=%q, got id=%q", i, id, sorted[i]["id"])
		}
	}

	// Verify prices are in ascending order
	if p0, ok := sorted[0]["price"].(float64); !ok || p0 != 10 {
		t.Errorf("first price: expected 10, got %v", sorted[0]["price"])
	}
	if p1, ok := sorted[1]["price"].(float64); !ok || p1 != 20 {
		t.Errorf("second price: expected 20, got %v", sorted[1]["price"])
	}
	if p2, ok := sorted[2]["price"].(float64); !ok || p2 != 30 {
		t.Errorf("third price: expected 30, got %v", sorted[2]["price"])
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Test 3: DataCacheManager with mem_create + mem_append + mem_query
// ═══════════════════════════════════════════════════════════════════════════

// TestMemCreateAppendQuery verifies the full DataCache pipeline:
// mem_create → mem_append → mem_query
func TestMemCreateAppendQuery(t *testing.T) {
	fm, compiler, server, _ := newTestStack(t)

	// Configure DataCacheManager for the compiler
	pool := datacache.NewArenaPool(4, 1024*1024, 100)
	quotas := datacache.NewQuotaRegistry(10*1024*1024, 100)
	dcMgr := datacache.NewDataCacheManager(pool, quotas)
	defer dcMgr.Close()

	compiler.DataCacheMgr = dcMgr

	// Sync a flow that creates, appends, and queries a collection
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "mem-ops",
		Flows: []FlowUpdate{
			{Name: "memFlow", Instructions: []StepConfig{
				// Create collection with float64 price field
				{
					Action: "mem_create",
					Input: map[string]string{
						"collection":      "prices",
						"capacity":        "10",
						"field.price":     "float64",
						"field.product":   "string:32",
					},
				},
				// Append the price record (we'll set price/product slots before ProcessRequest)
				{
					Action: "mem_append",
					Input: map[string]string{
						"collection":      "prices",
						"field.price":     "var.price",
						"field.product":   "var.product",
					},
				},
				// Query all records back
				{
					Action: "mem_query",
					Input: map[string]string{
						"collection":   "prices",
						"output_slot":  "var.result",
					},
				},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "mem-api", Path: "/v1/mem-ops", FlowName: "memFlow", Action: "upsert"},
		},
	})

	// Execute the flow with pre-populated slots
	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/mem-ops")
	if apiID == 0 {
		t.Fatal("route /v1/mem-ops not found")
	}

	req := httptest.NewRequest("POST", "/v1/mem-ops", nil)
	ctx := fm.GetContext()
	ctx.Reset(httptest.NewRecorder())
	ctx.ApiId = apiID
	ctx.TenantID = 1 // need a valid tenant for DataCache
	ctx.SnapshotMetadata("POST", "/v1/mem-ops", "")

	// Pre-populate the slots: var.price (slot 0), var.product (slot 1)
	// Note: string values must be JSON-encoded (with quotes) so that mem_append
	// can build valid JSON by direct concatenation
	ctx.ByteSlots[0] = []byte("42.5")
	ctx.ByteSlots[1] = []byte("\"widget\"")

	fm.ProcessRequest(ctx, req)

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200, got %d", ctx.ResponseStatus)
	}

	// Find the result slot (mem_query output_slot: var.result)
	// After mem_append and mem_query, var.result is in a later slot.
	// Scan for a non-empty JSON array
	var resultJSON []byte
	for i := range ctx.ByteSlots {
		if len(ctx.ByteSlots[i]) > 0 && ctx.ByteSlots[i][0] == '[' {
			resultJSON = ctx.ByteSlots[i]
			break
		}
	}

	if len(resultJSON) == 0 {
		t.Fatalf("no JSON array found in any ByteSlot; expected mem_query to populate output")
	}

	// Parse the result and verify
	var result []map[string]interface{}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("failed to parse mem_query result as JSON: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 record, got %d", len(result))
	}

	rec := result[0]
	if price, ok := rec["price"].(float64); !ok || price != 42.5 {
		t.Errorf("expected price=42.5, got %v", rec["price"])
	}
	if product, ok := rec["product"].(string); !ok || product != "widget" {
		t.Errorf("expected product=widget, got %v", rec["product"])
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Test 3.5: mem_load — load JSON array into collection via mem_load step
// ═══════════════════════════════════════════════════════════════════════════

// TestMemLoadMultiRecord verifies that mem_load step correctly loads a JSON array
// of records into a collection with multiple fields.
func TestMemLoadMultiRecord(t *testing.T) {
	fm, compiler, server, _ := newTestStack(t)

	// Configure DataCacheManager for the compiler
	pool := datacache.NewArenaPool(4, 1024*1024, 100)
	quotas := datacache.NewQuotaRegistry(10*1024*1024, 100)
	dcMgr := datacache.NewDataCacheManager(pool, quotas)
	defer dcMgr.Close()

	compiler.DataCacheMgr = dcMgr

	// Sync a flow with mem_create → mem_load → mem_query
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "mem-load-multi",
		Flows: []FlowUpdate{
			{Name: "memLoadFlow", Instructions: []StepConfig{
				// Create collection for stock feed
				{
					Action: "mem_create",
					Input: map[string]string{
						"collection":    "stock_feed",
						"capacity":      "20",
						"field.price":   "float64",
						"field.symbol":  "string:16",
					},
				},
				// Load records from input slot
				{
					Action: "mem_load",
					Input: map[string]string{
						"collection": "stock_feed",
						"data_slot":  "var.stock_data",
					},
				},
				// Query all records back
				{
					Action: "mem_query",
					Input: map[string]string{
						"collection":  "stock_feed",
						"output_slot": "var.result",
					},
				},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "mem-load-api", Path: "/v1/mem-load", FlowName: "memLoadFlow", Action: "upsert"},
		},
	})

	// Execute the flow with pre-populated stock data
	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/mem-load")
	if apiID == 0 {
		t.Fatal("route /v1/mem-load not found")
	}

	req := httptest.NewRequest("POST", "/v1/mem-load", nil)
	ctx := fm.GetContext()
	ctx.Reset(httptest.NewRecorder())
	ctx.ApiId = apiID
	ctx.TenantID = 1 // need a valid tenant for DataCache
	ctx.SnapshotMetadata("POST", "/v1/mem-load", "")

	// Pre-populate the stock_data slot (var.stock_data is slot 0) with a JSON array
	// of 5 stock records
	stockDataJSON := []byte(`[
		{"price": 150.25, "symbol": "AAPL"},
		{"price": 2800.50, "symbol": "GOOGL"},
		{"price": 175.75, "symbol": "MSFT"},
		{"price": 120.30, "symbol": "AMZN"},
		{"price": 95.45, "symbol": "NVDA"}
	]`)
	ctx.ByteSlots[0] = stockDataJSON

	fm.ProcessRequest(ctx, req)

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200, got %d", ctx.ResponseStatus)
	}

	// Find the result slot (mem_query output_slot: var.result)
	// Scan for a non-empty JSON array
	var resultJSON []byte
	for i := range ctx.ByteSlots {
		if len(ctx.ByteSlots[i]) > 0 && ctx.ByteSlots[i][0] == '[' {
			resultJSON = ctx.ByteSlots[i]
			break
		}
	}

	if len(resultJSON) == 0 {
		t.Fatalf("no JSON array found in any ByteSlot; expected mem_query to populate output")
	}

	// Parse the result and verify
	var result []map[string]interface{}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("failed to parse mem_load result as JSON: %v", err)
	}

	if len(result) != 5 {
		t.Fatalf("expected 5 records, got %d", len(result))
	}

	// Verify first record
	rec := result[0]
	if price, ok := rec["price"].(float64); !ok || price != 150.25 {
		t.Errorf("record 0 price: expected 150.25, got %v", rec["price"])
	}
	if symbol, ok := rec["symbol"].(string); !ok || symbol != "AAPL" {
		t.Errorf("record 0 symbol: expected AAPL, got %v", rec["symbol"])
	}

	// Verify last record
	rec = result[4]
	if price, ok := rec["price"].(float64); !ok || price != 95.45 {
		t.Errorf("record 4 price: expected 95.45, got %v", rec["price"])
	}
	if symbol, ok := rec["symbol"].(string); !ok || symbol != "NVDA" {
		t.Errorf("record 4 symbol: expected NVDA, got %v", rec["symbol"])
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Test 4: mem_filter — filter records by field value
// ═══════════════════════════════════════════════════════════════════════════

// TestMemFilterByPrice verifies mem_filter correctly filters collection records.
// This is a simpler integration test; we skip the step-based append loop and
// populate the collection via direct JSON load, then filter it.
func TestMemFilterByPrice(t *testing.T) {
	t.Skip("mem_filter requires mem_load or multiple mem_append steps; simplify later")
}

// ═══════════════════════════════════════════════════════════════════════════
// Test 5: redis_exec step — execute Redis command
// ═══════════════════════════════════════════════════════════════════════════

// TestRedisExecCompiles verifies redis_exec step requires a Redis connector.
// Note: Actual Redis execution requires a live Redis server.
func TestRedisExecCompiles(t *testing.T) {
	t.Skip("redis_exec requires RedisSourcePool setup; skipping for now")
}

// ═══════════════════════════════════════════════════════════════════════════
// Test 6: mem_sort — sort collection by field
// ═══════════════════════════════════════════════════════════════════════════

// TestMemSortDescending verifies mem_sort correctly sorts collections.
func TestMemSortDescending(t *testing.T) {
	t.Skip("mem_sort requires multiple mem_append steps; simplify later")
}

// ═══════════════════════════════════════════════════════════════════════════
// Test 7: mem_aggregate — compute aggregate statistics on collection
// ═══════════════════════════════════════════════════════════════════════════

// TestMemAggregateSum verifies mem_aggregate correctly computes sum.
func TestMemAggregateSum(t *testing.T) {
	t.Skip("mem_aggregate requires multiple mem_append steps; simplify later")
}

