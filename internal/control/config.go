package control

import "github.com/amitkhosla/rah/internal/config"

// GatewayConfig is the root configuration object for the entire gateway.
type GatewayConfig struct {
	// Global flows that can be called by multiple APIs (e.g., "auth", "logging")
	Flows map[string][]StepConfig `json:"flows"`
	// Specific API endpoints and their associated entry flows
	Apis []ApiConfig `json:"apis"`
	// DataStores configures where each logical data domain is persisted.
	DataStores config.DataStoreConfig `json:"data_stores"`
	// LLM configures the model catalog used by llm_call steps.
	LLM config.LLMConfig `json:"llm,omitempty"`
	// UpstreamDefaults configures default upstream passthrough behavior for all HTTP steps.
	UpstreamDefaults *UpstreamPassthroughConfig `json:"upstream_defaults,omitempty"`
}
