package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tier 1: Functional (happy path) tests
// ─────────────────────────────────────────────────────────────────────────────

func TestRoleRank_Order(t *testing.T) {
	// Verify the role hierarchy: admin(4) > deployer(3) > publisher(2) > reviewer(1) > viewer(0)
	tests := []struct {
		role     string
		expected int
	}{
		{"admin", 4},
		{"deployer", 3},
		{"publisher", 2},
		{"reviewer", 1},
		{"viewer", 0},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := roleRank[tt.role]
			if got != tt.expected {
				t.Errorf("roleRank[%q] = %d, expected %d", tt.role, got, tt.expected)
			}
		})
	}

	// Verify ordering relationships
	if roleRank["admin"] <= roleRank["deployer"] {
		t.Errorf("admin rank should be > deployer rank")
	}

	if roleRank["deployer"] <= roleRank["publisher"] {
		t.Errorf("deployer rank should be > publisher rank")
	}

	if roleRank["publisher"] <= roleRank["reviewer"] {
		t.Errorf("publisher rank should be > reviewer rank")
	}

	if roleRank["reviewer"] <= roleRank["viewer"] {
		t.Errorf("reviewer rank should be > viewer rank")
	}
}

func TestRequireRole_AuthDisabled(t *testing.T) {
	// Build a Server with sessions=nil (auth disabled)
	srv := &Server{sessions: nil}

	ctx := context.Background()
	w := httptest.NewRecorder()

	// requireRole should always return true when auth is disabled
	result := requireRole(ctx, w, srv, "admin")
	if !result {
		t.Fatalf("expected requireRole to return true when auth is disabled")
	}

	// Response should not have a 403 status when auth is disabled
	if w.Code == http.StatusForbidden {
		t.Errorf("expected no 403 when auth disabled, got %d", w.Code)
	}
}

func TestEnvAllowed_NilList(t *testing.T) {
	// When callerAllowedEnvs returns nil, all environments should be allowed
	srv := &Server{sessions: nil} // auth disabled means callerAllowedEnvs returns nil
	ctx := context.Background()

	// Try various environments
	for _, env := range []string{"prod", "staging", "dev", "unknown"} {
		if !envAllowed(ctx, srv, env) {
			t.Errorf("expected envAllowed to return true for %q when allowed list is nil", env)
		}
	}
}

func TestEnvAllowed_List(t *testing.T) {
	// Create a Server with sessions (auth enabled)
	srv := &Server{sessions: newSessionStore(), tokenStore: newTokenStore("", nil)}

	// Create a token with AllowedEnvs=["prod"]
	_, tok, _ := srv.tokenStore.Create("test-token", "full", "deployer", []string{"prod"}, "admin", 0)

	// Inject the token into context
	ctx := context.WithValue(context.Background(), ctxKeyStudioToken{}, tok)

	// prod should be allowed
	if !envAllowed(ctx, srv, "prod") {
		t.Errorf("expected envAllowed to return true for 'prod'")
	}

	// staging should not be allowed
	if envAllowed(ctx, srv, "staging") {
		t.Errorf("expected envAllowed to return false for 'staging'")
	}
}

func TestEnvAllowed_CaseInsensitive(t *testing.T) {
	srv := &Server{sessions: newSessionStore(), tokenStore: newTokenStore("", nil)}

	_, tok, _ := srv.tokenStore.Create("test-token", "full", "deployer", []string{"prod"}, "admin", 0)

	ctx := context.WithValue(context.Background(), ctxKeyStudioToken{}, tok)

	// Case variations should all work
	testCases := []struct {
		env      string
		expected bool
	}{
		{"prod", true},
		{"PROD", true},
		{"Prod", true},
		{"PRoD", true},
		{"staging", false},
		{"STAGING", false},
	}

	for _, tc := range testCases {
		if got := envAllowed(ctx, srv, tc.env); got != tc.expected {
			t.Errorf("envAllowed(ctx, srv, %q) = %v, expected %v", tc.env, got, tc.expected)
		}
	}
}

func TestEnvAllowed_Whitespace(t *testing.T) {
	srv := &Server{sessions: newSessionStore(), tokenStore: newTokenStore("", nil)}

	_, tok, _ := srv.tokenStore.Create("test-token", "full", "deployer", []string{"prod"}, "admin", 0)

	ctx := context.WithValue(context.Background(), ctxKeyStudioToken{}, tok)

	// Env with leading/trailing whitespace should match
	if !envAllowed(ctx, srv, "  prod  ") {
		t.Errorf("expected envAllowed to handle whitespace in environment name")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 2: Negative / error path tests
// ─────────────────────────────────────────────────────────────────────────────

func TestRequireRole_InsufficientRole(t *testing.T) {
	srv := &Server{sessions: newSessionStore()}

	// Create a session with "viewer" role
	sessionToken := srv.sessions.create("test-user", "viewer")
	entry, _ := srv.sessions.get(sessionToken)
	ctx := context.WithValue(context.Background(), ctxKeyStudioSession{}, entry)

	w := httptest.NewRecorder()

	// requireRole with minimum="publisher" should fail (viewer < publisher)
	result := requireRole(ctx, w, srv, "publisher")
	if result {
		t.Fatalf("expected requireRole to return false for insufficient role")
	}

	// Should have written a 403 response
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", w.Code)
	}
}

func TestRequireRole_SufficientRole(t *testing.T) {
	srv := &Server{sessions: newSessionStore()}

	// Create a session with "deployer" role
	sessionToken := srv.sessions.create("test-user", "deployer")
	entry, _ := srv.sessions.get(sessionToken)
	ctx := context.WithValue(context.Background(), ctxKeyStudioSession{}, entry)

	w := httptest.NewRecorder()

	// requireRole with minimum="publisher" should succeed (deployer > publisher)
	result := requireRole(ctx, w, srv, "publisher")
	if !result {
		t.Fatalf("expected requireRole to return true for sufficient role")
	}

	// Should not have written an error
	if w.Code == http.StatusForbidden {
		t.Errorf("expected no 403 when role is sufficient")
	}
}

func TestRequireAdmin_Allowed(t *testing.T) {
	srv := &Server{sessions: newSessionStore()}

	// Create a session with "admin" role
	sessionToken := srv.sessions.create("test-user", "admin")
	entry, _ := srv.sessions.get(sessionToken)
	ctx := context.WithValue(context.Background(), ctxKeyStudioSession{}, entry)

	w := httptest.NewRecorder()

	result := requireAdmin(ctx, w, srv)
	if !result {
		t.Fatalf("expected requireAdmin to return true for admin user")
	}
}

func TestRequireAdmin_Denied(t *testing.T) {
	srv := &Server{sessions: newSessionStore()}

	// Create a session with "viewer" role
	sessionToken := srv.sessions.create("test-user", "viewer")
	entry, _ := srv.sessions.get(sessionToken)
	ctx := context.WithValue(context.Background(), ctxKeyStudioSession{}, entry)

	w := httptest.NewRecorder()

	result := requireAdmin(ctx, w, srv)
	if result {
		t.Fatalf("expected requireAdmin to return false for non-admin user")
	}

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", w.Code)
	}
}

func TestEnvAllowed_MultipleEnvs(t *testing.T) {
	srv := &Server{sessions: newSessionStore(), tokenStore: newTokenStore("", nil)}

	// Token allowed for multiple environments
	_, tok, _ := srv.tokenStore.Create("multi-env-token", "full", "deployer", []string{"prod", "staging", "dev"}, "admin", 0)

	ctx := context.WithValue(context.Background(), ctxKeyStudioToken{}, tok)

	// All allowed envs should work
	for _, env := range []string{"prod", "staging", "dev"} {
		if !envAllowed(ctx, srv, env) {
			t.Errorf("expected envAllowed to return true for %q", env)
		}
	}

	// Other envs should not work
	if envAllowed(ctx, srv, "other") {
		t.Errorf("expected envAllowed to return false for non-listed env")
	}
}

func TestEnvAllowed_EmptyEnvList(t *testing.T) {
	srv := &Server{sessions: newSessionStore(), tokenStore: newTokenStore("", nil)}

	// Token with empty AllowedEnvs slice (not nil)
	_, tok, _ := srv.tokenStore.Create("no-env-token", "full", "deployer", []string{}, "admin", 0)

	ctx := context.WithValue(context.Background(), ctxKeyStudioToken{}, tok)

	// Any environment should be denied (empty list means no envs allowed)
	if envAllowed(ctx, srv, "prod") {
		t.Errorf("expected envAllowed to return false for empty allowed list")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 3: CallerRole and CallerAllowedEnvs utility tests
// ─────────────────────────────────────────────────────────────────────────────

func TestCallerRole_AuthDisabled(t *testing.T) {
	srv := &Server{sessions: nil}
	ctx := context.Background()

	role := callerRole(ctx, srv)
	if role != "admin" {
		t.Errorf("expected callerRole to return 'admin' when auth disabled, got %q", role)
	}
}

func TestCallerRole_TokenAuthenticated(t *testing.T) {
	srv := &Server{sessions: newSessionStore(), tokenStore: newTokenStore("", nil)}

	_, tok, _ := srv.tokenStore.Create("test", "full", "publisher", nil, "admin", 0)
	ctx := context.WithValue(context.Background(), ctxKeyStudioToken{}, tok)

	role := callerRole(ctx, srv)
	if role != "publisher" {
		t.Errorf("expected callerRole to return 'publisher', got %q", role)
	}
}

func TestCallerRole_SessionAuthenticated(t *testing.T) {
	srv := &Server{sessions: newSessionStore()}

	sessionToken := srv.sessions.create("test-user", "reviewer")
	entry, _ := srv.sessions.get(sessionToken)
	ctx := context.WithValue(context.Background(), ctxKeyStudioSession{}, entry)

	role := callerRole(ctx, srv)
	if role != "reviewer" {
		t.Errorf("expected callerRole to return 'reviewer', got %q", role)
	}
}

func TestCallerRole_NoAuth(t *testing.T) {
	srv := &Server{sessions: newSessionStore()}
	ctx := context.Background()

	role := callerRole(ctx, srv)
	if role != "" {
		t.Errorf("expected callerRole to return empty string for unauthenticated request, got %q", role)
	}
}

func TestCallerAllowedEnvs_AuthDisabled(t *testing.T) {
	srv := &Server{sessions: nil}
	ctx := context.Background()

	envs := callerAllowedEnvs(ctx, srv)
	if envs != nil {
		t.Errorf("expected callerAllowedEnvs to return nil when auth disabled, got %v", envs)
	}
}

func TestCallerAllowedEnvs_TokenAuthenticated(t *testing.T) {
	srv := &Server{sessions: newSessionStore(), tokenStore: newTokenStore("", nil)}

	_, tok, _ := srv.tokenStore.Create("test", "full", "deployer", []string{"prod", "staging"}, "admin", 0)
	ctx := context.WithValue(context.Background(), ctxKeyStudioToken{}, tok)

	envs := callerAllowedEnvs(ctx, srv)
	if len(envs) != 2 {
		t.Fatalf("expected 2 allowed envs, got %d", len(envs))
	}

	expectedEnvs := map[string]bool{"prod": true, "staging": true}
	for _, env := range envs {
		if !expectedEnvs[env] {
			t.Errorf("unexpected env: %q", env)
		}
	}
}

func TestCallerAllowedEnvs_AdminRole(t *testing.T) {
	srv := &Server{sessions: newSessionStore(), tokenStore: newTokenStore("", nil)}

	// Admin token with restricted AllowedEnvs should still return nil (admin has no restrictions)
	_, tok, _ := srv.tokenStore.Create("admin-token", "full", "admin", []string{"prod"}, "admin", 0)
	ctx := context.WithValue(context.Background(), ctxKeyStudioToken{}, tok)

	envs := callerAllowedEnvs(ctx, srv)
	if envs != nil {
		t.Errorf("expected callerAllowedEnvs to return nil for admin role, got %v", envs)
	}
}
