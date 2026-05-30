package steps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rah/internal/engine"
	"rah/internal/rctx"
	"rah/internal/vectorstore"
)

// semanticCacheClientCache pools HTTP clients by endpoint
var (
	semanticCacheClientCache sync.Map // key: endpoint string → *http.Client
	semanticCacheClientCount atomic.Int64
)

// getSemanticCacheClient returns or creates a pooled HTTP client for the embedding endpoint.
// This reuses the same pooling strategy as embedText to minimize client allocations.
func getSemanticCacheClient(endpoint string, timeoutMs int) *http.Client {
	if c, ok := semanticCacheClientCache.Load(endpoint); ok {
		return c.(*http.Client)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
		Timeout: timeout,
	}
	if semanticCacheClientCount.Load() < 256 {
		actual, loaded := semanticCacheClientCache.LoadOrStore(endpoint, client)
		if loaded {
			return actual.(*http.Client)
		}
		semanticCacheClientCount.Add(1)
	}
	return client
}

// SemanticCacheGetConfig configures a semantic_cache_get instruction.
type SemanticCacheGetConfig struct {
	// Embedding config (inline embed)
	EmbedCfg EmbedTextConfig

	// Vector store handle (injected at bake time)
	Store      vectorstore.VectorStore
	Collection string

	// SharedCollection disables per-tenant collection scoping.
	// When false (default), the collection is scoped as "<collection>_<tenantID>".
	// When true, all tenants share the same collection (cfg.Collection used as-is).
	SharedCollection bool

	// Slots
	QuerySlot      int     // ByteSlots: query text
	ResultSlot     int     // ByteSlots: write cached response here on hit
	HitSlot        int     // BoolSlots: true on cache hit, false on miss

	// Threshold
	MinScore float32 // default 0.92
}

// SemanticCachePutConfig configures a semantic_cache_put instruction.
type SemanticCachePutConfig struct {
	// Embedding config (inline embed)
	EmbedCfg EmbedTextConfig

	// Vector store handle
	Store      vectorstore.VectorStore
	Collection string

	// SharedCollection disables per-tenant collection scoping.
	// When false (default), the collection is scoped as "<collection>_<tenantID>".
	// When true, all tenants share the same collection (cfg.Collection used as-is).
	SharedCollection bool

	// Slots
	QuerySlot    int // ByteSlots: query text (to embed as key)
	ResponseSlot int // ByteSlots: response to cache as content
}

// SemanticCacheGet returns an engine.Instruction that looks up a semantically
// similar query in the vector store and returns the cached response on hit.
//
// At runtime:
//  1. Reads query from ctx.ByteSlots[cfg.QuerySlot]; if empty, sets hit=false, returns PC+1
//  2. Embeds the query text using cfg.EmbedCfg
//  3. Searches the vector store for vectors with similarity >= cfg.MinScore
//  4. On hit: writes cached response to ctx.ByteSlots[cfg.ResultSlot], sets ctx.BoolSlots[cfg.HitSlot] = true
//  5. On miss: sets ctx.BoolSlots[cfg.HitSlot] = false
//  6. Returns state.PC + 1
//
// Embedding failures and vector store errors are silent (treated as cache miss).
func SemanticCacheGet(cfg SemanticCacheGetConfig) engine.Instruction {
	timeoutMs := cfg.EmbedCfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 10_000
	}
	maxRetries := cfg.EmbedCfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	endpoint := embedEndpoint(cfg.EmbedCfg.Provider, cfg.EmbedCfg.BaseURL, cfg.EmbedCfg.Model)
	if endpoint == "" {
		// Misconfiguration — return poisoned instruction that always treats as miss
		return engine.Instruction{
			Name: "semantic_cache_get[bad_provider]",
			Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
				if cfg.HitSlot >= 0 && cfg.HitSlot < len(ctx.BoolSlots) {
					ctx.BoolSlots[cfg.HitSlot] = false
				}
				return state.PC + 1
			},
		}
	}

	minScore := cfg.MinScore
	if minScore <= 0 {
		minScore = 0.92
	}

	return engine.Instruction{
		Name: "semantic_cache_get[" + string(cfg.EmbedCfg.Provider) + "/" + cfg.EmbedCfg.Model + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read query text
			if cfg.QuerySlot < 0 || cfg.QuerySlot >= len(ctx.ByteSlots) {
				if cfg.HitSlot >= 0 && cfg.HitSlot < len(ctx.BoolSlots) {
					ctx.BoolSlots[cfg.HitSlot] = false
				}
				return state.PC + 1
			}

			query := ctx.ByteSlots[cfg.QuerySlot]
			if len(query) == 0 {
				if cfg.HitSlot >= 0 && cfg.HitSlot < len(ctx.BoolSlots) {
					ctx.BoolSlots[cfg.HitSlot] = false
				}
				return state.PC + 1
			}

			// 2. Resolve API key
			apiKey := cfg.EmbedCfg.APIKey
			if cfg.EmbedCfg.APIKeySlot >= 0 && cfg.EmbedCfg.APIKeySlot < len(ctx.ByteSlots) {
				if k := ctx.ByteSlots[cfg.EmbedCfg.APIKeySlot]; len(k) > 0 {
					apiKey = string(k)
				}
			}

			// 3. Build and send embed request
			body, buildErr := embedBuildRequest(cfg.EmbedCfg.Provider, cfg.EmbedCfg.Model, string(query))
			if buildErr != nil {
				if cfg.HitSlot >= 0 && cfg.HitSlot < len(ctx.BoolSlots) {
					ctx.BoolSlots[cfg.HitSlot] = false
				}
				return state.PC + 1
			}

			client := getSemanticCacheClient(endpoint, timeoutMs)
			attempts := maxRetries + 1

			var vector []float32
			var embedSuccess bool

			for attempt := 1; attempt <= attempts; attempt++ {
				reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
				httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
				if reqErr != nil {
					cancel()
					break
				}

				httpReq.Header.Set("Content-Type", "application/json")

				// Set auth header per provider
				switch cfg.EmbedCfg.Provider {
				case EmbedProviderOpenAI:
					if apiKey != "" {
						httpReq.Header.Set("Authorization", "Bearer "+apiKey)
					}
				case EmbedProviderGemini:
					if apiKey != "" {
						httpReq.Header.Set("x-goog-api-key", apiKey)
					}
				}

				resp, doErr := client.Do(httpReq)
				cancel()

				if doErr != nil {
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					break
				}

				respBody, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()

				if readErr != nil {
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					break
				}

				// Retry on rate-limit or transient errors
				if resp.StatusCode == 429 || resp.StatusCode >= 500 {
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					break
				}

				if resp.StatusCode != http.StatusOK {
					break
				}

				// Parse vector from response
				vec64, parseErr := embedParseResponse(cfg.EmbedCfg.Provider, respBody)
				if parseErr != nil {
					break
				}

				// Convert float64 to float32
				vector = float64SliceToFloat32(vec64)
				embedSuccess = true
				break
			}

			if !embedSuccess {
				if cfg.HitSlot >= 0 && cfg.HitSlot < len(ctx.BoolSlots) {
					ctx.BoolSlots[cfg.HitSlot] = false
				}
				return state.PC + 1
			}

			// 4. Search vector store
			if cfg.Store == nil {
				if cfg.HitSlot >= 0 && cfg.HitSlot < len(ctx.BoolSlots) {
					ctx.BoolSlots[cfg.HitSlot] = false
				}
				return state.PC + 1
			}

			// Scope collection by tenant unless shared_collection is set.
			collection := cfg.Collection
			if !cfg.SharedCollection {
				collection = fmt.Sprintf("%s_%d", cfg.Collection, ctx.TenantID)
			}

			// Use request context if available, else background
			searchCtx := context.Background()
			if ctx.Request != nil {
				searchCtx = ctx.Request.Context()
			}

			results, searchErr := cfg.Store.Search(searchCtx, collection, vector, 1, minScore, nil)
			if searchErr != nil || len(results) == 0 {
				if cfg.HitSlot >= 0 && cfg.HitSlot < len(ctx.BoolSlots) {
					ctx.BoolSlots[cfg.HitSlot] = false
				}
				return state.PC + 1
			}

			// 5. Cache hit: write response and set flag
			if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				content := []byte(results[0].Content)
				dst := ctx.Alloc(len(content))
				copy(dst, content)
				ctx.ByteSlots[cfg.ResultSlot] = dst
			}

			if cfg.HitSlot >= 0 && cfg.HitSlot < len(ctx.BoolSlots) {
				ctx.BoolSlots[cfg.HitSlot] = true
			}

			return state.PC + 1
		},
	}
}

// SemanticCachePut returns an engine.Instruction that stores a query-response pair
// in the vector store for future semantic cache lookups.
//
// At runtime:
//  1. Reads query from ctx.ByteSlots[cfg.QuerySlot]; if empty, returns PC+1 (no-op)
//  2. Embeds the query text using cfg.EmbedCfg
//  3. Reads response from ctx.ByteSlots[cfg.ResponseSlot]
//  4. Generates a deterministic ID from query hash
//  5. Calls cfg.Store.Upsert to cache the query-response pair
//  6. Returns state.PC + 1
//
// Embedding and upsert errors are silent (best-effort cache write).
func SemanticCachePut(cfg SemanticCachePutConfig) engine.Instruction {
	timeoutMs := cfg.EmbedCfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 10_000
	}
	maxRetries := cfg.EmbedCfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	endpoint := embedEndpoint(cfg.EmbedCfg.Provider, cfg.EmbedCfg.BaseURL, cfg.EmbedCfg.Model)
	if endpoint == "" {
		// Misconfiguration — return no-op instruction
		return engine.Instruction{
			Name: "semantic_cache_put[bad_provider]",
			Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
				return state.PC + 1
			},
		}
	}

	return engine.Instruction{
		Name: "semantic_cache_put[" + string(cfg.EmbedCfg.Provider) + "/" + cfg.EmbedCfg.Model + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read query text
			if cfg.QuerySlot < 0 || cfg.QuerySlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}

			query := ctx.ByteSlots[cfg.QuerySlot]
			if len(query) == 0 {
				return state.PC + 1
			}

			// 2. Resolve API key
			apiKey := cfg.EmbedCfg.APIKey
			if cfg.EmbedCfg.APIKeySlot >= 0 && cfg.EmbedCfg.APIKeySlot < len(ctx.ByteSlots) {
				if k := ctx.ByteSlots[cfg.EmbedCfg.APIKeySlot]; len(k) > 0 {
					apiKey = string(k)
				}
			}

			// 3. Build and send embed request
			body, buildErr := embedBuildRequest(cfg.EmbedCfg.Provider, cfg.EmbedCfg.Model, string(query))
			if buildErr != nil {
				return state.PC + 1
			}

			client := getSemanticCacheClient(endpoint, timeoutMs)
			attempts := maxRetries + 1

			var vector []float32
			var embedSuccess bool

			for attempt := 1; attempt <= attempts; attempt++ {
				reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
				httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
				if reqErr != nil {
					cancel()
					break
				}

				httpReq.Header.Set("Content-Type", "application/json")

				// Set auth header per provider
				switch cfg.EmbedCfg.Provider {
				case EmbedProviderOpenAI:
					if apiKey != "" {
						httpReq.Header.Set("Authorization", "Bearer "+apiKey)
					}
				case EmbedProviderGemini:
					if apiKey != "" {
						httpReq.Header.Set("x-goog-api-key", apiKey)
					}
				}

				resp, doErr := client.Do(httpReq)
				cancel()

				if doErr != nil {
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					break
				}

				respBody, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()

				if readErr != nil {
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					break
				}

				// Retry on rate-limit or transient errors
				if resp.StatusCode == 429 || resp.StatusCode >= 500 {
					if attempt < attempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					break
				}

				if resp.StatusCode != http.StatusOK {
					break
				}

				// Parse vector from response
				vec64, parseErr := embedParseResponse(cfg.EmbedCfg.Provider, respBody)
				if parseErr != nil {
					break
				}

				// Convert float64 to float32
				vector = float64SliceToFloat32(vec64)
				embedSuccess = true
				break
			}

			if !embedSuccess {
				return state.PC + 1
			}

			// 4. Read response to cache
			var response []byte
			if cfg.ResponseSlot >= 0 && cfg.ResponseSlot < len(ctx.ByteSlots) {
				response = ctx.ByteSlots[cfg.ResponseSlot]
			}

			// 5. Generate deterministic ID from query hash
			// Use first 64 chars of query + length as ID
			queryStr := string(query)
			var idPrefix string
			if len(queryStr) > 64 {
				idPrefix = queryStr[:64]
			} else {
				idPrefix = queryStr
			}
			sum := sha256.Sum256(query)
			itemID := fmt.Sprintf("%s_%x", idPrefix, sum[:8])
			// Sanitize ID: remove characters that might be problematic in vector stores
			itemID = strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
					return r
				}
				return '_'
			}, itemID)

			// 6. Upsert to vector store
			if cfg.Store == nil {
				return state.PC + 1
			}

			// Scope collection by tenant unless shared_collection is set.
			collection := cfg.Collection
			if !cfg.SharedCollection {
				collection = fmt.Sprintf("%s_%d", cfg.Collection, ctx.TenantID)
			}

			// Use request context if available, else background
			upsertCtx := context.Background()
			if ctx.Request != nil {
				upsertCtx = ctx.Request.Context()
			}

			items := []vectorstore.VectorItem{
				{
					ID:       itemID,
					Vector:   vector,
					Content:  string(response),
					Metadata: nil,
				},
			}

			_ = cfg.Store.Upsert(upsertCtx, collection, items) // ignore errors (best-effort)

			return state.PC + 1
		},
	}
}
