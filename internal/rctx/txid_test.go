package rctx

import (
	"testing"
)

func TestFormatTxIDInto_MatchesFormatTxID(t *testing.T) {
	gen := NewTxIDGenerator()
	id := gen.Generate(1000000000) // arbitrary start time

	// Format using the allocating variant
	formatted := FormatTxID(id)

	// Format using the zero-alloc variant
	dst := make([]byte, 32)
	FormatTxIDInto(dst, id)
	bufFormatted := string(dst)

	if formatted != bufFormatted {
		t.Errorf("FormatTxID(%v) = %q, but FormatTxIDInto = %q", id, formatted, bufFormatted)
	}
}

func TestFormatTxIDInto_ZeroID(t *testing.T) {
	id := [2]uint64{0, 0}
	dst := make([]byte, 32)
	FormatTxIDInto(dst, id)

	expected := "00000000000000000000000000000000"
	if string(dst) != expected {
		t.Errorf("FormatTxIDInto for zero ID = %q, want %q", string(dst), expected)
	}
}

func TestFormatTxIDInto_AllFFs(t *testing.T) {
	id := [2]uint64{^uint64(0), ^uint64(0)}
	dst := make([]byte, 32)
	FormatTxIDInto(dst, id)

	expected := "ffffffffffffffffffffffffffffffff"
	if string(dst) != expected {
		t.Errorf("FormatTxIDInto for all-FF ID = %q, want %q", string(dst), expected)
	}
}

func TestFormatTxIDInto_PartialPattern(t *testing.T) {
	// Test with a specific pattern to verify byte ordering
	id := [2]uint64{0x0123456789abcdef, 0xfedcba9876543210}
	dst := make([]byte, 32)
	FormatTxIDInto(dst, id)

	// id[0] = 0x0123456789abcdef, id[1] = 0xfedcba9876543210
	// FormatTxID uses fmt.Sprintf("%016x%016x", id[0], id[1])
	expected := FormatTxID(id)
	if string(dst) != expected {
		t.Errorf("FormatTxIDInto pattern test: got %q, want %q", string(dst), expected)
	}
}

func BenchmarkFormatTxIDInto(b *testing.B) {
	id := [2]uint64{0x0123456789abcdef, 0xfedcba9876543210}
	dst := make([]byte, 32)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		FormatTxIDInto(dst, id)
	}
}

func BenchmarkFormatTxID(b *testing.B) {
	id := [2]uint64{0x0123456789abcdef, 0xfedcba9876543210}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FormatTxID(id)
	}
}
