package vectorstore

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
)

// weaviateStore implements VectorStore against the Weaviate REST API.
// Docs: https://weaviate.io/developers/weaviate/api/rest
type weaviateStore struct {
	cfg    config.VectorStoreConfig
	apiKey string
	client *http.Client
}

func newWeaviateStore(cfg config.VectorStoreConfig, apiKey string) *weaviateStore {
	return &weaviateStore{cfg: cfg, apiKey: apiKey, client: getHTTPClient()}
}

func (s *weaviateStore) headers() map[string]string {
	h := map[string]string{}
	if s.apiKey != "" {
		h["Authorization"] = "Bearer " + s.apiKey
	}
	return h
}

// Search performs a vector similarity search via Weaviate GraphQL.
// POST {url}/v1/graphql
func (s *weaviateStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]SearchResult, error) {
	// Build nearVector clause
	vecStr := vectorString(vector)
	certaintyClause := ""
	if minScore > 0 {
		certaintyClause = fmt.Sprintf(", certainty: %g", minScore)
	}

	// GraphQL query — dynamically requests _additional { id certainty } and content
	query := fmt.Sprintf(`{ Get { %s(nearVector: {vector: %s%s} limit: %d) { _additional { id certainty } content } } }`,
		collection, vecStr, certaintyClause, topK)

	type gqlReq struct {
		Query string `json:"query"`
	}
	body, err := marshalJSON(gqlReq{Query: query})
	if err != nil {
		return nil, fmt.Errorf("weaviate: marshal search request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/graphql", s.cfg.URL)
	data, _, err := doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("weaviate: search: %w", err)
	}

	// Parse GraphQL response
	var resp struct {
		Data map[string]map[string][]map[string]any `json:"data"`
	}
	if err := unmarshalJSON(data, &resp); err != nil {
		return nil, fmt.Errorf("weaviate: parse search response: %w", err)
	}

	getBlock, ok := resp.Data["Get"]
	if !ok {
		return []SearchResult{}, nil
	}
	objects, ok := getBlock[collection]
	if !ok {
		return []SearchResult{}, nil
	}

	results := make([]SearchResult, 0, len(objects))
	for _, obj := range objects {
		sr := SearchResult{
			Metadata: make(map[string]any),
		}
		if add, ok := obj["_additional"].(map[string]any); ok {
			if id, ok := add["id"].(string); ok {
				sr.ID = id
			}
			if cert, ok := add["certainty"].(float64); ok {
				sr.Score = float32(cert)
			}
		}
		if c, ok := obj["content"].(string); ok {
			sr.Content = c
		}
		// Copy remaining fields as metadata (skip _additional and content)
		for k, v := range obj {
			if k == "_additional" || k == "content" {
				continue
			}
			sr.Metadata[k] = v
		}
		results = append(results, sr)
	}
	return results, nil
}

// Upsert inserts or updates vectors.
// POST {url}/v1/batch/objects
func (s *weaviateStore) Upsert(ctx context.Context, collection string, items []VectorItem) error {
	type weaviateObj struct {
		Class      string         `json:"class"`
		ID         string         `json:"id"`
		Vector     []float64      `json:"vector"`
		Properties map[string]any `json:"properties"`
	}
	type batchReq struct {
		Objects []weaviateObj `json:"objects"`
	}

	objects := make([]weaviateObj, len(items))
	for i, item := range items {
		id := item.ID
		if id == "" {
			id = contentID(item.Content)
		}
		props := make(map[string]any, len(item.Metadata)+1)
		for k, v := range item.Metadata {
			props[k] = v
		}
		props["content"] = item.Content
		objects[i] = weaviateObj{
			Class:      collection,
			ID:         id,
			Vector:     vectorToFloatSlice(item.Vector),
			Properties: props,
		}
	}

	body, err := marshalJSON(batchReq{Objects: objects})
	if err != nil {
		return fmt.Errorf("weaviate: marshal upsert request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/batch/objects", s.cfg.URL)
	_, _, err = doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return fmt.Errorf("weaviate: upsert: %w", err)
	}
	return nil
}

// Delete removes vectors by ID (one DELETE per ID as per Weaviate REST API).
// DELETE {url}/v1/objects/{className}/{id}
func (s *weaviateStore) Delete(ctx context.Context, collection string, ids []string) error {
	var errs []string
	for _, id := range ids {
		url := fmt.Sprintf("%s/v1/objects/%s/%s", s.cfg.URL, collection, id)
		_, _, err := doDelete(ctx, s.client, url, s.headers())
		if err != nil {
			errs = append(errs, fmt.Sprintf("id %s: %v", id, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("weaviate: delete errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *weaviateStore) Kind() string  { return "weaviate" }
func (s *weaviateStore) Close() error  { return nil }
