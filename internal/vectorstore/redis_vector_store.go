package vectorstore

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"

	"rah/internal/config"
)

// redisVectorStore implements VectorStore against a Redis Stack instance
// via the Upstash Redis REST API (JSON command format).
//
// Requires Redis Stack with RediSearch and vector index configured.
// Compatible with Upstash Redis REST API: POST {url}/pipeline
// Auth: Authorization: Bearer <token>
//
// Vector encoding: little-endian float32 binary blob, base64-encoded.
type redisVectorStore struct {
	cfg    config.VectorStoreConfig
	apiKey string
	client *http.Client
}

func newRedisVectorStore(cfg config.VectorStoreConfig, apiKey string) *redisVectorStore {
	return &redisVectorStore{cfg: cfg, apiKey: apiKey, client: getHTTPClient()}
}

func (s *redisVectorStore) headers() map[string]string {
	h := map[string]string{}
	if s.apiKey != "" {
		h["Authorization"] = "Bearer " + s.apiKey
	}
	return h
}

// execPipeline sends a batch of Redis commands via the Upstash REST pipeline endpoint.
// Each command is a []any where element 0 is the command name.
func (s *redisVectorStore) execPipeline(ctx context.Context, commands [][]any) ([]any, error) {
	body, err := marshalJSON(commands)
	if err != nil {
		return nil, fmt.Errorf("redis-vector: marshal pipeline: %w", err)
	}

	url := fmt.Sprintf("%s/pipeline", s.cfg.URL)
	data, _, err := doPost(ctx, s.client, url, s.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("redis-vector: pipeline: %w", err)
	}

	var results []any
	if err := unmarshalJSON(data, &results); err != nil {
		return nil, fmt.Errorf("redis-vector: parse pipeline response: %w", err)
	}
	return results, nil
}

// Search uses FT.SEARCH with KNN vector search.
// Encodes the query vector as a little-endian float32 binary blob, base64-encoded.
func (s *redisVectorStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]SearchResult, error) {
	blob := float32ToBytes(vector)
	blobB64 := base64.StdEncoding.EncodeToString(blob)

	query := fmt.Sprintf("*=>[KNN %d @vector $BLOB AS __score]", topK)
	cmd := []any{
		"FT.SEARCH", collection,
		query,
		"PARAMS", "2", "BLOB", blobB64,
		"RETURN", "3", "content", "metadata", "__score",
		"SORTBY", "__score",
		"DIALECT", "2",
	}

	results, err := s.execPipeline(ctx, [][]any{cmd})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return []SearchResult{}, nil
	}

	// Upstash pipeline response: [{result: ...}]
	type pipelineResult struct {
		Result any `json:"result"`
	}
	firstRaw, err := marshalJSON(results[0])
	if err != nil {
		return nil, fmt.Errorf("redis-vector: marshal first result: %w", err)
	}
	var pr pipelineResult
	if err := unmarshalJSON(firstRaw, &pr); err != nil {
		return nil, fmt.Errorf("redis-vector: parse pipeline result: %w", err)
	}

	// FT.SEARCH result: [count, key1, [field1, val1, ...], key2, [...], ...]
	rawArr, ok := pr.Result.([]any)
	if !ok || len(rawArr) < 1 {
		return []SearchResult{}, nil
	}

	out := make([]SearchResult, 0)
	// rawArr[0] = total count, then alternating key/fields pairs
	for i := 1; i+1 < len(rawArr); i += 2 {
		key, _ := rawArr[i].(string)
		fieldsRaw, ok := rawArr[i+1].([]any)
		if !ok {
			continue
		}
		fieldMap := parseRedisFieldList(fieldsRaw)
		score := float32(0)
		if sv, ok := fieldMap["__score"]; ok {
			if f, err := strconv.ParseFloat(sv, 32); err == nil {
				// Redis returns distance; convert to similarity (1 - distance for cosine)
				score = 1.0 - float32(f)
			}
		}
		if minScore > 0 && score < minScore {
			continue
		}
		sr := SearchResult{
			ID:    key,
			Score: score,
		}
		if c, ok := fieldMap["content"]; ok {
			sr.Content = c
		}
		if m, ok := fieldMap["metadata"]; ok {
			var meta map[string]any
			if err := unmarshalJSON([]byte(m), &meta); err == nil {
				sr.Metadata = meta
			}
		}
		out = append(out, sr)
	}
	return out, nil
}

// Upsert stores vectors using HSET with a vector field (binary blob).
func (s *redisVectorStore) Upsert(ctx context.Context, collection string, items []VectorItem) error {
	cmds := make([][]any, 0, len(items))
	for _, item := range items {
		id := item.ID
		if id == "" {
			id = contentID(item.Content)
		}
		key := fmt.Sprintf("%s:%s", collection, id)
		blob := float32ToBytes(item.Vector)

		metaJSON, _ := marshalJSON(item.Metadata)

		cmds = append(cmds, []any{
			"HSET", key,
			"vector", blob,
			"content", item.Content,
			"metadata", string(metaJSON),
		})
	}

	_, err := s.execPipeline(ctx, cmds)
	if err != nil {
		return fmt.Errorf("redis-vector: upsert: %w", err)
	}
	return nil
}

// Delete removes vectors by key.
func (s *redisVectorStore) Delete(ctx context.Context, collection string, ids []string) error {
	cmds := make([][]any, len(ids))
	for i, id := range ids {
		key := fmt.Sprintf("%s:%s", collection, id)
		cmds[i] = []any{"DEL", key}
	}
	_, err := s.execPipeline(ctx, cmds)
	if err != nil {
		return fmt.Errorf("redis-vector: delete: %w", err)
	}
	return nil
}

func (s *redisVectorStore) Kind() string  { return "redis" }
func (s *redisVectorStore) Close() error  { return nil }

// parseRedisFieldList converts ["field1", "val1", "field2", "val2"] into a map.
func parseRedisFieldList(fields []any) map[string]string {
	m := make(map[string]string, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		k, ok1 := fields[i].(string)
		v, ok2 := fields[i+1].(string)
		if ok1 && ok2 {
			m[k] = v
		}
	}
	return m
}
