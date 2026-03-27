package control

// ─── Step Descriptor Types ────────────────────────────────────────────────────
//
// StepDescriptor is the single source of truth for every action the compiler
// knows how to handle. It serves two purposes simultaneously:
//
//  1. Documentation — developers see the contract for each step.
//  2. Studio palette — GET /meta/steps returns these; the UI renders them
//     without any hardcoded knowledge of what steps exist.
//
// When adding a new action to compiler.go:
//  1. Add the case in compileStep().
//  2. Add a StepDescriptor here in AllStepDescriptors().
//
// That is the only required change — the Studio picks it up automatically on
// the next gateway start.

// StepField describes one editable input on a step card in the Studio.
type StepField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Placeholder string `json:"placeholder"`
}

// StepDescriptor fully describes one compiler action for the Studio palette.
// JSON shape is intentionally identical to studio.PaletteBlock so the Studio
// can consume it without any mapping.
type StepDescriptor struct {
	Type           string            `json:"type"`
	Title          string            `json:"title"`
	Description    string            `json:"description"`
	Category       string            `json:"category"`
	Capability     string            `json:"capability"`
	SupportsNested bool              `json:"supports_nested,omitempty"`
	NextHints      []string          `json:"next_hints,omitempty"`
	Defaults       map[string]string `json:"defaults"`
	Fields         []StepField       `json:"fields,omitempty"`
}

// StepCatalog is the top-level response served by GET /meta/steps.
type StepCatalog struct {
	Version    string           `json:"version"`
	Categories []string         `json:"categories"`
	Steps      []StepDescriptor `json:"blocks"` // "blocks" matches Studio's SchemaResponse.blocks
}

// sf is a concise constructor for StepField.
func sf(key, label, desc, placeholder string) StepField {
	return StepField{Key: key, Label: label, Description: desc, Placeholder: placeholder}
}

// AllStepDescriptors returns every action the compiler supports, in palette order.
// This is the authoritative list — compiler.go compileStep() mirrors it 1:1.
func AllStepDescriptors() []StepDescriptor {
	return []StepDescriptor{

		// ── Registry / Identity ───────────────────────────────────────────────────
		{
			Type: "registry_lookup", Title: "Registry Lookup", Category: "registry", Capability: "identity",
			Description: "Resolve an alias (from a slot) to a TenantID. Must run before any load_service_url or load_identifier steps.",
			Defaults: map[string]string{"key_identifier": "header.X-Tenant-ID"},
			Fields: []StepField{
				sf("key_identifier", "Alias slot", "Slot whose value is used as the tenant alias for lookup (e.g. a header, query param, or path segment)", "header.X-Tenant-ID"),
			},
		},
		{
			Type: "load_service_url", Title: "Load Service URL", Category: "registry", Capability: "service-url",
			Description: "Load a named upstream URL for the current tenant (stored as url:<name>) into a slot. Requires registry_lookup to have run first.",
			Defaults: map[string]string{"key": "primary", "as": "upstream_url"},
			Fields: []StepField{
				sf("key", "URL name", "Name of the service URL as registered in the tenant (e.g. primary, fallback, health)", "primary"),
				sf("as", "Store as", "Slot name to save the URL into; use this slot in a following http_call as url_var", "upstream_url"),
			},
		},
		{
			Type: "set_service_url", Title: "Set Service URL", Category: "registry", Capability: "write",
			Description: "Write a service URL for the current tenant into the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "primary", "source": "var.new_url"},
			Fields: []StepField{
				sf("key", "URL name", "Name of the service URL to write (e.g. primary, fallback, health)", "primary"),
				sf("source", "Value slot", "Slot whose value is written to the registry", "var.new_url"),
			},
		},
		{
			Type: "set_identifier", Title: "Set Identifier", Category: "registry", Capability: "write",
			Description: "Write an identifier / credential for the current tenant into the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "api_key", "source": "var.new_key"},
			Fields: []StepField{
				sf("key", "Identifier name", "Name of the identifier to write (e.g. api_key, client_id)", "api_key"),
				sf("source", "Value slot", "Slot whose value is written to the registry", "var.new_key"),
			},
		},
		{
			Type: "set_meta", Title: "Set Metadata", Category: "registry", Capability: "write",
			Description: "Write a metadata value for the current tenant into the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "tier", "source": "var.new_tier"},
			Fields: []StepField{
				sf("key", "Metadata key", "Name of the metadata field to write (e.g. tier, region, plan)", "tier"),
				sf("source", "Value slot", "Slot whose value is written to the registry", "var.new_tier"),
			},
		},
		{
			Type: "load_identifier", Title: "Load Identifier", Category: "registry", Capability: "credential",
			Description: "Load a named identifier / secret for the current tenant (stored as id:<name>) into a slot. Requires registry_lookup to have run first.",
			Defaults: map[string]string{"key": "api_key", "as": "tenant_api_key"},
			Fields: []StepField{
				sf("key", "Identifier name", "Name of the identifier as registered in the tenant (e.g. api_key, client_id, secret)", "api_key"),
				sf("as", "Store as", "Slot name to save the identifier value into", "tenant_api_key"),
			},
		},

		// ── Auth ─────────────────────────────────────────────────────────────────
		{
			Type: "token_validation", Title: "Token Validation", Category: "auth", Capability: "jwt",
			Description: "Validate a JWT or API token. Returns 401 if validation fails.",
			Defaults: map[string]string{"key_identifier": "header.Authorization"},
			Fields: []StepField{
				sf("key_identifier", "Token slot", "Slot or header source containing the token to validate (e.g. header.Authorization)", "header.Authorization"),
			},
		},

		// ── Rate Limiting ─────────────────────────────────────────────────────────
		{
			Type: "assign_quota_group", Title: "Assign Quota Group", Category: "rate-limit", Capability: "quota",
			Description: "Map a runtime string value (e.g. tenant tier from metadata) to a quota group ID. The group ID selects a rate limit config in the following check_rate_limit step. Must run before check_rate_limit.",
			Defaults: map[string]string{"key_identifier": "tier_slot"},
			Fields: []StepField{
				sf("key_identifier", "Source slot", "Slot holding the tier/plan string (e.g. loaded via load_meta)", "tier_slot"),
				sf("input", "Group map (JSON)", `Map of tier name → group ID (1–255). E.g. {"free":"1","pro":"2","enterprise":"3"}`, `{"free":"1","pro":"2","enterprise":"3"}`),
			},
		},
		{
			Type: "check_rate_limit", Title: "Check Rate Limit", Category: "rate-limit", Capability: "throttle",
			Description: "Enforce the rate limit configured for this API / endpoint. Returns 403 if the tenant is blocked; 429 if the window limit is exceeded. Optionally accepts a quota group → rate-limit config map so different SLA tiers share one flow.",
			Defaults: map[string]string{},
			Fields: []StepField{
				sf("input", "Quota group map (optional JSON)", `Map of group ID → rate limit config name. E.g. {"1":"free_rl","2":"pro_rl","3":"enterprise_rl"}. Leave empty if not using quota groups.`, ""),
			},
		},

		// ── Network ───────────────────────────────────────────────────────────────
		{
			Type: "bind_client_ip", Title: "Bind Client IP", Category: "network", Capability: "identity",
			Description: "Extract the real client IP address and store it in a slot. Resolution order: X-Forwarded-For (first IP) → X-Real-IP → TCP RemoteAddr.",
			Defaults: map[string]string{"key_identifier": "client_ip"},
			Fields: []StepField{
				sf("key_identifier", "Store as", "Slot name to store the client IP string in (e.g. client_ip)", "client_ip"),
			},
		},

		// ── HTTP ─────────────────────────────────────────────────────────────────
		{
			Type: "http_call", Title: "HTTP Call", Category: "http", Capability: "upstream",
			Description: "Make an outbound HTTP request and store the response body in a slot.",
			Defaults: map[string]string{"url": "https://example.com/api", "timeout": "5000", "as": "http_resp"},
			Fields: []StepField{
				sf("url", "URL", "Static upstream URL. Leave blank when using url_var.", "https://example.com/api"),
				sf("url_var", "URL slot", "Slot name holding a dynamic URL (e.g. loaded via load_service_url). Takes precedence over url.", "upstream_url"),
				sf("timeout", "Timeout (ms)", "Max wait in milliseconds before the request is aborted", "5000"),
				sf("retry_condition", "Retry condition", "Boolean expression; request is retried when true (e.g. status >= 500)", "status >= 500"),
				sf("max_retries", "Max retries", "Maximum retry attempts (0 = no retries)", "3"),
			},
		},

		// ── Observability / Tracing ───────────────────────────────────────────────
		{
			Type: "store_internal_tx_id", Title: "Store Transaction ID", Category: "observability", Capability: "tracing",
			Description: "Save the gateway-assigned internal transaction ID (InternalTxID) into a slot for use in downstream headers or logs.",
			Defaults: map[string]string{"as": "tx_id"},
			Fields: []StepField{
				sf("as", "Store as", "Slot name to save the formatted transaction ID string into", "tx_id"),
			},
		},
		{
			Type: "bind_correlation_id", Title: "Bind Correlation ID", Category: "observability", Capability: "tracing",
			Description: "Read a correlation ID from an incoming header; optionally generate one if the header is absent.",
			Defaults: map[string]string{"key": "X-Correlation-ID", "as": "corr_id", "generate_if_missing": "true"},
			Fields: []StepField{
				sf("key", "Header name", "Incoming request header to read the correlation ID from", "X-Correlation-ID"),
				sf("as", "Store as", "Slot name to save the correlation ID into", "corr_id"),
				sf("generate_if_missing", "Generate if missing", "Set to true to generate a new ID when the header is absent (true/false)", "true"),
			},
		},

		// ── Control Flow ─────────────────────────────────────────────────────────
		{
			Type: "if", Title: "If / Else", Category: "control", Capability: "branching", SupportsNested: true,
			Description: "Branch to one of two sub-flows based on a boolean condition.",
			Defaults: map[string]string{"condition": "", "then": "", "else": ""},
			NextHints: []string{"call", "http_call", "set_response_status"},
			Fields: []StepField{
				sf("condition", "Condition", "Boolean expression. Examples: header.X-Role == \"admin\", status >= 500, query.debug != \"\"", "header.X-Role == \"admin\""),
				sf("then", "Then → flow", "Flow name to execute when condition is true", ""),
				sf("else", "Else → flow", "Flow name to execute when condition is false (optional)", ""),
			},
		},
		{
			Type: "switch", Title: "Switch", Category: "control", Capability: "multi-branch", SupportsNested: true,
			Description: "Route to one of several sub-flows based on the string value of a slot.",
			Defaults: map[string]string{"as": "", "cases": ""},
			Fields: []StepField{
				sf("as", "Match expression", "Slot or request source to match on (e.g. header.X-Plan, query.mode)", "header.X-Plan"),
				sf("cases", "Cases", "Comma-separated key=flow pairs: free=free_flow,premium=premium_flow", "free=free_flow,premium=premium_flow"),
			},
		},
		{
			Type: "foreach", Title: "For Each", Category: "control", Capability: "iteration", SupportsNested: true,
			Description: "Iterate over a list in a slot and execute a sub-flow once per item.",
			Defaults: map[string]string{"source": "", "as": "item", "do": ""},
			Fields: []StepField{
				sf("source", "Source list slot", "Slot containing the array to iterate over", "var.items"),
				sf("as", "Item slot", "Slot name bound to the current item inside the sub-flow", "item"),
				sf("do", "Sub-flow", "Flow name called for each item in the list", "process_item"),
			},
		},
		{
			Type: "call", Title: "Call Flow", Category: "control", Capability: "sub-flow",
			Description: "Invoke a named sub-flow inline, sharing the current slot context.",
			Defaults: map[string]string{"flow_name": ""},
			Fields: []StepField{
				sf("flow_name", "Flow name", "Name of the sub-flow to invoke", "my_sub_flow"),
			},
		},

		// ── String Ops ───────────────────────────────────────────────────────────
		{
			Type: "concat", Title: "Concat", Category: "string", Capability: "string-op",
			Description: "Concatenate two string slots with an optional separator.",
			Defaults: map[string]string{"key_identifier": "var.prefix", "source": "var.suffix", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Left string", "Slot providing the first part", "var.prefix"),
				sf("source", "Right string", "Slot providing the second part", "var.suffix"),
				sf("value", "Separator", "String inserted between the two parts (blank = direct join)", ""),
				sf("as", "Store as", "Slot to save the result into", "result"),
			},
		},
		{
			Type: "to_lower", Title: "To Lower", Category: "string", Capability: "string-op",
			Description: "Convert a string slot to lower-case.",
			Defaults: map[string]string{"source": "var.input", "as": "lower_val"},
			Fields: []StepField{
				sf("source", "Source slot", "Slot containing the string to convert", "var.input"),
				sf("as", "Store as", "Slot to save the lower-cased result into", "lower_val"),
			},
		},
		{
			Type: "to_upper", Title: "To Upper", Category: "string", Capability: "string-op",
			Description: "Convert a string slot to upper-case.",
			Defaults: map[string]string{"source": "var.input", "as": "upper_val"},
			Fields: []StepField{
				sf("source", "Source slot", "Slot containing the string to convert", "var.input"),
				sf("as", "Store as", "Slot to save the upper-cased result into", "upper_val"),
			},
		},
		{
			Type: "substring", Title: "Substring", Category: "string", Capability: "string-op",
			Description: "Slice a string slot by byte offset. Provide start and/or length in the input map.",
			Defaults: map[string]string{"source": "var.input", "as": "sliced"},
			Fields: []StepField{
				sf("source", "Source slot", "Slot containing the string to slice", "var.input"),
				sf("as", "Store as", "Slot to save the substring into", "sliced"),
			},
		},
		{
			Type: "to_int", Title: "To Int", Category: "string", Capability: "type-convert",
			Description: "Parse a string slot as a 64-bit integer and store in an int slot.",
			Defaults: map[string]string{"source": "var.str_val", "as": "int_val"},
			Fields: []StepField{
				sf("source", "Source slot", "Slot containing the string to parse", "var.str_val"),
				sf("as", "Store as", "Slot to save the integer into", "int_val"),
			},
		},

		// ── Math ─────────────────────────────────────────────────────────────────
		{
			Type: "add", Title: "Add", Category: "math", Capability: "arithmetic",
			Description: "Add two numeric slots and store the result.",
			Defaults: map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Left operand", "Slot containing the first number", "var.a"),
				sf("source", "Right operand", "Slot containing the second number", "var.b"),
				sf("as", "Store as", "Slot to save the sum into", "result"),
			},
		},
		{
			Type: "sub", Title: "Subtract", Category: "math", Capability: "arithmetic",
			Description: "Subtract the right-operand slot from the left and store the result.",
			Defaults: map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Left operand", "Slot containing the number to subtract from", "var.a"),
				sf("source", "Right operand", "Slot containing the number to subtract", "var.b"),
				sf("as", "Store as", "Slot to save the difference into", "result"),
			},
		},
		{
			Type: "mul", Title: "Multiply", Category: "math", Capability: "arithmetic",
			Description: "Multiply two numeric slots and store the result.",
			Defaults: map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Left operand", "Slot containing the first factor", "var.a"),
				sf("source", "Right operand", "Slot containing the second factor", "var.b"),
				sf("as", "Store as", "Slot to save the product into", "result"),
			},
		},
		{
			Type: "div", Title: "Divide", Category: "math", Capability: "arithmetic",
			Description: "Divide the left-operand slot by the right and store the result.",
			Defaults: map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Dividend", "Slot containing the number to be divided", "var.a"),
				sf("source", "Divisor", "Slot containing the divisor", "var.b"),
				sf("as", "Store as", "Slot to save the quotient into", "result"),
			},
		},

		// ── Cache ─────────────────────────────────────────────────────────────────
		{
			Type: "cache_get", Title: "Cache Get (Tenant)", Category: "cache", Capability: "read",
			Description: "Look up a key in the tenant-scoped cache. On hit, writes the value to the dest slot. On miss, the dest slot is unchanged. Follow with an `if` step checking whether the slot is non-empty to branch on hit vs miss.",
			Defaults: map[string]string{"key_identifier": "cache_key", "as": "cached_body"},
			Fields: []StepField{
				sf("key_identifier", "Key slot", "Slot whose value is used as the cache lookup key", "cache_key"),
				sf("as", "Store as", "Slot to write the cached value into on a hit", "cached_body"),
			},
		},
		{
			Type: "cache_put", Title: "Cache Put (Tenant)", Category: "cache", Capability: "write",
			Description: "Store a value in the tenant-scoped cache under the given key with a TTL in seconds. Skipped silently if key or value slot is empty.",
			Defaults: map[string]string{"key_identifier": "cache_key", "source": "upstream_response", "ttl": "300"},
			Fields: []StepField{
				sf("key_identifier", "Key slot", "Slot whose value is used as the cache key", "cache_key"),
				sf("source", "Value slot", "Slot holding the value to cache", "upstream_response"),
				sf("ttl", "TTL (seconds)", "How long to cache the value; routed to the nearest TTL tier", "300"),
			},
		},
		{
			Type: "cache_get_global", Title: "Cache Get (Global)", Category: "cache", Capability: "read",
			Description: "Look up a key in the shared (tenant-agnostic) cache namespace. Useful for caching data that is the same for all tenants (e.g. public API responses, config payloads). Behaviour is identical to cache_get but tenantID=0 is used.",
			Defaults: map[string]string{"key_identifier": "cache_key", "as": "cached_body"},
			Fields: []StepField{
				sf("key_identifier", "Key slot", "Slot whose value is used as the cache lookup key", "cache_key"),
				sf("as", "Store as", "Slot to write the cached value into on a hit", "cached_body"),
			},
		},
		{
			Type: "cache_put_global", Title: "Cache Put (Global)", Category: "cache", Capability: "write",
			Description: "Store a value in the shared (tenant-agnostic) cache namespace with a TTL in seconds. The stored value is readable by all tenants via cache_get_global.",
			Defaults: map[string]string{"key_identifier": "cache_key", "source": "upstream_response", "ttl": "300"},
			Fields: []StepField{
				sf("key_identifier", "Key slot", "Slot whose value is used as the cache key", "cache_key"),
				sf("source", "Value slot", "Slot holding the value to cache", "upstream_response"),
				sf("ttl", "TTL (seconds)", "How long to cache the value; routed to the nearest TTL tier", "300"),
			},
		},

		// ── Batch / Extract ──────────────────────────────────────────────────────
		{
			Type: "batch_flush", Title: "Batch Flush", Category: "cache", Capability: "batch",
			Description: "Flush all queued storage ops (cache gets/puts, registry ops) in a single pipeline round-trip. Place after a group of cache_get_batched / json_extract_emit steps and before any step that reads the results.",
			Defaults: map[string]string{},
			Fields:   []StepField{},
		},
		{
			Type: "cache_get_batched", Title: "Cache Get (Batched)", Category: "cache", Capability: "read",
			Description: "Queue a cache GET into the op buffer. The result is written to the dest slot only after a batch_flush instruction executes. Use when multiple cache lookups can be batched before their results are needed.",
			Defaults: map[string]string{"variable": "cache_key", "destination": "cached_body"},
			Fields: []StepField{
				sf("variable", "Key slot", "Slot whose value is used as the cache lookup key", "cache_key"),
				sf("destination", "Store as", "Slot to write the cached value into after batch_flush", "cached_body"),
			},
		},
		{
			Type: "json_extract_emit", Title: "JSON Extract & Emit", Category: "cache", Capability: "batch",
			Description: "Extract multiple fields from a JSON body in one scan and emit one storage op per field. Use params to configure each extraction (path, key_prefix, op_type, target, dest_slot/value_slot, async).",
			Defaults: map[string]string{"variable": "http_resp"},
			Fields: []StepField{
				sf("variable", "Body slot", "Slot holding the JSON body to extract from", "http_resp"),
				sf("params", "Extract ops (JSON array)", `Array of op descriptors. Each: {"path":"user.id","key_prefix":"user:","op_type":"put","target":"cache","value_slot":"","async":"true"}`, ""),
			},
		},
		{
			Type: "json_foreach_emit", Title: "JSON Foreach & Emit", Category: "cache", Capability: "batch",
			Description: "Iterate over a JSON array and emit one batch of storage ops per element. Handles unbounded arrays via auto-flush. Use params to configure per-element extractions.",
			Defaults: map[string]string{"variable": "http_resp", "path": "items"},
			Fields: []StepField{
				sf("variable", "Body slot", "Slot holding the JSON body containing the array", "http_resp"),
				sf("path", "Array path", "gjson path to the array within the body (e.g. items, data.services)", "items"),
				sf("params", "Extract ops (JSON array)", `Array of op descriptors applied to each element. Each: {"path":"id","key_prefix":"item:","op_type":"put","target":"cache","value_slot":"","async":"true"}`, ""),
			},
		},

		// ── Response ─────────────────────────────────────────────────────────────
		{
			Type: "set_response_header", Title: "Set Response Header", Category: "response", Capability: "response-mod",
			Description: "Set a response header to the value from a slot.",
			Defaults: map[string]string{"key": "X-Custom-Header", "source": "var.header_value"},
			Fields: []StepField{
				sf("key", "Header name", "Name of the HTTP response header to set", "X-Request-ID"),
				sf("source", "Value slot", "Slot whose value is written to the header", "var.header_value"),
			},
		},
		{
			Type: "set_response_body", Title: "Set Response Body", Category: "response", Capability: "response-mod",
			Description: "Replace the response body with the value from a slot.",
			Defaults: map[string]string{"source": "var.body"},
			Fields: []StepField{
				sf("source", "Body slot", "Slot whose value becomes the response body", "var.body"),
			},
		},
		{
			Type: "set_response_status", Title: "Set Response Status", Category: "response", Capability: "response-mod",
			Description: "Set the HTTP response status code.",
			Defaults: map[string]string{"value": "200"},
			Fields: []StepField{
				sf("value", "Status code", "Numeric HTTP status code (e.g. 200, 401, 404, 503)", "200"),
			},
		},
		{
			Type: "echo_request", Title: "Echo Request", Category: "response", Capability: "debug",
			Description: "Mirror the incoming request back as the response. Useful for testing flows.",
			Defaults: map[string]string{},
			Fields:   []StepField{},
		},
	}
}

// BuildStepCatalog builds a StepCatalog from AllStepDescriptors, deduplicating
// and sorting categories. Called by ManagementServer.StepsMetaHandler.
func BuildStepCatalog() StepCatalog {
	steps := AllStepDescriptors()
	seen := map[string]struct{}{}
	cats := make([]string, 0)
	for _, s := range steps {
		if _, ok := seen[s.Category]; !ok {
			seen[s.Category] = struct{}{}
			cats = append(cats, s.Category)
		}
	}
	return StepCatalog{
		Version:    "1",
		Categories: cats,
		Steps:      steps,
	}
}
