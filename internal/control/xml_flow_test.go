package control

import (
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
)

// TestCompileXMLToJSON verifies that the xml_to_json compiler method emits one instruction.
func TestCompileXMLToJSON(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}
	c.slotMap["xml_in"] = 0
	c.slotMap["json_out"] = 1

	step := StepConfig{
		Action: "xml_to_json",
		Input: map[string]string{
			"src_slot": "xml_in",
			"dst_slot": "json_out",
			"fields":   "name,age",
		},
	}

	err := c.compileXMLToJSON(step)
	if err != nil {
		t.Fatalf("compileXMLToJSON failed: %v", err)
	}
	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}
	if c.GlobalTable[0].Name != "XML_TO_JSON" {
		t.Errorf("expected XML_TO_JSON, got %s", c.GlobalTable[0].Name)
	}
}

// TestCompileXMLToJSONMissingSrcSlot verifies that a missing src_slot returns an error.
func TestCompileXMLToJSONMissingSrcSlot(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}

	step := StepConfig{
		Action: "xml_to_json",
		Input: map[string]string{
			// missing src_slot
			"dst_slot": "json_out",
		},
	}

	err := c.compileXMLToJSON(step)
	if err == nil {
		t.Error("expected error for missing src_slot")
	}
}

// TestCompileJSONToXML verifies that the json_to_xml compiler method emits one instruction.
func TestCompileJSONToXML(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}
	c.slotMap["json_in"] = 0
	c.slotMap["xml_out"] = 1

	step := StepConfig{
		Action: "json_to_xml",
		Input: map[string]string{
			"src_slot": "json_in",
			"dst_slot": "xml_out",
		},
	}

	err := c.compileJSONToXML(step)
	if err != nil {
		t.Fatalf("compileJSONToXML failed: %v", err)
	}
	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}
	if c.GlobalTable[0].Name != "JSON_TO_XML" {
		t.Errorf("expected JSON_TO_XML, got %s", c.GlobalTable[0].Name)
	}
}

// TestCompileXMLGet verifies that the xml_get compiler method emits one instruction.
func TestCompileXMLGet(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}
	c.slotMap["xml_in"] = 0
	c.slotMap["value_out"] = 1

	step := StepConfig{
		Action: "xml_get",
		Input: map[string]string{
			"src_slot": "xml_in",
			"path":     "name",
			"dst_slot": "value_out",
		},
	}

	err := c.compileXMLGet(step)
	if err != nil {
		t.Fatalf("compileXMLGet failed: %v", err)
	}
	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}
	if c.GlobalTable[0].Name != "XML_GET" {
		t.Errorf("expected XML_GET, got %s", c.GlobalTable[0].Name)
	}
}

// TestCompileXMLGetRequiresPath verifies that omitting 'path' returns an error.
func TestCompileXMLGetRequiresPath(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}
	c.slotMap["xml_in"] = 0
	c.slotMap["value_out"] = 1

	step := StepConfig{
		Action: "xml_get",
		Input: map[string]string{
			"src_slot": "xml_in",
			// missing path
			"dst_slot": "value_out",
		},
	}

	err := c.compileXMLGet(step)
	if err == nil {
		t.Error("expected error for missing path in xml_get")
	}
}

// TestCompileParseXML verifies that the parse_xml compiler method emits one instruction.
func TestCompileParseXML(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}
	c.slotMap["xml_in"] = 0
	c.slotMap["xml_out"] = 1

	step := StepConfig{
		Action: "parse_xml",
		Input: map[string]string{
			"src_slot": "xml_in",
			"dst_slot": "xml_out",
		},
	}

	err := c.compileParseXML(step)
	if err != nil {
		t.Fatalf("compileParseXML failed: %v", err)
	}
	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}
	if c.GlobalTable[0].Name != "PARSE_XML" {
		t.Errorf("expected PARSE_XML, got %s", c.GlobalTable[0].Name)
	}
}

// TestCompileSetXMLResponse verifies that the set_xml_response compiler method emits one instruction.
func TestCompileSetXMLResponse(t *testing.T) {
	c := &Compiler{
		slotMap:     make(map[string]int),
		GlobalTable: make([]engine.Instruction, 0),
		fm:          nil,
	}
	c.slotMap["xml_body"] = 0

	step := StepConfig{
		Action: "set_xml_response",
		Input: map[string]string{
			"src_slot": "xml_body",
			"status":   "200",
		},
	}

	err := c.compileSetXMLResponse(step)
	if err != nil {
		t.Fatalf("compileSetXMLResponse failed: %v", err)
	}
	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}
	if c.GlobalTable[0].Name != "SET_XML_RESPONSE" {
		t.Errorf("expected SET_XML_RESPONSE, got %s", c.GlobalTable[0].Name)
	}
}

// TestXMLRoundTrip compiles xml_to_json and json_to_xml in sequence and executes them.
func TestXMLRoundTrip(t *testing.T) {
	c := NewCompiler(nil)
	c.slotMap = map[string]int{
		"xml_in":   0,
		"json_mid": 1,
		"xml_out":  2,
	}
	c.nextSlot = 3

	// Step 1: xml_to_json
	err := c.compileXMLToJSON(StepConfig{
		Action: "xml_to_json",
		Input: map[string]string{
			"src_slot": "xml_in",
			"dst_slot": "json_mid",
			"fields":   "name,age",
		},
	})
	if err != nil {
		t.Fatalf("compileXMLToJSON: %v", err)
	}

	// Step 2: json_to_xml
	err = c.compileJSONToXML(StepConfig{
		Action: "json_to_xml",
		Input: map[string]string{
			"src_slot": "json_mid",
			"dst_slot": "xml_out",
			"fields":   "name:<name>:</name>,age:<age>:</age>",
		},
	})
	if err != nil {
		t.Fatalf("compileJSONToXML: %v", err)
	}

	if len(c.GlobalTable) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(c.GlobalTable))
	}
}
