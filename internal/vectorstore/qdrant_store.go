package vectorstore

import (
	"context"
	"fmt"
	"net/http"

	"rah/internal/config"
)

// qdrantStore implements VectorStore against the Qdrant REST API.
// Docs: https://qdrant.tech/documentation/
type qdrantStore struct {
	cfg    config.VectorStoreConfig
	apiKey string
	client *http.Client
}

func newQdrantStore(cfg config.VectorStoreConfig, apiKey string) *qdrantStore {
	return &qdrantStore{cfg: cfg, apiKey: apiKey, client: getHTTPClient()}
}

func (s *qdrantStore) headers() map[string]string {
	h := map[string]string{}
	if s.apiKey != "" {
		h["api-key"] = s.apiKey
	}
	return h
}

// Search performs a vector similarity search.
// POST {url}/collections/{collection}/points/search
func (s *qdrantStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]SearchResult, error) {
	type searchReq struct {
		Vector         []float64 `json:"vector"`
		Limit          int       `json:"limit"`
		ScoreThreshold float32   `json:"score_threshold,omitempty"`
		WithPayload    bool      `json:"with_payload"`
		Filter         any       `json:"filter,omitempty"`
	}
	req := searchReq{
		Vector:      vectorToFloatSlice(vector),
		Limit:       topK,
		WithPayload: true,
	}
	if minScore > 0 {
		req.ScoreThreshold = minScore
	}
	if len(filter) > 0 {
		req.Filter = filter
	}

	body, err := marshalJSON(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant: marshal search request: %w", err)
	}

	url := fmt.Sprintf("%s/collections/%s/points/search", s.cfg.URL, collection)
	data, _, err := doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("qdrant: search: %w", err)
	}

	var resp struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := unmarshalJSON(data, &resp); err != nil {
		return nil, fmt.Errorf("qdrant: parse search response: %w", err)
	}

	results := make([]SearchResult, 0, len(resp.Result))
	for _, r := range resp.Result {
		sr := SearchResult{
			ID:       fmt.Sprintf("%v", r.ID),
			Score:    r.Score,
			Metadata: r.Payload,
		}
		if r.Payload != nil {
			if c, ok := r.Payload["content"].(string); ok {
				sr.Content = c
			}
		}
		results = append(results, sr)
	}
	return results, nil
}

// Upsert inserts or updates vectors.
// PUT {url}/collections/{collection}/points
func (s *qdrantStore) Upsert(ctx context.Context, collection string, items []VectorItem) error {
	type point struct {
		ID      string         `json:"id"`
		Vector  []float64      `json:"vector"`
		Payload map[string]any `json:"payload"`
	}
	type upsertReq struct {
		Points []point `json:"points"`
	}

	points := make([]point, len(items))
	for i, item := range items {
		id := item.ID
		if id == "" {
			id = contentID(item.Content)
		}
		payload := make(map[string]any, len(item.Metadata)+1)
		for k, v := range item.Metadata {
			payload[k] = v
		}
		payload["content"] = item.Content
		points[i] = point{
			ID:      id,
			Vector:  vectorToFloatSlice(item.Vector),
			Payload: payload,
		}
	}

	body, err := marshalJSON(upsertReq{Points: points})
	if err != nil {
		return fmt.Errorf("qdrant: marshal upsert request: %w", err)
	}

	url := fmt.Sprintf("%s/collections/%s/points", s.cfg.URL, collection)
	// Qdrant uses PUT for upsert
	req, err := newPutRequest(ctx, url, s.headers(), body)
	if err != nil {
		return fmt.Errorf("qdrant: upsert request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: upsert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant: upsert HTTP %d", resp.StatusCode)
	}
	return nil
}

// Delete removes vectors by ID.
// POST {url}/collections/{collection}/points/delete
func (s *qdrantStore) Delete(ctx context.Context, collection string, ids []string) error {
	type deleteReq struct {
		Points []string `json:"points"`
	}
	body, err := marshalJSON(deleteReq{Points: ids})
	if err != nil {
		return fmt.Errorf("qdrant: marshal delete request: %w", err)
	}

	url := fmt.Sprintf("%s/collections/%s/points/delete", s.cfg.URL, collection)
	_, _, err = doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return fmt.Errorf("qdrant: delete: %w", err)
	}
	return nil
}

func (s *qdrantStore) Kind() string  { return "qdrant" }
func (s *qdrantStore) Close() error  { return nil }
