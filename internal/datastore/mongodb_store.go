package datastore

import "github.com/amitkhosla/rah/internal/config"

func newMongoStore(cfg config.StoreConfig, domain string) KeyValueStore {
	return newMemoryStore(cfg, "mongodb", domain)
}
