package control

import (
	"testing"
	"unsafe"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/datasource"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test 1: Linter accepts db_foreach action
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_LinterAcceptsAction(t *testing.T) {
	descriptors := AllStepDescriptors()
	idx := make(map[string]StepDescriptor)
	for _, desc := range descriptors {
		idx[desc.Type] = desc
	}

	// Assert db_foreach exists in the index
	desc, ok := idx["db_foreach"]
	if !ok {
		t.Fatal("db_foreach not found in step descriptors")
	}

	// Verify the descriptor has correct Type
	if desc.Type != "db_foreach" {
		t.Errorf("expected descriptor Type 'db_foreach', got %q", desc.Type)
	}

	// Verify the descriptor is properly configured
	if desc.Title == "" {
		t.Error("db_foreach descriptor missing Title")
	}
	if desc.Category == "" {
		t.Error("db_foreach descriptor missing Category")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 2: Compiler happy path — valid db_foreach step
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_Compiles_ValidStep(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = createMockDataSourcePool(map[string]bool{"test_db": true})

	step := StepConfig{
		Action: "db_foreach",
		Key:    "test_db",
		Value:  "SELECT id, name FROM accounts",
		Bind: map[string]string{
			"id":   "acc_id",
			"name": "acc_name",
		},
		Do: []StepConfig{
			{Action: "set_response_body", Value: "ok"},
		},
	}

	// Compile the step
	err := compiler.compileStep(step, nil)
	if err != nil {
		t.Fatalf("compileStep failed: %v", err)
	}

	// Assert: GlobalTable contains DB_FOREACH_GATE instruction
	foundGate := false
	foundRepeat := false
	for _, instr := range compiler.GlobalTable {
		if instr.Name == "DB_FOREACH_GATE" {
			foundGate = true
		}
		if instr.Name == "LOOP_REPEAT" {
			foundRepeat = true
		}
	}

	if !foundGate {
		t.Error("GlobalTable does not contain DB_FOREACH_GATE instruction")
	}
	if !foundRepeat {
		t.Error("GlobalTable does not contain LOOP_REPEAT instruction")
	}

	// Assert: slotMap contains allocated slot names
	if _, ok := compiler.slotMap["acc_id"]; !ok {
		t.Error("slotMap missing 'acc_id'")
	}
	if _, ok := compiler.slotMap["acc_name"]; !ok {
		t.Error("slotMap missing 'acc_name'")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 3: Compiler error — missing bind map
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_Error_MissingBind(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = createMockDataSourcePool(map[string]bool{"test_db": true})

	step := StepConfig{
		Action: "db_foreach",
		Key:    "test_db",
		Value:  "SELECT id, name FROM accounts",
		Bind:   nil, // Missing bind map
		Do:     []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err := compiler.compileStep(step, nil)
	if err == nil {
		t.Fatal("expected error for missing bind map, got nil")
	}

	if err.Error() != "db_foreach: bind map is required" {
		t.Errorf("expected 'bind map is required' error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 4: Compiler error — unknown data source
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_Error_UnknownDataSource(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = createMockDataSourcePool(map[string]bool{}) // empty pool

	step := StepConfig{
		Action: "db_foreach",
		Key:    "nonexistent_db",
		Value:  "SELECT id FROM accounts",
		Bind:   map[string]string{"id": "row_id"},
		Do:     []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err := compiler.compileStep(step, nil)
	if err == nil {
		t.Fatal("expected error for unknown data source")
	}

	if err.Error() != "db_foreach: unknown data source \"nonexistent_db\"" {
		t.Errorf("expected 'unknown data source' error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 5: Compiler error — nil DataSourcePool
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_Error_NilDataSourcePool(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = nil // Explicitly nil

	step := StepConfig{
		Action: "db_foreach",
		Key:    "test_db",
		Value:  "SELECT id FROM accounts",
		Bind:   map[string]string{"id": "row_id"},
		Do:     []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err := compiler.compileStep(step, nil)
	if err == nil {
		t.Fatal("expected error for nil DataSourcePool")
	}

	if err.Error() != "db_foreach: no data_sources configured" {
		t.Errorf("expected 'no data_sources configured' error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 6: Compiler error — dynamic key rejected
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_Error_DynamicKey(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = createMockDataSourcePool(map[string]bool{"test_db": true})

	step := StepConfig{
		Action: "db_foreach",
		Key:    "{{my_db_slot}}", // Dynamic key
		Value:  "SELECT id FROM accounts",
		Bind:   map[string]string{"id": "row_id"},
		Do:     []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err := compiler.compileStep(step, nil)
	if err == nil {
		t.Fatal("expected error for dynamic key")
	}

	if err.Error() != "db_foreach: dynamic key ({{...}}) not yet supported; use a static key name" {
		t.Errorf("expected 'dynamic key' error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 7: Compiler error — cursor limit exceeded
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_Error_CursorLimit(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = createMockDataSourcePool(map[string]bool{"test_db": true})

	// Set cursor slot to limit (4)
	compiler.nextCursorSlot = 4

	step := StepConfig{
		Action: "db_foreach",
		Key:    "test_db",
		Value:  "SELECT id FROM accounts",
		Bind:   map[string]string{"id": "row_id"},
		Do:     []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err := compiler.compileStep(step, nil)
	if err == nil {
		t.Fatal("expected error for cursor limit exceeded")
	}

	if err.Error() != "db_foreach: cursor slot limit reached (max 4 nested db_foreach per flow)" {
		t.Errorf("expected 'cursor slot limit' error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 8: Compiler allocates slots for bind map entries
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_BindAllocatesSlots(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = createMockDataSourcePool(map[string]bool{"test_db": true})

	// Compile step with 3 bind entries
	step := StepConfig{
		Action: "db_foreach",
		Key:    "test_db",
		Value:  "SELECT id, name, key FROM accounts",
		Bind: map[string]string{
			"id":   "acc_id",
			"name": "acc_name",
			"key":  "api_key",
		},
		Do: []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err := compiler.compileStep(step, nil)
	if err != nil {
		t.Fatalf("compileStep failed: %v", err)
	}

	// Verify all three slots are allocated with distinct indices
	slots := []string{"acc_id", "acc_name", "api_key"}
	slotIndices := make(map[int]bool)

	for _, slotName := range slots {
		idx, ok := compiler.slotMap[slotName]
		if !ok {
			t.Errorf("slotMap missing %q", slotName)
			continue
		}
		if slotIndices[idx] {
			t.Errorf("slot index %d allocated twice", idx)
		}
		slotIndices[idx] = true
	}

	if len(slotIndices) != 3 {
		t.Errorf("expected 3 distinct slot indices, got %d", len(slotIndices))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 9: Compiler with parameterized SQL
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_CompilesParameterizedSQL(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = createMockDataSourcePool(map[string]bool{"test_db": true})

	// Pre-allocate a parameter slot
	paramSlotIdx := compiler.nextSlot
	compiler.slotMap["tenant_id"] = paramSlotIdx
	compiler.nextSlot++

	// Step with parameterized SQL
	step := StepConfig{
		Action: "db_foreach",
		Key:    "test_db",
		Value:  "SELECT id, name FROM accounts WHERE tenant_id = ${tenant_id}",
		Bind: map[string]string{
			"id":   "acc_id",
			"name": "acc_name",
		},
		Do: []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err := compiler.compileStep(step, nil)
	if err != nil {
		t.Fatalf("compileStep with parameterized SQL failed: %v", err)
	}

	// Verify both bind slots and that we got to GATE and REPEAT
	foundGate := false
	foundRepeat := false
	for _, instr := range compiler.GlobalTable {
		if instr.Name == "DB_FOREACH_GATE" {
			foundGate = true
		}
		if instr.Name == "LOOP_REPEAT" {
			foundRepeat = true
		}
	}

	if !foundGate || !foundRepeat {
		t.Error("parameterized SQL compilation missing gate or repeat")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 10: Multiple db_foreach in sequence (not nested, all within limit)
// ─────────────────────────────────────────────────────────────────────────────

func TestDbForeach_MultipleInSequence(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)
	compiler.DataSourcePool = createMockDataSourcePool(map[string]bool{"test_db": true})

	// First db_foreach
	step1 := StepConfig{
		Action: "db_foreach",
		Key:    "test_db",
		Value:  "SELECT id FROM accounts",
		Bind:   map[string]string{"id": "acc_id_1"},
		Do:     []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err := compiler.compileStep(step1, nil)
	if err != nil {
		t.Fatalf("first db_foreach failed: %v", err)
	}

	afterFirst := compiler.nextCursorSlot

	// Second db_foreach (should increment cursor slot again)
	step2 := StepConfig{
		Action: "db_foreach",
		Key:    "test_db",
		Value:  "SELECT name FROM users",
		Bind:   map[string]string{"name": "user_name"},
		Do:     []StepConfig{{Action: "set_response_body", Value: "ok"}},
	}

	err = compiler.compileStep(step2, nil)
	if err != nil {
		t.Fatalf("second db_foreach failed: %v", err)
	}

	afterSecond := compiler.nextCursorSlot

	// Cursor slot should have incremented once per foreach
	if afterSecond != afterFirst+1 {
		t.Errorf("expected cursor slot to increment by 1, got %d -> %d", afterFirst, afterSecond)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper: createMockDataSourcePool creates a DataSourcePool that responds to specific keys.
// Uses unsafe pointers to inject a mock pools map without needing real pgx connections.
// ─────────────────────────────────────────────────────────────────────────────

func createMockDataSourcePool(keys map[string]bool) *datasource.DataSourcePool {
	// Create a real DataSourcePool struct
	pool := &datasource.DataSourcePool{}

	// Create the pools map with dummy pgxpool.Pool values
	poolsMap := make(map[string]*pgxpool.Pool)
	for k := range keys {
		// We use nil as a sentinel; the compiler never actually dereferences these during compilation
		poolsMap[k] = nil
	}

	// Use unsafe to set the private pools field
	// DataSourcePool struct layout: pools map[string]*pgxpool.Pool, configs map[string]DataSourceConfig, loaders sync.Map
	// The first field (pools) is at offset 0
	poolsPtr := (*map[string]*pgxpool.Pool)(unsafe.Pointer(pool))
	*poolsPtr = poolsMap

	return pool
}
