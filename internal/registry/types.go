package registry

import (
	"context"
	"hash/maphash"
	"sync/atomic"
)

// ─── Alias Table (Hot Path) ───────────────────────────────────────────────────

// AliasSlot is 16 bytes — four slots fit in one 64-byte cache line.
// TenantID == 0 marks an empty slot; TenantID 0 is never assigned to a real tenant.
type AliasSlot struct {
	Hash     uint64 // maphash result — fast reject before string compare
	Offset   uint32 // byte offset of alias start in AliasTable.StringArena
	Len      uint16 // alias length in bytes
	TenantID uint16 // result; 0 = empty slot
}

// AliasTable is an immutable open-addressing hash table mapping alias strings to
// TenantIDs. Built once per management-plane update and installed via atomic
// pointer swap. Never mutated after installation — lock-free reads guaranteed.
type AliasTable struct {
	Slots       []AliasSlot  // power-of-2 length, linear probing, ≤75% max load
	Mask        uint32       // len(Slots) - 1
	_           uint32       // padding to align StringArena slice header
	StringArena []byte       // packed alias bytes; Slots contain no GC pointers
	Seed        maphash.Seed // fixed for the process lifetime
}

// ─── Rate Limit Types ─────────────────────────────────────────────────────────

// RateLimitFlags control per-entry rate limit and access behaviour.
type RateLimitFlags uint16

const (
	RLBlocked     RateLimitFlags = 1 << 0 // access denied for this API / endpoint
	RLCustomValue RateLimitFlags = 1 << 1 // use entry's rate values, not global default
	RLDisabled    RateLimitFlags = 1 << 2 // rate limiting off (unlimited)
)

// TenantRLFlags control tenant-wide rate limit adjustments.
type TenantRLFlags uint16

const (
	TenantBlocked   TenantRLFlags = 1 << 0 // all APIs blocked for this tenant
	TenantRLDisabled TenantRLFlags = 1 << 1 // rate limiting off for all APIs
)

// RateLimitConfig is the system-wide rate limit default for one RateLimitConfigId.
// RateLimitConfigId 0 is the system baseline (used when no specific config matches).
// RateLimitConfigIds are assigned to APIs and endpoints by the control plane at bake time.
type RateLimitConfig struct {
	PerSec      uint32 // max requests/second; 0 = unlimited
	PerMin      uint32 // max requests/minute; 0 = unlimited
	BurstFactor uint16 // burst multiplier: 100 = 1x, 150 = 1.5x, 200 = 2x
	Flags       uint16 // reserved for future use
}

// TenantRateLimitModifier is the lightweight per-tenant rate limit adjustment applied
// to every ResolveRateLimit call. Stored as a dense array indexed by TenantID (8 bytes each).
type TenantRateLimitModifier struct {
	ScalePct int16         // %-adjustment: +20 → 120%, -50 → 50%, 0 = no scaling
	Flags    TenantRLFlags // TenantBlocked | TenantRLDisabled
	_        uint32        // pad to 8 bytes
}

// TenantRateLimitEntry is one row in a TenantRateLimitTable — a sparse per-tenant
// override for a single RateLimitConfigId (API-level or endpoint-level).
type TenantRateLimitEntry struct {
	PerSec uint32
	PerMin uint32
	Flags  RateLimitFlags // RLBlocked | RLCustomValue | RLDisabled
	_      uint16         // pad to 12 bytes
}

// TenantRateLimitTable holds sparse per-tenant rate limit overrides.
// RateLimitConfigIds are kept sorted ascending for binary search (~5–50 entries → 3–6 comparisons).
type TenantRateLimitTable struct {
	PolicyIDs []uint16               // sorted ascending
	Entries   []TenantRateLimitEntry // parallel with PolicyIDs
}

// ─── General Property Storage (Management Plane) ─────────────────────────────

// RegistryNode is a 16-byte node for the Properties radix tree (key → KeyID).
// Uses offsets instead of pointers so the GC never scans the arena.
// Used by the management plane only — not in the request hot path.
type RegistryNode struct {
	PrefixOffset uint32
	PrefixLen    uint16
	ChildBase    uint32
	ChildCount   uint16
	Value        uint16
	Padding      uint16
}

// ─── Property Store ───────────────────────────────────────────────────────────

// PropStore is an independent key-value store for one property namespace.
// Each namespace (URLs, Identifiers, Metadata) has its own PropStore so key
// names never collide across namespaces and strides stay small.
type PropStore struct {
	Matrix     []uint32       // flat [TenantID * Stride + KeyID] = ValueID
	Stride     uint32         // number of columns; grows as new keys are added
	Keys       []RegistryNode // radix tree: key name → KeyID
	StringPool []byte         // backing store for Keys radix prefix strings
}

// ─── Registry Snapshot ────────────────────────────────────────────────────────

// TenantRegistry is the immutable system snapshot installed via atomic pointer swap.
// All fields are read-only after construction; hot-path reads are lock-free.
type TenantRegistry struct {
	// ── Hot path ─────────────────────────────────────────────────────────────

	// Aliases resolves incoming hostnames / identifiers to TenantIDs in ~40–70 ns.
	Aliases AliasTable

	// RateLimitConfigs holds system-wide rate limit defaults indexed by RateLimitConfigId.
	// Index 0 is the system baseline (PerSec=0 means unlimited).
	RateLimitConfigs []RateLimitConfig

	// TenantModifiers holds per-tenant rate limit adjustments, indexed by TenantID.
	// ScalePct and global block flags are applied to every rate limit resolution.
	TenantModifiers []TenantRateLimitModifier

	// TenantRateLimits holds sparse per-tenant rate limit overrides indexed by TenantID.
	// nil means "use global default + modifier only" — true for most tenants.
	TenantRateLimits []*TenantRateLimitTable

	// ── Reserved: consumer-side identity ─────────────────────────────────────
	// Always nil today. Declared now to prevent structural rewrites when
	// Products / Apps / AppKeys are introduced in a later phase.
	ProductRateLimits []*TenantRateLimitTable
	AppRateLimits     []*TenantRateLimitTable
	AppKeyRateLimits  []*TenantRateLimitTable

	// ── Management plane (general key-value properties) ──────────────────────

	// URLs stores service endpoint URLs (keys: "primary", "fallback", "health" …).
	URLs PropStore
	// IDs stores credentials / identifiers (keys: "api_key", "client_id" …).
	IDs PropStore
	// Meta stores arbitrary metadata (keys: "tier", "region", "plan" …).
	Meta PropStore

	// ValuePool stores the actual []byte values referenced by all three stores.
	ValuePool [][]byte

	// MaxTenants is the capacity: number of tenant rows allocated in each matrix.
	MaxTenants uint16
}

// ─── Global State ─────────────────────────────────────────────────────────────

// GlobalState holds the live registry snapshot and recycled TenantID slots.
type GlobalState struct {
	Active    atomic.Pointer[TenantRegistry]
	FreeSlots []uint16 // stack of deleted TenantIDs available for reuse
}

var State = &GlobalState{}

// ─── Rate Limit Resolution Result ─────────────────────────────────────────────

// ResolvedLimit is the effective rate limit returned by ResolveRateLimit after
// all four resolution layers (gateway → API → endpoint → tenant modifier).
// Fits in two CPU registers; zero allocation on the hot path.
type ResolvedLimit struct {
	PerSec      uint32
	PerMin      uint32
	BurstFactor uint16
	_           uint16 // pad to 12 bytes
}

// ─── Registry Persistence ─────────────────────────────────────────────────────

// TenantRecord is the management-plane record for one tenant.
// Maintained in RegistryManager.tenantData and serialised for persistence.
// PersistedID, when non-zero, carries the TenantID recovered from the datastore
// at startup so the same numeric ID can be re-assigned on restore.
// It is zero in all other contexts (live mutations, test tenants, etc.).
type TenantRecord struct {
	Aliases     []string          `json:"aliases"`
	// PersistedID is populated only during startup restore from the datastore.
	// It is not serialised; it is read separately via GetTenantID.
	PersistedID uint16            `json:"-"`
	ServiceURLs map[string]string `json:"service_urls,omitempty"`
	Identifiers map[string]string `json:"identifiers,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   int64             `json:"created_at,omitempty"` // Unix seconds; used for TTL sweep of test tenants

	// LogLevel overrides the gateway-wide log level for requests belonging to this tenant.
	// Valid values: "debug", "info", "warn", "error", "".
	// Empty string = no override (gateway default applies).
	LogLevel string `json:"log_level,omitempty"`

	// DebugEnabled is a shorthand for LogLevel="debug" AND TraceSampleRate=1.0 for this tenant.
	// When true it takes precedence over LogLevel.
	DebugEnabled bool `json:"debug_enabled,omitempty"`

	// TraceSampleRateOverride, when > 0, overrides the gateway-wide trace sample rate
	// for this tenant. Range 0.0–1.0. 0 means no override.
	TraceSampleRateOverride float64 `json:"trace_sample_rate_override,omitempty"`
}

// RateLimitRecord pairs a named rate limit config with its stored values.
type RateLimitRecord struct {
	Name   string          `json:"name"`
	Config RateLimitConfig `json:"config"`
}

// RegistrySnapshot is a point-in-time view of all tenant and rate-limit data.
// Used for bulk restore at startup (RestoreFromSnapshot). Not stored as a
// single blob — RegistryDatastore stores each entity individually.
type RegistrySnapshot struct {
	Tenants    []TenantRecord    `json:"tenants"`
	RateLimits []RateLimitRecord `json:"rate_limits"`
}

// ─── V2 Rate Limit Design — multi-window, multi-dimension ────────────────────

// RateLimitWindow defines a single time window for multi-window rate limiting.
// Multiple windows can coexist in one config; ALL must pass for the request to proceed.
type RateLimitWindow struct {
	Period      string `json:"period"`                 // "1s", "30s", "5m", "1h", "1d"
	PeriodSecs  uint32 `json:"period_secs,omitempty"`  // computed from Period at bake time
	Limit       uint32 `json:"limit"`                  // max requests per window; 0 = blocked (deny all)
	BurstFactor uint32 `json:"burst_factor,omitempty"` // burst percentage; 0 or 100 = no burst
}

// RateLimitHeaderNames allows overriding the standard rate limit response header names.
type RateLimitHeaderNames struct {
	Limit      string `json:"limit,omitempty"`       // default "X-RateLimit-Limit"
	Remaining  string `json:"remaining,omitempty"`   // default "X-RateLimit-Remaining"
	Reset      string `json:"reset,omitempty"`       // default "X-RateLimit-Reset"
	RetryAfter string `json:"retry_after,omitempty"` // default "Retry-After"
}

// RateLimitConfigV2 is the new multi-window rate limit configuration.
// Replaces the flat per_sec/per_min model in the legacy RateLimitConfig.
//
// REST API shapes (Management Server):
//
//	POST   /api/rate-limit-configs       → upsert RateLimitConfigV2
//	GET    /api/rate-limit-configs       → list []RateLimitConfigV2
//	DELETE /api/rate-limit-configs/{name}
//	POST   /api/tiers                    → upsert TierDef
//	GET    /api/tiers                    → list []TierDef
//	DELETE /api/tiers/{name}
//	POST   /api/upstream-services        → upsert UpstreamServiceDef
//	GET    /api/upstream-services        → list []UpstreamServiceDef
//	DELETE /api/upstream-services/{name}
type RateLimitConfigV2 struct {
	Name           string               `json:"name"`
	Enforcement    string               `json:"enforcement"`                 // "approximate" | "strict"
	RedisUnavail   string               `json:"redis_unavailable,omitempty"` // "fail_open" (default) | "fail_closed"
	Windows        []RateLimitWindow    `json:"windows"`
	ExceededStatus int                  `json:"exceeded_status,omitempty"` // default 429
	ExceededBody   string               `json:"exceeded_body,omitempty"`
	ExceededCType  string               `json:"exceeded_content_type,omitempty"`
	EmitHeaders    bool                 `json:"emit_headers,omitempty"`
	HeaderNames    RateLimitHeaderNames `json:"header_names,omitempty"`
	CaseSensitive  bool                 `json:"case_sensitive,omitempty"`
	OnEmptyKey     uint8                `json:"on_empty_key,omitempty"` // 0=fail, 1=skip, 2=fallback_tenant
	// DivideByNodes divides the per-window limit by the current live instance count
	// when enforcement is "approximate". Has no effect in strict mode (Redis is the
	// source of truth there). Requires InstanceCountFn to be wired in FlowManager.
	DivideByNodes  bool                 `json:"divide_by_nodes,omitempty"`
	// TokenBucket configures token bucket enforcement. When set, Windows are ignored
	// and the counter slot tracks [LastUpdate:32 | Tokens:32] instead of [epoch:32 | count:32].
	// enforcement field should be set to "token_bucket" to activate this path.
	TokenBucket    *TokenBucketConfig   `json:"token_bucket,omitempty"`
}

// TokenBucketConfig defines the token bucket parameters for V2 rate limiting.
type TokenBucketConfig struct {
	Rate  uint32 `json:"rate"`  // tokens added per second (refill rate)
	Burst uint32 `json:"burst"` // maximum token capacity (also initial fill)
}

// RateLimitConfigV2Record pairs a name with a V2 config for persistence.
type RateLimitConfigV2Record struct {
	Name   string           `json:"name"`
	Config RateLimitConfigV2 `json:"config"`
}

// TierDef defines a rate limit tier that can be assigned to tenants.
// A tier groups a rate limit config with API entitlement rules.
type TierDef struct {
	Name        string   `json:"name"`
	ConfigName  string   `json:"config_name,omitempty"`  // rate limit config for API/endpoint level
	OverallName string   `json:"overall_name,omitempty"` // cross-API overall quota config
	AllowedAPIs []string `json:"allowed_apis,omitempty"` // nil = all APIs allowed
	BlockedAPIs []string `json:"blocked_apis,omitempty"`
}

// UpstreamPattern maps a URL pattern to a named rate limit config.
type UpstreamPattern struct {
	Pattern    string `json:"pattern"`     // e.g. "https://api.openai.com/*"
	ConfigName string `json:"config_name"` // rate limit config to apply for this pattern
}

// UpstreamServiceDef defines a named upstream service with URL-pattern-based rate limiting.
// At request time the upstream URL is matched against Patterns (longest match wins).
type UpstreamServiceDef struct {
	Name       string            `json:"name"`
	Patterns   []UpstreamPattern `json:"patterns"`
	Unmatched  string            `json:"unmatched,omitempty"`      // "fail_open" (default) | "fail_closed" | "default_config"
	DefaultCfg string            `json:"default_config,omitempty"` // config name used when Unmatched = "default_config"
}

// ─── Registry Persistence Interface ──────────────────────────────────────────
//
// RegistryDatastore is the domain-specific persistence contract for the tenant
// registry. Methods map 1:1 to distinct entity types so each backend can choose
// its own storage layout:
//
//   - A disk backend may write one JSON file per tenant.
//   - A Redis backend may use a hash per tenant, separate hashes for URLs/IDs/Meta.
//   - A PostgreSQL backend may use one row in a tenants table with JSONB columns.
//
// The interface never exposes TenantIDs — those are ephemeral and instance-local.
// aliases[0] (the primary alias) is the stable, cross-instance tenant key.
//
// All write methods are called asynchronously after the in-memory registry is
// updated. Implementations must be safe for concurrent calls.
type RegistryDatastore interface {

	// ── Tenant: alias management ─────────────────────────────────────────────

	// PutTenantAliases stores the full alias list for a tenant.
	// primaryAlias (= aliases[0]) is the canonical storage key.
	// Called when a new tenant is created or an alias is added/removed.
	PutTenantAliases(ctx context.Context, primaryAlias string, aliases []string) error

	// ── Tenant: service URLs ─────────────────────────────────────────────────

	// PutServiceURL stores or updates a single service URL entry for a tenant.
	// key is the URL label (e.g. "primary", "fallback") — no prefix.
	PutServiceURL(ctx context.Context, primaryAlias, key, value string) error

	// ── Tenant: identifiers ──────────────────────────────────────────────────

	// PutIdentifier stores or updates a single identifier for a tenant.
	// key is the identifier label (e.g. "api_key", "client_id") — no prefix.
	PutIdentifier(ctx context.Context, primaryAlias, key, value string) error

	// ── Tenant: metadata ─────────────────────────────────────────────────────

	// PutMetadata stores or updates a single metadata value for a tenant.
	// key is the metadata label (e.g. "tier", "region") — no prefix.
	PutMetadata(ctx context.Context, primaryAlias, key, value string) error

	// ── Tenant: lifecycle ────────────────────────────────────────────────────

	// DeleteTenant removes all stored data for the tenant identified by primaryAlias.
	// Implementations should remove aliases, URLs, identifiers, and metadata.
	DeleteTenant(ctx context.Context, primaryAlias string) error

	// ── Rate limit configurations ────────────────────────────────────────────

	// PutRateLimitConfig stores or updates a named rate limit configuration.
	PutRateLimitConfig(ctx context.Context, name string, cfg RateLimitConfig) error

	// DeleteRateLimitConfig removes a named rate limit configuration.
	DeleteRateLimitConfig(ctx context.Context, name string) error

	// ── Startup restore ──────────────────────────────────────────────────────

	// LoadAll returns all persisted tenants and rate limit configs.
	// Called once at startup before any mutations occur (before SetStore).
	LoadAll(ctx context.Context) (RegistrySnapshot, error)
}
