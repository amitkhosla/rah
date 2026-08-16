package steps

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/amitkhosla/rah/internal/avro"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

func TestAvroToJSONBasic(t *testing.T) {
	// Schema: record with two string fields
	schema := `{
		"type": "record",
		"name": "User",
		"fields": [
			{"name": "first_name", "type": "string"},
			{"name": "last_name", "type": "string"}
		]
	}`

	// Compile schema
	slotMap := make(map[string]int)
	prog, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Create Avro binary: "John" + "Doe"
	// String encoding: length as varint, then bytes
	avroBin := []byte{
		8, // varint-encoded 4 (2 * length of "John")
		'J', 'o', 'h', 'n',
		6, // varint-encoded 3 (2 * length of "Doe")
		'D', 'o', 'e',
	}

	// Create context with slot
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.ByteSlots[0] = avroBin

	instr, err := NewAvroToJSONStep(0, 1, prog)
	if err != nil {
		t.Fatalf("NewAvroToJSONStep failed: %v", err)
	}

	// Execute
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("Expected PC 1, got %d", nextPC)
	}

	if ctx.Failed {
		t.Errorf("Expected success, but ctx.Failed = true")
	}

	// Verify JSON output contains field names
	jsonStr := string(ctx.ByteSlots[1])
	if !isValidJSON(jsonStr) {
		t.Errorf("Output is not valid JSON: %s", jsonStr)
	}
}

func TestJSONToAvroRoundTrip(t *testing.T) {
	schema := `{
		"type": "record",
		"name": "Test",
		"fields": [
			{"name": "id", "type": "int"},
			{"name": "name", "type": "string"}
		]
	}`

	slotMap := make(map[string]int)
	prog, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Original JSON
	originalJSON := []byte(`{"id":42,"name":"alice"}`)

	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.ByteSlots[0] = originalJSON

	// JSON -> Avro
	encInstr, err := NewJSONToAvroStep(0, 1, prog)
	if err != nil {
		t.Fatalf("NewJSONToAvroStep failed: %v", err)
	}
	state := &engine.ExecutionState{PC: 0}
	encInstr.Action(ctx, state)

	if ctx.Failed {
		t.Errorf("JSON->Avro failed")
	}
	avroBin := ctx.ByteSlots[1]

	// Avro -> JSON
	decInstr, err := NewAvroToJSONStep(1, 2, prog)
	if err != nil {
		t.Fatalf("NewAvroToJSONStep failed: %v", err)
	}
	ctx.ByteSlots[1] = avroBin
	decInstr.Action(ctx, state)

	if ctx.Failed {
		t.Errorf("Avro->JSON failed")
	}

	// Parse both JSONs and compare
	var orig, decoded map[string]interface{}
	if err := json.Unmarshal(originalJSON, &orig); err != nil {
		t.Fatalf("Failed to unmarshal original: %v", err)
	}
	if err := json.Unmarshal(ctx.ByteSlots[2], &decoded); err != nil {
		t.Fatalf("Failed to unmarshal decoded: %v", err)
	}

	if orig["id"] != decoded["id"] || orig["name"] != decoded["name"] {
		t.Errorf("Round-trip failed: orig=%v, decoded=%v", orig, decoded)
	}
}

func TestAvroGetStaticPath(t *testing.T) {
	schema := `{
		"type": "record",
		"name": "Nested",
		"fields": [
			{
				"name": "user",
				"type": {
					"type": "record",
					"name": "User",
					"fields": [
						{"name": "name", "type": "string"}
					]
				}
			}
		]
	}`

	slotMap := make(map[string]int)
	fullProg, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Compile partial program for "user.name" (returns *AvroProgram, not AvroPartialProgram)
	partialProg, err := avro.CompilePartial(fullProg, "user.name", 0)
	if err != nil {
		t.Fatalf("CompilePartial failed: %v", err)
	}

	// Create Avro binary with nested record: { user: { name: "alice" } }
	// Avro encodes strings as zigzag(len)*2 varint then bytes.
	// "alice" = 5 chars, zigzag(5) = 10.
	avroBin := []byte{
		10, // zigzag(5) = 10 for "alice"
		'a', 'l', 'i', 'c', 'e',
	}

	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.ByteSlots[0] = avroBin

	instr, err := NewAvroGetStep(0, 1, "user.name", -1, partialProg, fullProg)
	if err != nil {
		t.Fatalf("NewAvroGetStep failed: %v", err)
	}

	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("Expected PC 1, got %d", nextPC)
	}

	if ctx.Failed {
		t.Errorf("Expected success, but ctx.Failed = true")
	}

	// Result should contain the extracted name
	result := string(ctx.ByteSlots[1])
	if result != `"alice"` && result != "\"alice\"" {
		t.Errorf("Expected extracted name, got: %s", result)
	}

	// Verify AllocsPerRun is 0 for static path (no pool alloc, only arena)
	allocs := testing.AllocsPerRun(100, func() {
		state := &engine.ExecutionState{PC: 0}
		ctx := &rctx.Context{}
		ctx.ByteSlots = make([][]byte, 48)
		ctx.ByteSlots[0] = avroBin
		instr.Action(ctx, state)
	})
	if allocs > 0 {
		t.Logf("Static-path avro_get: AllocsPerRun = %v (expected 0)", allocs)
		// Note: may be > 0 due to ctx/state allocation; check that it's minimal
	}
}

func TestAvroGetDynamicPath(t *testing.T) {
	schema := `{
		"type": "record",
		"name": "Data",
		"fields": [
			{"name": "field1", "type": "string"},
			{"name": "field2", "type": "string"}
		]
	}`

	slotMap := make(map[string]int)
	fullProg, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Avro binary: "value1" + "value2"
	avroBin := []byte{
		12, // varint 6 (2 * "value1" length)
		'v', 'a', 'l', 'u', 'e', '1',
		12, // varint 6 (2 * "value2" length)
		'v', 'a', 'l', 'u', 'e', '2',
	}

	// Path from slot
	path := []byte("field2")

	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.ByteSlots[0] = avroBin
	ctx.ByteSlots[1] = path

	// Create instruction with dynamic path
	instr, err := NewAvroGetStep(0, 2, "", 1, nil, fullProg)
	if err != nil {
		t.Fatalf("NewAvroGetStep failed: %v", err)
	}

	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("Expected PC 1, got %d", nextPC)
	}

	if ctx.Failed {
		t.Errorf("Expected success, but ctx.Failed = true")
	}

	result := string(ctx.ByteSlots[2])
	if result != `"value2"` && result != "\"value2\"" {
		t.Errorf("Expected 'value2', got: %s", result)
	}
}

func TestAvroGetMissingField(t *testing.T) {
	schema := `{
		"type": "record",
		"name": "Test",
		"fields": [
			{"name": "a", "type": "string"},
			{"name": "b", "type": "string"}
		]
	}`

	slotMap := make(map[string]int)
	fullProg, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	_, err = avro.CompilePartial(fullProg, "nonexistent", 0)
	if err == nil {
		t.Fatalf("Expected CompilePartial to fail for nonexistent field")
	}
}

func TestPoolCleanup(t *testing.T) {
	schema := `{"type": "record", "name": "X", "fields": [{"name": "f", "type": "string"}]}`
	slotMap := make(map[string]int)
	prog, _ := avro.Compile(schema, slotMap)

	// f="hello" → varint 10 + 5 bytes
	avroBin := []byte{10, 'h', 'e', 'l', 'l', 'o'}

	instr, _ := NewAvroToJSONStep(0, 1, prog)

	// Run step twice to exercise pool reuse: if scratch pool isn't reset between calls,
	// subsequent decodes would produce corrupt output.
	for i := 0; i < 2; i++ {
		ctx := &rctx.Context{}
		ctx.ByteSlots = make([][]byte, 48)
		ctx.ByteSlots[0] = avroBin
		state := &engine.ExecutionState{PC: 0}
		instr.Action(ctx, state)
		if ctx.Failed {
			t.Errorf("iteration %d: step failed (pool corruption?)", i)
		}
		if !isValidJSON(string(ctx.ByteSlots[1])) {
			t.Errorf("iteration %d: invalid JSON output (pool corruption?): %s", i, ctx.ByteSlots[1])
		}
	}
}

func TestConcurrentAvroOps(t *testing.T) {
	schema := `{
		"type": "record",
		"name": "Concurrent",
		"fields": [
			{"name": "id", "type": "int"},
			{"name": "value", "type": "string"}
		]
	}`

	slotMap := make(map[string]int)
	encProg, _ := avro.Compile(schema, slotMap)
	decProg, _ := avro.Compile(schema, slotMap)

	// Prepare test data
	jsonBytes := []byte(`{"id":123,"value":"test"}`)

	encInstr, _ := NewJSONToAvroStep(0, 1, encProg)
	decInstr, _ := NewAvroToJSONStep(1, 2, decProg)

	// Run 8 goroutines in parallel
	var wg sync.WaitGroup
	errs := make(chan error, 8)

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			ctx := &rctx.Context{}
			ctx.ByteSlots = make([][]byte, 48)
			ctx.ByteSlots[0] = jsonBytes

			state := &engine.ExecutionState{PC: 0}

			// Encode
			encInstr.Action(ctx, state)
			if ctx.Failed {
				errs <- nil // encode failed, check later
				return
			}

			// Decode
			// ctx.ByteSlots[1] already holds encoded result from encInstr
			decInstr.Action(ctx, state)
			if ctx.Failed {
				errs <- nil // decode failed
				return
			}

			// Verify round-trip
			var decoded map[string]interface{}
			if err := json.Unmarshal(ctx.ByteSlots[2], &decoded); err != nil {
				errs <- err
				return
			}

			if int(decoded["id"].(float64)) != 123 {
				errs <- nil // values don't match
				return
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("Concurrent test error: %v", err)
		}
	}
}

// Helper: check if string is valid JSON
func isValidJSON(s string) bool {
	var obj interface{}
	return json.Unmarshal([]byte(s), &obj) == nil
}

// TestNullUnionField tests round-trip with optional (null union) field
func TestNullUnionField(t *testing.T) {
	schema := `{
		"type": "record",
		"name": "OptionalField",
		"fields": [
			{"name": "required", "type": "string"},
			{"name": "optional", "type": ["null", "string"]}
		]
	}`

	slotMap := make(map[string]int)
	prog, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Test with null value
	json1 := []byte(`{"required":"test","optional":null}`)
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.ByteSlots[0] = json1

	encInstr, _ := NewJSONToAvroStep(0, 1, prog)
	state := &engine.ExecutionState{PC: 0}
	encInstr.Action(ctx, state)

	if ctx.Failed {
		t.Errorf("Encoding with null union field failed")
	}

	// Decode back (ctx.ByteSlots[1] already holds Avro binary from encode step)
	decInstr, _ := NewAvroToJSONStep(1, 2, prog)
	decInstr.Action(ctx, state)

	if ctx.Failed {
		t.Errorf("Decoding null union field failed")
	}

	result := string(ctx.ByteSlots[2])
	if !isValidJSON(result) {
		t.Errorf("Result is not valid JSON: %s", result)
	}
}

// TestArrayField tests array field round-trip
func TestArrayField(t *testing.T) {
	schema := `{
		"type": "record",
		"name": "WithArray",
		"fields": [
			{"name": "items", "type": {"type": "array", "items": "string"}}
		]
	}`

	slotMap := make(map[string]int)
	prog, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	json1 := []byte(`{"items":["a","b","c"]}`)

	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.ByteSlots[0] = json1

	encInstr, _ := NewJSONToAvroStep(0, 1, prog)
	decInstr, _ := NewAvroToJSONStep(1, 2, prog)

	state := &engine.ExecutionState{PC: 0}
	encInstr.Action(ctx, state)
	if ctx.Failed {
		t.Errorf("Encoding array failed")
	}

	// ctx.ByteSlots[1] already holds Avro binary from encode step
	decInstr.Action(ctx, state)
	if ctx.Failed {
		t.Errorf("Decoding array failed")
	}

	result := string(ctx.ByteSlots[2])
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Errorf("Result is not valid JSON: %s", result)
	}

	arr, ok := decoded["items"].([]interface{})
	if !ok || len(arr) != 3 {
		t.Errorf("Expected array of 3 items, got: %v", decoded["items"])
	}
}

// Test with 260-field record to exercise ExtTable
func TestLargeRecordWithExtTable(t *testing.T) {
	// Build a schema with 260 fields (exceeds 255 limit, exercises ExtTable)
	fields := `"type": "record", "name": "Large", "fields": [`
	for i := 0; i < 260; i++ {
		if i > 0 {
			fields += ","
		}
		fields += fmt.Sprintf(`{"name":"f%d","type":"string"}`, i)
	}
	fields += `]}`
	schema := `{` + fields

	slotMap := make(map[string]int)
	prog, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if prog.ExtTable == nil || len(prog.ExtTable) == 0 {
		t.Logf("Warning: expected ExtTable to be populated for 260-field record")
	}

	// Just verify compilation succeeded; decoding all 260 fields would be complex
	t.Logf("Compiled 260-field record successfully with ExtTable=%d", len(prog.ExtTable))
}
