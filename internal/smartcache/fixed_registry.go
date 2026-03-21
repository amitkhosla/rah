package smartcache

import (
	"sync"
	"sync/atomic"
)

// GlobalKey represents a single policy-defined key.
type GlobalKey struct {
	key string
	id  uint8
}

// FixedRegistry handles the global mapping for < 32 strings.
// This is the "Management Plane" logic.
type FixedRegistry struct {
	mu sync.RWMutex
	// active is an atomic snapshot of the keys.
	// Reading this is just one pointer load.
	active atomic.Value // Stores []GlobalKey

	// Internal state for management
	idMap    map[string]uint8
	freeList []uint8
	nextID   uint8
}

var Registry = &FixedRegistry{
	idMap: make(map[string]uint8),
}

// GetSlotID: The Hot Path.
// At 32 entries, a linear scan is the fastest possible way to find a string.
func (r *FixedRegistry) GetSlotID(searchKey string) (uint8, bool) {
	val := r.active.Load()
	if val == nil {
		return 0, false
	}

	keys := val.([]GlobalKey)
	// For N < 32, the CPU pre-fetches this entire slice into L1 cache.
	// This loop usually executes in ~10-20 nanoseconds.
	for i := 0; i < len(keys); i++ {
		if keys[i].key == searchKey {
			return keys[i].id, true
		}
	}
	return 0, false
}

// UpdatePolicy: Rebuilds the flat slice. Called rarely.
func (r *FixedRegistry) UpdatePolicy(keys []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Reset/Update IDs
	newMap := make(map[string]uint8)
	newSlice := make([]GlobalKey, 0, len(keys))

	// Simple assignment: Index in the input is the ID
	for i, k := range keys {
		if i >= 32 {
			break
		} // Hard cap
		id := uint8(i)
		newMap[k] = id
		newSlice = append(newSlice, GlobalKey{key: k, id: id})
	}

	r.idMap = newMap
	r.active.Store(newSlice)
}
