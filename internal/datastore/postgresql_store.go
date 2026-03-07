package datastore

import "rah/internal/config"

func newPostgreSQLStore(cfg config.StoreConfig, domain string) KeyValueStore {
	return newMemoryStore(cfg, "postgresql", domain)
}
