package redis

import (
	"context"
	"errors"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

// ZMember is a scored member of a sorted set.
// Mirrors datastore.ZMember without importing that package.
type ZMember struct {
	Score  float64
	Member string
}

// ZAdd adds or updates members in a sorted set.
// If a member already exists its score is updated.
func (s *Store) ZAdd(ctx context.Context, tenant, key string, members ...ZMember) error {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	zms := make([]goredis.Z, len(members))
	for i, m := range members {
		zms[i] = goredis.Z{Score: m.Score, Member: m.Member}
	}
	return s.client.ZAdd(ctx, k, zms...).Err()
}

// ZRem removes members from a sorted set. No-op for members that do not exist.
func (s *Store) ZRem(ctx context.Context, tenant, key string, members ...string) error {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	ifaces := make([]any, len(members))
	for i, m := range members {
		ifaces[i] = m
	}
	return s.client.ZRem(ctx, k, ifaces...).Err()
}

// ZRange returns all members ordered by score ascending (lowest score first).
func (s *Store) ZRange(ctx context.Context, tenant, key string) ([]ZMember, error) {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return nil, err
	}
	res, err := s.client.ZRangeWithScores(ctx, k, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	return toZMembers(res), nil
}

// ZRangeByScore returns members whose score falls within [min, max] inclusive,
// ordered by score ascending.
func (s *Store) ZRangeByScore(ctx context.Context, tenant, key string, min, max float64) ([]ZMember, error) {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return nil, err
	}
	res, err := s.client.ZRangeByScoreWithScores(ctx, k, &goredis.ZRangeBy{
		Min: fmt.Sprintf("%g", min),
		Max: fmt.Sprintf("%g", max),
	}).Result()
	if err != nil {
		return nil, err
	}
	return toZMembers(res), nil
}

// ZScore returns the score of a member. found=false if the member does not exist.
func (s *Store) ZScore(ctx context.Context, tenant, key, member string) (float64, bool, error) {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return 0, false, err
	}
	score, err := s.client.ZScore(ctx, k, member).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return score, true, nil
}

// ZCard returns the number of members in the sorted set.
func (s *Store) ZCard(ctx context.Context, tenant, key string) (int64, error) {
	k, err := scopedKey(tenant, s.domain, key)
	if err != nil {
		return 0, err
	}
	return s.client.ZCard(ctx, k).Result()
}

func toZMembers(zs []goredis.Z) []ZMember {
	out := make([]ZMember, len(zs))
	for i, z := range zs {
		out[i] = ZMember{Score: z.Score, Member: z.Member.(string)}
	}
	return out
}
