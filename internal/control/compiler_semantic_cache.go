package control

import (
	"fmt"
	"strconv"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileSemanticCacheGet handles the "semantic_cache_get" step type.
//
// StepConfig fields:
//   - key_identifier       â†’ querySlot (query text to embed and cache lookup)
//   - as                   â†’ resultSlot (cached response on hit)
//   - input["hit_slot"]    â†’ BoolSlot name for cache hit/miss flag
//   - input["store"]       â†’ vector store name (required)
//   - input["collection"]  â†’ collection name (optional, default "semantic_cache")
//   - input["min_score"]   â†’ minimum similarity threshold (default 0.92)
//   - input["provider"]    â†’ embedding provider (openai, ollama, gemini; required)
//   - input["model"]       â†’ embedding model name (required)
//   - input["base_url"]    â†’ optional embedding endpoint override
//   - input["api_key_ref"] â†’ embedding API key or secret reference
//   - input["api_key_slot"]â†’ optional slot name for runtime key injection
//   - input["timeout_ms"]  â†’ optional timeout (default 10000)
//   - input["max_retries"] â†’ optional retry count (default 2)
func (c *Compiler) compileSemanticCacheGet(step StepConfig) error {
	querySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("semantic_cache_get: query slot: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("semantic_cache_get: result slot: %w", err)
	}

	hitSlot, err := c.getBoolSlot(step.Input["hit_slot"])
	if err != nil {
		return fmt.Errorf("semantic_cache_get: hit slot: %w", err)
	}

	// Resolve vector store
	storeName := step.Input["store"]
	if storeName == "" {
		return fmt.Errorf("semantic_cache_get: input[\"store\"] is required")
	}

	store, ok := c.VectorStores[storeName]
	if !ok {
		return fmt.Errorf("semantic_cache_get: vector store %q not found", storeName)
	}

	collection := step.Input["collection"]
	if collection == "" {
		collection = "semantic_cache"
	}

	minScore := float32(0.92)
	if v := step.Input["min_score"]; v != "" {
		if f, err := strconv.ParseFloat(v, 32); err == nil && f > 0 {
			minScore = float32(f)
		}
	}

	// Build embed config (reuse pattern from compiler_embed.go)
	providerStr := step.Input["provider"]
	if providerStr == "" {
		return fmt.Errorf("semantic_cache_get: input[\"provider\"] is required (openai, ollama, gemini)")
	}
	provider := steps.EmbedProvider(providerStr)
	switch provider {
	case steps.EmbedProviderOpenAI, steps.EmbedProviderOllama, steps.EmbedProviderGemini:
		// valid
	default:
		return fmt.Errorf("semantic_cache_get: unsupported provider %q; must be openai, ollama, or gemini", providerStr)
	}

	model := step.Input["model"]
	if model == "" {
		return fmt.Errorf("semantic_cache_get: input[\"model\"] is required")
	}

	baseURL := step.Input["base_url"]
	apiKey := step.Input["api_key_ref"]

	apiKeySlot := -1
	if v, ok := step.Input["api_key_slot"]; ok && v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("semantic_cache_get: api_key_slot: %w", slotErr)
		}
		apiKeySlot = s
	}

	timeoutMs := 10_000
	if v, ok := step.Input["timeout_ms"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	maxRetries := 2
	if v, ok := step.Input["max_retries"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n >= 0 {
			maxRetries = n
		}
	}

	embedCfg := steps.EmbedTextConfig{
		InputSlot:  -1, // not used; query comes from cfg.QuerySlot
		ResultSlot: -1, // not used; we extract vector directly
		DimSlot:    -1,
		Provider:   provider,
		Model:      model,
		BaseURL:    baseURL,
		APIKey:     apiKey,
		APIKeySlot: apiKeySlot,
		TimeoutMs:  timeoutMs,
		MaxRetries: maxRetries,
	}

	sharedCollectionGet := false
	if v, ok := step.Input["shared_collection"]; ok && v == "true" {
		sharedCollectionGet = true
	}

	cfg := steps.SemanticCacheGetConfig{
		EmbedCfg:         embedCfg,
		Store:            store,
		Collection:       collection,
		SharedCollection: sharedCollectionGet,
		QuerySlot:        querySlot,
		ResultSlot:       resultSlot,
		HitSlot:          hitSlot,
		MinScore:         minScore,
	}

	c.GlobalTable = append(c.GlobalTable, steps.SemanticCacheGet(cfg))
	return nil
}

// compileSemanticCachePut handles the "semantic_cache_put" step type.
//
// StepConfig fields:
//   - key_identifier       â†’ querySlot (query text to embed as cache key)
//   - input["response"]    â†’ responseSlot (response to cache)
//   - input["store"]       â†’ vector store name (required)
//   - input["collection"]  â†’ collection name (optional, default "semantic_cache")
//   - input["provider"]    â†’ embedding provider (openai, ollama, gemini; required)
//   - input["model"]       â†’ embedding model name (required)
//   - input["base_url"]    â†’ optional embedding endpoint override
//   - input["api_key_ref"] â†’ embedding API key or secret reference
//   - input["api_key_slot"]â†’ optional slot name for runtime key injection
//   - input["timeout_ms"]  â†’ optional timeout (default 10000)
//   - input["max_retries"] â†’ optional retry count (default 2)
func (c *Compiler) compileSemanticCachePut(step StepConfig) error {
	querySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("semantic_cache_put: query slot: %w", err)
	}

	responseSlot, err := c.getSlotReadOnly(step.Input["response"])
	if err != nil {
		return fmt.Errorf("semantic_cache_put: response slot: %w", err)
	}

	// Resolve vector store
	storeName := step.Input["store"]
	if storeName == "" {
		return fmt.Errorf("semantic_cache_put: input[\"store\"] is required")
	}

	store, ok := c.VectorStores[storeName]
	if !ok {
		return fmt.Errorf("semantic_cache_put: vector store %q not found", storeName)
	}

	collection := step.Input["collection"]
	if collection == "" {
		collection = "semantic_cache"
	}

	// Build embed config
	providerStr := step.Input["provider"]
	if providerStr == "" {
		return fmt.Errorf("semantic_cache_put: input[\"provider\"] is required (openai, ollama, gemini)")
	}
	provider := steps.EmbedProvider(providerStr)
	switch provider {
	case steps.EmbedProviderOpenAI, steps.EmbedProviderOllama, steps.EmbedProviderGemini:
		// valid
	default:
		return fmt.Errorf("semantic_cache_put: unsupported provider %q; must be openai, ollama, or gemini", providerStr)
	}

	model := step.Input["model"]
	if model == "" {
		return fmt.Errorf("semantic_cache_put: input[\"model\"] is required")
	}

	baseURL := step.Input["base_url"]
	apiKey := step.Input["api_key_ref"]

	apiKeySlot := -1
	if v, ok := step.Input["api_key_slot"]; ok && v != "" {
		s, slotErr := c.getSlot(v)
		if slotErr != nil {
			return fmt.Errorf("semantic_cache_put: api_key_slot: %w", slotErr)
		}
		apiKeySlot = s
	}

	timeoutMs := 10_000
	if v, ok := step.Input["timeout_ms"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	maxRetries := 2
	if v, ok := step.Input["max_retries"]; ok && v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n >= 0 {
			maxRetries = n
		}
	}

	embedCfg := steps.EmbedTextConfig{
		InputSlot:  -1, // not used; query comes from cfg.QuerySlot
		ResultSlot: -1, // not used; we extract vector directly
		DimSlot:    -1,
		Provider:   provider,
		Model:      model,
		BaseURL:    baseURL,
		APIKey:     apiKey,
		APIKeySlot: apiKeySlot,
		TimeoutMs:  timeoutMs,
		MaxRetries: maxRetries,
	}

	sharedCollectionPut := false
	if v, ok := step.Input["shared_collection"]; ok && v == "true" {
		sharedCollectionPut = true
	}

	cfg := steps.SemanticCachePutConfig{
		EmbedCfg:         embedCfg,
		Store:            store,
		Collection:       collection,
		SharedCollection: sharedCollectionPut,
		QuerySlot:        querySlot,
		ResponseSlot:     responseSlot,
	}

	c.GlobalTable = append(c.GlobalTable, steps.SemanticCachePut(cfg))
	return nil
}
