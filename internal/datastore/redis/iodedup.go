package redis

import (
	"bytes"
	"context"
	"sync"
	"time"
)

const dedupShards = 16

// ioEntry is a single cached key/value pair inside the dedup layer.
// redisTTL mirrors the TTL that was used when the value was written to Redis;
// it is used by ShouldSkipPut to detect TTL-only changes.
type ioEntry struct {
	val      []byte
	redisTTL time.Duration
}

// ioShard holds two consecutive generations of the dedup table for one shard.
// Reads check current first, then prev; writes go to current only.
// rotate() advances both pointers — the old current becomes prev, and a fresh
// empty map becomes the new current. The old prev is simply dropped and GC'd.
type ioShard struct {
	mu      sync.RWMutex
	current map[string]ioEntry
	prev    map[string]ioEntry // nil until the first rotation
}

// IODeduper is a 16-shard, generational in-process I/O deduplication layer
// for Redis/Dragonfly stores.
//
// # What it does
//
//   - Read deduplication: Get returns a cached value for up to 2×TTL after the
//     key was last fetched from Redis, absorbing thundering-herd bursts where
//     many goroutines read the same key concurrently.
//   - Write deduplication: ShouldSkipWrite returns true when a Put/PutWithTTL
//     would write an identical value (same bytes, same TTL) that is already
//     known to Redis, saving a round-trip.
//
// # What it does NOT do
//
//   - INCR, TryLock, Unlock, and ZSet mutations always bypass this layer.
//   - Delete always invalidates the cached entry and always reaches Redis.
//
// # GC characteristics
//
// Each shard holds at most (unique keys accessed in one TTL window) entries.
// Generational rotation GCs the prev map once per TTL cycle. For typical
// workloads (≤10 k unique keys, TTL 200–500 ms) the live set per shard is
// small (<1 k entries) — GC scanning costs single-digit microseconds.
type IODeduper struct {
	shards [dedupShards]ioShard
	ttl    time.Duration
}

// newIODeduper creates an IODeduper with the given TTL.
// Call StartRotation to start the background goroutine that retires old entries.
func newIODeduper(ttl time.Duration) *IODeduper {
	d := &IODeduper{ttl: ttl}
	for i := range d.shards {
		d.shards[i].current = make(map[string]ioEntry)
	}
	return d
}

// StartRotation launches a background goroutine that rotates all shard
// generations every TTL. Cancel ctx to stop it cleanly.
func (d *IODeduper) StartRotation(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(d.ttl)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.rotate()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// rotate advances every shard: prev ← current, current ← fresh empty map.
// The old prev is dropped and becomes eligible for GC.
func (d *IODeduper) rotate() {
	for i := range d.shards {
		sh := &d.shards[i]
		sh.mu.Lock()
		sh.prev = sh.current
		sh.current = make(map[string]ioEntry)
		sh.mu.Unlock()
	}
}

// shardFor returns the shard responsible for key using FNV-1a over 4 bits.
func (d *IODeduper) shardFor(key string) *ioShard {
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return &d.shards[h&(dedupShards-1)]
}

// Get returns the cached value for key if it exists in either generation.
// Returns (nil, false) on a miss.
func (d *IODeduper) Get(key string) ([]byte, bool) {
	sh := d.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	if e, ok := sh.current[key]; ok {
		return e.val, true
	}
	if sh.prev != nil {
		if e, ok := sh.prev[key]; ok {
			return e.val, true
		}
	}
	return nil, false
}

// Store writes key → value into the current generation.
// redisTTL is the TTL that was (or will be) used for the Redis write; pass 0
// for no-expiry puts.
func (d *IODeduper) Store(key string, val []byte, redisTTL time.Duration) {
	sh := d.shardFor(key)
	sh.mu.Lock()
	sh.current[key] = ioEntry{val: val, redisTTL: redisTTL}
	sh.mu.Unlock()
}

// ShouldSkipWrite returns true when an existing dedup entry already has the
// same bytes and the same redisTTL as the incoming write — meaning the value
// in Redis is already correct and the write can be elided.
func (d *IODeduper) ShouldSkipWrite(key string, newVal []byte, redisTTL time.Duration) bool {
	sh := d.shardFor(key)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	if e, ok := sh.current[key]; ok {
		return e.redisTTL == redisTTL && bytes.Equal(e.val, newVal)
	}
	if sh.prev != nil {
		if e, ok := sh.prev[key]; ok {
			return e.redisTTL == redisTTL && bytes.Equal(e.val, newVal)
		}
	}
	return false
}

// Invalidate removes key from both generations (called on Delete so stale
// values are never returned after a deletion).
func (d *IODeduper) Invalidate(key string) {
	sh := d.shardFor(key)
	sh.mu.Lock()
	delete(sh.current, key)
	if sh.prev != nil {
		delete(sh.prev, key)
	}
	sh.mu.Unlock()
}
