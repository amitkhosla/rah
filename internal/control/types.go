package control

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

	// TX ID / Correlation
	GenerateIfMissing bool `json:"generate_if_missing,omitempty"` // For bind_correlation_id: generate ID when header absent

	// Batch / Extract ops
	Variable    string              `json:"variable,omitempty"`    // For cache_get_batched / json_extract_emit / json_foreach_emit: input slot name
	Params      []map[string]string `json:"params,omitempty"`      // For json_extract_emit / json_foreach_emit: list of ExtractOp descriptors
	Destination string              `json:"destination,omitempty"` // For cache_get_batched: dest slot name
}

// EndpointConfig defines per-endpoint overrides within an API definition.
type EndpointConfig struct {
	Path          string `json:"path"`
	Method        string `json:"method,omitempty"`      // empty = ANY
	RateLimitName string `json:"rate_limit,omitempty"`
}

// ApiConfig maps a URL path to a specific execution plan.
type ApiConfig struct {
	ApiID           string           `json:"api_id"`
	Path            string           `json:"path"`
	Method          string           `json:"method,omitempty"`      // HTTP method; empty = all methods
	FlowName        string           `json:"flow_name"`             // The entry fragment
	RateLimitName   string           `json:"rate_limit,omitempty"`  // API-level rate limit config name
	QuotaGroup      string           `json:"quota_group,omitempty"` // Quota group name (e.g. "premium", "global")
	EntryPoint      int16            `json:"-"`                     // Absolute ID in GlobalTable (calculated at Bake)
	EndpointConfigs []EndpointConfig `json:"endpoint_configs,omitempty"`
}

type FlowUpdate struct {
	Name         string       `json:"name"`
	Instructions []StepConfig `json:"instructions"`
	Action       string       `json:"action"` // "upsert" or "delete"
}

type ApiUpdate struct {
	Name            string           `json:"name"`
	Path            string           `json:"path"`
	Method          string           `json:"method,omitempty"`      // HTTP method; empty = all methods
	FlowName        string           `json:"flow_name"`             // Reference to a Flow name
	RateLimitName   string           `json:"rate_limit,omitempty"`  // API-level rate limit config name
	QuotaGroup      string           `json:"quota_group,omitempty"` // Quota group name
	EndpointConfigs []EndpointConfig `json:"endpoint_configs,omitempty"`
	Action          string           `json:"action"`                // "upsert" or "delete"
}

type UnifiedSyncRequest struct {
	SyncUUID string       `json:"sync_uuid"`
	Flows    []FlowUpdate `json:"flows"`
	Apis     []ApiUpdate  `json:"apis"`
}

type Step struct {
	Type          string            `json:"type"`    // e.g., "read_body", "extract_json", "proxy"
	As            string            `json:"as"`      // Slot name/alias
	Source        string            `json:"source"`  // Where to get data (e.g., "header.X-User")
	IsHugePayload bool              `json:"is_huge"` // Hint for Streaming vs Buffering
	Parameters    map[string]string `json:"params"`  // Custom logic params
}
