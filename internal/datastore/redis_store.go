package datastore

import "rah/internal/config"

func newRedisStore(cfg config.StoreConfig, domain string) KeyValueStore {
	return newMemoryStore(cfg, "redis", domain)
}
