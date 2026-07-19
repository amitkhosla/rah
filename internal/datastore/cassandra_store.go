package datastore

import (
	"log"

	"github.com/amitkhosla/rah/internal/config"
)

func newCassandraStore(cfg config.StoreConfig, domain string) KeyValueStore {
	log.Printf("[datastore] WARNING: Cassandra backend is not yet implemented for domain %q — falling back to in-memory store. Data will not persist across restarts.", domain)
	return newMemoryStore(cfg, "cassandra", domain)
}
