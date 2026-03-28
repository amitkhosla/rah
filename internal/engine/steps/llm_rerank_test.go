package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

func makeRerankCtx() *rctx.Context {
	return &rctx.Context{
		ByteSlots: make([][]byte, 48),
		IntSlots:  make([]int64, 16),
		BoolSlots: make([]bool, 8),
	}
}

func TestCohereRerank(t *testing.T) {
	// Create a mock Cohere API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req cohereRerankRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Return results in reverse order (reranked)
		respBody := `{"results":[{"index":1,"relevance_score":0.95,"document":{"text":"doc2"}},{"index":0,"relevance_score":0.80,"document":{"text":"doc1"}}]}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respBody))
	}))
	defer server.Close()

	ctx := makeRerankCtx()
	ctx.ResponseStatus = 200

	// Prepare query and docs
	query := []byte("test query")
	docs := []string{"doc1", "doc2"}
	docsJSON, _ := json.Marshal(docs)

	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = docsJSON

	// Create instruction
	cfg := RerankConfig{
		Provider:   "cohere",
		BaseURL:    server.URL,
		APIKey:     "test-key",
		Model:      "rerank-english-v3.0",
		QuerySlot:  0,
		DocsSlot:   1,
		ResultSlot: 2,
		TopN:       10,
		TimeoutMs:  5000,
	}

	instr := Rerank(cfg)
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	// Verify execution
	if nextPC != 1 {
		t.Errorf("expected PC=1, got %d", nextPC)
	}
	if ctx.ResponseStatus != 200 {
		t.Errorf("expected status 200, got %d", ctx.ResponseStatus)
	}

	// Verify result slot contains reranked documents
	var result []string
	if err := json.Unmarshal(ctx.ByteSlots[2], &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 docs, got %d", len(result))
	}
	if result[0] != "doc2" || result[1] != "doc1" {
		t.Errorf("unexpected reranked order: %v", result)
	}
}

func TestJinaRerank(t *testing.T) {
	// Create a mock Jina API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jina-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req jinaRerankRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		respBody := `{"results":[{"index":2,"relevance_score":0.92,"document":{"text":"doc3"}},{"index":0,"relevance_score":0.85,"document":{"text":"doc1"}}]}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respBody))
	}))
	defer server.Close()

	ctx := makeRerankCtx()
	ctx.ResponseStatus = 200

	query := []byte("search query")
	docs := []string{"doc1", "doc2", "doc3"}
	docsJSON, _ := json.Marshal(docs)

	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = docsJSON

	cfg := RerankConfig{
		Provider:   "jina",
		BaseURL:    server.URL,
		APIKey:     "jina-key",
		Model:      "jina-reranker-v2-base-multilingual",
		QuerySlot:  0,
		DocsSlot:   1,
		ResultSlot: 2,
		TopN:       5,
		TimeoutMs:  5000,
	}

	instr := Rerank(cfg)
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected PC=1, got %d", nextPC)
	}

	var result []string
	if err := json.Unmarshal(ctx.ByteSlots[2], &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 docs, got %d", len(result))
	}
	if result[0] != "doc3" || result[1] != "doc1" {
		t.Errorf("unexpected reranked order: %v", result)
	}
}

func TestRerankTopNLimiting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req cohereRerankRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Verify TopN was sent in request
		if req.TopN != 2 {
			http.Error(w, fmt.Sprintf("expected TopN=2, got %d", req.TopN), http.StatusBadRequest)
			return
		}

		respBody := `{"results":[{"index":1,"relevance_score":0.95,"document":{"text":"doc2"}},{"index":0,"relevance_score":0.80,"document":{"text":"doc1"}}]}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respBody))
	}))
	defer server.Close()

	ctx := makeRerankCtx()
	ctx.ResponseStatus = 200

	query := []byte("query")
	docs := []string{"doc1", "doc2", "doc3"}
	docsJSON, _ := json.Marshal(docs)

	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = docsJSON

	cfg := RerankConfig{
		Provider:   "cohere",
		BaseURL:    server.URL,
		APIKey:     "test-key",
		Model:      "rerank-english-v3.0",
		QuerySlot:  0,
		DocsSlot:   1,
		ResultSlot: 2,
		TopN:       2,
		TimeoutMs:  5000,
	}

	instr := Rerank(cfg)
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected PC=1, got %d", nextPC)
	}

	var result []string
	if err := json.Unmarshal(ctx.ByteSlots[2], &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 docs in result, got %d", len(result))
	}
}

func TestRerankEmptyDocs(t *testing.T) {
	ctx := makeRerankCtx()
	ctx.ResponseStatus = 200

	query := []byte("query")
	emptyDocs := []string{}
	docsJSON, _ := json.Marshal(emptyDocs)

	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = docsJSON

	cfg := RerankConfig{
		Provider:   "cohere",
		BaseURL:    "https://example.com",
		APIKey:     "key",
		Model:      "model",
		QuerySlot:  0,
		DocsSlot:   1,
		ResultSlot: 2,
		TimeoutMs:  5000,
	}

	instr := Rerank(cfg)
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected PC=1, got %d", nextPC)
	}

	// Result should be empty array
	var result []string
	if err := json.Unmarshal(ctx.ByteSlots[2], &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d docs", len(result))
	}
}

func TestRerankAPI429Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	ctx := makeRerankCtx()
	ctx.ResponseStatus = 200

	query := []byte("query")
	docs := []string{"doc1", "doc2"}
	docsJSON, _ := json.Marshal(docs)

	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = docsJSON

	cfg := RerankConfig{
		Provider:   "cohere",
		BaseURL:    server.URL,
		APIKey:     "key",
		Model:      "model",
		QuerySlot:  0,
		DocsSlot:   1,
		ResultSlot: 2,
		TimeoutMs:  5000,
	}

	instr := Rerank(cfg)
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != -1 {
		t.Errorf("expected StopPlan (-1), got %d", nextPC)
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected status 502, got %d", ctx.ResponseStatus)
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed to be true")
	}
}

func TestRerankAPI500Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	ctx := makeRerankCtx()
	ctx.ResponseStatus = 200

	query := []byte("query")
	docs := []string{"doc1"}
	docsJSON, _ := json.Marshal(docs)

	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = docsJSON

	cfg := RerankConfig{
		Provider:   RerankJina,
		BaseURL:    server.URL,
		APIKey:     "key",
		Model:      "model",
		QuerySlot:  0,
		DocsSlot:   1,
		ResultSlot: 2,
		TimeoutMs:  5000,
	}

	instr := Rerank(cfg)
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != -1 {
		t.Errorf("expected StopPlan (-1), got %d", nextPC)
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected status 502, got %d", ctx.ResponseStatus)
	}
}

func TestRerankResponseReordering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respBody := `{"results":[{"index":2,"relevance_score":0.98,"document":{"text":"doc3"}},{"index":0,"relevance_score":0.75,"document":{"text":"doc1"}},{"index":1,"relevance_score":0.85,"document":{"text":"doc2"}}]}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respBody))
	}))
	defer server.Close()

	ctx := makeRerankCtx()
	ctx.ResponseStatus = 200

	query := []byte("query")
	docs := []string{"doc1", "doc2", "doc3"}
	docsJSON, _ := json.Marshal(docs)

	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = docsJSON

	cfg := RerankConfig{
		Provider:   "cohere",
		BaseURL:    server.URL,
		APIKey:     "key",
		Model:      "model",
		QuerySlot:  0,
		DocsSlot:   1,
		ResultSlot: 2,
		TimeoutMs:  5000,
	}

	instr := Rerank(cfg)
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected PC=1, got %d", nextPC)
	}

	var result []string
	if err := json.Unmarshal(ctx.ByteSlots[2], &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	// Verify order matches API response order (not original input order)
	expected := []string{"doc3", "doc1", "doc2"}
	if len(result) != len(expected) {
		t.Errorf("expected %d docs, got %d", len(expected), len(result))
	}
	for i, v := range result {
		if i < len(expected) && v != expected[i] {
			t.Errorf("result[%d]: expected %q, got %q", i, expected[i], v)
		}
	}
}

func TestRerankNoAuthHeader(t *testing.T) {
	// Verify that missing API key doesn't cause error - server decides
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server should still work without Authorization header
		respBody := `{"results":[{"index":0,"relevance_score":0.90,"document":{"text":"doc1"}}]}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respBody))
	}))
	defer server.Close()

	ctx := makeRerankCtx()
	ctx.ResponseStatus = 200

	query := []byte("query")
	docs := []string{"doc1"}
	docsJSON, _ := json.Marshal(docs)

	ctx.ByteSlots[0] = query
	ctx.ByteSlots[1] = docsJSON

	cfg := RerankConfig{
		Provider:   RerankCohere,
		BaseURL:    server.URL,
		APIKey:     "", // No API key
		Model:      "model",
		QuerySlot:  0,
		DocsSlot:   1,
		ResultSlot: 2,
		TimeoutMs:  5000,
	}

	instr := Rerank(cfg)
	state := &engine.ExecutionState{PC: 0}
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected PC=1, got %d", nextPC)
	}
	if ctx.Failed {
		t.Errorf("expected success with no API key")
	}
}
