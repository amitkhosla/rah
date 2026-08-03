package studio

import (
	"context"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tier 1: Functional (happy path) tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTokenStore_CreateAndLookup(t *testing.T) {
	store := newTokenStore("", nil) // in-memory, no encryption, no file
	ctx := context.Background()

	// Create a token
	rawToken, tok, err := store.Create("test-token", "full", "deployer", []string{"prod", "staging"}, "admin", 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if rawToken == "" {
		t.Fatalf("expected non-empty raw token")
	}

	if tok == nil {
		t.Fatalf("expected non-nil token struct")
	}

	// Lookup the token by raw value
	foundTok, ok := store.Lookup(rawToken)
	if !ok {
		t.Fatalf("expected Lookup to succeed")
	}

	if foundTok == nil {
		t.Fatalf("expected non-nil token from Lookup")
	}

	// Verify role, name, and allowedEnvs match
	if foundTok.Role != "deployer" {
		t.Errorf("expected role 'deployer', got %q", foundTok.Role)
	}

	if foundTok.Name != "test-token" {
		t.Errorf("expected name 'test-token', got %q", foundTok.Name)
	}

	if len(foundTok.AllowedEnvs) != 2 {
		t.Fatalf("expected 2 allowed envs, got %d", len(foundTok.AllowedEnvs))
	}

	expectedEnvs := map[string]bool{"prod": true, "staging": true}
	for _, env := range foundTok.AllowedEnvs {
		if !expectedEnvs[env] {
			t.Errorf("unexpected env: %q", env)
		}
	}

	_ = ctx // unused but makes test consistent with audit_store_test.go style
}

func TestTokenStore_Revoke(t *testing.T) {
	store := newTokenStore("", nil)

	// Create a token
	rawToken, tok, err := store.Create("revoke-test", "full", "publisher", nil, "admin", 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify it can be looked up
	_, ok := store.Lookup(rawToken)
	if !ok {
		t.Fatalf("expected Lookup to succeed before revoke")
	}

	// Revoke the token
	if err := store.Revoke(tok.ID); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Lookup should now fail
	_, ok = store.Lookup(rawToken)
	if ok {
		t.Fatalf("expected Lookup to fail after revoke")
	}
}

func TestTokenStore_List(t *testing.T) {
	store := newTokenStore("", nil)

	// Create 3 tokens
	_, tok1, err := store.Create("token-1", "full", "admin", nil, "admin", 0)
	if err != nil {
		t.Fatalf("Create token-1 failed: %v", err)
	}

	_, tok2, err := store.Create("token-2", "read_only", "viewer", nil, "admin", 0)
	if err != nil {
		t.Fatalf("Create token-2 failed: %v", err)
	}

	_, tok3, err := store.Create("token-3", "publish_only", "publisher", nil, "admin", 0)
	if err != nil {
		t.Fatalf("Create token-3 failed: %v", err)
	}

	// List should return all 3, all with Revoked=false
	tokens := store.List()
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d", len(tokens))
	}

	for _, tok := range tokens {
		if tok.Revoked {
			t.Errorf("expected Revoked=false for token %s", tok.ID)
		}
		if tok.HashSHA256 != "" {
			t.Errorf("expected HashSHA256 to be cleared for security, got %q", tok.HashSHA256)
		}
	}

	// Revoke one token
	if err := store.Revoke(tok2.ID); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// List should still return 3, but tok2 should show Revoked=true
	tokens = store.List()
	if len(tokens) != 2 {
		t.Fatalf("expected 2 non-revoked tokens after revoke, got %d", len(tokens))
	}

	// Verify that tok1 and tok3 are present (not revoked)
	foundIDs := make(map[string]bool)
	for _, tok := range tokens {
		foundIDs[tok.ID] = true
	}

	if !foundIDs[tok1.ID] {
		t.Errorf("expected token-1 to be in List")
	}

	if !foundIDs[tok3.ID] {
		t.Errorf("expected token-3 to be in List")
	}
}

func TestTokenStore_Expiry(t *testing.T) {
	store := newTokenStore("", nil)

	// Create a token with expiresInDays=0 (no expiry)
	rawToken, _, err := store.Create("no-expiry-token", "full", "admin", nil, "admin", 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Lookup should succeed
	_, ok := store.Lookup(rawToken)
	if !ok {
		t.Fatalf("expected Lookup to succeed for non-expiring token")
	}

	// Create a token with expiresInDays=1 (expires tomorrow)
	rawTokenFuture, _, err := store.Create("future-expiry-token", "full", "admin", nil, "admin", 1)
	if err != nil {
		t.Fatalf("Create with expiry failed: %v", err)
	}

	// Lookup should also succeed
	_, ok = store.Lookup(rawTokenFuture)
	if !ok {
		t.Fatalf("expected Lookup to succeed for token expiring tomorrow")
	}

	// Note: We don't test actual expiry since it would need time manipulation.
	// The important case (no expiry when expiresInDays=0) is tested above.
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 2: Negative / error path tests
// ─────────────────────────────────────────────────────────────────────────────

func TestTokenStore_LookupInvalidToken(t *testing.T) {
	store := newTokenStore("", nil)

	// Try to lookup a token that doesn't exist
	_, ok := store.Lookup("rahst_invalid_base64_here")
	if ok {
		t.Fatalf("expected Lookup to fail for invalid token")
	}
}

func TestTokenStore_RevokeNonexistent(t *testing.T) {
	store := newTokenStore("", nil)

	// Try to revoke a token that doesn't exist
	err := store.Revoke("tok_nonexistent")
	if err == nil {
		t.Fatalf("expected Revoke to fail for nonexistent token")
	}
}

func TestTokenStore_EmptyList(t *testing.T) {
	store := newTokenStore("", nil)

	// List on empty store
	tokens := store.List()
	if len(tokens) != 0 {
		t.Fatalf("expected 0 tokens on empty store, got %d", len(tokens))
	}
}

func TestTokenStore_ListAfterRevokeAll(t *testing.T) {
	store := newTokenStore("", nil)

	// Create 2 tokens
	_, tok1, _ := store.Create("tok1", "full", "admin", nil, "admin", 0)
	_, tok2, _ := store.Create("tok2", "full", "admin", nil, "admin", 0)

	// Revoke both
	_ = store.Revoke(tok1.ID)
	_ = store.Revoke(tok2.ID)

	// List should be empty
	tokens := store.List()
	if len(tokens) != 0 {
		t.Fatalf("expected 0 tokens after revoking all, got %d", len(tokens))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 3: Token details validation
// ─────────────────────────────────────────────────────────────────────────────

func TestTokenStore_TokenHasID(t *testing.T) {
	store := newTokenStore("", nil)

	_, tok, err := store.Create("test", "full", "admin", nil, "admin", 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if tok.ID == "" {
		t.Fatalf("expected non-empty token ID")
	}

	if tok.ID[:4] != "tok_" {
		t.Errorf("expected token ID to start with 'tok_', got %q", tok.ID)
	}
}

func TestTokenStore_TokenHasCreatedAt(t *testing.T) {
	store := newTokenStore("", nil)

	before := time.Now().Unix()
	_, tok, err := store.Create("test", "full", "admin", nil, "admin", 0)
	after := time.Now().Unix()

	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if tok.CreatedAt < before || tok.CreatedAt > after+1 {
		t.Errorf("expected CreatedAt to be in range [%d, %d], got %d", before, after+1, tok.CreatedAt)
	}
}

func TestTokenStore_RawTokenFormat(t *testing.T) {
	store := newTokenStore("", nil)

	rawToken, _, err := store.Create("test", "full", "admin", nil, "admin", 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if len(rawToken) < 10 || rawToken[:6] != "rahst_" {
		t.Errorf("expected raw token to start with 'rahst_', got %q", rawToken)
	}
}

func TestTokenStore_HashIsNotEmpty(t *testing.T) {
	store := newTokenStore("", nil)

	_, tok, err := store.Create("test", "full", "admin", nil, "admin", 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if tok.HashSHA256 == "" {
		t.Fatalf("expected non-empty HashSHA256")
	}

	// Verify it looks like a hex-encoded SHA-256 hash (64 chars)
	if len(tok.HashSHA256) != 64 {
		t.Errorf("expected SHA-256 hash to be 64 chars, got %d", len(tok.HashSHA256))
	}
}

func TestTokenStore_LookupReturnsCopy(t *testing.T) {
	store := newTokenStore("", nil)

	rawToken, _, _ := store.Create("test", "full", "admin", nil, "admin", 0)

	// Lookup twice
	tok1, _ := store.Lookup(rawToken)
	tok2, _ := store.Lookup(rawToken)

	// They should have the same values
	if tok1.ID != tok2.ID {
		t.Errorf("expected same ID from two Lookups")
	}

	// But they should not be the same pointer
	if tok1 == tok2 {
		t.Errorf("expected Lookup to return copies, not same pointer")
	}
}

func TestTokenStore_AllowedEnvsPreserved(t *testing.T) {
	store := newTokenStore("", nil)

	envs := []string{"prod", "staging", "dev"}
	rawToken, _, _ := store.Create("multi-env", "full", "deployer", envs, "admin", 0)

	foundTok, _ := store.Lookup(rawToken)

	if len(foundTok.AllowedEnvs) != len(envs) {
		t.Fatalf("expected %d envs, got %d", len(envs), len(foundTok.AllowedEnvs))
	}

	for i, env := range envs {
		if foundTok.AllowedEnvs[i] != env {
			t.Errorf("expected env[%d]=%q, got %q", i, env, foundTok.AllowedEnvs[i])
		}
	}
}

func TestTokenStore_ScopePreserved(t *testing.T) {
	store := newTokenStore("", nil)

	_, tok, _ := store.Create("scoped-token", "promote:staging", "deployer", nil, "admin", 0)

	if tok.Scope != "promote:staging" {
		t.Errorf("expected Scope='promote:staging', got %q", tok.Scope)
	}
}

func TestTokenStore_CreatedByPreserved(t *testing.T) {
	store := newTokenStore("", nil)

	_, tok, _ := store.Create("test", "full", "admin", nil, "creator-user", 0)

	if tok.CreatedBy != "creator-user" {
		t.Errorf("expected CreatedBy='creator-user', got %q", tok.CreatedBy)
	}
}
