package datacache

import (
	"bytes"
	"errors"
	"sort"
	"sync/atomic"
	"time"
)

var ErrPoolExhausted = errors.New("datacache: pool exhausted")

const DefaultPoolSlots = 256

type poolSlot struct {
	arena      Arena
	indices    []uint32 // sort index: indices[i] = actual arena row for sorted position i
	inUse      atomic.Bool
	ownerKey   [48]byte     // ascii "tenantID:appName\0..." for quota release
	ttlExpUnix atomic.Int64 // unix seconds, 0 = no TTL
	name       [128]byte    // collection name for TTL cleaner sync.Map cleanup
}

// ArenaPool is a GC-resistant pre-allocated pool of Arena slots.
// Zero value is not usable; construct with NewArenaPool.
//
// Design goals (following ctx_pool.go pattern):
//   - Single GC root (slots array) with strong references to all arenas
//   - Claim is lock-free: select on buffered chan
//   - Release is non-blocking: select with default to avoid blocking
//   - No allocations in hot paths (Claim, Release, SortBy)
type ArenaPool struct {
	slots       []poolSlot // pre-allocated once at NewArenaPool
	free        chan uint8 // buffered channel of free slot indices
	maxBufBytes int64
	maxSlots    int
	maxRecords  int // max records per slot (for indices pre-alloc)
}

// NewArenaPool allocates a pool with maxSlots arena slots, each backed by
// a maxBufBytes buffer and indices array for up to maxRecords entries.
// All buffers and indices are pre-allocated once; Claim and Release do not allocate.
func NewArenaPool(maxSlots int, maxBufBytes int64, maxRecords int) *ArenaPool {
	slots := make([]poolSlot, maxSlots)

	// Pre-allocate arena buffers and indices for all slots.
	for i := range slots {
		slots[i].arena.buf = make([]byte, maxBufBytes)
		slots[i].indices = make([]uint32, maxRecords)
		// liveBits is not pre-allocated; it will be lazily allocated on first use
	}

	// Create free channel with capacity maxSlots, pre-populated with all indices.
	free := make(chan uint8, maxSlots)
	for i := 0; i < maxSlots; i++ {
		free <- uint8(i)
	}

	return &ArenaPool{
		slots:       slots,
		free:        free,
		maxBufBytes: maxBufBytes,
		maxSlots:    maxSlots,
		maxRecords:  maxRecords,
	}
}

// Claim reserves an arena slot from the pool. If timeoutMs <= 0, Claim returns
// immediately with ErrPoolExhausted if no slot is available. Otherwise, Claim
// waits up to timeoutMs milliseconds for a slot to become available.
func (p *ArenaPool) Claim(timeoutMs int) (uint8, error) {
	if timeoutMs <= 0 {
		// Non-blocking: try immediate select
		select {
		case idx := <-p.free:
			p.slots[idx].inUse.Store(true)
			return idx, nil
		default:
			return 0, ErrPoolExhausted
		}
	}

	// Blocking with timeout
	select {
	case idx := <-p.free:
		p.slots[idx].inUse.Store(true)
		return idx, nil
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		return 0, ErrPoolExhausted
	}
}

// Release returns an arena slot to the pool. The arena buffer is reset and
// metadata is cleared. The slot is sent back to the free channel non-blockingly;
// if the channel is full (shouldn't happen), the slot is silently dropped
// (caller must handle this as an error condition).
func (p *ArenaPool) Release(idx uint8) {
	slot := &p.slots[idx]
	slot.arena.Reset()
	slot.inUse.Store(false)
	slot.ttlExpUnix.Store(0)
	slot.ownerKey = [48]byte{}
	slot.name = [128]byte{}

	// Non-blocking send back to free channel
	select {
	case p.free <- idx:
		// Successfully returned to pool
	default:
		// Channel is full — this shouldn't happen under normal operation.
		// The slot is dropped; GC will handle its eventual cleanup if needed.
		// In production, consider logging a warning here.
	}
}

// InitSlot initializes a claimed arena slot with the given capacity and schema.
// Must be called immediately after Claim before using the arena.
func (p *ArenaPool) InitSlot(idx uint8, capacity int, schema Schema) {
	slot := &p.slots[idx]
	slot.arena.Cap = capacity
	slot.arena.Schema = schema
	slot.arena.Size.Store(0)
	slot.arena.LiveCount.Store(0)

	// Reset freeRows from any previous reuse
	slot.arena.freeRows = nil

	// Re-slice indices to capacity and initialize to natural order (0, 1, 2, ..., capacity-1)
	slot.indices = slot.indices[:capacity]
	for i := 0; i < capacity; i++ {
		slot.indices[i] = uint32(i)
	}
}

// SortBy sorts the arena at slot idx by the column at colIdx. The sort is performed
// on the indices array only; no data is moved in the arena buffer.
// Only live rows are included in the sort.
// colKind determines the comparison strategy (Float64, Int64, Enum, StringN).
func (p *ArenaPool) SortBy(idx uint8, colIdx int) {
	slot := &p.slots[idx]
	arena := &slot.arena

	// Rebuild indices from live rows only
	n := 0
	for i := 0; i < int(arena.Size.Load()); i++ {
		if arena.IsLive(i) {
			slot.indices[n] = uint32(i)
			n++
		}
	}

	indices := slot.indices[:n]
	colKind := arena.Schema.Fields[colIdx].Kind

	sort.Slice(indices, func(i, j int) bool {
		rowI := int(indices[i])
		rowJ := int(indices[j])

		switch colKind {
		case FieldFloat64:
			return arena.Float64(colIdx, rowI) < arena.Float64(colIdx, rowJ)
		case FieldInt64:
			return arena.Int64(colIdx, rowI) < arena.Int64(colIdx, rowJ)
		case FieldEnum:
			return arena.Enum(colIdx, rowI) < arena.Enum(colIdx, rowJ)
		case FieldStringN:
			return bytes.Compare(arena.StringAt(colIdx, rowI), arena.StringAt(colIdx, rowJ)) < 0
		default:
			return false
		}
	})

	// Mark that the collection has been sorted by colIdx
	arena.Schema.SortByIdx = colIdx
}

// ResetSortIndex reinitializes the indices array for slot idx to natural order,
// including only live rows.
func (p *ArenaPool) ResetSortIndex(idx uint8) {
	slot := &p.slots[idx]
	arena := &slot.arena
	n := 0
	for i := 0; i < int(arena.Size.Load()); i++ {
		if arena.IsLive(i) {
			slot.indices[n] = uint32(i)
			n++
		}
	}

	// Reset sort indicator
	arena.Schema.SortByIdx = -1
}

// LiveIndexCount returns the number of live entries currently in the sort index.
func (p *ArenaPool) LiveIndexCount(idx uint8) int {
	slot := &p.slots[idx]
	arena := &slot.arena
	n := 0
	for i := 0; i < int(arena.Size.Load()); i++ {
		if arena.IsLive(i) {
			n++
		}
	}
	return n
}
