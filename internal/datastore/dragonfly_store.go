package datastore

import (
	"context"

	"github.com/amitkhosla/rah/internal/config"
)

func newDragonFlyStore(ctx context.Context, cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	return newRedisAdapter(ctx, cfg, domain, "dragonfly")
}
