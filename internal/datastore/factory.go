package datastore

import (
	"fmt"
	"rah/internal/config"
)

func NewStore(cfg config.StoreConfig, domain config.DataDomain) (KeyValueStore, error) {
	switch cfg.Kind {
	case config.StoreDisk:
		return newDiskStore(cfg, string(domain))
	case config.StoreRedis:
		return newRedisStore(cfg, string(domain)), nil
	case config.StoreMongoDB:
		return newMongoStore(cfg, string(domain)), nil
	case config.StoreDragonFly:
		return newDragonFlyStore(cfg, string(domain)), nil
	case config.StorePostgreSQL:
		return newPostgreSQLStore(cfg, string(domain)), nil
	case config.StoreCassandra:
		return newCassandraStore(cfg, string(domain)), nil
	default:
		return nil, fmt.Errorf("unsupported store kind: %q", cfg.Kind)
	}
}
