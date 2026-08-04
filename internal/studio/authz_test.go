package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tier 1: Functional (happy path) tests
// ─────────────────────────────────────────────────────────────────────────────

func TestNewExternalAuthzClient_NotExternal(t *testing.T) {
	// Test that NewExternalAuthzClient returns nil when provider is not "external"

	cfg := AuthzConfig{
		Provider: "",
		Endpoint: "http://example.com/authz",
	}

	client := NewExternalAuthzClient(cfg)
	if client != nil {
		t.Fatalf("expected nil client for non-external provider, got %v", client)
	}
}

func TestNewExternalAuthzClient_NotExternal_OtherValue(t *testing.T) {
	// Test with a different provider name

	cfg := AuthzConfig{
		Provider: "internal",
		Endpoint: "http://example.com/authz",
	}

	client := NewExternalAuthzClient(cfg)
	if client != nil {
		t.Fatalf("expected nil client for provider=%q, got %v", cfg.Provider, client)
	}
}

func TestNewExternalAuthzClient_External(t *testing.T) {
	// Test that NewExternalAuthzClient returns non-nil for provider="external"

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: "http://example.com/authz",
	}

	client := NewExternalAuthzClient(cfg)
	if client == nil {
		t.Fatalf("expected non-nil client for provider='external'")
	}

	// Default timeout should be 500ms
	if client.cfg.TimeoutMs != 500 {
		t.Errorf("expected default timeout 500ms, got %dms", client.cfg.TimeoutMs)
	}
}

func TestNewExternalAuthzClient_CustomTimeout(t *testing.T) {
	// Test that custom timeout is preserved

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  "http://example.com/authz",
		TimeoutMs: 1000,
	}

	client := NewExternalAuthzClient(cfg)
	if client.cfg.TimeoutMs != 1000 {
		t.Errorf("expected timeout 1000ms, got %dms", client.cfg.TimeoutMs)
	}
}

func TestFailOpen_AllowTimeout(t *testing.T) {
	// Test failOpen with OnTimeout="allow"

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  "http://example.com/authz",
		OnTimeout: "allow",
	}

	client := NewExternalAuthzClient(cfg)
	if !client.failOpen() {
		t.Errorf("expected failOpen to return true for OnTimeout='allow'")
	}
}

func TestFailOpen_DenyTimeout(t *testing.T) {
	// Test failOpen with OnTimeout not set (default deny)

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  "http://example.com/authz",
		OnTimeout: "",
	}

	client := NewExternalAuthzClient(cfg)
	if client.failOpen() {
		t.Errorf("expected failOpen to return false for default (deny) timeout")
	}
}

func TestFailOpen_DenyTimeoutExplicit(t *testing.T) {
	// Test failOpen with OnTimeout="deny"

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  "http://example.com/authz",
		OnTimeout: "deny",
	}

	client := NewExternalAuthzClient(cfg)
	if client.failOpen() {
		t.Errorf("expected failOpen to return false for OnTimeout='deny'")
	}
}

func TestInternalRoleForAction_Entries(t *testing.T) {
	// Verify the internal role mapping contains expected entries

	tests := []struct {
		action   string
		expected string
	}{
		{"read", "viewer"},
		{"publish", "publisher"},
		{"promote", "deployer"},
		{"approve", "reviewer"},
		{"admin", "admin"},
	}

	for _, tt := range tests {
		if got := internalRoleForAction[tt.action]; got != tt.expected {
			t.Errorf("internalRoleForAction[%q] = %q, expected %q", tt.action, got, tt.expected)
		}
	}
}

func TestAuthorize_Success(t *testing.T) {
	// Test Authorize with a mock server that returns allowed=true

	// Create a mock authorization server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req AuthzRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Echo back allowed=true
		response := AuthzResponse{
			Allowed: true,
			Reason:  "user is authorized",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{
			Username: "alice",
			Role:     "publisher",
		},
		Action: "publish",
		Resource: AuthzResource{
			Type: "release",
			Name: "v1.0.0",
		},
	}

	allowed, err := client.Authorize(ctx, req)
	if err != nil {
		t.Fatalf("Authorize failed: %v", err)
	}

	if !allowed {
		t.Errorf("expected allowed=true, got false")
	}
}

func TestAuthorize_Denied(t *testing.T) {
	// Test Authorize with a mock server that returns allowed=false

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := AuthzResponse{
			Allowed: false,
			Reason:  "user does not have permission",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "viewer"},
		Action:  "admin",
	}

	allowed, err := client.Authorize(ctx, req)
	if err != nil {
		t.Fatalf("Authorize failed: %v", err)
	}

	if allowed {
		t.Errorf("expected allowed=false, got true")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 2: Timeout and error handling
// ─────────────────────────────────────────────────────────────────────────────

func TestAuthorize_Timeout_FailOpen(t *testing.T) {
	// Test Authorize with timeout and failOpen=true

	// Mock server that sleeps past the timeout
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Sleep longer than timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  mockServer.URL,
		TimeoutMs: 100, // 100ms timeout
		OnTimeout: "allow",
	}

	client := NewExternalAuthzClient(cfg)

	// Use a context with its own timeout to ensure the test doesn't hang
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "publisher"},
		Action:  "publish",
	}

	allowed, err := client.Authorize(ctx, req)

	// On timeout with failOpen=true, should return true (allowed)
	if !allowed {
		t.Errorf("expected allowed=true (failOpen) on timeout, got false")
	}
	if err != nil {
		t.Logf("expected error on timeout (for debugging): %v", err)
	}
}

func TestAuthorize_Timeout_FailClosed(t *testing.T) {
	// Test Authorize with timeout and failOpen=false (deny)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  mockServer.URL,
		TimeoutMs: 100,
		OnTimeout: "deny", // or empty string for default deny
	}

	client := NewExternalAuthzClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "publisher"},
		Action:  "publish",
	}

	allowed, err := client.Authorize(ctx, req)

	// On timeout with failOpen=false, should return false (denied)
	if allowed {
		t.Errorf("expected allowed=false (failClosed) on timeout, got true")
	}
	if err != nil {
		t.Logf("expected error on timeout: %v", err)
	}
}

func TestAuthorize_ServerError(t *testing.T) {
	// Test Authorize when server returns non-200 status

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "publisher"},
		Action:  "publish",
	}

	allowed, err := client.Authorize(ctx, req)

	// Non-200 should deny
	if allowed {
		t.Errorf("expected allowed=false for server error, got true")
	}
	if err != nil {
		t.Logf("error on server error (expected): %v", err)
	}
}

func TestAuthorize_MalformedResponse(t *testing.T) {
	// Test Authorize when server returns malformed JSON

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "publisher"},
		Action:  "publish",
	}

	allowed, err := client.Authorize(ctx, req)

	// Malformed response should deny
	if allowed {
		t.Errorf("expected allowed=false for malformed response, got true")
	}
	if err != nil {
		t.Logf("error on malformed response (expected): %v", err)
	}
}

func TestAuthorize_NetworkError_FailOpen(t *testing.T) {
	// Test Authorize with invalid endpoint and failOpen=true

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  "http://localhost:1/invalid_endpoint_that_does_not_exist",
		TimeoutMs: 100,
		OnTimeout: "allow",
	}

	client := NewExternalAuthzClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "publisher"},
		Action:  "publish",
	}

	allowed, err := client.Authorize(ctx, req)

	// Network error with failOpen=true should return true
	if !allowed {
		t.Errorf("expected allowed=true (failOpen) on network error, got false")
	}
	if err != nil {
		t.Logf("error on network failure (expected): %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 3: Request/Response validation
// ─────────────────────────────────────────────────────────────────────────────

func TestAuthorize_RequestMarshaling(t *testing.T) {
	// Test that AuthzRequest is correctly marshaled and sent to server

	var capturedReq AuthzRequest
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req AuthzRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		capturedReq = req

		response := AuthzResponse{Allowed: true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{
			Username:    "testuser",
			Role:        "publisher",
			SSOProvider: "okta",
			Groups:      []string{"team-a", "team-b"},
		},
		Action: "publish",
		Resource: AuthzResource{
			Type:      "release",
			Name:      "v1.0",
			ReleaseID: "rel-123",
		},
	}

	_, _ = client.Authorize(ctx, req)

	// Verify the captured request matches what we sent
	if capturedReq.Subject.Username != "testuser" {
		t.Errorf("expected username 'testuser', got %q", capturedReq.Subject.Username)
	}

	if capturedReq.Subject.Role != "publisher" {
		t.Errorf("expected role 'publisher', got %q", capturedReq.Subject.Role)
	}

	if capturedReq.Subject.SSOProvider != "okta" {
		t.Errorf("expected SSO provider 'okta', got %q", capturedReq.Subject.SSOProvider)
	}

	if len(capturedReq.Subject.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(capturedReq.Subject.Groups))
	}

	if capturedReq.Action != "publish" {
		t.Errorf("expected action 'publish', got %q", capturedReq.Action)
	}

	if capturedReq.Resource.Type != "release" {
		t.Errorf("expected resource type 'release', got %q", capturedReq.Resource.Type)
	}

	if capturedReq.Resource.ReleaseID != "rel-123" {
		t.Errorf("expected release ID 'rel-123', got %q", capturedReq.Resource.ReleaseID)
	}
}

func TestAuthorize_ContentType(t *testing.T) {
	// Test that request is sent with correct Content-Type header

	var capturedHeader string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get("Content-Type")
		response := AuthzResponse{Allowed: true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "publisher"},
		Action:  "publish",
	}

	_, _ = client.Authorize(ctx, req)

	if capturedHeader != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", capturedHeader)
	}
}

func TestAuthorize_CustomHeaders(t *testing.T) {
	// Test that custom headers are included in the request

	var capturedAuthHeader string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuthHeader = r.Header.Get("X-API-Key")
		response := AuthzResponse{Allowed: true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
		Headers: map[string]string{
			"X-API-Key": "secret-key-123",
		},
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "publisher"},
		Action:  "publish",
	}

	_, _ = client.Authorize(ctx, req)

	if capturedAuthHeader != "secret-key-123" {
		t.Errorf("expected X-API-Key 'secret-key-123', got %q", capturedAuthHeader)
	}
}

func TestNewExternalAuthzClient_ZeroTimeoutBecomesDefault(t *testing.T) {
	// Test that zero timeout is converted to default 500ms

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  "http://example.com",
		TimeoutMs: 0,
	}

	client := NewExternalAuthzClient(cfg)
	if client.cfg.TimeoutMs != 500 {
		t.Errorf("expected zero timeout to become 500ms, got %dms", client.cfg.TimeoutMs)
	}
}

func TestNewExternalAuthzClient_NegativeTimeoutBecomesDefault(t *testing.T) {
	// Test that negative timeout is converted to default 500ms

	cfg := AuthzConfig{
		Provider:  "external",
		Endpoint:  "http://example.com",
		TimeoutMs: -1,
	}

	client := NewExternalAuthzClient(cfg)
	if client.cfg.TimeoutMs != 500 {
		t.Errorf("expected negative timeout to become 500ms, got %dms", client.cfg.TimeoutMs)
	}
}

func TestAuthorize_EmptyResponse(t *testing.T) {
	// Test Authorize when server returns valid JSON but with default values

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AuthzResponse{})
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice"},
		Action:  "read",
	}

	allowed, err := client.Authorize(ctx, req)

	// Empty response means Allowed=false (default)
	if allowed {
		t.Errorf("expected allowed=false for empty response, got true")
	}
	if err != nil {
		t.Errorf("expected no error for valid JSON response, got %v", err)
	}
}

func TestAuthorize_LargeResponseBody(t *testing.T) {
	// Test Authorize with a large response body

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := AuthzResponse{
			Allowed: true,
			Reason:  string(bytes.Repeat([]byte("x"), 10000)), // Large reason string
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer mockServer.Close()

	cfg := AuthzConfig{
		Provider: "external",
		Endpoint: mockServer.URL,
	}

	client := NewExternalAuthzClient(cfg)
	ctx := context.Background()

	req := AuthzRequest{
		Subject: AuthzSubject{Username: "alice", Role: "publisher"},
		Action:  "publish",
	}

	allowed, err := client.Authorize(ctx, req)

	// Should still work with large response
	if !allowed {
		t.Errorf("expected allowed=true, got false")
	}
	if err != nil {
		t.Errorf("expected no error for large response, got %v", err)
	}
}
