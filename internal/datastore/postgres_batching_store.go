package datastore

import (
	"context"
	"time"
)

// PostgresBatchingStore wraps an inner KeyValueStore (typically a postgresqlStore)
// and coalesces concurrent Get/Put/Delete calls from multiple goroutines into
// single batched MultiGet / MultiPut queries.
//
// Read-your-writes guarantee: a key written via Put is immediately visible to
// subsequent Get calls without a DB round-trip, via the shared writeBuffer.
//
// All other methods (ListKeys, PoolStats, Close, etc.) are passed through to
// the inner store unchanged.
type PostgresBatchingStore struct {
	inner   KeyValueStore
	buf     *writeBuffer
	writer  *WriteBatcher
	reader  *ReadBatcher
}

// NewPostgresBatchingStore creates a PostgresBatchingStore wrapping inner and
// starts the read and write dispatch goroutines. Both goroutines stop when ctx
// is cancelled.
func NewPostgresBatchingStore(ctx context.Context, inner KeyValueStore, maxBatch int) *PostgresBatchingStore {
	buf := newWriteBuffer()
	return &PostgresBatchingStore{
		inner:  inner,
		buf:    buf,
		writer: newWriteBatcher(ctx, inner, buf, maxBatch),
		reader: newReadBatcher(ctx, inner, buf, maxBatch),
	}
}

// --- KeyValueStore interface ---

// Get checks the write buffer first (read-your-writes) and, on a miss, fans
// the request into a batched MultiGet dispatch.
func (s *PostgresBatchingStore) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
	return s.reader.Get(ctx, tenant, key)
}

// Put routes through the WriteBatcher, which coalesces concurrent puts into
// a single MultiPut query.
func (s *PostgresBatchingStore) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
	return s.writer.Put(ctx, tenant, key, value)
}

// Delete routes through the WriteBatcher so it is coalesced with concurrent writes.
func (s *PostgresBatchingStore) Delete(ctx context.Context, tenant Tenant, key string) error {
	return s.writer.Delete(ctx, tenant, key)
}

// ListKeys passes through directly to the inner store.
func (s *PostgresBatchingStore) ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error) {
	return s.inner.ListKeys(ctx, tenant, prefix)
}

// Kind returns the inner store's kind.
func (s *PostgresBatchingStore) Kind() string { return s.inner.Kind() }

// Name returns the inner store's name.
func (s *PostgresBatchingStore) Name() string { return s.inner.Name() }

// PoolStats passes through to the inner store.
func (s *PostgresBatchingStore) PoolStats() PoolStats { return s.inner.PoolStats() }

// Close closes the inner store. The dispatch goroutines are stopped by
// cancelling the context passed to NewPostgresBatchingStore.
func (s *PostgresBatchingStore) Close() error { return s.inner.Close() }

// --- BatchStore interface ---

// MultiGet checks the write buffer for each key first; remaining misses are
// resolved via a direct inner.MultiGet call.
func (s *PostgresBatchingStore) MultiGet(ctx context.Context, tenant Tenant, keys []string) (map[string][]byte, error) {
	result := make(map[string][]byte, len(keys))
	var misses []string

	for _, key := range keys {
		bufKey := bufferKey(tenant, key)
		val, found, deleted := s.buf.Lookup(bufKey)
		if found {
			if !deleted {
				result[key] = val
			}
			// deleted → treat as not found; don't include in result or misses
			continue
		}
		misses = append(misses, key)
	}

	if len(misses) == 0 {
		return result, nil
	}

	// Resolve misses via inner store directly (no extra batching layer needed
	// since the caller is already doing bulk operations).
	var dbResult map[string][]byte
	var err error
	if bs, ok := s.inner.(BatchStore); ok {
		dbResult, err = bs.MultiGet(ctx, tenant, misses)
	} else {
		dbResult = make(map[string][]byte, len(misses))
		for _, k := range misses {
			v, found, e := s.inner.Get(ctx, tenant, k)
			if e != nil {
				return nil, e
			}
			if found {
				dbResult[k] = v
			}
		}
	}
	if err != nil {
		return nil, err
	}

	for k, v := range dbResult {
		result[k] = v
	}
	return result, nil
}

// MultiPut routes each entry through the WriteBatcher so they are coalesced
// with concurrent single-key puts and written atomically in one batch.
func (s *PostgresBatchingStore) MultiPut(ctx context.Context, tenant Tenant, kvs map[string][]byte) error {
	// Fan each key through the WriteBatcher individually. The batcher will
	// coalesce them (along with any other concurrent Put/MultiPut calls) into
	// a single MultiPut to the inner store.
	for key, value := range kvs {
		if err := s.writer.Put(ctx, tenant, key, value); err != nil {
			return err
		}
	}
	return nil
}

// --- ExpiringStore passthrough ---

// PutWithTTL passes through to the inner store if it implements ExpiringStore.
// PostgreSQL does not natively support TTL, so this will only work if the inner
// store implements ExpiringStore.
func (s *PostgresBatchingStore) PutWithTTL(ctx context.Context, tenant Tenant, key string, value []byte, ttl time.Duration) error {
	if es, ok := s.inner.(ExpiringStore); ok {
		return es.PutWithTTL(ctx, tenant, key, value, ttl)
	}
	// Fall back to plain Put ignoring TTL.
	return s.writer.Put(ctx, tenant, key, value)
}
