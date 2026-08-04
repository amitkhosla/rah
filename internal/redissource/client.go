package redissource

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var errRahReserved = errors.New("key uses reserved _rah: prefix")

// TenantScopedClient wraps a RedisCoalescer with a per-tenant prefix computed once at construction.
type TenantScopedClient struct {
	coalescer *RedisCoalescer
	prefix    string // pre-computed, e.g. "42:" or "acme:" or ""
	locks     sync.Map // key → token string for lock ownership
}

func newTenantScopedClient(c *RedisCoalescer, cfg RedisSourceConfig, tenantID uint16, tenantKey string) *TenantScopedClient {
	sep := cfg.KeySep
	if sep == "" {
		sep = ":"
	}
	var prefix string
	switch cfg.TenantPrefix {
	case PrefixID:
		prefix = fmt.Sprintf("%d%s", tenantID, sep)
	case PrefixAlias:
		prefix = tenantKey + sep
	default:
		prefix = ""
	}
	return &TenantScopedClient{coalescer: c, prefix: prefix}
}

func (c *TenantScopedClient) validateKey(k string) error {
	if len(k) >= len(RahReservedPrefix) && k[:len(RahReservedPrefix)] == RahReservedPrefix {
		return errRahReserved
	}
	return nil
}

func (c *TenantScopedClient) fullKey(k string) string {
	if c.prefix == "" {
		return k
	}
	return c.prefix + k
}

// Get retrieves a value by key.
func (c *TenantScopedClient) Get(ctx context.Context, key string) ([]byte, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	return c.coalescer.Get(ctx, c.fullKey(key))
}

// Put stores a value with optional TTL (0 = no expiry).
func (c *TenantScopedClient) Put(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	return c.coalescer.Set(ctx, c.fullKey(key), value, ttl)
}

// MGet retrieves multiple keys at once.
func (c *TenantScopedClient) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	full := make([]string, len(keys))
	for i, k := range keys {
		if err := c.validateKey(k); err != nil {
			return nil, err
		}
		full[i] = c.fullKey(k)
	}
	return c.coalescer.MGet(ctx, full)
}

// MSet stores multiple key-value pairs.
func (c *TenantScopedClient) MSet(ctx context.Context, pairs map[string][]byte, ttl time.Duration) error {
	for k := range pairs {
		if err := c.validateKey(k); err != nil {
			return err
		}
	}
	full := make(map[string][]byte, len(pairs))
	for k, v := range pairs {
		full[c.fullKey(k)] = v
	}
	return c.coalescer.MSet(ctx, full, ttl)
}

// Del deletes one or more keys.
func (c *TenantScopedClient) Del(ctx context.Context, keys ...string) error {
	full := make([]string, len(keys))
	for i, k := range keys {
		if err := c.validateKey(k); err != nil {
			return err
		}
		full[i] = c.fullKey(k)
	}
	return c.coalescer.Del(ctx, full...)
}

// Exists checks if a key exists.
func (c *TenantScopedClient) Exists(ctx context.Context, key string) (bool, error) {
	if err := c.validateKey(key); err != nil {
		return false, err
	}
	n, err := c.coalescer.client.Exists(ctx, c.fullKey(key)).Result()
	return n > 0, err
}

// Incr increments a key by delta (default 1).
func (c *TenantScopedClient) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	if delta == 0 {
		delta = 1
	}
	return c.coalescer.client.IncrBy(ctx, c.fullKey(key), delta).Result()
}

// Decr decrements a key by delta (default 1).
func (c *TenantScopedClient) Decr(ctx context.Context, key string, delta int64) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	if delta == 0 {
		delta = 1
	}
	return c.coalescer.client.DecrBy(ctx, c.fullKey(key), delta).Result()
}

// Expire sets TTL on an existing key.
func (c *TenantScopedClient) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	return c.coalescer.client.Expire(ctx, c.fullKey(key), ttl).Err()
}

// TTL returns the remaining TTL of a key in seconds (-1 = no TTL, -2 = does not exist).
func (c *TenantScopedClient) TTL(ctx context.Context, key string) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	d, err := c.coalescer.client.TTL(ctx, c.fullKey(key)).Result()
	return int64(d.Seconds()), err
}

// Persist removes the TTL from a key.
func (c *TenantScopedClient) Persist(ctx context.Context, key string) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	return c.coalescer.client.Persist(ctx, c.fullKey(key)).Err()
}

// Lock acquires a distributed lock using SET NX PX.
func (c *TenantScopedClient) Lock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if err := c.validateKey(key); err != nil {
		return false, err
	}
	token := fmt.Sprintf("%d", time.Now().UnixNano())
	fk := c.fullKey(key)
	ok, err := c.coalescer.client.SetNX(ctx, fk, token, ttl).Result()
	if ok {
		c.locks.Store(fk, token)
	}
	return ok, err
}

// Unlock releases a lock only if this client owns it (Lua check-and-delete).
func (c *TenantScopedClient) Unlock(ctx context.Context, key string) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	fk := c.fullKey(key)
	tok, ok := c.locks.Load(fk)
	if !ok {
		return errors.New("lock not held by this client")
	}
	const script = `if redis.call("get",KEYS[1]) == ARGV[1] then return redis.call("del",KEYS[1]) else return 0 end`
	return c.coalescer.client.Eval(ctx, script, []string{fk}, tok).Err()
}

// Publish publishes a message to a channel.
func (c *TenantScopedClient) Publish(ctx context.Context, channel string, message []byte) error {
	if err := c.validateKey(channel); err != nil {
		return err
	}
	return c.coalescer.client.Publish(ctx, c.fullKey(channel), message).Err()
}

// ZAdd adds a member with score to a sorted set. mode: ""=default, "nx","xx","gt","lt".
func (c *TenantScopedClient) ZAdd(ctx context.Context, key, member string, score float64, mode string) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	z := redis.Z{Score: score, Member: member}
	var cmd *redis.IntCmd
	fk := c.fullKey(key)
	switch mode {
	case "nx":
		cmd = c.coalescer.client.ZAddNX(ctx, fk, z)
	case "xx":
		cmd = c.coalescer.client.ZAddXX(ctx, fk, z)
	case "gt":
		cmd = c.coalescer.client.ZAddGT(ctx, fk, z)
	case "lt":
		cmd = c.coalescer.client.ZAddLT(ctx, fk, z)
	default:
		cmd = c.coalescer.client.ZAdd(ctx, fk, z)
	}
	return cmd.Result()
}

// ZIncrBy increments the score of a member in a sorted set.
func (c *TenantScopedClient) ZIncrBy(ctx context.Context, key, member string, incr float64) (float64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	return c.coalescer.client.ZIncrBy(ctx, c.fullKey(key), incr, member).Result()
}

// ZRange returns elements from a sorted set by rank range.
func (c *TenantScopedClient) ZRange(ctx context.Context, key string, start, stop int64, rev bool) ([]string, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	fk := c.fullKey(key)
	if rev {
		return c.coalescer.client.ZRevRange(ctx, fk, start, stop).Result()
	}
	return c.coalescer.client.ZRange(ctx, fk, start, stop).Result()
}

// ZRangeWithScores returns elements with scores from a sorted set.
func (c *TenantScopedClient) ZRangeWithScores(ctx context.Context, key string, start, stop int64, rev bool) ([]redis.Z, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	fk := c.fullKey(key)
	if rev {
		return c.coalescer.client.ZRevRangeWithScores(ctx, fk, start, stop).Result()
	}
	return c.coalescer.client.ZRangeWithScores(ctx, fk, start, stop).Result()
}

// ZRangeByScore returns elements by score range.
func (c *TenantScopedClient) ZRangeByScore(ctx context.Context, key, min, max string) ([]string, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	return c.coalescer.client.ZRangeByScore(ctx, c.fullKey(key), &redis.ZRangeBy{Min: min, Max: max}).Result()
}

// ZScore returns the score of a member.
func (c *TenantScopedClient) ZScore(ctx context.Context, key, member string) (float64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	return c.coalescer.client.ZScore(ctx, c.fullKey(key), member).Result()
}

// ZRank returns the rank of a member (0-based). rev=true for reverse rank.
func (c *TenantScopedClient) ZRank(ctx context.Context, key, member string, rev bool) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	fk := c.fullKey(key)
	if rev {
		return c.coalescer.client.ZRevRank(ctx, fk, member).Result()
	}
	return c.coalescer.client.ZRank(ctx, fk, member).Result()
}

// ZRem removes members from a sorted set.
func (c *TenantScopedClient) ZRem(ctx context.Context, key string, members ...string) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	return c.coalescer.client.ZRem(ctx, c.fullKey(key), args...).Err()
}

// ZPopMin pops the lowest-score members.
func (c *TenantScopedClient) ZPopMin(ctx context.Context, key string, count int64) ([]redis.Z, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	return c.coalescer.client.ZPopMin(ctx, c.fullKey(key), count).Result()
}

// ZPopMax pops the highest-score members.
func (c *TenantScopedClient) ZPopMax(ctx context.Context, key string, count int64) ([]redis.Z, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	return c.coalescer.client.ZPopMax(ctx, c.fullKey(key), count).Result()
}

// HSet sets a single hash field.
func (c *TenantScopedClient) HSet(ctx context.Context, key, field string, value []byte) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	return c.coalescer.client.HSet(ctx, c.fullKey(key), field, value).Err()
}

// HMSet sets multiple hash fields.
func (c *TenantScopedClient) HMSet(ctx context.Context, key string, fields map[string]any) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	return c.coalescer.client.HMSet(ctx, c.fullKey(key), fields).Err()
}

// HGet retrieves a single hash field.
func (c *TenantScopedClient) HGet(ctx context.Context, key, field string) ([]byte, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	s, err := c.coalescer.client.HGet(ctx, c.fullKey(key), field).Result()
	return []byte(s), err
}

// HMGet retrieves multiple hash fields.
func (c *TenantScopedClient) HMGet(ctx context.Context, key string, fields ...string) ([]any, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	return c.coalescer.client.HMGet(ctx, c.fullKey(key), fields...).Result()
}

// HGetAll retrieves all hash fields.
func (c *TenantScopedClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	return c.coalescer.client.HGetAll(ctx, c.fullKey(key)).Result()
}

// HDel deletes a hash field.
func (c *TenantScopedClient) HDel(ctx context.Context, key, field string) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	return c.coalescer.client.HDel(ctx, c.fullKey(key), field).Err()
}

// HIncrBy increments a hash field value.
func (c *TenantScopedClient) HIncrBy(ctx context.Context, key, field string, delta int64) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	return c.coalescer.client.HIncrBy(ctx, c.fullKey(key), field, delta).Result()
}

// LPush pushes values to the left of a list.
func (c *TenantScopedClient) LPush(ctx context.Context, key string, values ...[]byte) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return c.coalescer.client.LPush(ctx, c.fullKey(key), args...).Result()
}

// RPush pushes values to the right of a list.
func (c *TenantScopedClient) RPush(ctx context.Context, key string, values ...[]byte) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return c.coalescer.client.RPush(ctx, c.fullKey(key), args...).Result()
}

// LPop pops count elements from the left of a list.
func (c *TenantScopedClient) LPop(ctx context.Context, key string, count int64) ([]string, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	if count <= 1 {
		v, err := c.coalescer.client.LPop(ctx, c.fullKey(key)).Result()
		return []string{v}, err
	}
	return c.coalescer.client.LPopCount(ctx, c.fullKey(key), int(count)).Result()
}

// RPop pops count elements from the right of a list.
func (c *TenantScopedClient) RPop(ctx context.Context, key string, count int64) ([]string, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	if count <= 1 {
		v, err := c.coalescer.client.RPop(ctx, c.fullKey(key)).Result()
		return []string{v}, err
	}
	return c.coalescer.client.RPopCount(ctx, c.fullKey(key), int(count)).Result()
}

// LRange returns a range of elements from a list.
func (c *TenantScopedClient) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	return c.coalescer.client.LRange(ctx, c.fullKey(key), start, stop).Result()
}

// LLen returns the length of a list.
func (c *TenantScopedClient) LLen(ctx context.Context, key string) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	return c.coalescer.client.LLen(ctx, c.fullKey(key)).Result()
}

// SAdd adds members to a set.
func (c *TenantScopedClient) SAdd(ctx context.Context, key string, members ...[]byte) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	return c.coalescer.client.SAdd(ctx, c.fullKey(key), args...).Result()
}

// SRem removes members from a set.
func (c *TenantScopedClient) SRem(ctx context.Context, key string, members ...[]byte) error {
	if err := c.validateKey(key); err != nil {
		return err
	}
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	return c.coalescer.client.SRem(ctx, c.fullKey(key), args...).Err()
}

// SIsMember checks if a value is a member of a set.
func (c *TenantScopedClient) SIsMember(ctx context.Context, key string, member []byte) (bool, error) {
	if err := c.validateKey(key); err != nil {
		return false, err
	}
	return c.coalescer.client.SIsMember(ctx, c.fullKey(key), member).Result()
}

// SMembers returns all members of a set.
func (c *TenantScopedClient) SMembers(ctx context.Context, key string) ([]string, error) {
	if err := c.validateKey(key); err != nil {
		return nil, err
	}
	return c.coalescer.client.SMembers(ctx, c.fullKey(key)).Result()
}

// SCard returns the number of members in a set.
func (c *TenantScopedClient) SCard(ctx context.Context, key string) (int64, error) {
	if err := c.validateKey(key); err != nil {
		return 0, err
	}
	return c.coalescer.client.SCard(ctx, c.fullKey(key)).Result()
}
