package datastore

import "rah/internal/config"

func newMongoStore(cfg config.StoreConfig, domain string) KeyValueStore {
	return newMemoryStore(cfg, "mongodb", domain)
}
