package datastore

import "sync"

// writeBuffer is a shared read-your-writes buffer between WriteBatcher and ReadBatcher.
// pending holds keys that have been submitted to the inbox but not yet flushed.
// recent holds keys from the last successfully flushed batch.
// Only the dispatch goroutine may write to recent (via Rotate/Remove).
type writeBuffer struct {
	mu            sync.RWMutex
	pending       map[string][]byte // key -> value
	deletePending map[string]bool   // keys pending deletion
	recent        map[string][]byte // last successfully flushed batch
}

func newWriteBuffer() *writeBuffer {
	return &writeBuffer{
		pending:       make(map[string][]byte),
		deletePending: make(map[string]bool),
		recent:        make(map[string][]byte),
	}
}

// Lookup checks pending then recent.
// deleted=true means the key was explicitly deleted (caller should treat as not found).
// found=true and deleted=false means a cached value is available.
func (b *writeBuffer) Lookup(key string) (val []byte, found bool, deleted bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.deletePending[key] {
		return nil, true, true
	}
	if v, ok := b.pending[key]; ok {
		return v, true, false
	}
	if v, ok := b.recent[key]; ok {
		return v, true, false
	}
	return nil, false, false
}

// SetPending marks a key as having a pending write. Must be called before
// sending to the inbox channel so that concurrent reads see the value.
func (b *writeBuffer) SetPending(key string, val []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.deletePending, key)
	b.pending[key] = val
}

// SetDeletePending marks a key as having a pending delete.
func (b *writeBuffer) SetDeletePending(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, key)
	b.deletePending[key] = true
}

// Rotate is called by the dispatch goroutine on successful flush.
// It moves flushed keys from pending into recent, replacing the previous recent set.
func (b *writeBuffer) Rotate(flushedKVs map[string][]byte, flushedDeletes []string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Build a new recent map from the flushed batch.
	newRecent := make(map[string][]byte, len(flushedKVs))
	for k, v := range flushedKVs {
		newRecent[k] = v
	}
	b.recent = newRecent

	// Remove flushed keys from pending.
	for k := range flushedKVs {
		delete(b.pending, k)
	}
	for _, k := range flushedDeletes {
		delete(b.deletePending, k)
	}
}

// Remove is called by the dispatch goroutine on batch error.
// It clears the failed keys from pending so they don't linger.
func (b *writeBuffer) Remove(keys []string, deletes []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, k := range keys {
		delete(b.pending, k)
	}
	for _, k := range deletes {
		delete(b.deletePending, k)
	}
}
