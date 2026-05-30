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
			Description: "Resolve an alias (from a variable) to a TenantID. Must run before any load_service_url or load_identifier steps.",
			Defaults: map[string]string{"key_identifier": "header.X-Tenant-ID"},
			Fields: []StepField{
				sf("key_identifier", "Alias variable", "Variable whose value is used as the tenant alias for lookup (e.g. a header, query param, or path segment)", "header.X-Tenant-ID"),
			},
		},
		{
			Type: "load_service_url", Title: "Load Service URL", Category: "registry", Capability: "service-url",
			Description: "Load a named upstream URL for the current tenant (stored as url:<name>) into a variable. Requires registry_lookup to have run first.",
			Defaults: map[string]string{"key": "primary", "as": "upstream_url"},
			Fields: []StepField{
				sf("key", "URL name", "Name of the service URL as registered in the tenant (e.g. primary, fallback, health)", "primary"),
				sf("as", "Store as", "Variable name to save the URL into; use this variable in a following http_call as url_var", "upstream_url"),
			},
		},
		{
			Type: "load_service_url_var", Title: "Load Service URL (Dynamic Key)", Category: "registry", Capability: "service-url",
			Description: "Load a service URL using a key name from a variable (e.g. set via a route constant or earlier step). ~50–100 ns vs 2–5 ns for static Load Service URL. Use only when the key differs per API.",
			Defaults: map[string]string{"key_identifier": "url_key", "as": "upstream_url"},
			Fields: []StepField{
				sf("key_identifier", "Key name variable", "Variable holding the URL key name at runtime (e.g. 'payments_url')", "url_key"),
				sf("as", "Store as", "Variable to save the resolved URL into", "upstream_url"),
			},
		},
		{
			Type: "set_service_url", Title: "Set Service URL", Category: "registry", Capability: "write",
			Description: "Write a service URL for the current tenant into the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "primary", "source": "var.new_url"},
			Fields: []StepField{
				sf("key", "URL name", "Name of the service URL to write (e.g. primary, fallback, health)", "primary"),
				sf("source", "Value variable", "Variable whose value is written to the registry", "var.new_url"),
			},
		},
		{
			Type: "set_identifier", Title: "Set Identifier", Category: "registry", Capability: "write",
			Description: "Write an identifier / credential for the current tenant into the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "api_key", "source": "var.new_key"},
			Fields: []StepField{
				sf("key", "Identifier name", "Name of the identifier to write (e.g. api_key, client_id)", "api_key"),
				sf("source", "Value variable", "Variable whose value is written to the registry", "var.new_key"),
			},
		},
		{
			Type: "set_meta", Title: "Set Metadata", Category: "registry", Capability: "write",
			Description: "Write a metadata value for the current tenant into the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "tier", "source": "var.new_tier"},
			Fields: []StepField{
				sf("key", "Metadata key", "Name of the metadata field to write (e.g. tier, region, plan)", "tier"),
				sf("source", "Value variable", "Variable whose value is written to the registry", "var.new_tier"),
			},
		},
		{
			Type: "delete_service_url", Title: "Delete Service URL", Category: "registry", Capability: "write",
			Description: "Remove a service URL for the current tenant from the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "primary"},
			Fields: []StepField{
				sf("key", "URL name", "Name of the service URL to delete (e.g. primary, fallback, health)", "primary"),
			},
		},
		{
			Type: "delete_identifier", Title: "Delete Identifier", Category: "registry", Capability: "write",
			Description: "Remove an identifier / credential for the current tenant from the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "api_key"},
			Fields: []StepField{
				sf("key", "Identifier name", "Name of the identifier to delete (e.g. api_key, client_id)", "api_key"),
			},
		},
		{
			Type: "delete_meta", Title: "Delete Metadata", Category: "registry", Capability: "write",
			Description: "Remove a metadata value for the current tenant from the registry. Requires registry_lookup to have run first. Use in admin/onboarding flows.",
			Defaults: map[string]string{"key": "tier"},
			Fields: []StepField{
				sf("key", "Metadata key", "Name of the metadata field to delete (e.g. tier, region, plan)", "tier"),
			},
		},
		{
			Type: "load_identifier", Title: "Load Identifier", Category: "registry", Capability: "credential",
			Description: "Load a named identifier / secret for the current tenant (stored as id:<name>) into a variable. Requires registry_lookup to have run first.",
			Defaults: map[string]string{"key": "api_key", "as": "tenant_api_key"},
			Fields: []StepField{
				sf("key", "Identifier name", "Name of the identifier as registered in the tenant (e.g. api_key, client_id, secret)", "api_key"),
				sf("as", "Store as", "Variable name to save the identifier value into", "tenant_api_key"),
			},
		},
		{
			Type: "load_meta", Title: "Load Meta", Category: "registry", Capability: "identity",
			Description: "Load a metadata value for the current tenant (e.g. tier, region, field_enc_key_ref) into a variable. Requires registry_lookup to have run first.",
			Defaults: map[string]string{"key": "tier", "as": "tier_slot"},
			Fields: []StepField{
				sf("key", "Meta key", "Name of the metadata property registered in the tenant (e.g. tier, region, field_enc_key_ref)", "tier"),
				sf("as", "Store as", "Variable name to write the value into", "tier_slot"),
			},
		},

		// ── Auth ─────────────────────────────────────────────────────────────────
		{
			Type: "validate_api_key", Title: "Validate API Key",
			Description: "Validates an inbound API key by SHA-256 hash lookup. Sets ctx.CallerID (AppID, stable across rotation) and ctx.CallerKey (alias) on success. Use before check_rate_limit_v2 with count_by: app.",
			Category: "auth", Capability: "api_key_auth",
			Defaults: map[string]string{
				"apikey.source":         "header",
				"apikey.header":         "X-API-Key",
				"apikey.on_failure":     "stop",
				"apikey.failure_status": "401",
				"apikey.failure_body":   "unauthorized",
				"apikey.require_tenant": "false",
			},
			Fields: []StepField{
				sf("apikey.source",         "Key Source",       "header | query | cookie | variable",                      "header"),
				sf("apikey.header",         "Header Name",      "Header containing the API key (source=header)",       "X-API-Key"),
				sf("apikey.query_param",    "Query Param",      "Query parameter name (source=query)",                 "api_key"),
				sf("apikey.cookie",         "Cookie Name",      "Cookie name (source=cookie)",                         "api_key"),
				sf("apikey.slot",           "Variable",    "Variable name (source=variable)",                    ""),
				sf("apikey.on_failure",     "On Failure",       "stop = halt request; continue = write result and proceed", "stop"),
				sf("apikey.failure_status", "Failure Status",   "HTTP status on auth failure",                         "401"),
				sf("apikey.failure_body",   "Failure Body",     "Response body on auth failure",                       "unauthorized"),
				sf("apikey.require_tenant", "Require Tenant",   "true = key AllowedTenants must include ctx.TenantID", "false"),
				sf("apikey.result_var",     "Result Variable",  "Variable to write 'true'/'false' into (continue mode)",   ""),
			},
		},
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
			Type: "extract_jwt_claim", Title: "Extract JWT Claim", Category: "auth", Capability: "jwt",
			Description: "Extract a single named claim from a JWT without signature verification. " +
				"Use to pull identity or metadata claims (sub, scope, client_id, custom fields) " +
				"into a slot for downstream rate limiting, routing, or cost keying. " +
				"Does NOT validate the signature — use token_validation when cryptographic verification is required.",
			Defaults: map[string]string{
				"key_identifier": "header.Authorization",
				"as":             "var.jwt_sub",
			},
			Fields: []StepField{
				sf("key_identifier", "Token source",
					`Where to read the JWT. Examples: "header.Authorization" (strips Bearer prefix automatically), "header.X-Custom-Token", "var.token_slot"`,
					"header.Authorization"),
				sf("as", "Output slot", "Slot to write the extracted claim value into", "var.jwt_sub"),
				sf("claim_name", "Claim name", `JWT claim to extract (e.g. "sub", "scope", "client_id"). Default: "sub"`, "sub"),
				sf("default_value", "Default value", "Value to write when claim is absent or token is invalid. Default: empty string.", ""),
			},
		},

		{
			Type: "load_secret", Title: "Load Secret", Category: "auth", Capability: "auth",
			Description: "Fetch a secret from GSM, Vault, AWS SM, or env into a variable.",
			Defaults: map[string]string{"ref": "", "as": "secret_value"},
			Fields: []StepField{
				sf("ref", "Secret Reference", "Secret URI e.g. gsm://project/secrets/name or env:MY_VAR", "gsm://my-project/secrets/api-key"),
				sf("as", "Store into variable", "Variable name to write secret value into", "secret_value"),
			},
		},

		// ── DPoP (RFC 9449) ───────────────────────────────────────────────────────
		{
			Type: "validate_dpop", Title: "Validate DPoP Proof", Category: "auth", Capability: "dpop",
			Description: "Validate a DPoP (Demonstrating Proof of Possession) proof JWT per RFC 9449. Binds an access token to a specific client key so stolen bearer tokens cannot be replayed. Must run after token_validation when using DPoP-bound access tokens.",
			Defaults: map[string]string{
				"input.dpop.max_age_seconds": "60",
				"input.dpop.on_failure":      "stop",
				"input.dpop.failure_status":  "401",
				"input.dpop.failure_body":    "invalid dpop proof",
			},
			Fields: []StepField{
				sf("input.dpop.header",           "DPoP header name",        "Request header containing the proof JWT. Default: DPoP", "DPoP"),
				sf("input.dpop.max_age_seconds",  "Max age (seconds)",       "Maximum age of the iat claim. Default: 60", "60"),
				sf("input.dpop.match_query",       "Match query string",      "Include query string in htu comparison (true/false). Default: false", "false"),
				sf("input.dpop.require_ath",       "Require ath claim",       "Require ath = SHA256(access_token) claim. Set true when access_token_var is provided. Default: false", "false"),
				sf("input.dpop.check_cnf_jkt",    "Check cnf.jkt",          "Verify access token cnf.jkt matches proof key thumbprint (RFC 9449 §7). Requires access_token_var. Default: false", "false"),
				sf("input.dpop.on_failure",        "On failure",              `"stop" (return error) or "continue" (write result_var and proceed)`, "stop"),
				sf("input.dpop.failure_status",    "Failure status",          "HTTP status on failure. Default: 401", "401"),
				sf("input.dpop.failure_body",      "Failure body",            "Response body on failure", "invalid dpop proof"),
				sf("input.dpop.access_token_var",  "Access token variable",   "Variable holding the raw access token (used for ath and cnf.jkt checks)", ""),
				sf("input.dpop.result_var",        "Result variable",         "Variable to write 'true'/'false' into in continue mode", ""),
				sf("input.dpop.cnf_jkt_var",       "JWK thumbprint variable", "Variable to write the verified JWK SHA-256 thumbprint into on success", ""),
			},
		},

		// ── Token Introspection (RFC 7662) ────────────────────────────────────────
		{
			Type: "validate_token_introspection", Title: "Validate Token (Introspection)", Category: "auth", Capability: "introspection",
			Description: "Validate an opaque or JWT access token by calling an RFC 7662 introspection endpoint. Required for tokens that cannot be verified locally (e.g. opaque tokens from Keycloak, Okta). Responses are cached for cache_ttl_seconds to avoid hammering the auth server.",
			Defaults: map[string]string{
				"input.introspect.cache_ttl_seconds": "30",
				"input.introspect.on_failure":        "stop",
				"input.introspect.failure_status":    "401",
				"input.introspect.failure_body":      "unauthorized",
			},
			Fields: []StepField{
				sf("input.introspect.endpoint",          "Introspection endpoint",    "Full URL of the introspection endpoint (required)", "https://auth.example.com/oauth2/introspect"),
				sf("input.introspect.auth",              "Endpoint auth mode",        `"basic" (client_id+client_secret), "bearer" (bearer_token), or "none". Auto-detected from other fields if omitted.`, "basic"),
				sf("input.introspect.client_id",         "Client ID",                 "Gateway's client_id for Basic auth to the introspection endpoint", ""),
				sf("input.introspect.client_secret",     "Client secret",             "Gateway's client_secret for Basic auth (load via load_secret for security)", ""),
				sf("input.introspect.bearer_token",      "Bearer token (static)",     "Static bearer token for Bearer auth to the introspection endpoint", ""),
				sf("input.introspect.bearer_token_var",  "Bearer token (variable)",   "Variable holding a runtime bearer token for endpoint auth", ""),
				sf("input.introspect.cache_ttl_seconds", "Cache TTL (seconds)",       "How long to cache successful introspection responses. 0 = no caching. Default: 30", "30"),
				sf("input.introspect.required_scopes",   "Required scopes",           "Comma-separated scope values that must be present in the response", ""),
				sf("input.introspect.token_var",         "Token variable",            "Variable holding the access token to introspect", ""),
				sf("input.introspect.token_header",      "Token header",              "Request header to read the token from (e.g. Authorization). Bearer prefix is stripped.", "Authorization"),
				sf("input.introspect.on_failure",        "On failure",                `"stop" or "continue"`, "stop"),
				sf("input.introspect.failure_status",    "Failure status",            "HTTP status on failure. Default: 401", "401"),
				sf("input.introspect.failure_body",      "Failure body",              "Response body on failure. Default: unauthorized", "unauthorized"),
				sf("input.introspect.result_var",        "Result variable",           "Variable to write 'true'/'false' into in continue mode", ""),
				sf("input.introspect.claims_var",        "Claims output variable",    "Variable to write full introspection response JSON into on success", ""),
				sf("input.introspect.subject_var",       "Subject output variable",   "Variable to write the sub claim into on success", ""),
				sf("input.introspect.client_id_var",     "Client ID output variable", "Variable to write the client_id claim into on success", ""),
				sf("input.introspect.scopes_out_var",    "Scopes output variable",    "Variable to write space-separated scopes into on success", ""),
			},
		},

		// ── Token Revocation (jti blocklist) ──────────────────────────────────────
		{
			Type: "check_token_revoked", Title: "Check Token Revoked", Category: "auth", Capability: "revocation",
			Description: "Check whether a JWT's jti claim is in the revocation blocklist. Run after token_validation. Tokens are written to the blocklist by the management API when explicitly revoked. Fails closed on datastore errors.",
			Defaults: map[string]string{
				"input.revoke.datastore":      "default",
				"input.revoke.key_prefix":     "revoked:jti:",
				"input.revoke.on_failure":     "stop",
				"input.revoke.failure_status": "401",
				"input.revoke.failure_body":   "token revoked",
			},
			Fields: []StepField{
				sf("key_identifier",              "JTI variable",          "Variable holding the jti claim (write from token_validation's claims_var with an extract step, or use jwt.jti_var when available)", "var.token_jti"),
				sf("input.revoke.datastore",       "Datastore name",    "Named datastore for the blocklist. Default: default", "default"),
				sf("input.revoke.key_prefix",      "Key prefix",        "Blocklist key prefix. Default: revoked:jti:", "revoked:jti:"),
				sf("input.revoke.on_failure",      "On failure",        `"stop" (default) or "continue"`, "stop"),
				sf("input.revoke.failure_status",  "Failure status",    "HTTP status when token is revoked. Default: 401", "401"),
				sf("input.revoke.failure_body",    "Failure body",      "Response body when token is revoked. Default: token revoked", "token revoked"),
				sf("input.revoke.result_var",      "Result variable",   "Variable to write 'true' (not revoked) / 'false' (revoked) into in continue mode", ""),
			},
		},

		// ── Rate Limiting ─────────────────────────────────────────────────────────
		{
			Type: "assign_quota_group", Title: "Assign Quota Group", Category: "rate-limit", Capability: "quota",
			Description: "Map a runtime string value (e.g. tenant tier from metadata) to a quota group ID. The group ID selects a rate limit config in the following Check Rate Limit step. Must run before Check Rate Limit.",
			Defaults: map[string]string{"key_identifier": "tier_slot"},
			Fields: []StepField{
				sf("key_identifier", "Source variable", "Variable holding the tier/plan string (e.g. loaded via load_meta)", "tier_slot"),
				sf("input", "Group map (JSON)", `Map of tier name → group ID (1–255). E.g. {"free":"1","pro":"2","enterprise":"3"}`, `{"free":"1","pro":"2","enterprise":"3"}`),
			},
		},
		{
			Type: "check_rate_limit", Title: "Check Rate Limit", Category: "rate-limit", Capability: "throttle",
			Description: "Enforce a rate limit policy. Supports per-tenant, per-IP, per-variable, composite, static, and global counting strategies. Resolves the named config at bake time. Returns 429 when any window is exceeded.",
			Defaults: map[string]string{
				"input.count_by":  "tenant",
				"input.on_empty":  "fail",
				"input.fail_fast": "false",
			},
			Fields: []StepField{
				sf("input.config", "Config name", "Name of the rate limit config to enforce (required)", "my_rl_config"),
				sf("input.count_by", "Count by", `How to derive the counter key: "tenant" (default), "ip", "variable", "static", "composite", "global"`, "tenant"),
				sf("input.slot", "Variable name (count_by=variable)", "Variable name whose value is used as the counter key when count_by=variable", ""),
				sf("input.slots", "Variable names (count_by=composite)", "Comma-separated variable names concatenated as the counter key when count_by=composite", ""),
				sf("input.static_key", "Static key (count_by=static)", "Literal string baked at compile time as the counter key when count_by=static", ""),
				sf("input.xff_index", "XFF index (count_by=ip)", "Which X-Forwarded-For entry to use: 0=leftmost/true client (default), -1=rightmost/nearest proxy", "0"),
				sf("input.on_empty", "On empty key", `Policy when the key variable is empty: "fail" (deny, default), "skip" (pass through), "fallback_tenant" (use TenantID)`, "fail"),
				sf("input.fail_fast", "Fail fast", `"true" to stop on the first exceeded window; "false" (default) to check all windows`, "false"),
				sf("input.denied_label", "Denied branch label", "Optional flow label to jump to when the limit is exceeded (default: stop with 429)", ""),
			},
		},
		{
			Type: "check_rate_limit_v2", Title: "Check Rate Limit (V2)", Category: "rate-limit", Capability: "throttle",
			Description: "Enforce a named V2 rate limit config. Supports per-tenant, per-IP, per-slot, and global counting strategies with sliding or fixed windows. Returns 429 when any window is exceeded.",
			Defaults: map[string]string{
				"input.count_by": "tenant",
			},
			Fields: []StepField{
				sf("input.config", "Config name", "Name of the rate_limit_configs_v2 entry to enforce (required)", "my_rl_config"),
				sf("input.count_by", "Count by", `Counter key strategy: "tenant" (default), "ip", "slot", "global"`, "tenant"),
				sf("input.slot", "Variable name (count_by=slot)", "Variable name whose value is used as the counter key when count_by=slot", ""),
			},
		},
		{
			Type: "api_rate_limits", Title: "API Rate Limits", Category: "rate-limit", Capability: "throttle",
			Description: "Enforce the rate limits configured in the API definition at this position in the flow. If absent, limits are auto-injected at the start of the flow. No configuration needed — drag to control where enforcement happens.",
			Defaults: map[string]string{},
			Fields:   []StepField{},
		},
		{
			Type: "check_upstream_rate_limit", Title: "Check Upstream Rate Limit", Category: "rate-limit", Capability: "throttle",
			Description: "Enforce an upstream URL-pattern rate limit. Reads the upstream URL from the named variable, matches it against the UpstreamRegistry, and returns 429 if the rate limit is exceeded or the URL is blocked by a fail_closed policy.",
			Defaults: map[string]string{
				"input.url_slot": "upstream_url",
			},
			Fields: []StepField{
				sf("input.url_slot", "URL variable", "Variable name holding the upstream URL (e.g. loaded via load_service_url)", "upstream_url"),
				sf("input.denied_label", "Denied branch label", "Optional flow label to jump to when denied (default: stop with 429)", ""),
			},
		},

		// ── Resilience ────────────────────────────────────────────────────────────
		{
			Type: "spike_arrest", Title: "Spike Arrest", Category: "resilience", Capability: "gate",
			Description: "Smooth inbound traffic by allowing at most one request per interval_ms per (flow, tenant [, key]) bucket. Excess requests receive 429 immediately.",
			Defaults: map[string]string{"input.interval_ms": "100"},
			Fields: []StepField{
				sf("input.interval_ms", "Interval (ms)", "Minimum milliseconds between allowed requests per bucket. Default: 100 (10 req/s).", "100"),
				sf("source", "Key variable (optional)", "Variable name whose value is added to the bucket key for per-user/per-key throttling. Omit for per-tenant-only bucketing.", ""),
			},
		},
		{
			Type: "circuit_breaker", Title: "Circuit Breaker", Category: "resilience", Capability: "gate",
			Description: "Open the circuit after failure_threshold consecutive failures; return 503 while open (or jump to fallback_flow if specified). Probe recovery after open_duration_ms via half-open state.",
			Defaults: map[string]string{"input.failure_threshold": "5", "input.success_threshold": "2", "input.open_duration_ms": "30000"},
			Fields: []StepField{
				sf("input.failure_threshold", "Failure threshold", "Consecutive failures required to open the circuit. Default: 5.", "5"),
				sf("input.success_threshold", "Success threshold", "Consecutive successes in half-open state required to close the circuit. Default: 2.", "2"),
				sf("input.open_duration_ms", "Open duration (ms)", "How long the circuit stays open before attempting a probe request. Default: 30000 (30 s).", "30000"),
				sf("input.fallback_flow", "Fallback flow (optional)", "Name of a compiled fragment/flow to jump to when the circuit is open. Omit to return a bare 503.", ""),
			},
		},
		{
			Type: "record_circuit_outcome", Title: "Record Circuit Outcome", Category: "resilience", Capability: "action",
			Description: "Record the success or failure of the guarded work for the most-recently compiled circuit_breaker. Must be placed after the protected step(s).",
			Defaults: map[string]string{},
			Fields: []StepField{
				sf("condition", "Success condition (optional)", "Boolean expression evaluated at runtime. True = success, false = failure. Omit to always record success.", "status < 500"),
			},
		},

		// ── Network ───────────────────────────────────────────────────────────────
		{
			Type: "bind_body", Title: "Read Body Field", Category: "request", Capability: "request",
			Description: "Extract a field from the request body using a gjson path expression and store it in a variable.",
			Defaults: map[string]string{"key": "", "as": "body_field"},
			Fields: []StepField{
				sf("key", "gjson path", "gjson path expression to extract from the request body (e.g. result.tenant.plan)", "result.id"),
				sf("as", "Store into variable", "Variable name to write the extracted value into", "body_field"),
			},
		},
		{
			Type: "bind_header", Title: "Read Header", Category: "request", Capability: "request",
			Description: "Extract an HTTP request header value into a variable.",
			Defaults: map[string]string{"key": "", "as": "auth_header"},
			Fields: []StepField{
				sf("key", "Header Name", "Name of the HTTP header e.g. Authorization", "Authorization"),
				sf("as", "Store into variable", "Variable name to write value into", "auth_header"),
			},
		},
		{
			Type: "bind_query_param", Title: "Read Query Param", Category: "request", Capability: "request",
			Description: "Extract a URL query parameter value into a variable.",
			Defaults: map[string]string{"key": "", "as": "query_param"},
			Fields: []StepField{
				sf("key", "Param Name", "Query parameter name e.g. api_key", "api_key"),
				sf("as", "Store into variable", "Variable name to write value into", "query_param"),
			},
		},
		{
			Type: "bind_path", Title: "Read Path Param", Category: "request", Capability: "request",
			Description: "Extract a path parameter (e.g. {id}) into a variable by index.",
			Defaults: map[string]string{"index": "0", "as": "path_id"},
			Fields: []StepField{
				sf("index", "Param Index", "0-based index of the path parameter", "0"),
				sf("as", "Store into variable", "Variable name to write value into", "path_id"),
			},
		},
		{
			Type: "bind_client_ip", Title: "Bind Client IP", Category: "network", Capability: "identity",
			Description: "Extract the real client IP address and store it in a variable. Resolution order: X-Forwarded-For (entry selected by xff_index) → X-Real-IP → TCP RemoteAddr.",
			Defaults: map[string]string{"as": "client_ip", "input": `{"xff_index":"0"}`},
			Fields: []StepField{
				sf("as", "Variable name", "Variable to store the client IP string in (e.g. client_ip)", "client_ip"),
				sf("input", "XFF Index (JSON)", `{"xff_index":"0"} — which X-Forwarded-For entry to use: 0=first/leftmost (original client), -1=last/rightmost (nearest proxy)`, `{"xff_index":"0"}`),
			},
		},
		{
			Type: "set_request_header", Title: "Set Upstream Header", Category: "request", Capability: "request",
			Description: "Inject a header into the upstream request before proxying. The header name is static; the value is read from a variable at runtime.",
			Defaults: map[string]string{"key": "Cookie", "source": "var.cookie_val"},
			Fields: []StepField{
				sf("key", "Header Name", "Static header name to inject into the upstream request (e.g. Cookie, X-Auth-Token)", "Cookie"),
				sf("source", "Value variable", "Variable whose value is used as the header value", "var.cookie_val"),
			},
		},
		{
			Type: "remove_request_header", Title: "Remove Upstream Header", Category: "request", Capability: "request",
			Description: "Remove a header from the upstream request before proxying. The header name is static and baked at compile time.",
			Defaults: map[string]string{"key": "Cookie"},
			Fields: []StepField{
				sf("key", "Header Name", "Static header name to remove from the upstream request (e.g. Cookie, X-Auth-Token)", "Cookie"),
			},
		},
		{
			Type: "rename_request_header", Title: "Rename Upstream Header", Category: "request", Capability: "request",
			Description: "Rename a header in the upstream request: copy the source header to a new name and remove the original. Both names are static and baked at compile time.",
			Defaults: map[string]string{"key": "Authorization", "as": "X-Auth-Token"},
			Fields: []StepField{
				sf("key", "Source header", "Static header name to read from the incoming request", "Authorization"),
				sf("as", "Destination header", "Static header name to set on the upstream request", "X-Auth-Token"),
			},
		},

		// ── HTTP Utilities ────────────────────────────────────────────────────────
		{
			Type: "set_request_body", Title: "Set Request Body", Category: "http", Capability: "mutate",
			Description: "Stage a request body for the next http_call step. The body bytes come from a variable; the Content-Type is baked at compile time.",
			Defaults: map[string]string{"source": "var.body", "input.content_type": "application/json"},
			Fields: []StepField{
				sf("source", "Body variable", "Variable containing the request body bytes to send", "var.body"),
				sf("input.content_type", "Content-Type", "MIME type of the body (default: application/json)", "application/json"),
			},
		},
		{
			Type: "bind_request_url", Title: "Bind Request URL", Category: "http", Capability: "extract",
			Description: "Capture the incoming request URL (path + optional query string) into a variable.",
			Defaults: map[string]string{"as": "request_url"},
			Fields: []StepField{
				sf("as", "Store as", "Variable to write the URL into", "request_url"),
				sf("include_query", "Include query string", "true (default) to append ?query, false for path only", "true"),
			},
		},
		{
			Type: "copy_header", Title: "Copy Header", Category: "http", Capability: "mutate",
			Description: "Read an incoming request header and forward it to the upstream under a (possibly different) key. Both names are baked at compile time.",
			Defaults: map[string]string{},
			Fields: []StepField{
				sf("key", "Source header", "Incoming request header name to read", "X-Request-Id"),
				sf("as", "Destination header", "Header name to set on the upstream request", "X-Correlation-Id"),
			},
		},

		{
			Type: "ip_restriction", Title: "IP Restriction", Category: "network", Capability: "access-control",
			Description: "Allow or deny requests based on CIDR ranges. Returns configured status/body when blocked.",
			Defaults: map[string]string{"input": `{"mode":"allow","cidrs":"10.0.0.0/8","source":"header.X-Forwarded-For","on_violation_status":"403","on_violation_body":"ip not allowed"}`},
			Fields: []StepField{
				sf("key_identifier", "IP variable (optional)", "Optional variable with pre-resolved client IP (from bind_client_ip). If set, it overrides source resolution.", "client_ip"),
				sf("input", "Config (JSON)", `mode: allow|deny, cidrs: comma-separated CIDRs, source: header.X-Forwarded-For|header.X-Real-IP|remote_addr, on_violation_status: 4xx/5xx, on_violation_body: response text`, `{"mode":"allow","cidrs":"10.0.0.0/8,192.168.0.0/16","source":"header.X-Forwarded-For","on_violation_status":"403","on_violation_body":"ip not allowed"}`),
			},
		},

		{
			Type: "cors", Title: "CORS", Category: "network", Capability: "cors",
			Description: "Handle Cross-Origin Resource Sharing. Automatically responds to OPTIONS preflight requests with 204 and sets Access-Control headers on all matching requests. Zero allocations: all config is pre-baked at deploy time.",
			Defaults: map[string]string{
				"input.origins":        "*",
				"input.methods":        "GET, POST, PUT, DELETE, PATCH, OPTIONS",
				"input.headers":        "Content-Type, Authorization",
				"input.expose_headers": "",
				"input.max_age":        "86400",
				"input.credentials":    "false",
			},
			Fields: []StepField{
				sf("input.origins", "Allowed origins", `Comma-separated list of allowed origins, or * for wildcard. Examples: "https://app.example.com,https://staging.example.com" or "*"`, "*"),
				sf("input.methods", "Allowed methods", "Comma-separated HTTP methods to allow in cross-origin requests", "GET, POST, PUT, DELETE, PATCH, OPTIONS"),
				sf("input.headers", "Allowed headers", "Comma-separated request headers the browser is allowed to send", "Content-Type, Authorization"),
				sf("input.expose_headers", "Expose headers", "Comma-separated response headers to expose to JavaScript (leave empty to expose none)", ""),
				sf("input.max_age", "Max age (seconds)", "How long browsers may cache the preflight result (Access-Control-Max-Age)", "86400"),
				sf("input.credentials", "Allow credentials", `Set to "true" to allow cookies and credentials (requires specific origin, not *)`, "false"),
			},
		},

		// ── AI / LLM ─────────────────────────────────────────────────────────────
		{
			Type: "llm_call", Title: "LLM Call", Category: "ai", Capability: "inference",
			Description: "Send a prompt to a configured LLM (Anthropic, OpenAI, Gemini, Ollama) and store the text response in a variable. Returns 413 if the prompt exceeds the model's context limit.",
			Defaults: map[string]string{
				"key_identifier": "var.prompt",
				"as":             "llm_response",
				"input":          `{"model":"claude-sonnet-4-6","temperature":"0.7","max_tokens":"2000"}`,
			},
			Fields: []StepField{
				sf("key_identifier", "Prompt variable", "Variable containing the user prompt text", "var.prompt"),
				sf("as", "Store as", "Variable to write the LLM text response into", "llm_response"),
				sf("input", "Config (JSON)", `Keys: model (catalog alias), temperature, max_tokens, timeout_ms, system_variable, api_key, fallback_model (single alias), fallback_chain (comma-sep aliases), fallback_variable (dynamic prioritization), history_variable (stateful conversion), model_config_variable (runtime JSON model config), messages_variable (multi-turn: JSON []CanonicalMessage from parse_message_format), stop_reason_variable, input_tokens_variable, output_tokens_variable`, `{"model":"claude-sonnet-4-6","temperature":"0.7","max_tokens":"2000","history_variable":"var.history"}`),
			},
		},

		// ── HTTP ─────────────────────────────────────────────────────────────────
		{
			Type:        "http_call",
			Title:       "HTTP Call",
			Category:    "http",
			Capability:  "upstream",
			Description: "Make an outbound HTTP request. Captures response body, status, and headers into slots. Supports dynamic URL/body/content-type, MutationLog application, header forwarding, and condition-based retries.",
			Defaults:    map[string]string{"url": "https://example.com/api", "timeout": "5000"},
			Fields: []StepField{
				sf("url", "URL", "Static upstream URL. Leave blank when using url_var.", "https://example.com/api"),
				sf("url_var", "URL variable", "Variable holding a dynamic URL (e.g. from load_service_url). Takes precedence over url.", "upstream_url"),
				sf("method", "HTTP method", "HTTP method: GET, POST, PUT, PATCH, DELETE. Defaults to GET.", "GET"),
				sf("body_var", "Body variable", "Variable whose bytes are sent as the request body. Uses StagedRequestBody (set_request_body) when present.", "var.body"),
				sf("content_type", "Content-Type", "Static Content-Type header for the request body (e.g. application/json).", "application/json"),
				sf("response_body_var", "Response body variable", "Variable to store the response body bytes in. If omitted the body is discarded.", "var.resp_body"),
				sf("response_status_var", "Response status variable", "Variable name to store the HTTP response status code (int64). If omitted the status is not stored.", "var.resp_status"),
				sf("response_header_vars", "Response header variables (JSON)", `JSON object mapping header name → variable name. e.g. {"X-Request-Id":"var.req_id"}`, `{"X-Request-Id":"var.req_id"}`),
				sf("forward_incoming_headers", "Forward incoming headers", "When true, all non-hop-by-hop incoming request headers are forwarded upstream before applying block_headers.", "false"),
				sf("forward_response_headers", "Forward response headers", "When true, all non-hop-by-hop upstream response headers are forwarded to the client, excluding any in block_headers.", "false"),
				sf("block_headers", "Block headers (JSON array)", `JSON array of header names to suppress from the upstream request. e.g. ["Authorization","Cookie"]`, `["Authorization"]`),
				sf("timeout", "Timeout (ms)", "Max wait in milliseconds before the request is aborted.", "5000"),
				sf("retry_condition", "Retry condition", "Boolean expression evaluated after each attempt; retried when true (e.g. status >= 500).", "status >= 500"),
				sf("max_retries", "Max retries", "Maximum retry attempts (0 = no retries).", "3"),
				sf("service_code", "Service Code", "Egress profile resolved by service code (e.g. 'payment-service'). Matches code rules in the Egress configuration.", ""),
				sf("profile", "Egress Profile", "Explicit egress profile name. Overrides service_code matching. Profile must exist in the Egress configuration.", ""),
			},
		},

		// ── gRPC ─────────────────────────────────────────────────────────────────
		{
			Type:        "grpc_call",
			Title:       "gRPC Call",
			Category:    "grpc",
			Capability:  "upstream",
			Description: "Make an outbound gRPC unary call. Transcodes JSON ↔ proto using a pre-uploaded FileDescriptorSet. Supports TLS (grpcs://), metadata forwarding, deadline propagation, and automatic client-disconnect cancellation.",
			Defaults: map[string]string{
				"timeout_ms":    "5000",
				"wait_for_ready": "false",
			},
			Fields: []StepField{
				sf("descriptor_set", "Descriptor Set", "Name of the uploaded FileDescriptorSet (from the gRPC Descriptors library).", "user-service"),
				sf("service", "Service name", "Fully-qualified proto service name (e.g. com.example.UserService).", "com.example.UserService"),
				sf("method", "Method name", "RPC method name (e.g. GetUser).", "GetUser"),
				sf("static_url", "Static URL", "grpc://host:port (insecure) or grpcs://host:port (TLS). Leave blank when using url_variable.", "grpc://user-service:9090"),
				sf("url_slot", "URL variable", "Variable holding a dynamic grpc:// or grpcs:// URL (from load_service_url or earlier step).", "0"),
				sf("body_slot", "Request body variable", "Variable holding JSON request payload. Empty variable sends an empty proto message.", "1"),
				sf("response_slot", "Response body variable", "Variable to store the JSON response body. On error, stores {code, message, details}.", "2"),
				sf("status_slot", "Status variable", "Variable to store the HTTP status code as a string (e.g. '200', '404').", "3"),
				sf("timeout_ms", "Timeout (ms)", "Per-call deadline in milliseconds. 0 = no deadline (not recommended).", "5000"),
				sf("compress", "Compress request", "Send request with gzip compression (true/false).", "false"),
				sf("wait_for_ready", "Wait for ready", "Block until connection is ready instead of failing immediately (true/false).", "false"),
				sf("max_retries", "Max retries", "Retry attempts on codes listed in retry_on. 0 = no retry.", "0"),
				sf("retry_on", "Retry on codes", "Comma-separated gRPC status code names to retry on (e.g. UNAVAILABLE,UNKNOWN).", "UNAVAILABLE"),
				sf("forward_headers", "Forward headers", "Forward incoming HTTP headers as gRPC metadata (true/false).", "false"),
				sf("block_headers", "Block headers", "Comma-separated header names to exclude from metadata forwarding.", "authorization"),
				sf("egress_profile", "Egress Profile", "Egress profile name for TLS config and keepalive. Uses URL scheme by default.", ""),
			},
		},

		// ── Observability / Tracing ───────────────────────────────────────────────
		{
			Type: "store_internal_tx_id", Title: "Store Transaction ID", Category: "observability", Capability: "tracing",
			Description: "Save the gateway-assigned internal transaction ID (InternalTxID) into a variable for use in downstream headers or logs.",
			Defaults: map[string]string{"as": "tx_id"},
			Fields: []StepField{
				sf("as", "Store as", "Variable name to save the formatted transaction ID string into", "tx_id"),
			},
		},
		{
			Type: "bind_correlation_id", Title: "Bind Correlation ID", Category: "observability", Capability: "tracing",
			Description: "Read a correlation ID from an incoming header; optionally generate one if the header is absent.",
			Defaults: map[string]string{"key": "X-Correlation-ID", "as": "corr_id", "generate_if_missing": "true"},
			Fields: []StepField{
				sf("key", "Header name", "Incoming request header to read the correlation ID from", "X-Correlation-ID"),
				sf("as", "Store as", "Variable name to save the correlation ID into", "corr_id"),
				sf("generate_if_missing", "Generate if missing", "Set to true to generate a new ID when the header is absent (true/false)", "true"),
			},
		},
		{
			Type: "log_field", Title: "Log Custom Field", Category: "observability", Capability: "logging",
			Description: "Write a variable value as a named field in the request access log Extra section.",
			Defaults: map[string]string{"key": "client_id", "source": "var.client_id"},
			Fields: []StepField{
				sf("key", "Field name", "Name of the field as it appears in the access log Extra section (e.g. client_id, tenant_alias)", "client_id"),
				sf("source", "Source variable", "Variable whose value is written to the access log", "var.client_id"),
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
			Type: "pattern_match", Title: "Pattern Match", Category: "control", Capability: "branching", SupportsNested: true,
			Description: "Match a variable value against a regex pattern compiled at deploy time. Branches to then-flow on match or else-flow on no-match. Regex is compiled once at bake time — runtime cost is a single Match call with zero allocations.",
			Defaults: map[string]string{
				"source":         "header.x-service",
				"input.pattern":  "^(api|data).*",
				"input.flags":    "",
				"then":           "",
				"else":           "",
			},
			Fields: []StepField{
				sf("source", "Source variable", "Variable whose value is tested — e.g. a header, query param, or any earlier-bound variable", "header.x-service"),
				sf("input.pattern", "Regex pattern", "Go regular-expression pattern (RE2 syntax). Compiled once at deploy time.", "^(api|data).*"),
				sf("input.flags", "Regex flags", "Optional inline flags: i (case-insensitive), m (multiline), s (dot-all), x (verbose). Combine freely e.g. \"im\".", ""),
				sf("then", "Match → flow", "Flow name to execute when the pattern matches", ""),
				sf("else", "No-match → flow", "Flow name to execute when the pattern does not match (optional)", ""),
			},
		},
		{
			Type:           "validate_pattern",
			Category:       "string",
			Capability:     "match",
			SupportsNested: false,
		},
		{
			Type:           "extract_pattern",
			Category:       "string",
			Capability:     "extract",
			SupportsNested: false,
		},
		{
			Type: "switch", Title: "Switch", Category: "control", Capability: "multi-branch", SupportsNested: true,
			Description: "Route to one of several sub-flows based on the string value of a variable.",
			Defaults: map[string]string{"as": "", "cases": ""},
			Fields: []StepField{
				sf("as", "Match expression", "Variable or request source to match on (e.g. header.X-Plan, query.mode)", "header.X-Plan"),
				sf("cases", "Cases", "Comma-separated key=flow pairs: free=free_flow,premium=premium_flow", "free=free_flow,premium=premium_flow"),
			},
		},
		{
			Type: "foreach", Title: "For Each", Category: "control", Capability: "iteration", SupportsNested: true,
			Description: "Iterate over a list in a variable and execute a sub-flow once per item.",
			Defaults: map[string]string{"source": "", "as": "item", "do": ""},
			Fields: []StepField{
				sf("source", "Source list variable", "Variable containing the array to iterate over", "var.items"),
				sf("as", "Item variable", "Variable name bound to the current item inside the sub-flow", "item"),
				sf("do", "Sub-flow", "Flow name called for each item in the list", "process_item"),
			},
		},
		{
			Type: "foreach_header", Title: "For Each Header", Category: "control", Capability: "iteration", SupportsNested: true,
			Description: "Iterate over all HTTP request headers and execute a sub-flow once per header.",
			Defaults: map[string]string{"as": "header_name", "value_as": "header_value", "do": ""},
			Fields: []StepField{
				sf("as", "Header name variable", "Variable name bound to the current header name inside the sub-flow", "header_name"),
				sf("value_as", "Header value variable", "Variable name bound to the current header value inside the sub-flow", "header_value"),
				sf("do", "Sub-flow", "Flow name called for each header", "process_header"),
			},
		},
		{
			Type: "foreach_param", Title: "For Each Query Parameter", Category: "control", Capability: "iteration", SupportsNested: true,
			Description: "Iterate over all URL query parameters and execute a sub-flow once per parameter.",
			Defaults: map[string]string{"as": "param_name", "value_as": "param_value", "do": ""},
			Fields: []StepField{
				sf("as", "Parameter name variable", "Variable name bound to the current parameter name inside the sub-flow", "param_name"),
				sf("value_as", "Parameter value variable", "Variable name bound to the current parameter value inside the sub-flow", "param_value"),
				sf("do", "Sub-flow", "Flow name called for each parameter", "process_param"),
			},
		},
		{
			Type: "foreach_cookie", Title: "For Each Cookie", Category: "control", Capability: "iteration", SupportsNested: true,
			Description: "Iterate over all HTTP request cookies and execute a sub-flow once per cookie.",
			Defaults: map[string]string{"as": "cookie_name", "value_as": "cookie_value", "do": ""},
			Fields: []StepField{
				sf("as", "Cookie name variable", "Variable name bound to the current cookie name inside the sub-flow", "cookie_name"),
				sf("value_as", "Cookie value variable", "Variable name bound to the current cookie value inside the sub-flow", "cookie_value"),
				sf("do", "Sub-flow", "Flow name called for each cookie", "process_cookie"),
			},
		},
		{
			Type: "parallel", Title: "Parallel", Category: "control", Capability: "flow",
			Description: "Execute multiple branches concurrently. All branches run in parallel; the step waits for all to finish (or timeout). Use error_policy: fail_fast to stop early on the first failure.",
			Defaults: map[string]string{"timeout_ms": "3000", "error_policy": "continue"},
			Fields: []StepField{
				sf("timeout_ms", "Timeout (ms)", "Maximum time in milliseconds to wait for all branches (default: 3000)", "3000"),
				sf("error_policy", "Error policy", `"continue" (default): proceed even if a branch fails. "fail_fast": cancel remaining branches on first failure.`, "continue"),
				sf("branches", "Branches", "List of named branches, each with an inline flow definition", ""),
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
			Description: "Convert a string variable to lower-case.",
			Defaults: map[string]string{"source": "var.input", "as": "lower_val"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to convert", "var.input"),
				sf("as", "Store as", "Variable to save the lower-cased result into", "lower_val"),
			},
		},
		{
			Type: "to_upper", Title: "To Upper", Category: "string", Capability: "string-op",
			Description: "Convert a string variable to upper-case.",
			Defaults: map[string]string{"source": "var.input", "as": "upper_val"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to convert", "var.input"),
				sf("as", "Store as", "Variable to save the upper-cased result into", "upper_val"),
			},
		},
		{
			Type: "substring", Title: "Substring", Category: "string", Capability: "string-op",
			Description: "Slice a string variable by byte offset. Provide start and/or length in the input map.",
			Defaults: map[string]string{"source": "var.input", "as": "sliced"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to slice", "var.input"),
				sf("as", "Store as", "Variable to save the substring into", "sliced"),
			},
		},
		{
			Type: "to_int", Title: "To Int", Category: "string", Capability: "type-convert",
			Description: "Parse a string variable as a 64-bit integer and store in an integer variable.",
			Defaults: map[string]string{"source": "var.str_val", "as": "int_val"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to parse", "var.str_val"),
				sf("as", "Store as", "Variable to save the integer into", "int_val"),
			},
		},
		{
			Type: "byte_length", Title: "String Length", Category: "string", Capability: "type-convert",
			Description: "Write the byte-length of a variable value into an integer variable.",
			Defaults: map[string]string{"source": "var.str_val", "as": "int_len"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable whose byte-length to measure", "var.str_val"),
				sf("as", "Integer destination variable", "Variable to write the length into", "int_len"),
			},
		},

		{
			Type: "trim", Title: "Trim Whitespace", Category: "string", Capability: "string-op",
			Description: "Remove leading and trailing whitespace from a string variable. Zero-copy — the result is a sub-slice of the source.",
			Defaults: map[string]string{"source": "var.input", "as": "trimmed"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to trim", "var.input"),
				sf("as", "Store as", "Variable to save the trimmed value into", "trimmed"),
			},
		},
		{
			Type: "contains", Title: "Contains", Category: "string", Capability: "string-op",
			Description: "Check whether a string variable contains a fixed substring. Writes true/false to a boolean variable.",
			Defaults: map[string]string{"source": "var.input", "value": "needle", "as": "found"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to search", "var.input"),
				sf("value", "Needle (static)", "Fixed substring to search for (baked at compile time)", "needle"),
				sf("as", "Boolean result variable", "Variable to write true/false into", "found"),
			},
		},
		{
			Type: "starts_with", Title: "Starts With", Category: "string", Capability: "string-op",
			Description: "Check whether a string variable starts with a fixed prefix. Writes true/false to a boolean variable.",
			Defaults: map[string]string{"source": "var.input", "value": "prefix", "as": "matched"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to check", "var.input"),
				sf("value", "Prefix (static)", "Fixed prefix to test for (baked at compile time)", "prefix"),
				sf("as", "Boolean result variable", "Variable to write true/false into", "matched"),
			},
		},
		{
			Type: "ends_with", Title: "Ends With", Category: "string", Capability: "string-op",
			Description: "Check whether a string variable ends with a fixed suffix. Writes true/false to a boolean variable.",
			Defaults: map[string]string{"source": "var.input", "value": "suffix", "as": "matched"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to check", "var.input"),
				sf("value", "Suffix (static)", "Fixed suffix to test for (baked at compile time)", "suffix"),
				sf("as", "Boolean result variable", "Variable to write true/false into", "matched"),
			},
		},
		{
			Type: "replace", Title: "Replace", Category: "string", Capability: "string-op",
			Description: "Replace all occurrences of a static substring with another static string. One allocation for the output.",
			Defaults: map[string]string{"source": "var.input", "as": "replaced"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to modify", "var.input"),
				sf("input.old", "Find (static)", "Substring to replace (baked at compile time)", "old_value"),
				sf("input.new", "Replace with (static)", "Replacement string (baked at compile time)", "new_value"),
				sf("as", "Store as", "Variable to save the result into", "replaced"),
			},
		},
		{
			Type: "split", Title: "Split", Category: "string", Capability: "string-op",
			Description: "Split a string variable by a static separator and store the result as a JSON array into another variable. Use foreach to iterate the result.",
			Defaults: map[string]string{"source": "var.input", "value": ",", "as": "parts"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to split", "var.input"),
				sf("value", "Separator (static)", "Delimiter to split on (default: comma)", ","),
				sf("as", "Store as", "Variable to save the JSON array into", "parts"),
			},
		},
		{
			Type: "index_of", Title: "Index Of", Category: "string", Capability: "string-op",
			Description: "Find the byte offset of a static substring in a string variable. Writes -1 if not found.",
			Defaults: map[string]string{"source": "var.input", "value": "needle", "as": "pos"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to search", "var.input"),
				sf("value", "Needle (static)", "Fixed substring to find (baked at compile time)", "needle"),
				sf("as", "Integer result variable", "Variable to write the byte offset into (−1 if not found)", "pos"),
			},
		},

		{
			Type: "set_const", Title: "Set Literal Value", Category: "string", Capability: "data",
			Description: "Write a static literal string into a named variable. Use this to define a fixed response body, header value, or any constant before passing it to another step.",
			Defaults: map[string]string{"value": `{"status":"ok"}`, "as": "var.result"},
			Fields: []StepField{
				sf("value", "Literal value", "The static string to store (plain text, JSON, etc.)", `{"status":"ok"}`),
				sf("as", "Variable name", "Variable name that later steps can reference", "var.result"),
			},
		},
		{
			Type: "render_template", Title: "Render Template", Category: "string", Capability: "string-op",
			Description: "Build a string by interpolating variable values into a template. Use ${varname} to insert a variable; everything else is copied literally. The template is compiled once at deploy time — runtime cost is a single allocation. All referenced variables must be bound by earlier steps.",
			Defaults: map[string]string{
				"value": `{"userId":"${user_id}","plan":"${plan}"}`,
				"as":    "response_body",
			},
			Fields: []StepField{
				sf("value", "Template string", `Static template with \${varname} placeholders. Example: {"id":"\${user_id}","ts":"\${timestamp}"}`, `{"userId":"${user_id}"}`),
				sf("as", "Store result as", "Variable to write the rendered string into", "response_body"),
			},
		},

		// ── Encoding ─────────────────────────────────────────────────────────────
		{
			Type: "base64_encode", Title: "Base64 Encode", Category: "encoding", Capability: "encoding",
			Description: "Encode a byte variable to base64. Set input.encoding to 'std' (default), 'url', 'raw_url', or 'raw_std'.",
			Defaults: map[string]string{"source": "var.input", "as": "encoded"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing bytes to encode", "var.input"),
				sf("as", "Store as", "Variable for the base64 output", "encoded"),
				sf("input.encoding", "Encoding variant", "std | url | raw_url | raw_std (default: std)", "std"),
			},
		},
		{
			Type: "base64_decode", Title: "Base64 Decode", Category: "encoding", Capability: "encoding",
			Description: "Decode a base64 string variable into raw bytes. Clears the result variable on invalid input. Default encoding: raw_url (JWT-friendly).",
			Defaults: map[string]string{"source": "var.encoded", "as": "decoded"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the base64 string", "var.encoded"),
				sf("as", "Store as", "Variable for the decoded bytes", "decoded"),
				sf("input.encoding", "Encoding variant", "std | url | raw_url | raw_std (default: raw_url)", "raw_url"),
			},
		},
		{
			Type: "hex_encode", Title: "Hex Encode", Category: "encoding", Capability: "encoding",
			Description: "Encode a byte variable as a lowercase hexadecimal string.",
			Defaults: map[string]string{"source": "var.input", "as": "hex"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing bytes to encode", "var.input"),
				sf("as", "Store as", "Variable for the hex string", "hex"),
			},
		},
		{
			Type: "hex_decode", Title: "Hex Decode", Category: "encoding", Capability: "encoding",
			Description: "Decode a hex string variable into raw bytes. Clears the result variable on invalid input.",
			Defaults: map[string]string{"source": "var.hex", "as": "decoded"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the hex string", "var.hex"),
				sf("as", "Store as", "Variable for the decoded bytes", "decoded"),
			},
		},
		{
			Type: "url_encode", Title: "URL Encode", Category: "encoding", Capability: "encoding",
			Description: "Percent-encode a string variable (RFC 3986 unreserved characters pass through). Space is encoded as %20.",
			Defaults: map[string]string{"source": "var.input", "as": "encoded"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the string to encode", "var.input"),
				sf("as", "Store as", "Variable for the percent-encoded output", "encoded"),
			},
		},
		{
			Type: "url_decode", Title: "URL Decode", Category: "encoding", Capability: "encoding",
			Description: "Decode a percent-encoded string variable. '+' is decoded as space.",
			Defaults: map[string]string{"source": "var.encoded", "as": "decoded"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the percent-encoded string", "var.encoded"),
				sf("as", "Store as", "Variable for the decoded output", "decoded"),
			},
		},

		// ── Cookie ───────────────────────────────────────────────────────────────
		{
			Type: "extract_cookie", Title: "Extract Cookie", Category: "cookie", Capability: "extract",
			Description: "Extract a named cookie from the Cookie request header. Zero-copy — aliases header memory.",
			Defaults: map[string]string{"as": "cookie_val"},
			Fields: []StepField{
				sf("key", "Cookie name", "Name of the cookie to extract (baked at compile time)", "session_id"),
				sf("as", "Store as", "Variable to write the cookie value into", "cookie_val"),
			},
		},
		{
			Type: "set_response_cookie", Title: "Set Response Cookie", Category: "cookie", Capability: "mutate",
			Description: "Add a Set-Cookie response header. Cookie name and attributes are baked at compile time; value is read from a variable at runtime.",
			Defaults: map[string]string{"source": "var.session_id", "input.path": "/", "input.http_only": "true"},
			Fields: []StepField{
				sf("key", "Cookie name", "Name of the cookie to set", "session_id"),
				sf("source", "Value variable", "Variable containing the cookie value", "var.session_id"),
				sf("input.path", "Path", "Cookie path attribute (default: /)", "/"),
				sf("input.max_age", "Max-Age (seconds)", "0 = session cookie; negative = expire immediately", "0"),
				sf("input.http_only", "HttpOnly", "true to add HttpOnly flag", "true"),
				sf("input.secure", "Secure", "true to add Secure flag", "false"),
				sf("input.same_site", "SameSite", "Strict | Lax | None | (empty)", "Lax"),
			},
		},
		{
			Type: "set_request_cookie", Title: "Set Request Cookie", Category: "cookie", Capability: "mutate",
			Description: "Inject a cookie into the upstream request's Cookie header via the mutation log.",
			Defaults: map[string]string{"source": "var.cookie_val"},
			Fields: []StepField{
				sf("key", "Cookie name", "Name of the cookie to send upstream", "session_id"),
				sf("source", "Value variable", "Variable containing the cookie value", "var.cookie_val"),
			},
		},
		{
			Type: "remove_response_cookie", Title: "Remove Response Cookie", Category: "cookie", Capability: "mutate",
			Description: "Expire a client cookie by setting Max-Age=0. Cookie name and path are baked at compile time.",
			Defaults: map[string]string{"input.path": "/"},
			Fields: []StepField{
				sf("key", "Cookie name", "Name of the cookie to remove", "session_id"),
				sf("input.path", "Path", "Must match the original cookie path (default: /)", "/"),
			},
		},
		{
			Type: "cookie_flatten", Title: "Cookie Flatten", Category: "cookie", Capability: "transform",
			Description: "Build a Cookie header value string from multiple named variables, formatted as 'name=value; name2=value2; ...'. Empty variables are skipped.",
			Defaults: map[string]string{"as": "cookie_header", "input.session_id": "session_slot"},
			Fields: []StepField{
				sf("as", "Output variable", "Variable to write the flattened cookie header value", "cookie_header"),
				sf("input", "Cookie→Variable Mapping", "Map of cookie names to variable names (e.g. {\"session_id\":\"session_variable\",\"csrf\":\"csrf_variable\"})", ""),
			},
		},

		// ── Crypto / Hash ────────────────────────────────────────────────────────
		{
			Type: "hmac_sha256", Title: "HMAC-SHA256", Category: "crypto", Capability: "signing",
			Description: "Compute HMAC-SHA256 of a string variable using a bake-time secret key. Output is a lowercase hex string.",
			Defaults: map[string]string{"source": "var.payload", "as": "signature"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the data to sign", "var.payload"),
				sf("as", "Store as", "Variable for the HMAC hex string", "signature"),
				sf("input.key", "Secret key", "Static HMAC key (baked at compile time — use secrets manager for production keys)", ""),
			},
		},
		{
			Type: "hmac_sha1", Title: "HMAC-SHA1", Category: "crypto", Capability: "signing",
			Description: "Compute HMAC-SHA1 of a string variable using a bake-time key. Output is a lowercase hex string. Use only for legacy integrations.",
			Defaults: map[string]string{"source": "var.payload", "as": "signature"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the data to sign", "var.payload"),
				sf("as", "Store as", "Variable for the HMAC hex string", "signature"),
				sf("input.key", "Secret key", "Static HMAC key (baked at compile time)", ""),
			},
		},
		{
			Type: "sha256_hash", Title: "SHA-256 Hash", Category: "crypto", Capability: "hashing",
			Description: "Compute SHA-256 hash of a string variable. Output is a lowercase hex string. No secret key — use hmac_sha256 for signed hashes.",
			Defaults: map[string]string{"source": "var.input", "as": "digest"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the data to hash", "var.input"),
				sf("as", "Store as", "Variable for the SHA-256 hex string", "digest"),
			},
		},
		{
			Type: "md5_hash", Title: "MD5 Hash", Category: "crypto", Capability: "hashing",
			Description: "Compute MD5 hash of a string variable. Output is a lowercase hex string. MD5 is cryptographically broken — use only for checksums or legacy compatibility.",
			Defaults: map[string]string{"source": "var.input", "as": "digest"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the data to hash", "var.input"),
				sf("as", "Store as", "Variable for the MD5 hex string", "digest"),
			},
		},
		{
			Type: "aes_encrypt", Title: "AES Encrypt (GCM)", Category: "crypto", Capability: "encryption",
			Description: "Encrypt a variable with AES-GCM using a bake-time key (hex-encoded, 16/24/32 bytes). Output is nonce||ciphertext, written to a variable.",
			Defaults: map[string]string{"source": "var.plaintext", "as": "ciphertext"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing the plaintext bytes", "var.plaintext"),
				sf("as", "Store as", "Variable for the encrypted output (nonce prefix + ciphertext)", "ciphertext"),
				sf("input.key", "Key (hex)", "AES key as a hex string: 32 hex chars = AES-128, 64 = AES-256", ""),
			},
		},
		{
			Type: "aes_decrypt", Title: "AES Decrypt (GCM)", Category: "crypto", Capability: "encryption",
			Description: "Decrypt AES-GCM ciphertext (nonce||ciphertext) using a bake-time key. Sets Failed=true on authentication failure.",
			Defaults: map[string]string{"source": "var.ciphertext", "as": "plaintext"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing nonce||ciphertext bytes", "var.ciphertext"),
				sf("as", "Store as", "Variable for the decrypted plaintext", "plaintext"),
				sf("input.key", "Key (hex)", "Same AES key used during encryption", ""),
			},
		},
		{
			Type: "load_secret_var", Title: "Load Secret (Runtime Ref)", Category: "crypto", Capability: "secret",
			Description: "Resolves a secret reference stored in a variable at runtime. Use with load_meta to support per-tenant keys whose reference (e.g. 'env:TENANT_KEY' or 'gsm://...') is stored in the tenant registry. The secrets manager caches resolved values — no Vault/GSM call on every request.",
			Defaults: map[string]string{"source": "key_ref_slot", "as": "key_material"},
			Fields: []StepField{
				sf("source", "Reference variable", "Variable containing the secret reference string at runtime (e.g. 'gsm://...', 'env:MY_VAR')", "key_ref_slot"),
				sf("as", "Store as", "Variable to write the resolved secret bytes into", "key_material"),
			},
		},
		{
			Type: "aes_encrypt_slot_key", Title: "AES Encrypt (Runtime Key)", Category: "crypto", Capability: "encryption",
			Description: "Encrypt a variable with AES-256-GCM using a key loaded from another variable at runtime. Use with load_secret_var for per-tenant field-level encryption. Output format is identical to aes_encrypt.",
			Defaults: map[string]string{"source": "plaintext_slot", "as": "ciphertext_slot"},
			Fields: []StepField{
				sf("source", "Plaintext variable", "Variable containing the value to encrypt", "plaintext_slot"),
				sf("input.key_slot", "Key variable", "Variable holding the 32-byte AES-256 key (loaded via load_secret_var)", "key_material"),
				sf("as", "Output variable", "Variable to write the encrypted output into", "ciphertext_slot"),
			},
		},
		{
			Type: "aes_decrypt_slot_key", Title: "AES Decrypt (Runtime Key)", Category: "crypto", Capability: "encryption",
			Description: "Decrypt AES-256-GCM ciphertext using a key loaded from another variable at runtime. Counterpart to aes_encrypt_slot_key. Sets Failed=true on authentication failure.",
			Defaults: map[string]string{"source": "ciphertext_slot", "as": "plaintext_slot"},
			Fields: []StepField{
				sf("source", "Ciphertext variable", "Variable containing nonce||ciphertext bytes to decrypt", "ciphertext_slot"),
				sf("input.key_slot", "Key variable", "Variable holding the 32-byte AES-256 key", "key_material"),
				sf("as", "Output variable", "Variable to write the decrypted plaintext into", "plaintext_slot"),
			},
		},

		// ── Math ─────────────────────────────────────────────────────────────────
		{
			Type: "add", Title: "Add", Category: "math", Capability: "arithmetic",
			Description: "Add two numeric variables and store the result.",
			Defaults: map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Left operand", "Variable containing the first number", "var.a"),
				sf("source", "Right operand", "Variable containing the second number", "var.b"),
				sf("as", "Store as", "Variable to save the sum into", "result"),
			},
		},
		{
			Type: "sub", Title: "Subtract", Category: "math", Capability: "arithmetic",
			Description: "Subtract the right-operand variable from the left and store the result.",
			Defaults: map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Left operand", "Variable containing the number to subtract from", "var.a"),
				sf("source", "Right operand", "Variable containing the number to subtract", "var.b"),
				sf("as", "Store as", "Variable to save the difference into", "result"),
			},
		},
		{
			Type: "mul", Title: "Multiply", Category: "math", Capability: "arithmetic",
			Description: "Multiply two numeric variables and store the result.",
			Defaults: map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Left operand", "Variable containing the first factor", "var.a"),
				sf("source", "Right operand", "Variable containing the second factor", "var.b"),
				sf("as", "Store as", "Variable to save the product into", "result"),
			},
		},
		{
			Type: "div", Title: "Divide", Category: "math", Capability: "arithmetic",
			Description: "Divide the left-operand variable by the right and store the result.",
			Defaults: map[string]string{"key_identifier": "var.a", "source": "var.b", "as": "result"},
			Fields: []StepField{
				sf("key_identifier", "Dividend", "Variable containing the number to be divided", "var.a"),
				sf("source", "Divisor", "Variable containing the divisor", "var.b"),
				sf("as", "Store as", "Variable to save the quotient into", "result"),
			},
		},

		// ── Cache ─────────────────────────────────────────────────────────────────
		{
			Type: "cache_get", Title: "Cache Read · Per-Tenant", Category: "cache", Capability: "read",
			Description: "Read a cached value for this tenant. Each tenant has its own private cache — other tenants cannot see or affect this data. On a hit the value is saved to the output variable. On a miss the output variable is empty (if it was new) or keeps its previous value. Add an `if` step after this checking whether the output variable is non-empty to branch on hit vs miss.",
			Defaults: map[string]string{"key_identifier": "cache_key", "as": "cached_body"},
			Fields: []StepField{
				sf("key_identifier", "Cache key (variable)", "Variable whose value is used as the lookup key — e.g. a user ID or request path", "cache_key"),
				sf("as", "Output variable", "Variable to save the cached value into when there is a cache hit", "cached_body"),
			},
		},
		{
			Type: "cache_put", Title: "Cache Write · Per-Tenant", Category: "cache", Capability: "write",
			Description: "Store a value in this tenant's private cache. The entry expires after the TTL you set. Only this tenant can read back the value via Cache Read · Per-Tenant.",
			Defaults: map[string]string{"key_identifier": "cache_key", "source": "upstream_response", "ttl": "300"},
			Fields: []StepField{
				sf("key_identifier", "Cache key (variable)", "Variable whose value is used as the cache key — must match the key used on the read step", "cache_key"),
				sf("source", "Value to cache (variable)", "Variable holding the value you want to store — e.g. an upstream response body", "upstream_response"),
				sf("ttl", "Expires after (seconds)", "How long to keep the entry before it is automatically removed. 300 = 5 minutes, 3600 = 1 hour.", "300"),
			},
		},
		{
			Type: "cache_get_global", Title: "Cache Read · Shared", Category: "cache", Capability: "read",
			Description: "Read a value from the shared (global) cache — the same data is visible across all tenants. Use this for content that does not vary per tenant, such as public API responses or configuration payloads. On a hit the value is saved to the output variable. On a miss the output variable is empty (if it was new) or keeps its previous value. Add an `if` step after this checking whether the output variable is non-empty to branch on hit vs miss.",
			Defaults: map[string]string{"key_identifier": "cache_key", "as": "cached_body"},
			Fields: []StepField{
				sf("key_identifier", "Cache key (variable)", "Variable whose value is used as the lookup key", "cache_key"),
				sf("as", "Output variable", "Variable to save the cached value into when there is a cache hit", "cached_body"),
			},
		},
		{
			Type: "cache_put_global", Title: "Cache Write · Shared", Category: "cache", Capability: "write",
			Description: "Store a value in the shared (global) cache. The entry is readable by all tenants via Cache Read · Shared. Use only for data that is truly the same for every tenant.",
			Defaults: map[string]string{"key_identifier": "cache_key", "source": "upstream_response", "ttl": "300"},
			Fields: []StepField{
				sf("key_identifier", "Cache key (variable)", "Variable whose value is used as the cache key — must match the key used on the read step", "cache_key"),
				sf("source", "Value to cache (variable)", "Variable holding the value you want to store", "upstream_response"),
				sf("ttl", "Expires after (seconds)", "How long to keep the entry before it is automatically removed. 300 = 5 minutes, 3600 = 1 hour.", "300"),
			},
		},

		{
			Type: "cache_delete", Title: "Cache Invalidate · Per-Tenant", Category: "cache", Capability: "write",
			Description: "Remove a specific entry from this tenant's private cache immediately. Use this to force-expire a cached value before its TTL runs out — e.g. after a write that makes the cached response stale.",
			Defaults: map[string]string{"key_identifier": "cache_key"},
			Fields: []StepField{
				sf("key_identifier", "Cache key (variable)", "Variable whose value is used as the key to delete", "cache_key"),
			},
		},
		{
			Type: "cache_delete_global", Title: "Cache Invalidate · Shared", Category: "cache", Capability: "write",
			Description: "Remove a specific entry from the shared (global) cache immediately. Affects all tenants that read from Cache Read · Shared using the same key.",
			Defaults: map[string]string{"key_identifier": "cache_key"},
			Fields: []StepField{
				sf("key_identifier", "Cache key (variable)", "Variable whose value is used as the key to delete from the shared cache", "cache_key"),
			},
		},
		{
			Type: "cache_exists", Title: "Cache Exists", Category: "cache", Capability: "read",
			Description: "Check whether a key exists in L1 cache without fetching its value. Writes true/false to a boolean variable. ~2-3× faster than cache_get.",
			Defaults: map[string]string{"source": "var.cache_key", "as": "cache_hit"},
			Fields: []StepField{
				sf("source", "Key variable", "Variable containing the cache key to check", "var.cache_key"),
				sf("as", "Boolean result variable", "Variable to write true (hit) or false (miss) into", "cache_hit"),
			},
		},
		{
			Type: "cache_incr", Title: "Cache Increment", Category: "cache", Capability: "write",
			Description: "Atomically increment an int64 counter stored in the cache. Initialises to delta if the key is absent. Result is written to an integer variable.",
			Defaults: map[string]string{"source": "var.counter_key", "as": "counter_val"},
			Fields: []StepField{
				sf("source", "Key variable", "Variable containing the cache key for the counter", "var.counter_key"),
				sf("as", "Integer result variable", "Variable to write the updated counter value into", "counter_val"),
				sf("delta", "Delta", "Amount to add each call (default: 1; negative to decrement)", "1"),
				sf("ttl", "TTL (seconds)", "Expiry for a newly created counter; 0 = no expiry", "3600"),
			},
		},
		{
			Type: "cache_touch", Title: "Cache Touch", Category: "cache", Capability: "write",
			Description: "Refresh the TTL of an existing cache entry without reading or rewriting its value. No-op if the key does not exist.",
			Defaults: map[string]string{"source": "var.cache_key"},
			Fields: []StepField{
				sf("source", "Key variable", "Variable containing the cache key to touch", "var.cache_key"),
				sf("ttl", "New TTL (seconds)", "New expiry from now", "3600"),
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
			Type: "cache_get_batched", Title: "Cache Read (Batched)", Category: "cache", Capability: "read",
			Description: "Queue a cache lookup into the batch buffer — the result is not available until a Batch Flush step runs. Use this when you need multiple cache lookups and want to issue them all in one round-trip for efficiency.",
			Defaults: map[string]string{"variable": "cache_key", "destination": "cached_body"},
			Fields: []StepField{
				sf("variable", "Cache key (variable)", "Variable whose value is used as the lookup key", "cache_key"),
				sf("destination", "Output variable", "Variable to write the cached value into after the batch flush runs", "cached_body"),
			},
		},
		{
			Type: "json_extract_emit", Title: "JSON Extract & Emit", Category: "cache", Capability: "batch",
			Description: "Extract multiple fields from a JSON body in one scan and emit one storage op per field. Use params to configure each extraction (path, key_prefix, op_type, target, dest_variable/value_variable, async).",
			Defaults: map[string]string{"variable": "http_resp"},
			Fields: []StepField{
				sf("variable", "Body variable", "Variable holding the JSON body to extract from", "http_resp"),
				sf("params", "Extract ops (JSON array)", `Array of op descriptors. Each: {"path":"user.id","key_prefix":"user:","op_type":"put","target":"cache","value_variable":"","async":"true"}`, ""),
			},
		},
		{
			Type: "json_foreach_emit", Title: "JSON Foreach & Emit", Category: "cache", Capability: "batch",
			Description: "Iterate over a JSON array and emit one batch of storage ops per element. Handles unbounded arrays via auto-flush. Use params to configure per-element extractions.",
			Defaults: map[string]string{"variable": "http_resp", "path": "items"},
			Fields: []StepField{
				sf("variable", "Body variable", "Variable holding the JSON body containing the array", "http_resp"),
				sf("path", "Array path", "gjson path to the array within the body (e.g. items, data.services)", "items"),
				sf("params", "Extract ops (JSON array)", `Array of op descriptors applied to each element. Each: {"path":"id","key_prefix":"item:","op_type":"put","target":"cache","value_variable":"","async":"true"}`, ""),
			},
		},
		{
			Type: "json_set", Title: "JSON Set", Category: "json", Capability: "transform",
			Description: "Set a value at a JSON path in a variable. Uses gjson to locate the field and hand-rolled splicing to replace it.",
			Defaults: map[string]string{"source": "var.body", "as": "var.body", "key": "user.id"},
			Fields: []StepField{
				sf("source", "Source variable", "Variable with JSON input to mutate", "var.body"),
				sf("as", "Output variable", "Variable for JSON output (can be same as source)", "var.body"),
				sf("key", "JSON Path", "gjson path to the field to set (e.g. user.id, items.0.url)", "user.id"),
				sf("input.value_var", "Value variable", "Variable containing the value to set (optional; if absent, use value field)", ""),
				sf("value", "Static Value", "Static value to set (ignored if value_var is provided)", ""),
			},
		},

		// ── Response ─────────────────────────────────────────────────────────────
		{
			Type: "set_response_header", Title: "Set Response Header", Category: "response", Capability: "response-mod",
			Description: "Set a response header to the value from a variable.",
			Defaults: map[string]string{"key": "X-Custom-Header", "source": "var.header_value"},
			Fields: []StepField{
				sf("key", "Header name", "Name of the HTTP response header to set", "X-Request-ID"),
				sf("source", "Value variable", "Variable whose value is written to the header", "var.header_value"),
			},
		},
		{
			Type: "remove_response_header", Title: "Remove Response Header", Category: "response", Capability: "response-mod",
			Description: "Remove a response header by name.",
			Defaults: map[string]string{"key": "X-Custom-Header"},
			Fields: []StepField{
				sf("key", "Header name", "Name of the HTTP response header to remove", "X-Custom-Header"),
			},
		},
		{
			Type: "set_response_body", Title: "Set Response Body", Category: "response", Capability: "response-mod",
			Description: "Replace the response body with the value from a variable.",
			Defaults: map[string]string{"source": "var.body"},
			Fields: []StepField{
				sf("source", "Body variable", "Variable whose value becomes the response body", "var.body"),
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
			Type: "set_response_status_from_slot", Title: "Set Response Status from Variable", Category: "response", Capability: "response-mod",
			Description: "Set the HTTP response status code from an integer variable.",
			Defaults: map[string]string{"source": "var.status"},
			Fields: []StepField{
				sf("source", "Status variable", "Integer variable whose value becomes the response status code", "var.status"),
			},
		},
		{
			Type: "map_status", Title: "Map Status Code", Category: "response", Capability: "response-mod",
			Description: "Remap HTTP status codes from a variable using a lookup table. Useful for transforming upstream status codes to client-facing codes.",
			Defaults: map[string]string{"source_var": "upstream_status", "default": "pass"},
			Fields: []StepField{
				sf("source_var", "Source variable", "Variable holding the upstream HTTP status code (integer variable)", "upstream_status"),
				sf("mappings", "Status mappings", "Map of upstream status codes to client status codes (e.g. 502→503)", ""),
				sf("default", "Default", "What to do when no mapping matches: 'pass' to leave unchanged, or a status code like 503", "pass"),
			},
		},
		{
			Type: "echo_request", Title: "Echo Request", Category: "response", Capability: "debug",
			Description: "Mirror the incoming request back as the response. Useful for testing flows.",
			Defaults: map[string]string{},
			Fields:   []StepField{},
		},
		{
			Type: "current_timestamp", Title: "Current Timestamp", Category: "response", Capability: "time",
			Description: "Write the current time into a variable. Format: unix_s (coarse, ~1 ns via cached clock), unix_ms (default), unix_ns, rfc3339.",
			Defaults: map[string]string{"as": "ts", "format": "unix_ms"},
			Fields: []StepField{
				sf("as", "Store as", "Variable name to write the timestamp into.", "ts"),
				sf("format", "Format", "Output format: unix_s | unix_ms | unix_ns | rfc3339. Default: unix_ms.", "unix_ms"),
			},
		},

		// ── Validation ────────────────────────────────────────────────────────────
		{
			Type: "validate_route", Title: "Validate Route", Category: "validation", Capability: "condition",
			Description: "Evaluates one or more condition rules against request/response data and routes to different next steps based on match outcomes. Supports AND/OR/DIRECT operators with nested conditions.",
			Defaults:    map[string]string{"default_next": ""},
			Fields: []StepField{
				sf("default_next", "Default Next Step", "Step to proceed to when no rule matches. Leave empty to fall through to the next instruction.", ""),
			},
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
	base = append(base, SecurityHeaderStepDescriptors()...)
	base = append(base, BotDetectionStepDescriptors()...)
	base = append(base, OWASPStepDescriptors()...)
	base = append(base, BodyLimitStepDescriptors()...)
	base = append(base, GeoBlockStepDescriptors()...)
	return base
}

// BotDetectionStepDescriptors returns descriptors for the detect_bot step.
func BotDetectionStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type: "detect_bot", Title: "Detect Bot", Category: "security", Capability: "bot_detection",
			Description: "Check the request User-Agent against known bot, scraper, and vulnerability-scanner patterns. Blocks the request (mode=block) or tags a slot with true/false (mode=tag). Empty or missing User-Agent is treated as a bot. Known search engine crawlers can be exempted via allow_crawlers=true.",
			Defaults: map[string]string{
				"bot.mode":           "block",
				"bot.failure_status": "403",
				"bot.failure_body":   "access denied",
				"bot.allow_crawlers": "false",
			},
			Fields: []StepField{
				sf("bot.mode", "Mode", `"block" to reject detected bots (default), or "tag" to write true/false into a variable and continue.`, "block"),
				sf("bot.tag_var", "Tag variable", `Variable to write "true" (bot) or "false" (not bot) into when mode=tag.`, "var.is_bot"),
				sf("bot.failure_status", "Failure status", "HTTP status code to return when a bot is blocked (default 403).", "403"),
				sf("bot.failure_body", "Failure body", "Response body to return when a bot is blocked (default: access denied).", "access denied"),
				sf("bot.allow_crawlers", "Allow crawlers", `Set to "true" to allow known legitimate search engine crawlers (Googlebot, Bingbot, etc.) through without bot-checking.`, "false"),
				sf("bot.extra_patterns", "Extra patterns", "Comma-separated additional User-Agent substrings to block (case-insensitive).", ""),
			},
		},
	}
}

// OWASPStepDescriptors returns descriptors for the owasp_check step.
func OWASPStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type: "owasp_check", Title: "OWASP Check", Category: "security", Capability: "owasp_check",
			Description: "Scan the incoming request (body, query string, headers) for OWASP Top-10 injection patterns: SQL injection, XSS, path traversal, and command injection. In block mode (default) the request is rejected with a 403. In tag mode the name of the first matched check category is written to a variable and execution continues.",
			Defaults: map[string]string{
				"owasp.checks":         "all",
				"owasp.targets":        "all",
				"owasp.mode":           "block",
				"owasp.failure_status": "403",
				"owasp.failure_body":   "request blocked",
			},
			Fields: []StepField{
				sf("owasp.mode", "Mode", `"block" to reject matching requests (default), or "tag" to write the matched category into a variable and continue.`, "block"),
				sf("owasp.checks", "Checks", `Comma-separated list of checks to run: "sqli", "xss", "path_traversal", "cmd_inject", or "all" (default).`, "all"),
				sf("owasp.targets", "Targets", `Comma-separated list of request parts to scan: "body", "query", "headers", or "all" (default).`, "all"),
				sf("owasp.tag_var", "Tag variable", `Variable to write the first matched check category into when mode=tag. Empty string means no match.`, "var.owasp_hit"),
				sf("owasp.failure_status", "Failure status", "HTTP status code to return when a request is blocked (default 403).", "403"),
				sf("owasp.failure_body", "Failure body", "Response body to return when a request is blocked (default: request blocked).", "request blocked"),
			},
		},
	}
}

// SecurityHeaderStepDescriptors returns descriptors for the set_security_headers step.
func SecurityHeaderStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type: "set_security_headers", Title: "Security Headers", Category: "network", Capability: "security_headers",
			Description: "Set HTTP security response headers. Every field is independently opt-in — only headers you configure are written. No forced defaults.",
			Fields: []StepField{
				sf("security_headers.hsts_max_age", "HSTS max-age (seconds)", `Enables Strict-Transport-Security. E.g. "31536000" for 1 year. Leave empty to omit header.`, ""),
				sf("security_headers.hsts_include_subdomains", "HSTS includeSubDomains", `Set to "true" to append "; includeSubDomains" to the HSTS header.`, "false"),
				sf("security_headers.frame_options", "X-Frame-Options", `Prevents clickjacking. E.g. "DENY" or "SAMEORIGIN". Leave empty to omit header.`, ""),
				sf("security_headers.content_type_options", "X-Content-Type-Options", `Set to "true" to send "nosniff". Leave empty to omit header.`, ""),
				sf("security_headers.referrer_policy", "Referrer-Policy", `E.g. "strict-origin-when-cross-origin". Leave empty to omit header.`, ""),
				sf("security_headers.csp", "Content-Security-Policy", `Full CSP value. E.g. "default-src 'self'". Leave empty to omit header.`, ""),
			},
		},
	}
}

// BodyLimitStepDescriptors returns descriptors for the limit_body step.
func BodyLimitStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type: "limit_body", Title: "Limit Body Size", Category: "security", Capability: "body_limit",
			Description: "Enforce a maximum request body size for this flow. Returns 413 if the Content-Length header or the actual body read exceeds the configured limit. Use body.max_bytes, body.max_kb, or body.max_mb to set the limit.",
			Defaults: map[string]string{
				"body.failure_status": "413",
				"body.failure_body":   "request body too large",
			},
			Fields: []StepField{
				sf("body.max_bytes", "Max bytes", "Maximum allowed request body size in bytes. Supports optional suffix: kb or mb (e.g. \"64kb\", \"10mb\"). Set to 0 to disable. Takes precedence over body.max_kb and body.max_mb.", ""),
				sf("body.max_kb", "Max kilobytes", "Maximum allowed body size in kilobytes (convenience alias for body.max_bytes). Ignored if body.max_bytes is set.", ""),
				sf("body.max_mb", "Max megabytes", "Maximum allowed body size in megabytes (convenience alias for body.max_bytes). Ignored if body.max_bytes or body.max_kb is set.", ""),
				sf("body.failure_status", "Failure status", "HTTP status code to return when the body is too large (default 413).", "413"),
				sf("body.failure_body", "Failure body", "Response body to return when the body is too large (default: request body too large).", "request body too large"),
			},
		},
	}
}

// GeoBlockStepDescriptors returns descriptors for the geo_block step.
func GeoBlockStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type: "geo_block", Title: "Geo Block", Category: "security", Capability: "geo_block",
			Description: "Block or allow requests based on the client's country (MaxMind GeoLite2-Country). Use block_list to reject specific countries, or allow_list to only permit listed countries.",
			Defaults: map[string]string{
				"geo.mode":           "block_list",
				"geo.countries":      "",
				"geo.trusted_proxy":  "false",
				"geo.allow_private":  "true",
				"geo.failure_status": "403",
				"geo.failure_body":   "access denied",
				"geo.on_block":       "stop",
			},
			Fields: []StepField{
				sf("geo.mode", "Mode", `"block_list" (default): block IPs in listed countries. "allow_list": only allow IPs from listed countries.`, "block_list"),
				sf("geo.countries", "Countries", "Comma-separated ISO 3166-1 alpha-2 country codes (uppercase), e.g. CN,RU,KP", ""),
				sf("geo.trusted_proxy", "Trust proxy headers", `Set to "true" to read X-Forwarded-For / X-Real-IP instead of RemoteAddr.`, "false"),
				sf("geo.allow_private", "Allow private IPs", `Set to "false" to block private/loopback addresses. Defaults to "true" (allow).`, "true"),
				sf("geo.failure_status", "Failure status", "HTTP status code to return when blocked (default 403).", "403"),
				sf("geo.failure_body", "Failure body", "Response body to return when blocked (default: access denied).", "access denied"),
				sf("geo.on_block", "On block", `"stop" (default): halt flow and return error response. "continue": write result to geo.result_var and continue.`, "stop"),
				sf("geo.result_var", "Result variable", `Variable to write "blocked" or "allowed" into when geo.on_block=continue.`, "geo_result"),
			},
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

// IngestStepDescriptors returns descriptors for the ingestion/logging pipeline steps.
func IngestStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type: "emit_event", Title: "Emit Ingest Event", Category: "ingest", Capability: "logging",
			Description: "Emit a structured event to the ingestion pipeline (non-blocking). Runs after response if deferred=true.",
			Fields: []StepField{
				sf("input.kind", "Event kind", "One of: prompt_in, prompt_out, llm_request, llm_response, tool_call, tool_result, route_decision, cache_hit, custom", "llm_request"),
				sf("input.payload_slot", "Payload variable", "Variable name containing the event payload (raw bytes or JSON)", "var.prompt"),
				sf("input.model_slot", "Model variable", "Optional: variable containing the model name string", "var.chosen_model"),
				sf("input.session_slot", "Session variable", "Optional: variable containing the session ID", "var.session_id"),
				sf("input.deferred", "Deferred", "true = emit after HTTP response is committed; false = emit immediately", "false"),
			},
		},
	}
}
