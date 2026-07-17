package datastore

import "github.com/amitkhosla/rah/internal/config"

func newCassandraStore(cfg config.StoreConfig, domain string) KeyValueStore {
	return newMemoryStore(cfg, "cassandra", domain)
}
