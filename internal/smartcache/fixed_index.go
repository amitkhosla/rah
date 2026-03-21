package smartcache

import (
	"sync/atomic"
)

// FixedIndex represents the tenant-specific storage for globally known keys.
// At 32 slots, it occupies exactly 256 bytes (4 CPU cache lines).
type FixedIndex struct {
	// slots stores the 64-bit SmartPointers (addresses) to the actual data.
	// We use [32]uint64 instead of a slice to ensure contiguous memory 
	// and zero allocation during tenant creation.
	slots [32]uint64
}

// NewFixedIndex initializes a new tenant's fixed storage.
func NewFixedIndex() *FixedIndex {
	return &FixedIndex{}
}

// Get performs a raw O(1) jump.
// The slotID is retrieved beforehand from Registry.GetSlotID(key).
func (f *FixedIndex) Get(slotID uint8) (uint64, bool) {
	// Boundary check (the compiler usually optimizes this for fixed-size arrays)
	if slotID >= 32 {
		return 0, false
	}

	// Atomic load ensures thread-safety without Mutex overhead.
	// This is the "Hot Path" that will run in ~1-2ns if the array is in cache.
	ptr := atomic.LoadUint64(&f.slots[slotID])
	if ptr == 0 {
		return 0, false
	}
	return ptr, true
}

// Set updates the value for a specific slot.
func (f *FixedIndex) Set(slotID uint8, pointer uint64) {
	if slotID < 32 {
		atomic.StoreUint64(&f.slots[slotID], pointer)
	}
}

// Reset zeroes out the entire index (useful for tenant recycling).
func (f *FixedIndex) Reset() {
	for i := 0; i < 32; i++ {
		atomic.StoreUint64(&f.slots[i], 0)
	}
}