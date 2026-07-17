package engine

import (
	"log"
	"net/http"
	"github.com/amitkhosla/rah/internal/rctx"
)

// APIKeyResolver provides multi-level API key resolution with fallback chain.
// Resolution order (first match wins):
// 1. X-API-Key header (runtime override)
// 2. Per-tenant per-model override
// 3. Per-tenant default key
// 4. Configured/baked key
// 5. Empty string
type APIKeyResolver struct {
	// tenantKeys maps: "tenantID:modelAlias" â†’ API key
	// or "tenantID:default" â†’ default key for tenant
	tenantKeys map[string]string
}

// NewAPIKeyResolver creates a new resolver
func NewAPIKeyResolver() *APIKeyResolver {
	return &APIKeyResolver{
		tenantKeys: make(map[string]string),
	}
}

// RegisterTenantKey registers an API key for a specific tenant
func (r *APIKeyResolver) RegisterTenantKey(tenantID, modelAlias, apiKey string) {
	if tenantID == "" {
		return
	}

	key := tenantID + ":" + modelAlias
	r.tenantKeys[key] = apiKey
	log.Printf("Registered API key for tenant %s model %s", tenantID, modelAlias)
}

// RegisterTenantDefaultKey registers a default API key for a tenant (used for all models)
func (r *APIKeyResolver) RegisterTenantDefaultKey(tenantID, apiKey string) {
	if tenantID == "" {
		return
	}

	key := tenantID + ":default"
	r.tenantKeys[key] = apiKey
	log.Printf("Registered default API key for tenant %s", tenantID)
}

// Resolve returns the API key to use with the fallback chain:
// 1. X-API-Key header (if request available)
// 2. Per-tenant per-model key
// 3. Per-tenant default key
// 4. Baked/configured key
func (r *APIKeyResolver) Resolve(
	ctx *rctx.Context,
	req *http.Request,
	tenantID, modelAlias, bakedKey string,
) string {
	// 1. Check X-API-Key header (if request available)
	if req != nil {
		if headerKey := req.Header.Get("X-API-Key"); headerKey != "" {
			return headerKey
		}
	}

	// 2. Check per-tenant per-model key
	if tenantID != "" && modelAlias != "" {
		key := tenantID + ":" + modelAlias
		if apiKey, ok := r.tenantKeys[key]; ok {
			return apiKey
		}
	}

	// 3. Check per-tenant default key
	if tenantID != "" {
		key := tenantID + ":default"
		if apiKey, ok := r.tenantKeys[key]; ok {
			return apiKey
		}
	}

	// 4. Return baked/configured key
	return bakedKey
}

// GetRegisteredKeys returns all registered tenant keys (for admin purposes)
func (r *APIKeyResolver) GetRegisteredKeys() map[string]string {
	result := make(map[string]string)
	for k := range r.tenantKeys {
		// Don't return actual keys in logs, just indicate they're registered
		result[k] = "***masked***"
	}
	return result
}
