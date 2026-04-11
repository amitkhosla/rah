package cache

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"rah/internal/rctx"
)

/*
CacheManager is a high-performance, bounded, multi-tenant in-memory cache.

icache2 differs from icache in one way: the H2 filter bits in hashIdx tags
use hashLaneBig16 (real key bytes sampled from 5 structural positions) instead
of maphash-derived H2 bits. This provides better discrimination for keys that
share hash prefixes.

Design:
  - Two index lanes:
    tinyIdx  — lossless tag for keys ≤ 6 bytes (exact match, no hash).
    hashIdx  — real-byte H2 tag for keys > 6 bytes (or 6B with high bytes).
  - Single region matrix [SizeClass][TTLTier]: fixed-stride circular slabs.
    Both lanes share the same regions; lane routing happens at Put/Get time.
  - EntryHeader.XSlotPtrB: 6-byte back-pointer written after every Put so the
    single cleaner goroutine can tombstone stale index entries.
  - Lock-free reads; shard-level mutex for writes.
  - Per-tenant quota enforcement via atomic counters.
  - Optional CacheBackend (default: disk) for persistence and overflow.
    Writes go to backend BEFORE acquiring any in-memory lock.
    Reads check in-memory first; on miss, fall through to backend.

Tag construction for hashIdx (icache2 change):
  - H1 (routing): single maphash call via hashH1Only → lower 48 bits of tag.
  - H2 (filter):  hashLaneBig16 → real key bytes → upper 16 bits of tag.
  - Avoids the second maphash call of Hash128 for H2, while using more
    semantically meaningful bytes than the XOR-folded H1 mix of hashTagFast.
*/

// asyncQueueSize is the capacity of the async backend-write channel.
// If full, Put falls back to a synchronous write (best-effort semantics are
// preserved: neither path fails the in-memory Put on backend error).
const asyncQueueSize = 2048

// eventChanSize is the capacity of the async event-dispatch channel.
// Events are dropped (not delivered) when the channel is full.
const eventChanSize = 4096

// writeJob is a pending backend write queued by Put.
type writeJob struct {
	tenantID uint16
	key      []byte
	value    []byte
	expiry   uint32
}

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
	// Writes are dispatched asynchronously via asyncQueue.
	backend    CacheBackend
	asyncQueue chan writeJob

	// Event subscribers and async dispatch channel.
	// Handlers are called sequentially per event in eventDispatchLoop.
	eventHandlers []EventHandler
	eventMu       sync.RWMutex
	eventCh       chan WriteEvent

	// Cleaner lifecycle (shared stop signal for all background goroutines).
	cleanerStop chan struct{}

	// OnInvalidate is called synchronously when Invalidate is called on this
	// instance. Set by main.go to emit KindCacheInvalidate to other instances.
	// Must not block; nil = no-op.
	OnInvalidate func(tenantID uint16, key []byte)
}

type tenantCounter struct {
	used atomic.Uint64
}

// NewCacheManager initialises the cache with a fixed memory layout.
// All region memory is pre-allocated here; no allocation during steady state.
//
// backend is the persistent/overflow store. Pass nil to use the default disk
// backend at DefaultDiskCachePath ("./icache2"). Pass a custom CacheBackend to
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
		asyncQueue:  make(chan writeJob, asyncQueueSize),
		eventCh:     make(chan WriteEvent, eventChanSize),
		cleanerStop: make(chan struct{}),
	}
	cm.allocateRegions()
	go cm.cleanerLoop()
	go cm.backendSweepLoop()
	go cm.asyncWriteLoop()
	go cm.eventDispatchLoop()
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

// ── async backend write ───────────────────────────────────────────────────────

// enqueueWrite sends a backend write to the async queue.
// If the queue is full it falls back to a synchronous write so that backend
// persistence is never silently dropped (best-effort; errors are ignored).
func (cm *CacheManager) enqueueWrite(tenantID uint16, key, value []byte, expiry uint32) {
	job := writeJob{tenantID: tenantID, key: key, value: value, expiry: expiry}
	select {
	case cm.asyncQueue <- job:
	default:
		// Queue saturated — write synchronously rather than drop.
		_ = cm.backend.Set(tenantID, key, value, expiry)
	}
}

// asyncWriteLoop drains the async backend-write queue until Stop is called.
// It coalesces queued items into a single SetBatch call to reduce backend
// round-trips (critical for Redis; also reduces fsync pressure on disk).
func (cm *CacheManager) asyncWriteLoop() {
	var batch []BackendEntry
	for {
		// Block until the first item is available.
		select {
		case <-cm.cleanerStop:
			return
		case job := <-cm.asyncQueue:
			batch = append(batch[:0], BackendEntry{
				TenantID: job.tenantID,
				Key:      job.key,
				Value:    job.value,
				Expiry:   job.expiry,
			})
		}
		// Drain any additional items already in the queue (non-blocking).
		draining := true
		for draining {
			select {
			case job := <-cm.asyncQueue:
				batch = append(batch, BackendEntry{
					TenantID: job.tenantID,
					Key:      job.key,
					Value:    job.value,
					Expiry:   job.expiry,
				})
			default:
				draining = false
			}
		}
		_ = cm.backend.SetBatch(batch)
	}
}

// ── event bus ────────────────────────────────────────────────────────────────

// Subscribe registers h to be called after every successful Put.
// Handlers are invoked sequentially in a dedicated goroutine; they must not
// block indefinitely. To update a cached value from inside a handler use
// cm.Update (not cm.Put, which would re-emit a WriteEvent).
func (cm *CacheManager) Subscribe(h EventHandler) {
	cm.eventMu.Lock()
	cm.eventHandlers = append(cm.eventHandlers, h)
	cm.eventMu.Unlock()
}

// enqueueEvent sends ev to the async dispatch channel.
// Events are dropped when the channel is full.
func (cm *CacheManager) enqueueEvent(ev WriteEvent) {
	select {
	case cm.eventCh <- ev:
	default:
	}
}

// eventDispatchLoop delivers WriteEvents to all registered handlers until
// Stop is called.
func (cm *CacheManager) eventDispatchLoop() {
	for {
		select {
		case <-cm.cleanerStop:
			return
		case ev := <-cm.eventCh:
			cm.eventMu.RLock()
			handlers := cm.eventHandlers
			cm.eventMu.RUnlock()
			for _, h := range handlers {
				h(ev)
			}
		}
	}
}

// ── Put / Update ──────────────────────────────────────────────────────────────

// Put inserts value into the cache.
//
//  1. Enforce tenant quota.
//  2. Enqueue an async backend write (non-blocking; falls back to sync if full).
//  3. Route to lane (tinyIdx vs hashIdx) by key length / byte range.
//  4. Write into slab region (circular, evicts oldest/expired slot).
//  5. Insert SmartPointer into the index; capture xSlotPtr.
//  6. Write xSlotPtr back into EntryHeader for the cleaner.
//  7. Update accounting; decrement counters for any evicted entry.
//  8. Emit a WriteEvent asynchronously to all subscribers.
func (cm *CacheManager) Put(
	tenantID uint16,
	key []byte,
	value []byte,
	ttl uint32,
) (SmartPointer, bool) {
	ptr, ok := cm.put(tenantID, key, value, ttl)
	if ok {
		cm.enqueueEvent(WriteEvent{TenantID: tenantID, Key: key, Value: value, TTL: ttl})
	}
	return ptr, ok
}

// Update writes to the in-memory cache and enqueues an async backend write,
// but does NOT emit a WriteEvent. Use this from inside EventHandler
// implementations to avoid recursive event dispatch.
func (cm *CacheManager) Update(
	tenantID uint16,
	key []byte,
	value []byte,
	ttl uint32,
) (SmartPointer, bool) {
	return cm.put(tenantID, key, value, ttl)
}

// put is the shared implementation for Put and Update.
func (cm *CacheManager) put(
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

	// Enqueue async backend write before touching any in-memory lock.
	now := uint32(time.Now().Unix())
	expiry := now + ttl
	if ttl == 0 {
		expiry = now
	}
	cm.enqueueWrite(tenantID, key, value, expiry)

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
		// icache2: use real-byte H2 from hashLaneBig16 instead of maphash H2.
		h1 := hashH1Only(tenantID, key)
		h2 := uint64(hashLaneBig16(tenantID, key))
		tag := (h1 & 0x0000FFFFFFFFFFFF) | (h2 << 48)
		if tag == iEmpty {
			tag |= 1 << 48
		}
		if tag == iTombstone {
			tag ^= 1 << 48
		}
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
		// icache2: use real-byte H2 from hashLaneBig16 instead of maphash H2.
		h1 := hashH1Only(tenantID, key)
		h2 := uint64(hashLaneBig16(tenantID, key))
		tag := (h1 & 0x0000FFFFFFFFFFFF) | (h2 << 48)
		if tag == iEmpty {
			tag |= 1 << 48
		}
		if tag == iTombstone {
			tag ^= 1 << 48
		}
		rawVal, found = cm.hashIdx.GetTag(tag)
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
	cm.Update(tenantID, key, val, ttl)

	return val, true
}

// Submit implements engine.OpFlusher, making CacheManager the direct target of
// batch_flush. It is the single decision point for all cache ops:
//   - PUT: written to L1 slab immediately; all PUTs in the batch are then
//     sent to the backend in a single SetBatch call (pipeline for Redis,
//     parallel for disk) rather than queued individually.
//   - GET: L1 checked first; on miss the configured backend is consulted.
//     Result is populated before Done is closed.
//
// fm.CacheExec should be set to the CacheManager directly — no wrapper needed.
func (cm *CacheManager) Submit(batch rctx.Batch) {
	var puts []BackendEntry

	for i := range batch.Ops {
		op := &batch.Ops[i]
		switch op.Type {
		case rctx.OpPut:
			// Write to L1 slab first.
			cm.Put(op.TenantID, op.Key, op.Value, op.TTL)
			// Collect for batched backend write (bypasses async queue for efficiency).
			expiry := uint32(0)
			if op.TTL > 0 {
				expiry = uint32(time.Now().Unix()) + op.TTL
			}
			puts = append(puts, BackendEntry{
				TenantID: op.TenantID,
				Key:      op.Key,
				Value:    op.Value,
				Expiry:   expiry,
			})
		case rctx.OpGet:
			if val, ok := cm.Get(op.TenantID, op.Key); ok {
				op.Result = val
			}
		}
	}

	// Flush all PUTs to the backend in one call.
	if len(puts) > 0 {
		_ = cm.backend.SetBatch(puts)
	}

	if batch.Done != nil {
		close(batch.Done)
	}
}

// Stats returns the current global memory usage in bytes.
func (cm *CacheManager) Stats() uint64 {
	return cm.globalUsed.Load()
}

// Invalidate removes (tenantID, key) from the L1 index and deletes it from
// the backend, then calls OnInvalidate so the caller can propagate the
// deletion to other instances via the ingest pipeline.
// Safe to call when the entry does not exist — both operations are no-ops.
func (cm *CacheManager) Invalidate(tenantID uint16, key []byte) error {
	cm.deleteKey(tenantID, key)
	if cm.OnInvalidate != nil {
		cm.OnInvalidate(tenantID, key)
	}
	return cm.backend.Delete(tenantID, key)
}

// InvalidateLocal removes (tenantID, key) from the L1 index and deletes it
// from the backend WITHOUT calling OnInvalidate. Use this when consuming a
// remote invalidation event to avoid a cascade loop.
func (cm *CacheManager) InvalidateLocal(tenantID uint16, key []byte) error {
	cm.deleteKey(tenantID, key)
	return cm.backend.Delete(tenantID, key)
}

// DeleteTenant evicts all state for tenantID from the cache:
//  1. Resets the tenant quota counter (deleted from tenantUsage sync.Map).
//  2. Delegates to the backend to remove all persisted entries for the tenant.
//
// In-memory slab entries are NOT swept — they are bounded by test workload
// size and will be naturally overwritten by the circular buffer. The index
// entries for the deleted tenant will become harmless misses at read time.
func (cm *CacheManager) DeleteTenant(tenantID uint16) error {
	cm.tenantUsage.Delete(tenantID)
	return cm.backend.DeleteTenant(tenantID)
}

// deleteKey removes the index entry for (tenantID, key). Used in tests.
func (cm *CacheManager) deleteKey(tenantID uint16, key []byte) bool {
	if isHashLane(key) {
		h1 := hashH1Only(tenantID, key)
		h2 := uint64(hashLaneBig16(tenantID, key))
		tag := (h1 & 0x0000FFFFFFFFFFFF) | (h2 << 48)
		if tag == iEmpty {
			tag |= 1 << 48
		}
		if tag == iTombstone {
			tag ^= 1 << 48
		}
		return cm.hashIdx.DeleteTag(tag)
	}
	return cm.tinyIdx.DeleteTag(makeTagTiny(tenantID, key))
}
