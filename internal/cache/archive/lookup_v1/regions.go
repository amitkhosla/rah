package lookup_v1

import (
	"sync"
	"time"
)

// regionStride returns the fixed slot stride for a given SizeClass:
// stride = align8(EntryHeaderSize + sizeClass).
func regionStride(sizeClass uint32) uint32 {
	total := uint32(EntryHeaderSize) + sizeClass
	return (total + 7) &^ 7
}

// overwrittenEntry carries metadata about the slab slot that was evicted when
// a new write claimed its position in the circular buffer.
// Used by CacheManager to update tenant accounting and (Phase 2) tombstone the
// stale index entry via xSlotPtr.
type overwrittenEntry struct {
	tenantID uint16
	valueLen uint16
	slotPtr  xSlotPtr // back-pointer to old xSlot; zero until Phase 2 populates it
}

// Region is a fixed-stride circular slab for one (SizeClass × TTLTier) cell.
//
// Layout:
//   - buf: contiguous pre-allocated byte slice, len = count × stride.
//   - Each slot: EntryHeader(16B) + value bytes (≤SizeClass) + zero padding.
//   - Writes advance writeSlot monotonically; physSlot = writeSlot % count.
//   - gen = uint8(writeSlot / count): wraps at 256, used as a stale-pointer
//     guard in concert with EntryHeader.Expiry.
//
// Concurrency:
//   - Writes: serialised by mu.
//   - Reads:  lock-free; validated by gen + expiry + keyMid checks.
type Region struct {
	buf        []byte
	count      uint64     // total slots = len(buf) / stride
	stride     uint32     // EntryHeaderSize + SizeClass, aligned to 8
	ttlSeconds uint32     // TTL for entries written to this region
	writeSlot  uint64     // monotonic slot counter, protected by mu
	mu         sync.Mutex
}

// NewRegion allocates a Region with the given byte budget, TTL, and stride.
// Actual capacity = floor(size / stride) slots; may be 0 for tiny budgets.
func NewRegion(size uint64, ttl uint32, stride uint32) *Region {
	count := size / uint64(stride)
	return &Region{
		buf:        make([]byte, count*uint64(stride)),
		count:      count,
		stride:     stride,
		ttlSeconds: ttl,
	}
}

// Write claims the next slot, stores the entry, and returns its location.
//
//   - physOff: byte offset of the new slot in buf.
//   - gen:     8-bit generation counter (writeSlot / count, truncated to uint8).
//   - old:     metadata of the entry that previously occupied this slot (if any).
//   - hasOld:  true when a prior entry was evicted (Expiry was set).
//   - ok:      false only when count == 0 or len(value) > SizeClass.
func (r *Region) Write(
	tenantID uint16,
	keyMid uint8,
	value []byte,
	ttl uint32,
) (physOff uint64, gen uint8, old overwrittenEntry, hasOld bool, ok bool) {
	maxVal := uint64(r.stride) - EntryHeaderSize
	if r.count == 0 || uint64(len(value)) > maxVal {
		return 0, 0, old, false, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	slot := r.writeSlot
	r.writeSlot++

	physSlot := slot % r.count
	physOff = physSlot * uint64(r.stride)
	gen = uint8(slot / r.count) // wraps at 256; 0 on first revolution

	// Snapshot old header for eviction accounting before overwriting.
	oldH := headerAt(r.buf, physOff)
	if oldH.Expiry > 0 { // Expiry == 0 means slot was never written
		hasOld = true
		old = overwrittenEntry{
			tenantID: oldH.TenantID,
			valueLen: oldH.ValueLen,
			slotPtr:  xSlotPtrFrom6(oldH.XSlotPtrB), // zero until Phase 2
		}
	}

	// Write new header. Gen written last so a concurrent reader seeing the new
	// Gen is guaranteed to also see the updated Expiry and KeyMid.
	now := uint32(time.Now().Unix())
	expiry := now + ttl
	if ttl == 0 {
		expiry = now
	}

	h := headerAt(r.buf, physOff)
	h.Expiry = expiry
	h.KeyMid = keyMid
	h.TenantID = tenantID
	h.ValueLen = uint16(len(value))
	// h.XSlotPtrB populated by CacheManager after index Set (Phase 2: TODO).
	h.Gen = gen // written last: signals to concurrent readers that write is complete

	copy(r.buf[physOff+EntryHeaderSize:], value)

	return physOff, gen, old, hasOld, true
}

// UpdateXSlotPtr writes sp into the EntryHeader at physOff.
// Called by CacheManager after SetTagGetPtr returns the final xSlot location.
// Lock-free: only one goroutine (the one that called Write) ever calls this.
func (r *Region) UpdateXSlotPtr(physOff uint64, sp xSlotPtr) {
	headerAt(r.buf, physOff).XSlotPtrB = xSlotPtrTo6(sp)
}

// Read validates and returns the value slice stored at physOff.
//
// Validation sequence (all lock-free):
//  1. ExpTrunc pre-check  — avoids slab touch for obviously expired entries.
//  2. Gen & 0x3 == gen2b  — fast stale-slot detection (slot recycled).
//  3. Expiry authoritative — full unix32 TTL confirmation.
//  4. KeyMid match        — identity byte for Lane 2 (hashIdx) entries;
//     Lane 1 passes 0x00 since full key identity is guaranteed by tag match.
//
// The returned slice is a direct view into the Region buffer; callers must not
// retain it beyond the next Write to the same Region.
func (r *Region) Read(
	physOff uint64,
	gen2b uint8,
	expTrunc uint16,
	keyMid uint8,
) ([]byte, bool) {
	if physOff+EntryHeaderSize > uint64(len(r.buf)) {
		return nil, false
	}

	now := uint32(time.Now().Unix())

	// Fast pre-check: truncated expiry (no slab cache miss needed).
	if ExpTruncExpired(expTrunc, now) {
		return nil, false
	}

	h := headerAt(r.buf, physOff)

	// Gen pre-check: low 2 bits of generation match the val field.
	if h.Gen&0x3 != gen2b {
		return nil, false
	}
	// Authoritative expiry.
	if h.Expiry < now {
		return nil, false
	}
	// Key identity byte (Lane 2 only; Lane 1 passes 0x00 stored in header).
	if h.KeyMid != keyMid {
		return nil, false
	}

	end := physOff + EntryHeaderSize + uint64(h.ValueLen)
	if end > uint64(len(r.buf)) {
		return nil, false
	}
	return r.buf[physOff+EntryHeaderSize : end], true
}
