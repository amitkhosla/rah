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

// EndpointConfig defines per-endpoint overrides within an API definition.
type EndpointConfig struct {
	Path          string            `json:"path"`
	Method        string            `json:"method,omitempty"`      // empty = ANY
	RateLimitName string            `json:"rate_limit,omitempty"`
	FlowName      string            `json:"flow_name,omitempty"`   // overrides API-level flow when set
	Constants     map[string]string `json:"constants,omitempty"`   // pre-loaded named slots for this endpoint
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
	AliasPaths      []string         `json:"alias_paths,omitempty"` // additional basepaths → same ApiID
}

type FlowUpdate struct {
	Name         string       `json:"name"`
	Instructions []StepConfig `json:"instructions"`
	Action       string       `json:"action"` // "upsert" or "delete"
}

type ApiUpdate struct {
	Name            string            `json:"name"`
	Path            string            `json:"path"`
	Method          string            `json:"method,omitempty"`      // HTTP method; empty = all methods
	FlowName        string            `json:"flow_name"`             // Reference to a Flow name
	RateLimitName   string            `json:"rate_limit,omitempty"`  // API-level rate limit config name
	QuotaGroup      string            `json:"quota_group,omitempty"` // Quota group name
	Async           string            `json:"async,omitempty"`       // "" | "allowed" | "forced"
	EndpointConfigs []EndpointConfig  `json:"endpoint_configs,omitempty"`
	AliasPaths      []string          `json:"alias_paths,omitempty"` // additional basepaths → same ApiID
	Constants       map[string]string `json:"constants,omitempty"`   // pre-loaded named slots for this API
	Action          string            `json:"action"`                // "upsert" or "delete"
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
