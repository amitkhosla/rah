package smartcache

import (
	"errors"
	"sync/atomic"
)

const (
	Lane2Slots      = 4
	Lane2Buckets    = 8
	SecondaryFlag   = 1 << 63 // MSB of ValuePtr indicates overflow address
	SqueezeMask     = 0x7F    // 7 bits
)

type Slot2 struct {
	K1  uint64 // First 9 bytes (squeezed)
	K2  uint64 // Next 9 bytes (squeezed) ^ TenantID
	Ptr uint64 // Value pointer or Secondary block address
}

type Lane2 struct {
	// 8 buckets, each with 4 slots. Total 192 bytes.
	// Aligns perfectly with 3 CPU cache lines (64 bytes each).
	Buckets [Lane2Buckets][Lane2Slots]Slot2
}

// Get retrieves a value for a key between 7-18 bytes.
func (l *Lane2) Get(tenantID uint16, key []byte) (uint64, bool) {
	if len(key) < 7 || len(key) > 18 {
		return 0, false
	}

	// 1. Calculate Bucket (Folded Hash)
	bIdx := fold18(key, tenantID) & (Lane2Buckets - 1)
	bucket := &l.Buckets[bIdx]

	// 2. Prepare Squeezed Registers
	sk1, sk2 := squeeze18(key, tenantID)

	// 3. Scan Primary Slots (0-2)
	for i := 0; i < 3; i++ {
		slot := &bucket[i]
		s1 := atomic.LoadUint64(&slot.K1)
		s2 := atomic.LoadUint64(&slot.K2)

		if s1 == sk1 && s2 == sk2 {
			return atomic.LoadUint64(&slot.Ptr), true
		}
		if s1 == 0 {
			return 0, false // Fast path: slot is empty, key doesn't exist
		}
	}

	// 4. Check Gatekeeper (Slot 3)
	gate := &bucket[3]
	g1 := atomic.LoadUint64(&gate.K1)
	g2 := atomic.LoadUint64(&gate.K2)
	gPtr := atomic.LoadUint64(&gate.Ptr)

	if g1 == sk1 && g2 == sk2 {
		return gPtr, true
	}

	// 5. Overflow Trigger
	if gPtr&SecondaryFlag != 0 {
		// Hand-off logic for Common Secondary Access goes here
		// return l.resolveSecondary(gPtr, key)
		return gPtr, true 
	}

	return 0, false
}

// Set inserts or updates a key. Returns error if Lane 2 is full (requires promotion).
func (l *Lane2) Set(tenantID uint16, key []byte, value uint64) error {
	if len(key) < 7 || len(key) > 18 {
		return errors.New("key size out of range for Lane 2")
	}

	bIdx := fold18(key, tenantID) & (Lane2Buckets - 1)
	bucket := &l.Buckets[bIdx]
	sk1, sk2 := squeeze18(key, tenantID)

	// Update existing or find empty slot
	var emptySlot *Slot2
	for i := 0; i < 3; i++ {
		slot := &bucket[i]
		if atomic.LoadUint64(&slot.K1) == sk1 && atomic.LoadUint64(&slot.K2) == sk2 {
			atomic.StoreUint64(&slot.Ptr, value)
			return nil
		}
		if emptySlot == nil && atomic.LoadUint64(&slot.K1) == 0 {
			emptySlot = slot
		}
	}

	if emptySlot != nil {
		atomic.StoreUint64(&emptySlot.K1, sk1)
		atomic.StoreUint64(&emptySlot.K2, sk2)
		atomic.StoreUint64(&emptySlot.Ptr, value)
		return nil
	}

	// Handle Gatekeeper (Slot 3)
	gate := &bucket[3]
	if atomic.LoadUint64(&gate.K1) == sk1 && atomic.LoadUint64(&gate.K2) == sk2 {
		atomic.StoreUint64(&gate.Ptr, value)
		return nil
	}
    
	// If gatekeeper is empty, use it for data. 
	// If it contains a SecondaryFlag, this Set must be handled by Secondary Access.
	if atomic.LoadUint64(&gate.K1) == 0 && atomic.LoadUint64(&gate.Ptr) == 0 {
		atomic.StoreUint64(&gate.K1, sk1)
		atomic.StoreUint64(&gate.K2, sk2)
		atomic.StoreUint64(&gate.Ptr, value)
		return nil
	}

	return errors.New("lane 2 bucket full: promotion to secondary required")
}

// Delete removes a key by zeroing its entry.
func (l *Lane2) Delete(tenantID uint16, key []byte) {
	bIdx := fold18(key, tenantID) & (Lane2Buckets - 1)
	bucket := &l.Buckets[bIdx]
	sk1, sk2 := squeeze18(key, tenantID)

	for i := 0; i < 4; i++ {
		slot := &bucket[i]
		if atomic.LoadUint64(&slot.K1) == sk1 && atomic.LoadUint64(&slot.K2) == sk2 {
			atomic.StoreUint64(&slot.K1, 0)
			atomic.StoreUint64(&slot.K2, 0)
			atomic.StoreUint64(&slot.Ptr, 0)
			return
		}
	}
}

// --- Internal Helpers ---



func squeeze18(key []byte, tID uint16) (uint64, uint64) {
	var k1, k2 uint64
	// Squeeze first 9 bytes (1-9)
	for i := 0; i < 9 && i < len(key); i++ {
		k1 |= uint64(key[i]&SqueezeMask) << (i * 7)
	}
	// Squeeze next 9 bytes (10-18)
	for i := 0; i < 9 && i+9 < len(key); i++ {
		k2 |= uint64(key[i+9]&SqueezeMask) << (i * 7)
	}
	// Inject TenantID isolation
	k2 ^= uint64(tID) << 48 
	return k1, k2
}

func fold18(key []byte, tID uint16) uint32 {
	// Simple high-entropy fold for bucket selection
	l := len(key)
	h := uint32(key[0]) ^ uint32(key[l-1])<<8 ^ uint32(tID)
	return h ^ (h >> 4)
}