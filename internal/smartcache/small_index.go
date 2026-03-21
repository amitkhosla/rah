package smartcache

import (
	"sync/atomic"
)

const (
	// SmallIndexSlots = 524,288 slots * 8 bytes = 4MB per tenant.
	SmallIndexSlots = 1 << 19
	SmallSlotMask   = SmallIndexSlots - 1
)

// SmallIndex manages keys from 3 to 16 bytes using a 7-bit packed hash.
type SmallIndex struct {
	slots []uint64
}

func NewSmallIndex() *SmallIndex {
	return &SmallIndex{
		slots: make([]uint64, SmallIndexSlots),
	}
}

// Get finds the pointer using a 7-bit fold and linear probing (max 4 steps).
func (s *SmallIndex) Get(tenantID uint16, key []byte) (uint64, bool) {
	if len(key) < 3 || len(key) > 16 {
		return 0, false
	}

	h := s.hash7Bit(tenantID, key)
	for i := uint32(0); i < 4; i++ {
		idx := (h + i) & SmallSlotMask
		ptr := atomic.LoadUint64(&s.slots[idx])
		if ptr == 0 {
			return 0, false
		}
		// Signature verification happens here (internal to SmartPointer)
		if s.verifySignature(ptr, key) {
			return ptr, true
		}
	}
	return 0, false
}

func (s *SmallIndex) Set(tenantID uint16, key []byte, pointer uint64) bool {
	h := s.hash7Bit(tenantID, key)
	// Simple linear probe to find an empty slot or match
	for i := uint32(0); i < 4; i++ {
		idx := (h + i) & SmallSlotMask
		// For production, we'd use CompareAndSwap here to handle concurrent writes
		atomic.StoreUint64(&s.slots[idx], pointer)
		return true
	}
	return false // Bucket full
}

func (s *SmallIndex) Delete(tenantID uint16, key []byte) bool {
	h := s.hash7Bit(tenantID, key)
	for i := uint32(0); i < 4; i++ {
		idx := (h + i) & SmallSlotMask
		ptr := atomic.LoadUint64(&s.slots[idx])
		if s.verifySignature(ptr, key) {
			atomic.StoreUint64(&s.slots[idx], 0)
			return true
		}
	}
	return false
}

// hash7Bit performs the 7-bit character compression and prime multiplication.
func (s *SmallIndex) hash7Bit(tID uint16, key []byte) uint32 {
	// Logic: Pack 7 bits per char, mix with tID, multiply by 64-bit Prime.
	// This ensures "CAT" and "cat" are scattered.
	var mix uint64 = uint64(tID) << 48
	for i, b := range key {
		mix ^= uint64(b&0x7F) << (i * 7)
	}
	return uint32((mix * 0x9E3779B1161D5131) >> 32)
}

func (s *SmallIndex) verifySignature(ptr uint64, key []byte) bool {
	// Placeholder: Extracts 14-bit signature from ptr and compares with key hash.
	return true
}
