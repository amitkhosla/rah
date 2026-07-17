package steps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/xml"
)

func newXMLTestCtx() (*rctx.Context, *engine.ExecutionState) {
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.IntSlots = make([]int64, 16)
	ctx.BoolSlots = make([]bool, 8)
	ctx.ResponseHeaders = make([]rctx.HeaderMutation, 0, 8)
	state := &engine.ExecutionState{PC: 0}
	return ctx, state
}

// buildScanProg builds an XMLScanProgram for the given element names.
// Each name is mapped to a JSON key of the same name.
func buildScanProg(names ...string) xml.XMLScanProgram {
	var b xml.XMLScanProgramBuilder
	for _, n := range names {
		b.AddOp(n, "", 0, 0)
	}
	return b.Build()
}

// buildBuildProg builds an XMLBuildProgram from "jsonPath:openTag:closeTag" entries.
// Only openTag and closeTag are used by json_to_xml; slotIdx is set to 0.
func buildBuildProg(entries ...string) xml.XMLBuildProgram {
	var b xml.XMLBuildProgramBuilder
	for _, entry := range entries {
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) != 3 {
			continue
		}
		b.AddOp(parts[1], parts[2], 0, 0)
	}
	if len(entries) == 0 {
		b.AddOp("<root>", "</root>", 0, 0)
	}
	return b.Build()
}

// TestXMLToJSON verifies that a simple XML document is converted to a JSON object.
func TestXMLToJSON(t *testing.T) {
	ctx, state := newXMLTestCtx()

	xmlInput := `<root><name>Alice</name><age>30</age></root>`
	ctx.ByteSlots[0] = []byte(xmlInput)

	instr := NewXMLToJSONStep(0, 1, buildScanProg("name", "age"))
	pc := instr.Action(ctx, state)

	if pc != 1 {
		t.Errorf("expected PC 1, got %d", pc)
	}
	if ctx.Failed {
		t.Fatal("xml_to_json: ctx.Failed set unexpectedly")
	}
	if len(ctx.ByteSlots[1]) == 0 {
		t.Fatal("xml_to_json: output slot is empty")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(ctx.ByteSlots[1], &result); err != nil {
		t.Fatalf("xml_to_json: output is not valid JSON: %v (got: %s)", err, ctx.ByteSlots[1])
	}
	if result["name"] != "Alice" {
		t.Errorf("xml_to_json: expected name=Alice, got %v", result["name"])
	}
	if result["age"] != "30" {
		t.Errorf("xml_to_json: expected age=30, got %v", result["age"])
	}
}

// TestJSONToXML verifies that a JSON object is converted to XML bytes.
func TestJSONToXML(t *testing.T) {
	ctx, state := newXMLTestCtx()

	jsonInput := `{"name":"Bob","city":"London"}`
	ctx.ByteSlots[0] = []byte(jsonInput)

	// Build a simple program: name -> <name></name>, city -> <city></city>
	prog := buildBuildProg("name:<name>:</name>", "city:<city>:</city>")
	instr := NewJSONToXMLStep(0, 1, prog)
	pc := instr.Action(ctx, state)

	if pc != 1 {
		t.Errorf("expected PC 1, got %d", pc)
	}
	if ctx.Failed {
		t.Fatal("json_to_xml: ctx.Failed set unexpectedly")
	}
	if len(ctx.ByteSlots[1]) == 0 {
		t.Fatal("json_to_xml: output slot is empty")
	}

	out := string(ctx.ByteSlots[1])
	if !strings.Contains(out, "<name>") {
		t.Errorf("json_to_xml: expected <name> tag in output, got: %s", out)
	}
}

// TestParseXMLValid verifies that valid XML passes parse_xml and the bytes are stored.
func TestParseXMLValid(t *testing.T) {
	ctx, state := newXMLTestCtx()

	xmlInput := `<record><id>42</id></record>`
	ctx.ByteSlots[0] = []byte(xmlInput)

	instr := NewParseXMLStep(0, 1)
	pc := instr.Action(ctx, state)

	if pc != 1 {
		t.Errorf("expected PC 1, got %d", pc)
	}
	if ctx.Failed {
		t.Fatal("parse_xml: ctx.Failed set for valid XML")
	}
	if string(ctx.ByteSlots[1]) != xmlInput {
		t.Errorf("parse_xml: expected src bytes in dst, got: %s", ctx.ByteSlots[1])
	}
}

// TestParseXMLInvalid verifies that malformed XML causes ctx.Failed to be set.
func TestParseXMLInvalid(t *testing.T) {
	ctx, state := newXMLTestCtx()

	// Empty / non-XML input should fail the ValidateAndSkipProlog check.
	ctx.ByteSlots[0] = []byte(`this is not xml at all`)

	instr := NewParseXMLStep(0, 1)
	instr.Action(ctx, state)

	if !ctx.Failed {
		t.Error("parse_xml: expected ctx.Failed for invalid XML")
	}
	if ctx.ByteSlots[1] != nil {
		t.Error("parse_xml: expected nil in dst slot on failure")
	}
}

// TestXMLGet verifies extracting a named element's text content.
func TestXMLGet(t *testing.T) {
	ctx, state := newXMLTestCtx()

	xmlInput := `<order><item>Widget</item><qty>5</qty></order>`
	ctx.ByteSlots[0] = []byte(xmlInput)

	instr := NewXMLGetStep(0, 1, "item")
	pc := instr.Action(ctx, state)

	if pc != 1 {
		t.Errorf("expected PC 1, got %d", pc)
	}
	if ctx.Failed {
		t.Fatal("xml_get: ctx.Failed set unexpectedly")
	}
	if string(ctx.ByteSlots[1]) != "Widget" {
		t.Errorf("xml_get: expected 'Widget', got %q", string(ctx.ByteSlots[1]))
	}
}

// TestXMLGetMissingElement verifies that xml_get sets ctx.Failed when element is absent.
func TestXMLGetMissingElement(t *testing.T) {
	ctx, state := newXMLTestCtx()

	xmlInput := `<order><item>Widget</item></order>`
	ctx.ByteSlots[0] = []byte(xmlInput)

	instr := NewXMLGetStep(0, 1, "missing_element")
	instr.Action(ctx, state)

	if !ctx.Failed {
		t.Error("xml_get: expected ctx.Failed when element is not found")
	}
	if ctx.ByteSlots[1] != nil {
		t.Error("xml_get: expected nil in dst slot when element not found")
	}
}

// TestSetXMLResponse verifies that set_xml_response populates ResponseBuffer
// and adds a Content-Type header.
func TestSetXMLResponse(t *testing.T) {
	ctx, state := newXMLTestCtx()

	xmlBody := `<response><status>ok</status></response>`
	ctx.ByteSlots[0] = []byte(xmlBody)

	instr := NewSetXMLResponseStep(0, 200)
	pc := instr.Action(ctx, state)

	if pc != 1 {
		t.Errorf("expected PC 1, got %d", pc)
	}
	if string(ctx.ResponseBuffer) != xmlBody {
		t.Errorf("set_xml_response: expected ResponseBuffer=%q, got %q", xmlBody, ctx.ResponseBuffer)
	}
	if ctx.ResponseStatus != 200 {
		t.Errorf("set_xml_response: expected ResponseStatus=200, got %d", ctx.ResponseStatus)
	}
	if !ctx.IsBuffered {
		t.Error("set_xml_response: expected IsBuffered=true")
	}

	// Verify Content-Type header mutation was appended
	if ctx.ResHeaderCount != 1 {
		t.Errorf("set_xml_response: expected 1 response header mutation, got %d", ctx.ResHeaderCount)
	}
	if len(ctx.ResponseHeaders) == 0 {
		t.Fatal("set_xml_response: ResponseHeaders slice is empty")
	}
	ct := ctx.ResponseHeaders[0]
	if string(ct.Key) != "Content-Type" {
		t.Errorf("set_xml_response: expected Content-Type header key, got %q", ct.Key)
	}
	if !strings.Contains(string(ct.Value), "application/xml") {
		t.Errorf("set_xml_response: expected application/xml in Content-Type, got %q", ct.Value)
	}
}

// TestSetXMLResponsePreservesExistingStatus verifies that an already-set status is not overwritten.
func TestSetXMLResponsePreservesExistingStatus(t *testing.T) {
	ctx, state := newXMLTestCtx()
	ctx.ResponseStatus = 201

	ctx.ByteSlots[0] = []byte(`<created/>`)

	instr := NewSetXMLResponseStep(0, 200)
	instr.Action(ctx, state)

	if ctx.ResponseStatus != 201 {
		t.Errorf("set_xml_response: should not overwrite existing status 201, got %d", ctx.ResponseStatus)
	}
}
