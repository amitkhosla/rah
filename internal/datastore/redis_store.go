package datastore

import (
	"context"
	"time"

	"rah/internal/config"
	redistore "rah/internal/datastore/redis"
)

// redisAdapter wraps redistore.Store and implements KeyValueStore, BatchStore,
// ExpiringStore, and DistributedStore by converting datastore.Tenant → string.
type redisAdapter struct {
	s *redistore.Store
}

func newRedisStore(ctx context.Context, cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	return newRedisAdapter(ctx, cfg, domain, "redis")
}

func newRedisAdapter(ctx context.Context, cfg config.StoreConfig, domain, kind string) (KeyValueStore, error) {
	s, err := redistore.New(ctx, cfg, domain, kind)
	if err != nil {
		return nil, err
	}
	return &redisAdapter{s: s}, nil
}

// -- KeyValueStore --

func (a *redisAdapter) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
	return a.s.Put(ctx, string(tenant), key, value)
}

func (a *redisAdapter) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
	return a.s.Get(ctx, string(tenant), key)
}

func (a *redisAdapter) Delete(ctx context.Context, tenant Tenant, key string) error {
	return a.s.Delete(ctx, string(tenant), key)
}

func (a *redisAdapter) ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error) {
	return a.s.ListKeys(ctx, string(tenant), prefix)
}

func (a *redisAdapter) Kind() string       { return a.s.Kind() }
func (a *redisAdapter) Name() string       { return a.s.Name() }
func (a *redisAdapter) Close() error       { return a.s.Close() }
func (a *redisAdapter) PoolStats() PoolStats {
	st := a.s.Stats()
	return PoolStats{MaxOpen: st.MaxOpen, InUse: st.InUse, Waiters: st.Waiters}
}

// -- BatchStore --

func (a *redisAdapter) MultiGet(ctx context.Context, tenant Tenant, keys []string) (map[string][]byte, error) {
	return a.s.MultiGet(ctx, string(tenant), keys)
}

func (a *redisAdapter) MultiPut(ctx context.Context, tenant Tenant, kvs map[string][]byte) error {
	return a.s.MultiPut(ctx, string(tenant), kvs)
}

// -- ExpiringStore --

func (a *redisAdapter) PutWithTTL(ctx context.Context, tenant Tenant, key string, value []byte, ttl time.Duration) error {
	return a.s.PutWithTTL(ctx, string(tenant), key, value, ttl)
}

// -- ZSetStore --

func (a *redisAdapter) ZAdd(ctx context.Context, tenant Tenant, key string, members ...ZMember) error {
	rms := make([]redistore.ZMember, len(members))
	for i, m := range members {
		rms[i] = redistore.ZMember{Score: m.Score, Member: m.Member}
	}
	return a.s.ZAdd(ctx, string(tenant), key, rms...)
}

func (a *redisAdapter) ZRem(ctx context.Context, tenant Tenant, key string, members ...string) error {
	return a.s.ZRem(ctx, string(tenant), key, members...)
}

func (a *redisAdapter) ZRange(ctx context.Context, tenant Tenant, key string) ([]ZMember, error) {
	res, err := a.s.ZRange(ctx, string(tenant), key)
	if err != nil {
		return nil, err
	}
	return toDatastoreZMembers(res), nil
}

func (a *redisAdapter) ZRangeByScore(ctx context.Context, tenant Tenant, key string, min, max float64) ([]ZMember, error) {
	res, err := a.s.ZRangeByScore(ctx, string(tenant), key, min, max)
	if err != nil {
		return nil, err
	}
	return toDatastoreZMembers(res), nil
}

func (a *redisAdapter) ZScore(ctx context.Context, tenant Tenant, key, member string) (float64, bool, error) {
	return a.s.ZScore(ctx, string(tenant), key, member)
}

func (a *redisAdapter) ZCard(ctx context.Context, tenant Tenant, key string) (int64, error) {
	return a.s.ZCard(ctx, string(tenant), key)
}

func toDatastoreZMembers(in []redistore.ZMember) []ZMember {
	out := make([]ZMember, len(in))
	for i, m := range in {
		out[i] = ZMember{Score: m.Score, Member: m.Member}
	}
	return out
}

// -- DistributedStore --

func (a *redisAdapter) Increment(ctx context.Context, tenant Tenant, key string, delta int64) (int64, error) {
	return a.s.Increment(ctx, string(tenant), key, delta)
}

func (a *redisAdapter) TryLock(ctx context.Context, tenant Tenant, key, token string, ttl time.Duration) (bool, error) {
	return a.s.TryLock(ctx, string(tenant), key, token, ttl)
}

func (a *redisAdapter) Unlock(ctx context.Context, tenant Tenant, key, token string) error {
	return a.s.Unlock(ctx, string(tenant), key, token)
}
