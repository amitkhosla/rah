package datastore

import (
	"context"
	"time"

	"rah/internal/ingest"
)

// EventingStore wraps any KeyValueStore and emits ingest events after
// successful writes (Put, Delete, MultiPut, PutWithTTL).
//
// It unconditionally implements BatchStore: if the inner store natively
// supports MultiGet/MultiPut those are used; otherwise it falls back to
// sequential single-key operations. This lets every domain store be used
// uniformly via the BatchStore interface regardless of backend.
//
// sourceID (instance fingerprint) is stamped as Model on every emitted event.
// Consumers compare Model to their own fingerprint and skip events they emitted,
// preventing the write-emit-consume-write loop when multiple instances share
// the same store.
type EventingStore struct {
	inner    KeyValueStore
	pipeline *ingest.Pipeline
	domain   string
	sourceID string // instance fingerprint; Model field in emitted events
}

// WrapWithEventing returns an EventingStore wrapping store.
// sourceID should be the instance fingerprint (e.g. fm.TxIDGen.Fingerprint()).
// If pipeline is nil the original store is returned unchanged (no overhead).
func WrapWithEventing(store KeyValueStore, pipeline *ingest.Pipeline, domain, sourceID string) KeyValueStore {
	if pipeline == nil {
		return store
	}
	return &EventingStore{inner: store, pipeline: pipeline, domain: domain, sourceID: sourceID}
}

// ── KeyValueStore ────────────────────────────────────────────────────────────

func (s *EventingStore) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
	if err := s.inner.Put(ctx, tenant, key, value); err != nil {
		return err
	}
	s.emitPut(tenant, key, value)
	return nil
}

func (s *EventingStore) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
	return s.inner.Get(ctx, tenant, key)
}

func (s *EventingStore) Delete(ctx context.Context, tenant Tenant, key string) error {
	if err := s.inner.Delete(ctx, tenant, key); err != nil {
		return err
	}
	s.emitDelete(tenant, key)
	return nil
}

func (s *EventingStore) ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error) {
	return s.inner.ListKeys(ctx, tenant, prefix)
}

func (s *EventingStore) Kind() string       { return s.inner.Kind() }
func (s *EventingStore) Name() string       { return s.inner.Name() }
func (s *EventingStore) PoolStats() PoolStats { return s.inner.PoolStats() }
func (s *EventingStore) Close() error       { return s.inner.Close() }

// ── BatchStore — always available ────────────────────────────────────────────

// MultiGet delegates to the inner BatchStore if available; otherwise falls
// back to sequential Gets in a single call (no goroutines, no extra allocs).
func (s *EventingStore) MultiGet(ctx context.Context, tenant Tenant, keys []string) (map[string][]byte, error) {
	if b, ok := s.inner.(BatchStore); ok {
		return b.MultiGet(ctx, tenant, keys)
	}
	result := make(map[string][]byte, len(keys))
	for _, k := range keys {
		v, found, err := s.inner.Get(ctx, tenant, k)
		if err != nil {
			return nil, err
		}
		if found {
			result[k] = v
		}
	}
	return result, nil
}

// MultiPut delegates to the inner BatchStore if available; otherwise falls
// back to sequential Puts. Events are emitted only after all writes succeed.
func (s *EventingStore) MultiPut(ctx context.Context, tenant Tenant, kvs map[string][]byte) error {
	if b, ok := s.inner.(BatchStore); ok {
		if err := b.MultiPut(ctx, tenant, kvs); err != nil {
			return err
		}
	} else {
		for k, v := range kvs {
			if err := s.inner.Put(ctx, tenant, k, v); err != nil {
				return err
			}
		}
	}
	for k, v := range kvs {
		s.emitPut(tenant, k, v)
	}
	return nil
}

// ── ExpiringStore — pass-through if inner supports it ────────────────────────

// PutWithTTL delegates to the inner ExpiringStore if available; falls back to
// a plain Put (no TTL honoured) and emits an event either way.
func (s *EventingStore) PutWithTTL(ctx context.Context, tenant Tenant, key string, value []byte, ttl time.Duration) error {
	if e, ok := s.inner.(ExpiringStore); ok {
		if err := e.PutWithTTL(ctx, tenant, key, value, ttl); err != nil {
			return err
		}
	} else {
		if err := s.inner.Put(ctx, tenant, key, value); err != nil {
			return err
		}
	}
	s.emitPut(tenant, key, value)
	return nil
}

// ── event helpers ─────────────────────────────────────────────────────────────

func (s *EventingStore) emitPut(tenant Tenant, key string, value []byte) {
	n := s.pipeline.NumSinksForKind(ingest.KindDBPut)
	if n == 0 {
		return
	}
	fk, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return
	}
	e := ingest.Event{
		Kind:        ingest.KindDBPut,
		Model:       s.sourceID,
		SessionID:   s.domain,
		TxID:        fk,
		TimestampNs: time.Now().UnixNano(),
	}
	e.SetPayload(value, n)
	s.pipeline.Emit(e)
}

func (s *EventingStore) emitDelete(tenant Tenant, key string) {
	n := s.pipeline.NumSinksForKind(ingest.KindDBDelete)
	if n == 0 {
		return
	}
	fk, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return
	}
	e := ingest.Event{
		Kind:        ingest.KindDBDelete,
		Model:       s.sourceID,
		SessionID:   s.domain,
		TxID:        fk,
		TimestampNs: time.Now().UnixNano(),
	}
	s.pipeline.Emit(e)
}
