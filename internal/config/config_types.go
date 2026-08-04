package config

import (
	"github.com/amitkhosla/rah/internal/datasource"
	"github.com/amitkhosla/rah/internal/emailprovider"
	"github.com/amitkhosla/rah/internal/storage"
)

// CacheConfig controls the in-process response cache (Gen-2 slab cache).
//
// Backend.Kind selects the persistence/overflow layer:
//   - "" or "disk"   → disk backend; Backend.Connection.Path sets the directory (default ./icache2)
//   - "memory"       → no persistence (pure in-process; data lost on restart)
//   - "redis"        → Redis backend
//   - "dragonfly"    → Dragonfly backend (Redis-compatible)
//
// Disabled=true skips CacheManager creation entirely; cache_get/cache_put steps
// become no-ops at compile time.
type CacheConfig struct {
	Disabled      bool        `json:"disabled,omitempty"        yaml:"disabled,omitempty"`
	MemBudgetMB   int         `json:"mem_budget_mb,omitempty"   yaml:"mem_budget_mb,omitempty"`
	TenantLimitMB int         `json:"tenant_limit_mb,omitempty" yaml:"tenant_limit_mb,omitempty"`
	SizeClasses   []uint32    `json:"size_classes,omitempty"    yaml:"size_classes,omitempty"`
	TTLTiers      []uint32    `json:"ttl_tiers,omitempty"       yaml:"ttl_tiers,omitempty"`
	Backend       StoreConfig `json:"backend"         yaml:"backend"`
}

// GeoConfig configures the geo-blocking subsystem.
// DBPath is an optional local GeoLite2-Country.mmdb file to load at startup.
// Datastore is the logical data domain name (from DataStoreConfig) used to
// share and cache the mmdb binary across instances.  Leave empty to disable
// datastore sync.
// LicenseKey enables automatic MaxMind downloads; omit to use a local file only.
// UpdateInterval is a Go duration string (e.g. "168h") controlling how often
// the database is refreshed.  Defaults to 7 days when LicenseKey is set.
// OnMissing controls behaviour when the database is unavailable: "block"
// (default, fail-closed) or "allow" (fail-open).
type GeoConfig struct {
	Datastore      string `json:"datastore,omitempty"       yaml:"datastore,omitempty"`
	LicenseKey     string `json:"license_key,omitempty"     yaml:"license_key,omitempty"`
	UpdateInterval string `json:"update_interval,omitempty" yaml:"update_interval,omitempty"` // e.g. "168h"
	DBPath         string `json:"db_path,omitempty"         yaml:"db_path,omitempty"`
	OnMissing      string `json:"on_missing,omitempty"      yaml:"on_missing,omitempty"`
}

// LLMProviderAdapter identifies which wire-format adapter to use for a model.
type LLMProviderAdapter string

const (
	AdapterAnthropic LLMProviderAdapter = "anthropic"
	AdapterOpenAI    LLMProviderAdapter = "openai"
	AdapterGemini    LLMProviderAdapter = "gemini"
	AdapterOllama    LLMProviderAdapter = "ollama"
	AdapterDeepSeek  LLMProviderAdapter = "deepseek"
	// AdapterCustom uses the OpenAI-compatible wire format with auth headers
	// driven entirely by AuthHeaderName and AuthHeaderPrefix in LLMModelConfig.
	// Use this for any OpenAI-compatible provider (HuggingFace TGI, vLLM,
	// LM Studio, Groq, Together AI, etc.) without requiring a code change.
	AdapterCustom LLMProviderAdapter = "custom"
	// AdapterBedrock uses AWS SigV4 signing for AWS Bedrock (Anthropic Claude family).
	// BaseURL = AWS region (e.g. "us-east-1") or a full Bedrock endpoint URL.
	// APIKeyRef = "ACCESS_KEY_ID:SECRET_ACCESS_KEY" or "ACCESS_KEY_ID:SECRET_ACCESS_KEY:SESSION_TOKEN".
	// Alias = Bedrock model ID e.g. "anthropic.claude-3-5-sonnet-20241022-v2:0".
	AdapterBedrock LLMProviderAdapter = "bedrock"
)

// ModelCapabilities declares what a model supports and its hard limits.
// Used by check_compatibility (Phase 2) and token-budget routing.
type ModelCapabilities struct {
	MaxContextTokens      int      `json:"max_context_tokens,omitempty"       yaml:"max_context_tokens,omitempty"`
	MaxSystemPromptTokens int      `json:"max_system_prompt_tokens,omitempty" yaml:"max_system_prompt_tokens,omitempty"`
	MaxHistoryTurns       int      `json:"max_history_turns,omitempty"        yaml:"max_history_turns,omitempty"`
	SupportedFeatures     []string `json:"supported_features,omitempty"       yaml:"supported_features,omitempty"`
	SupportedToolFormats  []string `json:"supported_tool_formats,omitempty"   yaml:"supported_tool_formats,omitempty"`
}

// LLMModelConfig describes one model in the catalog.
// Alias is the stable identifier used in flow step.Input["model"] and fallback_chain.
// ModelID is the actual model identifier sent to the provider API (e.g. "gpt-4o-2024-08-06").
// If ModelID is empty, Alias is used as the model ID on the wire.
// APIKeyRef is either a literal API key or a secret reference (e.g. "gsm://…").
// CostPerInputToken and CostPerOutputToken define model pricing (cost per 1M tokens).
// If not specified, gateway will fetch from provider API at startup or use hardcoded defaults.
type LLMModelConfig struct {
	Alias string `json:"alias"                          yaml:"alias"`
	// ModelID is the provider-facing model name sent on the wire (e.g. "gpt-4o-2024-08-06").
	// Defaults to Alias when empty, allowing friendly aliases while routing to exact model versions.
	ModelID            string             `json:"model_id,omitempty"             yaml:"model_id,omitempty"`
	Provider           string             `json:"provider"                       yaml:"provider"`
	Adapter            LLMProviderAdapter `json:"adapter"                        yaml:"adapter"`
	BaseURL            string             `json:"base_url,omitempty"             yaml:"base_url,omitempty"`
	APIKeyRef          string             `json:"api_key_ref,omitempty"          yaml:"api_key_ref,omitempty"`
	MaxTokens          int                `json:"max_tokens,omitempty"           yaml:"max_tokens,omitempty"`
	Capabilities       ModelCapabilities  `json:"capabilities"                   yaml:"capabilities"`
	CostPerInputToken  float64            `json:"cost_per_input_token,omitempty" yaml:"cost_per_input_token,omitempty"`
	CostPerOutputToken float64            `json:"cost_per_output_token,omitempty" yaml:"cost_per_output_token,omitempty"`
	// AuthHeaderName and AuthHeaderPrefix are used only when Adapter == "custom".
	// AuthHeaderName is the HTTP header to set (default: "Authorization").
	// AuthHeaderPrefix is prepended to the API key value (default: "Bearer ").
	// Example: AuthHeaderName="x-api-key", AuthHeaderPrefix="" → x-api-key: <key>
	AuthHeaderName   string `json:"auth_header_name,omitempty"   yaml:"auth_header_name,omitempty"`
	AuthHeaderPrefix string `json:"auth_header_prefix,omitempty" yaml:"auth_header_prefix,omitempty"`
	// ProviderParams holds provider-specific request fields that are merged into
	// the wire-format JSON body at call time. Keys and values are provider-defined.
	// Examples:
	//   OpenAI:    {"service_tier": "flex"}
	//   Anthropic: {"thinking": {"type": "enabled", "budget_tokens": 5000}}
	//   Gemini:    {"safetySettings": [...]}
	// These are merged at the top level of the provider's request JSON, so they
	// can override or extend any field the adapter normally produces.
	ProviderParams map[string]any `json:"provider_params,omitempty" yaml:"provider_params,omitempty"`
	// UseCompletionTokens: when true, the OpenAI adapter sends max_completion_tokens
	// instead of max_tokens. Required for o-series and newer GPT-5+ models that
	// return "Unsupported parameter: 'max_tokens'" errors.
	UseCompletionTokens bool `json:"use_completion_tokens,omitempty" yaml:"use_completion_tokens,omitempty"`
	// EndpointOverride, if non-empty, bypasses the adapter's Endpoint() logic entirely
	// and POSTs to this exact URL. Use for providers that don't fit standard URL patterns
	// (e.g. a full Google Generative Language URL with a specific version path).
	EndpointOverride string `json:"endpoint_override,omitempty" yaml:"endpoint_override,omitempty"`
	// ExtraHeaders holds additional HTTP headers sent with every request to this model.
	// These are merged after the adapter's standard auth header, so they can override it.
	// Example: {"x-custom-header": "value", "x-org-id": "my-org"}
	ExtraHeaders map[string]string `json:"extra_headers,omitempty" yaml:"extra_headers,omitempty"`
	// RateLimits configures proactive provider-facing rate limiting for this model.
	// The gateway checks local atomic counters before calling the provider and
	// redirects to the fallback chain if any window is exhausted — avoiding wasted
	// network round-trips when the provider would return 429.
	// Up to 4 windows are supported; window values: "second", "minute", "hour", "day".
	// Example: [{window: "minute", limit: 60}, {window: "day", limit: 1000}]
	RateLimits []LLMRateLimitWindow `json:"rate_limits,omitempty" yaml:"rate_limits,omitempty"`
	// APIVersion selects the Google Generative Language API version for Gemini models.
	// "v1beta" — (default) required for system_instruction, tools, and thinking support.
	// "v1"     — stable production API; lacks system_instruction and function calling.
	// Ignored for non-Gemini adapters and when base_url or endpoint_override is set.
	APIVersion string `json:"api_version,omitempty" yaml:"api_version,omitempty"`
}

// LLMRateLimitWindow defines a single rate limit window for a model.
type LLMRateLimitWindow struct {
	Window string `json:"window" yaml:"window"` // "second" | "minute" | "hour" | "day"
	Limit  int    `json:"limit"  yaml:"limit"`
}

// LLMConfig is the gateway-level catalog of all usable LLM models.
// Default is the alias used when a flow step does not specify a model.
type LLMConfig struct {
	Models     []LLMModelConfig  `json:"models,omitempty"      yaml:"models,omitempty"`
	Default    string            `json:"default,omitempty"     yaml:"default,omitempty"`
	MCPServers []MCPServerConfig `json:"mcp_servers,omitempty" yaml:"mcp_servers,omitempty"`
}

// MCPTransport identifies the connection method for an MCP server.
type MCPTransport string

const (
	MCPTransportHTTP  MCPTransport = "http"
	MCPTransportSSE   MCPTransport = "sse"
	MCPTransportStdio MCPTransport = "stdio"
)

// MCPServerConfig describes one registered MCP tool server.
// Only HTTP transport is active in Phase 2; stdio/SSE come later.
type MCPServerConfig struct {
	Alias     string       `json:"alias"                 yaml:"alias"`
	Transport MCPTransport `json:"transport"             yaml:"transport"`
	URL       string       `json:"url,omitempty"         yaml:"url,omitempty"`
	Command   []string     `json:"command,omitempty"     yaml:"command,omitempty"`
	APIKeyRef string       `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty"`
	TimeoutMs int          `json:"timeout_ms,omitempty"  yaml:"timeout_ms,omitempty"`
}

// AsyncConfig controls the asynchronous job execution subsystem.
// When Enabled=false (the default), all endpoints execute synchronously
// regardless of their Async setting.
//
// Backend selects where job state is persisted:
//
//	"memory"  — in-process only; state lost on restart (default, zero infra needed)
//	any domain name — uses the datastore domain of that name (e.g. "async_jobs")
//
// MaxWorkers caps the number of concurrent in-process job goroutines.
// JobTTLSecs controls how long completed/failed jobs are retained.
type AsyncConfig struct {
	Enabled    bool   `json:"enabled,omitempty"      yaml:"enabled,omitempty"`
	Backend    string `json:"backend,omitempty"      yaml:"backend,omitempty"`      // "memory" or datastore domain name
	MaxWorkers int    `json:"max_workers,omitempty"  yaml:"max_workers,omitempty"`  // default 64
	JobTTLSecs int64  `json:"job_ttl_secs,omitempty" yaml:"job_ttl_secs,omitempty"` // default 3600
}

// GatewayConfig is the top-level configuration read from a JSON or YAML file.
// It is the single struct passed to config.Manager and distributed to all
// components via Manager accessors.
// VectorStoreKind identifies the vector database backend.
type VectorStoreKind string

const (
	VectorStoreQdrant   VectorStoreKind = "qdrant"
	VectorStoreChroma   VectorStoreKind = "chroma"
	VectorStoreWeaviate VectorStoreKind = "weaviate"
	VectorStoreRedis    VectorStoreKind = "redis"    // Redis Stack / RediSearch with vector index
	VectorStoreMongoDB  VectorStoreKind = "mongodb"  // MongoDB Atlas Vector Search
	VectorStorePgVector VectorStoreKind = "pgvector" // PostgreSQL + pgvector via REST
	VectorStoreHTTP     VectorStoreKind = "http"     // Generic HTTP — any custom backend
)

// VectorStoreConfig describes one registered vector store backend.
// Multiple stores can be registered under different names; steps reference
// them by name (same convention as MCPServerConfig).
type VectorStoreConfig struct {
	// Name is the identifier referenced by vector_search / vector_upsert steps.
	Name string `json:"name" yaml:"name"`
	// Kind selects the backend implementation.
	Kind VectorStoreKind `json:"kind" yaml:"kind"`
	// URL is the base URL of the vector store service.
	URL string `json:"url" yaml:"url"`
	// APIKey is a literal API key (prefer APIKeyRef for production).
	APIKey string `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	// APIKeyRef resolves the key at startup: "secret:name" or literal.
	APIKeyRef string `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty"`
	// DefaultCollection is used when the step does not specify a collection slot.
	DefaultCollection string `json:"default_collection,omitempty" yaml:"default_collection,omitempty"`
	// Dimension is the vector size (required by some backends at index creation time).
	Dimension int `json:"dimension,omitempty" yaml:"dimension,omitempty"`
	// Options holds backend-specific tuning (e.g. index type, distance metric).
	// Keys: "distance" (cosine|dot|euclid), "index_type", "ef", "m", etc.
	Options map[string]string `json:"options,omitempty" yaml:"options,omitempty"`
}

// ModelPricing defines pricing for a single model (in YAML pricing section).
// Cost is per 1 million tokens. Optional — if missing, gateway will skip cost calculation.
type ModelPricing struct {
	Model              string  `json:"model"                           yaml:"model"`
	Provider           string  `json:"provider,omitempty"              yaml:"provider,omitempty"`
	CostPerInputToken  float64 `json:"cost_per_input_token,omitempty"  yaml:"cost_per_input_token,omitempty"`
	CostPerOutputToken float64 `json:"cost_per_output_token,omitempty" yaml:"cost_per_output_token,omitempty"`
}

// PricingConfig defines pricing information for LLM models.
// All fields are optional — if missing, gateway will use fallback defaults (if any) or skip cost calculation.
// Pricing can come from: (1) this config section, (2) individual LLMModelConfig.CostPerXToken fields,
// or (3) live API fetch at startup (future). The gateway gracefully handles missing pricing.
type PricingConfig struct {
	// Models is a list of explicit model pricing overrides
	Models []ModelPricing `json:"models,omitempty"              yaml:"models,omitempty"`

	// CacheTTL controls how long pricing is cached in memory before refreshing
	CacheTTL string `json:"cache_ttl,omitempty"           yaml:"cache_ttl,omitempty"` // e.g., "1h"

	// RefreshInterval controls how often to fetch live pricing from provider APIs
	RefreshInterval string `json:"refresh_interval,omitempty"    yaml:"refresh_interval,omitempty"` // e.g., "1h"

	// AllowMissingPricing: if true, gateway continues even if pricing is missing
	// (default true — gateway always continues)
	AllowMissingPricing bool `json:"allow_missing_pricing,omitempty" yaml:"allow_missing_pricing,omitempty"`

	// LogMissingModels: if true, logs a warning when a model's pricing is not found
	LogMissingModels bool `json:"log_missing_models,omitempty"  yaml:"log_missing_models,omitempty"`
}

// TenantQuotaConfig defines cost quota limits for a single tenant.
// All fields are optional — if missing, tenant is unlimited.
// Supports both legacy daily/monthly limits and flexible rolling windows.
type TenantQuotaConfig struct {
	TenantID         string                   `json:"tenant_id"                    yaml:"tenant_id"`
	DailyCostLimit   float64                  `json:"daily_cost_limit,omitempty"   yaml:"daily_cost_limit,omitempty"`
	MonthlyCostLimit float64                  `json:"monthly_cost_limit,omitempty" yaml:"monthly_cost_limit,omitempty"`
	Windows          []map[string]interface{} `json:"windows,omitempty"            yaml:"windows,omitempty"`
}

// QuotasConfig defines cost quota settings for all tenants.
// All fields are optional — if missing, all tenants are unlimited.
type QuotasConfig struct {
	// Tenants is a list of per-tenant quota configurations
	Tenants []TenantQuotaConfig `json:"tenants,omitempty" yaml:"tenants,omitempty"`

	// LogMissingQuotas: if true, logs a warning when a tenant has no quota configured
	LogMissingQuotas bool `json:"log_missing_quotas,omitempty" yaml:"log_missing_quotas,omitempty"`
}

// ObsStoreConfig selects the persistence backend for observability data.
// type "memory" is the default and requires no external infrastructure.
// type "postgres" or "redis" require the obs_access_log domain to be bound
// in the datastores section.
type ObsStoreConfig struct {
	Type         string `json:"type,omitempty"          yaml:"type,omitempty"`            // "memory" (default) | "postgres" | "redis"
	MaxAccessLog int    `json:"max_access_log,omitempty" yaml:"max_access_log,omitempty"` // memory only; default 10000
	MaxTraces    int    `json:"max_traces,omitempty"     yaml:"max_traces,omitempty"`     // memory only; default 500
}

// ObsAccessLogConfig controls per-request access log capture.
type ObsAccessLogConfig struct {
	Enabled       *bool           `json:"enabled,omitempty"          yaml:"enabled,omitempty"`
	SampleRate    float64         `json:"sample_rate,omitempty"      yaml:"sample_rate,omitempty"`    // 0.0–1.0; default 1.0
	RetentionDays int             `json:"retention_days,omitempty"   yaml:"retention_days,omitempty"` // default 7
	ExtraFields   []ObsExtraField `json:"extra_fields,omitempty"     yaml:"extra_fields,omitempty"`
	// SigningKeyRef, if non-empty, enables HMAC-SHA256 tamper-evidence on each log line.
	// Resolved at startup via the secrets resolver (hex:, env:, vault://, etc.).
	// The resolved value must be ≥16 bytes; 32 bytes is recommended.
	// Each line gains a trailing sig=<hex64> field; verify with HMAC-SHA256 over
	// the rest of the line (excluding the trailing " sig=..." field).
	SigningKeyRef string `json:"signing_key_ref,omitempty" yaml:"signing_key_ref,omitempty"`
}

// ObsExtraField adds a custom column to every access log entry.
type ObsExtraField struct {
	Name   string `json:"name"   yaml:"name"`
	Source string `json:"source" yaml:"source"` // "header" | "query"
	Key    string `json:"key"    yaml:"key"`
}

// ObsMetricsConfig controls pre-aggregated metric snapshot writes.
type ObsMetricsConfig struct {
	Enabled   bool     `json:"enabled,omitempty"    yaml:"enabled,omitempty"`
	Windows   []string `json:"windows,omitempty"    yaml:"windows,omitempty"`    // e.g. ["1m","5m","1h"]; default ["1m","5m"]
	PerAPI    bool     `json:"per_api,omitempty"    yaml:"per_api,omitempty"`    // default true
	PerTenant bool     `json:"per_tenant,omitempty" yaml:"per_tenant,omitempty"` // default true
}

// ObsTracesConfig controls sampled full-request trace capture.
type ObsTracesConfig struct {
	Enabled           bool    `json:"enabled,omitempty"             yaml:"enabled,omitempty"`
	SampleRate        float64 `json:"sample_rate,omitempty"         yaml:"sample_rate,omitempty"`        // 0.0–1.0
	AlwaysTrace5xx    bool    `json:"always_trace_5xx,omitempty"    yaml:"always_trace_5xx,omitempty"`   // default true
	InstructionTiming bool    `json:"instruction_timing,omitempty"  yaml:"instruction_timing,omitempty"` // default true
	RetentionDays     int     `json:"retention_days,omitempty"      yaml:"retention_days,omitempty"`     // default 3
}

// ObsAPIConfig is a per-API observability override.
type ObsAPIConfig struct {
	AccessLog         *bool   `json:"access_log,omitempty"          yaml:"access_log,omitempty"`
	TracesSampleRate  float64 `json:"traces_sample_rate,omitempty"  yaml:"traces_sample_rate,omitempty"`
	InstructionTiming *bool   `json:"instruction_timing,omitempty"  yaml:"instruction_timing,omitempty"`
}

// ObsTenantConfig is a per-tenant observability override.
type ObsTenantConfig struct {
	AlwaysTrace      bool    `json:"always_trace,omitempty"        yaml:"always_trace,omitempty"`
	TracesSampleRate float64 `json:"traces_sample_rate,omitempty"  yaml:"traces_sample_rate,omitempty"`
}

// ObsGCStatsConfig controls the periodic GC/heap stats logger written to stderr.
// Default disabled — absent from YAML means disabled.
type ObsGCStatsConfig struct {
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

// ObsInfoLogConfig controls the per-request info log line written to stderr.
// Default disabled — absent from YAML means disabled.
// Fields lists which request fields to include; defaults to ["api_id","status","duration_ns"] when empty.
type ObsInfoLogConfig struct {
	Enabled bool     `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Fields  []string `json:"fields,omitempty"  yaml:"fields,omitempty"`
}

// ObsExportConfig controls push to external observability systems.
type ObsExportConfig struct {
	Prometheus ObsPrometheusConfig `json:"prometheus,omitempty" yaml:"prometheus,omitempty"`
	OTEL       ObsOTELConfig       `json:"otel,omitempty"       yaml:"otel,omitempty"`
	Webhook    ObsWebhookConfig    `json:"webhook,omitempty"    yaml:"webhook,omitempty"`
}

// ObsPrometheusConfig enables the /metrics scrape endpoint.
type ObsPrometheusConfig struct {
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

// ObsOTELConfig enables OpenTelemetry export.
type ObsOTELConfig struct {
	Enabled     bool   `json:"enabled,omitempty"      yaml:"enabled,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"     yaml:"endpoint,omitempty"` // gRPC endpoint e.g. "localhost:4317"
	Insecure    bool   `json:"insecure,omitempty"     yaml:"insecure,omitempty"`
	ServiceName string `json:"service_name,omitempty" yaml:"service_name,omitempty"`
}

// ObsWebhookConfig enables push to an arbitrary HTTP endpoint.
type ObsWebhookConfig struct {
	Enabled        bool              `json:"enabled,omitempty"          yaml:"enabled,omitempty"`
	URL            string            `json:"url,omitempty"              yaml:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"          yaml:"headers,omitempty"`
	BatchSize      int               `json:"batch_size,omitempty"       yaml:"batch_size,omitempty"`       // default 100
	FlushIntervalS int               `json:"flush_interval_s,omitempty" yaml:"flush_interval_s,omitempty"` // default 10
}

// ObservabilityConfig is the top-level observability configuration block.
type ObservabilityConfig struct {
	Store     ObsStoreConfig             `json:"store,omitempty"      yaml:"store,omitempty"`
	AccessLog ObsAccessLogConfig         `json:"access_log,omitempty" yaml:"access_log,omitempty"`
	Metrics   ObsMetricsConfig           `json:"metrics,omitempty"    yaml:"metrics,omitempty"`
	Traces    ObsTracesConfig            `json:"traces,omitempty"     yaml:"traces,omitempty"`
	GCStats   ObsGCStatsConfig           `json:"gc_stats,omitempty"   yaml:"gc_stats,omitempty"`
	InfoLog   ObsInfoLogConfig           `json:"info_log,omitempty"   yaml:"info_log,omitempty"`
	APIs      map[string]ObsAPIConfig    `json:"apis,omitempty"       yaml:"apis,omitempty"`
	Tenants   map[string]ObsTenantConfig `json:"tenants,omitempty"    yaml:"tenants,omitempty"`
	Export    ObsExportConfig            `json:"export,omitempty"     yaml:"export,omitempty"`
	// UpstreamLogFields is a comma-separated list of fields to include in upstream call logs.
	// Available: method, url, status_code, duration_ms, request_size, response_size,
	// connect_time_ms, tls_time_ms, ttfb_ms, retry_count, error
	// Default (empty string): "method,url,status_code,duration_ms,error"
	UpstreamLogFields string `json:"upstream_log_fields,omitempty" yaml:"upstream_log_fields,omitempty"`
	// LogLevel sets the gateway-wide minimum log level: debug|info|warn|error.
	// Default: "info". Override per-tenant via PATCH /tenants/{alias}/log-level.
	LogLevel string `json:"log_level,omitempty" yaml:"log_level,omitempty"`
}

// AdminUserConfig is one entry in the seed user list (loaded from gateway.yaml).
// AdminUserConfig is one seed user entry in gateway.yaml.
// PasswordHash must be a bcrypt hash — generate it via POST /admin/users/hash.
type AdminUserConfig struct {
	Username     string `json:"username"      yaml:"username"`
	PasswordHash string `json:"password_hash" yaml:"password_hash"`
	// Role must match a role defined in AdminConfig.Roles, or one of the
	// built-in defaults: "admin" (full access) or "readonly" (GET only).
	Role string `json:"role" yaml:"role"`
}

// RoleConfig defines a named role and the set of requests it is allowed to make.
//
// Permission format: "METHOD:path" where METHOD is an HTTP verb or "*" for any,
// and path is an exact path, a prefix ending in "/*", or "*" for any path.
//
// Examples:
//   - "*"                  — allow everything
//   - "GET:*"              — allow all GET requests
//   - "POST:/sync"         — allow POST to exactly /sync
//   - "GET:/observability/*" — allow GET to any path under /observability/
//   - "*:/admin/*"         — allow any method under /admin/
type RoleConfig struct {
	Name        string   `json:"name"                  yaml:"name"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Permissions []string `json:"permissions"           yaml:"permissions"`
}

// AdminConfig controls authentication and role-based access for the management
// plane (:8081) and optionally the gateway plane (:8080).
//
// When Enabled is false (the default) all endpoints are open — suitable for
// development or internal-only deployments. Set Enabled: true to enforce
// HTTP Basic Auth with role-based permission checks.
type AdminConfig struct {
	// Enabled turns on Basic Auth enforcement. Default: false (open access).
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// Realm is the WWW-Authenticate realm string. Default "RAH".
	Realm string `json:"realm,omitempty" yaml:"realm,omitempty"`
	// RequireGatewayAuth, when true, also protects :8080 gateway traffic.
	// Default false — gateway flows handle their own auth via validate_token steps.
	RequireGatewayAuth bool `json:"require_gateway_auth,omitempty" yaml:"require_gateway_auth,omitempty"`
	// Roles defines custom roles. Built-in roles "admin" ("*") and "readonly"
	// ("GET:*") are always available and can be overridden here.
	Roles []RoleConfig `json:"roles,omitempty" yaml:"roles,omitempty"`
	// Users is the seed list loaded at startup and merged with datastore users.
	// Config takes precedence — change a hash here to reset a password.
	Users []AdminUserConfig `json:"users,omitempty" yaml:"users,omitempty"`
}

// TLSConfig configures an optional TLS listener for the gateway.
// When present, the gateway starts a second HTTPS listener alongside the plain HTTP one.
// CertFile and KeyFile must both be non-empty for TLS to be enabled.
// Plain HTTP is never disabled — the customer removes it by not routing to that port.
type TLSConfig struct {
	CertFile string `json:"cert_file,omitempty" yaml:"cert_file,omitempty"` // path to PEM cert
	KeyFile  string `json:"key_file,omitempty"  yaml:"key_file,omitempty"`  // path to PEM key
	Port     int    `json:"port,omitempty"      yaml:"port,omitempty"`      // default 8443
}

// ConcurrencyConfig tunes the adaptive concurrency limiter and its AIMD controller.
// All fields are optional; zero values are replaced with GOMAXPROCS-derived defaults
// by engine.FlowManager.StartController.
type ConcurrencyConfig struct {
	// Enabled activates the concurrency gate and AIMD controller.
	// Default false = feature completely off; no 429s, no background goroutine.
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// Disabled turns off the adaptive AIMD controller while keeping the gate active.
	// Only meaningful when Enabled is true. Use for fixed-limit mode.
	Disabled bool `json:"disabled,omitempty" yaml:"disabled,omitempty"`

	// TargetOverheadMs is the gateway-overhead p99 target in milliseconds.
	// Gateway overhead = total client latency − upstream wait time.
	// Default: 50 ms.
	TargetOverheadMs int64 `json:"target_overhead_ms,omitempty" yaml:"target_overhead_ms,omitempty"`

	// InitialLimit is the starting concurrency cap. The AIMD controller probes
	// upward from here under healthy conditions. Default: GOMAXPROCS × 1000.
	InitialLimit int64 `json:"initial_limit,omitempty" yaml:"initial_limit,omitempty"`

	// MinLimit is the AIMD floor — the controller will never cut below this value
	// regardless of how stressed the system is. Set it high enough that normal
	// low-traffic periods never produce 429s. Rule of thumb: at least your expected
	// peak-low-traffic concurrency. Default: GOMAXPROCS × 500.
	MinLimit int64 `json:"min_limit,omitempty" yaml:"min_limit,omitempty"`

	// MaxLimit is the ceiling the controller will never grow above.
	// Default: GOMAXPROCS × 4000.
	MaxLimit int64 `json:"max_limit,omitempty" yaml:"max_limit,omitempty"`

	// AddStep is the additive increase applied each tick when conditions are healthy.
	// Default: 50.
	AddStep int64 `json:"add_step,omitempty" yaml:"add_step,omitempty"`

	// CutFactor is the multiplicative decrease factor applied when distress is detected
	// (0 < CutFactor < 1). Default: 0.85 (cut 15 % per tick).
	CutFactor float64 `json:"cut_factor,omitempty" yaml:"cut_factor,omitempty"`

	// TickSec is the controller evaluation interval in seconds. Default: 2.
	TickSec int `json:"tick_sec,omitempty" yaml:"tick_sec,omitempty"`

	// MinSamples is the minimum request count in a tick window before the controller
	// acts. Prevents reacting to statistical noise at low traffic. Default: 50.
	MinSamples int64 `json:"min_samples,omitempty" yaml:"min_samples,omitempty"`

	// CooldownTicks is how many ticks the controller waits after a cut before
	// cutting again. Prevents oscillation. Default: 3.
	CooldownTicks int `json:"cooldown_ticks,omitempty" yaml:"cooldown_ticks,omitempty"`
}

// RegistryAuditConfig controls the registry audit consumer that polls the
// registry_audit table for changes written by other gateway instances.
type RegistryAuditConfig struct {
	Enabled         bool `json:"enabled,omitempty"           yaml:"enabled,omitempty"`
	PollIntervalSec int  `json:"poll_interval_sec,omitempty" yaml:"poll_interval_sec,omitempty"`
	BatchSize       int  `json:"batch_size,omitempty"        yaml:"batch_size,omitempty"`
}

// RegistryConfig groups all registry-subsystem configuration knobs.
type RegistryConfig struct {
	Audit RegistryAuditConfig `json:"audit,omitempty" yaml:"audit,omitempty"`
}

// WSGatewayConfig controls the WebSocket gateway subsystem.
type WSGatewayConfig struct {
	Enabled         bool `json:"enabled,omitempty"           yaml:"enabled,omitempty"`
	MaxConnections  int  `json:"max_connections,omitempty"   yaml:"max_connections,omitempty"`
	ReadBufferSize  int  `json:"read_buffer_size,omitempty"  yaml:"read_buffer_size,omitempty"`
	WriteBufferSize int  `json:"write_buffer_size,omitempty" yaml:"write_buffer_size,omitempty"`
	Compression     bool `json:"compression,omitempty"       yaml:"compression,omitempty"`
}

// WSUpstreamConfig configures one WebSocket upstream peer.
type WSUpstreamConfig struct {
	Name                 string `json:"name"                     yaml:"name"`
	URL                  string `json:"url"                      yaml:"url"`
	CredRef              string `json:"cred_ref,omitempty"       yaml:"cred_ref,omitempty"`
	MaxMessageSizeKB     int    `json:"max_message_size_kb,omitempty"     yaml:"max_message_size_kb,omitempty"`
	PingIntervalSec      int    `json:"ping_interval_sec,omitempty"       yaml:"ping_interval_sec,omitempty"`
	PongTimeoutSec       int    `json:"pong_timeout_sec,omitempty"        yaml:"pong_timeout_sec,omitempty"`
	ReconnectIntervalSec int    `json:"reconnect_interval_sec,omitempty"  yaml:"reconnect_interval_sec,omitempty"`
	InboundFlow          string `json:"inbound_flow,omitempty"   yaml:"inbound_flow,omitempty"`
	TLSInsecure          bool   `json:"tls_insecure,omitempty"   yaml:"tls_insecure,omitempty"`
}

// SchedulerConfig controls the scheduler subsystem.
type SchedulerConfig struct {
	Enabled        bool   `json:"enabled,omitempty"         yaml:"enabled,omitempty"`
	Backend        string `json:"backend,omitempty"         yaml:"backend,omitempty"`
	LookaheadSec   int    `json:"lookahead_sec,omitempty"   yaml:"lookahead_sec,omitempty"`
	MaxConcurrent  int    `json:"max_concurrent,omitempty"  yaml:"max_concurrent,omitempty"`
	LeaderElection bool   `json:"leader_election,omitempty" yaml:"leader_election,omitempty"`
	LeaderTTLSec   int    `json:"leader_ttl_sec,omitempty"   yaml:"leader_ttl_sec,omitempty"`
}

type GatewayConfig struct {
	Layout        GlobalLayout        `json:"layout"                   yaml:"layout"`
	DataStore     DataStoreConfig     `json:"datastore"                yaml:"datastore"`
	Secrets       SecretsConfig       `json:"secrets"                  yaml:"secrets"`
	Cache         CacheConfig         `json:"cache"                    yaml:"cache"`
	LLM           LLMConfig           `json:"llm"                      yaml:"llm"`
	Pricing       PricingConfig       `json:"pricing,omitempty"        yaml:"pricing,omitempty"`
	Quotas        QuotasConfig        `json:"quotas,omitempty"         yaml:"quotas,omitempty"`
	Async         AsyncConfig         `json:"async"                    yaml:"async"`
	VectorStores  []VectorStoreConfig `json:"vector_stores,omitempty"  yaml:"vector_stores,omitempty"`
	Ingest        IngestConfig        `json:"ingest,omitempty"         yaml:"ingest,omitempty"`
	Observability ObservabilityConfig `json:"observability,omitempty"  yaml:"observability,omitempty"`
	Instance      InstanceConfig      `json:"instance,omitempty"       yaml:"instance,omitempty"`
	Admin         AdminConfig         `json:"admin,omitempty"          yaml:"admin,omitempty"`
	Concurrency   ConcurrencyConfig   `json:"concurrency,omitempty"    yaml:"concurrency,omitempty"`
	Egress        *EgressConfig       `json:"egress,omitempty"         yaml:"egress,omitempty"`
	Grpc          *GrpcConfig         `json:"grpc,omitempty"           yaml:"grpc,omitempty"`
	TLS           *TLSConfig          `json:"tls,omitempty"            yaml:"tls,omitempty"`
	MQTT          MQTTConfig          `json:"mqtt,omitempty"           yaml:"mqtt,omitempty"`
	XML           *XMLConfig          `json:"xml,omitempty"            yaml:"xml,omitempty"`
	Avro          *AvroConfig         `json:"avro,omitempty"           yaml:"avro,omitempty"`
	Registry      RegistryConfig      `json:"registry,omitempty"       yaml:"registry,omitempty"`
	WebSocket      WSGatewayConfig                    `json:"websocket,omitempty"        yaml:"websocket,omitempty"`
	WSUpstreams    []WSUpstreamConfig                 `json:"ws_upstreams,omitempty"     yaml:"ws_upstreams,omitempty"`
	Scheduler      SchedulerConfig                    `json:"scheduler,omitempty"        yaml:"scheduler,omitempty"`
	DataSources      []datasource.DataSourceConfig      `json:"data_sources,omitempty"      yaml:"data_sources,omitempty"`
	EmailProviders   []emailprovider.EmailProviderConfig `json:"email_providers,omitempty"   yaml:"email_providers,omitempty"`
	StorageProviders []storage.StorageProviderConfig    `json:"storage_providers,omitempty" yaml:"storage_providers,omitempty"`
}

// ── Ingestion pipeline ───────────────────────────────────────────────────────

// IngestSinkKind identifies the backend type for an ingestion sink.
type IngestSinkKind string

const (
	IngestSinkStdout      IngestSinkKind = "stdout"
	IngestSinkFile        IngestSinkKind = "file"
	IngestSinkHTTP        IngestSinkKind = "http"
	IngestSinkRedisStream IngestSinkKind = "redis_stream"
)

// IngestSinkConfig describes one named sink in the ingestion pipeline.
// Name is the unique identifier referenced by IngestKindConfig.Sinks.
type IngestSinkConfig struct {
	Name string         `json:"name"                    yaml:"name"`
	Kind IngestSinkKind `json:"kind"                    yaml:"kind"`
	// Formatting
	Format   string `json:"format,omitempty"        yaml:"format,omitempty"`   // "ndjson" (default), "json_array", "text"
	Template string `json:"template,omitempty"      yaml:"template,omitempty"` // Go template string; required when format="text"
	// File sink
	FilePath  string `json:"file_path,omitempty"     yaml:"file_path,omitempty"`
	FileBufKB int    `json:"file_buf_kb,omitempty"   yaml:"file_buf_kb,omitempty"`
	// HTTP sink
	URL       string            `json:"url,omitempty"           yaml:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"       yaml:"headers,omitempty"`
	TimeoutMs int               `json:"timeout_ms,omitempty"    yaml:"timeout_ms,omitempty"`
	// Redis stream sink
	DSN        string `json:"dsn,omitempty"           yaml:"dsn,omitempty"`
	StreamName string `json:"stream_name,omitempty"   yaml:"stream_name,omitempty"`
	MaxLen     int64  `json:"max_len,omitempty"       yaml:"max_len,omitempty"` // XTRIM MAXLEN (0=no trim)
	// Worker pool per sink
	MinWorkers    int `json:"min_workers,omitempty"    yaml:"min_workers,omitempty"`      // default 2
	MaxWorkers    int `json:"max_workers,omitempty"    yaml:"max_workers,omitempty"`      // default 8
	SinkQueueSize int `json:"sink_queue_size,omitempty" yaml:"sink_queue_size,omitempty"` // default 16384
	// Batching
	MaxBatchItems int `json:"max_batch_items,omitempty" yaml:"max_batch_items,omitempty"` // default 256
	MaxBatchBytes int `json:"max_batch_bytes,omitempty" yaml:"max_batch_bytes,omitempty"` // default 1048576
	FlushMs       int `json:"flush_ms,omitempty"        yaml:"flush_ms,omitempty"`        // default 200
}

// IngestKindConfig routes one event kind to a set of named sinks.
// Each kind has its own ring buffer, drop policy, and drain priority.
type IngestKindConfig struct {
	Kind      string   `json:"kind"                   yaml:"kind"`                 // EventKind value e.g. "llm_request"
	RingSize  int      `json:"ring_size,omitempty"    yaml:"ring_size,omitempty"`  // default 65536
	AllowDrop bool     `json:"allow_drop,omitempty"   yaml:"allow_drop,omitempty"` // if true, drop on overflow instead of blocking
	Priority  int      `json:"priority,omitempty"     yaml:"priority,omitempty"`   // lower = drained first; default 10
	Sinks     []string `json:"sinks"                  yaml:"sinks"`                // names from IngestSinkConfig.Name
}

// IngestSourceKind identifies the backend type for an event source (consumer side).
type IngestSourceKind string

const IngestSourceRedisStream IngestSourceKind = "redis_stream"

// IngestSourceConfig describes one event source — the read side of the pipeline.
// Sources are consumed by background goroutines that call registered handlers.
type IngestSourceConfig struct {
	Kind      IngestSourceKind `json:"kind"                  yaml:"kind"`
	DSN       string           `json:"dsn,omitempty"         yaml:"dsn,omitempty"`         // Redis URL e.g. redis://localhost:6379
	Stream    string           `json:"stream_name,omitempty" yaml:"stream_name,omitempty"` // Redis stream key
	TimeoutMs int              `json:"timeout_ms,omitempty"  yaml:"timeout_ms,omitempty"`  // XREAD block timeout; default 5000
	// Events filters which kinds are forwarded to handlers. Empty = all kinds.
	Events []string `json:"events,omitempty" yaml:"events,omitempty"`
}

// IngestConfig is the gateway-level configuration for the ingestion pipeline.
// All behavior is driven by config — main.go makes one call to NewPipelineFromConfig.
type IngestConfig struct {
	Enabled                bool                 `json:"enabled"                            yaml:"enabled"`
	FanoutWorkers          int                  `json:"fanout_workers,omitempty"           yaml:"fanout_workers,omitempty"`           // goroutines draining rings; default 2
	BackpressureTimeoutSec int                  `json:"backpressure_timeout_sec,omitempty" yaml:"backpressure_timeout_sec,omitempty"` // default 30
	Kinds                  []IngestKindConfig   `json:"kinds,omitempty"                    yaml:"kinds,omitempty"`
	Sinks                  []IngestSinkConfig   `json:"sinks,omitempty"                    yaml:"sinks,omitempty"`
	Sources                []IngestSourceConfig `json:"sources,omitempty"                  yaml:"sources,omitempty"` // consumer/read side
}

type ResourceLimit struct {
	MaxHeaderSize  int   `json:"max_header_size,omitempty"  yaml:"max_header_size,omitempty"`  // e.g., 16KB
	MaxHeaderCount int   `json:"max_header_count,omitempty" yaml:"max_header_count,omitempty"` // e.g., 50
	MaxBodySize    int64 `json:"max_body_size,omitempty"    yaml:"max_body_size,omitempty"`    // e.g., 1MB (SME) vs 1GB (Enterprise)

	// Inbound server timeouts.
	// ReadHeaderTimeoutMs: how long the client has to send request headers.
	// 0 (default) = disabled — no slowloris protection; use when behind a trusted LB/CDN.
	ReadHeaderTimeoutMs int `json:"read_header_timeout_ms,omitempty" yaml:"read_header_timeout_ms,omitempty"`
	// IdleTimeoutMs: max time a keepalive connection may sit idle between requests.
	// 0 (default) = 30s built-in default.
	IdleTimeoutMs int `json:"idle_timeout_ms,omitempty" yaml:"idle_timeout_ms,omitempty"`
}

// RateLimitPreset defines a named rate limit preset used as a system-wide default.
// IDs are assigned at sync time by the control plane and stored in
// registry.RateLimitConfigs. ID 0 is reserved for the system baseline.
type RateLimitPreset struct {
	ID          uint16 `json:"id"                     yaml:"id"`
	Name        string `json:"name"                   yaml:"name"`
	RatePerSec  uint32 `json:"rate_per_sec,omitempty" yaml:"rate_per_sec,omitempty"` // max requests/second; 0 = unlimited
	RatePerMin  uint32 `json:"rate_per_min,omitempty" yaml:"rate_per_min,omitempty"` // max requests/minute; 0 = unlimited
	BurstFactor uint16 `json:"burst_factor,omitempty" yaml:"burst_factor,omitempty"` // 100 = 1x, 150 = 1.5x, 200 = 2x
}

// ── Instance / deployment configuration ──────────────────────────────────────

// InstanceConfig identifies this gateway instance and controls config polling.
// Set in gateway.yaml under the "instance:" key.
type InstanceConfig struct {
	// EnvironmentID is the logical environment this instance belongs to (e.g. "prod", "dev").
	// Instances with the same EnvironmentID share a config_versions stream.
	EnvironmentID string `json:"environment_id,omitempty" yaml:"environment_id,omitempty"`

	// PollIntervalS is how often (in seconds) this instance polls the DB for a newer
	// config_version. Default 10. Set to 0 to disable polling (push-only mode).
	PollIntervalS int `json:"poll_interval_s,omitempty" yaml:"poll_interval_s,omitempty"`

	// HeartbeatIntervalS is how often (in seconds) this instance writes its heartbeat.
	// Default 10. Instances silent for 3× this interval are considered dead.
	HeartbeatIntervalS int `json:"heartbeat_interval_s,omitempty" yaml:"heartbeat_interval_s,omitempty"`

	// AcceptDraftExecution allows this instance to serve POST /test/execute requests
	// against draft instruction sets. Safe to enable on all instances; draft flows
	// never enter the live router.
	AcceptDraftExecution bool `json:"accept_draft_execution,omitempty" yaml:"accept_draft_execution,omitempty"`
}

type GlobalLayout struct {
	MaxBytesSlots         int               `json:"max_bytes_slots,omitempty"      yaml:"max_bytes_slots,omitempty"`
	MaxIntsSlots          int               `json:"max_ints_slots,omitempty"       yaml:"max_ints_slots,omitempty"`
	MaxBoolsSlots         int               `json:"max_bools_slots,omitempty"      yaml:"max_bools_slots,omitempty"`
	MaxHeapBytes          int64             `json:"max_heap_bytes,omitempty"       yaml:"max_heap_bytes,omitempty"`
	DefaultLimits         ResourceLimit     `json:"default_limits"       yaml:"default_limits"`
	SlotValueThreshold    int               `json:"slot_value_threshold,omitempty"    yaml:"slot_value_threshold,omitempty"`      // per-value arena limit; 0 = rctx default (256)
	DefaultRateLimits     []RateLimitPreset `json:"default_rate_limits,omitempty"     yaml:"default_rate_limits,omitempty"`       // system-wide defaults loaded at startup
	TransportShardsPerCPU int               `json:"transport_shards_per_cpu,omitempty" yaml:"transport_shards_per_cpu,omitempty"` // http transport pool shards per CPU; 0 = default (4)
}
