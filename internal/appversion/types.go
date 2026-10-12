package appversion

import "time"

// AppVersion is a named, immutable snapshot of an app bundle deployed to the gateway.
// AppName is the physical name (e.g. "orders-v2"); LogicalApp is the family (e.g. "orders").
type AppVersion struct {
	AppName    string    `json:"app_name"`
	LogicalApp string    `json:"logical_app"`
	Version    string    `json:"version"`
	Status     string    `json:"status"`      // "draft" | "active" | "retired"
	BundleHash string    `json:"bundle_hash"` // SHA-256 of the deployed UnifiedSyncRequest
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// RoutingRule maps a tenant (or all tenants when TenantID is nil) to an active app version
// within a given environment.
type RoutingRule struct {
	TenantID    *uint16   `json:"tenant_id,omitempty"` // nil = default for all tenants
	LogicalApp  string    `json:"logical_app"`
	ActiveApp   string    `json:"active_app"` // physical app name, e.g. "orders-v2"
	Environment string    `json:"environment"`
	UpdatedBy   string    `json:"updated_by,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}
