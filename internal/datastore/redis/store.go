package redis

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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
	dedup    *IODeduper         // nil when IODedupWindow is ""
}

// New creates a Store from config. kind is "redis" or "dragonfly".
// ctx is used to cancel the IODeduper's background rotation goroutine when
// IODedupWindow is non-empty; pass the gateway's root context.
func New(ctx context.Context, cfg config.StoreConfig, domain, kind string) (*Store, error) {
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

	var dedup *IODeduper
	if w := cfg.Connection.IODedupWindow; w != "" {
		ttl, err := time.ParseDuration(w)
		if err != nil {
			return nil, fmt.Errorf("invalid io_dedup_window %q: %w", w, err)
		}
		if ttl <= 0 {
			return nil, fmt.Errorf("io_dedup_window must be a positive duration (got %q)", w)
		}
		dedup = newIODeduper(ttl)
		dedup.StartRotation(ctx)
	}

	return &Store{
		name:     cfg.Name,
		kind:     kind,
		domain:   domain,
		topology: topology,
		client:   b.cmdable,
		bundle:   b,
		dedup:    dedup,
	}, nil
}

// Put stores value at key with no expiry. Use PutWithTTL for expiring entries.
// If IODedupWindow is configured and the key already holds the same bytes in
// the dedup layer, the Redis write is skipped.
func (s *Store) Put(ctx context.Context, tenant, key string, value []byte) error {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	if s.dedup != nil && s.dedup.ShouldSkipWrite(k, value, 0) {
		return nil
	}
	if err := s.client.Set(ctx, k, value, 0).Err(); err != nil {
		return err
	}
	if s.dedup != nil {
		s.dedup.Store(k, value, 0)
	}
	return nil
}

// getResult is the value type shared by all callers coalesced by singleflight.
type getResult struct {
	val   []byte
	found bool
}

// Get retrieves a value. Returns (nil, false, nil) when the key does not exist.
//
// If IODedupWindow is configured, the dedup layer is checked first; a Redis
// round-trip is only made on a miss, and the result is stored back into the
// dedup layer for subsequent burst requests.
//
// Within the same burst (multiple goroutines requesting the same key while no
// dedup entry exists yet), singleflight ensures only one Redis GET is issued.
func (s *Store) Get(ctx context.Context, tenant, key string) ([]byte, bool, error) {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return nil, false, err
	}

	// Fast path: dedup layer hit — no Redis round-trip needed.
	if s.dedup != nil {
		if val, ok := s.dedup.Get(k); ok {
			return val, val != nil, nil
		}
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
	if s.dedup != nil {
		// Store the result (including misses as nil) so the next burst hit is local.
		s.dedup.Store(k, r.val, 0)
	}
	return r.val, r.found, nil
}

// Delete removes a key. No-op if the key does not exist.
// Always reaches Redis (never skipped); also invalidates any dedup entry.
func (s *Store) Delete(ctx context.Context, tenant, key string) error {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	if err := s.client.Del(ctx, k).Err(); err != nil {
		return err
	}
	if s.dedup != nil {
		s.dedup.Invalidate(k)
	}
	return nil
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
