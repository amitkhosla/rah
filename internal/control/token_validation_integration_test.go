package control

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
)

func TestGatewayIntegration_TokenValidationSignatureOnly(t *testing.T) {
	priv, kid, jwksURL := newTestJWKS(t)
	fm, apiID := setupTokenValidationFlow(t, jwksURL, "/v1/secure/signature", map[string]string{
		"jwt.jwks_url": jwksURL,
	})

	validToken := signRS256JWT(t, priv, kid, map[string]any{
		"sub": "user-1",
		"exp": time.Now().Add(2 * time.Minute).Unix(),
	})

	cases := []struct {
		name      string
		authz     string
		wantCode  int
		wantError bool
	}{
		{name: "valid signature", authz: "Bearer " + validToken, wantCode: http.StatusOK},
		{name: "tampered token", authz: "Bearer " + tamperPayload(validToken), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "malformed token", authz: "Bearer not-a-jwt", wantCode: http.StatusUnauthorized, wantError: true},
		{name: "wrong signing key id", authz: "Bearer " + signRS256JWT(t, priv, "wrong-kid", map[string]any{"exp": time.Now().Add(2 * time.Minute).Unix()}), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "missing bearer token", authz: "", wantCode: http.StatusUnauthorized, wantError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertTokenValidationResult(t, fm, apiID, "/v1/secure/signature", tc.authz, tc.wantCode, tc.wantError)
		})
	}
}

func TestGatewayIntegration_TokenValidationJWKSKeySelectionAndMismatch(t *testing.T) {
	goodPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate good rsa key: %v", err)
	}
	wrongPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate wrong rsa key: %v", err)
	}
	otherPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other rsa key: %v", err)
	}

	t.Run("multiple jwks keys selects matching kid", func(t *testing.T) {
		jwksURL := newJWKSWithKeys(t, []map[string]string{
			jwkFromPublicKey("other-kid", &otherPriv.PublicKey),
			jwkFromPublicKey("good-kid", &goodPriv.PublicKey),
		})
		fm, apiID := setupTokenValidationFlow(t, jwksURL, "/v1/secure/jwks-multi", map[string]string{
			"jwt.jwks_url": jwksURL,
		})
		token := signRS256JWT(t, goodPriv, "good-kid", map[string]any{
			"exp": time.Now().Add(2 * time.Minute).Unix(),
		})
		assertTokenValidationResult(t, fm, apiID, "/v1/secure/jwks-multi", "Bearer "+token, http.StatusOK, false)
	})

	t.Run("matching kid but wrong key material rejected", func(t *testing.T) {
		jwksURL := newJWKSWithKeys(t, []map[string]string{
			jwkFromPublicKey("good-kid", &wrongPriv.PublicKey),
		})
		fm, apiID := setupTokenValidationFlow(t, jwksURL, "/v1/secure/jwks-wrong-material", map[string]string{
			"jwt.jwks_url": jwksURL,
		})
		token := signRS256JWT(t, goodPriv, "good-kid", map[string]any{
			"exp": time.Now().Add(2 * time.Minute).Unix(),
		})
		assertTokenValidationResult(t, fm, apiID, "/v1/secure/jwks-wrong-material", "Bearer "+token, http.StatusUnauthorized, true)
	})

	t.Run("jwks has no matching kid rejected", func(t *testing.T) {
		jwksURL := newJWKSWithKeys(t, []map[string]string{
			jwkFromPublicKey("some-other-kid", &goodPriv.PublicKey),
		})
		fm, apiID := setupTokenValidationFlow(t, jwksURL, "/v1/secure/jwks-no-kid", map[string]string{
			"jwt.jwks_url": jwksURL,
		})
		token := signRS256JWT(t, goodPriv, "good-kid", map[string]any{
			"exp": time.Now().Add(2 * time.Minute).Unix(),
		})
		assertTokenValidationResult(t, fm, apiID, "/v1/secure/jwks-no-kid", "Bearer "+token, http.StatusUnauthorized, true)
	})
}

func TestGatewayIntegration_TokenValidationIssuerOnly(t *testing.T) {
	priv, kid, jwksURL := newTestJWKS(t)
	fm, apiID := setupTokenValidationFlow(t, jwksURL, "/v1/secure/issuer", map[string]string{
		"jwt.jwks_url": jwksURL,
		"jwt.issuer":   "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
	})

	cases := []struct {
		name      string
		token     string
		wantCode  int
		wantError bool
	}{
		{
			name: "issuer match",
			token: signRS256JWT(t, priv, kid, map[string]any{
				"iss": "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
				"exp": time.Now().Add(2 * time.Minute).Unix(),
			}),
			wantCode: http.StatusOK,
		},
		{
			name: "issuer mismatch",
			token: signRS256JWT(t, priv, kid, map[string]any{
				"iss": "https://issuer-not-allowed.example.com/",
				"exp": time.Now().Add(2 * time.Minute).Unix(),
			}),
			wantCode:  http.StatusUnauthorized,
			wantError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertTokenValidationResult(t, fm, apiID, "/v1/secure/issuer", "Bearer "+tc.token, tc.wantCode, tc.wantError)
		})
	}
}

func TestGatewayIntegration_TokenValidationScopesOnly(t *testing.T) {
	priv, kid, jwksURL := newTestJWKS(t)
	fm, apiID := setupTokenValidationFlow(t, jwksURL, "/v1/secure/scopes", map[string]string{
		"jwt.jwks_url":        jwksURL,
		"jwt.required_scopes": "read:gateway,write:gateway",
	})

	cases := []struct {
		name      string
		token     string
		wantCode  int
		wantError bool
	}{
		{
			name: "all required scopes present",
			token: signRS256JWT(t, priv, kid, map[string]any{
				"scope": "read:gateway write:gateway admin:other",
				"exp":   time.Now().Add(2 * time.Minute).Unix(),
			}),
			wantCode: http.StatusOK,
		},
		{
			name: "required scope missing",
			token: signRS256JWT(t, priv, kid, map[string]any{
				"scope": "read:gateway",
				"exp":   time.Now().Add(2 * time.Minute).Unix(),
			}),
			wantCode:  http.StatusUnauthorized,
			wantError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertTokenValidationResult(t, fm, apiID, "/v1/secure/scopes", "Bearer "+tc.token, tc.wantCode, tc.wantError)
		})
	}
}

func TestGatewayIntegration_TokenValidationComprehensiveGatewayCases(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	kid := "gateway-kid-1"
	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
		"alg": "RS256",
		"use": "sig",
	}}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})
	compiler := NewCompiler(fm)
	registry := NewNameRegistry()
	server := NewManagementServer(fm, compiler, registry, nil)

	syncReq := UnifiedSyncRequest{
		SyncUUID: "token-validation-1",
		Flows: []FlowUpdate{{
			Name: "secureFlow",
			Instructions: []StepConfig{
				{
					Action:        "token_validation",
					KeyIdentifier: "header.Authorization",
					Input: map[string]string{
						"jwt.jwks_url":        jwksServer.URL,
						"jwt.issuer":          "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
						"jwt.audience":        "https://local-gateway",
						"jwt.required_scopes": "read:gateway",
					},
				},
				{Action: "set_response_status", Value: "200"},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{Name: "secure-api", Path: "/v1/secure", FlowName: "secureFlow", Action: "upsert"}},
	}

	payload, _ := json.Marshal(syncReq)
	httpReq := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	httpResp := httptest.NewRecorder()
	server.UnifiedSyncHandler(httpResp, httpReq)
	if httpResp.Code != http.StatusOK {
		t.Fatalf("expected status 200 from /sync, got %d body=%s", httpResp.Code, httpResp.Body.String())
	}

	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/secure")
	if apiID == 0 {
		t.Fatalf("expected /v1/secure route to be present")
	}

	validToken := signRS256JWT(t, priv, kid, map[string]any{
		"iss":   "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
		"aud":   "https://local-gateway",
		"scope": "read:gateway write:other",
		"exp":   time.Now().Add(2 * time.Minute).Unix(),
		"nbf":   time.Now().Add(-1 * time.Minute).Unix(),
	})

	tests := []struct {
		name      string
		authz     string
		wantCode  int
		wantError bool
	}{
		{name: "valid token", authz: "Bearer " + validToken, wantCode: http.StatusOK},
		{name: "tampered token", authz: "Bearer " + tamperPayload(validToken), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "audience mismatch", authz: "Bearer " + signRS256JWT(t, priv, kid, map[string]any{
			"iss":   "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
			"aud":   "https://wrong-audience",
			"scope": "read:gateway",
			"exp":   time.Now().Add(2 * time.Minute).Unix(),
		}), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "required scope missing", authz: "Bearer " + signRS256JWT(t, priv, kid, map[string]any{
			"iss":   "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
			"aud":   "https://local-gateway",
			"scope": "write:gateway",
			"exp":   time.Now().Add(2 * time.Minute).Unix(),
		}), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "issuer missing", authz: "Bearer " + signRS256JWT(t, priv, kid, map[string]any{
			"aud":   "https://local-gateway",
			"scope": "read:gateway",
			"exp":   time.Now().Add(2 * time.Minute).Unix(),
		}), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "expired token", authz: "Bearer " + signRS256JWT(t, priv, kid, map[string]any{
			"iss":   "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
			"aud":   "https://local-gateway",
			"scope": "read:gateway",
			"exp":   time.Now().Add(-1 * time.Minute).Unix(),
		}), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "not before in future", authz: "Bearer " + signRS256JWT(t, priv, kid, map[string]any{
			"iss":   "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
			"aud":   "https://local-gateway",
			"scope": "read:gateway",
			"exp":   time.Now().Add(2 * time.Minute).Unix(),
			"nbf":   time.Now().Add(2 * time.Minute).Unix(),
		}), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "missing authorization header", authz: "", wantCode: http.StatusUnauthorized, wantError: true},
		{name: "invalid bearer format", authz: "Token " + validToken, wantCode: http.StatusUnauthorized, wantError: true},
		{name: "invalid jwt format", authz: "Bearer invalid.token", wantCode: http.StatusUnauthorized, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtimeReq := httptest.NewRequest(http.MethodGet, "http://localhost/v1/secure", nil)
			if tc.authz != "" {
				runtimeReq.Header.Set("Authorization", tc.authz)
			}
			runtimeResp := httptest.NewRecorder()

			ctx := fm.GetContext()
			defer fm.Pool.Put(ctx)
			ctx.Reset(runtimeResp)
			ctx.ApiId = apiID
			ctx.SnapshotMetadata(runtimeReq.Method, runtimeReq.URL.Path, runtimeReq.URL.RawQuery)
			fm.ProcessRequest(ctx, runtimeReq)

			if ctx.ResponseStatus != tc.wantCode {
				t.Fatalf("expected status %d, got %d", tc.wantCode, ctx.ResponseStatus)
			}
			if tc.wantError {
				if got := strings.TrimSpace(runtimeResp.Body.String()); got != "unauthorized" {
					t.Fatalf("expected unauthorized body, got %q", got)
				}
			}
		})
	}
}

func TestGatewayIntegration_TokenValidationLiveAuth0(t *testing.T) {
	if os.Getenv("RUN_LIVE_AUTH0_TEST") != "1" {
		t.Skip("set RUN_LIVE_AUTH0_TEST=1 to run live Auth0 integration test")
	}

	tokenURL := strings.TrimSpace(os.Getenv("AUTH0_TOKEN_URL"))
	clientID := strings.TrimSpace(os.Getenv("AUTH0_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("AUTH0_CLIENT_SECRET"))
	audience := strings.TrimSpace(os.Getenv("AUTH0_AUDIENCE"))
	issuer := strings.TrimSpace(os.Getenv("AUTH0_ISSUER"))
	jwksURL := strings.TrimSpace(os.Getenv("AUTH0_JWKS_URL"))

	if tokenURL == "" || clientID == "" || clientSecret == "" || audience == "" || issuer == "" {
		t.Skip("missing required env vars: AUTH0_TOKEN_URL, AUTH0_CLIENT_ID, AUTH0_CLIENT_SECRET, AUTH0_AUDIENCE, AUTH0_ISSUER")
	}
	if jwksURL == "" {
		jwksURL = strings.TrimSuffix(issuer, "/") + "/.well-known/jwks.json"
	}

	accessToken := fetchClientCredentialsToken(t, tokenURL, clientID, clientSecret, audience)

	t.Run("valid live token accepted", func(t *testing.T) {
		fm, apiID := setupTokenValidationFlow(t, jwksURL, "/v1/secure/live-auth0-valid", map[string]string{
			"jwt.jwks_url": jwksURL,
			"jwt.issuer":   issuer,
			"jwt.audience": audience,
		})
		assertTokenValidationResult(t, fm, apiID, "/v1/secure/live-auth0-valid", "Bearer "+accessToken, http.StatusOK, false)
	})

	t.Run("live token rejected for wrong audience config", func(t *testing.T) {
		fm, apiID := setupTokenValidationFlow(t, jwksURL, "/v1/secure/live-auth0-wrong-aud", map[string]string{
			"jwt.jwks_url": jwksURL,
			"jwt.issuer":   issuer,
			"jwt.audience": audience + "-mismatch",
		})
		assertTokenValidationResult(t, fm, apiID, "/v1/secure/live-auth0-wrong-aud", "Bearer "+accessToken, http.StatusUnauthorized, true)
	})
}

func newTestJWKS(t *testing.T) (*rsa.PrivateKey, string, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	kid := "gateway-kid-1"
	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
		"alg": "RS256",
		"use": "sig",
	}}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(jwksServer.Close)
	return priv, kid, jwksServer.URL
}

func newJWKSWithKeys(t *testing.T, keys []map[string]string) string {
	t.Helper()
	jwks := map[string]any{"keys": keys}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(jwksServer.Close)
	return jwksServer.URL
}

func jwkFromPublicKey(kid string, pub *rsa.PublicKey) map[string]string {
	return map[string]string{
		"kty": "RSA",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		"alg": "RS256",
		"use": "sig",
	}
}

func fetchClientCredentialsToken(t *testing.T, tokenURL, clientID, clientSecret, audience string) string {
	t.Helper()
	payload := map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"audience":      audience,
		"grant_type":    "client_credentials",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal oauth token payload: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create oauth token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request oauth token: %v", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode oauth token response: %v", err)
	}
	if resp.StatusCode != http.StatusOK || tokenResp.AccessToken == "" {
		t.Fatalf("expected oauth token response with access token, got status=%d token_type=%q", resp.StatusCode, tokenResp.TokenType)
	}
	return tokenResp.AccessToken
}

func setupTokenValidationFlow(t *testing.T, jwksURL string, path string, input map[string]string) (*engine.FlowManager, uint32) {
	t.Helper()
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})
	compiler := NewCompiler(fm)
	registry := NewNameRegistry()
	server := NewManagementServer(fm, compiler, registry, nil)

	clonedInput := map[string]string{"jwt.jwks_url": jwksURL}
	for k, v := range input {
		clonedInput[k] = v
	}
	syncReq := UnifiedSyncRequest{
		SyncUUID: "token-validation-setup-" + strings.ReplaceAll(path, "/", "-"),
		Flows: []FlowUpdate{{
			Name: "flow-" + strings.ReplaceAll(path, "/", "-"),
			Instructions: []StepConfig{
				{
					Action:        "token_validation",
					KeyIdentifier: "header.Authorization",
					Input:         clonedInput,
				},
				{Action: "set_response_status", Value: "200"},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{
			Name:     "api-" + strings.ReplaceAll(path, "/", "-"),
			Path:     path,
			FlowName: "flow-" + strings.ReplaceAll(path, "/", "-"),
			Action:   "upsert",
		}},
	}

	payload, _ := json.Marshal(syncReq)
	httpReq := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	httpResp := httptest.NewRecorder()
	server.UnifiedSyncHandler(httpResp, httpReq)
	if httpResp.Code != http.StatusOK {
		t.Fatalf("expected status 200 from /sync, got %d body=%s", httpResp.Code, httpResp.Body.String())
	}

	state := fm.State.Load()
	apiID := state.Router.Lookup(path)
	if apiID == 0 {
		t.Fatalf("expected %s route to be present", path)
	}
	return fm, apiID
}

func assertTokenValidationResult(t *testing.T, fm *engine.FlowManager, apiID uint32, path string, authz string, wantCode int, wantError bool) {
	t.Helper()
	runtimeReq := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	if authz != "" {
		runtimeReq.Header.Set("Authorization", authz)
	}
	runtimeResp := httptest.NewRecorder()

	ctx := fm.GetContext()
	defer fm.Pool.Put(ctx)
	ctx.Reset(runtimeResp)
	ctx.ApiId = apiID
	ctx.SnapshotMetadata(runtimeReq.Method, runtimeReq.URL.Path, runtimeReq.URL.RawQuery)
	fm.ProcessRequest(ctx, runtimeReq)

	if ctx.ResponseStatus != wantCode {
		t.Fatalf("expected status %d, got %d", wantCode, ctx.ResponseStatus)
	}
	if wantError {
		if got := strings.TrimSpace(runtimeResp.Body.String()); got != "unauthorized" {
			t.Fatalf("expected unauthorized body, got %q", got)
		}
	}
}

func signRS256JWT(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
	hdrJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	hdr := base64.RawURLEncoding.EncodeToString(hdrJSON)
	pl := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := hdr + "." + pl
	h := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func tamperPayload(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return token + "x"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 {
		return token + "x"
	}
	payload[0] ^= 1
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	return strings.Join(parts, ".")
}
