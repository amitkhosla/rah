package datastore

import "rah/internal/config"

func newDragonFlyStore(cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	return newRedisStoreWithKind(cfg, domain, "dragonfly")
}
