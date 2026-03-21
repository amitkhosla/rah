package smartcache

import (
	"errors"
	"sync/atomic"
)

const (
	Lane1Slots   = 4
	Lane1Buckets = 8
	// SecondaryFlag remains 1 << 63 on the ValuePtr
)

type Slot1 struct {
	// 16 Bytes total per slot.
	// This allows 4 slots to fit into a single 64-byte CPU Cache Line.
	KeyID    uint64 // Packed: [6 bytes Key][2 bytes TenantID]
	ValuePtr uint64 // Actual data pointer OR Secondary area address
}

type Lane1 struct {
	// Total size: 128 bytes. Exactly 2 Cache Lines.
	Buckets [Lane1Buckets][Lane1Slots]Slot1
}

// Get retrieves a value for a key between 1-6 bytes.
func (l *Lane1) Get(tenantID uint16, key []byte) (uint64, bool) {
	ln := len(key)
	if ln < 1 || ln > 6 {
		return 0, false
	}

	// 1. Pack Key and TenantID into a single uint64
	// Logic: [TenantID (16b)][Key (48b)]
	searchID := pack6(key, tenantID)

	// 2. Calculate Bucket (Simple Masking)
	// We use the last 3 bits of the packed ID for near-zero cost routing.
	bIdx := searchID & (Lane1Buckets - 1)
	bucket := &l.Buckets[bIdx]

	// 3. Scan Primary Slots (0-2)
	// This loop is so small the compiler will likely unroll it.
	for i := 0; i < 3; i++ {
		slot := &bucket[i]
		sid := atomic.LoadUint64(&slot.KeyID)

		if sid == searchID {
			return atomic.LoadUint64(&slot.ValuePtr), true
		}
		if sid == 0 {
			return 0, false // Empty slot: key definitely doesn't exist
		}
	}

	// 4. Check Gatekeeper (Slot 3)
	gate := &bucket[3]
	gid := atomic.LoadUint64(&gate.KeyID)
	gPtr := atomic.LoadUint64(&gate.ValuePtr)

	if gid == searchID {
		return gPtr, true
	}

	// 5. Overflow Trigger
	if gPtr&SecondaryFlag != 0 {
		return gPtr, true // Pass to Common Secondary Access
	}

	return 0, false
}

// Set inserts a 1-6 byte key.
func (l *Lane1) Set(tenantID uint16, key []byte, value uint64) error {
	if len(key) < 1 || len(key) > 6 {
		return errors.New("key size out of range for Lane 1")
	}

	searchID := pack6(key, tenantID)
	bIdx := searchID & (Lane1Buckets - 1)
	bucket := &l.Buckets[bIdx]

	var emptySlot *Slot1
	for i := 0; i < 3; i++ {
		slot := &bucket[i]
		sid := atomic.LoadUint64(&slot.KeyID)
		if sid == searchID {
			atomic.StoreUint64(&slot.ValuePtr, value)
			return nil
		}
		if emptySlot == nil && sid == 0 {
			emptySlot = slot
		}
	}

	if emptySlot != nil {
		atomic.StoreUint64(&emptySlot.KeyID, searchID)
		atomic.StoreUint64(&emptySlot.ValuePtr, value)
		return nil
	}

	// Handle Gatekeeper
	gate := &bucket[3]
	if atomic.LoadUint64(&gate.KeyID) == searchID {
		atomic.StoreUint64(&gate.ValuePtr, value)
		return nil
	}

	if atomic.LoadUint64(&gate.KeyID) == 0 && atomic.LoadUint64(&gate.ValuePtr) == 0 {
		atomic.StoreUint64(&gate.KeyID, searchID)
		atomic.StoreUint64(&gate.ValuePtr, value)
		return nil
	}

	return errors.New("lane 1 bucket full")
}

// Delete removes a 1-6 byte key.
func (l *Lane1) Delete(tenantID uint16, key []byte) {
	searchID := pack6(key, tenantID)
	bIdx := searchID & (Lane1Buckets - 1)
	bucket := &l.Buckets[bIdx]

	for i := 0; i < 4; i++ {
		slot := &bucket[i]
		if atomic.LoadUint64(&slot.KeyID) == searchID {
			atomic.StoreUint64(&slot.KeyID, 0)
			atomic.StoreUint64(&slot.ValuePtr, 0)
			return
		}
	}
}

// pack6 creates a single uint64 from 6 bytes of key and 2 bytes of TenantID.
func pack6(key []byte, tID uint16) uint64 {
	var res uint64
	// Place TenantID in the highest 16 bits
	res = uint64(tID) << 48

	// Place Key in the lower 48 bits
	// This loop is unrolled for 1-6 iterations
	for i := 0; i < len(key); i++ {
		res |= uint64(key[i]) << (i * 8)
	}
	return res
}
