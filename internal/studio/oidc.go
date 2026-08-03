package studio

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// ── Config structs ────────────────────────────────────────────────────────────

// OIDCConfig holds configuration for one or more OIDC identity providers.
type OIDCConfig struct {
	Providers []OIDCProvider `json:"providers,omitempty" yaml:"providers,omitempty"`
}

// OIDCProvider is one configured OIDC IDP entry.
type OIDCProvider struct {
	Name                 string            `json:"name"                    yaml:"name"`
	ClientID             string            `json:"client_id"               yaml:"client_id"`
	ClientSecret         string            `json:"client_secret"           yaml:"client_secret"` // supports "env:VAR"
	IssuerURL            string            `json:"issuer_url"              yaml:"issuer_url"`
	RedirectURI          string            `json:"redirect_uri"            yaml:"redirect_uri"`
	Scopes               []string          `json:"scopes"                  yaml:"scopes"`
	ClaimMappings        OIDCClaimMappings `json:"claim_mappings"          yaml:"claim_mappings"`
	ReapplyClaimsOnLogin bool              `json:"reapply_claims_on_login" yaml:"reapply_claims_on_login"`
}

// OIDCClaimMappings defines how OIDC token claims map to rah roles.
type OIDCClaimMappings struct {
	EmailClaim  string        `json:"email_claim"  yaml:"email_claim"`
	RoleClaim   string        `json:"role_claim"   yaml:"role_claim"`
	Mappings    []OIDCRoleMap `json:"mappings"     yaml:"mappings"`
	DefaultRole string        `json:"default_role" yaml:"default_role"` // fallback role; default "viewer"
}

// OIDCRoleMap maps one claim value to a rah role and optional env scope.
type OIDCRoleMap struct {
	ClaimValue  string   `json:"claim_value"            yaml:"claim_value"`
	Role        string   `json:"role"                   yaml:"role"`
	AllowedEnvs []string `json:"allowed_envs,omitempty" yaml:"allowed_envs,omitempty"`
}

// ── OIDC discovery ────────────────────────────────────────────────────────────

// OIDCDiscovery holds the subset of the OIDC discovery document that we need.
type OIDCDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// discoverOIDC fetches the OIDC discovery document from issuerURL.
func discoverOIDC(ctx context.Context, client *http.Client, issuerURL string) (*OIDCDiscovery, error) {
	discoveryURL := strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery: server returned %d", resp.StatusCode)
	}
	var disc OIDCDiscovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&disc); err != nil {
		return nil, fmt.Errorf("oidc discovery: decode: %w", err)
	}
	// Validate issuer matches (trim trailing slash from both sides).
	if strings.TrimRight(disc.Issuer, "/") != strings.TrimRight(issuerURL, "/") {
		return nil, fmt.Errorf("oidc discovery: issuer mismatch: got %q, expected %q", disc.Issuer, issuerURL)
	}
	return &disc, nil
}

// ── Token exchange ────────────────────────────────────────────────────────────

// exchangeCodeForIDToken exchanges an authorization code for an ID token.
func exchangeCodeForIDToken(ctx context.Context, client *http.Client, disc *OIDCDiscovery, provider OIDCProvider, code, codeVerifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {provider.RedirectURI},
		"client_id":     {provider.ClientID},
		"client_secret": {provider.ClientSecret},
		"code_verifier": {codeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, disc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oidc token exchange: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oidc token exchange: request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oidc token exchange: server returned %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("oidc token exchange: decode: %w", err)
	}
	idToken, ok := result["id_token"].(string)
	if !ok || idToken == "" {
		return "", fmt.Errorf("oidc token exchange: no id_token in response")
	}
	return idToken, nil
}

// ── ID token validation ───────────────────────────────────────────────────────

// validateIDToken verifies an ID token's signature, issuer, audience, and expiry.
// Returns the parsed claims on success.
func validateIDToken(ctx context.Context, client *http.Client, disc *OIDCDiscovery, clientID, rawToken string) (map[string]any, error) {
	// 1. Fetch JWKS.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, disc.JWKSURI, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc validate: fetch JWKS request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc validate: fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	jwksBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oidc validate: read JWKS: %w", err)
	}

	// 2. Parse JWKS.
	var jwks jose.JSONWebKeySet
	if err := json.Unmarshal(jwksBody, &jwks); err != nil {
		return nil, fmt.Errorf("oidc validate: parse JWKS: %w", err)
	}

	// 3. Parse the JWT.
	jws, err := jose.ParseSigned(rawToken, []jose.SignatureAlgorithm{jose.RS256, jose.ES256, jose.RS384, jose.ES384})
	if err != nil {
		return nil, fmt.Errorf("oidc validate: parse JWT: %w", err)
	}

	// 4. Find the matching key and verify signature.
	var payload []byte
	var verifyErr error
	if len(jws.Signatures) == 0 {
		return nil, fmt.Errorf("oidc validate: no signatures in JWT")
	}
	kid := jws.Signatures[0].Header.KeyID
	var keys []jose.JSONWebKey
	if kid != "" {
		keys = jwks.Key(kid)
	}
	if len(keys) == 0 {
		// Try all keys if kid not matched.
		keys = jwks.Keys
	}
	for _, key := range keys {
		payload, verifyErr = jws.Verify(key)
		if verifyErr == nil {
			break
		}
	}
	if verifyErr != nil {
		return nil, fmt.Errorf("oidc validate: signature verification failed: %w", verifyErr)
	}

	// 5. Parse claims.
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("oidc validate: parse claims: %w", err)
	}

	// 6. Verify issuer.
	iss, _ := claims["iss"].(string)
	if strings.TrimRight(iss, "/") != strings.TrimRight(disc.Issuer, "/") {
		return nil, fmt.Errorf("oidc validate: issuer mismatch: got %q, expected %q", iss, disc.Issuer)
	}

	// 7. Verify audience.
	audOK := false
	switch aud := claims["aud"].(type) {
	case string:
		audOK = aud == clientID
	case []any:
		for _, a := range aud {
			if s, ok := a.(string); ok && s == clientID {
				audOK = true
				break
			}
		}
	}
	if !audOK {
		return nil, fmt.Errorf("oidc validate: audience does not contain client_id %q", clientID)
	}

	// 8. Verify expiry.
	switch exp := claims["exp"].(type) {
	case float64:
		if time.Now().Unix() > int64(exp) {
			return nil, fmt.Errorf("oidc validate: token has expired")
		}
	default:
		return nil, fmt.Errorf("oidc validate: missing or invalid exp claim")
	}

	return claims, nil
}

// ── Role resolution ───────────────────────────────────────────────────────────

// resolveRole maps OIDC claims to a rah role and optional env allowlist.
// Walks mappings in order; first match wins. Falls back to DefaultRole ("viewer").
func resolveRole(claims map[string]any, cm *OIDCClaimMappings) (role string, allowedEnvs []string) {
	if cm == nil {
		return "viewer", nil
	}

	// Extract the role claim value(s).
	claimVal := claims[cm.RoleClaim]
	var claimValues []string
	switch v := claimVal.(type) {
	case string:
		claimValues = []string{v}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				claimValues = append(claimValues, s)
			}
		}
	}

	// Walk mappings in order; first match wins.
	for _, mapping := range cm.Mappings {
		for _, cv := range claimValues {
			if cv == mapping.ClaimValue {
				return mapping.Role, mapping.AllowedEnvs
			}
		}
	}

	// Fall back to DefaultRole.
	defaultRole := cm.DefaultRole
	if defaultRole == "" {
		defaultRole = "viewer"
	}
	return defaultRole, nil
}

// extractEmail returns the email claim value, or "" if not found.
func extractEmail(claims map[string]any, emailClaim string) string {
	if emailClaim == "" {
		emailClaim = "email"
	}
	v, _ := claims[emailClaim].(string)
	return v
}

// ── OIDC state store (CSRF protection) ───────────────────────────────────────

// oidcStateEntry holds the data associated with a pending OIDC login.
type oidcStateEntry struct {
	ProviderName string
	CodeVerifier string
	ExpiresAt    time.Time
}

// oidcStateStore is a short-lived, in-memory store of OIDC state tokens.
// Each token is single-use to prevent replay attacks.
type oidcStateStore struct {
	mu      sync.Mutex
	entries map[string]oidcStateEntry
}

// newOIDCStateStore creates a state store and starts the background sweep goroutine.
func newOIDCStateStore() *oidcStateStore {
	s := &oidcStateStore{entries: make(map[string]oidcStateEntry)}
	go s.sweepLoop()
	return s
}

// create generates a new state nonce, stores it with the provider name and PKCE verifier.
func (s *oidcStateStore) create(providerName, codeVerifier string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	state := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.entries[state] = oidcStateEntry{
		ProviderName: providerName,
		CodeVerifier: codeVerifier,
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	}
	s.mu.Unlock()
	return state
}

// consume atomically retrieves and deletes a state entry.
// Returns the entry and true if the state exists and has not expired.
// The atomic check+delete prevents replay attacks.
func (s *oidcStateStore) consume(state string) (oidcStateEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[state]
	if !ok || time.Now().After(entry.ExpiresAt) {
		delete(s.entries, state) // clean up expired entry if present
		return oidcStateEntry{}, false
	}
	delete(s.entries, state)
	return entry, true
}

// sweepLoop removes expired entries every 2 minutes.
func (s *oidcStateStore) sweepLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for k, e := range s.entries {
			if now.After(e.ExpiresAt) {
				delete(s.entries, k)
			}
		}
		s.mu.Unlock()
	}
}

// ── PKCE helpers ─────────────────────────────────────────────────────────────

// generateCodeVerifier generates a PKCE code verifier (32 random bytes, base64url encoded).
func generateCodeVerifier() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// codeChallenge computes the PKCE S256 code challenge from a verifier.
func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
