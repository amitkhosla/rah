package redis

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/amitkhosla/rah/internal/config"
)

// Topology selects the Redis deployment model.
type Topology string

const (
	TopologySingle   Topology = "single"   // standalone node (default)
	TopologySentinel Topology = "sentinel" // HA with automatic failover, single logical master
	TopologyCluster  Topology = "cluster"  // horizontal sharding across N primaries (CRC16 slots)
)

// Stats is connection pool telemetry returned by Store.Stats().
// Mirrors datastore.PoolStats without importing that package.
type Stats struct {
	MaxOpen int64
	InUse   int64
	Waiters int64
}

// clientBundle wraps the go-redis client with topology-aware helpers.
type clientBundle struct {
	cmdable goredis.Cmdable
	statsFn func() Stats
	closeFn func() error
	// non-nil only for cluster topology; used by ListKeys to scan each master.
	cluster *goredis.ClusterClient
}

// parseTopology reads the topology from StoreConnection, defaulting to single.
func parseTopology(cfg config.StoreConfig) (Topology, error) {
	raw := strings.ToLower(strings.TrimSpace(cfg.Connection.Topology))
	switch Topology(raw) {
	case TopologySingle, "":
		return TopologySingle, nil
	case TopologySentinel:
		return TopologySentinel, nil
	case TopologyCluster:
		return TopologyCluster, nil
	default:
		return "", fmt.Errorf("unsupported redis topology %q (valid: single, sentinel, cluster)", raw)
	}
}

func newClientBundle(cfg config.StoreConfig, topology Topology) (*clientBundle, error) {
	poolSize := poolSizeFrom(cfg)
	username := cfg.Connection.Username
	password := cfg.Connection.Password
	maxRetries := cfg.Connection.MaxRetries
	minBackoff := parseBackoff(cfg.Connection.MinRetryBackoff)
	maxBackoff := parseBackoff(cfg.Connection.MaxRetryBackoff)

	switch topology {
	case TopologySingle:
		c := goredis.NewClient(&goredis.Options{
			Addr:            cfg.Connection.EffectiveAddress(),
			Username:        username,
			Password:        password,
			DB:              dbIndexFrom(cfg),
			PoolSize:        poolSize,
			MaxRetries:      maxRetries,
			MinRetryBackoff: minBackoff,
			MaxRetryBackoff: maxBackoff,
		})
		return &clientBundle{
			cmdable: c,
			statsFn: wrapPoolStats(c.PoolStats),
			closeFn: c.Close,
		}, nil

	case TopologySentinel:
		master := strings.TrimSpace(cfg.Connection.SentinelMaster)
		if master == "" {
			return nil, fmt.Errorf("redis sentinel topology requires sentinel_master in connection config")
		}
		c := goredis.NewFailoverClient(&goredis.FailoverOptions{
			MasterName:      master,
			SentinelAddrs:   nodeAddrs(cfg),
			Username:        username,
			Password:        password,
			PoolSize:        poolSize,
			MaxRetries:      maxRetries,
			MinRetryBackoff: minBackoff,
			MaxRetryBackoff: maxBackoff,
		})
		return &clientBundle{
			cmdable: c,
			statsFn: wrapPoolStats(c.PoolStats),
			closeFn: c.Close,
		}, nil

	case TopologyCluster:
		addrs := nodeAddrs(cfg)
		if len(addrs) == 0 {
			return nil, fmt.Errorf("redis cluster topology requires cluster_addrs or address in connection config")
		}
		cc := goredis.NewClusterClient(&goredis.ClusterOptions{
			Addrs:           addrs,
			Username:        username,
			Password:        password,
			PoolSize:        poolSize,
			MaxRetries:      maxRetries,
			MinRetryBackoff: minBackoff,
			MaxRetryBackoff: maxBackoff,
		})
		return &clientBundle{
			cmdable: cc,
			statsFn: wrapPoolStats(cc.PoolStats),
			closeFn: cc.Close,
			cluster: cc,
		}, nil

	default:
		return nil, fmt.Errorf("unknown topology %q", topology)
	}
}

// parseBackoff converts a config backoff string to a time.Duration for go-redis.
//   - ""  or "0"  â†’ 0 (go-redis uses its own default: 8ms min, 512ms max)
//   - "-1"        â†’ -1 (go-redis disables backoff â€” retries immediately)
//   - "50ms" etc. â†’ parsed duration
func parseBackoff(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0
	}
	if s == "-1" {
		return -1
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0 // fall back to go-redis default on bad input
	}
	return d
}

// wrapPoolStats converts a go-redis PoolStats method value into a Stats closure.
func wrapPoolStats(fn func() *goredis.PoolStats) func() Stats {
	return func() Stats {
		s := fn()
		return Stats{
			MaxOpen: int64(s.TotalConns),
			InUse:   int64(s.TotalConns - s.IdleConns),
			Waiters: int64(s.Misses),
		}
	}
}

// nodeAddrs returns the list of node addresses from ClusterAddrs, falling back
// to the single Address field. Used for both cluster seeds and sentinel addrs.
func nodeAddrs(cfg config.StoreConfig) []string {
	if len(cfg.Connection.ClusterAddrs) > 0 {
		return cfg.Connection.ClusterAddrs
	}
	if addr := cfg.Connection.EffectiveAddress(); addr != "" {
		return []string{addr}
	}
	return nil
}

func dbIndexFrom(cfg config.StoreConfig) int {
	if n, err := strconv.Atoi(cfg.Connection.Database); err == nil {
		return n
	}
	return 0
}

func poolSizeFrom(cfg config.StoreConfig) int {
	if cfg.Connection.PoolSize > 0 {
		return cfg.Connection.PoolSize
	}
	return 32
}

// NewUniversalClient creates a goredis.UniversalClient from cfg.
// The returned closeFn must be called when the client is no longer needed.
// All three topology types (single, sentinel, cluster) return a value that
// implements goredis.UniversalClient.
func NewUniversalClient(cfg config.StoreConfig) (goredis.UniversalClient, func() error, error) {
	topo, err := parseTopology(cfg)
	if err != nil {
		return nil, nil, err
	}
	bundle, err := newClientBundle(cfg, topo)
	if err != nil {
		return nil, nil, err
	}
	uc, ok := bundle.cmdable.(goredis.UniversalClient)
	if !ok {
		return nil, nil, fmt.Errorf("redis: client type %T does not implement UniversalClient", bundle.cmdable)
	}
	return uc, bundle.closeFn, nil
}
