package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// AuthzConfig configures the optional external authorization service.
type AuthzConfig struct {
	Provider  string            `json:"provider"   yaml:"provider"` // "external" or ""
	Endpoint  string            `json:"endpoint"   yaml:"endpoint"`
	TimeoutMs int               `json:"timeout_ms" yaml:"timeout_ms"` // default 500
	OnTimeout string            `json:"on_timeout" yaml:"on_timeout"` // "deny"|"allow"
	Headers   map[string]string `json:"headers"    yaml:"headers"`    // values support ${ENV_VAR}
}

// AuthzSubject identifies the caller in an authorization request.
type AuthzSubject struct {
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	SSOProvider string   `json:"sso_provider,omitempty"`
	Groups      []string `json:"groups,omitempty"`
}

// AuthzResource identifies the resource being acted upon.
type AuthzResource struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	ReleaseID string `json:"release_id,omitempty"`
}

// AuthzRequest is the payload sent to the external authorization endpoint.
type AuthzRequest struct {
	Subject  AuthzSubject  `json:"subject"`
	Action   string        `json:"action"` // "publish"|"promote"|"approve"|"read"|"admin"
	Resource AuthzResource `json:"resource"`
}

// AuthzResponse is the expected response from the external authorization endpoint.
type AuthzResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// ExternalAuthzClient calls a customer-provided authorization service.
type ExternalAuthzClient struct {
	cfg    AuthzConfig
	client *http.Client
}

// NewExternalAuthzClient returns nil if cfg.Provider is not "external".
func NewExternalAuthzClient(cfg AuthzConfig) *ExternalAuthzClient {
	if cfg.Provider != "external" {
		return nil
	}
	if cfg.TimeoutMs <= 0 {
		cfg.TimeoutMs = 500
	}
	return &ExternalAuthzClient{
		cfg:    cfg,
		client: &http.Client{},
	}
}

// Authorize sends an authorization request to the external service.
// On timeout or network error: respects cfg.OnTimeout ("allow" = fail-open, default = deny).
// On non-200 or malformed response: deny.
func (c *ExternalAuthzClient) Authorize(ctx context.Context, req AuthzRequest) (bool, error) {
	timeout := time.Duration(c.cfg.TimeoutMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(req)
	if err != nil {
		return c.failOpen(), fmt.Errorf("authz: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return c.failOpen(), fmt.Errorf("authz: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.cfg.Headers {
		httpReq.Header.Set(k, os.ExpandEnv(v))
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		// Network error or timeout — respect on_timeout setting.
		return c.failOpen(), nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	var authzResp AuthzResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&authzResp); err != nil {
		return false, nil
	}
	return authzResp.Allowed, nil
}

// failOpen returns true when cfg.OnTimeout == "allow", false otherwise.
func (c *ExternalAuthzClient) failOpen() bool {
	return c.cfg.OnTimeout == "allow"
}

// internalRoleForAction maps action names to minimum required role for internal RBAC fallback.
var internalRoleForAction = map[string]string{
	"read":    "viewer",
	"publish": "publisher",
	"promote": "deployer",
	"approve": "reviewer",
	"admin":   "admin",
}
