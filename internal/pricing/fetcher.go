package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

const liteLLMPricingURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// litellmEntry represents a single model entry from the LiteLLM community pricing JSON.
type litellmEntry struct {
	InputCostPerToken           float64 `json:"input_cost_per_token"`
	OutputCostPerToken          float64 `json:"output_cost_per_token"`
	CacheReadInputTokenCost     float64 `json:"cache_read_input_token_cost"`
	CacheCreationInputTokenCost float64 `json:"cache_creation_input_token_cost"`
	LiteLLMProvider             string  `json:"litellm_provider"`
	MaxTokens                   int     `json:"max_tokens"`
	MaxInputTokens              int     `json:"max_input_tokens"`
	MaxOutputTokens             int     `json:"max_output_tokens"`
}

// PricingFetcher fetches model pricing from provider APIs at startup
type PricingFetcher struct {
	httpClient *http.Client
	timeout    time.Duration
}

// NewPricingFetcher creates a new fetcher with standard timeouts
func NewPricingFetcher(timeout time.Duration) *PricingFetcher {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &PricingFetcher{
		httpClient: &http.Client{Timeout: timeout},
		timeout:    timeout,
	}
}

// fetchRawLiteLLM fetches and decodes the raw LiteLLM pricing JSON keyed by model ID.
func (f *PricingFetcher) fetchRawLiteLLM() (map[string]litellmEntry, error) {
	raw := make(map[string]litellmEntry)
	if err := f.fetchURLAsJSON(liteLLMPricingURL, &raw); err != nil {
		return nil, fmt.Errorf("litellm fetch: %w", err)
	}
	return raw, nil
}

// convertEntry converts a litellmEntry to a PricingInfo, multiplying per-token costs by 1e6.
func convertEntry(entry litellmEntry) PricingInfo {
	info := PricingInfo{
		InputCostPer1MTok:  entry.InputCostPerToken * 1e6,
		OutputCostPer1MTok: entry.OutputCostPerToken * 1e6,
		LastUpdated:        time.Now().Format("2006-01-02"),
		Source:             "litellm-community",
		Cached:             true,
	}
	if entry.CacheReadInputTokenCost > 0 {
		info.CacheCostPer1MTok = entry.CacheReadInputTokenCost * 1e6
	}
	if entry.CacheCreationInputTokenCost > 0 {
		info.CacheWriteCostPer1MTok = entry.CacheCreationInputTokenCost * 1e6
	}
	return info
}

// FetchFromLiteLLM fetches the full model pricing catalog from the LiteLLM community
// pricing JSON and returns a flat map keyed by model ID (e.g. "gpt-4o").
// Entries where both input and output costs are zero are skipped.
// Source is set to "litellm-community" on every returned entry.
func (f *PricingFetcher) FetchFromLiteLLM() (map[string]PricingInfo, error) {
	raw, err := f.fetchRawLiteLLM()
	if err != nil {
		return nil, err
	}

	result := make(map[string]PricingInfo, len(raw))
	for modelID, entry := range raw {
		inputPer1M := entry.InputCostPerToken * 1e6
		outputPer1M := entry.OutputCostPerToken * 1e6

		// Skip models with no pricing data
		if inputPer1M == 0 && outputPer1M == 0 {
			continue
		}

		result[modelID] = convertEntry(entry)
	}

	log.Printf("FetchFromLiteLLM: fetched %d priced models from litellm-community", len(result))
	return result, nil
}

// FetchOpenAIPricing fetches current pricing for OpenAI models.
// Calls FetchFromLiteLLM internally and filters by litellm_provider == "openai".
// Falls back to hardcoded OpenAIPricing on error.
func (f *PricingFetcher) FetchOpenAIPricing() (map[string]PricingInfo, error) {
	return f.fetchByProvider("openai", OpenAIPricing)
}

// FetchAnthropicPricing fetches current pricing for Anthropic models.
// Calls FetchFromLiteLLM internally and filters by litellm_provider == "anthropic".
// Falls back to hardcoded AnthropicPricing on error.
func (f *PricingFetcher) FetchAnthropicPricing() (map[string]PricingInfo, error) {
	return f.fetchByProvider("anthropic", AnthropicPricing)
}

// FetchGooglePricing fetches current pricing for Google models.
// Calls FetchFromLiteLLM internally and filters by litellm_provider == "google".
// Falls back to hardcoded GooglePricing on error.
func (f *PricingFetcher) FetchGooglePricing() (map[string]PricingInfo, error) {
	return f.fetchByProvider("google", GooglePricing)
}

// fetchByProvider fetches the raw LiteLLM data, filters by provider, and returns the result.
// On any fetch/parse error it logs a warning and returns the provided fallback map.
func (f *PricingFetcher) fetchByProvider(provider string, fallback map[string]PricingInfo) (map[string]PricingInfo, error) {
	raw, err := f.fetchRawLiteLLM()
	if err != nil {
		log.Printf("warning: fetchByProvider(%s): litellm fetch failed (%v), using hardcoded fallback", provider, err)
		return fallback, nil
	}

	result := make(map[string]PricingInfo)
	for modelID, entry := range raw {
		if entry.LiteLLMProvider != provider {
			continue
		}
		inputPer1M := entry.InputCostPerToken * 1e6
		outputPer1M := entry.OutputCostPerToken * 1e6
		if inputPer1M == 0 && outputPer1M == 0 {
			continue
		}
		result[modelID] = convertEntry(entry)
	}

	if len(result) == 0 {
		log.Printf("warning: fetchByProvider(%s): no models found in litellm data, using hardcoded fallback", provider)
		return fallback, nil
	}

	return result, nil
}

// FetchAllPricing fetches pricing from all providers and returns merged map
func (f *PricingFetcher) FetchAllPricing() (map[string]map[string]PricingInfo, error) {
	result := make(map[string]map[string]PricingInfo)

	// Fetch OpenAI pricing
	openaiPricing, err := f.FetchOpenAIPricing()
	if err != nil {
		fmt.Printf("warning: failed to fetch OpenAI pricing: %v\n", err)
		openaiPricing = OpenAIPricing // fallback to hardcoded
	}
	result["openai"] = openaiPricing

	// Fetch Anthropic pricing
	anthropicPricing, err := f.FetchAnthropicPricing()
	if err != nil {
		fmt.Printf("warning: failed to fetch Anthropic pricing: %v\n", err)
		anthropicPricing = AnthropicPricing // fallback to hardcoded
	}
	result["anthropic"] = anthropicPricing

	// Fetch Google pricing
	googlePricing, err := f.FetchGooglePricing()
	if err != nil {
		fmt.Printf("warning: failed to fetch Google pricing: %v\n", err)
		googlePricing = GooglePricing // fallback to hardcoded
	}
	result["google"] = googlePricing

	return result, nil
}

// OpenAIPricingResponse represents the response from OpenAI's pricing endpoint (future)
type OpenAIPricingResponse struct {
	Models []struct {
		ID              string  `json:"id"`
		InputCostPer1M  float64 `json:"input_cost_per_1m_tokens"`
		OutputCostPer1M float64 `json:"output_cost_per_1m_tokens"`
	} `json:"models"`
	LastUpdated string `json:"last_updated"`
}

// AnthropicPricingResponse represents the response from Anthropic's pricing endpoint
type AnthropicPricingResponse struct {
	Models []struct {
		Name       string  `json:"name"`
		InputCost  float64 `json:"input_cost_per_1m"`
		OutputCost float64 `json:"output_cost_per_1m"`
	} `json:"models"`
	LastUpdated string `json:"last_updated"`
}

// FetchOpenAIPricingFromAPI fetches from OpenAI's pricing endpoint (when available)
func (f *PricingFetcher) FetchOpenAIPricingFromAPI(apiKey string) (map[string]PricingInfo, error) {
	// OpenAI doesn't yet have a public pricing API endpoint.
	// Placeholder for future integration.
	return OpenAIPricing, nil
}

// FetchAnthropicPricingFromAPI fetches from Anthropic's pricing endpoint
func (f *PricingFetcher) FetchAnthropicPricingFromAPI(apiKey string) (map[string]PricingInfo, error) {
	// Placeholder for future direct Anthropic pricing API integration.
	return AnthropicPricing, nil
}

// fetchURLAsJSON is a helper to fetch JSON from a URL with error handling
func (f *PricingFetcher) fetchURLAsJSON(url string, target interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch from %s: %w", url, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			gatewaylog.Default.Debug("[Pricing] response body close failed",
				gatewaylog.F("error", err.Error()),
			)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("received status %d from %s: %s", resp.StatusCode, url, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("failed to parse JSON response: %w", err)
	}

	return nil
}
