package datastore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Tenant is the globally stable tenant identifier used for persisted store keys.
// This must be a tenant string (name/slug), not a local numeric tenant ID.
type Tenant string

// KeyValueStore is the generic contract used by all backend implementations.
type KeyValueStore interface {
	Put(ctx context.Context, tenant Tenant, key string, value []byte) error
	Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error)
	Delete(ctx context.Context, tenant Tenant, key string) error
	ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error)
	Kind() string
	Name() string
	PoolStats() PoolStats
	Close() error
}

// BatchStore is an optional interface for stores that support bulk operations
// in a single round-trip. Callers should type-assert before use:
//
//	if b, ok := store.(datastore.BatchStore); ok { b.MultiGet(...) }
type BatchStore interface {
	MultiGet(ctx context.Context, tenant Tenant, keys []string) (map[string][]byte, error)
	MultiPut(ctx context.Context, tenant Tenant, kvs map[string][]byte) error
}

// ExpiringStore is an optional interface for stores that support TTL on values.
type ExpiringStore interface {
	PutWithTTL(ctx context.Context, tenant Tenant, key string, value []byte, ttl time.Duration) error
}

// ZMember is a scored member of a sorted set.
type ZMember struct {
	Score  float64
	Member string
}

// ZSetStore is an optional interface for Redis sorted-set operations.
// Use for ordered collections: aliases, identifiers, weighted service URLs.
//
// Score semantics by use case:
//   - Aliases / identifiers: score = insertion Unix millis (stable ordering)
//   - Service URLs: score = weight/priority (higher = preferred in LB)
//   - Rate limit tiers: score = tier level
type ZSetStore interface {
	// ZAdd adds or updates members. Existing members have their score updated.
	ZAdd(ctx context.Context, tenant Tenant, key string, members ...ZMember) error
	// ZRem removes members. No-op for members that don't exist.
	ZRem(ctx context.Context, tenant Tenant, key string, members ...string) error
	// ZRange returns all members ordered by score ascending (lowest first).
	ZRange(ctx context.Context, tenant Tenant, key string) ([]ZMember, error)
	// ZRangeByScore returns members whose score is within [min, max] inclusive.
	ZRangeByScore(ctx context.Context, tenant Tenant, key string, min, max float64) ([]ZMember, error)
	// ZScore returns the score of a member. found=false if member does not exist.
	ZScore(ctx context.Context, tenant Tenant, key string, member string) (float64, bool, error)
	// ZCard returns the number of members in the sorted set.
	ZCard(ctx context.Context, tenant Tenant, key string) (int64, error)
}

// DistributedStore is an optional interface for distributed coordination
// primitives. Implemented by Redis/Dragonfly; not available on disk/file stores.
type DistributedStore interface {
	// Increment atomically adds delta to the counter at key (rate limiting).
	Increment(ctx context.Context, tenant Tenant, key string, delta int64) (int64, error)
	// TryLock acquires a lock identified by token; returns false if already held.
	TryLock(ctx context.Context, tenant Tenant, key, token string, ttl time.Duration) (bool, error)
	// Unlock releases the lock only if the caller's token matches.
	Unlock(ctx context.Context, tenant Tenant, key, token string) error
}

// BuildScopedKey enforces tenant-first key layout across all stores.
// Format: tenant:{tenant}:{domain}:{key}
func BuildScopedKey(tenant Tenant, domain, key string) (string, error) {
	tenantValue := strings.TrimSpace(string(tenant))
	domain = strings.TrimSpace(domain)
	key = strings.TrimSpace(key)

	if tenantValue == "" {
		return "", fmt.Errorf("tenant is required")
	}
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}
	if key == "" {
		return "", fmt.Errorf("key is required")
	}

	return fmt.Sprintf("tenant:%s:%s:%s", tenantValue, domain, key), nil
}

func BuildScopedPrefix(tenant Tenant, domain, prefix string) (string, error) {
	tenantValue := strings.TrimSpace(string(tenant))
	domain = strings.TrimSpace(domain)
	prefix = strings.TrimSpace(prefix)

	if tenantValue == "" {
		return "", fmt.Errorf("tenant is required")
	}
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}

	base := fmt.Sprintf("tenant:%s:%s:", tenantValue, domain)
	if prefix == "" {
		return base, nil
	}
	return base + prefix, nil
}
