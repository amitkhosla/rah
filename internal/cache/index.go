package cache

import (
	"encoding/binary"
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
	mu         sync.Mutex            // 8 bytes
	tbl        atomic.Pointer[table] // 8 bytes
	count      uint32                // 4 bytes
	tombstones uint32                // 4 bytes
	_          [40]byte              // Padding to ensure 64-byte alignment (Cache Line)
}

// LookupIndex is the top-level concurrent hash map.
type LookupIndex struct {
	shards [ShardCount]*Shard
}

func NewLookupIndex(expectedEntries uint64) *LookupIndex {
	idx := &LookupIndex{}

	entriesPerShard := expectedEntries / ShardCount
	capacity := nextPowerOfTwo(entriesPerShard * 2) // load factor safety

	for i := 0; i < ShardCount; i++ {
		t := &table{
			data: make([]uint64, capacity*2),
			mask: capacity - 1,
		}
		idx.shards[i] = &Shard{}
		idx.shards[i].tbl.Store(t)
	}

	return idx
}

// --- PUBLIC API ---

// Set inserts fingerprint → pointer
func (idx *LookupIndex) Set(fp [16]byte, ptr uint64) bool {

	sig := lower64(fp)
	shard := idx.shards[sig&(ShardCount-1)]

	shard.mu.Lock()
	defer shard.mu.Unlock()

	return idx.setInternal(shard, sig, ptr)
}

// Get retrieves pointer by fingerprint
func (idx *LookupIndex) Get(fp [16]byte) (uint64, bool) {

	sig := lower64(fp)
	shard := idx.shards[sig&(ShardCount-1)]

	t := shard.tbl.Load()

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
	}
	return 0, false
}

// --- INTERNAL LOGIC ---

// setInternal performs the actual insertion.
// It does NOT lock because it expects the caller (Set) to hold the shard lock.
func (idx *LookupIndex) setInternal(s *Shard, sig uint64, ptr uint64) bool {
	t := s.tbl.Load()

	bucketBase := (sig & t.mask) * SlotsPerBucket * 2

	for i := 0; i < 16; i++ {
		pos := (bucketBase + uint64(i*2)) % uint64(len(t.data))
		currSig := t.data[pos]

		if currSig == sig {
			t.data[pos+1] = ptr
			return true
		}

		if currSig == Empty {
			t.data[pos] = sig
			t.data[pos+1] = ptr
			s.count++
			return true
		}
	}

	// No free slot in neighborhood
	return false
}

// nextPowerOfTwo returns the smallest power-of-two
// that is >= n.
//
// If n is already power-of-two, it returns n.
//
// This is required because our hash table uses
// bitmask indexing (index = hash & (capacity-1)).
func nextPowerOfTwo(n uint64) uint64 {
	if n == 0 {
		return 1
	}

	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	n++

	return n
}
func lower64(fp [16]byte) uint64 {
	return binary.LittleEndian.Uint64(fp[:8])
}

func (idx *LookupIndex) Delete(fp [16]byte) {

	sig := lower64(fp)
	shard := idx.shards[sig&(ShardCount-1)]

	shard.mu.Lock()
	defer shard.mu.Unlock()

	t := shard.tbl.Load()

	bucketBase := (sig & t.mask) * SlotsPerBucket * 2

	for i := 0; i < 16; i++ {
		pos := (bucketBase + uint64(i*2)) % uint64(len(t.data))

		if t.data[pos] == sig {
			t.data[pos] = Tombstone
			t.data[pos+1] = 0
			shard.count--
			shard.tombstones++
			return
		}

		if t.data[pos] == Empty {
			return
		}
	}
}
