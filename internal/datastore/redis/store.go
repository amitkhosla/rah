package redis

import (
	"context"
	"errors"
	"fmt"
	"sync"

	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"rah/internal/config"
)

// Store implements a Redis-backed key-value store for single, sentinel, and
// cluster topologies. Dragonfly is also served by this store (single only).
//
// This package does not import internal/datastore to avoid a circular import.
// The thin wrapper in internal/datastore/redis_store.go adapts it to the
// datastore.KeyValueStore, BatchStore, ExpiringStore, and DistributedStore
// interfaces using datastore.Tenant.
type Store struct {
	name     string
	kind     string // "redis" or "dragonfly"
	domain   string
	topology Topology
	client   goredis.Cmdable
	bundle   *clientBundle
	sf       singleflight.Group // coalesces concurrent reads for the same key
}

// New creates a Store from config. kind is "redis" or "dragonfly".
func New(cfg config.StoreConfig, domain, kind string) (*Store, error) {
	topology, err := parseTopology(cfg)
	if err != nil {
		return nil, err
	}
	if kind == "dragonfly" && topology != TopologySingle {
		return nil, fmt.Errorf("dragonfly supports single topology only (got %q)", topology)
	}
	b, err := newClientBundle(cfg, topology)
	if err != nil {
		return nil, err
	}
	return &Store{
		name:     cfg.Name,
		kind:     kind,
		domain:   domain,
		topology: topology,
		client:   b.cmdable,
		bundle:   b,
	}, nil
}

// Put stores value at key with no expiry. Use PutWithTTL for expiring entries.
func (s *Store) Put(ctx context.Context, tenant, key string, value []byte) error {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, k, value, 0).Err()
}

// getResult is the value type shared by all callers coalesced by singleflight.
type getResult struct {
	val   []byte
	found bool
}

// Get retrieves a value. Returns (nil, false, nil) when the key does not exist.
//
// Concurrent calls for the same key are coalesced: only one Redis GET is sent
// and all waiting goroutines receive the same result. This prevents thundering
// herd on cache misses when many goroutines request the same key simultaneously.
func (s *Store) Get(ctx context.Context, tenant, key string) ([]byte, bool, error) {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return nil, false, err
	}

	v, err, _ := s.sf.Do(k, func() (any, error) {
		val, err := s.client.Get(ctx, k).Bytes()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				return getResult{nil, false}, nil
			}
			return nil, err
		}
		return getResult{val, true}, nil
	})
	if err != nil {
		return nil, false, err
	}
	r := v.(getResult)
	return r.val, r.found, nil
}

// Delete removes a key. No-op if the key does not exist.
func (s *Store) Delete(ctx context.Context, tenant, key string) error {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	return s.client.Del(ctx, k).Err()
}

// ListKeys returns all keys under prefix for the given tenant+domain.
// The prefix is stripped from each returned key (callers see un-scoped keys).
// In cluster mode, all master nodes are scanned concurrently.
func (s *Store) ListKeys(ctx context.Context, tenant, prefix string) ([]string, error) {
	sp, err := scopedPrefix(tenant, s.domain, prefix)
	if err != nil {
		return nil, err
	}
	pattern := sp + "*"
	if s.bundle.cluster != nil {
		return scanCluster(ctx, s.bundle.cluster, sp, pattern)
	}
	return scanSingle(ctx, s.client, sp, pattern)
}

func scanSingle(ctx context.Context, client goredis.Cmdable, prefix, pattern string) ([]string, error) {
	var keys []string
	var cursor uint64
	for {
		batch, next, err := client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range batch {
			keys = append(keys, k[len(prefix):])
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}

// scanCluster fans out SCAN to every master node concurrently and merges results.
func scanCluster(ctx context.Context, cc *goredis.ClusterClient, prefix, pattern string) ([]string, error) {
	var (
		keys []string
		mu   sync.Mutex
	)
	err := cc.ForEachMaster(ctx, func(ctx context.Context, c *goredis.Client) error {
		nodeKeys, err := scanSingle(ctx, c, prefix, pattern)
		if err != nil {
			return err
		}
		mu.Lock()
		keys = append(keys, nodeKeys...)
		mu.Unlock()
		return nil
	})
	return keys, err
}

// Kind returns "redis" or "dragonfly".
func (s *Store) Kind() string { return s.kind }

// Name returns the store name as defined in config.
func (s *Store) Name() string { return s.name }

// Stats returns connection pool telemetry.
func (s *Store) Stats() Stats { return s.bundle.statsFn() }

// Close shuts down the underlying Redis client.
func (s *Store) Close() error { return s.bundle.closeFn() }
