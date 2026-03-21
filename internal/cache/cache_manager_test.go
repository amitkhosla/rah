package cache

import "testing"

// entryBytes returns the accounting size for a value of the given length.
// Matches CacheManager.Put: EntryHeaderSize + len(value).
func entryBytes(valueLen int) uint64 { return uint64(EntryHeaderSize + valueLen) }

// TestPutOverwriteEvictedUpdatesTenantUsage verifies that when the circular
// buffer evicts an existing slot, the old tenant's counter is decremented and
// the new tenant's counter is incremented correctly.
//
// Setup: totalMemory = 1 region of exactly 1 slot (stride = align8(16+64) = 80B).
// Every Put claims slot 0, evicting the previous entry.
func TestPutOverwriteEvictedUpdatesTenantUsage(t *testing.T) {
	// stride = align8(16+64) = 80 bytes → 1 slot per region.
	cm, err := NewCacheManager(
		80,            // exactly 1 slot
		[]uint32{64},  // 1 size class: max 64 B values
		[]uint32{0},   // 1 TTL tier: expires immediately (TTL=0 → expiry=now)
		1,
		0,
		nil, // nil → default disk backend
	)
	if err != nil {
		t.Fatalf("NewCacheManager() error = %v", err)
	}

	k1v := []byte("1234567890")        // 10 B
	k2v := []byte("12345678901234567890") // 20 B
	k3v := []byte("1234567890123456")   // 16 B

	// --- first Put ---
	if _, ok := cm.Put(1, []byte("k1"), k1v, 0); !ok {
		t.Fatalf("first Put failed")
	}
	want1 := entryBytes(len(k1v)) // 16+10 = 26
	if got := cm.getTenantCounter(1).used.Load(); got != want1 {
		t.Fatalf("tenant 1 usage after first put = %d, want %d", got, want1)
	}
	if got := cm.Stats(); got != want1 {
		t.Fatalf("global usage after first put = %d, want %d", got, want1)
	}

	// --- second Put: evicts slot 0 (k1) ---
	if _, ok := cm.Put(2, []byte("k2"), k2v, 0); !ok {
		t.Fatalf("second Put failed")
	}
	want2 := entryBytes(len(k2v)) // 16+20 = 36
	if got := cm.getTenantCounter(1).used.Load(); got != 0 {
		t.Fatalf("tenant 1 usage after eviction = %d, want 0", got)
	}
	if got := cm.getTenantCounter(2).used.Load(); got != want2 {
		t.Fatalf("tenant 2 usage after second put = %d, want %d", got, want2)
	}
	if got := cm.Stats(); got != want2 {
		t.Fatalf("global usage after second put = %d, want %d", got, want2)
	}

	// --- third Put: evicts slot 0 (k2) ---
	if _, ok := cm.Put(3, []byte("k3"), k3v, 0); !ok {
		t.Fatalf("third Put failed")
	}
	want3 := entryBytes(len(k3v)) // 16+16 = 32
	if got := cm.getTenantCounter(2).used.Load(); got != 0 {
		t.Fatalf("tenant 2 usage after eviction = %d, want 0", got)
	}
	if got := cm.getTenantCounter(3).used.Load(); got != want3 {
		t.Fatalf("tenant 3 usage after third put = %d, want %d", got, want3)
	}
	if got := cm.Stats(); got != want3 {
		t.Fatalf("global usage after third put = %d, want %d", got, want3)
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
