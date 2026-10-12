package appversion

import "context"

// Store persists app versions and per-tenant routing rules.
// Implementations: PostgresStore (shared DB mode), MemoryStore (direct-push mode).
type Store interface {
	// Version lifecycle
	UpsertVersion(ctx context.Context, v AppVersion) error
	GetVersion(ctx context.Context, appName string) (*AppVersion, error)
	ListVersions(ctx context.Context, logicalApp string) ([]AppVersion, error)
	// RetireVersion returns an error if any routing rule still points to appName.
	RetireVersion(ctx context.Context, appName string) error

	// Routing rules
	UpsertRoutingRule(ctx context.Context, r RoutingRule) error
	DeleteRoutingRule(ctx context.Context, tenantID *uint16, logicalApp, env string) error
	ListRoutingRules(ctx context.Context, logicalApp string) ([]RoutingRule, error)
	// ResolveRoute returns the active physical app name for the given tenant+env.
	// Priority: tenant-specific rule > default (nil-tenant) rule > logicalApp as-is.
	ResolveRoute(ctx context.Context, tenantID uint16, logicalApp, env string) (string, error)
}
