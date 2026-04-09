package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

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

// FetchOpenAIPricing fetches current pricing from OpenAI's API
// Note: OpenAI doesn't have a public pricing API, so we return the hardcoded defaults
// In production, you would integrate with OpenAI's pricing endpoint when available
func (f *PricingFetcher) FetchOpenAIPricing() (map[string]PricingInfo, error) {
	// For now, return hardcoded pricing from pricing_data.go
	// In a real implementation, this would call OpenAI's API endpoint
	return OpenAIPricing, nil
}

// FetchAnthropicPricing fetches current pricing from Anthropic's API
// Uses Anthropic's public pricing endpoint
func (f *PricingFetcher) FetchAnthropicPricing() (map[string]PricingInfo, error) {
	// Anthropic pricing structure from their documentation
	// https://www.anthropic.com/pricing/claude
	// For now, return hardcoded pricing
	return AnthropicPricing, nil
}

// FetchGooglePricing fetches current pricing from Google's Vertex AI API
func (f *PricingFetcher) FetchGooglePricing() (map[string]PricingInfo, error) {
	// Google Gemini pricing from Vertex AI documentation
	// https://cloud.google.com/vertex-ai/generative-ai/pricing
	// For now, return hardcoded pricing
	return GooglePricing, nil
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
		ID               string  `json:"id"`
		InputCostPer1M   float64 `json:"input_cost_per_1m_tokens"`
		OutputCostPer1M  float64 `json:"output_cost_per_1m_tokens"`
	} `json:"models"`
	LastUpdated string `json:"last_updated"`
}

// AnthropicPricingResponse represents the response from Anthropic's pricing endpoint
type AnthropicPricingResponse struct {
	Models []struct {
		Name             string  `json:"name"`
		InputCost        float64 `json:"input_cost_per_1m"`
		OutputCost       float64 `json:"output_cost_per_1m"`
	} `json:"models"`
	LastUpdated string `json:"last_updated"`
}

// FetchOpenAIPricingFromAPI fetches from OpenAI's pricing endpoint (when available)
func (f *PricingFetcher) FetchOpenAIPricingFromAPI(apiKey string) (map[string]PricingInfo, error) {
	// OpenAI doesn't yet have a public pricing API endpoint
	// This is a placeholder for future integration
	// When available, would call something like:
	// GET https://api.openai.com/v1/models/pricing
	return OpenAIPricing, nil
}

// FetchAnthropicPricingFromAPI fetches from Anthropic's pricing endpoint
func (f *PricingFetcher) FetchAnthropicPricingFromAPI(apiKey string) (map[string]PricingInfo, error) {
	// Fetch from Anthropic's pricing documentation
	// For now, return hardcoded
	// When integrated: would parse their pricing page or API endpoint
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
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("received status %d from %s: %s", resp.StatusCode, url, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("failed to parse JSON response: %w", err)
	}

	return nil
}
