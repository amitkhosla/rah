// File: internal/pricing/manager.go
// Copy this to your project

package pricing

import (
	"fmt"
	"log"
	"rah/internal/config"
	"sync"
	"time"
)

// PricingInfo holds cost information for a model
type PricingInfo struct {
	InputCostPer1MTok      float64 `json:"input_cost_per_1m_tokens"`
	OutputCostPer1MTok     float64 `json:"output_cost_per_1m_tokens"`
	CacheCostPer1MTok      float64 `json:"cache_cost_per_1m_tokens,omitempty"`
	CacheWriteCostPer1MTok float64 `json:"cache_write_cost_per_1m_tokens,omitempty"`
	LastUpdated            string  `json:"last_updated"`
	Source                 string  `json:"source,omitempty"`
	Cached                 bool    `json:"cached"`
}

// PricingManager manages model pricing with caching and auto-refresh
type PricingManager struct {
	mu        sync.RWMutex
	cache     map[string]PricingInfo
	ttl       time.Duration
	lastFetch time.Time
	stopCh    chan struct{}
}

// NewPricingManager creates a pricing manager with auto-refresh
func NewPricingManager() *PricingManager {
	pm := &PricingManager{
		cache:  make(map[string]PricingInfo),
		ttl:    time.Hour,  // Refresh hourly
		stopCh: make(chan struct{}),
	}

	// Bootstrap from hardcoded data
	pm.bootstrapPricing()

	// Start background refresh
	go pm.backgroundRefresh()

	return pm
}

// GetPrice retrieves pricing (cache-first, fallback to fetch)
func (pm *PricingManager) GetPrice(provider, modelID string) (PricingInfo, error) {
	pm.mu.RLock()
	cachedPrice, ok := pm.cache[modelID]
	lastFetch := pm.lastFetch
	pm.mu.RUnlock()

	// Return cache if fresh and found
	if ok && time.Since(lastFetch) < pm.ttl {
		return cachedPrice, nil
	}

	// Cache miss or stale → attempt fresh fetch
	var price PricingInfo
	var err error

	switch provider {
	case "openai":
		price, err = GetOpenAIPricing(modelID)
	case "anthropic":
		price, err = GetAnthropicPricing(modelID)
	case "google":
		price, err = GetGooglePricing(modelID)
	case "custom":
		// Local models have no pricing (return zero)
		return PricingInfo{}, nil
	default:
		return PricingInfo{}, fmt.Errorf("unknown provider: %s", provider)
	}

	if err != nil {
		// Fetch failed: fallback to cached value if available
		if ok {
			log.Printf("Warning: pricing fetch failed for %s: %v, using cached value", modelID, err)
			return cachedPrice, nil
		}
		log.Printf("Error: pricing not found for %s/%s: %v", provider, modelID, err)
		return PricingInfo{}, err
	}

	// Update cache with fresh data
	price.Cached = true
	price.LastUpdated = time.Now().Format("2006-01-02 15:04:05 UTC")

	pm.mu.Lock()
	pm.cache[modelID] = price
	pm.lastFetch = time.Now()
	pm.mu.Unlock()

	log.Printf("Updated pricing for %s/%s: $%.6f in, $%.6f out", provider, modelID, price.InputCostPer1MTok, price.OutputCostPer1MTok)

	return price, nil
}

// GetPricing returns all cached pricing (for admin dashboard)
func (pm *PricingManager) GetPricing() map[string]PricingInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make(map[string]PricingInfo)
	for k, v := range pm.cache {
		result[k] = v
	}
	return result
}

// bootstrapPricing loads all hardcoded pricing data as fallback
func (pm *PricingManager) bootstrapPricing() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Load OpenAI models
	for modelID, price := range OpenAIPricing {
		price.Cached = true
		price.LastUpdated = "bootstrapped"
		pm.cache[modelID] = price
	}

	// Load Anthropic models
	for modelID, price := range AnthropicPricing {
		price.Cached = true
		price.LastUpdated = "bootstrapped"
		pm.cache[modelID] = price
	}

	// Load Google models
	for modelID, price := range GooglePricing {
		price.Cached = true
		price.LastUpdated = "bootstrapped"
		pm.cache[modelID] = price
	}

	pm.lastFetch = time.Now()
	log.Printf("Bootstrap complete: loaded %d models from hardcoded defaults", len(pm.cache))
}

// LoadFromConfig loads pricing from the GatewayConfig's Pricing section
// This overrides bootstrapped defaults. If config has no pricing, bootstrapped defaults remain.
// Graceful: if pricing config is empty or missing, gateway continues to work.
func (pm *PricingManager) LoadFromConfig(pricingCfg config.PricingConfig) error {
	if len(pricingCfg.Models) == 0 {
		// No explicit pricing in config, use bootstrapped defaults
		log.Printf("No explicit pricing in config, using bootstrapped defaults")
		return nil
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	loadedCount := 0
	for _, modelPricing := range pricingCfg.Models {
		if modelPricing.Model == "" {
			continue
		}

		price := PricingInfo{
			InputCostPer1MTok:  modelPricing.CostPerInputToken,
			OutputCostPer1MTok: modelPricing.CostPerOutputToken,
			LastUpdated:        time.Now().Format("2006-01-02 15:04:05 UTC"),
			Source:             "config",
			Cached:             true,
		}

		pm.cache[modelPricing.Model] = price
		loadedCount++
	}

	log.Printf("Loaded %d pricing entries from config (merged with %d bootstrapped)", loadedCount, len(pm.cache)-loadedCount)
	return nil
}

// LoadFromLLMConfig extracts pricing from individual LLMModelConfig entries
// This is a second-level fallback: per-model pricing defined in the llm.models section
// Example: llm.models[0].cost_per_input_token = 5.0
func (pm *PricingManager) LoadFromLLMConfig(llmCfg config.LLMConfig) error {
	if len(llmCfg.Models) == 0 {
		return nil
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	loadedCount := 0
	for _, modelCfg := range llmCfg.Models {
		if modelCfg.Alias == "" {
			continue
		}

		// Only use if both costs are specified
		if modelCfg.CostPerInputToken > 0 && modelCfg.CostPerOutputToken > 0 {
			price := PricingInfo{
				InputCostPer1MTok:  modelCfg.CostPerInputToken,
				OutputCostPer1MTok: modelCfg.CostPerOutputToken,
				LastUpdated:        time.Now().Format("2006-01-02 15:04:05 UTC"),
				Source:             "llm_config",
				Cached:             true,
			}

			// Only override if not already set from pricing config
			if _, exists := pm.cache[modelCfg.Alias]; !exists {
				pm.cache[modelCfg.Alias] = price
				loadedCount++
			}
		}
	}

	if loadedCount > 0 {
		log.Printf("Loaded %d pricing entries from llm.models[] section", loadedCount)
	}
	return nil
}

// ForceRefresh immediately refreshes all pricing
func (pm *PricingManager) ForceRefresh() {
	log.Println("Force refreshing pricing data...")
	pm.mu.Lock()
	pm.lastFetch = time.Time{}  // Mark cache as stale
	pm.mu.Unlock()
}

// backgroundRefresh periodically invalidates the cache
func (pm *PricingManager) backgroundRefresh() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Println("Pricing cache TTL expired, will refresh on next request")
			pm.mu.Lock()
			pm.lastFetch = time.Time{}  // Mark cache as stale
			pm.mu.Unlock()

		case <-pm.stopCh:
			log.Println("Pricing manager stopped")
			return
		}
	}
}

// Stop gracefully stops the pricing manager
func (pm *PricingManager) Stop() {
	close(pm.stopCh)
}

// CalculateCost computes request cost given tokens and pricing
func CalculateCost(inputTokens, outputTokens int, pricing PricingInfo) float64 {
	inputCost := (float64(inputTokens) / 1e6) * pricing.InputCostPer1MTok
	outputCost := (float64(outputTokens) / 1e6) * pricing.OutputCostPer1MTok
	return inputCost + outputCost
}
