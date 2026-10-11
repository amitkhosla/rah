package datacache

import (
	"testing"
)

// TestBakeSchema_Float64Only tests schema baking with 3 float64 fields.
// Verifies that ColRegions offsets are sequential (0, 800, 1600) for capacity 100.
func TestBakeSchema_Float64Only(t *testing.T) {
	fields := []FieldDef{
		{Name: "f1", Kind: FieldFloat64},
		{Name: "f2", Kind: FieldFloat64},
		{Name: "f3", Kind: FieldFloat64},
	}
	capacity := 100

	schema := BakeSchema(fields, capacity)

	// Check that ColRegions exist for all fields
	if len(schema.ColRegions) != len(fields) {
		t.Fatalf("expected %d ColRegions, got %d", len(fields), len(schema.ColRegions))
	}

	// Verify offsets are sequential: 0, 800, 1600
	// Each float64 is 8 bytes; 3 fields × 100 capacity = 800 bytes per field
	expectedOffsets := []int{0, 800, 1600}
	expectedStride := 8

	for i, expected := range expectedOffsets {
		if schema.ColRegions[i].Offset != expected {
			t.Errorf("field %d: expected offset %d, got %d", i, expected, schema.ColRegions[i].Offset)
		}
		if schema.ColRegions[i].Stride != expectedStride {
			t.Errorf("field %d: expected stride %d, got %d", i, expectedStride, schema.ColRegions[i].Stride)
		}
	}

	// BufSize should be 3 × 800 = 2400
	expectedBufSize := 2400
	if schema.BufSize != expectedBufSize {
		t.Errorf("expected BufSize %d, got %d", expectedBufSize, schema.BufSize)
	}

	// Verify offsets are computed correctly: col[i].Offset == sum of (fields[j].Width * capacity) for j < i
	cap := 100
	for i, cr := range schema.ColRegions {
		expectedOffset := 0
		for j := 0; j < i; j++ {
			expectedOffset += schema.Fields[j].Width * cap
		}
		if cr.Offset != expectedOffset {
			t.Errorf("ColRegions[%d].Offset = %d, want %d (computed)", i, cr.Offset, expectedOffset)
		}
	}
}

// TestBakeSchema_MixedTypes tests schema baking with mixed field types.
// Tests float64 + int64 + string:16 + enum fields; verifies offsets and strides.
func TestBakeSchema_MixedTypes(t *testing.T) {
	fields := []FieldDef{
		{Name: "price", Kind: FieldFloat64},     // stride 8
		{Name: "count", Kind: FieldInt64},       // stride 8
		{Name: "label", Kind: FieldStringN, Width: 16}, // stride 16
		{Name: "status", Kind: FieldEnum},       // stride 1
	}
	capacity := 10

	schema := BakeSchema(fields, capacity)

	if len(schema.ColRegions) != 4 {
		t.Fatalf("expected 4 ColRegions, got %d", len(schema.ColRegions))
	}

	// Expected offsets:
	// price: 0, stride 8, occupies 80 bytes (8 * 10)
	// count: 80, stride 8, occupies 80 bytes
	// label: 160, stride 16, occupies 160 bytes (16 * 10)
	// status: 320, stride 1, occupies 10 bytes (1 * 10)
	expectedOffsets := []int{0, 80, 160, 320}
	expectedStrides := []int{8, 8, 16, 1}

	for i := 0; i < 4; i++ {
		if schema.ColRegions[i].Offset != expectedOffsets[i] {
			t.Errorf("field %d: expected offset %d, got %d", i, expectedOffsets[i], schema.ColRegions[i].Offset)
		}
		if schema.ColRegions[i].Stride != expectedStrides[i] {
			t.Errorf("field %d: expected stride %d, got %d", i, expectedStrides[i], schema.ColRegions[i].Stride)
		}
	}

	// BufSize: 80 + 80 + 160 + 10 = 330
	expectedBufSize := 330
	if schema.BufSize != expectedBufSize {
		t.Errorf("expected BufSize %d, got %d", expectedBufSize, schema.BufSize)
	}
}

// TestBakeSchema_BufferSize tests that BufSize is correctly calculated
// for a mixed-field schema.
func TestBakeSchema_BufferSize(t *testing.T) {
	fields := []FieldDef{
		{Name: "price", Kind: FieldFloat64},
		{Name: "count", Kind: FieldInt64},
	}
	capacity := 10

	schema := BakeSchema(fields, capacity)

	// price: 0, stride 8, occupies 80 bytes
	// count: 80, stride 8, occupies 80 bytes
	// BufSize should be 160
	expectedBufSize := 160
	if schema.BufSize != expectedBufSize {
		t.Errorf("expected BufSize %d, got %d", expectedBufSize, schema.BufSize)
	}
}
