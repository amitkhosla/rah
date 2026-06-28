package control

import (
	"encoding/json"
	"testing"

	"rah/internal/avro"
	"rah/internal/engine"
	"rah/internal/engine/steps"
	"rah/internal/rctx"
)

// TestCompileAvroToJSON tests avro_to_json compiler method
func TestCompileAvroToJSON(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}

	schema := `{
		"type": "record",
		"name": "User",
		"fields": [
			{"name": "name", "type": "string"},
			{"name": "age", "type": "int"}
		]
	}`

	cfg := StepConfig{
		Action: "avro_to_json",
		Input: map[string]string{
			"src_slot": "avro_data",
			"schema":   schema,
			"dst_slot": "json_output",
		},
	}

	// Register slots
	c.slotMap["avro_data"] = 0
	c.slotMap["json_output"] = 1

	err := c.compileAvroToJSON(cfg)
	if err != nil {
		t.Fatalf("compileAvroToJSON failed: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}

	if c.GlobalTable[0].Name != "AVRO_TO_JSON" {
		t.Errorf("Expected instruction name 'AVRO_TO_JSON', got %s", c.GlobalTable[0].Name)
	}
}

// TestCompileJSONToAvro tests json_to_avro compiler method
func TestCompileJSONToAvro(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}

	schema := `{
		"type": "record",
		"name": "User",
		"fields": [
			{"name": "name", "type": "string"}
		]
	}`

	cfg := StepConfig{
		Action: "json_to_avro",
		Input: map[string]string{
			"src_slot": "json_data",
			"schema":   schema,
			"dst_slot": "avro_output",
		},
	}

	c.slotMap["json_data"] = 0
	c.slotMap["avro_output"] = 1

	err := c.compileJSONToAvro(cfg)
	if err != nil {
		t.Fatalf("compileJSONToAvro failed: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}

	if c.GlobalTable[0].Name != "JSON_TO_AVRO" {
		t.Errorf("Expected instruction name 'JSON_TO_AVRO', got %s", c.GlobalTable[0].Name)
	}
}

// TestCompileAvroGetStatic tests avro_get with static path
func TestCompileAvroGetStatic(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}

	schema := `{
		"type": "record",
		"name": "Data",
		"fields": [
			{
				"name": "user",
				"type": {
					"type": "record",
					"name": "User",
					"fields": [{"name": "name", "type": "string"}]
				}
			}
		]
	}`

	cfg := StepConfig{
		Action: "avro_get",
		Input: map[string]string{
			"src_slot": "avro_data",
			"schema":   schema,
			"path":     "user.name",
			"dst_slot": "extracted",
		},
	}

	c.slotMap["avro_data"] = 0
	c.slotMap["extracted"] = 1

	err := c.compileAvroGet(cfg)
	if err != nil {
		t.Fatalf("compileAvroGet static path failed: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}

	if c.GlobalTable[0].Name != "AVRO_GET" {
		t.Errorf("Expected instruction name 'AVRO_GET', got %s", c.GlobalTable[0].Name)
	}
}

// TestCompileAvroGetDynamic tests avro_get with dynamic path_slot
func TestCompileAvroGetDynamic(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}

	schema := `{
		"type": "record",
		"name": "Data",
		"fields": [
			{"name": "a", "type": "string"},
			{"name": "b", "type": "string"}
		]
	}`

	cfg := StepConfig{
		Action: "avro_get",
		Input: map[string]string{
			"src_slot":  "avro_data",
			"schema":    schema,
			"path_slot": "path_var",
			"dst_slot":  "extracted",
		},
	}

	c.slotMap["avro_data"] = 0
	c.slotMap["path_var"] = 1
	c.slotMap["extracted"] = 2

	err := c.compileAvroGet(cfg)
	if err != nil {
		t.Fatalf("compileAvroGet dynamic path failed: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}

	if c.GlobalTable[0].Name != "AVRO_GET" {
		t.Errorf("Expected instruction name 'AVRO_GET', got %s", c.GlobalTable[0].Name)
	}
}

// TestAvroFlowEndToEnd tests a complete flow with avro_to_json and json_to_avro
func TestAvroFlowEndToEnd(t *testing.T) {
	schema := `{
		"type": "record",
		"name": "EndToEnd",
		"fields": [
			{"name": "id", "type": "int"},
			{"name": "name", "type": "string"},
			{"name": "email", "type": "string"}
		]
	}`

	// Compile programs
	slotMap := make(map[string]int)
	decProg, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile decode program failed: %v", err)
	}
	encProg, err := avro.Compile(schema, slotMap)
	if err != nil {
		t.Fatalf("Compile encode program failed: %v", err)
	}

	// Start from JSON and encode to Avro binary first (avoids hand-crafting binary).
	origJSON := []byte(`{"id":1,"name":"alice","email":"alice@example.com"}`)

	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.ByteSlots[0] = origJSON

	// Step 1: JSON → Avro
	encInstr, _ := newJSONToAvroStepHelper(0, 1, encProg)
	state := &engine.ExecutionState{PC: 0}
	encInstr.Action(ctx, state)

	if ctx.Failed {
		t.Fatalf("JSON->Avro encoding failed")
	}

	// Step 2: Avro → JSON (decode what we just encoded)
	decInstr, _ := newAvroToJSONStepHelper(1, 2, decProg)
	decInstr.Action(ctx, state)

	if ctx.Failed {
		t.Fatalf("Avro->JSON decoding failed")
	}

	jsonBytes := ctx.ByteSlots[2]
	var decoded map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("Decoded JSON is invalid: %v", err)
	}

	// Verify fields
	if int(decoded["id"].(float64)) != 1 {
		t.Errorf("Expected id=1, got %v", decoded["id"])
	}

	// Step 3: re-encode JSON back to Avro and decode once more (full round-trip)
	encInstr2, _ := newJSONToAvroStepHelper(2, 3, encProg)
	encInstr2.Action(ctx, state)

	if ctx.Failed {
		t.Fatalf("Round-trip JSON->Avro encoding failed")
	}

	decInstr2, _ := newAvroToJSONStepHelper(3, 4, decProg)
	ctx.ByteSlots = append(ctx.ByteSlots, nil) // ensure slot 4 exists
	decInstr2.Action(ctx, state)

	if ctx.Failed {
		t.Fatalf("Round-trip Avro->JSON decoding failed")
	}

	finalJSON := ctx.ByteSlots[4]
	var final map[string]interface{}
	if err := json.Unmarshal(finalJSON, &final); err != nil {
		t.Fatalf("Final JSON is invalid: %v", err)
	}

	if int(final["id"].(float64)) != 1 {
		t.Errorf("Round-trip: id mismatch, got %v", final["id"])
	}
	if final["name"].(string) != "alice" {
		t.Errorf("Round-trip: name mismatch, got %s", final["name"])
	}
}

// TestCompileErrorHandling tests error cases in compilation
func TestCompileErrorHandling(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}

	// Test invalid schema
	cfg := StepConfig{
		Action: "avro_to_json",
		Input: map[string]string{
			"src_slot": "avro_data",
			"schema":   "not-a-valid-json-schema",
			"dst_slot": "json_output",
		},
	}

	c.slotMap["avro_data"] = 0
	c.slotMap["json_output"] = 1

	err := c.compileAvroToJSON(cfg)
	if err == nil {
		t.Errorf("Expected compilation error for invalid schema")
	}

	// Test missing required fields
	cfg2 := StepConfig{
		Action: "avro_get",
		Input: map[string]string{
			"src_slot": "avro_data",
			// missing "schema"
			"dst_slot": "extracted",
		},
	}

	err = c.compileAvroGet(cfg2)
	if err == nil {
		t.Errorf("Expected error for missing schema")
	}

	// Test avro_get without path or path_slot
	cfg3 := StepConfig{
		Action: "avro_get",
		Input: map[string]string{
			"src_slot": "avro_data",
			"schema":   `{"type":"record","name":"X","fields":[]}`,
			"dst_slot": "extracted",
			// neither path nor path_slot provided
		},
	}

	c.slotMap["avro_data"] = 0
	c.slotMap["extracted"] = 1

	err = c.compileAvroGet(cfg3)
	if err == nil {
		t.Errorf("Expected error for avro_get without path or path_slot")
	}
}

// TestAllStepDescriptorsIncludeAvro checks that Avro steps are in the descriptor list
func TestAllStepDescriptorsIncludeAvro(t *testing.T) {
	descriptors := AllStepDescriptors()

	avroTypes := map[string]bool{
		"avro_to_json":  false,
		"json_to_avro":  false,
		"avro_get":      false,
		"avro_to_xml":   false,
		"xml_to_avro":   false,
		"avro_to_proto": false,
		"proto_to_avro": false,
	}

	for _, desc := range descriptors {
		if _, found := avroTypes[desc.Type]; found {
			avroTypes[desc.Type] = true
		}
	}

	for stepType, found := range avroTypes {
		if !found {
			t.Errorf("Avro step %q not found in AllStepDescriptors()", stepType)
		}
	}
}

// newAvroToJSONStepHelper is a test helper (delegates to steps package)
func newAvroToJSONStepHelper(srcSlot, dstSlot int, decProg *avro.AvroProgram) (engine.Instruction, error) {
	return steps.NewAvroToJSONStep(srcSlot, dstSlot, decProg)
}

// newJSONToAvroStepHelper is a test helper
func newJSONToAvroStepHelper(srcSlot, dstSlot int, encProg *avro.AvroProgram) (engine.Instruction, error) {
	return steps.NewJSONToAvroStep(srcSlot, dstSlot, encProg)
}

// newAvroGetStepHelper is a test helper
func newAvroGetStepHelper(srcSlot, dstSlot int, staticPath string, pathSlot int,
	partialProg *avro.AvroProgram, fullDecProg *avro.AvroProgram) (engine.Instruction, error) {
	return steps.NewAvroGetStep(srcSlot, dstSlot, staticPath, pathSlot, partialProg, fullDecProg)
}
