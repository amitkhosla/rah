package control

import (
	"fmt"
	"strconv"

	"rah/internal/engine/steps"
	"rah/internal/vectorstore"
)

// compileVectorSearch handles the "vector_search" step type.
//
// StepConfig fields:
//   - key_identifier              → vectorSlot (query vector JSON from embed_text)
//   - as                          → resultSlot ([]SearchResult JSON output)
//   - input["store"]              → named VectorStore (resolved from Compiler.VectorStores)
//   - input["collection"]         → default collection name (fallback)
//   - input["collection_slot"]    → optional slot name holding the collection at runtime
//   - input["top_k"]              → max results to return (default 5)
//   - input["min_score"]          → minimum similarity score 0.0–1.0 (default 0)
//   - input["count_slot"]         → optional IntSlot index for result count output
func (c *Compiler) compileVectorSearch(step StepConfig) error {
	vectorSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("vector_search: vector slot: %w", err)
	}
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("vector_search: result slot: %w", err)
	}

	storeName := step.Input["store"]
	store := c.resolveVectorStore(storeName)

	// collection_slot: optional slot name for runtime collection resolution
	collectionSlot := -1
	if v, ok := step.Input["collection_slot"]; ok && v != "" {
		if s, slotErr := c.getSlot(v); slotErr == nil {
			collectionSlot = s
		}
	}

	// top_k: optional, default 5
	topK := 5
	if v, ok := step.Input["top_k"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
			topK = n
		}
	}

	// min_score: optional, default 0
	minScore := float32(0)
	if v, ok := step.Input["min_score"]; ok && v != "" {
		if f, convErr := strconv.ParseFloat(v, 32); convErr == nil {
			minScore = float32(f)
		}
	}

	// count_slot: optional IntSlot index (literal integer)
	countSlot := -1
	if v, ok := step.Input["count_slot"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil {
			countSlot = n
		}
	}

	defaultCollection := step.Input["collection"]

	cfg := steps.VectorSearchConfig{
		VectorSlot:        vectorSlot,
		ResultSlot:        resultSlot,
		CollectionSlot:    collectionSlot,
		CountSlot:         countSlot,
		TopK:              topK,
		MinScore:          minScore,
		Store:             store,
		DefaultCollection: defaultCollection,
	}

	c.GlobalTable = append(c.GlobalTable, steps.VectorSearch(cfg))
	return nil
}

// compileVectorUpsert handles the "vector_upsert" step type.
//
// StepConfig fields:
//   - key_identifier              → vectorSlot (embedding vector JSON)
//   - input["store"]              → named VectorStore
//   - input["collection"]         → default collection name
//   - input["collection_slot"]    → optional slot name holding collection at runtime
//   - input["content_slot"]       → slot name holding the text chunk to index
//   - input["id_slot"]            → optional slot name holding the item ID (-1 = auto sha256)
//   - input["metadata_slot"]      → optional slot name holding JSON metadata object
func (c *Compiler) compileVectorUpsert(step StepConfig) error {
	vectorSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("vector_upsert: vector slot: %w", err)
	}

	storeName := step.Input["store"]
	store := c.resolveVectorStore(storeName)

	// content_slot: optional (empty content is allowed)
	contentSlot := -1
	if v, ok := step.Input["content_slot"]; ok && v != "" {
		if s, slotErr := c.getSlot(v); slotErr == nil {
			contentSlot = s
		}
	}

	// id_slot: optional (-1 = auto-generate)
	idSlot := -1
	if v, ok := step.Input["id_slot"]; ok && v != "" {
		if s, slotErr := c.getSlot(v); slotErr == nil {
			idSlot = s
		}
	}

	// metadata_slot: optional (-1 = no metadata)
	metadataSlot := -1
	if v, ok := step.Input["metadata_slot"]; ok && v != "" {
		if s, slotErr := c.getSlot(v); slotErr == nil {
			metadataSlot = s
		}
	}

	// collection_slot: optional
	collectionSlot := -1
	if v, ok := step.Input["collection_slot"]; ok && v != "" {
		if s, slotErr := c.getSlot(v); slotErr == nil {
			collectionSlot = s
		}
	}

	defaultCollection := step.Input["collection"]

	cfg := steps.VectorUpsertConfig{
		VectorSlot:        vectorSlot,
		ContentSlot:       contentSlot,
		IDSlot:            idSlot,
		MetadataSlot:      metadataSlot,
		CollectionSlot:    collectionSlot,
		Store:             store,
		DefaultCollection: defaultCollection,
	}

	c.GlobalTable = append(c.GlobalTable, steps.VectorUpsert(cfg))
	return nil
}

// resolveVectorStore returns the named VectorStore from the compiler's map,
// or nil if the map is not set or the name is not found.
// A nil store causes the instruction to return StopPlan at runtime with a 502.
func (c *Compiler) resolveVectorStore(name string) vectorstore.VectorStore {
	if c.VectorStores == nil || name == "" {
		return nil
	}
	return c.VectorStores[name]
}
