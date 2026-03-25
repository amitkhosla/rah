package cache

import (
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"rah/internal/config"
)

// redisBackend implements CacheBackend using Redis or Dragonfly.
//
// Key format:   rah:c:{tenantID}:{32-char hex of Hash128(tenantID, key)}
// Value format: [4-byte expiry uint32 LE][raw value bytes]
//
// The 4-byte expiry is embedded in the stored value (same encoding as
// diskBackend) so that Get can return the authoritative expiry timestamp
// without a second round-trip. The Redis TTL is also set so that Redis
// evicts entries automatically.
type redisBackend struct {
	client  goredis.Cmdable
	closeFn func() error
}

// NewRedisBackend creates a CacheBackend backed by Redis or Dragonfly.
// ctx is used to cancel background work (e.g. from the gateway root context).
// Supports single, sentinel, and cluster topologies via cfg.Connection.Topology.
func NewRedisBackend(ctx context.Context, cfg config.StoreConfig) (CacheBackend, error) {
	client, closeFn, err := newRedisClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("cache redis backend: %w", err)
	}
	// Ping to validate connectivity at startup.
	if err := client.Ping(ctx).Err(); err != nil {
		_ = closeFn()
		return nil, fmt.Errorf("cache redis backend: ping failed: %w", err)
	}
	return &redisBackend{client: client, closeFn: closeFn}, nil
}

// redisKey builds the Redis key for (tenantID, key).
func redisKey(tenantID uint16, key []byte) string {
	fp := Hash128(tenantID, key)
	const hx = "0123456789abcdef"
	name := make([]byte, 32)
	for i, b := range fp {
		name[i*2] = hx[b>>4]
		name[i*2+1] = hx[b&0xf]
	}
	return "rah:c:" + strconv.FormatUint(uint64(tenantID), 10) + ":" + string(name)
}

func (r *redisBackend) Get(tenantID uint16, key []byte) ([]byte, uint32, bool) {
	k := redisKey(tenantID, key)
	data, err := r.client.Get(context.Background(), k).Bytes()
	if err != nil || len(data) < 4 {
		return nil, 0, false
	}
	expiry := binary.LittleEndian.Uint32(data[:4])
	if expiry > 0 && expiry < uint32(time.Now().Unix()) {
		// Expired — lazy delete.
		r.client.Del(context.Background(), k)
		return nil, 0, false
	}
	// Return a copy so callers can't observe the internal buffer.
	val := make([]byte, len(data)-4)
	copy(val, data[4:])
	return val, expiry, true
}

func (r *redisBackend) Set(tenantID uint16, key []byte, value []byte, expiry uint32) error {
	k := redisKey(tenantID, key)

	buf := make([]byte, 4+len(value))
	binary.LittleEndian.PutUint32(buf[:4], expiry)
	copy(buf[4:], value)

	var ttl time.Duration
	if expiry > 0 {
		remaining := int64(expiry) - time.Now().Unix()
		if remaining <= 0 {
			return nil // already expired; skip the write
		}
		ttl = time.Duration(remaining) * time.Second
	}
	// ttl == 0 means no expiry in go-redis SetArgs (persistent).
	return r.client.Set(context.Background(), k, buf, ttl).Err()
}

func (r *redisBackend) Delete(tenantID uint16, key []byte) error {
	return r.client.Del(context.Background(), redisKey(tenantID, key)).Err()
}

// Sweep is a no-op for Redis: TTL-based eviction is handled by Redis itself.
func (r *redisBackend) Sweep() int { return 0 }

func (r *redisBackend) Close() error { return r.closeFn() }

// ── Redis client construction ─────────────────────────────────────────────────

func newRedisClient(cfg config.StoreConfig) (goredis.Cmdable, func() error, error) {
	topo := strings.ToLower(strings.TrimSpace(cfg.Connection.Topology))
	poolSize := cfg.Connection.PoolSize
	if poolSize <= 0 {
		poolSize = 32
	}

	switch topo {
	case "", "single":
		c := goredis.NewClient(&goredis.Options{
			Addr:     cfg.Connection.Address,
			Username: cfg.Connection.Username,
			Password: cfg.Connection.Password,
			PoolSize: poolSize,
		})
		return c, c.Close, nil

	case "sentinel":
		if cfg.Connection.SentinelMaster == "" {
			return nil, nil, fmt.Errorf("sentinel topology requires sentinel_master")
		}
		c := goredis.NewFailoverClient(&goredis.FailoverOptions{
			MasterName:    cfg.Connection.SentinelMaster,
			SentinelAddrs: cfg.Connection.ClusterAddrs,
			Username:      cfg.Connection.Username,
			Password:      cfg.Connection.Password,
			PoolSize:      poolSize,
		})
		return c, c.Close, nil

	case "cluster":
		addrs := cfg.Connection.ClusterAddrs
		if len(addrs) == 0 && cfg.Connection.Address != "" {
			addrs = []string{cfg.Connection.Address}
		}
		c := goredis.NewClusterClient(&goredis.ClusterOptions{
			Addrs:    addrs,
			Username: cfg.Connection.Username,
			Password: cfg.Connection.Password,
			PoolSize: poolSize,
		})
		return c, c.Close, nil

	default:
		return nil, nil, fmt.Errorf("unsupported topology %q (valid: single, sentinel, cluster)", topo)
	}
}
