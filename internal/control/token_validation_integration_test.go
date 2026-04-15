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
	"strings"
	"testing"
	"time"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
)

func TestGatewayIntegration_TokenValidationSignatureAudienceAndScopes(t *testing.T) {
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
		token     string
		wantCode  int
		wantError bool
	}{
		{name: "valid token", token: validToken, wantCode: http.StatusOK},
		{name: "tampered token", token: tamperPayload(validToken), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "audience mismatch", token: signRS256JWT(t, priv, kid, map[string]any{
			"iss":   "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
			"aud":   "https://wrong-audience",
			"scope": "read:gateway",
			"exp":   time.Now().Add(2 * time.Minute).Unix(),
		}), wantCode: http.StatusUnauthorized, wantError: true},
		{name: "required scope missing", token: signRS256JWT(t, priv, kid, map[string]any{
			"iss":   "https://dev-ew23s35bshjhy1jl.us.auth0.com/",
			"aud":   "https://local-gateway",
			"scope": "write:gateway",
			"exp":   time.Now().Add(2 * time.Minute).Unix(),
		}), wantCode: http.StatusUnauthorized, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtimeReq := httptest.NewRequest(http.MethodGet, "http://localhost/v1/secure", nil)
			runtimeReq.Header.Set("Authorization", "Bearer "+tc.token)
			runtimeResp := httptest.NewRecorder()

			ctx := fm.Pool.Get().(*rctx.Context)
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
