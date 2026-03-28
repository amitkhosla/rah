package vectorstore

import (
	"testing"

	"rah/internal/config"
)

func TestNewVectorStore_UnknownKind_Error(t *testing.T) {
	_, err := NewVectorStore(config.VectorStoreConfig{
		Name: "test",
		Kind: config.VectorStoreKind("unknown_backend"),
		URL:  "http://localhost:9999",
	}, "")
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}

func TestNewVectorStore_AllKinds_NoError(t *testing.T) {
	kinds := []config.VectorStoreKind{
		config.VectorStoreQdrant,
		config.VectorStoreChroma,
		config.VectorStoreWeaviate,
		config.VectorStoreRedis,
		config.VectorStoreMongoDB,
		config.VectorStorePgVector,
		config.VectorStoreHTTP,
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			store, err := NewVectorStore(config.VectorStoreConfig{
				Name: "test-" + string(kind),
				Kind: kind,
				URL:  "http://localhost:9999",
			}, "dummy-key")
			if err != nil {
				t.Fatalf("unexpected error for kind %q: %v", kind, err)
			}
			if store == nil {
				t.Fatal("expected non-nil store")
			}
			if store.Kind() != string(kind) {
				t.Errorf("Kind() = %q, want %q", store.Kind(), string(kind))
			}
			if err := store.Close(); err != nil {
				t.Errorf("Close() returned unexpected error: %v", err)
			}
		})
	}
}
