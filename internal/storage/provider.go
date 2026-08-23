package storage

import (
	"context"
	"time"
)

// storageProvider is the internal interface all backend implementations satisfy.
type storageProvider interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, content []byte, contentType string) error
	Delete(ctx context.Context, key string) error
}

// presigner is implemented by providers that support generating time-limited signed URLs.
// Type-assert a storageProvider to presigner before calling Presign.
type presigner interface {
	Presign(ctx context.Context, key, method string, expirySeconds int) (string, error)
}

// lister is implemented by providers that support listing objects by key prefix.
type lister interface {
	List(ctx context.Context, prefix string) ([]ListItem, error)
}

// ListItem describes one object returned by a List call.
type ListItem struct {
	Key         string
	Size        int64
	ContentType string
	UpdatedAt   time.Time
}
