package v3

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

/*
CacheManager is a high-performance, bounded, multi-tenant in-memory cache.

icache3 differences from icache2:
  - EntryHeader is 24 bytes: KeyFP [3]byte replaces single KeyMid uint8.
  - InlineIndex nodes carry klenWord and tenantWord for triple-mask pre-filtering.
  - GetTag takes (tag, keyLen, tenantID) — filters to candidate slots via three masks.
  - SetTagGetPtr takes (tag, val, lane, keyLen, tenantID) — maintains klenWord/tenantWord.
  - Region.Write/Read use [3]byte keyFP sampled from key[(n/2)+1%n], [(n/4)+1%n], [(3n/4)+1%n].
*/

type CacheManager struct {
	totalMemory uint64
	sizeClasses []uint32
	ttlTiers    []uint32

	regions    [][]*Region
	cleanSlots [][]uint64

	tinyIdx *InlineIndex
	hashIdx *InlineIndex

	tenantLimit uint64
	tenantUsage sync.Map

	globalUsed atomic.Uint64

	backend CacheBackend

	cleanerStop chan struct{}
}

type tenantCounter struct {
	used atomic.Uint64
}

// NewCacheManager initialises the cache with a fixed memory layout.
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

func (cm *CacheManager) Stop() {
	close(cm.cleanerStop)
	_ = cm.backend.Close()
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

const cleanerBatchSize = 256

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

		if h.Expiry == 0 || h.Expiry >= now {
			continue
		}

		sp := xSlotPtrFrom6(h.XSlotPtrB)
		if uint64(sp) == 0 {
			continue
		}

		gen2b := h.Gen & 0x3
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

// makeKeyFP computes the 3-byte slab fingerprint from key adjacent positions.
// Samples key[(n/2)+1%n], key[(n/4)+1%n], key[(3n/4)+1%n].
// Only called for hashLane keys (len > 6), so indices are always valid.
func makeKeyFP(key []byte) [3]byte {
	ln := len(key)
	mid := ln >> 1
	q1 := ln >> 2
	q3 := (ln * 3) >> 2
	return [3]byte{
		key[(mid+1)%ln],
		key[(q1+1)%ln],
		key[(q3+1)%ln],
	}
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

	now := uint32(time.Now().Unix())
	expiry := now + ttl
	if ttl == 0 {
		expiry = now
	}
	_ = cm.backend.Set(tenantID, key, value, expiry)

	classID := cm.selectSizeClass(len(value))
	tierID := cm.selectTTLTier(ttl)
	region := cm.regions[classID][tierID]

	hashLane := isHashLane(key)
	keyFP := [3]byte{}
	if hashLane {
		keyFP = makeKeyFP(key)
	}

	physOff, gen, old, hasOld, ok := region.Write(tenantID, keyFP, value, ttl)
	if !ok {
		return 0, false
	}

	if hasOld {
		cm.subUsage(old.tenantID, uint64(EntryHeaderSize+old.valueLen))
	}

	ptr := PackSlabVal(gen&0x3, ExpTrunc(expiry), uint8(classID), uint8(tierID), physOff)

	var sp xSlotPtr
	if hashLane {
		h1 := hashH1Only(tenantID, key)
		h2 := uint64(hashLaneBig16(tenantID, key))
		tag := (h1 & 0x0000FFFFFFFFFFFF) | (h2 << 48)
		if tag == iEmpty {
			tag |= 1 << 48
		}
		if tag == iTombstone {
			tag ^= 1 << 48
		}
		sp = cm.hashIdx.SetTagGetPtr(tag, ptr, xPtrLaneHash, uint16(len(key)), tenantID)
	} else {
		tag := makeTagTiny(tenantID, key)
		sp = cm.tinyIdx.SetTagGetPtr(tag, ptr, xPtrLaneTiny, 0, 0)
	}

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

func (cm *CacheManager) Get(tenantID uint16, key []byte) ([]byte, bool) {
	hashLane := isHashLane(key)

	var rawVal uint64
	var found bool
	if hashLane {
		h1 := hashH1Only(tenantID, key)
		h2 := uint64(hashLaneBig16(tenantID, key))
		tag := (h1 & 0x0000FFFFFFFFFFFF) | (h2 << 48)
		if tag == iEmpty {
			tag |= 1 << 48
		}
		if tag == iTombstone {
			tag ^= 1 << 48
		}
		rawVal, found = cm.hashIdx.GetTag(tag, uint16(len(key)), tenantID)
	} else {
		rawVal, found = cm.tinyIdx.GetTag(makeTagTiny(tenantID, key), 0, 0)
	}

	if found {
		gen2b, expTrunc, typ := Unpack(rawVal)
		if typ == xValTypeSlabRAM {
			classID, tierID, physOff := UnpackSlab(rawVal)
			if int(classID) < len(cm.regions) && int(tierID) < len(cm.regions[classID]) {
				keyFP := [3]byte{}
				if hashLane {
					keyFP = makeKeyFP(key)
				}
				if val, ok := cm.regions[classID][tierID].Read(physOff, gen2b, expTrunc, keyFP); ok {
					return val, true
				}
			}
		}
	}

	val, expiry, ok := cm.backend.Get(tenantID, key)
	if !ok {
		return nil, false
	}

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

// deleteKey removes the index entry for (tenantID, key). Used to allow customers to delete from cache on the fly.
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
