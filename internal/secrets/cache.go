package secrets

import (
	"sync"
	"time"
)

// cacheEntry holds one resolved secret with its expiry metadata.
// value is zeroed (via clear) when the entry is evicted.
type cacheEntry struct {
	value     []byte
	fetchedAt time.Time
	ttl       time.Duration // 0 = never expires
}

func (e *cacheEntry) expired(now time.Time) bool {
	return e.ttl > 0 && now.After(e.fetchedAt.Add(e.ttl))
}

// secretCache is an in-memory store for resolved secret values.
// On eviction, the stored []byte is zeroed to limit the window in which a
// plaintext secret exists in heap memory.
//
// Concurrent reads use a shared RWMutex. Writes only occur on first fetch and
// during periodic rotation — neither is on the request hot path.
type secretCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

func newSecretCache() *secretCache {
	return &secretCache{entries: make(map[string]*cacheEntry)}
}

// get returns a fresh copy of the cached value, or (nil, false) on miss/expiry.
func (c *secretCache) get(ref string) ([]byte, bool) {
	c.mu.RLock()
	e, ok := c.entries[ref]
	c.mu.RUnlock()
	if !ok || e.expired(time.Now()) {
		return nil, false
	}
	// Return a copy — cache owns the authoritative slice.
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, true
}

// set stores a copy of value in the cache under ref.
// Any previous entry for ref is zeroed before replacement.
func (c *secretCache) set(ref string, value []byte, ttl time.Duration) {
	cp := make([]byte, len(value))
	copy(cp, value)

	c.mu.Lock()
	if old, ok := c.entries[ref]; ok {
		clear(old.value) // zero old plaintext
	}
	c.entries[ref] = &cacheEntry{value: cp, fetchedAt: time.Now(), ttl: ttl}
	c.mu.Unlock()
}

// evictExpired zeroes and removes all entries whose TTL has elapsed.
// Called by the Manager's background rotation goroutine.
func (c *secretCache) evictExpired() {
	now := time.Now()
	c.mu.Lock()
	for ref, e := range c.entries {
		if e.expired(now) {
			clear(e.value)
			delete(c.entries, ref)
		}
	}
	c.mu.Unlock()
}

// purge zeroes and removes all entries. Called on Manager.Close().
func (c *secretCache) purge() {
	c.mu.Lock()
	for _, e := range c.entries {
		clear(e.value)
	}
	c.entries = make(map[string]*cacheEntry)
	c.mu.Unlock()
}
