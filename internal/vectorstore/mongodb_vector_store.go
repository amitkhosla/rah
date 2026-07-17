package vectorstore

import (
	"context"
	"fmt"
	"net/http"

	"github.com/amitkhosla/rah/internal/config"
)

// mongoVectorStore implements VectorStore against MongoDB Atlas Vector Search
// via the Atlas Data API REST endpoint.
//
// Required Options:
//   "database"    â€” database name (default: "rah")
//   "data_source" â€” Atlas cluster name (default: "Cluster0")
//   "index"       â€” vector search index name (default: "vector_index")
//   "path"        â€” embedding field path in documents (default: "embedding")
//
// Auth: api-key header.
type mongoVectorStore struct {
	cfg    config.VectorStoreConfig
	apiKey string
	client *http.Client
}

func newMongoVectorStore(cfg config.VectorStoreConfig, apiKey string) *mongoVectorStore {
	return &mongoVectorStore{cfg: cfg, apiKey: apiKey, client: getHTTPClient()}
}

func (s *mongoVectorStore) headers() map[string]string {
	return map[string]string{
		"api-key": s.apiKey,
	}
}

func (s *mongoVectorStore) database() string {
	return getOption(s.cfg.Options, "database", "rah")
}

func (s *mongoVectorStore) dataSource() string {
	return getOption(s.cfg.Options, "data_source", "Cluster0")
}

func (s *mongoVectorStore) indexName() string {
	return getOption(s.cfg.Options, "index", "vector_index")
}

func (s *mongoVectorStore) embeddingPath() string {
	return getOption(s.cfg.Options, "path", "embedding")
}

// Search performs vector similarity search via $vectorSearch aggregation pipeline.
// POST {url}/action/aggregate
func (s *mongoVectorStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]SearchResult, error) {
	numCandidates := topK * 10
	if numCandidates < 100 {
		numCandidates = 100
	}

	vectorSearch := map[string]any{
		"index":         s.indexName(),
		"path":          s.embeddingPath(),
		"queryVector":   vectorToFloatSlice(vector),
		"numCandidates": numCandidates,
		"limit":         topK,
	}
	if len(filter) > 0 {
		vectorSearch["filter"] = filter
	}

	pipeline := []map[string]any{
		{"$vectorSearch": vectorSearch},
		{"$project": map[string]any{
			"_id":     1,
			"content": 1,
			"score":   map[string]any{"$meta": "vectorSearchScore"},
		}},
	}

	reqBody := map[string]any{
		"collection": collection,
		"database":   s.database(),
		"dataSource": s.dataSource(),
		"pipeline":   pipeline,
	}

	body, err := marshalJSON(reqBody)
	if err != nil {
		return nil, fmt.Errorf("mongodb: marshal search request: %w", err)
	}

	url := fmt.Sprintf("%s/action/aggregate", s.cfg.URL)
	data, _, err := doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("mongodb: search: %w", err)
	}

	var resp struct {
		Documents []map[string]any `json:"documents"`
	}
	if err := unmarshalJSON(data, &resp); err != nil {
		return nil, fmt.Errorf("mongodb: parse search response: %w", err)
	}

	results := make([]SearchResult, 0, len(resp.Documents))
	for _, doc := range resp.Documents {
		score := float32(0)
		if sv, ok := doc["score"].(float64); ok {
			score = float32(sv)
		}
		if minScore > 0 && score < minScore {
			continue
		}

		sr := SearchResult{
			Score:    score,
			Metadata: make(map[string]any),
		}
		if id, ok := doc["_id"].(string); ok {
			sr.ID = id
		} else if id, ok := doc["_id"].(map[string]any); ok {
			// ObjectId case: {"$oid": "..."}
			if oid, ok := id["$oid"].(string); ok {
				sr.ID = oid
			}
		}
		if c, ok := doc["content"].(string); ok {
			sr.Content = c
		}
		for k, v := range doc {
			if k == "_id" || k == "content" || k == "score" || k == s.embeddingPath() {
				continue
			}
			sr.Metadata[k] = v
		}
		results = append(results, sr)
	}
	return results, nil
}

// Upsert inserts or updates documents via updateOne with upsert=true.
// POST {url}/action/updateMany
func (s *mongoVectorStore) Upsert(ctx context.Context, collection string, items []VectorItem) error {
	// Atlas Data API does not have a batch upsert; use updateOne for each item.
	for _, item := range items {
		id := item.ID
		if id == "" {
			id = contentID(item.Content)
		}

		doc := map[string]any{
			"_id":           id,
			"content":       item.Content,
			s.embeddingPath(): vectorToFloatSlice(item.Vector),
		}
		for k, v := range item.Metadata {
			doc[k] = v
		}

		reqBody := map[string]any{
			"collection": collection,
			"database":   s.database(),
			"dataSource": s.dataSource(),
			"filter":     map[string]any{"_id": id},
			"update":     map[string]any{"$set": doc},
			"upsert":     true,
		}

		body, err := marshalJSON(reqBody)
		if err != nil {
			return fmt.Errorf("mongodb: marshal upsert request: %w", err)
		}

		url := fmt.Sprintf("%s/action/updateOne", s.cfg.URL)
		_, _, err = doPost(ctx, s.client, url, s.headers(), body)
		if err != nil {
			return fmt.Errorf("mongodb: upsert id %s: %w", id, err)
		}
	}
	return nil
}

// Delete removes documents by ID.
// POST {url}/action/deleteMany
func (s *mongoVectorStore) Delete(ctx context.Context, collection string, ids []string) error {
	// Build $in filter
	idList := make([]any, len(ids))
	for i, id := range ids {
		idList[i] = id
	}

	reqBody := map[string]any{
		"collection": collection,
		"database":   s.database(),
		"dataSource": s.dataSource(),
		"filter":     map[string]any{"_id": map[string]any{"$in": idList}},
	}

	body, err := marshalJSON(reqBody)
	if err != nil {
		return fmt.Errorf("mongodb: marshal delete request: %w", err)
	}

	url := fmt.Sprintf("%s/action/deleteMany", s.cfg.URL)
	_, _, err = doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return fmt.Errorf("mongodb: delete: %w", err)
	}
	return nil
}

func (s *mongoVectorStore) Kind() string  { return "mongodb" }
func (s *mongoVectorStore) Close() error  { return nil }
