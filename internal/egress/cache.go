package egress

import "sync"

// egressEntry is exactly 16 bytes with no pointer fields.
// The GC never traces individual entries — the slots slice is opaque to GC.
type egressEntry struct {
	hash       uint64 // fnv64a of key string; 0 = empty slot
	offset     uint32 // byte offset of key string data in arena (past the 2-byte length header)
	length     uint16 // key string byte length
	pid        uint8  // profileID: 0=empty slot, 255=no match found
	patternIdx uint8  // resolving pattern index; 255 = exact/code match or not set
}

// egressCache is a concurrent open-addressing hash table mapping strings to
// uint8 profileIDs. All string data lives in the embedded arenaPool.
// Both the slots array and the arena contain no GC-traced pointers on the
// hot read path — the GC sees only two slice headers regardless of entry count.
type egressCache struct {
	mu    sync.RWMutex
	arena arenaPool
	slots []egressEntry // length is always a power of two
	mask  uint64        // len(slots) - 1
	count int
}

// fnv64a computes the FNV-1a 64-bit hash of s.
// Returns 1 if the natural result is 0 (0 is reserved for empty slots).
func fnv64a(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	if h == 0 {
		return 1
	}
	return h
}

// newEgressCache creates a cache with at least initialCap slots rounded up to
// the next power of two. Minimum allocation is 64 slots.
func newEgressCache(initialCap int) *egressCache {
	n := 64
	for n < initialCap {
		n <<= 1
	}
	return &egressCache{
		slots: make([]egressEntry, n),
		mask:  uint64(n - 1),
	}
}

// matchSlot reports whether the key stored in e equals s.
// e.offset points directly at the string bytes (past the 2-byte length header).
func (c *egressCache) matchSlot(e egressEntry, s string) bool {
	if e.length != uint16(len(s)) {
		return false
	}
	end := e.offset + uint32(e.length)
	if end > uint32(len(c.arena.data)) {
		return false
	}
	return string(c.arena.data[e.offset:end]) == s
}

// get returns the profileID stored for s. found is false on a cache miss.
// Safe for concurrent use.
func (c *egressCache) get(s string) (pid uint8, found bool) {
	h := fnv64a(s)
	c.mu.RLock()
	idx := h & c.mask
	for {
		e := c.slots[idx]
		if e.hash == 0 {
			c.mu.RUnlock()
			return 0, false
		}
		if e.hash == h && c.matchSlot(e, s) {
			pid = e.pid
			c.mu.RUnlock()
			return pid, true
		}
		idx = (idx + 1) & c.mask
	}
}

// put stores the (s → pid) mapping.
// patternIdx records which pattern rule resolved this entry; use 255 for
// exact/code matches or entries not resolved via pattern matching.
// Safe for concurrent use.
func (c *egressCache) put(s string, pid uint8, patternIdx uint8) {
	h := fnv64a(s)
	c.mu.Lock()
	// Grow before inserting when load factor exceeds 70%.
	if (c.count+1)*10 > len(c.slots)*7 {
		c.grow()
	}
	idx := h & c.mask
	for {
		e := &c.slots[idx]
		if e.hash == 0 {
			// Empty slot — write string to arena, fill slot.
			// arena.Write returns (off, length) where off points at the length header.
			// Store off+2 so matchSlot can index directly into the string bytes.
			off, length := c.arena.Write(s)
			e.hash = h
			e.offset = off + 2
			e.length = length
			e.pid = pid
			e.patternIdx = patternIdx
			c.count++
			c.mu.Unlock()
			return
		}
		if e.hash == h && c.matchSlot(*e, s) {
			// Existing entry — update resolved values only.
			e.pid = pid
			e.patternIdx = patternIdx
			c.mu.Unlock()
			return
		}
		idx = (idx + 1) & c.mask
	}
}

// invalidatePattern zeros the pid of all entries resolved via patternIdx so
// they will be re-evaluated on the next get() miss. This is a cold-path
// operation called only when pattern rules change.
func (c *egressCache) invalidatePattern(patternIdx uint8) {
	c.mu.Lock()
	for i := range c.slots {
		if c.slots[i].hash != 0 && c.slots[i].patternIdx == patternIdx {
			c.slots[i].pid = 0
			c.slots[i].patternIdx = 255
		}
	}
	c.mu.Unlock()
}

// grow doubles the slot table and rehashes all existing entries.
// Must be called with c.mu held (write lock). Arena data is unchanged —
// all offsets remain valid after rehashing.
func (c *egressCache) grow() {
	newCap := len(c.slots) * 2
	if newCap == 0 {
		newCap = 64
	}
	newSlots := make([]egressEntry, newCap)
	newMask := uint64(newCap - 1)
	for _, e := range c.slots {
		if e.hash == 0 {
			continue
		}
		idx := e.hash & newMask
		for newSlots[idx].hash != 0 {
			idx = (idx + 1) & newMask
		}
		newSlots[idx] = e
	}
	c.slots = newSlots
	c.mask = newMask
}

// reset clears all entries and releases arena memory back to the pool.
// Safe for concurrent use.
func (c *egressCache) reset() {
	c.mu.Lock()
	c.slots = make([]egressEntry, 64)
	c.mask = 63
	c.count = 0
	c.arena.Release()
	c.mu.Unlock()
}
