package datacache

import (
	"bytes"
	"encoding/binary"
	"log"
	"sync"
	"time"
)

// encodeOwnerKey encodes a tenantID and app name into a [48]byte owner key.
// Format:
//   - ownerKey[0:2] = tenantID as uint16 little-endian
//   - ownerKey[2:34] = app name, null-padded
//   - ownerKey[34:48] = reserved (zeros)
func encodeOwnerKey(tenantID uint16, app string) [48]byte {
	var key [48]byte
	binary.LittleEndian.PutUint16(key[0:2], tenantID)
	copy(key[2:34], app)
	return key
}

// decodeOwnerKey decodes a [48]byte owner key back into tenantID and app name.
func decodeOwnerKey(key [48]byte) (uint16, string) {
	tenantID := binary.LittleEndian.Uint16(key[0:2])
	app := string(bytes.TrimRight(key[2:34], "\x00"))
	return tenantID, app
}


// TTLCleaner manages background expiration of data in the ArenaPool.
// It scans all pool slots for expired TTLs and releases them back to the pool
// and quota registry.
type TTLCleaner struct {
	pool   *ArenaPool
	quotas *QuotaRegistry
	named  *sync.Map // collection name → *collectionEntry
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewTTLCleaner creates a new TTL cleaner.
// pool: the ArenaPool to clean
// quotas: the QuotaRegistry to update when entries are released
// named: the sync.Map storing collection names to their entries
func NewTTLCleaner(pool *ArenaPool, quotas *QuotaRegistry, named *sync.Map) *TTLCleaner {
	return &TTLCleaner{
		pool:   pool,
		quotas: quotas,
		named:  named,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start launches the background cleaner goroutine.
// It ticks every 1 second and scans all pool slots for expired entries.
func (c *TTLCleaner) Start() {
	go c.cleanerLoop()
}

// Stop signals the cleaner goroutine to exit and waits for it to finish.
func (c *TTLCleaner) Stop() {
	close(c.stopCh)
	<-c.doneCh
}

// cleanerLoop is the background goroutine that expires TTL entries.
// This implementation follows the pattern from cache_manager.go's cleanerLoop
// (lines 223-255), adapted for the ArenaPool data structure.
func (c *TTLCleaner) cleanerLoop() {
	defer close(c.doneCh)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.sweepSlots()
		}
	}
}

// sweepSlots scans all pool slots for expired entries and releases them.
func (c *TTLCleaner) sweepSlots() {
	now := time.Now().Unix()

	// Iterate through all slots in the pool.
	for slotIdx := 0; slotIdx < c.pool.maxSlots; slotIdx++ {
		slot := &c.pool.slots[slotIdx]

		// Skip slots that are not in use.
		if !slot.inUse.Load() {
			continue
		}

		// Check if the slot has a TTL and if it has expired.
		exp := slot.ttlExpUnix.Load()
		if exp == 0 || now < exp {
			// Not expired yet.
			continue
		}

		// Slot has expired: perform cleanup.

		// Decode owner key to get tenantID and app name.
		tenantID, app := decodeOwnerKey(slot.ownerKey)

		// Estimate the bytes used by this slot.
		// Use arena capacity × (estimated record size), or just arena.Cap for total.
		var byteCount int64
		if c.pool.slots[slotIdx].arena.Schema.BufSize > 0 {
			byteCount = int64(c.pool.slots[slotIdx].arena.Schema.BufSize)
		} else {
			byteCount = 0
		}

		// Release the quota.
		c.quotas.Release(tenantID, app, byteCount, 1)

		// Decode collection name from slot and delete from named map.
		collectionName := string(bytes.TrimRight(slot.name[:], "\x00"))
		if len(collectionName) > 0 {
			c.named.Delete(collectionName)
		}

		// Release the slot back to the pool.
		c.pool.Release(uint8(slotIdx))

		// Log the expiry event.
		log.Printf("datacache: TTL evicted collection=%q slot=%d tenantID=%d app=%s", collectionName, slotIdx, tenantID, app)
	}
}
