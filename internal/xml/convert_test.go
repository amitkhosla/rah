package xml

import (
	"strings"
	"sync"
	"testing"
)

func TestAppendXMLToJSONBasic(t *testing.T) {
	src := []byte(`<user><name>Alice</name><age>30</age></user>`)
	var b XMLScanProgramBuilder
	b.AddOp("name", `"name":`, 0, 0)
	b.AddOp("age", `"age":`, 0, 0)
	prog := b.Build()

	got, err := AppendXMLToJSON(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"name":"Alice","age":"30"}`
	if string(got) != want {
		t.Fatalf("expected %q, got %q", want, string(got))
	}
}

func TestAppendXMLToJSONMissingRequiredEmitsNull(t *testing.T) {
	src := []byte(`<user><name>Alice</name></user>`)
	var b XMLScanProgramBuilder
	b.AddOp("name", `"name":`, 0, 0)
	b.AddOp("missing", `"missing":`, 0, 0) // required, not optional
	prog := b.Build()

	got, err := AppendXMLToJSON(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(got), `"missing":null`) {
		t.Fatalf("expected null for missing required field, got %q", string(got))
	}
}

func TestAppendXMLToJSONOptionalMissingOmitted(t *testing.T) {
	src := []byte(`<user><name>Alice</name></user>`)
	var b XMLScanProgramBuilder
	b.AddOp("name", `"name":`, 0, 0)
	b.AddOp("extra", `"extra":`, 0, XMLFlagIsOptional) // optional — omit if missing
	prog := b.Build()

	got, err := AppendXMLToJSON(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(got), "extra") {
		t.Fatalf("optional missing field should be omitted, got %q", string(got))
	}
}

func TestAppendXMLToJSONArrayFlag(t *testing.T) {
	src := []byte(`<list><item>a</item><item>b</item><item>c</item></list>`)
	var b XMLScanProgramBuilder
	b.AddOp("item", `"items":`, 0, XMLFlagIsArray)
	prog := b.Build()

	got, err := AppendXMLToJSON(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"items":["a","b","c"]}`
	if string(got) != want {
		t.Fatalf("expected %q, got %q", want, string(got))
	}
}

func TestAppendXMLToJSONAttributeExtraction(t *testing.T) {
	src := []byte(`<root><order type="express">123</order></root>`)
	var b XMLScanProgramBuilder
	// Attribute flag: find <order> tag and extract attr "order"
	// The name here is the attribute name to extract
	b.AddOp("order", `"type":`, 0, XMLFlagIsAttr)
	prog := b.Build()

	got, err := AppendXMLToJSON(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(got), `"type":"express"`) {
		t.Fatalf("expected type:express attribute in JSON, got %q", string(got))
	}
}

func TestAppendXMLToJSONDTDReturnsError(t *testing.T) {
	src := []byte(`<!DOCTYPE foo SYSTEM "foo.dtd"><root/>`)
	var b XMLScanProgramBuilder
	prog := b.Build()

	dst := []byte("unchanged")
	got, err := AppendXMLToJSON(dst, src, prog)
	if err != ErrDTDRejected {
		t.Fatalf("expected ErrDTDRejected, got %v", err)
	}
	// dst should be returned as-is when error occurs before any writes
	if string(got) != "unchanged" {
		t.Fatalf("expected dst unchanged on error, got %q", string(got))
	}
}

func TestAppendJSONToXMLBasic(t *testing.T) {
	src := []byte(`{"name":"Alice"}`)
	var b XMLBuildProgramBuilder
	root := b.AddOp("<user>", "</user>", 0, XMLBuildFlagHasChildren)
	nameOp := b.AddOp("<name>", "</name>", 0, 0)
	b.AddChild(root, nameOp)
	prog := b.Build()

	got, err := AppendJSONToXML(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "<user><name>Alice</name></user>"
	if string(got) != want {
		t.Fatalf("expected %q, got %q", want, string(got))
	}
}

func TestAppendJSONToXMLMissingFieldOmitted(t *testing.T) {
	src := []byte(`{"name":"Alice"}`)
	var b XMLBuildProgramBuilder
	root := b.AddOp("<user>", "</user>", 0, XMLBuildFlagHasChildren)
	nameOp := b.AddOp("<name>", "</name>", 0, 0)
	emailOp := b.AddOp("<email>", "</email>", 1, 0)
	b.AddChild(root, nameOp)
	b.AddChild(root, emailOp)
	prog := b.Build()

	slots := make([][]byte, 48) // not used in JSON→XML path
	_ = slots

	got, err := AppendJSONToXML(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// <email> should be omitted since "email" not in JSON
	if strings.Contains(string(got), "<email>") {
		t.Fatalf("expected <email> to be omitted, got %q", string(got))
	}
	if !strings.Contains(string(got), "<name>Alice</name>") {
		t.Fatalf("expected <name>Alice</name>, got %q", string(got))
	}
}

func TestAppendJSONToXMLNested(t *testing.T) {
	src := []byte(`{"id":"42","status":"active"}`)
	var b XMLBuildProgramBuilder
	root := b.AddOp("<order>", "</order>", 0, XMLBuildFlagHasChildren)
	idOp := b.AddOp("<id>", "</id>", 0, 0)
	statusOp := b.AddOp("<status>", "</status>", 1, 0)
	b.AddChild(root, idOp)
	b.AddChild(root, statusOp)
	prog := b.Build()

	got, err := AppendJSONToXML(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "<order><id>42</id><status>active</status></order>"
	if string(got) != want {
		t.Fatalf("expected %q, got %q", want, string(got))
	}
}

func TestRoundTrip(t *testing.T) {
	// XML → JSON → XML should produce structurally equivalent output
	xmlSrc := []byte(`<person><name>Bob</name><city>London</city></person>`)

	var sb XMLScanProgramBuilder
	sb.AddOp("name", `"name":`, 0, 0)
	sb.AddOp("city", `"city":`, 0, 0)
	scanProg := sb.Build()

	jsonOut, err := AppendXMLToJSON(nil, xmlSrc, scanProg)
	if err != nil {
		t.Fatalf("AppendXMLToJSON error: %v", err)
	}

	var bb XMLBuildProgramBuilder
	root := bb.AddOp("<person>", "</person>", 0, XMLBuildFlagHasChildren)
	nameOp := bb.AddOp("<name>", "</name>", 0, 0)
	cityOp := bb.AddOp("<city>", "</city>", 1, 0)
	bb.AddChild(root, nameOp)
	bb.AddChild(root, cityOp)
	buildProg := bb.Build()

	xmlOut, err := AppendJSONToXML(nil, jsonOut, buildProg)
	if err != nil {
		t.Fatalf("AppendJSONToXML error: %v", err)
	}

	// Check structural equivalence
	if !strings.Contains(string(xmlOut), "<name>Bob</name>") {
		t.Fatalf("round-trip: expected <name>Bob</name>, got %q", string(xmlOut))
	}
	if !strings.Contains(string(xmlOut), "<city>London</city>") {
		t.Fatalf("round-trip: expected <city>London</city>, got %q", string(xmlOut))
	}
}

func TestAppendXMLToJSONStringEscaping(t *testing.T) {
	// XML content with characters that need JSON escaping
	src := []byte("<root><name>O'Brien &amp; \"Co\"</name></root>")
	var b XMLScanProgramBuilder
	b.AddOp("name", `"name":`, 0, 0)
	prog := b.Build()

	got, err := AppendXMLToJSON(nil, src, prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The double-quote chars should be escaped in JSON output
	if !strings.Contains(string(got), `\"Co\"`) {
		t.Fatalf("expected escaped quotes in JSON output, got %q", string(got))
	}
}

func TestAppendXMLToJSONConcurrent(t *testing.T) {
	src := []byte(`<user><name>Alice</name><age>30</age></user>`)
	var b XMLScanProgramBuilder
	b.AddOp("name", `"name":`, 0, 0)
	b.AddOp("age", `"age":`, 0, 0)
	prog := b.Build()

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				got, err := AppendXMLToJSON(nil, src, prog)
				if err != nil {
					panic(err)
				}
				want := `{"name":"Alice","age":"30"}`
				if string(got) != want {
					panic("wrong output: " + string(got))
				}
			}
		}()
	}
	wg.Wait()
}
