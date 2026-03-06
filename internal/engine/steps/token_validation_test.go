package steps

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
	"strings"
	"testing"
	"time"
)

type fakeJWTCacheProvider struct {
	store map[string][]byte
}

type fakeJWKSResolver struct {
	uri string
}

func (r fakeJWKSResolver) ResolveJWKSURI(ctx *rctx.Context, cfg TokenValidationConfig) (string, error) {
	_ = ctx
	_ = cfg
	return r.uri, nil
}

type headerBasedJWKSResolver struct {
	uris map[string]string
}

func (r headerBasedJWKSResolver) ResolveJWKSURI(ctx *rctx.Context, cfg TokenValidationConfig) (string, error) {
	_ = cfg
	if ctx.Request == nil {
		return "", nil
	}
	return r.uris[ctx.Request.Header.Get("X-Issuer")], nil
}

func (f *fakeJWTCacheProvider) Get(key string) ([]byte, bool) {
	v, ok := f.store[key]
	return v, ok
}

func (f *fakeJWTCacheProvider) Set(key string, value []byte, _ time.Duration) {
	if f.store == nil {
		f.store = map[string][]byte{}
	}
	f.store[key] = value
}

func TestTokenValidationAcceptsValidRS256JWTFromJWKS(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	jwks := map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": "kid-1",
			"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
			"alg": "RS256",
			"use": "sig",
		}},
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	issuer := "https://demo.okta.com/oauth2/default"
	audience := "api://rah"
	token := signJWT(t, priv, "kid-1", map[string]any{
		"iss": issuer,
		"aud": audience,
		"exp": time.Now().Add(2 * time.Minute).Unix(),
		"nbf": time.Now().Add(-1 * time.Minute).Unix(),
	})

	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	defer fm.Pool.Put(ctx)

	resp := httptest.NewRecorder()
	ctx.Reset(resp)
	ctx.ByteSlots[10] = []byte("Bearer " + token)

	instr := TokenValidation(10, ParseTokenValidationConfig("header.Authorization", map[string]string{
		"jwt.jwks_url": jwksServer.URL,
		"jwt.issuer":   issuer,
		"jwt.audience": audience,
	}))

	next := instr.Action(ctx, &engine.ExecutionState{PC: 5})
	if next != 6 {
		t.Fatalf("expected next pc 6 for valid token, got %d", next)
	}
	if resp.Body.Len() != 0 {
		t.Fatalf("expected no response body for valid token, got %q", resp.Body.String())
	}
}

func TestTokenValidationRejectsInvalidSignature(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	otherPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	jwks := map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": "kid-1",
			"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
			"alg": "RS256",
			"use": "sig",
		}},
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	token := signJWT(t, otherPriv, "kid-1", map[string]any{
		"exp": time.Now().Add(2 * time.Minute).Unix(),
	})

	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	defer fm.Pool.Put(ctx)

	resp := httptest.NewRecorder()
	ctx.Reset(resp)
	ctx.ByteSlots[10] = []byte("Bearer " + token)

	instr := TokenValidation(10, ParseTokenValidationConfig("header.Authorization", map[string]string{"jwt.jwks_url": jwksServer.URL}))

	next := instr.Action(ctx, &engine.ExecutionState{PC: 5})
	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan for invalid signature, got %d", next)
	}
	if ctx.ResponseStatus != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", ctx.ResponseStatus)
	}
	if got := strings.TrimSpace(resp.Body.String()); got != "unauthorized" {
		t.Fatalf("expected unauthorized body, got %q", got)
	}
}

func TestTokenValidationAllowsSkippingAudienceValidation(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	jwks := map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": "kid-1",
			"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
		}},
	}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	token := signJWT(t, priv, "kid-1", map[string]any{
		"iss": "issuer",
		"aud": "unexpected-audience",
		"exp": time.Now().Add(2 * time.Minute).Unix(),
	})

	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	defer fm.Pool.Put(ctx)

	resp := httptest.NewRecorder()
	ctx.Reset(resp)
	ctx.ByteSlots[10] = []byte("Bearer " + token)

	instr := TokenValidation(10, ParseTokenValidationConfig("header.Authorization", map[string]string{
		"jwt.jwks_uri": jwksServer.URL,
		"jwt.audience": "required-audience",
		"jwt.validate": "signature,exp",
	}))

	next := instr.Action(ctx, &engine.ExecutionState{PC: 1})
	if next != 2 {
		t.Fatalf("expected success when audience validation is disabled, got %d", next)
	}
}

func TestTokenValidationUsesExternalJWTCacheProvider(t *testing.T) {
	provider := &fakeJWTCacheProvider{}
	SetJWTCacheProvider(provider)
	defer SetJWTCacheProvider(nil)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	jwksPayload, _ := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": "kid-1",
			"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
		}},
	})

	provider.Set("jwks:https://issuer.example/jwks", jwksPayload, time.Minute)
	token := signJWT(t, priv, "kid-1", map[string]any{"exp": time.Now().Add(2 * time.Minute).Unix()})

	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	defer fm.Pool.Put(ctx)

	resp := httptest.NewRecorder()
	ctx.Reset(resp)
	ctx.ByteSlots[10] = []byte("Bearer " + token)

	instr := TokenValidation(10, ParseTokenValidationConfig("header.Authorization", map[string]string{"jwt.jwks_uri": "https://issuer.example/jwks"}))
	next := instr.Action(ctx, &engine.ExecutionState{PC: 2})
	if next != 3 {
		t.Fatalf("expected cache-backed validation success, got %d", next)
	}
}

func TestTokenValidationResolvesJWKSURIFromReference(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": "kid-1",
		"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
	}}}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	SetJWKSURIResolver(fakeJWKSResolver{uri: jwksServer.URL})
	defer SetJWKSURIResolver(nil)

	token := signJWT(t, priv, "kid-1", map[string]any{"exp": time.Now().Add(2 * time.Minute).Unix()})

	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	defer fm.Pool.Put(ctx)

	resp := httptest.NewRecorder()
	ctx.Reset(resp)
	ctx.ByteSlots[10] = []byte("Bearer " + token)

	instr := TokenValidation(10, ParseTokenValidationConfig("header.Authorization", map[string]string{"jwt.jwks_ref": "tenant/default/oidc"}))
	next := instr.Action(ctx, &engine.ExecutionState{PC: 7})
	if next != 8 {
		t.Fatalf("expected resolver-backed validation success, got %d", next)
	}
}

func TestTokenValidationSupportsDifferentJWKSPerRequest(t *testing.T) {
	privA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key A: %v", err)
	}
	privB, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key B: %v", err)
	}

	serverForKey := func(priv *rsa.PrivateKey) *httptest.Server {
		jwks := map[string]any{"keys": []map[string]string{{
			"kty": "RSA",
			"kid": "kid-1",
			"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
		}}}
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(jwks)
		}))
	}

	jwksA := serverForKey(privA)
	defer jwksA.Close()
	jwksB := serverForKey(privB)
	defer jwksB.Close()

	SetJWKSURIResolver(headerBasedJWKSResolver{uris: map[string]string{
		"issuer-a": jwksA.URL,
		"issuer-b": jwksB.URL,
	}})
	defer SetJWKSURIResolver(nil)

	instr := TokenValidation(10, ParseTokenValidationConfig("header.Authorization", map[string]string{"jwt.jwks_ref": "tenant/provider"}))

	run := func(priv *rsa.PrivateKey, issuer string, pc int16) int16 {
		token := signJWT(t, priv, "kid-1", map[string]any{"exp": time.Now().Add(2 * time.Minute).Unix()})
		fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
		ctx := fm.Pool.Get().(*rctx.Context)
		defer fm.Pool.Put(ctx)

		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://localhost/v1/users", nil)
		req.Header.Set("X-Issuer", issuer)

		ctx.Reset(resp)
		ctx.Request = req
		ctx.ByteSlots[10] = []byte("Bearer " + token)
		return instr.Action(ctx, &engine.ExecutionState{PC: pc})
	}

	if next := run(privA, "issuer-a", 10); next != 11 {
		t.Fatalf("expected issuer-a request to validate with jwksA, got next=%d", next)
	}
	if next := run(privB, "issuer-b", 20); next != 21 {
		t.Fatalf("expected issuer-b request to validate with jwksB, got next=%d", next)
	}
}

func signJWT(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	head := map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
	headBytes, _ := json.Marshal(head)
	claimsBytes, _ := json.Marshal(claims)

	headEnc := base64.RawURLEncoding.EncodeToString(headBytes)
	claimsEnc := base64.RawURLEncoding.EncodeToString(claimsBytes)
	signed := headEnc + "." + claimsEnc

	h := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestTokenValidationReadsTokenDirectlyFromHeaderSource(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": "kid-1",
		"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
	}}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	token := signJWT(t, priv, "kid-1", map[string]any{"exp": time.Now().Add(2 * time.Minute).Unix()})
	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	defer fm.Pool.Put(ctx)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/v1/users", nil)
	req.Header.Set("X-Access-Token", token)
	ctx.Reset(resp)
	ctx.Request = req

	instr := TokenValidation(10, ParseTokenValidationConfig("header.Authorization", map[string]string{
		"token.source": "header",
		"token.key":    "X-Access-Token",
		"jwt.jwks_uri": jwksServer.URL,
	}))

	if next := instr.Action(ctx, &engine.ExecutionState{PC: 30}); next != 31 {
		t.Fatalf("expected header-source token validation success, got %d", next)
	}
}

func TestTokenValidationRejectsWhenRequiredScopeMissing(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": "kid-1",
		"n":   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
	}}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	token := signJWT(t, priv, "kid-1", map[string]any{
		"exp":   time.Now().Add(2 * time.Minute).Unix(),
		"scope": "read:users",
	})

	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	defer fm.Pool.Put(ctx)
	resp := httptest.NewRecorder()
	ctx.Reset(resp)
	ctx.ByteSlots[10] = []byte("Bearer " + token)

	instr := TokenValidation(10, ParseTokenValidationConfig("header.Authorization", map[string]string{
		"jwt.jwks_uri":        jwksServer.URL,
		"jwt.required_scopes": "read:users,write:users",
	}))

	next := instr.Action(ctx, &engine.ExecutionState{PC: 1})
	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan when required scope missing, got %d", next)
	}
	if ctx.ResponseStatus != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", ctx.ResponseStatus)
	}
}
