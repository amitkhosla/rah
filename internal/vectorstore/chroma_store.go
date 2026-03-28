package vectorstore

import (
	"context"
	"fmt"
	"net/http"

	"rah/internal/config"
)

// chromaStore implements VectorStore against the Chroma REST API v1.
// Docs: https://docs.trychroma.com/reference/js-client
type chromaStore struct {
	cfg    config.VectorStoreConfig
	apiKey string
	client *http.Client
}

func newChromaStore(cfg config.VectorStoreConfig, apiKey string) *chromaStore {
	return &chromaStore{cfg: cfg, apiKey: apiKey, client: getHTTPClient()}
}

func (s *chromaStore) headers() map[string]string {
	h := map[string]string{}
	if s.apiKey != "" {
		h["Authorization"] = "Bearer " + s.apiKey
	}
	return h
}

// Search performs a vector similarity search.
// POST {url}/api/v1/collections/{collection}/query
// Chroma returns distances (lower = better); we convert to similarity: score = 1 - distance.
func (s *chromaStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]SearchResult, error) {
	type queryReq struct {
		QueryEmbeddings [][]float64 `json:"query_embeddings"`
		NResults        int         `json:"n_results"`
		Include         []string    `json:"include"`
		Where           any         `json:"where,omitempty"`
	}

	req := queryReq{
		QueryEmbeddings: [][]float64{vectorToFloatSlice(vector)},
		NResults:        topK,
		Include:         []string{"documents", "metadatas", "distances"},
	}
	if len(filter) > 0 {
		req.Where = filter
	}

	body, err := marshalJSON(req)
	if err != nil {
		return nil, fmt.Errorf("chroma: marshal search request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/collections/%s/query", s.cfg.URL, collection)
	data, _, err := doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("chroma: search: %w", err)
	}

	// Chroma response arrays are nested: [[item1, item2, ...]] (one outer list per query)
	var resp struct {
		IDs       [][]string       `json:"ids"`
		Documents [][]string       `json:"documents"`
		Metadatas [][]map[string]any `json:"metadatas"`
		Distances [][]float32      `json:"distances"`
	}
	if err := unmarshalJSON(data, &resp); err != nil {
		return nil, fmt.Errorf("chroma: parse search response: %w", err)
	}

	if len(resp.IDs) == 0 || len(resp.IDs[0]) == 0 {
		return []SearchResult{}, nil
	}

	ids := resp.IDs[0]
	docs := resp.Documents[0]
	var metas []map[string]any
	if len(resp.Metadatas) > 0 {
		metas = resp.Metadatas[0]
	}
	var dists []float32
	if len(resp.Distances) > 0 {
		dists = resp.Distances[0]
	}

	results := make([]SearchResult, 0, len(ids))
	for i, id := range ids {
		score := float32(1.0)
		if i < len(dists) {
			score = 1.0 - dists[i]
		}
		if minScore > 0 && score < minScore {
			continue
		}
		var content string
		if i < len(docs) {
			content = docs[i]
		}
		var meta map[string]any
		if i < len(metas) {
			meta = metas[i]
		}
		results = append(results, SearchResult{
			ID:       id,
			Score:    score,
			Content:  content,
			Metadata: meta,
		})
	}
	return results, nil
}

// Upsert inserts or updates vectors.
// POST {url}/api/v1/collections/{collection}/upsert
func (s *chromaStore) Upsert(ctx context.Context, collection string, items []VectorItem) error {
	type upsertReq struct {
		IDs        []string         `json:"ids"`
		Embeddings [][]float64      `json:"embeddings"`
		Documents  []string         `json:"documents"`
		Metadatas  []map[string]any `json:"metadatas"`
	}

	req := upsertReq{
		IDs:        make([]string, len(items)),
		Embeddings: make([][]float64, len(items)),
		Documents:  make([]string, len(items)),
		Metadatas:  make([]map[string]any, len(items)),
	}
	for i, item := range items {
		id := item.ID
		if id == "" {
			id = contentID(item.Content)
		}
		req.IDs[i] = id
		req.Embeddings[i] = vectorToFloatSlice(item.Vector)
		req.Documents[i] = item.Content
		req.Metadatas[i] = item.Metadata
	}

	body, err := marshalJSON(req)
	if err != nil {
		return fmt.Errorf("chroma: marshal upsert request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/collections/%s/upsert", s.cfg.URL, collection)
	_, _, err = doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return fmt.Errorf("chroma: upsert: %w", err)
	}
	return nil
}

// Delete removes vectors by ID.
// POST {url}/api/v1/collections/{collection}/delete
func (s *chromaStore) Delete(ctx context.Context, collection string, ids []string) error {
	type deleteReq struct {
		IDs []string `json:"ids"`
	}
	body, err := marshalJSON(deleteReq{IDs: ids})
	if err != nil {
		return fmt.Errorf("chroma: marshal delete request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/collections/%s/delete", s.cfg.URL, collection)
	_, _, err = doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return fmt.Errorf("chroma: delete: %w", err)
	}
	return nil
}

func (s *chromaStore) Kind() string  { return "chroma" }
func (s *chromaStore) Close() error  { return nil }
