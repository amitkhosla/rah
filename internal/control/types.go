package control

import registrypkg "rah/internal/registry"

// StepConfig defines a single atomic instruction in a flow.
type StepConfig struct {
	Action string `json:"action"` // if, http_call, registry_lookup, foreach, call, etc.

	// Variables & Extraction
	Key string `json:"key,omitempty"` // Header/Query key name
	As  string `json:"as,omitempty"`  // The semantic name (Compiler maps this to a Slot ID)

	FlowName string `json:"flow_name,omitempty"`

	// HTTP / Proxy Logic
	URL            string            `json:"url,omitempty"`             // Static URL
	UrlVar         string            `json:"url_var,omitempty"`         // Slot name for dynamic/fallback URLs
	Method         string            `json:"method,omitempty"`          // GET, POST, etc.
	Timeout        uint32            `json:"timeout,omitempty"`         // In milliseconds
	RetryCondition string            `json:"retry_condition,omitempty"` // e.g., "res.status >= 500"
	MaxRetries     int               `json:"max_retries,omitempty"`
	KeyIdentifier  string            `json:"key_identifier,omitempty"`
	Input          map[string]string `json:"input,omitempty"`

	// Logic & Branching
	Condition string            `json:"condition,omitempty"` // String logic like "(header.Auth == 'y') && status"
	Then      string            `json:"then,omitempty"`      // Subflow name to jump to if true
	Else      string            `json:"else,omitempty"`      // Subflow name to jump to if false
	Cases     map[string]string `json:"cases,omitempty"`     // For "switch" actions
	Do        []StepConfig      `json:"do,omitempty"`        //For foreach/retry
	Source    string            `json:"source,omitempty"`    //For looping

	// Registry & Storage
	Scope  string `json:"scope,omitempty"` // "tenant", "global", or "cache"
	Value  string `json:"value,omitempty"` // Static value to set
	Path   string `json:"path,omitempty"`  // JSONPath or URL Path segment index
	TTL    uint32 `json:"ttl,omitempty"`
	OnMiss string `json:"on_miss,omitempty"` // Flow to call if Registry/Cache misses

	// Delta is the increment value for cache_incr. Default 1 if zero.
	Delta int64 `json:"delta,omitempty"`

	// TX ID / Correlation
	GenerateIfMissing bool `json:"generate_if_missing,omitempty"` // For bind_correlation_id: generate ID when header absent

	// HTTP Utilities
	IncludeQuery *bool `json:"include_query,omitempty"` // For bind_request_url: nil=true (include query), false=path only

	// Batch / Extract ops
	Variable    string              `json:"variable,omitempty"`    // For cache_get_batched / json_extract_emit / json_foreach_emit: input slot name
	Params      []map[string]string `json:"params,omitempty"`      // For json_extract_emit / json_foreach_emit: list of ExtractOp descriptors
	Destination string              `json:"destination,omitempty"` // For cache_get_batched: dest slot name

	// Per-step observability hooks — applied after the step's own instruction(s).
	// LogAs, if non-empty, emits a log_field instruction that writes the step's
	// output variable (As) into the request access log under this field name.
	// TraceCapture, if true, emits a trace_capture instruction that copies the step's
	// output variable into the instruction trace output so it appears in trace detail.
	// TraceVars, if non-empty, emits a trace_capture instruction for each named variable
	// (looked up in slotMap). Complements TraceCapture; duplicates are skipped.
	LogAs        string   `json:"log_as,omitempty"`
	TraceCapture bool     `json:"trace_capture,omitempty"`
	TraceVars    []string `json:"trace_vars,omitempty"`

	// Error handling
	// OnError controls what happens when this step signals failure (sets ctx.Failed + StopPlan).
	// Values:
	//   "" or "fail"     — default: stop execution (no wrapper emitted)
	//   "continue"       — clear error and continue to next step
	//   "jump:<flow>"    — jump to named flow's entry point on error
	//   "status:<code>"  — set HTTP status <code> and stop cleanly (e.g. "status:503")
	OnError string `json:"on_error,omitempty"`

	// Status is the HTTP response code for "return" and "fail" actions.
	// Also used as the static status for on_error:"status:<code>" when the code
	// is not parseable from OnError (fallback).
	Status int `json:"status,omitempty"`

	// Body is used by the "return" action as a static response body string.
	// If empty and a BodySlot is named via As, that slot's value is used.
	Body string `json:"body,omitempty"`
}

// ─── Rate Limit Warning Types ─────────────────────────────────────────────────

// RLWarnCode classifies a rate limit validation warning produced during bake.
type RLWarnCode string

const (
	// RLWarnNoFlow: the API has no flow assigned (or the flow does not exist).
	RLWarnNoFlow RLWarnCode = "no_flow"
	// RLWarnNotEnforced: RL policies are defined but the flow tree has no RL step.
	// The compiler will auto-inject enforcement at flow start.
	RLWarnNotEnforced RLWarnCode = "not_enforced"
	// RLWarnSlotUnfilled: an entry uses a slot as its count key or dynamic source
	// but no step in the flow fills that slot before the rate limit check.
	RLWarnSlotUnfilled RLWarnCode = "slot_unfilled"
	// RLWarnConfigMissing: a named (or dynamic) entry references a RateLimitConfigV2
	// that does not exist at bake time.
	RLWarnConfigMissing RLWarnCode = "config_missing"
)

// RateLimitWarning is a single advisory produced by the compiler validation pass.
// Warnings are non-blocking — bake succeeds regardless. The sync response
// includes all warnings so the Studio can surface them per-row in the API screen.
type RateLimitWarning struct {
	Code    RLWarnCode `json:"code"`
	Message string     `json:"message"`
	API     string     `json:"api,omitempty"`  // which API triggered the warning
	Row     int        `json:"row,omitempty"`  // which APIRateLimitEntry (0-indexed)
	Slot    string     `json:"slot,omitempty"` // relevant slot name for slot_unfilled
}

// ─── Rate Limit Policy Types ──────────────────────────────────────────────────

// RateLimitEntryKind distinguishes how a rate limit entry's config is specified.
type RateLimitEntryKind string

const (
	RLEntryNamed   RateLimitEntryKind = "named"   // references existing RateLimitConfigV2 by name
	RLEntryFixed   RateLimitEntryKind = "fixed"   // inline windows defined directly on this entry
	RLEntryDynamic RateLimitEntryKind = "dynamic" // config name resolved from a runtime value
)

// FixedWindow defines one inline rate limit window for RLEntryFixed entries.
type FixedWindow struct {
	EpochSec uint32 `json:"epoch_sec"` // seconds per window: 1=per-second, 60=per-minute, 3600=per-hour, 86400=per-day
	Limit    uint32 `json:"limit"`     // max requests allowed per window
}

// DynamicRLMapping maps a runtime string value to a rate limit config name.
// Used for tier-based or plan-based rate limit dispatch.
type DynamicRLMapping struct {
	Source   string            `json:"source"`   // where to read the value: "meta.<key>", "header.<name>", "slot.<name>"
	Mappings map[string]string `json:"mappings"` // runtime value → config name, e.g. {"free":"free_rl","pro":"pro_rl"}
}

// APIRateLimitEntry is one row in the API definition's rate limit policy table.
// Multiple entries are evaluated in order at request time.
type APIRateLimitEntry struct {
	Kind       RateLimitEntryKind `json:"kind"`                  // named | fixed | dynamic
	Config     string             `json:"config,omitempty"`      // named: RateLimitConfigV2 name to enforce
	CountBy    string             `json:"count_by"`              // "tenant"|"ip"|"global"|"slot"|"static"|"composite"
	SlotSource string             `json:"slot_source,omitempty"` // count_by=slot: slot name holding the key
	StaticKey  string             `json:"static_key,omitempty"`  // count_by=static: literal key string
	Windows    []FixedWindow      `json:"windows,omitempty"`     // fixed kind: inline window definitions
	Dynamic    *DynamicRLMapping  `json:"dynamic,omitempty"`     // dynamic kind: runtime dispatch mapping
}

// UpstreamUrlConfig describes how the gateway resolves the upstream URL for a
// route. It is set once at deploy time and injected into the flow's
// "upstream_url" slot before execution — no flow step required.
//
//   source   meaning of value
//   ------   ----------------
//   static        literal URL string (e.g. "https://api.example.com")
//   registry      registry URL key name (e.g. "primary"); resolved per-tenant
//   cache         cache key name; looked up at request time
//   header        HTTP request header name (e.g. "X-Upstream-URL")
//   queryparam    query parameter name (e.g. "upstream")
type UpstreamUrlConfig struct {
	Source string `json:"source"` // "static" | "registry" | "cache" | "header" | "queryparam"
	Value  string `json:"value"`  // URL for static; key/header/param name for others
}

// EndpointConfig defines per-endpoint overrides within an API definition.
type EndpointConfig struct {
	Path          string             `json:"path"`
	Method        string             `json:"method,omitempty"`          // empty = ANY
	RateLimitName string             `json:"rate_limit,omitempty"`
	RateLimitMode string             `json:"rate_limit_mode,omitempty"` // "global" | "tenant" | "ip" | "slot" | "" (inherit/default)
	FlowName      string             `json:"flow_name,omitempty"`       // overrides API-level flow when set
	Constants     map[string]string  `json:"constants,omitempty"`       // pre-loaded named slots for this endpoint
	UpstreamUrl   *UpstreamUrlConfig `json:"upstream_url,omitempty"`    // gateway-native upstream URL config

	// V2 rate limit fields — multi-window, multi-dimension design.
	// These are additive; legacy RateLimitName/RateLimitMode fields remain for
	// backwards compatibility until full migration (Session S17).
	RLCountBy      string   `json:"rl_count_by,omitempty"`      // "tenant"|"ip"|"slot"|"static"|"composite"|"global"
	RLSlot         string   `json:"rl_slot,omitempty"`          // slot name for count_by=slot
	RLSlots        []string `json:"rl_slots,omitempty"`         // slot names for count_by=composite
	RLStaticKey    string   `json:"rl_static_key,omitempty"`    // static key for count_by=static
	RLXFFIndex     int      `json:"rl_xff_index,omitempty"`     // XFF index for count_by=ip (0 = leftmost)
	RLOnEmpty      string   `json:"rl_on_empty,omitempty"`      // "fail"|"skip"|"fallback_tenant"
	RLFailFast     bool     `json:"rl_fail_fast,omitempty"`     // stop on first window failure
	RLConfig       string   `json:"rl_config,omitempty"`        // named RateLimitConfigV2 (static ref)
	RLDynSource    string   `json:"rl_dyn_source,omitempty"`    // "registry"|"cache"|"header"|"queryparam"
	RLDynKey       string   `json:"rl_dyn_key,omitempty"`       // key name for dynamic config resolution
	UpstreamSvc    string   `json:"upstream_service,omitempty"` // upstream service name for URL-pattern RL

	// Multi-entry rate limit policies. Replaces scattered RL* fields for new configurations.
	// Existing RateLimitName/RLConfig/etc. fields are kept for backwards compatibility.
	RateLimitPolicies []APIRateLimitEntry `json:"rate_limit_policies,omitempty"`
	SkipRateLimit     bool                `json:"skip_rate_limit,omitempty"`
}

// ApiConfig maps a URL path to a specific execution plan.
type ApiConfig struct {
	ApiID           string           `json:"api_id"`
	Path            string           `json:"path"`
	Method          string           `json:"method,omitempty"`      // HTTP method; empty = all methods
	FlowName        string           `json:"flow_name"`             // The entry fragment
	RateLimitName   string           `json:"rate_limit,omitempty"`  // API-level rate limit config name
	QuotaGroup      string           `json:"quota_group,omitempty"` // Quota group name (e.g. "premium", "global")
	Async           string           `json:"async,omitempty"`       // "" | "allowed" | "forced"
	EntryPoint      int16            `json:"-"`                     // Absolute ID in GlobalTable (calculated at Bake)
	EndpointConfigs []EndpointConfig `json:"endpoint_configs,omitempty"`
	AliasPaths        []string            `json:"alias_paths,omitempty"`         // additional basepaths → same ApiID
	RateLimitPolicies []APIRateLimitEntry `json:"rate_limit_policies,omitempty"` // multi-entry RL policies (new model)
	SkipRateLimit     bool                `json:"skip_rate_limit,omitempty"`     // suppress auto-injection and warnings
}

type FlowUpdate struct {
	Name         string       `json:"name"`
	Instructions []StepConfig `json:"instructions"`
	Action       string       `json:"action"` // "upsert" or "delete"
}

type ApiUpdate struct {
	Name            string             `json:"name"`
	Path            string             `json:"path"`
	Method          string             `json:"method,omitempty"`          // HTTP method; empty = all methods
	FlowName        string             `json:"flow_name"`                 // Reference to a Flow name
	RateLimitName   string             `json:"rate_limit,omitempty"`      // API-level rate limit config name
	RateLimitMode   string             `json:"rate_limit_mode,omitempty"` // "global" | "tenant" | "ip" | "slot" | "" (inherit/default)
	QuotaGroup      string             `json:"quota_group,omitempty"`     // Quota group name
	Async           string             `json:"async,omitempty"`           // "" | "allowed" | "forced"
	EndpointConfigs []EndpointConfig   `json:"endpoint_configs,omitempty"`
	AliasPaths      []string           `json:"alias_paths,omitempty"`     // additional basepaths → same ApiID
	Constants       map[string]string  `json:"constants,omitempty"`       // pre-loaded named slots for this API
	UpstreamUrl     *UpstreamUrlConfig `json:"upstream_url,omitempty"`    // gateway-native upstream URL config
	Action          string             `json:"action"`                    // "upsert" or "delete"

	// V2 rate limit fields — additive alongside legacy fields.
	RLCountBy      string   `json:"rl_count_by,omitempty"`      // "tenant"|"ip"|"slot"|"static"|"composite"|"global"
	RLSlot         string   `json:"rl_slot,omitempty"`          // slot name for count_by=slot
	RLSlots        []string `json:"rl_slots,omitempty"`         // slot names for count_by=composite
	RLStaticKey    string   `json:"rl_static_key,omitempty"`    // static key for count_by=static
	RLXFFIndex     int      `json:"rl_xff_index,omitempty"`     // XFF index for count_by=ip (0 = leftmost)
	RLOnEmpty      string   `json:"rl_on_empty,omitempty"`      // "fail"|"skip"|"fallback_tenant"
	RLFailFast     bool     `json:"rl_fail_fast,omitempty"`     // stop on first window failure
	RLConfig       string   `json:"rl_config,omitempty"`        // named RateLimitConfigV2 (static ref)
	RLDynSource    string   `json:"rl_dyn_source,omitempty"`    // "registry"|"cache"|"header"|"queryparam"
	RLDynKey       string   `json:"rl_dyn_key,omitempty"`       // key name for dynamic config resolution
	UpstreamSvc    string   `json:"upstream_service,omitempty"` // upstream service name for URL-pattern RL

	// Multi-entry rate limit policies. Replaces scattered RL* fields for new configurations.
	// Existing RateLimitName/RLConfig/etc. fields are kept for backwards compatibility.
	RateLimitPolicies []APIRateLimitEntry `json:"rate_limit_policies,omitempty"`
	SkipRateLimit     bool                `json:"skip_rate_limit,omitempty"`
}

type UnifiedSyncRequest struct {
	SyncUUID string       `json:"sync_uuid"`
	Flows    []FlowUpdate `json:"flows"`
	Apis     []ApiUpdate  `json:"apis"`

	// V2 rate limit resources — persisted and restored alongside flows/apis.
	RateLimitConfigsV2 []registrypkg.RateLimitConfigV2    `json:"rate_limit_configs_v2,omitempty"`
	Tiers              []registrypkg.TierDef              `json:"tiers,omitempty"`
	UpstreamServices   []registrypkg.UpstreamServiceDef   `json:"upstream_services,omitempty"`
}

type Step struct {
	Type          string            `json:"type"`    // e.g., "read_body", "extract_json", "proxy"
	As            string            `json:"as"`      // Slot name/alias
	Source        string            `json:"source"`  // Where to get data (e.g., "header.X-User")
	IsHugePayload bool              `json:"is_huge"` // Hint for Streaming vs Buffering
	Parameters    map[string]string `json:"params"`  // Custom logic params
}
