package document

import "context"

// GetRequest retrieves a single document by ID or filter
type GetRequest struct {
	Collection string
	ID         string
	Filter     []byte
}

// PutRequest inserts or replaces a document
type PutRequest struct {
	Collection string
	Document   []byte
	ID         string
	Upsert     bool
}

// DeleteRequest deletes documents matching a filter
type DeleteRequest struct {
	Collection string
	Filter     []byte
}

// QueryRequest queries documents with filtering, projection, sorting, limit, and skip
type QueryRequest struct {
	Collection string
	Filter     []byte
	Projection []byte
	Sort       []byte
	Limit      int
	Skip       int
}

// ExecuteRequest executes a raw database command
type ExecuteRequest struct {
	Command []byte
}

// GetManyRequest fetches multiple documents by ID from the same collection.
type GetManyRequest struct {
	Collection string
	IDs        []string
}

// PutManyRequest upserts multiple documents into the same collection.
type PutManyRequest struct {
	Collection string
	Docs       map[string][]byte // id → JSON document bytes
	Upsert     bool
}

// DeleteManyRequest deletes multiple documents by ID from the same collection.
type DeleteManyRequest struct {
	Collection string
	IDs        []string
}

// DocumentProvider is the interface for document storage operations
type DocumentProvider interface {
	Get(ctx context.Context, req GetRequest) ([]byte, error)
	Put(ctx context.Context, req PutRequest) error
	Delete(ctx context.Context, req DeleteRequest) error
	Query(ctx context.Context, req QueryRequest) ([]byte, error)
	Count(ctx context.Context, req QueryRequest) (int64, error)
	Execute(ctx context.Context, req ExecuteRequest) ([]byte, error)
	Ping(ctx context.Context) error
	Close() error
	GetMany(ctx context.Context, req GetManyRequest) (map[string][]byte, error)
	PutMany(ctx context.Context, req PutManyRequest) error
	DeleteMany(ctx context.Context, req DeleteManyRequest) error
}
