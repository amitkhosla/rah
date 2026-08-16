package vectorstore

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
)

// pgVectorStore implements VectorStore against PostgreSQL + pgvector
// via the PostgREST HTTP API.
//
// Required database setup:
//   - A stored function "vector_search(query_embedding, match_count, collection_name, min_score)"
//   - Tables named after collections with columns: id, content, embedding (vector), and metadata fields
//
// Options:
//   "auth_type"  — "bearer" (default) or "apikey" (Supabase anon key header)
//   "apikey_header" — header name for apikey mode (default: "apikey")
type pgVectorStore struct {
	cfg    config.VectorStoreConfig
	apiKey string
	client *http.Client
}

func newPgVectorStore(cfg config.VectorStoreConfig, apiKey string) *pgVectorStore {
	return &pgVectorStore{cfg: cfg, apiKey: apiKey, client: getHTTPClient()}
}

func (s *pgVectorStore) headers() map[string]string {
	h := map[string]string{}
	if s.apiKey == "" {
		return h
	}
	authType := getOption(s.cfg.Options, "auth_type", "bearer")
	if authType == "apikey" {
		hdr := getOption(s.cfg.Options, "apikey_header", "apikey")
		h[hdr] = s.apiKey
	} else {
		h["Authorization"] = "Bearer " + s.apiKey
	}
	return h
}

// Search calls the vector_search RPC function via PostgREST.
// POST {url}/rpc/vector_search
func (s *pgVectorStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]SearchResult, error) {
	type searchReq struct {
		QueryEmbedding []float64 `json:"query_embedding"`
		MatchCount     int       `json:"match_count"`
		CollectionName string    `json:"collection_name"`
		MinScore       float32   `json:"min_score"`
	}
	req := searchReq{
		QueryEmbedding: vectorToFloatSlice(vector),
		MatchCount:     topK,
		CollectionName: collection,
		MinScore:       minScore,
	}

	body, err := marshalJSON(req)
	if err != nil {
		return nil, fmt.Errorf("pgvector: marshal search request: %w", err)
	}

	url := fmt.Sprintf("%s/rpc/vector_search", s.cfg.URL)
	data, _, err := doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("pgvector: search: %w", err)
	}

	var rows []map[string]any
	if err := unmarshalJSON(data, &rows); err != nil {
		return nil, fmt.Errorf("pgvector: parse search response: %w", err)
	}

	results := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		sr := SearchResult{
			Metadata: make(map[string]any),
		}
		if id, ok := row["id"].(string); ok {
			sr.ID = id
		}
		if c, ok := row["content"].(string); ok {
			sr.Content = c
		}
		if sim, ok := row["similarity"].(float64); ok {
			sr.Score = float32(sim)
		}
		for k, v := range row {
			if k == "id" || k == "content" || k == "similarity" || k == "embedding" {
				continue
			}
			sr.Metadata[k] = v
		}
		results = append(results, sr)
	}
	return results, nil
}

// Upsert uses PostgREST POST with Prefer: resolution=merge-duplicates.
// POST {url}/{collection} with Prefer: resolution=merge-duplicates
func (s *pgVectorStore) Upsert(ctx context.Context, collection string, items []VectorItem) error {
	rows := make([]map[string]any, len(items))
	for i, item := range items {
		id := item.ID
		if id == "" {
			id = contentID(item.Content)
		}
		row := map[string]any{
			"id":        id,
			"content":   item.Content,
			"embedding": vectorString(item.Vector), // pgvector text format: "[0.1,0.2,...]"
		}
		for k, v := range item.Metadata {
			row[k] = v
		}
		rows[i] = row
	}

	body, err := marshalJSON(rows)
	if err != nil {
		return fmt.Errorf("pgvector: marshal upsert request: %w", err)
	}

	url := fmt.Sprintf("%s/%s", s.cfg.URL, collection)
	h := s.headers()
	h["Prefer"] = "resolution=merge-duplicates"

	_, _, err = doPost(ctx, s.client, url, h, body)
	if err != nil {
		return fmt.Errorf("pgvector: upsert: %w", err)
	}
	return nil
}

// Delete removes rows by ID using PostgREST DELETE with ?id=in.(id1,id2,...).
// DELETE {url}/{collection}?id=in.(id1,id2)
func (s *pgVectorStore) Delete(ctx context.Context, collection string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	inList := strings.Join(ids, ",")
	url := fmt.Sprintf("%s/%s?id=in.(%s)", s.cfg.URL, collection, inList)

	_, _, err := doDelete(ctx, s.client, url, s.headers())
	if err != nil {
		return fmt.Errorf("pgvector: delete: %w", err)
	}
	return nil
}

func (s *pgVectorStore) Kind() string  { return "pgvector" }
func (s *pgVectorStore) Close() error  { return nil }
