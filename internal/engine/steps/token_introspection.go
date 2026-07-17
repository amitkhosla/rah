package steps

// Token Introspection â€” RFC 7662.
//
// Validates an opaque or JWT access token by calling an introspection endpoint.
// This is necessary for tokens that cannot be verified locally (no JWKS), such as:
//   - Opaque tokens from Keycloak / Okta / Auth0
//   - Short-lived tokens where revocation must be checked on every request
//
// The introspection endpoint returns a JSON object with at minimum:
//
//	{ "active": true, ... claims ... }
//
// The gateway caches successful introspection responses for a short TTL to avoid
// hammering the auth server on every request.
//
// Auth to the introspection endpoint supports:
//   - HTTP Basic (client_id + client_secret)
//   - Bearer token (static or from slot)
//   - No auth (for open introspection endpoints)

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/datastore"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

const (
	defaultIntrospectionCacheTTL = 30 * time.Second
	defaultIntrospectionTimeout  = 5 * time.Second
)

// introspectionAuthMode controls how the gateway authenticates to the introspection endpoint.
type introspectionAuthMode uint8

const (
	introspectionAuthNone   introspectionAuthMode = iota
	introspectionAuthBasic                         // HTTP Basic: client_id + client_secret
	introspectionAuthBearer                        // Authorization: Bearer <token>
)

// TokenIntrospectionConfig is the bake-time config for validate_token_introspection.
type TokenIntrospectionConfig struct {
	// EndpointURL is the introspection endpoint (e.g. https://auth.example.com/introspect).
	EndpointURL string
	// AuthMode controls how the gateway authenticates to the endpoint.
	AuthMode introspectionAuthMode
	// ClientID is the gateway's client_id for Basic auth.
	ClientID string
	// ClientSecret is the gateway's client_secret for Basic auth.
	ClientSecret string
	// BearerToken is a static bearer token for Bearer auth.
	BearerToken string

	// Timeout is the HTTP client timeout for the introspection endpoint call (default 5s).
	Timeout time.Duration
	// RetryMaxAttempts is total attempts including the first (default 1 = no retry).
	RetryMaxAttempts int
	// RetryBackoff is the base delay between retries; doubles each attempt (default 100ms).
	RetryBackoff time.Duration

	// CacheTTL is how long a successful introspection response is cached.
	// Set to 0 to disable caching (introspect every request).
	CacheTTL time.Duration

	// RequiredScopes are scope strings that must be present in the introspection response.
	RequiredScopes []string

	// OnFailureStatus is the HTTP status on validation failure (default 401).
	OnFailureStatus int
	// OnFailureBody is the response body on failure (default "unauthorized").
	OnFailureBody string
	// ContinueOnFailure writes the result to ResultSlot instead of halting.
	ContinueOnFailure bool
}

// TokenIntrospectionSlots holds runtime slot indexes for validate_token_introspection.
type TokenIntrospectionSlots struct {
	// Token is the slot holding the access token to introspect. -1 = not used (reads from header).
	Token int
	// TokenHeader is the name of the request header (e.g. "Authorization").
	// Only used when Token == -1. Bearer prefix is stripped automatically.
	TokenHeader string
	// Result is the slot to write "true"/"false" in continue mode. -1 = not used.
	Result int
	// Claims is the slot to write the full introspection response as JSON. -1 = not used.
	Claims int
	// Subject is the slot to write the sub claim. -1 = not used.
	Subject int
	// ClientID is the slot to write the client_id claim. -1 = not used.
	ClientID int
	// ScopesOut is the slot to write comma-sep scopes. -1 = not used.
	ScopesOut int
	// BearerToken is the slot holding a runtime bearer token for endpoint auth. -1 = not used.
	BearerToken int
}

// ParseTokenIntrospectionConfig converts a step Input map into a TokenIntrospectionConfig.
func ParseTokenIntrospectionConfig(input map[string]string) (TokenIntrospectionConfig, error) {
	cfg := TokenIntrospectionConfig{
		EndpointURL:     strings.TrimSpace(input["introspect.endpoint"]),
		ClientID:        strings.TrimSpace(input["introspect.client_id"]),
		ClientSecret:    strings.TrimSpace(input["introspect.client_secret"]),
		BearerToken:     strings.TrimSpace(input["introspect.bearer_token"]),
		RequiredScopes:  splitAndTrim(input["introspect.required_scopes"], ","),
		OnFailureStatus: 401,
		OnFailureBody:   "unauthorized",
		Timeout:         defaultIntrospectionTimeout,
		CacheTTL:        defaultIntrospectionCacheTTL,
	}
	if v := strings.TrimSpace(input["introspect.timeout_ms"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Timeout = time.Duration(n) * time.Millisecond
		}
	}
	cfg.RetryMaxAttempts = 1
	cfg.RetryBackoff = 100 * time.Millisecond
	if v := strings.TrimSpace(input["introspect.retry_max_attempts"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.RetryMaxAttempts = n
		}
	}
	if v := strings.TrimSpace(input["introspect.retry_backoff_ms"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.RetryBackoff = time.Duration(n) * time.Millisecond
		}
	}
	if cfg.EndpointURL == "" {
		return cfg, errors.New("introspect.endpoint is required")
	}
	switch strings.ToLower(strings.TrimSpace(input["introspect.auth"])) {
	case "basic":
		cfg.AuthMode = introspectionAuthBasic
	case "bearer":
		cfg.AuthMode = introspectionAuthBearer
	default:
		cfg.AuthMode = introspectionAuthNone
		if cfg.ClientID != "" && cfg.ClientSecret != "" {
			cfg.AuthMode = introspectionAuthBasic
		} else if cfg.BearerToken != "" {
			cfg.AuthMode = introspectionAuthBearer
		}
	}
	if v := strings.TrimSpace(input["introspect.cache_ttl_seconds"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.CacheTTL = time.Duration(n) * time.Second
		}
	}
	if v := strings.TrimSpace(input["introspect.failure_status"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.OnFailureStatus = n
		}
	}
	if v := strings.TrimSpace(input["introspect.failure_body"]); v != "" {
		cfg.OnFailureBody = v
	}
	cfg.ContinueOnFailure = strings.EqualFold(strings.TrimSpace(input["introspect.on_failure"]), "continue")
	return cfg, nil
}

// introspectionResponse is the parsed JSON from the introspection endpoint.
type introspectionResponse struct {
	Active   bool           `json:"active"`
	Sub      string         `json:"sub"`
	ClientID string         `json:"client_id"`
	Scope    string         `json:"scope"`
	Extra    map[string]any `json:"-"`
}

// ValidateTokenIntrospection builds the token introspection validation instruction.
//
// Step config keys:
//
//	introspect.endpoint          â€” introspection endpoint URL (required)
//	introspect.auth              â€” "basic", "bearer", or "none" (auto-detected from client_id/bearer_token)
//	introspect.client_id         â€” client ID for Basic auth
//	introspect.client_secret     â€” client secret for Basic auth
//	introspect.bearer_token      â€” static bearer token for Bearer auth
//	introspect.cache_ttl_seconds â€” cache TTL (default 30, set 0 to disable)
//	introspect.required_scopes   â€” comma-sep required scopes
//	introspect.on_failure        â€” "stop" (default) or "continue"
//	introspect.failure_status    â€” HTTP status on failure (default 401)
//	introspect.failure_body      â€” body on failure (default "unauthorized")
//
// Slot config keys:
//
//	introspect.token_var         â€” slot holding the access token to introspect
//	introspect.token_header      â€” request header name (e.g. "Authorization") â€” used if token_var not set
//	introspect.bearer_token_var  â€” slot holding runtime bearer token for endpoint auth
//	introspect.result_var        â€” slot to write "true"/"false" in continue mode
//	introspect.claims_var        â€” slot to write full response JSON
//	introspect.subject_var       â€” slot to write sub claim
//	introspect.client_id_var     â€” slot to write client_id claim
//	introspect.scopes_out_var    â€” slot to write comma-sep scopes
func ValidateTokenIntrospection(slots TokenIntrospectionSlots, cfg TokenIntrospectionConfig, store datastore.KeyValueStore) engine.Instruction {
	httpClient := &http.Client{Timeout: cfg.Timeout}

	return engine.Instruction{
		Name: "VALIDATE_TOKEN_INTROSPECTION",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			stopFail := func() int16 {
				if cfg.ContinueOnFailure {
					if slots.Result >= 0 && slots.Result < len(ctx.ByteSlots) {
						ctx.ByteSlots[slots.Result] = []byte("false")
					}
					return s.PC + 1
				}
				status := cfg.OnFailureStatus
				body := cfg.OnFailureBody
				ctx.ResponseStatus = status
				ctx.Write([]byte(body))
				ctx.Failed = true
				ctx.ErrorCode = int16(status)
				ctx.ErrorMsg = ctx.Alloc(len(body))
				copy(ctx.ErrorMsg, body)
				return engine.StopPlan
			}

			// Extract the token to introspect.
			rawToken := ""
			if slots.Token >= 0 && slots.Token < len(ctx.ByteSlots) {
				rawToken = string(ctx.ByteSlots[slots.Token])
			}
			if rawToken == "" && slots.TokenHeader != "" && ctx.Request != nil {
				rawToken = extractBearerOrRaw(ctx.Request.Header.Get(slots.TokenHeader))
			}
			if rawToken == "" {
				return stopFail()
			}

			// Cache lookup via datastore.
			var cacheKey string
			if cfg.CacheTTL > 0 && store != nil {
				cacheKey = sha256Hash(rawToken)
				if val, found, _ := store.Get(context.Background(), datastore.Tenant(ctx.TenantKey), cacheKey); found && len(val) > 0 {
					var cached introspectionResponse
					if err := json.Unmarshal(val, &cached); err == nil {
						return applyIntrospectionResult(ctx, s, slots, cfg, cached, val)
					}
				}
			}

			// Resolve bearer token for endpoint auth (runtime slot overrides static config).
			bearerToken := cfg.BearerToken
			if slots.BearerToken >= 0 && slots.BearerToken < len(ctx.ByteSlots) {
				if v := string(ctx.ByteSlots[slots.BearerToken]); v != "" {
					bearerToken = v
				}
			}

			// Call the introspection endpoint with retry on transient errors / 5xx.
			var resp introspectionResponse
			var rawJSON []byte
			var err error
			backoff := cfg.RetryBackoff
			for attempt := 1; attempt <= cfg.RetryMaxAttempts; attempt++ {
				if attempt > 1 {
					time.Sleep(backoff)
					backoff *= 2
				}
				resp, rawJSON, err = callIntrospection(httpClient, cfg, rawToken, bearerToken)
				if err == nil {
					break
				}
			}
			if !resp.Active {
				return stopFail()
			}

			// Scope check.
			if len(cfg.RequiredScopes) > 0 {
				present := map[string]struct{}{}
				for scope := range strings.FieldsSeq(resp.Scope) {
					present[scope] = struct{}{}
				}
				for _, req := range cfg.RequiredScopes {
					if _, ok := present[req]; !ok {
						return stopFail()
					}
				}
			}

			// Cache the successful result.
			if cfg.CacheTTL > 0 && store != nil && cacheKey != "" {
				if exp, ok := store.(datastore.ExpiringStore); ok {
					_ = exp.PutWithTTL(context.Background(), datastore.Tenant(ctx.TenantKey), cacheKey, rawJSON, cfg.CacheTTL)
				}
			}

			return applyIntrospectionResult(ctx, s, slots, cfg, resp, rawJSON)
		},
	}
}

func applyIntrospectionResult(
	ctx *rctx.Context, s *engine.ExecutionState,
	slots TokenIntrospectionSlots, _ TokenIntrospectionConfig,
	resp introspectionResponse, rawJSON []byte,
) int16 {
	if slots.Result >= 0 && slots.Result < len(ctx.ByteSlots) {
		ctx.ByteSlots[slots.Result] = []byte("true")
	}
	if slots.Claims >= 0 && slots.Claims < len(ctx.ByteSlots) && len(rawJSON) > 0 {
		ctx.ByteSlots[slots.Claims] = rawJSON
	}
	if slots.Subject >= 0 && slots.Subject < len(ctx.ByteSlots) && resp.Sub != "" {
		ctx.ByteSlots[slots.Subject] = []byte(resp.Sub)
	}
	if slots.ClientID >= 0 && slots.ClientID < len(ctx.ByteSlots) && resp.ClientID != "" {
		ctx.ByteSlots[slots.ClientID] = []byte(resp.ClientID)
	}
	if slots.ScopesOut >= 0 && slots.ScopesOut < len(ctx.ByteSlots) && resp.Scope != "" {
		ctx.ByteSlots[slots.ScopesOut] = []byte(resp.Scope)
	}
	return s.PC + 1
}

func callIntrospection(
	client *http.Client,
	cfg TokenIntrospectionConfig,
	token, bearerToken string,
) (introspectionResponse, []byte, error) {
	form := url.Values{"token": {token}}
	req, err := http.NewRequest(http.MethodPost, cfg.EndpointURL, strings.NewReader(form.Encode()))
	if err != nil {
		return introspectionResponse{}, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	switch cfg.AuthMode {
	case introspectionAuthBasic:
		req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	case introspectionAuthBearer:
		tok := bearerToken
		if tok == "" {
			tok = cfg.BearerToken
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	httpResp, err := client.Do(req)
	if err != nil {
		return introspectionResponse{}, nil, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return introspectionResponse{}, nil, fmt.Errorf("introspection endpoint returned %d", httpResp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 64*1024))
	if err != nil {
		return introspectionResponse{}, nil, err
	}

	var resp introspectionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return introspectionResponse{}, nil, err
	}
	// Re-parse into Extra for full claims.
	_ = json.Unmarshal(body, &resp.Extra)

	return resp, body, nil
}

// sha256Hash returns a hex SHA-256 of the input string for use as a cache key.
func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	buf := make([]byte, 64)
	hexEncode(buf, h[:])
	return string(buf)
}

func hexEncode(dst, src []byte) {
	const hextable = "0123456789abcdef"
	for i, b := range src {
		dst[i*2] = hextable[b>>4]
		dst[i*2+1] = hextable[b&0x0f]
	}
}
