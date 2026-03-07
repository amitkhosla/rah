package datastore

import (
	"context"
	"fmt"
	"strings"
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
