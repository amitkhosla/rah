package xml

import (
	"testing"
)

func TestAppendXMLTwoLeafOps(t *testing.T) {
	var b XMLBuildProgramBuilder
	// root element with two children
	root := b.AddOp("<user>", "</user>", 0, XMLBuildFlagHasChildren)
	name := b.AddOp("<name>", "</name>", 0, 0)
	age := b.AddOp("<age>", "</age>", 1, 0)
	b.AddChild(root, name)
	b.AddChild(root, age)
	prog := b.Build()

	slots := make([][]byte, 48)
	slots[0] = []byte("Alice")
	slots[1] = []byte("30")

	got := AppendXML(nil, prog, slots)
	want := "<user><name>Alice</name><age>30</age></user>"
	if string(got) != want {
		t.Fatalf("expected %q, got %q", want, string(got))
	}
}

func TestAppendXMLNilSlotSelfClose(t *testing.T) {
	var b XMLBuildProgramBuilder
	b.AddOp("<empty>", "</empty>", 0, 0)
	prog := b.Build()

	slots := make([][]byte, 48)
	slots[0] = nil // empty

	got := AppendXML(nil, prog, slots)
	// Empty leaf: should be rewritten as self-closing
	want := "<empty/>"
	if string(got) != want {
		t.Fatalf("expected %q (self-closing), got %q", want, string(got))
	}
}

func TestAppendXMLNestedOps(t *testing.T) {
	var b XMLBuildProgramBuilder
	root := b.AddOp("<order>", "</order>", 0, XMLBuildFlagHasChildren)
	meta := b.AddOp("<meta>", "</meta>", 0, XMLBuildFlagHasChildren)
	id := b.AddOp("<id>", "</id>", 0, 0)
	status := b.AddOp("<status>", "</status>", 1, 0)
	b.AddChild(root, meta)
	b.AddChild(meta, id)
	b.AddChild(meta, status)
	prog := b.Build()

	slots := make([][]byte, 48)
	slots[0] = []byte("42")
	slots[1] = []byte("active")

	got := AppendXML(nil, prog, slots)
	want := "<order><meta><id>42</id><status>active</status></meta></order>"
	if string(got) != want {
		t.Fatalf("expected %q, got %q", want, string(got))
	}
}

func TestAppendXMLNoAllocsFromSlab(t *testing.T) {
	var b XMLBuildProgramBuilder
	b.AddOp("<x>", "</x>", 0, 0)
	prog := b.Build()

	slots := make([][]byte, 48)
	slots[0] = []byte("v")

	dst := make([]byte, 0, 64)
	allocs := testing.AllocsPerRun(100, func() {
		dst = AppendXML(dst[:0], prog, slots)
	})
	if allocs > 0 {
		t.Fatalf("expected zero allocations, got %.0f", allocs)
	}
}
