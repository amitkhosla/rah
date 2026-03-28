package vectorstore

import (
	"fmt"

	"rah/internal/config"
)

// NewVectorStore creates a VectorStore for the given config.
// The resolved API key should be passed in (resolved from APIKeyRef before calling).
func NewVectorStore(cfg config.VectorStoreConfig, resolvedAPIKey string) (VectorStore, error) {
	switch cfg.Kind {
	case config.VectorStoreQdrant:
		return newQdrantStore(cfg, resolvedAPIKey), nil
	case config.VectorStoreChroma:
		return newChromaStore(cfg, resolvedAPIKey), nil
	case config.VectorStoreWeaviate:
		return newWeaviateStore(cfg, resolvedAPIKey), nil
	case config.VectorStoreRedis:
		return newRedisVectorStore(cfg, resolvedAPIKey), nil
	case config.VectorStoreMongoDB:
		return newMongoVectorStore(cfg, resolvedAPIKey), nil
	case config.VectorStorePgVector:
		return newPgVectorStore(cfg, resolvedAPIKey), nil
	case config.VectorStoreHTTP:
		return newHTTPVectorStore(cfg, resolvedAPIKey), nil
	default:
		return nil, fmt.Errorf("unsupported vector store kind: %q", cfg.Kind)
	}
}
