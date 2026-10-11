package datacache

import "testing"

func TestArena_FreeListReuse(t *testing.T) {
	fields := []FieldDef{
		{Name: "price", Kind: FieldFloat64, Width: 8},
	}
	cap := 8
	schema := BakeSchema(fields, cap)
	a := &Arena{
		Schema:   schema,
		Cap:      cap,
		buf:      make([]byte, schema.BufSize),
		freeRows: make([]uint32, 0, cap),
	}

	// Append 3 records
	_ = a.Append([]interface{}{10.0})
	_ = a.Append([]interface{}{20.0})
	_ = a.Append([]interface{}{30.0})
	if int(a.Size.Load()) != 3 {
		t.Fatalf("expected Size=3, got %d", a.Size.Load())
	}

	// Delete row 1
	a.Delete(1)
	if a.IsLive(1) {
		t.Error("row 1 should not be live after Delete")
	}
	if a.LiveCount.Load() != 2 {
		t.Errorf("LiveCount after Delete: want 2, got %d", a.LiveCount.Load())
	}
	if len(a.freeRows) != 1 || a.freeRows[0] != 1 {
		t.Errorf("freeRows should contain [1], got %v", a.freeRows)
	}

	// Append new record — must reuse row 1
	_ = a.Append([]interface{}{99.0})
	if int(a.Size.Load()) != 3 {
		t.Errorf("Size should still be 3 after reuse append, got %d", a.Size.Load())
	}
	if a.LiveCount.Load() != 3 {
		t.Errorf("LiveCount after reuse append: want 3, got %d", a.LiveCount.Load())
	}
	if !a.IsLive(1) {
		t.Error("row 1 should be live after reuse")
	}
	if got := a.Float64(0, 1); got != 99.0 {
		t.Errorf("reused row 1 price: want 99.0, got %v", got)
	}
	if len(a.freeRows) != 0 {
		t.Errorf("freeRows should be empty after reuse, got %v", a.freeRows)
	}
}

func TestArena_MultipleReuseRoundTrip(t *testing.T) {
	fields := []FieldDef{{Name: "x", Kind: FieldInt64, Width: 8}}
	cap := 4
	schema := BakeSchema(fields, cap)
	a := &Arena{
		Schema:   schema,
		Cap:      cap,
		buf:      make([]byte, schema.BufSize),
		freeRows: make([]uint32, 0, cap),
	}

	for i := 0; i < cap; i++ {
		_ = a.Append([]interface{}{int64(i + 1)})
	}
	// Arena full
	if err := a.Append([]interface{}{int64(99)}); err != ErrArenaFull {
		t.Errorf("expected ErrArenaFull, got %v", err)
	}

	// Delete all rows
	for i := 0; i < cap; i++ {
		a.Delete(i)
	}
	if a.LiveCount.Load() != 0 {
		t.Errorf("expected LiveCount=0, got %d", a.LiveCount.Load())
	}

	// Re-append cap rows — should all reuse
	for i := 0; i < cap; i++ {
		if err := a.Append([]interface{}{int64(i + 100)}); err != nil {
			t.Fatalf("re-append %d: %v", i, err)
		}
	}
	if int(a.Size.Load()) != cap {
		t.Errorf("Size should remain %d after full reuse, got %d", cap, a.Size.Load())
	}
	if a.LiveCount.Load() != int32(cap) {
		t.Errorf("LiveCount after full reuse: want %d, got %d", cap, a.LiveCount.Load())
	}
}
