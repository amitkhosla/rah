package studio

import (
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tier 1: Functional (happy path) tests
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveRole_FirstMatchWins(t *testing.T) {
	// Test that first matching mapping is used, even if later ones also match

	mappings := &OIDCClaimMappings{
		RoleClaim:   "roles",
		DefaultRole: "viewer",
		Mappings: []OIDCRoleMap{
			{ClaimValue: "admin", Role: "admin", AllowedEnvs: nil},
			{ClaimValue: "admin", Role: "publisher", AllowedEnvs: []string{"prod"}},
		},
	}

	claims := map[string]any{
		"roles": "admin",
	}

	role, envs := resolveRole(claims, mappings)

	// First match should win, so role="admin"
	if role != "admin" {
		t.Errorf("expected first matching role 'admin', got %q", role)
	}

	if envs != nil {
		t.Errorf("expected nil envs for first match, got %v", envs)
	}
}

func TestResolveRole_DefaultRole(t *testing.T) {
	// Test that DefaultRole is used when no mappings match

	mappings := &OIDCClaimMappings{
		RoleClaim:   "roles",
		DefaultRole: "reviewer",
		Mappings: []OIDCRoleMap{
			{ClaimValue: "admin", Role: "admin"},
			{ClaimValue: "moderator", Role: "publisher"},
		},
	}

	claims := map[string]any{
		"roles": "unknown_role",
	}

	role, envs := resolveRole(claims, mappings)

	if role != "reviewer" {
		t.Errorf("expected default role 'reviewer', got %q", role)
	}

	if envs != nil {
		t.Errorf("expected nil envs for default role, got %v", envs)
	}
}

func TestResolveRole_DefaultRoleEmpty(t *testing.T) {
	// Test that DefaultRole defaults to "viewer" when not specified

	mappings := &OIDCClaimMappings{
		RoleClaim: "roles",
		// DefaultRole is empty
		Mappings: []OIDCRoleMap{
			{ClaimValue: "admin", Role: "admin"},
		},
	}

	claims := map[string]any{
		"roles": "unknown",
	}

	role, _ := resolveRole(claims, mappings)

	if role != "viewer" {
		t.Errorf("expected default role 'viewer', got %q", role)
	}
}

func TestResolveRole_NilMappings(t *testing.T) {
	// Test that nil OIDCClaimMappings returns "viewer" and nil envs

	claims := map[string]any{
		"roles": "admin",
	}

	role, envs := resolveRole(claims, nil)

	if role != "viewer" {
		t.Errorf("expected default role 'viewer' for nil mappings, got %q", role)
	}

	if envs != nil {
		t.Errorf("expected nil envs for nil mappings, got %v", envs)
	}
}

func TestResolveRole_WithAllowedEnvs(t *testing.T) {
	// Test that AllowedEnvs from matching mapping are returned

	mappings := &OIDCClaimMappings{
		RoleClaim:   "roles",
		DefaultRole: "viewer",
		Mappings: []OIDCRoleMap{
			{
				ClaimValue:  "deployer",
				Role:        "deployer",
				AllowedEnvs: []string{"prod", "staging"},
			},
		},
	}

	claims := map[string]any{
		"roles": "deployer",
	}

	role, envs := resolveRole(claims, mappings)

	if role != "deployer" {
		t.Errorf("expected role 'deployer', got %q", role)
	}

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

func TestExtractEmail_DefaultClaim(t *testing.T) {
	// Test extracting email from default "email" claim

	claims := map[string]any{
		"email": "user@example.com",
	}

	email := extractEmail(claims, "")

	if email != "user@example.com" {
		t.Errorf("expected email 'user@example.com', got %q", email)
	}
}

func TestExtractEmail_CustomClaim(t *testing.T) {
	// Test extracting email from custom claim name

	claims := map[string]any{
		"mail": "custom@example.com",
	}

	email := extractEmail(claims, "mail")

	if email != "custom@example.com" {
		t.Errorf("expected email 'custom@example.com', got %q", email)
	}
}

func TestExtractEmail_NotFound(t *testing.T) {
	// Test when email claim is not present

	claims := map[string]any{
		"name": "John Doe",
	}

	email := extractEmail(claims, "email")

	if email != "" {
		t.Errorf("expected empty email for missing claim, got %q", email)
	}
}

func TestCodeChallenge(t *testing.T) {
	// Test that codeChallenge produces a valid base64url-encoded SHA-256 hash

	verifier := "test_code_verifier"
	challenge := codeChallenge(verifier)

	// Challenge should be non-empty
	if challenge == "" {
		t.Fatalf("expected non-empty code challenge")
	}

	// Challenge should be base64url-encoded (no padding, no +/)
	for _, ch := range challenge {
		if ch == '+' || ch == '/' || ch == '=' {
			t.Errorf("code challenge contains invalid base64url character: %c", ch)
		}
	}

	// SHA-256 hash is 32 bytes, base64url encoded should be 43 chars (no padding)
	// Actually: 32 bytes -> ceil(32*8/6) = 43 chars for base64, minus padding = 43
	if len(challenge) != 43 {
		t.Errorf("expected code challenge to be 43 chars (base64url of SHA-256), got %d", len(challenge))
	}

	// Same verifier should produce same challenge
	challenge2 := codeChallenge(verifier)
	if challenge != challenge2 {
		t.Errorf("code challenge not deterministic: %q vs %q", challenge, challenge2)
	}

	// Different verifier should produce different challenge
	challenge3 := codeChallenge("different_verifier")
	if challenge == challenge3 {
		t.Errorf("different verifiers should produce different challenges")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 2: OIDCStateStore tests
// ─────────────────────────────────────────────────────────────────────────────

func TestOIDCStateStore_ConsumeOnce(t *testing.T) {
	// Test that a state can be created and consumed once

	store := newOIDCStateStore()

	state := store.create("google", "test_verifier")

	if state == "" {
		t.Fatalf("expected non-empty state")
	}

	// First consume should succeed
	entry, ok := store.consume(state)
	if !ok {
		t.Fatalf("expected first consume to succeed")
	}

	if entry.ProviderName != "google" {
		t.Errorf("expected provider 'google', got %q", entry.ProviderName)
	}

	if entry.CodeVerifier != "test_verifier" {
		t.Errorf("expected code verifier 'test_verifier', got %q", entry.CodeVerifier)
	}

	// Second consume should fail (single-use)
	entry2, ok := store.consume(state)
	if ok {
		t.Fatalf("expected second consume to fail (single-use)")
	}

	if entry2.ProviderName != "" {
		t.Errorf("expected empty entry on failed consume, got %v", entry2)
	}
}

func TestOIDCStateStore_ExpiredState(t *testing.T) {
	// Test that expired states cannot be consumed

	store := newOIDCStateStore()

	state := store.create("google", "verifier")

	// Manually expire the entry by accessing the internal structure
	store.mu.Lock()
	if entry, ok := store.entries[state]; ok {
		entry.ExpiresAt = time.Now().Add(-1 * time.Second) // expire in the past
		store.entries[state] = entry
	}
	store.mu.Unlock()

	// Consume should fail
	_, ok := store.consume(state)
	if ok {
		t.Fatalf("expected consume to fail for expired state")
	}
}

func TestOIDCStateStore_NonexistentState(t *testing.T) {
	// Test consuming a state that was never created

	store := newOIDCStateStore()

	_, ok := store.consume("nonexistent_state")
	if ok {
		t.Fatalf("expected consume to fail for nonexistent state")
	}
}

func TestOIDCStateStore_MultipleStates(t *testing.T) {
	// Test creating and consuming multiple states independently

	store := newOIDCStateStore()

	state1 := store.create("google", "verifier1")
	state2 := store.create("okta", "verifier2")
	state3 := store.create("azure", "verifier3")

	// Consume state2 first
	entry2, ok := store.consume(state2)
	if !ok {
		t.Fatalf("expected consume state2 to succeed")
	}
	if entry2.ProviderName != "okta" {
		t.Errorf("expected provider 'okta', got %q", entry2.ProviderName)
	}

	// state1 and state3 should still be consumable
	entry1, ok := store.consume(state1)
	if !ok {
		t.Fatalf("expected consume state1 to succeed")
	}
	if entry1.ProviderName != "google" {
		t.Errorf("expected provider 'google', got %q", entry1.ProviderName)
	}

	entry3, ok := store.consume(state3)
	if !ok {
		t.Fatalf("expected consume state3 to succeed")
	}
	if entry3.ProviderName != "azure" {
		t.Errorf("expected provider 'azure', got %q", entry3.ProviderName)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 3: Mapping edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveRole_ArrayClaims(t *testing.T) {
	// Test when claim value is an array of strings

	mappings := &OIDCClaimMappings{
		RoleClaim:   "roles",
		DefaultRole: "viewer",
		Mappings: []OIDCRoleMap{
			{ClaimValue: "admin", Role: "admin"},
			{ClaimValue: "publisher", Role: "publisher"},
		},
	}

	claims := map[string]any{
		"roles": []any{"user", "publisher", "viewer"},
	}

	role, _ := resolveRole(claims, mappings)

	// Should match "publisher" (first match in claim values order)
	if role != "publisher" {
		t.Errorf("expected role 'publisher', got %q", role)
	}
}

func TestResolveRole_MixedTypeArray(t *testing.T) {
	// Test when claim array contains non-string values (should be skipped)

	mappings := &OIDCClaimMappings{
		RoleClaim:   "roles",
		DefaultRole: "viewer",
		Mappings: []OIDCRoleMap{
			{ClaimValue: "admin", Role: "admin"},
		},
	}

	claims := map[string]any{
		"roles": []any{123, true, "admin", nil},
	}

	role, _ := resolveRole(claims, mappings)

	// Should find "admin" and ignore non-string values
	if role != "admin" {
		t.Errorf("expected role 'admin', got %q", role)
	}
}

func TestExtractEmail_WrongType(t *testing.T) {
	// Test when email claim exists but is not a string

	claims := map[string]any{
		"email": 12345, // numeric instead of string
	}

	email := extractEmail(claims, "email")

	if email != "" {
		t.Errorf("expected empty email for non-string claim value, got %q", email)
	}
}

func TestCodeChallenge_DifferentLengthVerifiers(t *testing.T) {
	// Test code challenge for different verifier lengths

	verifiers := []string{
		"short",
		"medium_length_verifier",
		"this_is_a_very_long_code_verifier_that_should_still_work_correctly_for_pkce",
	}

	for _, verifier := range verifiers {
		challenge := codeChallenge(verifier)

		if len(challenge) != 43 {
			t.Errorf("expected 43-char challenge for verifier of length %d, got %d", len(verifier), len(challenge))
		}

		// Should be valid base64url
		for _, ch := range challenge {
			if ch == '+' || ch == '/' || ch == '=' {
				t.Errorf("challenge contains invalid base64url character: %c", ch)
			}
		}
	}
}

func TestOIDCStateStore_RapidCreation(t *testing.T) {
	// Test creating many states rapidly

	store := newOIDCStateStore()

	states := make([]string, 100)
	for i := 0; i < 100; i++ {
		states[i] = store.create("google", "verifier_"+string(rune(i)))
	}

	// All states should be unique
	stateSet := make(map[string]bool)
	for _, state := range states {
		if stateSet[state] {
			t.Errorf("duplicate state created")
		}
		stateSet[state] = true
	}

	// All should be consumable
	for i, state := range states {
		_, ok := store.consume(state)
		if !ok {
			t.Errorf("state %d failed to consume", i)
		}
	}
}

func TestResolveRole_EmptyMappings(t *testing.T) {
	// Test with empty mappings list

	mappings := &OIDCClaimMappings{
		RoleClaim:   "roles",
		DefaultRole: "reviewer",
		Mappings:    []OIDCRoleMap{}, // empty
	}

	claims := map[string]any{
		"roles": "admin",
	}

	role, _ := resolveRole(claims, mappings)

	// Should fall back to DefaultRole
	if role != "reviewer" {
		t.Errorf("expected default role 'reviewer', got %q", role)
	}
}

func TestResolveRole_MissingClaimInClaims(t *testing.T) {
	// Test when the role claim key doesn't exist in claims

	mappings := &OIDCClaimMappings{
		RoleClaim:   "roles",
		DefaultRole: "viewer",
		Mappings: []OIDCRoleMap{
			{ClaimValue: "admin", Role: "admin"},
		},
	}

	claims := map[string]any{
		"name": "John Doe",
		// "roles" key is missing
	}

	role, _ := resolveRole(claims, mappings)

	// Should fall back to DefaultRole
	if role != "viewer" {
		t.Errorf("expected default role 'viewer', got %q", role)
	}
}
