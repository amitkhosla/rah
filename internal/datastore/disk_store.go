package datastore

import "rah/internal/config"

func newDiskStore(cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	return newFileStore(cfg, domain)
}
