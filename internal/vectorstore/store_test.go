package vectorstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"rah/internal/config"
)

// setupHTTPVectorServer starts a test server handling /search, /upsert, /delete.
// Each handler records the request body and returns a canned response.
type recordedRequest struct {
	method string
	path   string
	body   map[string]any
}

func setupHTTPVectorServer(t *testing.T, records *[]recordedRequest) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		*records = append(*records, recordedRequest{method: r.Method, path: r.URL.Path, body: body})
		resp := map[string]any{
			"results": []map[string]any{
				{
					"id":       "abc123",
					"score":    0.92,
					"content":  "hello world",
					"metadata": map[string]any{"source": "test"},
				},
				{
					"id":      "def456",
					"score":   0.85,
					"content": "foo bar",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/upsert", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		*records = append(*records, recordedRequest{method: r.Method, path: r.URL.Path, body: body})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		*records = append(*records, recordedRequest{method: r.Method, path: r.URL.Path, body: body})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"deleted": 2})
	})

	return httptest.NewServer(mux)
}

func newTestHTTPVectorStore(url string) *httpVectorStore {
	return newHTTPVectorStore(config.VectorStoreConfig{
		Name: "test",
		Kind: config.VectorStoreHTTP,
		URL:  url,
	}, "test-key")
}

// TestHTTPVectorStore_Search verifies that Search parses the server response correctly.
func TestHTTPVectorStore_Search(t *testing.T) {
	var records []recordedRequest
	srv := setupHTTPVectorServer(t, &records)
	defer srv.Close()

	store := newTestHTTPVectorStore(srv.URL)

	vec := []float32{0.1, 0.2, 0.3}
	results, err := store.Search(context.Background(), "my_collection", vec, 5, 0.5, nil)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}

	// Should have 2 results
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify first result
	r0 := results[0]
	if r0.ID != "abc123" {
		t.Errorf("results[0].ID = %q, want %q", r0.ID, "abc123")
	}
	if r0.Score != 0.92 {
		t.Errorf("results[0].Score = %v, want 0.92", r0.Score)
	}
	if r0.Content != "hello world" {
		t.Errorf("results[0].Content = %q, want %q", r0.Content, "hello world")
	}
	if r0.Metadata["source"] != "test" {
		t.Errorf("results[0].Metadata[source] = %v, want %q", r0.Metadata["source"], "test")
	}

	// Verify second result
	r1 := results[1]
	if r1.ID != "def456" {
		t.Errorf("results[1].ID = %q, want %q", r1.ID, "def456")
	}

	// Check request was sent correctly
	if len(records) != 1 {
		t.Fatalf("expected 1 recorded request, got %d", len(records))
	}
	req := records[0]
	if req.path != "/search" {
		t.Errorf("request path = %q, want /search", req.path)
	}
	if req.method != http.MethodPost {
		t.Errorf("request method = %q, want POST", req.method)
	}
	if req.body["collection"] != "my_collection" {
		t.Errorf("request body collection = %v, want %q", req.body["collection"], "my_collection")
	}
	if req.body["top_k"] != float64(5) {
		t.Errorf("request body top_k = %v, want 5", req.body["top_k"])
	}
}

// TestHTTPVectorStore_Upsert verifies upsert sends items in the correct format.
func TestHTTPVectorStore_Upsert(t *testing.T) {
	var records []recordedRequest
	srv := setupHTTPVectorServer(t, &records)
	defer srv.Close()

	store := newTestHTTPVectorStore(srv.URL)

	items := []VectorItem{
		{
			ID:      "item-1",
			Vector:  []float32{0.1, 0.2},
			Content: "test content",
			Metadata: map[string]any{"key": "val"},
		},
		{
			// No ID — should be auto-generated
			Vector:  []float32{0.3, 0.4},
			Content: "second item",
		},
	}

	if err := store.Upsert(context.Background(), "col", items); err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 recorded request, got %d", len(records))
	}
	req := records[0]
	if req.path != "/upsert" {
		t.Errorf("request path = %q, want /upsert", req.path)
	}
	if req.body["collection"] != "col" {
		t.Errorf("request body collection = %v, want %q", req.body["collection"], "col")
	}

	itemsRaw, ok := req.body["items"].([]any)
	if !ok {
		t.Fatalf("request body items is not []any: %T", req.body["items"])
	}
	if len(itemsRaw) != 2 {
		t.Fatalf("expected 2 items in request, got %d", len(itemsRaw))
	}

	// First item should preserve its ID
	first, ok := itemsRaw[0].(map[string]any)
	if !ok {
		t.Fatal("first item is not a map")
	}
	if first["id"] != "item-1" {
		t.Errorf("first item id = %v, want item-1", first["id"])
	}
	if first["content"] != "test content" {
		t.Errorf("first item content = %v, want 'test content'", first["content"])
	}

	// Second item should have a generated ID
	second, ok := itemsRaw[1].(map[string]any)
	if !ok {
		t.Fatal("second item is not a map")
	}
	if second["id"] == "" || second["id"] == nil {
		t.Error("second item should have a generated ID")
	}
}

// TestHTTPVectorStore_Delete verifies delete sends IDs in the correct format.
func TestHTTPVectorStore_Delete(t *testing.T) {
	var records []recordedRequest
	srv := setupHTTPVectorServer(t, &records)
	defer srv.Close()

	store := newTestHTTPVectorStore(srv.URL)

	ids := []string{"id-1", "id-2", "id-3"}
	if err := store.Delete(context.Background(), "my_col", ids); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 recorded request, got %d", len(records))
	}
	req := records[0]
	if req.path != "/delete" {
		t.Errorf("request path = %q, want /delete", req.path)
	}
	if req.body["collection"] != "my_col" {
		t.Errorf("request body collection = %v, want %q", req.body["collection"], "my_col")
	}

	idsRaw, ok := req.body["ids"].([]any)
	if !ok {
		t.Fatalf("request body ids is not []any: %T", req.body["ids"])
	}
	got := make([]string, len(idsRaw))
	for i, v := range idsRaw {
		got[i], _ = v.(string)
	}
	if !reflect.DeepEqual(got, ids) {
		t.Errorf("request ids = %v, want %v", got, ids)
	}
}

// TestHTTPVectorStore_Search_EmptyResults verifies an empty result list is returned (not nil).
func TestHTTPVectorStore_Search_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	store := newTestHTTPVectorStore(srv.URL)
	results, err := store.Search(context.Background(), "col", []float32{0.1}, 10, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results == nil {
		t.Fatal("expected empty slice, not nil")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// TestHTTPVectorStore_ServerError verifies HTTP errors are propagated.
func TestHTTPVectorStore_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := newTestHTTPVectorStore(srv.URL)
	_, err := store.Search(context.Background(), "col", []float32{0.1}, 5, 0, nil)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}
