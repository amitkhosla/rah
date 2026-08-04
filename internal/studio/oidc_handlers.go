package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// oidcProvidersHandler serves GET /api/oidc/providers
// Returns the list of configured OIDC provider names for the login UI.
func (s *Server) oidcProvidersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if s.oidcStateStore == nil || s.config.OIDC == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []string{}})
		return
	}
	names := make([]string, 0, len(s.config.OIDC.Providers))
	for _, p := range s.config.OIDC.Providers {
		names = append(names, p.Name)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"providers": names})
}

// oidcLoginHandler serves GET /api/oidc/login?provider=<name>
// Builds the authorization URL and redirects the browser.
func (s *Server) oidcLoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.config.OIDC == nil || s.oidcStateStore == nil {
		http.Error(w, "OIDC not configured", http.StatusServiceUnavailable)
		return
	}

	providerName := r.URL.Query().Get("provider")
	provider, ok := s.findOIDCProvider(providerName)
	if !ok {
		http.Error(w, "unknown OIDC provider", http.StatusBadRequest)
		return
	}

	disc, err := discoverOIDC(r.Context(), s.httpClient, provider.IssuerURL)
	if err != nil {
		http.Error(w, "OIDC discovery failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	verifier := generateCodeVerifier()
	state := s.oidcStateStore.create(provider.Name, verifier)

	scopes := provider.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}

	authURL, _ := url.Parse(disc.AuthorizationEndpoint)
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", provider.ClientID)
	q.Set("redirect_uri", provider.RedirectURI)
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	http.Redirect(w, r, authURL.String(), http.StatusFound)
}

// oidcCallbackHandler serves GET /api/oidc/callback?code=...&state=...
// Exchanges the code for an ID token, validates it, provisions/updates the user, and creates a session.
func (s *Server) oidcCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if s.config.OIDC == nil || s.oidcStateStore == nil {
		http.Error(w, "OIDC not configured", http.StatusServiceUnavailable)
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "missing state or code", http.StatusBadRequest)
		return
	}

	stateEntry, ok := s.oidcStateStore.consume(state)
	if !ok {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	provider, ok := s.findOIDCProvider(stateEntry.ProviderName)
	if !ok {
		http.Error(w, "unknown OIDC provider", http.StatusBadRequest)
		return
	}

	disc, err := discoverOIDC(r.Context(), s.httpClient, provider.IssuerURL)
	if err != nil {
		http.Error(w, "OIDC discovery failed", http.StatusBadGateway)
		return
	}

	rawToken, err := exchangeCodeForIDToken(r.Context(), s.httpClient, disc, provider, code, stateEntry.CodeVerifier)
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}

	claims, err := validateIDToken(r.Context(), s.httpClient, disc, provider.ClientID, rawToken)
	if err != nil {
		http.Error(w, "token validation failed", http.StatusUnauthorized)
		return
	}

	email := extractEmail(claims, provider.ClaimMappings.EmailClaim)
	if email == "" {
		http.Error(w, "email claim missing in ID token", http.StatusUnauthorized)
		return
	}

	role, allowedEnvs := resolveRole(claims, &provider.ClaimMappings)

	// JIT provisioning: look up by email, create or update.
	username := email
	existingUser, exists := s.userStore.GetByEmail(email)
	if exists {
		username = existingUser.Username
	}

	user := StudioUser{
		Username:       username,
		Role:           role,
		AllowedEnvs:    allowedEnvs,
		SSOProvisioned: true,
		SSOProvider:    provider.Name,
		SSOEmail:       email,
	}
	if exists && !provider.ReapplyClaimsOnLogin {
		// Keep previously stored role/envs, only refresh SSO metadata.
		user.Role = existingUser.Role
		user.AllowedEnvs = existingUser.AllowedEnvs
	}
	if err := s.userStore.UpsertSSO(user); err != nil {
		http.Error(w, "failed to provision user", http.StatusInternalServerError)
		return
	}

	sessionToken := s.sessions.create(username, user.Role)
	setSessionCookie(w, sessionToken)

	go func() {
		_ = s.auditStore.Append(context.Background(), AuditRecord{
			ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now().UTC(),
			Actor: username, Action: "login.oidc", ResourceType: "session",
			Status: "success", Summary: fmt.Sprintf("%s logged in via OIDC (%s)", username, provider.Name),
		})
	}()

	// Redirect to the Studio root. The frontend reads /api/me after the redirect.
	http.Redirect(w, r, "/", http.StatusFound)
}

// oidcDispatch routes /api/oidc/* requests.
func (s *Server) oidcDispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/oidc")
	switch path {
	case "/providers", "/providers/":
		s.oidcProvidersHandler(w, r)
	case "/login", "/login/":
		s.oidcLoginHandler(w, r)
	case "/callback", "/callback/":
		s.oidcCallbackHandler(w, r)
	default:
		http.NotFound(w, r)
	}
}

// findOIDCProvider finds a provider by name (case-insensitive).
func (s *Server) findOIDCProvider(name string) (OIDCProvider, bool) {
	if s.config.OIDC == nil {
		return OIDCProvider{}, false
	}
	for _, p := range s.config.OIDC.Providers {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return OIDCProvider{}, false
}

