package v3

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
type overwrittenEntry struct {
	tenantID uint16
	valueLen uint16
	slotPtr  xSlotPtr
}

// Region is a fixed-stride circular slab for one (SizeClass × TTLTier) cell.
//
// Layout:
//   - buf: contiguous pre-allocated byte slice, len = count × stride.
//   - Each slot: EntryHeader(24B) + value bytes (≤SizeClass) + zero padding.
//   - Writes advance writeSlot monotonically; physSlot = writeSlot % count.
//   - gen = uint8(writeSlot / count): wraps at 256.
//
// Concurrency:
//   - Writes: serialised by mu.
//   - Reads:  lock-free; validated by gen + expiry + keyFP checks.
type Region struct {
	buf        []byte
	count      uint64
	stride     uint32
	ttlSeconds uint32
	writeSlot  uint64
	mu         sync.Mutex
}

// NewRegion allocates a Region with the given byte budget, TTL, and stride.
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
// keyFP is the 3-byte fingerprint of the key: key[(n/2)+1%n], key[(n/4)+1%n], key[(3n/4)+1%n].
func (r *Region) Write(
	tenantID uint16,
	keyFP [3]byte,
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
	gen = uint8(slot / r.count)

	// Snapshot old header for eviction accounting before overwriting.
	oldH := headerAt(r.buf, physOff)
	if oldH.Expiry > 0 {
		hasOld = true
		old = overwrittenEntry{
			tenantID: oldH.TenantID,
			valueLen: oldH.ValueLen,
			slotPtr:  xSlotPtrFrom6(oldH.XSlotPtrB),
		}
	}

	now := uint32(time.Now().Unix())
	expiry := now + ttl
	if ttl == 0 {
		expiry = now
	}

	h := headerAt(r.buf, physOff)
	h.Expiry = expiry
	h.TenantID = tenantID
	h.ValueLen = uint16(len(value))
	h.KeyFP = keyFP
	// h.XSlotPtrB populated by CacheManager after index Set.
	h.Gen = gen // written last: signals to concurrent readers that write is complete

	copy(r.buf[physOff+EntryHeaderSize:], value)

	return physOff, gen, old, hasOld, true
}

// UpdateXSlotPtr writes sp into the EntryHeader at physOff.
func (r *Region) UpdateXSlotPtr(physOff uint64, sp xSlotPtr) {
	headerAt(r.buf, physOff).XSlotPtrB = xSlotPtrTo6(sp)
}

// Read validates and returns the value slice stored at physOff.
//
// Validation sequence (all lock-free):
//  1. ExpTrunc pre-check  — avoids slab touch for obviously expired entries.
//  2. Gen & 0x3 == gen2b  — fast stale-slot detection (slot recycled).
//  3. Expiry authoritative — full unix32 TTL confirmation.
//  4. KeyFP match         — 3-byte fingerprint check for hashIdx entries.
func (r *Region) Read(
	physOff uint64,
	gen2b uint8,
	expTrunc uint16,
	keyFP [3]byte,
) ([]byte, bool) {
	if physOff+EntryHeaderSize > uint64(len(r.buf)) {
		return nil, false
	}

	now := uint32(time.Now().Unix())

	if ExpTruncExpired(expTrunc, now) {
		return nil, false
	}

	h := headerAt(r.buf, physOff)

	if h.Gen&0x3 != gen2b {
		return nil, false
	}
	if h.Expiry < now {
		return nil, false
	}
	if h.KeyFP != keyFP {
		return nil, false
	}

	end := physOff + EntryHeaderSize + uint64(h.ValueLen)
	if end > uint64(len(r.buf)) {
		return nil, false
	}
	return r.buf[physOff+EntryHeaderSize : end], true
}
