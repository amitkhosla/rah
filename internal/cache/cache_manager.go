package cache

import (
	"rah/internal/config"
	"sync"
	"sync/atomic"
	"unsafe"
)

type CacheManager struct {
	SlabRegistry [64]*Slab
	SlabMap      map[uint64]uint8
	cfg          config.GlobalLayout
	currentUsage uint64
	nextSlabID   uint32
	Index        *LookupIndex
	mu           sync.RWMutex
}

func NewCacheManager(cfg config.GlobalLayout) *CacheManager {
	return &CacheManager{
		SlabMap: make(map[uint64]uint8),
		cfg:     cfg,
		Index:   NewLookupIndex(),
	}
}

func (sm *CacheManager) Put(tenantID uint16, keyBytes []byte, data []byte, ttl uint32) SmartPointer {
	key := unsafe.String(unsafe.SliceData(keyBytes), len(keyBytes))
	size := uint32(len(data))
	isTiny := size <= 14

	// 1. Slab Selection
	classKey := uint64(size)<<32 | uint64(ttl)
	if isTiny {
		classKey = 0xFFFFFFFF | uint64(ttl)
	}

	sm.mu.Lock()
	slabID, exists := sm.SlabMap[classKey]
	if !exists {
		slabID = uint8(atomic.AddUint32(&sm.nextSlabID, 1) - 1)
		sm.SlabMap[classKey] = slabID
		// Logic to determine initial size (default to 2MB for now)
		sm.SlabRegistry[slabID] = NewSlab(slabID, 2*1024*1024, ttl, isTiny)
	}
	slab := sm.SlabRegistry[slabID]
	sm.mu.Unlock()

	// 2. Physical Write
	segID, version, offset := slab.Push(tenantID, key, data)

	// 3. Fix: Cast tag to uint8 and Pack [cite: 3]
	var tag uint8 = TagSlabRAM
	if isTiny {
		tag = TagTiny
	}

	ptr := PackPointer(tag, slab.ID, version, segID, size, offset)

	// 4. Update Index
	sm.Index.Set(tenantID, key, uint64(ptr))

	return ptr
}

func (sm *CacheManager) Get(tenantID uint16, key []byte) ([]byte, bool) {
	keyStr := unsafe.String(unsafe.SliceData(key), len(key))

	rawPtr, found := sm.Index.Get(tenantID, keyStr)
	if !found {
		return nil, false
	}

	ptr := SmartPointer(rawPtr)

	// Use the functional helper from cache_types.go
	sID := GetSlabID(ptr)
	slab := sm.SlabRegistry[sID]
	if slab == nil {
		return nil, false
	}

	return slab.Get(ptr, keyStr, tenantID)
}
