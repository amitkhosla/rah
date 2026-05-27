package cache

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// reallocGrowThreshold: grow when writeSlot >= count * 70 / 100.
const reallocGrowThreshold = 70

// defaultShrinkThreshold is the default percentage below which TryShrink
// considers a region underutilised. Configurable per CacheManager.
const defaultShrinkThreshold = 10

// regionStride returns the fixed slot stride for a given SizeClass:
// stride = align8(EntryHeaderSize + sizeClass).
func regionStride(sizeClass uint32) uint32 {
	total := uint32(EntryHeaderSize) + sizeClass
	return (total + 7) &^ 7
}

// nowSec returns the current unix second from the cached clock, or falls back
// to time.Now() when no clock is provided (e.g. unit tests with nil clock).
func nowSec(clk *cachedClock) uint32 {
	if clk != nil {
		return clk.now()
	}
	return uint32(time.Now().Unix())
}

// overwrittenEntry carries metadata about the slab slot evicted when a new
// write claimed its position in the circular buffer.
// Used by CacheManager to update tenant accounting and tombstone the stale
// index entry via xSlotPtr.
type overwrittenEntry struct {
	tenantID uint16
	valueLen uint16
	slotPtr  xSlotPtr // back-pointer to old xSlot; zero until XSlotPtrB is populated
}

// Region is a single-buffer slab that starts at 64 slots and doubles in-place
// when 70 % of current slots are consumed. Once the buffer reaches maxCount
// slots it switches to circular FIFO eviction. Memory can be reclaimed by
// TryShrink when the region becomes sparsely populated.
//
// Read path:  2 atomic loads (count + buf pointer) — fully lock-free.
// Write path: serialised by mu (same as fixed Region).
//
// Memory ordering guarantee (Go memory model §"machine word" sync):
//
//	Write stores buf BEFORE count; Read loads count BEFORE buf.
//	A reader that sees the new count is guaranteed to also see the new buf.
//	A reader that sees the old count sees either buf, but builds the slice
//	with the old (smaller) length — safe because new buf is always ≥ old.
type Region struct {
	// Lock-free read fields.
	// WRITE ORDER: store buf first, then count.
	// READ  ORDER: load count first, then buf.
	buf   atomic.Pointer[byte]
	count atomic.Uint64 // current slot capacity

	stride     uint32
	ttlSeconds uint32

	// Protected by mu.
	writeSlot uint64
	maxCount  uint64 // growth stops here; circular FIFO begins
	clock     *cachedClock
	mu        sync.Mutex
}

// NewRegion creates a Region starting with 64 slots that doubles on demand
// until maxSlots is reached.
//
//   - ttl:      entry TTL in seconds
//   - stride:   use regionStride(sizeClass)
//   - maxSlots: cap on growth; 0 uses 4 MiB / stride
//   - clk:      shared cachedClock (nil = time.Now() fallback)
func NewRegion(ttl, stride uint32, maxSlots uint64, clk *cachedClock) *Region {
	const initialSlots = 64
	if maxSlots == 0 {
		maxSlots = (4 << 20) / uint64(stride)
	}
	if maxSlots < initialSlots {
		maxSlots = initialSlots
	}

	buf := make([]byte, initialSlots*uint64(stride))
	r := &Region{
		stride:     stride,
		ttlSeconds: ttl,
		maxCount:   maxSlots,
		clock:      clk,
	}
	r.buf.Store(&buf[0])
	r.count.Store(initialSlots)
	return r
}

// grow doubles the buffer and copies all existing entries so that all physOff
// values remain valid across the reallocation. Called under mu.
func (r *Region) grow(oldCount uint64) {
	newCount := oldCount * 2
	if newCount > r.maxCount {
		newCount = r.maxCount
	}

	oldBufLen := oldCount * uint64(r.stride)
	newBuf := make([]byte, newCount*uint64(r.stride))

	oldPtr := r.buf.Load()
	copy(newBuf, unsafe.Slice(oldPtr, oldBufLen))

	// Publish: buf BEFORE count (memory ordering guarantee).
	r.buf.Store(&newBuf[0])
	r.count.Store(newCount)
}

// Write claims the next slot and stores the entry.
// Grows at 70 % capacity during the linear phase; circular FIFO once maxCount.
func (r *Region) Write(
	tenantID uint16,
	keyMid uint8,
	value []byte,
	ttl uint32,
) (physOff uint64, gen uint8, old overwrittenEntry, hasOld bool, ok bool) {
	if uint64(len(value)) > uint64(r.stride)-EntryHeaderSize {
		return 0, 0, old, false, false
	}

	r.mu.Lock()

	now := nowSec(r.clock)
	cnt := r.count.Load()

	// Trigger growth when 70 % full and still below the cap.
	if r.writeSlot >= cnt*reallocGrowThreshold/100 && cnt < r.maxCount {
		r.grow(cnt)
		cnt = r.count.Load()
	}

	// Linear assignment during growth; circular once maxCount is reached.
	var physSlot uint64
	if cnt >= r.maxCount {
		physSlot = r.writeSlot % r.maxCount
		gen = uint8(r.writeSlot / r.maxCount)
	} else {
		physSlot = r.writeSlot
		gen = 0
	}
	r.writeSlot++

	physOff = physSlot * uint64(r.stride)

	p := r.buf.Load()
	buf := unsafe.Slice(p, cnt*uint64(r.stride))

	// Capture the evicted entry for accounting (circular phase only).
	h := headerAt(buf, physOff)
	if h.Expiry > 0 {
		hasOld = true
		old = overwrittenEntry{
			tenantID: h.TenantID,
			valueLen: h.ValueLen,
			slotPtr:  xSlotPtrFrom6(h.XSlotPtrB),
		}
	}

	expiry := now + ttl
	if ttl == 0 {
		expiry = now
	}
	h.Expiry = expiry
	h.KeyMid = keyMid
	h.TenantID = tenantID
	h.ValueLen = uint16(len(value))
	h.XSlotPtrB = [6]byte{}
	h.Gen = gen

	copy(buf[physOff+EntryHeaderSize:], value)

	r.mu.Unlock()
	return physOff, gen, old, hasOld, true
}

// Read validates and returns the value at physOff. Lock-free.
//
// Memory ordering: count is loaded before buf so that if the reader sees a
// grown count it is guaranteed to also see the grown buf pointer.
func (r *Region) Read(
	physOff uint64,
	gen2b uint8,
	expTrunc uint16,
	keyMid uint8,
) ([]byte, bool) {
	now := nowSec(r.clock)
	if ExpTruncExpired(expTrunc, now) {
		return nil, false
	}

	// Load count FIRST, then buf — memory ordering guarantee.
	cnt := r.count.Load()
	p := r.buf.Load()
	if p == nil {
		return nil, false
	}

	bufLen := cnt * uint64(r.stride)
	if physOff+EntryHeaderSize > bufLen {
		return nil, false
	}

	buf := unsafe.Slice(p, bufLen)
	h := headerAt(buf, physOff)
	if h.Gen&0x3 != gen2b {
		return nil, false
	}
	if h.Expiry < now {
		return nil, false
	}
	if h.KeyMid != keyMid {
		return nil, false
	}

	end := physOff + EntryHeaderSize + uint64(h.ValueLen)
	if end > bufLen {
		return nil, false
	}
	return buf[physOff+EntryHeaderSize : end], true
}

// UpdateXSlotPtr writes sp into the EntryHeader at physOff. Lock-free.
// Called by CacheManager after SetTagGetPtr returns the xSlot location.
func (r *Region) UpdateXSlotPtr(physOff uint64, sp xSlotPtr) {
	cnt := r.count.Load()
	p := r.buf.Load()
	if p == nil {
		return
	}
	bufLen := cnt * uint64(r.stride)
	if physOff+EntryHeaderSize > bufLen {
		return
	}
	headerAt(unsafe.Slice(p, bufLen), physOff).XSlotPtrB = xSlotPtrTo6(sp)
}

// SlotCount returns the current slot capacity.
func (r *Region) SlotCount() uint64 { return r.count.Load() }

// ResidentBytes returns the byte size of the current buffer allocation.
func (r *Region) ResidentBytes() uint64 {
	return r.count.Load() * uint64(r.stride)
}

// TryShrink attempts to halve the region's allocated memory when all of the
// following conditions hold:
//
//  1. The region is in circular FIFO phase (count == maxCount, maxCount >= 2).
//  2. Non-expired entries are fewer than shrinkPct % of total slots.
//  3. ALL non-expired entries reside in the lower half (slots < maxCount/2).
//
// On success: a new buffer of half the size is allocated, the lower half is
// copied into it, count/maxCount are halved, writeSlot is wrapped into the
// new circular range, and the old buffer is released to the GC.
// Returns true if a shrink was performed.
func (r *Region) TryShrink(shrinkPct uint32, nowSecs uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	cnt := r.count.Load()

	// Only shrink when in the circular FIFO phase and large enough to halve.
	if cnt < r.maxCount || r.maxCount < 2 {
		return false
	}

	half := r.maxCount / 2
	threshold := cnt * uint64(shrinkPct) / 100

	p := r.buf.Load()
	if p == nil {
		return false
	}
	bufLen := cnt * uint64(r.stride)
	buf := unsafe.Slice(p, bufLen)

	// Single pass: count active entries and detect any in the upper half.
	var active uint64
	for slot := uint64(0); slot < cnt; slot++ {
		h := headerAt(buf, slot*uint64(r.stride))
		if h.Expiry == 0 || h.Expiry < nowSecs {
			continue // unwritten or expired
		}
		active++
		if slot >= half {
			// An active entry lives in the upper half — cannot shrink safely.
			return false
		}
	}

	if active >= threshold {
		return false
	}

	// Allocate half-sized buffer and copy the lower half into it.
	halfBytes := half * uint64(r.stride)
	newBuf := make([]byte, halfBytes)
	copy(newBuf, buf[:halfBytes])

	// Publish: buf BEFORE count (same ordering guarantee as grow).
	r.buf.Store(&newBuf[0])
	r.count.Store(half)
	r.maxCount = half

	// Wrap writeSlot into the new circular range.
	r.writeSlot = r.writeSlot % half

	return true
}
