package datacache

import (
	"math"
	"testing"
)

// TestArena_Float64GetSet tests setting and reading float64 values from an arena.
// BakeSchema([float64], 10), Append([42.5]), Float64(0,0) == 42.5
func TestArena_Float64GetSet(t *testing.T) {
	fields := []FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}
	schema := BakeSchema(fields, 10)
	arena := NewArena(schema, 10)

	// Append a single float64 value
	err := arena.Append([]interface{}{42.5})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Verify size and live count
	if int(arena.Size.Load()) != 1 {
		t.Errorf("expected Size 1, got %d", arena.Size.Load())
	}
	if arena.LiveCount.Load() != 1 {
		t.Errorf("expected LiveCount 1, got %d", arena.LiveCount.Load())
	}

	// Read and verify the value
	val := arena.Float64(0, 0)
	if val != 42.5 {
		t.Errorf("expected 42.5, got %v", val)
	}
}

// TestArena_Int64GetSet tests setting and reading int64 values from an arena.
// BakeSchema([int64], 10), Append([int64(999)]), Int64(0,0) == 999
func TestArena_Int64GetSet(t *testing.T) {
	fields := []FieldDef{
		{Name: "count", Kind: FieldInt64},
	}
	schema := BakeSchema(fields, 10)
	arena := NewArena(schema, 10)

	// Append a single int64 value
	err := arena.Append([]interface{}{int64(999)})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Verify size and live count
	if int(arena.Size.Load()) != 1 {
		t.Errorf("expected Size 1, got %d", arena.Size.Load())
	}
	if arena.LiveCount.Load() != 1 {
		t.Errorf("expected LiveCount 1, got %d", arena.LiveCount.Load())
	}

	// Read and verify the value
	val := arena.Int64(0, 0)
	if val != 999 {
		t.Errorf("expected 999, got %v", val)
	}
}

// TestArena_StringAt tests setting and reading fixed-width string values.
// BakeSchema([string:8], 5), Append(["hello"]), StringAt(0,0) == "hello"
func TestArena_StringAt(t *testing.T) {
	fields := []FieldDef{
		{Name: "label", Kind: FieldStringN, Width: 8},
	}
	schema := BakeSchema(fields, 5)
	arena := NewArena(schema, 5)

	// Append a string value
	err := arena.Append([]interface{}{"hello"})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Read the string and verify
	val := arena.StringAt(0, 0)
	strVal := string(val)
	if strVal[:5] != "hello" {
		t.Errorf("expected 'hello', got %q", strVal[:5])
	}

	// Verify it's zero-padded to the full stride
	if len(val) != 8 {
		t.Errorf("expected stride 8, got %d", len(val))
	}
}

// TestArena_Tombstone tests marking records as deleted.
// Append 3 records, Delete(1), verify IsLive(1)==false, LiveCount==2
func TestArena_Tombstone(t *testing.T) {
	fields := []FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}
	schema := BakeSchema(fields, 10)
	arena := NewArena(schema, 10)

	// Append 3 records
	for i := 0; i < 3; i++ {
		err := arena.Append([]interface{}{float64(i)})
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	if int(arena.Size.Load()) != 3 {
		t.Errorf("expected Size 3, got %d", arena.Size.Load())
	}
	if arena.LiveCount.Load() != 3 {
		t.Errorf("expected LiveCount 3, got %d", arena.LiveCount.Load())
	}

	// Delete the second record
	arena.Delete(1)

	// Verify deletion status
	if arena.IsLive(1) {
		t.Error("expected record 1 to be deleted")
	}
	if !arena.IsLive(0) {
		t.Error("expected record 0 to be live")
	}
	if !arena.IsLive(2) {
		t.Error("expected record 2 to be live")
	}

	// LiveCount should be 2
	if arena.LiveCount.Load() != 2 {
		t.Errorf("expected LiveCount 2 after delete, got %d", arena.LiveCount.Load())
	}
}

// TestArena_AppendFull tests that Append returns ErrArenaFull when capacity is exceeded.
func TestArena_AppendFull(t *testing.T) {
	fields := []FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}
	schema := BakeSchema(fields, 2) // capacity 2
	arena := NewArena(schema, 2)

	// Append 2 records (should succeed)
	for i := 0; i < 2; i++ {
		err := arena.Append([]interface{}{float64(i)})
		if err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	// Third append should fail with ErrArenaFull
	err := arena.Append([]interface{}{float64(2)})
	if err != ErrArenaFull {
		t.Errorf("expected ErrArenaFull, got %v", err)
	}

	if int(arena.Size.Load()) != 2 {
		t.Errorf("expected Size 2, got %d", arena.Size.Load())
	}
}

// TestArena_Reset tests that Reset clears record count and live count.
// Append 5, Reset(), Size==0, LiveCount==0
func TestArena_Reset(t *testing.T) {
	fields := []FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}
	schema := BakeSchema(fields, 10)
	arena := NewArena(schema, 10)

	// Append 5 records
	for i := 0; i < 5; i++ {
		err := arena.Append([]interface{}{float64(i)})
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	if int(arena.Size.Load()) != 5 {
		t.Errorf("expected Size 5, got %d", arena.Size.Load())
	}
	if arena.LiveCount.Load() != 5 {
		t.Errorf("expected LiveCount 5, got %d", arena.LiveCount.Load())
	}

	// Reset the arena
	arena.Reset()

	// Verify both Size and LiveCount are 0
	if int(arena.Size.Load()) != 0 {
		t.Errorf("expected Size 0 after Reset, got %d", arena.Size.Load())
	}
	if arena.LiveCount.Load() != 0 {
		t.Errorf("expected LiveCount 0 after Reset, got %d", arena.LiveCount.Load())
	}
}

// TestArena_Enum tests enum field storage and retrieval.
func TestArena_Enum(t *testing.T) {
	fields := []FieldDef{
		{Name: "status", Kind: FieldEnum},
	}
	schema := BakeSchema(fields, 5)
	arena := NewArena(schema, 5)

	// Append enum values
	testValues := []uint8{0, 1, 5, 255, 128}
	for _, v := range testValues {
		err := arena.Append([]interface{}{v})
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// Verify values
	for i, expectedVal := range testValues {
		val := arena.Enum(0, i)
		if val != expectedVal {
			t.Errorf("row %d: expected %d, got %d", i, expectedVal, val)
		}
	}
}

// TestArena_MultiField tests appending and reading multiple fields per record.
func TestArena_MultiField(t *testing.T) {
	fields := []FieldDef{
		{Name: "price", Kind: FieldFloat64},
		{Name: "count", Kind: FieldInt64},
		{Name: "status", Kind: FieldEnum},
	}
	schema := BakeSchema(fields, 5)
	arena := NewArena(schema, 5)

	// Append a multi-field record
	err := arena.Append([]interface{}{
		99.99,
		int64(42),
		uint8(1),
	})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Verify all fields
	if arena.Float64(0, 0) != 99.99 {
		t.Error("float64 field mismatch")
	}
	if arena.Int64(1, 0) != 42 {
		t.Error("int64 field mismatch")
	}
	if arena.Enum(2, 0) != 1 {
		t.Error("enum field mismatch")
	}
}

// TestArena_BadType tests that Append rejects wrong types.
func TestArena_BadType(t *testing.T) {
	fields := []FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}
	schema := BakeSchema(fields, 5)
	arena := NewArena(schema, 5)

	// Try to append int64 to float64 field
	err := arena.Append([]interface{}{int64(42)})
	if err != ErrBadType {
		t.Errorf("expected ErrBadType, got %v", err)
	}
}

// TestArena_NaNAndInf tests that special float64 values (NaN, Inf) are stored correctly.
func TestArena_NaNAndInf(t *testing.T) {
	fields := []FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}
	schema := BakeSchema(fields, 10)
	arena := NewArena(schema, 10)

	testValues := []float64{
		math.NaN(),
		math.Inf(1),
		math.Inf(-1),
		0.0,
		0.0,
	}

	for i, val := range testValues {
		err := arena.Append([]interface{}{val})
		if err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	// Verify values (NaN requires special comparison)
	for i, val := range testValues {
		readVal := arena.Float64(0, i)
		if math.IsNaN(val) {
			if !math.IsNaN(readVal) {
				t.Errorf("row %d: expected NaN, got %v", i, readVal)
			}
		} else if readVal != val {
			t.Errorf("row %d: expected %v, got %v", i, val, readVal)
		}
	}
}

// TestArena_StringPadding tests zero-padding of strings shorter than stride.
func TestArena_StringPadding(t *testing.T) {
	fields := []FieldDef{
		{Name: "label", Kind: FieldStringN, Width: 16},
	}
	schema := BakeSchema(fields, 5)
	arena := NewArena(schema, 5)

	// Append a short string
	err := arena.Append([]interface{}{"hi"})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Read the full field
	val := arena.StringAt(0, 0)
	if val[0] != 'h' || val[1] != 'i' {
		t.Errorf("expected 'hi' at start, got %q", val[:2])
	}

	// Verify zero-padding
	for i := 2; i < 16; i++ {
		if val[i] != 0 {
			t.Errorf("expected zero padding at index %d, got %d", i, val[i])
		}
	}
}

// TestArena_BoundaryAccess tests reading at first and last index in full arena.
func TestArena_BoundaryAccess(t *testing.T) {
	fields := []FieldDef{
		{Name: "price", Kind: FieldFloat64, Width: 8},
		{Name: "vol", Kind: FieldInt64, Width: 8},
	}
	cap := 4
	schema := BakeSchema(fields, cap)
	a := &Arena{Schema: schema, Cap: cap, buf: make([]byte, schema.BufSize)}
	a.freeRows = make([]uint32, 0, 8)

	// Append to fill capacity
	for i := 0; i < cap; i++ {
		err := a.Append([]interface{}{float64(i) * 10.0, int64(i) * 100})
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// Read at first and last index
	if got := a.Float64(0, 0); got != 0.0 {
		t.Errorf("Float64(col=0, row=0) = %v, want 0.0", got)
	}
	if got := a.Float64(0, cap-1); got != float64(cap-1)*10.0 {
		t.Errorf("Float64(col=0, row=%d) = %v, want %v", cap-1, got, float64(cap-1)*10.0)
	}
	if got := a.Int64(1, 0); got != 0 {
		t.Errorf("Int64(col=1, row=0) = %v, want 0", got)
	}
	if got := a.Int64(1, cap-1); got != int64(cap-1)*100 {
		t.Errorf("Int64(col=1, row=%d) = %v, want %v", cap-1, got, int64(cap-1)*100)
	}
}

// TestArena_DoubleDelete tests that deleting same row twice doesn't decrement LiveCount below the proper value.
func TestArena_DoubleDelete(t *testing.T) {
	fields := []FieldDef{{Name: "x", Kind: FieldFloat64, Width: 8}}
	schema := BakeSchema(fields, 4)
	a := &Arena{Schema: schema, Cap: 4, buf: make([]byte, schema.BufSize)}
	a.freeRows = make([]uint32, 0, 4)

	if err := a.Append([]interface{}{1.0}); err != nil {
		t.Fatal(err)
	}
	if err := a.Append([]interface{}{2.0}); err != nil {
		t.Fatal(err)
	}
	// LiveCount should be 2
	if a.LiveCount.Load() != 2 {
		t.Fatalf("expected LiveCount=2, got %d", a.LiveCount.Load())
	}
	a.Delete(0)
	if a.LiveCount.Load() != 1 {
		t.Fatalf("after first Delete, expected LiveCount=1, got %d", a.LiveCount.Load())
	}
	// Delete same row again — LiveCount must not go below 1
	a.Delete(0)
	if a.LiveCount.Load() != 1 {
		t.Errorf("after double Delete, LiveCount should still be 1, got %d", a.LiveCount.Load())
	}
}
