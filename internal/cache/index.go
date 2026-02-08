package cache

import (
	"hash/fnv" // Used for the 64-bit signature
	"sync"
)

const (
	ShardCount     = 1024
	SlotsPerBucket = 4
	// Tombstone is a "dead" marker that keeps the linear probe chain alive.
	Tombstone  = 0xFFFFFFFFFFFFFFFF
	Empty      = 0
	LoadFactor = 0.7
)

type Shard struct {
	mu sync.RWMutex
	// Flat array of [Signature, Pointer, Signature, Pointer...]
	data       []uint64
	mask       uint64 // Used for bitwise modulo
	count      uint32 // Active items
	tombstones uint32 // Count of dead markers
}

type LookupIndex struct {
	shards [ShardCount]*Shard
}

func NewLookupIndex() *LookupIndex {
	idx := &LookupIndex{}
	for i := 0; i < ShardCount; i++ {
		// Initialize each shard with 512 slots (8KB per shard initially)
		idx.shards[i] = &Shard{
			data: make([]uint64, 512),
			mask: (512 / (SlotsPerBucket * 2)) - 1,
		}
	}
	return idx
}

// Set associates a path with a pointer using Tombstone Resurrection logic.
func (idx *LookupIndex) Set(tenantID uint16, key string, ptr uint64) {
	shardIdx := tenantID % ShardCount
	s := idx.shards[shardIdx]

	sig := hashKey(key)

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Check Load Factor (including tombstones)
	if float64(s.count+s.tombstones) > float64(len(s.data)/2)*LoadFactor {
		s.resize()
	}

	// 2. Linear Probing with 4-bucket neighborhood
	bucketBase := (sig & s.mask) * SlotsPerBucket * 2
	firstTombstone := -1

	for i := 0; i < 16; i++ { // Limit probe to 4 buckets (16 slots)
		pos := (bucketBase + uint64(i*2)) % uint64(len(s.data))
		currSig := s.data[pos]

		if currSig == sig {
			s.data[pos+1] = ptr // Update existing
			return
		}

		if currSig == Empty {
			target := pos
			if firstTombstone != -1 {
				target = uint64(firstTombstone)
				s.tombstones--
			}
			s.data[target] = sig
			s.data[target+1] = ptr
			s.count++
			return
		}

		if currSig == Tombstone && firstTombstone == -1 {
			firstTombstone = int(pos)
		}
	}

	// 3. Force Resize if neighborhood is too congested
	s.resize()
	// Recursive call will now find a clean spot
	idx.Set(tenantID, key, ptr)
}

// Get finds the pointer for a given key
func (idx *LookupIndex) Get(tenantID uint16, key string) (uint64, bool) {
	sig := hashKey(key)
	shard := idx.shards[tenantID%ShardCount]

	shard.mu.RLock()
	defer shard.mu.RUnlock()

	bucketBase := (sig & shard.mask) * SlotsPerBucket * 2
	for i := 0; i < 16; i++ { // Probe 4 buckets
		pos := (bucketBase + uint64(i*2)) % uint64(len(shard.data))
		if shard.data[pos] == sig {
			return shard.data[pos+1], true
		}
		if shard.data[pos] == Empty {
			return 0, false
		}
	}
	return 0, false
}

// Resize: Doubles capacity and purges all Tombstones
func (s *Shard) resize() {
	oldData := s.data
	newSize := len(oldData) * 2
	newData := make([]uint64, newSize)
	newMask := uint64(newSize/(SlotsPerBucket*2)) - 1

	for i := 0; i < len(oldData); i += 2 {
		sig, ptr := oldData[i], oldData[i+1]
		if sig == Empty || sig == Tombstone {
			continue
		}
		bucketIdx := (sig & newMask) * SlotsPerBucket * 2
		for j := uint64(0); ; j++ {
			pos := (bucketIdx + (j * 2)) % uint64(newSize)
			if newData[pos] == Empty {
				newData[pos], newData[pos+1] = sig, ptr
				break
			}
		}
	}
	s.data, s.mask, s.tombstones = newData, newMask, 0
}

func hashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}
