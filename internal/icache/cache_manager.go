package icache

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

/*
CacheManager is a high-performance, bounded, multi-tenant in-memory cache.

Design:
  - Two index lanes:
    tinyIdx  — lossless tag for keys ≤ 6 bytes (exact match, no hash).
    hashIdx  — H2-based tag for keys > 6 bytes (or 6B with high bytes).
  - Single region matrix [SizeClass][TTLTier]: fixed-stride circular slabs.
    Both lanes share the same regions; lane routing happens at Put/Get time.
  - EntryHeader.XSlotPtrB: 6-byte back-pointer written after every Put so the
    single cleaner goroutine can tombstone stale index entries.
  - Lock-free reads; shard-level mutex for writes.
  - Per-tenant quota enforcement via atomic counters.
  - Optional CacheBackend (default: disk) for persistence and overflow.
    Writes go to backend BEFORE acquiring any in-memory lock.
    Reads check in-memory first; on miss, fall through to backend.

Difference from cache.CacheManager:
  - Uses InlineIndex (inline-slot trie) instead of LookupIndex (separate-pool trie).
  - No COW node-slice overhead: node writes are O(1) memory.
  - Shallower cache-miss paths: inline slots on CL1 of each node.
  - hashIdx uses makeTagHashH2 to embed fingerprint bytes into tag bits 48–63,
    enabling the 16-bit H2 filter to use real fingerprint data.
*/

type CacheManager struct {
	// Immutable after init.
	totalMemory uint64
	sizeClasses []uint32
	ttlTiers    []uint32

	// 2-D region matrix [sizeClass][ttlTier].
	regions [][]*Region

	// Per-region cleaner scan cursors [sizeClass][ttlTier].
	cleanSlots [][]uint64

	// Two independent inline-slot trie indices.
	tinyIdx *InlineIndex // keys ≤ 6 bytes (lossless tag)
	hashIdx *InlineIndex // keys > 6 bytes, or 6B with high bytes (H2 tag)

	// Per-tenant quota.
	tenantLimit uint64
	tenantUsage sync.Map // tenantID uint16 → *tenantCounter

	// Global usage in bytes.
	globalUsed atomic.Uint64

	// Persistent / overflow backend. Never nil: defaults to diskBackend.
	// Writes happen before in-memory locks are acquired.
	backend CacheBackend

	// Cleaner lifecycle.
	cleanerStop chan struct{}
}

type tenantCounter struct {
	used atomic.Uint64
}

// NewCacheManager initialises the cache with a fixed memory layout.
// All region memory is pre-allocated here; no allocation during steady state.
//
// backend is the persistent/overflow store. Pass nil to use the default disk
// backend at DefaultDiskCachePath ("./icache"). Pass a custom CacheBackend to
// use Redis, Dragonfly, or any other implementation.
//
// Two background goroutines are started:
//   - in-memory cleaner: tombstones expired slab index entries (50ms idle sleep)
//   - backend sweeper:   deletes expired backend entries (runs every 5 minutes)
func NewCacheManager(
	totalMemory uint64,
	sizeClasses []uint32,
	ttlTiers []uint32,
	expectedEntries uint64,
	tenantLimit uint64,
	backend CacheBackend,
) (*CacheManager, error) {
	if len(sizeClasses) == 0 || len(ttlTiers) == 0 {
		return nil, errors.New("sizeClasses and ttlTiers must not be empty")
	}

	if backend == nil {
		var err error
		backend, err = NewDiskBackend(DefaultDiskCachePath)
		if err != nil {
			return nil, err
		}
	}

	cm := &CacheManager{
		totalMemory: totalMemory,
		sizeClasses: sizeClasses,
		ttlTiers:    ttlTiers,
		tinyIdx:     NewInlineIndex(0),
		hashIdx:     NewInlineIndex(0),
		tenantLimit: tenantLimit,
		backend:     backend,
		cleanerStop: make(chan struct{}),
	}
	cm.allocateRegions()
	go cm.cleanerLoop()
	go cm.backendSweepLoop()
	return cm, nil
}

// Stop signals both background goroutines to exit and closes the backend.
// Call once on shutdown.
func (cm *CacheManager) Stop() {
	close(cm.cleanerStop)
	cm.backend.Close()
}

func (cm *CacheManager) allocateRegions() {
	classCount := len(cm.sizeClasses)
	tierCount := len(cm.ttlTiers)
	totalRegions := classCount * tierCount
	regionMemory := cm.totalMemory / uint64(totalRegions)

	cm.regions = make([][]*Region, classCount)
	cm.cleanSlots = make([][]uint64, classCount)
	for i := 0; i < classCount; i++ {
		cm.regions[i] = make([]*Region, tierCount)
		cm.cleanSlots[i] = make([]uint64, tierCount)
		stride := regionStride(cm.sizeClasses[i])
		for j := 0; j < tierCount; j++ {
			cm.regions[i][j] = NewRegion(regionMemory, cm.ttlTiers[j], stride)
		}
	}
}

// ── backend sweep ────────────────────────────────────────────────────────────

const backendSweepInterval = 5 * time.Minute

// backendSweepLoop runs every 5 minutes and deletes expired entries from the
// persistent backend.
func (cm *CacheManager) backendSweepLoop() {
	for {
		select {
		case <-cm.cleanerStop:
			return
		case <-time.After(backendSweepInterval):
			cm.backend.Sweep()
		}
	}
}

// ── cleaner ──────────────────────────────────────────────────────────────────

const cleanerBatchSize = 256 // slots examined per region per sweep pass

// cleanerLoop is the single background goroutine that expires index entries
// whose slab slots have TTL-lapsed but have not yet been evicted by the
// circular buffer write path.
func (cm *CacheManager) cleanerLoop() {
	for {
		select {
		case <-cm.cleanerStop:
			return
		default:
		}

		now := uint32(time.Now().Unix())
		anyWork := false
		for ci := range cm.regions {
			for ti := range cm.regions[ci] {
				if cm.sweepBatch(ci, ti, now) > 0 {
					anyWork = true
				}
			}
		}

		if !anyWork {
			select {
			case <-cm.cleanerStop:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

// sweepBatch scans up to cleanerBatchSize slots in region [ci][ti] starting at
// cleanSlots[ci][ti]. For each slot whose TTL has lapsed it attempts to
// tombstone the index entry via the EntryHeader.XSlotPtrB back-pointer.
// Returns the number of entries tombstoned.
func (cm *CacheManager) sweepBatch(ci, ti int, now uint32) int {
	r := cm.regions[ci][ti]
	if r.count == 0 {
		return 0
	}

	start := cm.cleanSlots[ci][ti]
	cleaned := 0

	for i := uint64(0); i < cleanerBatchSize; i++ {
		slot := (start + i) % r.count
		physOff := slot * uint64(r.stride)

		h := headerAt(r.buf, physOff)

		// Skip unwritten slots (Expiry==0) and live entries.
		if h.Expiry == 0 || h.Expiry >= now {
			continue
		}

		sp := xSlotPtrFrom6(h.XSlotPtrB)
		if uint64(sp) == 0 {
			// XSlotPtrB not yet populated (edge case: Put was interrupted).
			continue
		}

		gen2b := h.Gen & 0x3
		// Reconstruct the tag from the xSlotPtr routing bits.
		tag := sp.tagBits()
		var idx *InlineIndex
		if sp.laneBit() == 0 {
			idx = cm.tinyIdx
		} else {
			idx = cm.hashIdx
		}

		if idx.TryTombstone(tag, physOff, gen2b) {
			cleaned++
		}
	}

	cm.cleanSlots[ci][ti] = (start + cleanerBatchSize) % r.count
	return cleaned
}

// ── lane helpers ─────────────────────────────────────────────────────────────

// isHashLane reports whether key must use hashIdx.
// Returns true for keys > 6 bytes, or 6-byte keys with any byte ≥ 128
// (which cannot be losslessly packed into a 6×7-bit tiny tag).
func isHashLane(key []byte) bool {
	if len(key) > 6 {
		return true
	}
	if len(key) == 6 {
		for _, b := range key {
			if b >= 128 {
				return true
			}
		}
	}
	return false
}

// middleByte returns key[len/2], stored in EntryHeader.KeyMid for hashIdx
// entries as an extra identity check at read time.
func middleByte(key []byte) uint8 {
	if len(key) == 0 {
		return 0
	}
	return key[len(key)/2]
}

// ── index and region routing ──────────────────────────────────────────────────

func (cm *CacheManager) selectSizeClass(valueLen int) int {
	for i, size := range cm.sizeClasses {
		if uint32(valueLen) <= size {
			return i
		}
	}
	return len(cm.sizeClasses) - 1
}

func (cm *CacheManager) selectTTLTier(ttl uint32) int {
	for i, tier := range cm.ttlTiers {
		if ttl <= tier {
			return i
		}
	}
	return len(cm.ttlTiers) - 1
}

func (cm *CacheManager) getTenantCounter(tenantID uint16) *tenantCounter {
	val, _ := cm.tenantUsage.LoadOrStore(tenantID, &tenantCounter{})
	return val.(*tenantCounter)
}

// ── Put ───────────────────────────────────────────────────────────────────────

// Put inserts value into the cache.
//
//  1. Enforce tenant quota.
//  2. Write to backend BEFORE acquiring any in-memory lock (slow I/O first).
//  3. Route to lane (tinyIdx vs hashIdx) by key length / byte range.
//  4. Write into slab region (circular, evicts oldest/expired slot).
//  5. Insert SmartPointer into the index; capture xSlotPtr.
//  6. Write xSlotPtr back into EntryHeader for the cleaner.
//  7. Update accounting; decrement counters for any evicted entry.
func (cm *CacheManager) Put(
	tenantID uint16,
	key []byte,
	value []byte,
	ttl uint32,
) (SmartPointer, bool) {
	entrySize := uint64(EntryHeaderSize + len(value))

	counter := cm.getTenantCounter(tenantID)
	if cm.tenantLimit > 0 && counter.used.Load()+entrySize > cm.tenantLimit {
		return 0, false
	}

	// Write to backend first — before any in-memory lock is acquired.
	now := uint32(time.Now().Unix())
	expiry := now + ttl
	if ttl == 0 {
		expiry = now
	}
	_ = cm.backend.Set(tenantID, key, value, expiry) // best-effort; don't fail Put on backend error

	classID := cm.selectSizeClass(len(value))
	tierID := cm.selectTTLTier(ttl)
	region := cm.regions[classID][tierID]

	hashLane := isHashLane(key)
	keyMid := uint8(0)
	if hashLane {
		keyMid = middleByte(key)
	}

	physOff, gen, old, hasOld, ok := region.Write(tenantID, keyMid, value, ttl)
	if !ok {
		return 0, false
	}

	// Eviction accounting: slot was occupied by a different (now-evicted) entry.
	if hasOld {
		cm.subUsage(old.tenantID, uint64(EntryHeaderSize+old.valueLen))
	}

	ptr := PackSlabVal(gen&0x3, ExpTrunc(expiry), uint8(classID), uint8(tierID), physOff)

	// Insert into index and capture the back-pointer for the cleaner.
	var sp xSlotPtr
	if hashLane {
		fp := Hash128(tenantID, key)
		tag := makeTagHashH2(fp) // use H2-enriched tag for better filter discrimination
		sp = cm.hashIdx.SetTagGetPtr(tag, ptr, xPtrLaneHash)
	} else {
		tag := makeTagTiny(tenantID, key)
		sp = cm.tinyIdx.SetTagGetPtr(tag, ptr, xPtrLaneTiny)
	}

	// Write xSlotPtr back into the slab entry so the cleaner can navigate here.
	if sp != 0 {
		region.UpdateXSlotPtr(physOff, sp)
	}

	counter.used.Add(entrySize)
	cm.globalUsed.Add(entrySize)
	return ptr, true
}

func subtractUint64(v *atomic.Uint64, delta uint64) {
	if delta == 0 {
		return
	}
	v.Add(^uint64(delta - 1))
}

func (cm *CacheManager) subUsage(tenantID uint16, entrySize uint64) {
	subtractUint64(&cm.getTenantCounter(tenantID).used, entrySize)
	subtractUint64(&cm.globalUsed, entrySize)
}

// ── Get ───────────────────────────────────────────────────────────────────────

// Get retrieves a value from the cache.
//
//  1. In-memory path (lock-free): index lookup → slab read.
//  2. On miss: fall through to backend (disk / Redis).
//  3. On backend hit: warm the in-memory slab for future reads.
func (cm *CacheManager) Get(tenantID uint16, key []byte) ([]byte, bool) {
	hashLane := isHashLane(key)

	var rawVal uint64
	var found bool
	if hashLane {
		fp := Hash128(tenantID, key)
		rawVal, found = cm.hashIdx.GetTag(makeTagHashH2(fp))
	} else {
		rawVal, found = cm.tinyIdx.GetTag(makeTagTiny(tenantID, key))
	}

	if found {
		gen2b, expTrunc, typ := Unpack(rawVal)
		if typ == xValTypeSlabRAM {
			classID, tierID, physOff := UnpackSlab(rawVal)
			if int(classID) < len(cm.regions) && int(tierID) < len(cm.regions[classID]) {
				keyMid := uint8(0)
				if hashLane {
					keyMid = middleByte(key)
				}
				if val, ok := cm.regions[classID][tierID].Read(physOff, gen2b, expTrunc, keyMid); ok {
					return val, true
				}
			}
		}
	}

	// In-memory miss: check backend. No lock held during I/O.
	val, expiry, ok := cm.backend.Get(tenantID, key)
	if !ok {
		return nil, false
	}

	// Warm the in-memory slab. TTL derived from remaining lifetime.
	now := uint32(time.Now().Unix())
	var ttl uint32
	if expiry > now {
		ttl = expiry - now
	}
	cm.Put(tenantID, key, val, ttl)

	return val, true
}

// Stats returns the current global memory usage in bytes.
func (cm *CacheManager) Stats() uint64 {
	return cm.globalUsed.Load()
}

// deleteKey removes the index entry for (tenantID, key). Used in tests.
func (cm *CacheManager) deleteKey(tenantID uint16, key []byte) bool {
	if isHashLane(key) {
		fp := Hash128(tenantID, key)
		return cm.hashIdx.DeleteTag(makeTagHashH2(fp))
	}
	return cm.tinyIdx.DeleteTag(makeTagTiny(tenantID, key))
}
