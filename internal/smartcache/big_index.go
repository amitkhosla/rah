package smartcache

import (
	"sync"
	"sync/atomic"
)

// BigIndex handles large keys (>16 bytes) across a global sharded memory space.
type BigIndex struct {
	shards [16]*bigShard
}

type bigShard struct {
	mu   sync.RWMutex
	data []uint64
	mask uint32
}

func NewBigIndex(totalSlots uint32) *BigIndex {
	bi := &BigIndex{}
	slotsPerShard := totalSlots / 16
	for i := 0; i < 16; i++ {
		bi.shards[i] = &bigShard{
			data: make([]uint64, slotsPerShard),
			mask: slotsPerShard - 1,
		}
	}
	return bi
}

func (b *BigIndex) Get(tenantID uint16, key []byte) (uint64, bool) {
	shardID := tenantID & 0x0F
	shard := b.shards[shardID]

	h := b.stitchHash(tenantID, key)
	idx := h & shard.mask

	// Lockless read path
	ptr := atomic.LoadUint64(&shard.data[idx])
	if ptr == 0 {
		return 0, false
	}
	return ptr, true
}

func (b *BigIndex) Set(tenantID uint16, key []byte, pointer uint64) bool {
	shardID := tenantID % 16
	shard := b.shards[shardID]

	h := b.stitchHash(tenantID, key)
	idx := h & shard.mask

	shard.mu.Lock()
	defer shard.mu.Unlock()

	atomic.StoreUint64(&shard.data[idx], pointer)
	return true
}

func (b *BigIndex) Delete(tenantID uint16, key []byte) bool {
	shardID := tenantID % 16
	shard := b.shards[shardID]

	h := b.stitchHash(tenantID, key)
	idx := h & shard.mask

	shard.mu.Lock()
	shard.data[idx] = 0
	shard.mu.Unlock()
	return true
}

// stitchHash performs the 8-point sampling XOR-fold.
func (b *BigIndex) stitchHash(tID uint16, key []byte) uint32 {
	// Sample key at 8 points, XOR with tID, return 32-bit hash.
	return uint32(len(key)) ^ uint32(tID) // Simplified for structure
}
