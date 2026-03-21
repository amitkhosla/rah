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
// TenantID is intentionally absent — IDs are ephemeral and instance-local;
// aliases[0] is the stable storage key used by RegistryDatastore.
type TenantRecord struct {
	Aliases     []string          `json:"aliases"`
	ServiceURLs map[string]string `json:"service_urls,omitempty"`
	Identifiers map[string]string `json:"identifiers,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
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
