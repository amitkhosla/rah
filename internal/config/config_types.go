package config

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

// LLMProviderAdapter identifies which wire-format adapter to use for a model.
type LLMProviderAdapter string

const (
	AdapterAnthropic LLMProviderAdapter = "anthropic"
	AdapterOpenAI    LLMProviderAdapter = "openai"
	AdapterGemini    LLMProviderAdapter = "gemini"
	AdapterOllama    LLMProviderAdapter = "ollama"
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
// APIKeyRef is either a literal API key or a secret reference (e.g. "gsm://…").
// CostPerInputToken and CostPerOutputToken define model pricing (cost per 1M tokens).
// If not specified, gateway will fetch from provider API at startup or use hardcoded defaults.
type LLMModelConfig struct {
	Alias                string             `json:"alias"                          yaml:"alias"`
	Provider             string             `json:"provider"                       yaml:"provider"`
	Adapter              LLMProviderAdapter `json:"adapter"                        yaml:"adapter"`
	BaseURL              string             `json:"base_url,omitempty"             yaml:"base_url,omitempty"`
	APIKeyRef            string             `json:"api_key_ref,omitempty"          yaml:"api_key_ref,omitempty"`
	MaxTokens            int                `json:"max_tokens,omitempty"           yaml:"max_tokens,omitempty"`
	Capabilities         ModelCapabilities  `json:"capabilities"                   yaml:"capabilities"`
	CostPerInputToken    float64            `json:"cost_per_input_token,omitempty" yaml:"cost_per_input_token,omitempty"`
	CostPerOutputToken   float64            `json:"cost_per_output_token,omitempty" yaml:"cost_per_output_token,omitempty"`
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
	Backend    string `json:"backend,omitempty"      yaml:"backend,omitempty"`  // "memory" or datastore domain name
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
	Model               string  `json:"model"                           yaml:"model"`
	Provider            string  `json:"provider,omitempty"              yaml:"provider,omitempty"`
	CostPerInputToken   float64 `json:"cost_per_input_token,omitempty"  yaml:"cost_per_input_token,omitempty"`
	CostPerOutputToken  float64 `json:"cost_per_output_token,omitempty" yaml:"cost_per_output_token,omitempty"`
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
	TenantID         string                 `json:"tenant_id"                    yaml:"tenant_id"`
	DailyCostLimit   float64                `json:"daily_cost_limit,omitempty"   yaml:"daily_cost_limit,omitempty"`
	MonthlyCostLimit float64                `json:"monthly_cost_limit,omitempty" yaml:"monthly_cost_limit,omitempty"`
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

type GatewayConfig struct {
	Layout       GlobalLayout        `json:"layout"             yaml:"layout"`
	DataStore    DataStoreConfig     `json:"datastore"          yaml:"datastore"`
	Secrets      SecretsConfig       `json:"secrets"            yaml:"secrets"`
	Cache        CacheConfig         `json:"cache"              yaml:"cache"`
	LLM          LLMConfig           `json:"llm"                yaml:"llm"`
	Pricing      PricingConfig       `json:"pricing,omitempty"  yaml:"pricing,omitempty"`
	Quotas       QuotasConfig        `json:"quotas,omitempty"   yaml:"quotas,omitempty"`
	Async        AsyncConfig         `json:"async"              yaml:"async"`
	VectorStores []VectorStoreConfig `json:"vector_stores,omitempty" yaml:"vector_stores,omitempty"`
	Ingest       IngestConfig        `json:"ingest,omitempty"   yaml:"ingest,omitempty"`
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
	Name      string            `json:"name"                    yaml:"name"`
	Kind      IngestSinkKind    `json:"kind"                    yaml:"kind"`
	// Formatting
	Format    string            `json:"format,omitempty"        yaml:"format,omitempty"`   // "ndjson" (default), "json_array", "text"
	Template  string            `json:"template,omitempty"      yaml:"template,omitempty"` // Go template string; required when format="text"
	// File sink
	FilePath  string            `json:"file_path,omitempty"     yaml:"file_path,omitempty"`
	FileBufKB int               `json:"file_buf_kb,omitempty"   yaml:"file_buf_kb,omitempty"`
	// HTTP sink
	URL       string            `json:"url,omitempty"           yaml:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"       yaml:"headers,omitempty"`
	TimeoutMs int               `json:"timeout_ms,omitempty"    yaml:"timeout_ms,omitempty"`
	// Redis stream sink
	DSN        string `json:"dsn,omitempty"           yaml:"dsn,omitempty"`
	StreamName string `json:"stream_name,omitempty"   yaml:"stream_name,omitempty"`
	MaxLen     int64  `json:"max_len,omitempty"       yaml:"max_len,omitempty"` // XTRIM MAXLEN (0=no trim)
	// Worker pool per sink
	MinWorkers    int `json:"min_workers,omitempty"    yaml:"min_workers,omitempty"`    // default 2
	MaxWorkers    int `json:"max_workers,omitempty"    yaml:"max_workers,omitempty"`    // default 8
	SinkQueueSize int `json:"sink_queue_size,omitempty" yaml:"sink_queue_size,omitempty"` // default 16384
	// Batching
	MaxBatchItems int `json:"max_batch_items,omitempty" yaml:"max_batch_items,omitempty"` // default 256
	MaxBatchBytes int `json:"max_batch_bytes,omitempty" yaml:"max_batch_bytes,omitempty"` // default 1048576
	FlushMs       int `json:"flush_ms,omitempty"        yaml:"flush_ms,omitempty"`        // default 200
}

// IngestKindConfig routes one event kind to a set of named sinks.
// Each kind has its own ring buffer, drop policy, and drain priority.
type IngestKindConfig struct {
	Kind      string   `json:"kind"                   yaml:"kind"`       // EventKind value e.g. "llm_request"
	RingSize  int      `json:"ring_size,omitempty"    yaml:"ring_size,omitempty"`  // default 65536
	AllowDrop bool     `json:"allow_drop,omitempty"   yaml:"allow_drop,omitempty"` // if true, drop on overflow instead of blocking
	Priority  int      `json:"priority,omitempty"     yaml:"priority,omitempty"`   // lower = drained first; default 10
	Sinks     []string `json:"sinks"                  yaml:"sinks"`               // names from IngestSinkConfig.Name
}

// IngestConfig is the gateway-level configuration for the ingestion pipeline.
// All behavior is driven by config — main.go makes one call to NewPipelineFromConfig.
type IngestConfig struct {
	Enabled                bool               `json:"enabled"                            yaml:"enabled"`
	FanoutWorkers          int                `json:"fanout_workers,omitempty"           yaml:"fanout_workers,omitempty"`           // goroutines draining rings; default 2
	BackpressureTimeoutSec int                `json:"backpressure_timeout_sec,omitempty" yaml:"backpressure_timeout_sec,omitempty"` // default 30
	Kinds                  []IngestKindConfig `json:"kinds,omitempty"                    yaml:"kinds,omitempty"`
	Sinks                  []IngestSinkConfig `json:"sinks,omitempty"                    yaml:"sinks,omitempty"`
}

type ResourceLimit struct {
	MaxHeaderSize  int   `json:"max_header_size,omitempty"  yaml:"max_header_size,omitempty"`  // e.g., 16KB
	MaxHeaderCount int   `json:"max_header_count,omitempty" yaml:"max_header_count,omitempty"` // e.g., 50
	MaxBodySize    int64 `json:"max_body_size,omitempty"    yaml:"max_body_size,omitempty"`    // e.g., 1MB (SME) vs 1GB (Enterprise)
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

type GlobalLayout struct {
	MaxBytesSlots      int               `json:"max_bytes_slots,omitempty"      yaml:"max_bytes_slots,omitempty"`
	MaxIntsSlots       int               `json:"max_ints_slots,omitempty"       yaml:"max_ints_slots,omitempty"`
	MaxBoolsSlots      int               `json:"max_bools_slots,omitempty"      yaml:"max_bools_slots,omitempty"`
	MaxHeapBytes       int64             `json:"max_heap_bytes,omitempty"       yaml:"max_heap_bytes,omitempty"`
	DefaultLimits      ResourceLimit     `json:"default_limits"       yaml:"default_limits"`
	SlotValueThreshold int               `json:"slot_value_threshold,omitempty" yaml:"slot_value_threshold,omitempty"` // per-value arena limit; 0 = rctx default (256)
	DefaultRateLimits  []RateLimitPreset `json:"default_rate_limits,omitempty"  yaml:"default_rate_limits,omitempty"`  // system-wide defaults loaded at startup
}
