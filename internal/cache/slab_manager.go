package cache

import (
	"encoding/binary"
	"rah/internal/config"
	"sync"
	"sync/atomic"
	"time"
)

type SlabManager struct {
	SlabRegistry [64]*Slab
	SlabMap      map[uint64]uint8
	cfg          config.GlobalLayout
	currentUsage uint64
	nextSlabID   uint32
	mu           sync.RWMutex
}

func NewSlabManager(cfg config.GlobalLayout) *SlabManager {
	return &SlabManager{
		SlabMap: make(map[uint64]uint8),
		cfg:     cfg,
	}
}

func (sm *SlabManager) Put(tenantID uint32, size uint32, ttl uint32, data []byte) SmartPointer {
	// Check if it qualifies for Tiny Slab (Fixed slots)
	isTiny := size <= 14

	// Key includes size-class, TTL, and type
	key := uint64(size)<<32 | uint64(ttl)
	if isTiny {
		key = 0xFFFFFFFF | uint64(ttl)
	}

	sm.mu.Lock()
	slabID, exists := sm.SlabMap[key]
	if !exists {
		slabID = uint8(atomic.AddUint32(&sm.nextSlabID, 1) - 1)
		sm.SlabMap[key] = slabID
		initialSize := sm.calculateInitialSize(size)
		sm.SlabRegistry[slabID] = NewSlab(slabID, initialSize, ttl, isTiny)
		atomic.AddUint64(&sm.currentUsage, uint64(initialSize))
	}
	slab := sm.SlabRegistry[slabID]
	sm.mu.Unlock()

	// Handle Space/Growth
	if !slab.IsSpaceAvailable(size) {
		if slab.IsOldestExpired() {
			slab.ResetToZero()
		} else {
			nextSize := uint32(len(slab.Segments[slab.CurrentSegID].Data)) * 2
			if atomic.LoadUint64(&sm.currentUsage)+uint64(nextSize) <= uint64(sm.cfg.MaxHeapBytes) {
				slab.Grow()
				atomic.AddUint64(&sm.currentUsage, uint64(nextSize))
			}
		}
	}

	segId, ver, offset := slab.Push(tenantID, data)
	tag := uint8(TagSlabRAM)
	if isTiny {
		tag = TagTiny
	}

	return PackPointer(tag, slab.ID, ver, segId, uint32(len(data)), offset)
}

func (sm *SlabManager) Get(ptr SmartPointer, expectedTenant uint32) ([]byte, bool) {
	tag, slabID, ver, segID, dLen, offset := Unpack(ptr)

	if tag != TagSlabRAM && tag != TagTiny {
		return nil, false
	}

	slab := sm.SlabRegistry[slabID]
	if slab == nil {
		return nil, false
	}
	seg := slab.Segments[segID]

	// Triple Validation
	if seg.Data[offset] != MagicByte || (seg.Data[offset+1]&0xF) != ver {
		return nil, false
	}
	
	storedTenant := binary.LittleEndian.Uint32(seg.Data[offset+4:])
	if storedTenant != expectedTenant {
		return nil, false
	}

	// Expiry Check
	expiry := binary.LittleEndian.Uint32(seg.Data[offset+8:])
	if uint32(time.Now().Unix()) > expiry {
		return nil, false
	}

	headerSize := uint32(12)
	if tag == TagTiny {
		headerSize = 10
	}

	return seg.Data[offset+headerSize : offset+headerSize+dLen], true
}

func (sm *SlabManager) calculateInitialSize(itemSize uint32) uint32 {
	if itemSize < 1024 {
		return 128 * 1024
	} // 128KB
	return 2 * 1024 * 1024 // 2MB
}
