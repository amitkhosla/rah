package vectorstore

import "context"

// VectorStore is the generic contract for all vector database backends.
// All implementations are HTTP-based (no CGo) so they run on any platform.
//
// The interface mirrors the datastore.KeyValueStore pattern: a factory
// creates a named implementation from config; steps reference it by name.
type VectorStore interface {
	// Search returns the top-K most similar vectors to the query vector.
	// collection is the index/collection/namespace name.
	// minScore filters results below a similarity threshold (0 = return all).
	// filter is optional metadata pre-filter (backend-dependent, may be nil).
	Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]SearchResult, error)

	// Upsert inserts or updates vectors with their content and metadata.
	Upsert(ctx context.Context, collection string, items []VectorItem) error

	// Delete removes vectors by ID from a collection.
	Delete(ctx context.Context, collection string, ids []string) error

	// Kind returns the backend name ("qdrant", "chroma", "weaviate", etc.)
	Kind() string

	// Close releases any persistent connections.
	Close() error
}

// SearchResult is one result from a similarity search.
type SearchResult struct {
	// ID is the vector's unique identifier in the store.
	ID string
	// Score is the similarity score (higher = more similar; range depends on backend).
	Score float32
	// Content is the text associated with this vector (the original chunk).
	Content string
	// Metadata holds arbitrary fields stored alongside the vector.
	Metadata map[string]any
}

// VectorItem is the unit of storage for upsert operations.
type VectorItem struct {
	// ID is the unique identifier. If empty, the backend generates one.
	ID string
	// Vector is the float32 embedding. Must match the collection's configured dimension.
	Vector []float32
	// Content is the source text chunk. Stored as metadata under "content" key.
	Content string
	// Metadata holds arbitrary fields to store alongside the vector.
	Metadata map[string]any
}
