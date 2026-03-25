package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// unlockScript atomically releases a lock only if the caller's token matches.
// Returns 1 if deleted, 0 if the token did not match (lock owned by someone else).
// Using a Lua script ensures the GET+DEL is atomic — no race between check and delete.
var unlockScript = goredis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`)

// PutWithTTL stores value at key with an expiry duration.
// ttl=0 stores permanently (identical to Put).
// If IODedupWindow is configured and the key already holds the same value with
// the same TTL, the Redis write is skipped.
func (s *Store) PutWithTTL(ctx context.Context, tenant, key string, value []byte, ttl time.Duration) error {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	if s.dedup != nil && s.dedup.ShouldSkipWrite(k, value, ttl) {
		return nil
	}
	if err := s.client.Set(ctx, k, value, ttl).Err(); err != nil {
		return err
	}
	if s.dedup != nil {
		s.dedup.Store(k, value, ttl)
	}
	return nil
}

// Increment atomically adds delta to the integer at key and returns the new value.
// The key is initialised to 0 before incrementing if it does not exist.
// Primary use: distributed rate-limit counters across multiple gateway instances.
func (s *Store) Increment(ctx context.Context, tenant, key string, delta int64) (int64, error) {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return 0, err
	}
	return s.client.IncrBy(ctx, k, delta).Result()
}

// TryLock attempts to acquire a distributed lock via SET NX PX.
// Returns true if acquired, false if the lock is already held by another caller.
// The lock expires automatically after ttl to prevent deadlocks on crash.
// token must be unique per caller (e.g. a UUID); pass the same token to Unlock.
func (s *Store) TryLock(ctx context.Context, tenant, key, token string, ttl time.Duration) (bool, error) {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return false, err
	}
	return s.client.SetNX(ctx, k, token, ttl).Result()
}

// Unlock releases a distributed lock only if the token matches.
// Safe to call when the lock has already expired or was never held — idempotent.
func (s *Store) Unlock(ctx context.Context, tenant, key, token string) error {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	// Ignore "0" return (lock not owned); treat it as a no-op, not an error.
	err = unlockScript.Run(ctx, s.client, []string{k}, token).Err()
	if err == goredis.Nil {
		return nil
	}
	return err
}
