package steps

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"rah/internal/engine"
	"rah/internal/rctx"
	"rah/internal/vectorstore"
)

// VectorSearchConfig is resolved at bake time and captured in the instruction closure.
type VectorSearchConfig struct {
	// VectorSlot: ByteSlot holding the query vector as JSON float array (from embed_text)
	VectorSlot int
	// ResultSlot: ByteSlot to write []SearchResult as JSON
	ResultSlot int
	// CollectionSlot: ByteSlot holding collection name (-1 = use DefaultCollection from store config)
	CollectionSlot int
	// CountSlot: IntSlot to write number of results found (-1 = skip)
	CountSlot int
	// TopK: maximum results to return (default 5)
	TopK int
	// MinScore: minimum similarity threshold 0.0-1.0 (default 0 = return all)
	MinScore float32
	// Store: the VectorStore implementation (resolved at bake time)
	Store vectorstore.VectorStore
	// DefaultCollection: fallback when CollectionSlot is -1 or slot is empty
	DefaultCollection string
}

// VectorUpsertConfig is resolved at bake time and captured in the instruction closure.
type VectorUpsertConfig struct {
	// VectorSlot: ByteSlot holding the vector as JSON float array
	VectorSlot int
	// ContentSlot: ByteSlot holding the text content (the chunk being indexed)
	ContentSlot int
	// IDSlot: ByteSlot holding the item ID (-1 = auto-generate from content hash)
	IDSlot int
	// MetadataSlot: ByteSlot holding extra metadata as JSON object (-1 = none)
	MetadataSlot int
	// CollectionSlot: ByteSlot holding collection name (-1 = use DefaultCollection)
	CollectionSlot int
	// Store: the VectorStore implementation
	Store vectorstore.VectorStore
	// DefaultCollection: fallback collection name
	DefaultCollection string
}

// float64SliceToFloat32 converts a []float64 to []float32.
func float64SliceToFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}

// resolveVectorCollection determines the collection name to use.
// Returns ("", false) if no collection could be resolved.
func resolveVectorCollection(ctx *rctx.Context, collectionSlot int, defaultCollection string) (string, bool) {
	if collectionSlot >= 0 && collectionSlot < len(ctx.ByteSlots) {
		if v := ctx.ByteSlots[collectionSlot]; len(v) > 0 {
			return string(v), true
		}
	}
	if defaultCollection != "" {
		return defaultCollection, true
	}
	return "", false
}

// vectorRequestContext returns the context to use for vector store calls.
// Falls back to context.Background() when ctx.Request is nil (e.g. in tests).
func vectorRequestContext(ctx *rctx.Context) context.Context {
	if ctx.Request != nil {
		return ctx.Request.Context()
	}
	return context.Background()
}

// VectorSearch returns an engine.Instruction that queries a vector store and
// writes the top-K results as JSON to the result slot.
//
// At runtime:
//  1. Reads query vector JSON from ctx.ByteSlots[cfg.VectorSlot]; if empty, returns PC+1 (no-op)
//  2. Unmarshals as []float64 then converts to []float32
//  3. Resolves collection: slot value if CollectionSlot >= 0 and non-empty, else DefaultCollection
//  4. If no collection: sets ResponseStatus=400, Failed=true, returns StopPlan
//  5. Calls cfg.Store.Search(...)
//  6. Marshals results as JSON → ctx.ByteSlots[cfg.ResultSlot]
//  7. Writes int64(len(results)) to ctx.IntSlots[cfg.CountSlot] if CountSlot >= 0 and in bounds
//  8. Returns state.PC + 1
//
// On store error: sets ResponseStatus=502, Failed=true, returns StopPlan.
func VectorSearch(cfg VectorSearchConfig) engine.Instruction {
	topK := cfg.TopK
	if topK <= 0 {
		topK = 5
	}

	return engine.Instruction{
		Name: "vector_search",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read vector JSON
			if cfg.VectorSlot < 0 || cfg.VectorSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			raw := ctx.ByteSlots[cfg.VectorSlot]
			if len(raw) == 0 {
				return state.PC + 1
			}

			// 2. Unmarshal []float64 → []float32
			var vec64 []float64
			if err := json.Unmarshal(raw, &vec64); err != nil {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				msg := "vector_search: invalid vector JSON: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}
			vector := float64SliceToFloat32(vec64)

			// 3. Resolve collection
			collection, ok := resolveVectorCollection(ctx, cfg.CollectionSlot, cfg.DefaultCollection)
			if !ok {
				// 4. No collection name → 400
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				msg := "vector_search: no collection specified"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// Handle nil store gracefully
			if cfg.Store == nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "vector_search: store not configured"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 5. Call store
			results, err := cfg.Store.Search(vectorRequestContext(ctx), collection, vector, topK, cfg.MinScore, nil)
			if err != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "vector_search: store error: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 6. Marshal results as JSON → ResultSlot
			if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				resultJSON, marshalErr := json.Marshal(results)
				if marshalErr == nil {
					dst := ctx.Alloc(len(resultJSON))
					copy(dst, resultJSON)
					ctx.ByteSlots[cfg.ResultSlot] = dst
				}
			}

			// 7. Write count to IntSlot
			if cfg.CountSlot >= 0 && cfg.CountSlot < len(ctx.IntSlots) {
				ctx.IntSlots[cfg.CountSlot] = int64(len(results))
			}

			return state.PC + 1
		},
	}
}

// VectorUpsert returns an engine.Instruction that inserts or updates a vector
// with its content and metadata in a vector store.
//
// At runtime:
//  1. Reads vector JSON from cfg.VectorSlot; if empty, returns PC+1 (no-op)
//  2. Unmarshals []float64 → []float32
//  3. Reads content from cfg.ContentSlot (may be empty — allowed)
//  4. Resolves ID: slot value if IDSlot >= 0 and non-empty, else sha256(content)
//  5. Resolves metadata: unmarshal from MetadataSlot if >= 0 and non-empty, else nil
//  6. Resolves collection same as VectorSearch
//  7. Calls cfg.Store.Upsert(...)
//  8. On error: sets ResponseStatus=502, Failed=true, returns StopPlan
//  9. Returns state.PC + 1
func VectorUpsert(cfg VectorUpsertConfig) engine.Instruction {
	return engine.Instruction{
		Name: "vector_upsert",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read vector JSON
			if cfg.VectorSlot < 0 || cfg.VectorSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			raw := ctx.ByteSlots[cfg.VectorSlot]
			if len(raw) == 0 {
				return state.PC + 1
			}

			// 2. Unmarshal []float64 → []float32
			var vec64 []float64
			if err := json.Unmarshal(raw, &vec64); err != nil {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				msg := "vector_upsert: invalid vector JSON: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}
			vector := float64SliceToFloat32(vec64)

			// 3. Read content (may be empty)
			var content []byte
			if cfg.ContentSlot >= 0 && cfg.ContentSlot < len(ctx.ByteSlots) {
				content = ctx.ByteSlots[cfg.ContentSlot]
			}

			// 4. Resolve ID
			var itemID string
			if cfg.IDSlot >= 0 && cfg.IDSlot < len(ctx.ByteSlots) {
				if v := ctx.ByteSlots[cfg.IDSlot]; len(v) > 0 {
					itemID = string(v)
				}
			}
			if itemID == "" {
				// Deterministic ID from content hash
				sum := sha256.Sum256(content)
				itemID = fmt.Sprintf("%x", sum)
			}

			// 5. Resolve metadata
			var metadata map[string]any
			if cfg.MetadataSlot >= 0 && cfg.MetadataSlot < len(ctx.ByteSlots) {
				if v := ctx.ByteSlots[cfg.MetadataSlot]; len(v) > 0 {
					_ = json.Unmarshal(v, &metadata) // ignore unmarshal errors; nil metadata is fine
				}
			}

			// 6. Resolve collection
			collection, ok := resolveVectorCollection(ctx, cfg.CollectionSlot, cfg.DefaultCollection)
			if !ok {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				msg := "vector_upsert: no collection specified"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// Handle nil store
			if cfg.Store == nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "vector_upsert: store not configured"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 7. Build item and upsert
			items := []vectorstore.VectorItem{
				{
					ID:       itemID,
					Vector:   vector,
					Content:  string(content),
					Metadata: metadata,
				},
			}

			// 8. Call store
			if err := cfg.Store.Upsert(vectorRequestContext(ctx), collection, items); err != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "vector_upsert: store error: " + err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			return state.PC + 1
		},
	}
}
