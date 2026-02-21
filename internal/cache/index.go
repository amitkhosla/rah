package cache

import (
	"hash/fnv"
	"sync"
	"sync/atomic"
)

// Constants for tuning the hash map behavior.
const (
	ShardCount     = 1024
	SlotsPerBucket = 4
	// Tombstone is a "dead" marker that keeps the linear probe chain alive.
	Tombstone  = 0xFFFFFFFFFFFFFFFF
	Empty      = 0
	LoadFactor = 0.7
)

// table holds the actual hash map data.
// We separate this from the Shard to allow atomic "swapping" during resize.
type table struct {
	data []uint64
	mask uint64
}

// Shard manages a segment of the index.
type Shard struct {
	// mu protects writers only. Readers (Get) do not use this lock.
	mu         sync.Mutex
	tbl        atomic.Pointer[table]
	count      uint32
	tombstones uint32
}

// LookupIndex is the top-level concurrent hash map.
type LookupIndex struct {
	shards [ShardCount]*Shard
}

// NewLookupIndex initializes a 1024-shard index.
func NewLookupIndex() *LookupIndex {
	idx := &LookupIndex{}
	for i := 0; i < ShardCount; i++ {
		t := &table{
			data: make([]uint64, 512), // 512 uint64s = 256 slots (Signature + Pointer)
			mask: (512 / (SlotsPerBucket * 2)) - 1,
		}
		idx.shards[i] = &Shard{}
		idx.shards[i].tbl.Store(t)
	}
	return idx
}

// --- PUBLIC API ---

// Set associates a key with a pointer. It is thread-safe.
func (idx *LookupIndex) Set(tenantID uint16, key string, ptr uint64) {
	s := idx.shards[tenantID%ShardCount]

	// Writers must lock to prevent multiple writers from colliding.
	s.mu.Lock()
	defer s.mu.Unlock()

	sig := hashKey(key)
	idx.setInternal(s, sig, ptr)
}

// Get finds the pointer for a given key. It is lock-free and high-performance.
func (idx *LookupIndex) Get(tenantID uint16, key string) (uint64, bool) {
	sig := hashKey(key)
	s := idx.shards[tenantID%ShardCount]

	// ATOMIC LOAD: Gets a consistent snapshot of the data array and mask.
	t := s.tbl.Load()

	bucketBase := (sig & t.mask) * SlotsPerBucket * 2
	for i := 0; i < 16; i++ {
		pos := (bucketBase + uint64(i*2)) % uint64(len(t.data))
		currSig := t.data[pos]

		if currSig == sig {
			return t.data[pos+1], true
		}
		if currSig == Empty {
			return 0, false
		}
		// Continue if Tombstone
	}
	return 0, false
}

// --- INTERNAL LOGIC ---

// setInternal performs the actual insertion.
// It does NOT lock because it expects the caller (Set) to hold the shard lock.
func (idx *LookupIndex) setInternal(s *Shard, sig uint64, ptr uint64) {
	t := s.tbl.Load()

	// 1. Check Load Factor
	if float64(s.count+s.tombstones) > float64(len(t.data)/2)*LoadFactor {
		s.resize()
		t = s.tbl.Load() // Refresh local reference after resize
	}

	// 2. Linear Probing with Tombstone Resurrection
	bucketBase := (sig & t.mask) * SlotsPerBucket * 2
	firstTombstone := -1

	for i := 0; i < 16; i++ {
		pos := (bucketBase + uint64(i*2)) % uint64(len(t.data))
		currSig := t.data[pos]

		if currSig == sig {
			t.data[pos+1] = ptr // Update existing
			return
		}

		if currSig == Empty {
			target := pos
			if firstTombstone != -1 {
				target = uint64(firstTombstone)
				s.tombstones--
			}
			t.data[target] = sig
			t.data[target+1] = ptr
			s.count++
			return
		}

		if currSig == Tombstone && firstTombstone == -1 {
			firstTombstone = int(pos)
		}
	}

	// 3. Neighborhood is full. Force resize and retry.
	// This recursion is safe because we still hold s.mu.
	s.resize()
	idx.setInternal(s, sig, ptr)
}

// resize doubles the capacity and removes all tombstones.
func (s *Shard) resize() {
	oldTbl := s.tbl.Load()
	newSize := len(oldTbl.data) * 2
	newData := make([]uint64, newSize)
	newMask := uint64(newSize/(SlotsPerBucket*2)) - 1

	for i := 0; i < len(oldTbl.data); i += 2 {
		sig, ptr := oldTbl.data[i], oldTbl.data[i+1]
		if sig == Empty || sig == Tombstone {
			continue
		}

		// Re-hash into the new table
		bucketIdx := (sig & newMask) * SlotsPerBucket * 2
		for j := uint64(0); ; j++ {
			pos := (bucketIdx + (j * 2)) % uint64(newSize)
			if newData[pos] == Empty {
				newData[pos], newData[pos+1] = sig, ptr
				break
			}
		}
	}

	// PUBLISH: Swap the old table with the new table atomically.
	s.tbl.Store(&table{data: newData, mask: newMask})
	s.tombstones = 0
}

func hashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}
