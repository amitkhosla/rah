package datastore

import "rah/internal/config"

func newCassandraStore(cfg config.StoreConfig, domain string) KeyValueStore {
	return newMemoryStore(cfg, "cassandra", domain)
}
