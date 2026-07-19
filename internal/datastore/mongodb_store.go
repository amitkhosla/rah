package datastore

import (
	"log"

	"github.com/amitkhosla/rah/internal/config"
)

func newMongoStore(cfg config.StoreConfig, domain string) KeyValueStore {
	log.Printf("[datastore] WARNING: MongoDB backend is not yet implemented for domain %q — falling back to in-memory store. Data will not persist across restarts.", domain)
	return newMemoryStore(cfg, "mongodb", domain)
}
