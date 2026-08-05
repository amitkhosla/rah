package redissource

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisSourcePool manages a set of named Redis coalescers and per-tenant client caches.
type RedisSourcePool struct {
	coalescers map[string]*RedisCoalescer
	configs    map[string]RedisSourceConfig
	cache      sync.Map // key: "sourceName:tenantID" → *TenantScopedClient
}

// New creates a RedisSourcePool from configs, starts all coalescers, and returns.
func New(ctx context.Context, cfgs []RedisSourceConfig) (*RedisSourcePool, error) {
	p := &RedisSourcePool{
		coalescers: make(map[string]*RedisCoalescer, len(cfgs)),
		configs:    make(map[string]RedisSourceConfig, len(cfgs)),
	}
	for _, cfg := range cfgs {
		client, err := buildClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("redis source %q: %w", cfg.Name, err)
		}
		c := newCoalescer(client, defaultBatchWindow, defaultBatchMax)
		c.Start(ctx)
		p.coalescers[cfg.Name] = c
		p.configs[cfg.Name] = cfg
	}
	return p, nil
}

// GetForTenant returns a TenantScopedClient for the named source and tenant.
// The client is cached per (source, tenantID) — prefix string computed once.
func (p *RedisSourcePool) GetForTenant(name string, tenantID uint16, tenantKey string) (*TenantScopedClient, bool) {
	c, ok := p.coalescers[name]
	if !ok {
		return nil, false
	}
	cfg := p.configs[name]
	cacheKey := fmt.Sprintf("%s:%d", name, tenantID)
	if v, loaded := p.cache.Load(cacheKey); loaded {
		return v.(*TenantScopedClient), true
	}
	tc := newTenantScopedClient(c, cfg, tenantID, tenantKey)
	p.cache.Store(cacheKey, tc)
	return tc, true
}

// Names returns all configured source names.
func (p *RedisSourcePool) Names() []string {
	names := make([]string, 0, len(p.configs))
	for n := range p.configs {
		names = append(names, n)
	}
	return names
}

// Close closes all underlying redis connections.
func (p *RedisSourcePool) Close() {
	for _, c := range p.coalescers {
		_ = c.Close()
	}
}

func buildClient(cfg RedisSourceConfig) (redis.UniversalClient, error) {
	password := resolveEnvRef(cfg.Password)
	opts := &redis.UniversalOptions{
		Password: password,
		DB:       cfg.DB,
	}
	if len(cfg.Addrs) > 0 {
		opts.Addrs = cfg.Addrs
	} else if cfg.Addr != "" {
		opts.Addrs = []string{cfg.Addr}
	} else {
		opts.Addrs = []string{"localhost:6379"}
	}
	if cfg.TLS {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return redis.NewUniversalClient(opts), nil
}

func resolveEnvRef(s string) string {
	const prefix = "env:"
	if strings.HasPrefix(s, prefix) {
		return os.Getenv(s[len(prefix):])
	}
	return s
}

// HealthCheck pings all coalescers. Returns map of name → error (nil = healthy).
func (p *RedisSourcePool) HealthCheck(timeout time.Duration) map[string]error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result := make(map[string]error, len(p.coalescers))
	for name, c := range p.coalescers {
		result[name] = c.client.Ping(ctx).Err()
	}
	return result
}
