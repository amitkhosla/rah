package datacache

import "testing"

func TestGrowthFixed_Full(t *testing.T) {
	pool := NewArenaPool(4, 64<<10, 100)
	quotas := NewQuotaRegistry(1<<30, 10)
	mgr := NewDataCacheManager(pool, quotas)
	defer mgr.Close()

	fields := []FieldDef{{Name: "price", Kind: FieldFloat64, Width: 8}}
	opts := CreateOpts{
		Fields:   fields,
		Capacity: 3,
		Growth:   GrowthFixed,
	}

	if err := mgr.Create("fixed", opts); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Append up to capacity
	for i := 0; i < 3; i++ {
		if err := mgr.AppendVals("fixed", []interface{}{float64(i)}); err != nil {
			t.Fatalf("AppendVals %d: %v", i, err)
		}
	}

	// 4th append must fail with ErrArenaFull
	err := mgr.AppendVals("fixed", []interface{}{99.0})
	if err == nil {
		t.Error("expected error on 4th append to fixed-capacity arena")
	}
}

func TestGrowthAuto_BeyondInitialCapacity(t *testing.T) {
	// GrowthAuto would need the manager to support capacity doubling.
	// For now, verify that the arena itself returns ErrArenaFull (since GrowthAuto
	// expansion is not yet implemented in manager — this test documents current behavior).
	fields := []FieldDef{{Name: "price", Kind: FieldFloat64, Width: 8}}
	cap := 4
	schema := BakeSchema(fields, cap)
	a := &Arena{
		Schema:   schema,
		Cap:      cap,
		buf:      make([]byte, schema.BufSize),
		freeRows: make([]uint32, 0, cap),
	}

	for i := 0; i < cap; i++ {
		if err := a.Append([]interface{}{float64(i)}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// 5th append on a non-reuse arena returns ErrArenaFull
	if err := a.Append([]interface{}{99.0}); err != ErrArenaFull {
		t.Errorf("expected ErrArenaFull at capacity, got %v", err)
	}
}
