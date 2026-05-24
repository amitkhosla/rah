package config

// EgressProfileConfig defines a named connection profile for upstream calls.
// Type controls the transport protocol; TLS fields apply to https type only.
type EgressProfileConfig struct {
	Name          string   `json:"name"                       yaml:"name"`
	// Type values: "auto" (default), "http1", "https", "h2c"
	// auto: https URLs get HTTP/2 via ALPN, http URLs get HTTP/1.1
	// http1: force HTTP/1.1 regardless of scheme
	// https: HTTPS with HTTP/2 via ALPN + optional custom TLS config
	// h2c: cleartext HTTP/2 (upstream must support it)
	Type          string   `json:"type"                       yaml:"type"`
	TLSSkipVerify bool     `json:"tls_skip_verify,omitempty"  yaml:"tls_skip_verify,omitempty"`
	// TLSCACerts: list of secrets manager refs, e.g. "ref:secrets/certs/internal-ca"
	// Multiple entries → all trusted simultaneously (supports cert rotation)
	TLSCACerts    []string `json:"tls_ca_certs,omitempty"     yaml:"tls_ca_certs,omitempty"`
	DialTimeoutMs int      `json:"dial_timeout_ms,omitempty"  yaml:"dial_timeout_ms,omitempty"`
	ReqTimeoutMs  int      `json:"req_timeout_ms,omitempty"   yaml:"req_timeout_ms,omitempty"`
}

// EgressCodeRuleConfig maps an exact service code string to a named profile.
// Service codes are resolved before URL pattern matching (higher priority).
type EgressCodeRuleConfig struct {
	ServiceCode string `json:"service_code" yaml:"service_code"`
	Profile     string `json:"profile"      yaml:"profile"`
}

// EgressPatternRuleConfig maps a URL host glob pattern to a named profile.
// Patterns use * as wildcard. More specific patterns (longer literal prefix) take priority.
// Example: "*.agents.internal:*" → "internal-h2c"
type EgressPatternRuleConfig struct {
	Pattern string `json:"pattern" yaml:"pattern"`
	Profile string `json:"profile" yaml:"profile"`
}

// EgressConfig is the top-level egress configuration section.
// Profiles define named connection configs; rules map service codes or URL
// patterns to those profiles. All fields are optional — omitting means
// default transport behaviour (HTTP/1.1 for http://, HTTP/2 auto for https://).
type EgressConfig struct {
	Profiles     []EgressProfileConfig     `json:"profiles,omitempty"       yaml:"profiles,omitempty"`
	CodeRules    []EgressCodeRuleConfig    `json:"code_rules,omitempty"     yaml:"code_rules,omitempty"`
	PatternRules []EgressPatternRuleConfig `json:"pattern_rules,omitempty"  yaml:"pattern_rules,omitempty"`
}
