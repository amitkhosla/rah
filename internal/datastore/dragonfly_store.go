package datastore

import "rah/internal/config"

func newDragonFlyStore(cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	return newRedisAdapter(cfg, domain, "dragonfly")
}
