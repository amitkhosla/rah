package steps

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/vectorstore"
)

// mockSemanticStore is a test mock for VectorStore.
type mockSemanticStore struct {
	upserted []vectorstore.VectorItem
	results  []vectorstore.SearchResult
}

func (m *mockSemanticStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]vectorstore.SearchResult, error) {
	var out []vectorstore.SearchResult
	for _, r := range m.results {
		if r.Score >= minScore {
			out = append(out, r)
		}
	}
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func (m *mockSemanticStore) Upsert(ctx context.Context, collection string, items []vectorstore.VectorItem) error {
	m.upserted = append(m.upserted, items...)
	return nil
}

func (m *mockSemanticStore) Delete(ctx context.Context, collection string, ids []string) error {
	return nil
}

func (m *mockSemanticStore) Kind() string {
	return "mock"
}

func (m *mockSemanticStore) Close() error {
	return nil
}

// TestSemanticCacheGetMiss verifies cache miss when store is empty.
func TestSemanticCacheGetMiss(t *testing.T) {
	// Setup: mock embedding server returning a valid vector
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	store := &mockSemanticStore{
		results: []vectorstore.SearchResult{}, // empty results
	}

	cfg := SemanticCacheGetConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
		Store:      store,
		Collection: "test",
		QuerySlot:  0,
		ResultSlot: 1,
		HitSlot:    0,
		MinScore:   0.92,
	}

	instr := SemanticCacheGet(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 2),
		BoolSlots: make([]bool, 1),
	}
	ctx.ByteSlots[0] = []byte("what is love")

	state := &engine.ExecutionState{}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected nextPC=1, got %d", nextPC)
	}
	if ctx.BoolSlots[0] != false {
		t.Errorf("expected HitSlot=false on miss, got true")
	}
}

// TestSemanticCacheGetHit verifies cache hit returns stored response.
func TestSemanticCacheGetHit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	store := &mockSemanticStore{
		results: []vectorstore.SearchResult{
			{
				ID:       "result-1",
				Score:    0.95,
				Content:  "Love is patient, love is kind",
				Metadata: nil,
			},
		},
	}

	cfg := SemanticCacheGetConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
		Store:      store,
		Collection: "test",
		QuerySlot:  0,
		ResultSlot: 1,
		HitSlot:    0,
		MinScore:   0.92,
	}

	instr := SemanticCacheGet(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 2),
		BoolSlots: make([]bool, 1),
	}
	ctx.ByteSlots[0] = []byte("what is love")

	state := &engine.ExecutionState{}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected nextPC=1, got %d", nextPC)
	}
	if ctx.BoolSlots[0] != true {
		t.Errorf("expected HitSlot=true on hit, got false")
	}
	if string(ctx.ByteSlots[1]) != "Love is patient, love is kind" {
		t.Errorf("expected cached response in ResultSlot, got %q", string(ctx.ByteSlots[1]))
	}
}

// TestSemanticCacheGetEmptyQuery skips embedding and returns miss.
func TestSemanticCacheGetEmptyQuery(t *testing.T) {
	store := &mockSemanticStore{}

	cfg := SemanticCacheGetConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
		},
		Store:      store,
		Collection: "test",
		QuerySlot:  0,
		ResultSlot: 1,
		HitSlot:    0,
		MinScore:   0.92,
	}

	instr := SemanticCacheGet(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 2),
		BoolSlots: make([]bool, 1),
	}
	ctx.ByteSlots[0] = []byte("") // empty query

	state := &engine.ExecutionState{}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected nextPC=1, got %d", nextPC)
	}
	if ctx.BoolSlots[0] != false {
		t.Errorf("expected HitSlot=false on empty query, got true")
	}
}

// TestSemanticCachePutStoresQuery verifies put stores query-response pair.
func TestSemanticCachePutStoresQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	store := &mockSemanticStore{}

	cfg := SemanticCachePutConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
		Store:        store,
		Collection:   "test",
		QuerySlot:    0,
		ResponseSlot: 1,
	}

	instr := SemanticCachePut(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 2),
	}
	ctx.ByteSlots[0] = []byte("what is love")
	ctx.ByteSlots[1] = []byte("Love is patient, love is kind")

	state := &engine.ExecutionState{}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected nextPC=1, got %d", nextPC)
	}
	if len(store.upserted) != 1 {
		t.Errorf("expected 1 upserted item, got %d", len(store.upserted))
	}
	if len(store.upserted) > 0 {
		item := store.upserted[0]
		if item.Content != "Love is patient, love is kind" {
			t.Errorf("expected content to match, got %q", item.Content)
		}
		if len(item.Vector) != 3 {
			t.Errorf("expected vector dimension 3, got %d", len(item.Vector))
		}
	}
}

// TestSemanticCachePutEmptyResponse skips upsert.
func TestSemanticCachePutEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	store := &mockSemanticStore{}

	cfg := SemanticCachePutConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
		Store:        store,
		Collection:   "test",
		QuerySlot:    0,
		ResponseSlot: 1,
	}

	instr := SemanticCachePut(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 2),
	}
	ctx.ByteSlots[0] = []byte("what is love")
	ctx.ByteSlots[1] = []byte("") // empty response

	state := &engine.ExecutionState{}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected nextPC=1, got %d", nextPC)
	}
	// With empty response, we still embed and upsert, but with empty content
	if len(store.upserted) != 1 {
		t.Errorf("expected 1 upserted item, got %d", len(store.upserted))
	}
}

// TestSemanticCacheMinScoreThreshold verifies results below threshold are rejected.
func TestSemanticCacheMinScoreThreshold(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	store := &mockSemanticStore{
		results: []vectorstore.SearchResult{
			{
				ID:       "result-1",
				Score:    0.85, // below 0.92 threshold
				Content:  "Below threshold result",
				Metadata: nil,
			},
		},
	}

	cfg := SemanticCacheGetConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
		Store:      store,
		Collection: "test",
		QuerySlot:  0,
		ResultSlot: 1,
		HitSlot:    0,
		MinScore:   0.92,
	}

	instr := SemanticCacheGet(cfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 2),
		BoolSlots: make([]bool, 1),
	}
	ctx.ByteSlots[0] = []byte("query")

	state := &engine.ExecutionState{}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected nextPC=1, got %d", nextPC)
	}
	if ctx.BoolSlots[0] != false {
		t.Errorf("expected HitSlot=false when result below threshold, got true")
	}
}

// TestSemanticCacheRoundTrip verifies put then get works end-to-end.
func TestSemanticCacheRoundTrip(t *testing.T) {
	embedCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embedCount++
		response := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	store := &mockSemanticStore{}

	// Step 1: Put the query-response pair
	putCfg := SemanticCachePutConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
		Store:        store,
		Collection:   "test",
		QuerySlot:    0,
		ResponseSlot: 1,
	}

	putInstr := SemanticCachePut(putCfg)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 2),
	}
	query := []byte("hello world")
	response := []byte("Hello there!")
	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = response

	state := &engine.ExecutionState{}
	nextPC := putInstr.Action(ctx, state)

	if nextPC != 1 {
		t.Fatalf("put failed: expected nextPC=1, got %d", nextPC)
	}
	if len(store.upserted) != 1 {
		t.Fatalf("put failed: expected 1 upserted item, got %d", len(store.upserted))
	}

	// Step 2: Get the cached response
	getCfg := SemanticCacheGetConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
		Store:      store,
		Collection: "test",
		QuerySlot:  0,
		ResultSlot: 1,
		HitSlot:    0,
		MinScore:   0.92,
	}

	// Populate store results from what was upserted
	store.results = []vectorstore.SearchResult{
		{
			ID:       store.upserted[0].ID,
			Score:    0.95, // above threshold
			Content:  store.upserted[0].Content,
			Metadata: nil,
		},
	}

	getInstr := SemanticCacheGet(getCfg)

	ctx2 := &rctx.Context{
		ByteSlots: make([][]byte, 2),
		BoolSlots: make([]bool, 1),
	}
	ctx2.ByteSlots[0] = query // same query

	state2 := &engine.ExecutionState{}
	nextPC2 := getInstr.Action(ctx2, state2)

	if nextPC2 != 1 {
		t.Errorf("get failed: expected nextPC=1, got %d", nextPC2)
	}
	if !ctx2.BoolSlots[0] {
		t.Errorf("get failed: expected HitSlot=true, got false")
	}
	if string(ctx2.ByteSlots[1]) != "Hello there!" {
		t.Errorf("get failed: expected cached response, got %q", string(ctx2.ByteSlots[1]))
	}
}

// BenchmarkSemanticCacheGet benchmarks semantic cache lookup.
func BenchmarkSemanticCacheGet(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": []float64{0.1, 0.2, 0.3}},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	store := &mockSemanticStore{
		results: []vectorstore.SearchResult{
			{
				ID:       "cached-1",
				Score:    0.95,
				Content:  "Cached response here",
				Metadata: nil,
			},
		},
	}

	cfg := SemanticCacheGetConfig{
		EmbedCfg: EmbedTextConfig{
			Provider: EmbedProviderOpenAI,
			Model:    "text-embedding-3-small",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
		Store:      store,
		Collection: "test",
		QuerySlot:  0,
		ResultSlot: 1,
		HitSlot:    0,
		MinScore:   0.92,
	}

	instr := SemanticCacheGet(cfg)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 2),
		BoolSlots: make([]bool, 1),
	}
	ctx.ByteSlots[0] = []byte("benchmark query")

	state := &engine.ExecutionState{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		instr.Action(ctx, state)
	}
}
