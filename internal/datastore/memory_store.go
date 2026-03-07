package datastore

import (
	"context"
	"rah/internal/config"
	"sync"
)

// memoryStore is a simple shared implementation used as a safe fallback.
type memoryStore struct {
	name   string
	kind   string
	domain string
	pool   ConnectionPool

	mu       sync.RWMutex
	registry map[string][]byte
}

func newMemoryStore(cfg config.StoreConfig, kind string, domain string) KeyValueStore {
	maxOpen := parsePoolMaxOpen(cfg, 32)

	return &memoryStore{
		name:     cfg.Name,
		kind:     kind,
		domain:   domain,
		pool:     NewTokenPool(maxOpen),
		registry: make(map[string][]byte),
	}
}

func (s *memoryStore) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
	release, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	scopedKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	buf := make([]byte, len(value))
	copy(buf, value)
	s.registry[scopedKey] = buf
	return nil
}

func (s *memoryStore) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
	release, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	defer release()

	scopedKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return nil, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.registry[scopedKey]
	if !ok {
		return nil, false, nil
	}
	buf := make([]byte, len(val))
	copy(buf, val)
	return buf, true, nil
}

func (s *memoryStore) Delete(ctx context.Context, tenant Tenant, key string) error {
	release, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	scopedKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registry, scopedKey)
	return nil
}

func (s *memoryStore) ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error) {
	release, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	scopedPrefix, err := BuildScopedPrefix(tenant, s.domain, prefix)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0)
	for k := range s.registry {
		if len(k) >= len(scopedPrefix) && k[:len(scopedPrefix)] == scopedPrefix {
			out = append(out, k[len(scopedPrefix):])
		}
	}
	return out, nil
}

func (s *memoryStore) Kind() string         { return s.kind }
func (s *memoryStore) Name() string         { return s.name }
func (s *memoryStore) PoolStats() PoolStats { return s.pool.Stats() }
func (s *memoryStore) Close() error         { return s.pool.Close() }
