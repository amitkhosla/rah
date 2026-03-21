package datastore

import (
	"context"
	"errors"
	"fmt"
	"rah/internal/config"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

type redisStore struct {
	name   string
	kind   string
	domain string
	client *redis.Client
}

func newRedisStore(cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	return newRedisStoreWithKind(cfg, domain, "redis")
}

func newRedisStoreWithKind(cfg config.StoreConfig, domain, kind string) (KeyValueStore, error) {
	dbIndex := 0
	if cfg.Connection.Database != "" {
		if n, err := strconv.Atoi(cfg.Connection.Database); err == nil {
			dbIndex = n
		}
	}

	poolSize := parsePoolMaxOpen(cfg, 32)

	opts := &redis.Options{
		Addr:     cfg.Connection.Address,
		Password: cfg.Connection.Password,
		DB:       dbIndex,
		PoolSize: poolSize,
	}

	client := redis.NewClient(opts)

	return &redisStore{
		name:   cfg.Name,
		kind:   kind,
		domain: domain,
		client: client,
	}, nil
}

func (s *redisStore) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
	scopedKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, scopedKey, value, 0).Err()
}

func (s *redisStore) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
	scopedKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return nil, false, err
	}
	val, err := s.client.Get(ctx, scopedKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return val, true, nil
}

func (s *redisStore) Delete(ctx context.Context, tenant Tenant, key string) error {
	scopedKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	return s.client.Del(ctx, scopedKey).Err()
}

func (s *redisStore) ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error) {
	scopedPrefix, err := BuildScopedPrefix(tenant, s.domain, prefix)
	if err != nil {
		return nil, err
	}
	pattern := scopedPrefix + "*"

	var keys []string
	var cursor uint64
	for {
		batch, next, err := s.client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, fmt.Errorf("redis SCAN: %w", err)
		}
		for _, k := range batch {
			if strings.HasPrefix(k, scopedPrefix) {
				keys = append(keys, k[len(scopedPrefix):])
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}

func (s *redisStore) Kind() string { return s.kind }
func (s *redisStore) Name() string { return s.name }

func (s *redisStore) PoolStats() PoolStats {
	stats := s.client.PoolStats()
	return PoolStats{
		MaxOpen: int64(stats.TotalConns),
		InUse:   int64(stats.TotalConns - stats.IdleConns),
		Waiters: int64(stats.Misses),
	}
}

func (s *redisStore) Close() error {
	return s.client.Close()
}
