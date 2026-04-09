// File: internal/pricing/pricing_data.go
// Hardcoded pricing data for all providers (updated 2025-04-01)

package pricing

import "fmt"

// OpenAIPricing contains current pricing for OpenAI models
var OpenAIPricing = map[string]PricingInfo{
	"gpt-4o": {
		InputCostPer1MTok:  5.00,      // $5 per 1M input tokens
		OutputCostPer1MTok: 15.00,     // $15 per 1M output tokens
		LastUpdated:        "2025-04-01",
		Source:             "https://openai.com/pricing",
	},
	"gpt-4o-mini": {
		InputCostPer1MTok:  0.15,
		OutputCostPer1MTok: 0.60,
		LastUpdated:        "2025-04-01",
		Source:             "https://openai.com/pricing",
	},
	"gpt-4-turbo": {
		InputCostPer1MTok:  3.00,
		OutputCostPer1MTok: 6.00,
		LastUpdated:        "2025-04-01",
		Source:             "https://openai.com/pricing",
	},
	"gpt-3.5-turbo": {
		InputCostPer1MTok:  0.50,
		OutputCostPer1MTok: 1.50,
		LastUpdated:        "2025-04-01",
		Source:             "https://openai.com/pricing",
	},
	"o1": {
		InputCostPer1MTok:  15.00,
		OutputCostPer1MTok: 60.00,
		LastUpdated:        "2025-04-01",
		Source:             "https://openai.com/pricing",
	},
	"o1-mini": {
		InputCostPer1MTok:  3.00,
		OutputCostPer1MTok: 12.00,
		LastUpdated:        "2025-04-01",
		Source:             "https://openai.com/pricing",
	},
}

// AnthropicPricing contains current pricing for Anthropic models
// Note: Anthropic also charges for cache reads (10% of input) and writes (25% of input)
var AnthropicPricing = map[string]PricingInfo{
	"claude-opus": {
		InputCostPer1MTok:      15.00,    // $15 per 1M input tokens
		OutputCostPer1MTok:     75.00,    // $75 per 1M output tokens
		CacheCostPer1MTok:      1.50,     // Cache reads cost 10% of input
		CacheWriteCostPer1MTok: 1.875,    // Cache writes cost 25% of input
		LastUpdated:            "2025-04-01",
		Source:                 "https://www.anthropic.com/pricing",
	},
	"claude-sonnet": {
		InputCostPer1MTok:      3.00,
		OutputCostPer1MTok:     15.00,
		CacheCostPer1MTok:      0.30,     // 10% of input
		CacheWriteCostPer1MTok: 0.375,    // 25% of input
		LastUpdated:            "2025-04-01",
		Source:                 "https://www.anthropic.com/pricing",
	},
	"claude-haiku": {
		InputCostPer1MTok:      0.08,
		OutputCostPer1MTok:     0.40,
		CacheCostPer1MTok:      0.008,    // 10% of input
		CacheWriteCostPer1MTok: 0.01,     // 25% of input
		LastUpdated:            "2025-04-01",
		Source:                 "https://www.anthropic.com/pricing",
	},
}

// GooglePricing contains current pricing for Google AI models
var GooglePricing = map[string]PricingInfo{
	"gemini-2.0-flash": {
		InputCostPer1MTok:  0.075,      // $0.075 per 1M input tokens
		OutputCostPer1MTok: 0.30,       // $0.30 per 1M output tokens
		LastUpdated:        "2025-04-01",
		Source:             "https://ai.google.dev/pricing",
	},
	"gemini-2.0-mini": {
		InputCostPer1MTok:  0.032,
		OutputCostPer1MTok: 0.08,
		LastUpdated:        "2025-04-01",
		Source:             "https://ai.google.dev/pricing",
	},
	"gemini-1.5-pro": {
		InputCostPer1MTok:  0.625,      // $0.625 per 1M input tokens
		OutputCostPer1MTok: 1.25,       // $1.25 per 1M output tokens
		LastUpdated:        "2025-04-01",
		Source:             "https://ai.google.dev/pricing",
	},
	"gemini-1.5-flash": {
		InputCostPer1MTok:  0.075,
		OutputCostPer1MTok: 0.30,
		LastUpdated:        "2025-04-01",
		Source:             "https://ai.google.dev/pricing",
	},
}

// GetOpenAIPricing retrieves pricing for an OpenAI model
func GetOpenAIPricing(modelID string) (PricingInfo, error) {
	if price, ok := OpenAIPricing[modelID]; ok {
		price.Cached = true
		return price, nil
	}
	return PricingInfo{}, fmt.Errorf("unknown OpenAI model: %s", modelID)
}

// GetAnthropicPricing retrieves pricing for an Anthropic model
func GetAnthropicPricing(modelID string) (PricingInfo, error) {
	if price, ok := AnthropicPricing[modelID]; ok {
		price.Cached = true
		return price, nil
	}
	return PricingInfo{}, fmt.Errorf("unknown Anthropic model: %s", modelID)
}

// GetGooglePricing retrieves pricing for a Google model
func GetGooglePricing(modelID string) (PricingInfo, error) {
	if price, ok := GooglePricing[modelID]; ok {
		price.Cached = true
		return price, nil
	}
	return PricingInfo{}, fmt.Errorf("unknown Google model: %s", modelID)
}
