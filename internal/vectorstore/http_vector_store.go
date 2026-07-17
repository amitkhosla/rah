package vectorstore

import (
	"context"
	"fmt"
	"net/http"

	"github.com/amitkhosla/rah/internal/config"
)

// httpVectorStore implements VectorStore as a generic HTTP client.
// It uses a standard envelope format compatible with any custom HTTP vector service.
//
// Options (all optional):
//   "search_path"  â€” URL path for search (default: "/search")
//   "upsert_path"  â€” URL path for upsert (default: "/upsert")
//   "delete_path"  â€” URL path for delete (default: "/delete")
//   "auth_header"  â€” auth header name (default: "Authorization")
//   "auth_prefix"  â€” auth value prefix (default: "Bearer")
type httpVectorStore struct {
	cfg        config.VectorStoreConfig
	apiKey     string
	client     *http.Client
	searchPath string
	upsertPath string
	deletePath string
	authHeader string
	authPrefix string
}

func newHTTPVectorStore(cfg config.VectorStoreConfig, apiKey string) *httpVectorStore {
	return &httpVectorStore{
		cfg:        cfg,
		apiKey:     apiKey,
		client:     getHTTPClient(),
		searchPath: getOption(cfg.Options, "search_path", "/search"),
		upsertPath: getOption(cfg.Options, "upsert_path", "/upsert"),
		deletePath: getOption(cfg.Options, "delete_path", "/delete"),
		authHeader: getOption(cfg.Options, "auth_header", "Authorization"),
		authPrefix: getOption(cfg.Options, "auth_prefix", "Bearer"),
	}
}

func (s *httpVectorStore) headers() map[string]string {
	h := map[string]string{}
	if s.apiKey != "" {
		h[s.authHeader] = s.authPrefix + " " + s.apiKey
	}
	return h
}

// Search sends a standard search envelope and parses the results envelope.
// POST {url}{searchPath}
// Request:  {"collection": "...", "vector": [...], "top_k": N, "min_score": 0.7, "filter": {...}}
// Response: {"results": [{"id": "...", "score": 0.9, "content": "...", "metadata": {...}}]}
func (s *httpVectorStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]SearchResult, error) {
	reqMap := map[string]any{
		"collection": collection,
		"vector":     vectorToFloatSlice(vector),
		"top_k":      topK,
	}
	if minScore > 0 {
		reqMap["min_score"] = minScore
	}
	if len(filter) > 0 {
		reqMap["filter"] = filter
	}

	body, err := marshalJSON(reqMap)
	if err != nil {
		return nil, fmt.Errorf("http-vector: marshal search request: %w", err)
	}

	url := s.cfg.URL + s.searchPath
	data, _, err := doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("http-vector: search: %w", err)
	}

	var resp struct {
		Results []struct {
			ID       string         `json:"id"`
			Score    float32        `json:"score"`
			Content  string         `json:"content"`
			Metadata map[string]any `json:"metadata"`
		} `json:"results"`
	}
	if err := unmarshalJSON(data, &resp); err != nil {
		return nil, fmt.Errorf("http-vector: parse search response: %w", err)
	}

	results := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, SearchResult{
			ID:       r.ID,
			Score:    r.Score,
			Content:  r.Content,
			Metadata: r.Metadata,
		})
	}
	return results, nil
}

// Upsert sends items in the standard upsert envelope.
// POST {url}{upsertPath}
// Request: {"collection": "...", "items": [{"id": "...", "vector": [...], "content": "...", "metadata": {...}}]}
func (s *httpVectorStore) Upsert(ctx context.Context, collection string, items []VectorItem) error {
	type itemEnvelope struct {
		ID       string         `json:"id"`
		Vector   []float64      `json:"vector"`
		Content  string         `json:"content"`
		Metadata map[string]any `json:"metadata,omitempty"`
	}

	envelopes := make([]itemEnvelope, len(items))
	for i, item := range items {
		id := item.ID
		if id == "" {
			id = contentID(item.Content)
		}
		envelopes[i] = itemEnvelope{
			ID:       id,
			Vector:   vectorToFloatSlice(item.Vector),
			Content:  item.Content,
			Metadata: item.Metadata,
		}
	}

	reqMap := map[string]any{
		"collection": collection,
		"items":      envelopes,
	}
	body, err := marshalJSON(reqMap)
	if err != nil {
		return fmt.Errorf("http-vector: marshal upsert request: %w", err)
	}

	url := s.cfg.URL + s.upsertPath
	_, _, err = doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return fmt.Errorf("http-vector: upsert: %w", err)
	}
	return nil
}

// Delete sends IDs in the standard delete envelope.
// POST {url}{deletePath}
// Request: {"collection": "...", "ids": ["id1", "id2"]}
func (s *httpVectorStore) Delete(ctx context.Context, collection string, ids []string) error {
	reqMap := map[string]any{
		"collection": collection,
		"ids":        ids,
	}
	body, err := marshalJSON(reqMap)
	if err != nil {
		return fmt.Errorf("http-vector: marshal delete request: %w", err)
	}

	url := s.cfg.URL + s.deletePath
	_, _, err = doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return fmt.Errorf("http-vector: delete: %w", err)
	}
	return nil
}

func (s *httpVectorStore) Kind() string  { return "http" }
func (s *httpVectorStore) Close() error  { return nil }
