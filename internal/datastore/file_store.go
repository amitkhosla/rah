package datastore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"github.com/amitkhosla/rah/internal/config"
	"sync"
)

type fileStore struct {
	name   string
	kind   string
	domain string
	pool   ConnectionPool
	path   string

	mu       sync.RWMutex
	registry map[string][]byte
}

func newFileStore(cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	basePath := cfg.Connection.Path
	if basePath == "" {
		basePath = "./data"
	}
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, err
	}

	maxOpen := parsePoolMaxOpen(cfg, 32)
	store := &fileStore{
		name:     cfg.Name,
		kind:     "disk",
		domain:   domain,
		pool:     NewTokenPool(maxOpen),
		path:     filepath.Join(basePath, "store_"+domain+".json"),
		registry: make(map[string][]byte),
	}

	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *fileStore) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var payload map[string][]byte
	if err := json.Unmarshal(b, &payload); err != nil {
		return err
	}
	s.registry = payload
	return nil
}

func (s *fileStore) persistLocked() error {
	blob, err := json.Marshal(s.registry)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *fileStore) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
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
	return s.persistLocked()
}

func (s *fileStore) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
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

func (s *fileStore) Delete(ctx context.Context, tenant Tenant, key string) error {
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
	return s.persistLocked()
}

func (s *fileStore) ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error) {
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

func (s *fileStore) Kind() string         { return s.kind }
func (s *fileStore) Name() string         { return s.name }
func (s *fileStore) PoolStats() PoolStats { return s.pool.Stats() }
func (s *fileStore) Close() error         { return s.pool.Close() }

func parsePoolMaxOpen(cfg config.StoreConfig, fallback int) int {
	maxOpen := fallback
	if cfg.Connection.Params != nil {
		if raw, ok := cfg.Connection.Params["pool_max_open"]; ok {
			var parsed int
			for _, ch := range raw {
				if ch < '0' || ch > '9' {
					parsed = 0
					break
				}
				parsed = parsed*10 + int(ch-'0')
			}
			if parsed > 0 {
				maxOpen = parsed
			}
		}
	}
	return maxOpen
}
