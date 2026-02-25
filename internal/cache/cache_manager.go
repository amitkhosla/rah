package cache

import (
	"errors"
	"sync"
	"sync/atomic"
)

/*
CacheManager is a high-performance, bounded, multi-tenant in-memory cache.

Design Principles:
------------------
1. Deterministic memory usage (preallocated regions).
2. No dynamic slab creation.
3. No resizing under load.
4. Lock-free reads (index).
5. Shard-level locking for writes only.
6. 128-bit fingerprint for collision resistance.
7. Per-tenant quota enforcement.
8. Circular region per SizeClass × TTLTier.

Memory Layout:
--------------
TotalMemory is divided across:
    len(SizeClasses) × len(TTLTiers)

Each combination gets one Region (circular buffer).

Index:
------
Sharded open-addressed hash table.
Lower 64 bits of fingerprint used for shard routing.

Safety:
-------
Each entry validates:
    - Generation
    - Expiry
    - TenantID
    - 128-bit fingerprint
*/

type CacheManager struct {
	// Configured at startup (immutable after init)
	totalMemory uint64
	sizeClasses []uint32
	ttlTiers    []uint32

	// 2D region matrix: [sizeClass][ttlTier]
	regions [][]*Region

	// Lock-free read index
	index *LookupIndex

	// Per-tenant quota configuration
	tenantLimit uint64 // max bytes per tenant

	// tenantID -> *tenantCounter
	tenantUsage sync.Map

	// Stats
	globalUsed atomic.Uint64
}

/*
tenantCounter tracks memory usage per tenant.

Atomic ensures no race during concurrent writes.
*/
type tenantCounter struct {
	used atomic.Uint64
}

/*
NewCacheManager initializes the cache with fixed memory layout.

All memory is preallocated here.
No allocation happens during steady-state operation.
*/
func NewCacheManager(
	totalMemory uint64,
	sizeClasses []uint32,
	ttlTiers []uint32,
	expectedEntries uint64,
	tenantLimit uint64,
) (*CacheManager, error) {

	if len(sizeClasses) == 0 || len(ttlTiers) == 0 {
		return nil, errors.New("sizeClasses and ttlTiers must not be empty")
	}

	cm := &CacheManager{
		totalMemory: totalMemory,
		sizeClasses: sizeClasses,
		ttlTiers:    ttlTiers,
		index:       NewLookupIndex(expectedEntries),
		tenantLimit: tenantLimit,
	}

	cm.allocateRegions()

	return cm, nil
}

/*
allocateRegions preallocates memory evenly across
SizeClass × TTLTier combinations.

This ensures fully bounded memory.
*/
func (cm *CacheManager) allocateRegions() {

	classCount := len(cm.sizeClasses)
	tierCount := len(cm.ttlTiers)

	totalRegions := classCount * tierCount
	regionMemory := cm.totalMemory / uint64(totalRegions)

	cm.regions = make([][]*Region, classCount)

	for i := 0; i < classCount; i++ {
		cm.regions[i] = make([]*Region, tierCount)

		for j := 0; j < tierCount; j++ {
			cm.regions[i][j] = NewRegion(regionMemory, cm.ttlTiers[j])
		}
	}
}

/*
selectSizeClass returns the smallest size class that can hold valueLen.

O(N) over small slice (usually <= 8 classes).
*/
func (cm *CacheManager) selectSizeClass(valueLen int) int {
	for i, size := range cm.sizeClasses {
		if uint32(valueLen) <= size {
			return i
		}
	}
	return len(cm.sizeClasses) - 1
}

/*
selectTTLTier returns the smallest tier >= requested TTL.
*/
func (cm *CacheManager) selectTTLTier(ttl uint32) int {
	for i, tier := range cm.ttlTiers {
		if ttl <= tier {
			return i
		}
	}
	return len(cm.ttlTiers) - 1
}

/*
getTenantCounter returns (or creates) atomic usage counter for tenant.
*/
func (cm *CacheManager) getTenantCounter(tenantID uint16) *tenantCounter {
	val, _ := cm.tenantUsage.LoadOrStore(tenantID, &tenantCounter{})
	return val.(*tenantCounter)
}

/*
Put inserts value into cache.

Flow:
1. Compute 128-bit fingerprint.
2. Enforce tenant quota.
3. Select size class & TTL tier.
4. Attempt write into region.
5. Insert pointer into index.

If region is full or quota exceeded, caller may fallback to external store.
*/
func (cm *CacheManager) Put(
	tenantID uint16,
	key []byte,
	value []byte,
	ttl uint32,
) (SmartPointer, bool) {

	fingerprint := Hash128(tenantID, key)

	entrySize := uint64(32 + len(value))

	// Tenant quota check
	counter := cm.getTenantCounter(tenantID)
	current := counter.used.Load()

	if cm.tenantLimit > 0 && current+entrySize > cm.tenantLimit {
		return 0, false
	}

	classID := cm.selectSizeClass(len(value))
	tierID := cm.selectTTLTier(ttl)

	region := cm.regions[classID][tierID]

	offset, generation, oldFP, hasOld, ok := region.Write(tenantID, fingerprint, value)
	if !ok {
		return 0, false
	}

	// Remove overwritten expired entry from index and accounting.
	// We decrement counters only if old fingerprint was still indexed.
	if hasOld && cm.index.Delete(oldFP.fingerprint) {
		oldEntrySize := uint64(32 + oldFP.valueLen)
		cm.subUsage(oldFP.tenantID, oldEntrySize)
	}

	ptr := PackPointer(TagSlabRAM, uint8(classID), uint8(tierID), generation, offset)

	// Insert into index
	if !cm.index.Set(fingerprint, uint64(ptr)) {
		return 0, false
	}

	// Update accounting
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

/*
Get retrieves value from cache.

Lock-free read path:
- Compute fingerprint.
- Index lookup.
- Region read validation.
*/
func (cm *CacheManager) Get(
	tenantID uint16,
	key []byte,
) ([]byte, bool) {

	fingerprint := Hash128(tenantID, key)

	rawPtr, ok := cm.index.Get(fingerprint)
	if !ok {
		return nil, false
	}

	ptr := SmartPointer(rawPtr)

	tag, classID, tierID, generation, offset := Unpack(ptr)

	if tag != TagSlabRAM {
		return nil, false
	}

	region := cm.regions[classID][tierID]

	return region.Read(offset, generation, tenantID, fingerprint)
}

/*
Stats returns current memory usage metrics.
*/
func (cm *CacheManager) Stats() (globalUsed uint64) {
	return cm.globalUsed.Load()
}
