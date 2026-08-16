package events

import (
	"context"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// DedupStore is a distributed-safe store for deduplication keys.
// IsDuplicate returns true if the key was seen within the given window.
// If it returns false, the key is recorded for the window duration.
type DedupStore interface {
	IsDuplicate(ctx context.Context, key string, window time.Duration) (bool, error)
	Close()
}

// InMemoryDedupStore is a single-instance dedup store backed by a sync.Map.
type InMemoryDedupStore struct {
	seen   sync.Map // string → time.Time (expiry)
	stopCh chan struct{}
}

func newInMemoryDedupStore(sweepInterval time.Duration) *InMemoryDedupStore {
	s := &InMemoryDedupStore{stopCh: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				s.seen.Range(func(k, v any) bool {
					if now.After(v.(time.Time)) {
						s.seen.Delete(k)
					}
					return true
				})
			case <-s.stopCh:
				return
			}
		}
	}()
	return s
}

func (s *InMemoryDedupStore) IsDuplicate(_ context.Context, key string, window time.Duration) (bool, error) {
	now := time.Now()
	if exp, ok := s.seen.Load(key); ok {
		if now.Before(exp.(time.Time)) {
			return true, nil
		}
	}
	s.seen.Store(key, now.Add(window))
	return false, nil
}

func (s *InMemoryDedupStore) Close() {
	close(s.stopCh)
}

// RedisDedupStore uses Redis SET NX EX for distributed deduplication.
type RedisDedupStore struct {
	client goredis.UniversalClient
	prefix string
}

func newRedisDedupStore(client goredis.UniversalClient) *RedisDedupStore {
	return &RedisDedupStore{client: client, prefix: "rah:dedup:"}
}

func (s *RedisDedupStore) IsDuplicate(ctx context.Context, key string, window time.Duration) (bool, error) {
	redisKey := s.prefix + key
	// SET NX EX: only set if not exists; returns true if key was set (not duplicate)
	set, err := s.client.SetNX(ctx, redisKey, 1, window).Result()
	if err != nil {
		return false, err
	}
	// set=true means key was newly created → not a duplicate
	return !set, nil
}

func (s *RedisDedupStore) Close() {}
