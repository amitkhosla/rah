package datacache

import (
	"testing"
)

// TestPool_ClaimRelease tests basic Claim and Release operations.
// NewArenaPool(2, 1024, 10), Claim(0) → idx, Release(idx) → Claim again succeeds
func TestPool_ClaimRelease(t *testing.T) {
	pool := NewArenaPool(2, 1024, 10)

	// First Claim should succeed
	idx1, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("first Claim failed: %v", err)
	}

	// Second Claim should succeed (pool has 2 slots)
	idx2, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("second Claim failed: %v", err)
	}

	if idx1 == idx2 {
		t.Errorf("expected different indices, got %d and %d", idx1, idx2)
	}

	// Release the first slot
	pool.Release(idx1)

	// Claim again should get the released slot back
	idx3, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("claim after release failed: %v", err)
	}

	if idx3 != idx1 {
		t.Errorf("expected to reclaim same index %d, got %d", idx1, idx3)
	}
}

// TestPool_ExhaustedReturnsError tests that Claim returns ErrPoolExhausted
// when all slots are in use.
// NewArenaPool(1, 1024, 10), Claim twice → second returns ErrPoolExhausted
func TestPool_ExhaustedReturnsError(t *testing.T) {
	pool := NewArenaPool(1, 1024, 10)

	// First Claim succeeds
	idx1, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("first Claim failed: %v", err)
	}

	// Second Claim should fail (only 1 slot)
	_, err = pool.Claim(0)
	if err != ErrPoolExhausted {
		t.Errorf("expected ErrPoolExhausted, got %v", err)
	}

	// Release and verify Claim works again
	pool.Release(idx1)
	_, err = pool.Claim(0)
	if err != nil {
		t.Fatalf("claim after release failed: %v", err)
	}
}

// TestPool_InitSlot tests that InitSlot properly sets up an arena.
// Claim, InitSlot → arena.Cap == given capacity
func TestPool_InitSlot(t *testing.T) {
	pool := NewArenaPool(2, 1024, 10)

	idx, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}

	capacity := 5
	schema := BakeSchema([]FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}, capacity)

	pool.InitSlot(idx, capacity, schema)

	arena := &pool.slots[idx].arena
	if arena.Cap != capacity {
		t.Errorf("expected Cap %d, got %d", capacity, arena.Cap)
	}
	if int(arena.Size.Load()) != 0 {
		t.Errorf("expected Size 0, got %d", int(arena.Size.Load()))
	}
	if arena.LiveCount.Load() != 0 {
		t.Errorf("expected LiveCount 0, got %d", arena.LiveCount.Load())
	}

	// Verify indices are initialized to natural order
	indices := pool.slots[idx].indices[:capacity]
	for i := 0; i < capacity; i++ {
		if indices[i] != uint32(i) {
			t.Errorf("index %d: expected %d, got %d", i, i, indices[i])
		}
	}
}

// TestPool_SortBy_Float64 tests sorting a collection by float64 values.
// Claim, InitSlot with float64 schema, Append 3 records with different values, SortBy → check indices order and values
func TestPool_SortBy_Float64(t *testing.T) {
	pool := NewArenaPool(2, 2048, 10)

	idx, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}

	schema := BakeSchema([]FieldDef{
		{Name: "price", Kind: FieldFloat64},
	}, 10)

	pool.InitSlot(idx, 10, schema)
	arena := &pool.slots[idx].arena

	// Append 3 records with values 30.0, 10.0, 20.0
	vals := []float64{30.0, 10.0, 20.0}
	for _, v := range vals {
		err := arena.Append([]interface{}{v})
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// Sort by column 0 (float64)
	pool.SortBy(idx, 0)

	// Verify indices are sorted by values
	indices := pool.slots[idx].indices[:3]
	expectedOrder := []uint32{1, 2, 0} // indices pointing to rows with values 10.0, 20.0, 30.0

	for i, expected := range expectedOrder {
		if indices[i] != expected {
			t.Errorf("sorted index %d: expected %d, got %d", i, expected, indices[i])
		}
	}

	// Verify actual float64 values come out in sorted order
	expectedValues := []float64{10.0, 20.0, 30.0}
	for i, expected := range expectedValues {
		rowIdx := indices[i]
		val := arena.Float64(0, int(rowIdx))
		if val != expected {
			t.Errorf("sorted value position %d: expected %v, got %v", i, expected, val)
		}
	}
}

// TestPool_ResetSortIndex tests that ResetSortIndex restores natural order.
func TestPool_ResetSortIndex(t *testing.T) {
	pool := NewArenaPool(1, 1024, 10)

	idx, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}

	schema := BakeSchema([]FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}, 5)

	pool.InitSlot(idx, 5, schema)
	arena := &pool.slots[idx].arena

	// Append values
	for i := 0; i < 5; i++ {
		arena.Append([]interface{}{float64(4 - i)}) // descending
	}

	// Sort (ascending)
	pool.SortBy(idx, 0)

	// Reset to natural order
	pool.ResetSortIndex(idx)

	// Verify indices are 0, 1, 2, 3, 4
	indices := pool.slots[idx].indices[:5]
	for i := 0; i < 5; i++ {
		if indices[i] != uint32(i) {
			t.Errorf("index %d: expected %d, got %d", i, i, indices[i])
		}
	}
}

// TestPool_SortBy_Int64 tests sorting by int64 column.
func TestPool_SortBy_Int64(t *testing.T) {
	pool := NewArenaPool(1, 2048, 10)

	idx, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}

	schema := BakeSchema([]FieldDef{
		{Name: "count", Kind: FieldInt64},
	}, 10)

	pool.InitSlot(idx, 10, schema)
	arena := &pool.slots[idx].arena

	// Append values
	vals := []int64{500, 100, 300, 200}
	for _, v := range vals {
		arena.Append([]interface{}{v})
	}

	// Sort
	pool.SortBy(idx, 0)

	// Verify sorted order
	indices := pool.slots[idx].indices[:4]
	expectedOrder := []uint32{1, 3, 2, 0} // rows with values 100, 200, 300, 500

	for i, expected := range expectedOrder {
		if indices[i] != expected {
			t.Errorf("sorted index %d: expected %d, got %d", i, expected, indices[i])
		}
	}
}

// TestPool_SortBy_String tests sorting by string column.
func TestPool_SortBy_String(t *testing.T) {
	pool := NewArenaPool(1, 2048, 10)

	idx, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}

	schema := BakeSchema([]FieldDef{
		{Name: "name", Kind: FieldStringN, Width: 16},
	}, 10)

	pool.InitSlot(idx, 10, schema)
	arena := &pool.slots[idx].arena

	// Append values
	vals := []string{"zebra", "apple", "banana"}
	for _, v := range vals {
		arena.Append([]interface{}{v})
	}

	// Sort
	pool.SortBy(idx, 0)

	// Verify sorted order
	indices := pool.slots[idx].indices[:3]
	expectedOrder := []uint32{1, 2, 0} // "apple", "banana", "zebra"

	for i, expected := range expectedOrder {
		if indices[i] != expected {
			t.Errorf("sorted index %d: expected %d, got %d", i, expected, indices[i])
		}
	}
}

// TestPool_Release_ResetsClearsMetadata tests that Release properly clears slot metadata.
func TestPool_Release_ClearsMetadata(t *testing.T) {
	pool := NewArenaPool(1, 1024, 10)

	idx, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}

	slot := &pool.slots[idx]

	// Set some metadata
	slot.ownerKey = [48]byte{1, 2, 3}
	slot.ttlExpUnix.Store(12345)
	slot.name = [128]byte{65, 66, 67} // "ABC"

	// Release
	pool.Release(idx)

	// Verify metadata is cleared
	if slot.ownerKey != [48]byte{} {
		t.Error("expected ownerKey to be cleared")
	}
	if slot.ttlExpUnix.Load() != 0 {
		t.Error("expected ttlExpUnix to be cleared")
	}
	if slot.name != [128]byte{} {
		t.Error("expected name to be cleared")
	}
}

// TestPool_ReclaimArenaReset tests that re-claiming a released slot resets the arena.
func TestPool_ReclaimArenaReset(t *testing.T) {
	pool := NewArenaPool(2, 64<<10, 100)
	fields := []FieldDef{{Name: "v", Kind: FieldFloat64, Width: 8}}
	schema := BakeSchema(fields, 10)

	idx, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	pool.InitSlot(idx, 10, schema)
	_ = pool.slots[idx].arena.Append([]interface{}{42.0})

	pool.Release(idx)

	// Re-claim the same slot
	idx2, err := pool.Claim(0)
	if err != nil {
		t.Fatalf("re-Claim: %v", err)
	}
	// Arena must be reset
	if int(pool.slots[idx2].arena.Size.Load()) != 0 {
		t.Errorf("re-claimed arena Size = %d, want 0", int(pool.slots[idx2].arena.Size.Load()))
	}
	if pool.slots[idx2].arena.LiveCount.Load() != 0 {
		t.Errorf("re-claimed arena LiveCount = %d, want 0", pool.slots[idx2].arena.LiveCount.Load())
	}
}
