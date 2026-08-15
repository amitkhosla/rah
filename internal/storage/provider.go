package storage

import "context"

// storageProvider is the internal interface all backend implementations satisfy.
type storageProvider interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, content []byte, contentType string) error
	Delete(ctx context.Context, key string) error
}
