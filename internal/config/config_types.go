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
// Slug is the stable identifier used in flow step.Input["model"].
// APIKeyRef is either a literal API key or a secret reference (e.g. "gsm://…").
type LLMModelConfig struct {
	Slug         string             `json:"slug"                  yaml:"slug"`
	Provider     string             `json:"provider"              yaml:"provider"`
	Adapter      LLMProviderAdapter `json:"adapter"               yaml:"adapter"`
	BaseURL      string             `json:"base_url,omitempty"    yaml:"base_url,omitempty"`
	APIKeyRef    string             `json:"api_key_ref,omitempty" yaml:"api_key_ref,omitempty"`
	MaxTokens    int                `json:"max_tokens,omitempty"  yaml:"max_tokens,omitempty"`
	Capabilities ModelCapabilities  `json:"capabilities" yaml:"capabilities"`
}

// LLMConfig is the gateway-level catalog of all usable LLM models.
// Default is the slug used when a flow step does not specify a model.
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

type GatewayConfig struct {
	Layout       GlobalLayout        `json:"layout"             yaml:"layout"`
	DataStore    DataStoreConfig     `json:"datastore"          yaml:"datastore"`
	Secrets      SecretsConfig       `json:"secrets"            yaml:"secrets"`
	Cache        CacheConfig         `json:"cache"              yaml:"cache"`
	LLM          LLMConfig           `json:"llm"                yaml:"llm"`
	Async        AsyncConfig         `json:"async"              yaml:"async"`
	VectorStores []VectorStoreConfig `json:"vector_stores,omitempty" yaml:"vector_stores,omitempty"`
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
