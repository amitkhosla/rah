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
	base := []StepDescriptor{

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
			Description: "Validate a JWT. Verifies signature (JWKS), standard claims, required scopes, and arbitrary custom claims. Every parameter supports a static value or a runtime variable loaded by any earlier step.",
			Defaults: map[string]string{"key_identifier": "header.Authorization"},
			Fields: []StepField{
				// Token source
				sf("key_identifier",                 "Token source",                    "Where to read the token: header.X, query.X, cookie.X, or a variable name", "header.Authorization"),
				// JWKS / crypto
				sf("input.jwt.jwks_uri",             "JWKS URL (static)",               "JWKS endpoint URL", "https://YOUR_IDP/.well-known/jwks.json"),
				sf("input.jwt.jwks_uri_var",         "JWKS URL (variable)",             "Variable holding the JWKS URL (e.g. from load_service_url)", ""),
				sf("input.jwt.alg",                  "Algorithm (static)",              "JWT algorithm. Default: RS256", "RS256"),
				sf("input.jwt.alg_var",              "Algorithm (variable)",            "Variable holding the algorithm string", ""),
				sf("input.jwt.leeway_seconds",       "Leeway seconds (static)",         "Clock skew tolerance in seconds. Default: 30", "30"),
				sf("input.jwt.leeway_var",           "Leeway seconds (variable)",       "Variable holding clock leeway as a number string", ""),
				sf("input.jwt.prefetch_jwks",        "Prefetch JWKS",                   "Pre-warm JWKS cache at deploy time (true/false)", "true"),
				// Validation checks
				sf("input.jwt.validate",             "Validate (static)",               "Comma-sep: signature,issuer,audience,expiry,not_before. Empty = all.", "signature,expiry"),
				sf("input.jwt.validate_var",         "Validate (variable)",             "Variable holding the comma-sep validation check list", ""),
				// Claim values
				sf("input.jwt.issuer",               "Issuer (static)",                 "Expected iss claim value", "https://accounts.example.com"),
				sf("input.jwt.issuer_var",           "Issuer (variable)",               "Variable holding the expected issuer", ""),
				sf("input.jwt.audience",             "Audience (static)",               "Expected aud claim value", "my-api"),
				sf("input.jwt.audience_var",         "Audience (variable)",             "Variable holding the expected audience", ""),
				// Scopes
				sf("input.jwt.required_scopes",     "Required scopes (static)",        "Comma-sep scope values that must be present", "read:orders"),
				sf("input.jwt.required_scopes_var", "Required scopes (variable)",      "Variable holding comma-sep required scopes", ""),
				sf("input.jwt.scope_claims",         "Scope claim keys (static)",       "Claim keys to scan for scopes. Default: scope,scp", "scope,scp"),
				sf("input.jwt.scope_claims_var",     "Scope claim keys (variable)",     "Variable holding the scope claim key list", ""),
				// Custom claims
				sf("input.jwt.custom_claims",        "Custom claims (JSON)",            `JSON object of static claim checks e.g. {"role":"admin"}`, `{"role":"admin"}`),
				sf("input.jwt.custom_claims_vars",   "Custom claim variables (JSON)",   `JSON object mapping claim keys to variable names e.g. {"org":"var.tenant_org"}`, ""),
				// Failure config
				sf("input.jwt.on_failure",           "On failure mode (static)",        `"stop" (return error) or "continue" (write result variable and proceed)`, "stop"),
				sf("input.jwt.on_failure_var",       "On failure mode (variable)",      "Variable holding 'stop' or 'continue'", ""),
				sf("input.jwt.failure_status",       "Failure status (static)",         "HTTP status code on failure. Default: 401", "401"),
				sf("input.jwt.failure_status_var",   "Failure status (variable)",       "Variable holding the failure HTTP status code string", ""),
				sf("input.jwt.failure_body",         "Failure body (static)",           "Response body on failure. Default: unauthorized", "unauthorized"),
				sf("input.jwt.failure_body_var",     "Failure body (variable)",         "Variable holding the failure response body", ""),
				// Result values
				sf("input.jwt.result_success",       "Success result value (static)",   "Value written to result variable on success. Default: true", "true"),
				sf("input.jwt.result_success_var",   "Success result value (variable)", "Variable holding the success result value", ""),
				sf("input.jwt.result_failure",       "Failure result value (static)",   "Value written to result variable on failure. Default: false", "false"),
				sf("input.jwt.result_failure_var",   "Failure result value (variable)", "Variable holding the failure result value", ""),
				// Output variables
				sf("input.jwt.result_var",           "Result variable",                 "Variable to write result value into (requires on_failure=continue)", ""),
				sf("input.jwt.claims_var",           "Claims output variable",          "Variable to write all JWT claims JSON into on success", ""),
				sf("input.jwt.subject_var",          "Subject output variable",         "Variable to write the JWT sub (subject) claim into on success", ""),
				sf("input.jwt.client_id_var",        "Client ID output variable",       "Variable to write the client_id (or azp/appid) claim into on success", ""),
				sf("input.jwt.scopes_out_var",       "Scopes output variable",          "Variable to write comma-separated parsed scopes into on success", ""),
			},
		},

		{
			Type: "load_secret", Title: "Load Secret", Category: "auth", Capability: "auth",
			Description: "Fetch a secret from GSM, Vault, AWS SM, or env into a slot.",
			Defaults: map[string]string{"ref": "", "slot": "0"},
			Fields: []StepField{
				sf("ref", "Secret Reference", "Secret URI e.g. gsm://project/secrets/name or env:MY_VAR", "gsm://my-project/secrets/api-key"),
				sf("slot", "Destination Slot", "ByteSlot index to write secret value into", "0"),
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
			Type: "bind_header", Title: "Read Header", Category: "request", Capability: "request",
			Description: "Extract an HTTP request header value into a slot.",
			Defaults: map[string]string{"key": "", "slot": "0"},
			Fields: []StepField{
				sf("key", "Header Name", "Name of the HTTP header e.g. Authorization", "Authorization"),
				sf("slot", "Destination Slot", "ByteSlot index to write value into", "0"),
			},
		},
		{
			Type: "bind_query", Title: "Read Query Param", Category: "request", Capability: "request",
			Description: "Extract a URL query parameter value into a slot.",
			Defaults: map[string]string{"key": "", "slot": "0"},
			Fields: []StepField{
				sf("key", "Param Name", "Query parameter name e.g. api_key", "api_key"),
				sf("slot", "Destination Slot", "ByteSlot index to write value into", "0"),
			},
		},
		{
			Type: "bind_path", Title: "Read Path Param", Category: "request", Capability: "request",
			Description: "Extract a path parameter (e.g. {id}) into a slot by index.",
			Defaults: map[string]string{"index": "0", "slot": "0"},
			Fields: []StepField{
				sf("index", "Param Index", "0-based index of the path parameter", "0"),
				sf("slot", "Destination Slot", "ByteSlot index to write value into", "0"),
			},
		},
		{
			Type: "bind_client_ip", Title: "Bind Client IP", Category: "network", Capability: "identity",
			Description: "Extract the real client IP address and store it in a slot. Resolution order: X-Forwarded-For (first IP) → X-Real-IP → TCP RemoteAddr.",
			Defaults: map[string]string{"key_identifier": "client_ip"},
			Fields: []StepField{
				sf("key_identifier", "Store as", "Slot name to store the client IP string in (e.g. client_ip)", "client_ip"),
			},
		},
		{
			Type: "set_request_header", Title: "Set Upstream Header", Category: "request", Capability: "request",
			Description: "Inject a header into the upstream request before proxying. The header name is static; the value is read from a slot at runtime.",
			Defaults: map[string]string{"key": "Cookie", "source": "var.cookie_val"},
			Fields: []StepField{
				sf("key", "Header Name", "Static header name to inject into the upstream request (e.g. Cookie, X-Auth-Token)", "Cookie"),
				sf("source", "Value slot", "Slot whose value is used as the header value", "var.cookie_val"),
			},
		},
		{
			Type: "ip_restriction", Title: "IP Restriction", Category: "network", Capability: "access-control",
			Description: "Allow or deny requests based on CIDR ranges. Returns configured status/body when blocked.",
			Defaults: map[string]string{"input": `{"mode":"allow","cidrs":"10.0.0.0/8","source":"header.X-Forwarded-For","on_violation_status":"403","on_violation_body":"ip not allowed"}`},
			Fields: []StepField{
				sf("key_identifier", "IP slot (optional)", "Optional slot with pre-resolved client IP (from bind_client_ip). If set, it overrides source resolution.", "client_ip"),
				sf("input", "Config (JSON)", `mode: allow|deny, cidrs: comma-separated CIDRs, source: header.X-Forwarded-For|header.X-Real-IP|remote_addr, on_violation_status: 4xx/5xx, on_violation_body: response text`, `{"mode":"allow","cidrs":"10.0.0.0/8,192.168.0.0/16","source":"header.X-Forwarded-For","on_violation_status":"403","on_violation_body":"ip not allowed"}`),
			},
		},

		// ── AI / LLM ─────────────────────────────────────────────────────────────
		{
			Type: "llm_call", Title: "LLM Call", Category: "ai", Capability: "inference",
			Description: "Send a prompt to a configured LLM (Anthropic, OpenAI, Gemini, Ollama) and store the text response in a slot. Returns 413 if the prompt exceeds the model's context limit.",
			Defaults: map[string]string{
				"key_identifier": "var.prompt",
				"as":             "llm_response",
				"input":          `{"model":"claude-sonnet-4-6","temperature":"0.7","max_tokens":"2000"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Prompt slot", "Slot containing the user prompt text", "var.prompt"),
				sf("as", "Store as", "Slot to write the LLM text response into", "llm_response"),
				sf("input", "Config (JSON)", `Keys: model (catalog alias), temperature, max_tokens, timeout_ms, system_slot, api_key, fallback_model (single alias), fallback_chain (comma-sep aliases), fallback_slot (dynamic prioritization), history_slot (stateful conversion), model_config_slot (runtime JSON model config), messages_slot (multi-turn: JSON []CanonicalMessage from parse_message_format), stop_reason_slot, input_tokens_slot (IntSlot index), output_tokens_slot (IntSlot index)`, `{"model":"claude-sonnet-4-6","temperature":"0.7","max_tokens":"2000","history_slot":"var.history"}`),
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
		{
			Type: "log_field", Title: "Log Custom Field", Category: "observability", Capability: "logging",
			Description: "Write a slot value as a named field in the request access log Extra section.",
			Defaults: map[string]string{"key": "client_id", "source": "var.client_id"},
			Fields: []StepField{
				sf("key", "Field name", "Name of the field as it appears in the access log Extra section (e.g. client_id, tenant_alias)", "client_id"),
				sf("source", "Source slot", "Slot whose value is written to the access log", "var.client_id"),
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
		{
			Type: "return", Title: "Return Response", Category: "control", Capability: "termination",
			Description: "Terminate flow immediately and send an HTTP response to the caller.",
			Defaults: map[string]string{"status": "200", "body": ""},
			Fields: []StepField{
				sf("status", "HTTP status", "Numeric HTTP status code to send (default 200)", "200"),
				sf("body", "Static body", "Static response body string. Leave empty to use the variable named by 'as'.", ""),
				sf("as", "Body variable", "Variable whose value is used as the response body (when body is empty)", ""),
			},
		},
		{
			Type: "fail", Title: "Fail", Category: "control", Capability: "termination",
			Description: "Mark the request as failed with a code and message, then stop.",
			Defaults: map[string]string{"status": "500", "body": ""},
			Fields: []StepField{
				sf("status", "Error code", "Numeric error code stored in ctx.ErrorCode (default 500)", "500"),
				sf("body", "Static message", "Static error message string. Leave empty to use the variable named by 'as'.", ""),
				sf("as", "Message variable", "Variable holding a dynamic error message (used when body is empty)", ""),
			},
		},
		{
			Type: "capture_error", Title: "Capture Error", Category: "control", Capability: "error-handling",
			Description: "Capture current error state into variables and clear it, allowing the flow to continue.",
			Defaults: map[string]string{},
			Fields: []StepField{
				sf("key", "Error code variable", "Variable to capture the numeric error code into", ""),
				sf("as", "Error message variable", "Variable to capture the error message into", "err_msg"),
			},
		},

		// ── String Ops ───────────────────────────────────────────────────────────
		{
			Type: "concat", Title: "Concat", Category: "string", Capability: "string-op",
			Description: "Join two string variables into one. Result = left + separator + right. When only 'source' is given, 'value' acts as a static prefix placed before the source value.",
			Defaults: map[string]string{"source": "var.input", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Left variable (optional)", "Variable providing the left part. Omit to use 'value' alone as a static prefix.", ""),
				sf("source", "Right variable", "Variable (or request source like path.id, header.X-Name) providing the right part", "var.input"),
				sf("value", "Static prefix / separator", "Static text. Placed before 'source' when left variable is absent; inserted between left and right when both are present.", ""),
				sf("as", "Save result as", "Variable name to write the concatenated value into", "result"),
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
		{
			Type: "byte_length", Title: "String Length", Category: "string", Capability: "type-convert",
			Description: "Write the byte-length of a slot value into an integer slot.",
			Defaults: map[string]string{"source": "var.str_val", "as": "int_len"},
			Fields: []StepField{
				sf("source", "Source slot", "Slot whose byte-length to measure", "var.str_val"),
				sf("as", "Integer dest slot", "Int slot to write the length into", "int_len"),
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

	// Append AI sub-group step descriptors.
	base = append(base, SanitizeStepDescriptors()...)
	base = append(base, RoutingStepDescriptors()...)
	base = append(base, FormatStepDescriptors()...)
	base = append(base, DetectIntentStepDescriptors()...)
	base = append(base, ClassifyStepDescriptors()...)
	base = append(base, MCPStepDescriptors()...)
	base = append(base, MCPCallToolDescriptors()...)
	base = append(base, HistoryStepDescriptors()...)
	base = append(base, DetectStepDescriptors()...)
	base = append(base, ContextFitStepDescriptors()...)
	base = append(base, TransformStepDescriptors()...)
	base = append(base, OverflowStepDescriptors()...)
	base = append(base, ToolParseStepDescriptors()...)
	base = append(base, LLMKeyStepDescriptors()...)
	base = append(base, WhileStepDescriptors()...)
	base = append(base, EmbedStepDescriptors()...)
	base = append(base, ExecutePlanStepDescriptors()...)
	base = append(base, VectorStepDescriptors()...)
	base = append(base, ChunkStepDescriptors()...)
	base = append(base, SSEStepDescriptors()...)
	base = append(base, RerankStepDescriptors()...)
	base = append(base, SemanticCacheStepDescriptors()...)
	base = append(base, MCPStepDescriptors()...)
	base = append(base, ServeMCPStepDescriptors()...)
	base = append(base, IngestStepDescriptors()...)
	base = append(base, CostStepDescriptors()...)
	return base
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

// IngestStepDescriptors returns descriptors for the ingestion/logging pipeline steps.
func IngestStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type: "emit_event", Title: "Emit Ingest Event", Category: "ingest", Capability: "logging",
			Description: "Emit a structured event to the ingestion pipeline (non-blocking). Runs after response if deferred=true.",
			Fields: []StepField{
				sf("input.kind", "Event kind", "One of: prompt_in, prompt_out, llm_request, llm_response, tool_call, tool_result, route_decision, cache_hit, custom", "llm_request"),
				sf("input.payload_slot", "Payload slot", "Slot name containing the event payload (raw bytes or JSON)", "var.prompt"),
				sf("input.model_slot", "Model slot", "Optional: slot containing the model name string", "var.chosen_model"),
				sf("input.session_slot", "Session slot", "Optional: slot containing the session ID", "var.session_id"),
				sf("input.deferred", "Deferred", "true = emit after HTTP response is committed; false = emit immediately", "false"),
			},
		},
	}
}
