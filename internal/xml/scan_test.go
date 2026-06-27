package xml

import (
	"sync"
	"testing"
)

func TestFindElementCaseInsensitive(t *testing.T) {
	src := []byte(`<root><Order><id>1</id></Order></root>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	// Must find <Order> when searching for "order" (case-insensitive)
	if !s.FindElement([]byte("order")) {
		t.Fatal("expected to find <Order> case-insensitively")
	}
}

func TestFindElementNoPartialMatch(t *testing.T) {
	src := []byte(`<root><ordered>no</ordered></root>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	// Must NOT match <ordered> when searching for "order"
	if s.FindElement([]byte("order")) {
		t.Fatal("FindElement must not match partial tag name <ordered> for search 'order'")
	}
}

func TestAppendContentExtractsText(t *testing.T) {
	src := []byte(`<root><name>  Alice  </name></root>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	if !s.FindElement([]byte("name")) {
		t.Fatal("expected to find <name>")
	}
	content, ok := s.AppendContent(nil)
	if !ok {
		t.Fatal("AppendContent returned false")
	}
	if string(content) != "Alice" {
		t.Fatalf("expected 'Alice' (trimmed), got %q", string(content))
	}
}

func TestAppendContentNestedSameName(t *testing.T) {
	// Outer <items> should return the inner <items>inner</items> as content
	src := []byte(`<root><items><items>inner</items></items></root>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	if !s.FindElement([]byte("items")) {
		t.Fatal("expected to find outer <items>")
	}
	content, ok := s.AppendContent(nil)
	if !ok {
		t.Fatal("AppendContent returned false")
	}
	// The outer element contains the inner element tag
	if string(content) != "<items>inner</items>" {
		t.Fatalf("expected inner element as content, got %q", string(content))
	}
}

func TestFindElementN(t *testing.T) {
	src := []byte(`<root><item>a</item><item>b</item><item>c</item></root>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	// Find the 3rd occurrence (index 2)
	if !s.FindElementN([]byte("item"), 2) {
		t.Fatal("expected to find 3rd <item>")
	}
	content, ok := s.AppendContent(nil)
	if !ok {
		t.Fatal("AppendContent returned false")
	}
	if string(content) != "c" {
		t.Fatalf("expected 'c', got %q", string(content))
	}
}

func TestLookupExtReturns300(t *testing.T) {
	var b XMLScanProgramBuilder
	// Add an op with index 300 — must go through ExtTable
	idx := b.AddOp("item", `"item":`, 300, 0)
	prog := b.Build()

	if prog.Ops[idx].Index != 255 {
		t.Fatalf("expected sentinel 255 for overflow index, got %d", prog.Ops[idx].Index)
	}
	if len(prog.ExtTable) == 0 {
		t.Fatal("expected ExtTable to be populated")
	}
	ext := prog.lookupExt(idx)
	if ext.Index != 300 {
		t.Fatalf("expected lookupExt to return 300, got %d", ext.Index)
	}
}

func TestAppendAttr(t *testing.T) {
	src := []byte(`<root><order type="express" id="42">content</order></root>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	if !s.FindElement([]byte("order")) {
		t.Fatal("expected to find <order>")
	}
	got, ok := s.AppendAttr(nil, []byte("type"))
	if !ok {
		t.Fatal("AppendAttr returned false for 'type'")
	}
	if string(got) != "express" {
		t.Fatalf("expected 'express', got %q", string(got))
	}
}

func TestAppendAttrNamespacedAttribute(t *testing.T) {
	src := []byte(`<root><order ns:type="x">content</order></root>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	if !s.FindElement([]byte("order")) {
		t.Fatal("expected to find <order>")
	}
	got, ok := s.AppendAttr(nil, []byte("type"))
	if !ok {
		t.Fatal("AppendAttr returned false for namespaced attr 'type'")
	}
	if string(got) != "x" {
		t.Fatalf("expected 'x', got %q", string(got))
	}
}

func TestFindElementMissingReturnsFalse(t *testing.T) {
	src := []byte(`<root><name>Alice</name></root>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	dst := []byte("unchanged")
	if s.FindElement([]byte("missing")) {
		t.Fatal("expected FindElement to return false for missing element")
	}
	// dst should be unchanged since FindElement doesn't touch it
	if string(dst) != "unchanged" {
		t.Fatalf("expected dst unchanged, got %q", string(dst))
	}
}

func TestDTDRejected(t *testing.T) {
	src := []byte(`<?xml version="1.0"?><!DOCTYPE foo SYSTEM "foo.dtd"><root/>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	err := s.ValidateAndSkipProlog()
	if err != ErrDTDRejected {
		t.Fatalf("expected ErrDTDRejected, got %v", err)
	}
}

func TestExternalEntityRejected(t *testing.T) {
	src := []byte(`<?xml version="1.0"?><!ENTITY xxe SYSTEM "file:///etc/passwd"><root/>`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	err := s.ValidateAndSkipProlog()
	if err != ErrDTDRejected {
		t.Fatalf("expected ErrDTDRejected for ENTITY, got %v", err)
	}
}

func TestMalformedXMLAppendContentReturnsFalse(t *testing.T) {
	// Open tag with no matching close tag
	src := []byte(`<root><name>Alice`)
	s := GetXMLScanner(src)
	defer PutXMLScanner(s)
	_ = s.ValidateAndSkipProlog()

	if !s.FindElement([]byte("name")) {
		t.Fatal("expected to find <name>")
	}
	_, ok := s.AppendContent(nil)
	if ok {
		t.Fatal("expected AppendContent to return false for malformed XML")
	}
}

func TestPutXMLScannerClearsSrc(t *testing.T) {
	src := []byte(`<root/>`)
	s := GetXMLScanner(src)
	PutXMLScanner(s)
	// After Put, src must be nil (same package — can access unexported field)
	if s.src != nil {
		t.Fatal("PutXMLScanner must set s.src = nil to prevent GC retention")
	}
}

func TestConcurrentScannerPoolRaceClean(t *testing.T) {
	src := []byte(`<root><item>val</item><item>val2</item></root>`)
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s := GetXMLScanner(src)
				_ = s.ValidateAndSkipProlog()
				s.FindElement([]byte("item"))
				s.AppendContent(nil)
				PutXMLScanner(s)
			}
		}()
	}
	wg.Wait()
}
