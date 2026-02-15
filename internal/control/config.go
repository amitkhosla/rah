package control

// GatewayConfig is the root configuration object for the entire gateway.
type GatewayConfig struct {
	// Global flows that can be called by multiple APIs (e.g., "auth", "logging")
	Flows map[string][]StepConfig `json:"flows"`
	// Specific API endpoints and their associated entry flows
	Apis []ApiConfig `json:"apis"`
}
