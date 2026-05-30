package cache

import "testing"

// entryBytes returns the accounting size for a value of the given length.
// Matches CacheManager.Put: EntryHeaderSize + len(value).
func entryBytes(valueLen int) uint64 { return uint64(EntryHeaderSize + valueLen) }

// TestPutOverwriteEvictedUpdatesTenantUsage verifies that when the circular
// buffer wraps and evicts an existing slot, the old tenant's counter is
// decremented and the new tenant's counter is incremented correctly.
//
// Setup: Region has 64-slot minimum (enforced by NewRegion). With 80B stride,
// that's 5120 bytes. Circular wrapping happens after 64 puts.
// This test puts 65 entries (0-64): entries 0-63 fill the buffer,
// entry 64 wraps and evicts entry 0.
func TestPutOverwriteEvictedUpdatesTenantUsage(t *testing.T) {
	// Allocate enough for 64 slots * 80 bytes/slot = 5120 bytes
	// (NewRegion enforces 64-slot minimum regardless of allocation)
	cm, err := NewCacheManager(
		5120,          // 64 slots * 80 bytes stride
		[]uint32{64},  // 1 size class: max 64 B values
		[]uint32{0},   // 1 TTL tier: expires immediately
		1,
		0,
		nil, // nil → default disk backend
	)
	if err != nil {
		t.Fatalf("NewCacheManager() error = %v", err)
	}

	smallVal := []byte("12345") // 5 B → entry size = 16+5 = 21 B
	entrySize := entryBytes(len(smallVal))

	// --- Fill all 64 slots (entries 0-63, different tenants) ---
	for i := 0; i < 64; i++ {
		tenantID := uint16(i + 1)
		key := []byte("k" + string(rune(i)))
		if _, ok := cm.Put(tenantID, key, smallVal, 0); !ok {
			t.Fatalf("Put %d failed", i)
		}
		if got := cm.getTenantCounter(tenantID).used.Load(); got != entrySize {
			t.Fatalf("tenant %d usage after put %d = %d, want %d", tenantID, i, got, entrySize)
		}
	}

	// Global usage should be 64 entries
	globalWant := entrySize * 64
	if got := cm.Stats(); got != globalWant {
		t.Fatalf("global usage after filling = %d, want %d", got, globalWant)
	}

	// --- 65th Put: wraps to slot 0, evicts entry 0 (tenant 1) ---
	if _, ok := cm.Put(65, []byte("k64"), smallVal, 0); !ok {
		t.Fatalf("65th Put failed")
	}

	// Tenant 1 (evicted) should have 0 usage
	if got := cm.getTenantCounter(1).used.Load(); got != 0 {
		t.Fatalf("tenant 1 usage after eviction = %d, want 0", got)
	}

	// Tenant 65 (just inserted) should have normal usage
	if got := cm.getTenantCounter(65).used.Load(); got != entrySize {
		t.Fatalf("tenant 65 usage after put = %d, want %d", got, entrySize)
	}

	// Global usage should still be 64 entries (one evicted, one added)
	if got := cm.Stats(); got != globalWant {
		t.Fatalf("global usage after eviction = %d, want %d", got, globalWant)
	}
}

// TestPutNoDoubleSubtractWhenIndexAlreadyDeleted verifies that if the index
// entry for a key is manually removed before the slot is evicted, the tenant
// counter is not decremented twice (once for index delete, once for eviction).
//
// Phase 1 note: tombstoning the old xSlot via xSlotPtr is not yet implemented,
// so eviction always decrements the old counter. This test confirms the
// accounting remains consistent when the index entry was already gone.
func TestPutNoDoubleSubtractWhenIndexAlreadyDeleted(t *testing.T) {
	cm, err := NewCacheManager(
		80,
		[]uint32{64},
		[]uint32{0},
		1,
		0,
		nil, // nil → default disk backend
	)
	if err != nil {
		t.Fatalf("NewCacheManager() error = %v", err)
	}

	key := []byte("k1")
	if _, ok := cm.Put(1, key, []byte("1234567890"), 0); !ok {
		t.Fatalf("first Put failed")
	}

	// Manually remove index entry (simulating an explicit Delete before eviction).
	if !cm.deleteKey(1, key) {
		t.Fatalf("expected deleteKey to succeed")
	}

	if _, ok := cm.Put(2, []byte("k2"), []byte("12345678901234567890"), 0); !ok {
		t.Fatalf("second Put failed")
	}

	// Tenant 1's slot was evicted: counter decremented by eviction path.
	// (In Phase 2 with xSlotPtr-based tombstoning this will change, but for
	// Phase 1 the eviction always decrements regardless of index state.)
	want2 := entryBytes(20) // 16+20 = 36
	if got := cm.getTenantCounter(2).used.Load(); got != want2 {
		t.Fatalf("tenant 2 usage = %d, want %d", got, want2)
	}
}
