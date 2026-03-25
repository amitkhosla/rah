package datastore

import (
	"context"
	"fmt"
	"rah/internal/config"
)

// NewStore creates a KeyValueStore for the given config and domain.
// ctx is forwarded to Redis/Dragonfly stores to control their IODeduper
// background goroutine lifetime; pass the gateway's root context.
func NewStore(ctx context.Context, cfg config.StoreConfig, domain config.DataDomain) (KeyValueStore, error) {
	switch cfg.Kind {
	case config.StoreDisk:
		return newDiskStore(cfg, string(domain))
	case config.StoreRedis:
		return newRedisStore(ctx, cfg, string(domain))
	case config.StoreMongoDB:
		return newMongoStore(cfg, string(domain)), nil
	case config.StoreDragonFly:
		return newDragonFlyStore(ctx, cfg, string(domain))
	case config.StorePostgreSQL:
		return newPostgreSQLStore(cfg, string(domain))
	case config.StoreCassandra:
		return newCassandraStore(cfg, string(domain)), nil
	default:
		return nil, fmt.Errorf("unsupported store kind: %q", cfg.Kind)
	}
}
