package datastore

import "rah/internal/config"

func newDragonFlyStore(cfg config.StoreConfig, domain string) KeyValueStore {
	return newMemoryStore(cfg, "dragonfly", domain)
}
