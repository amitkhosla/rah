package datastore

import "github.com/amitkhosla/rah/internal/config"

func newDiskStore(cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	return newFileStore(cfg, domain)
}
