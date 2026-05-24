package apikey

import "context"

// Store is the persistence interface implemented by control.DataStoreManager.
// A nil Store is valid — the gateway runs in memory-only mode (acceptable in
// tests or ephemeral deployments).
type Store interface {
	PutApp(ctx context.Context, appID uint32, raw []byte) error
	DeleteApp(ctx context.Context, appID uint32) error
	ListApps(ctx context.Context) ([][]byte, error)

	PutKey(ctx context.Context, keyID uint32, raw []byte) error
	DeleteKey(ctx context.Context, keyID uint32) error
	ListKeys(ctx context.Context) ([][]byte, error)
}
