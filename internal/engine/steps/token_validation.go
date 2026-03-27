package steps

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"rah/internal/engine"
	"rah/internal/rctx"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultJWTLeeway    = 30 * time.Second
	defaultJWKSCacheTTL = 5 * time.Minute
)

// TokenReadSource controls where token bytes are pulled from at runtime.
//
// Why this exists:
// - JWT payloads can be large (KB-level with many scopes/claims)
// - customers may store token in different request locations
// - runtime should support direct reads from request object, not only slots
type TokenReadSource uint8

const (
	TokenReadFromSlot TokenReadSource = iota
	TokenReadFromHeader
	TokenReadFromQuery
	TokenReadFromCookie
)

// TokenValidationConfig is a typed, precompiled config for JWT verification.
//
// Hot-path design:
// - input map is parsed once during compilation
// - Action closure reads typed fields only
// - no map parsing in request loop
//
// Slot note:
//   - Context.ByteSlots is [][]byte and each entry can reference variable-length bytes.
//   - Slot count is fixed by MaxBytesSlots, but payload byte-length per slot is not fixed.
//   - For very large tokens, prefer direct request-source reads (header/query/cookie)
//     to avoid unnecessary bind/copy steps.
type TokenValidationConfig struct {
	TokenRefKey string
	TokenSource TokenReadSource

	JWKSURI       string
	JWKSIssuerRef string
	Issuer        string
	Audience      string
	Algorithm     string
	Leeway        time.Duration
	Validate      ValidationSet
	PrefetchJWKS  bool

	RequiredScopes []string
	ScopeClaimKeys []string // default: scope, scp
}

// ValidationSet allows selecting claim/signature checks.
// Missing selection defaults to "all enabled".
type ValidationSet struct {
	Signature bool
	Issuer    bool
	Audience  bool
	Expiry    bool
	NotBefore bool
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Issuer    string         `json:"iss"`
	Audience  any            `json:"aud"`
	ExpiresAt int64          `json:"exp"`
	NotBefore int64          `json:"nbf"`
	Extra     map[string]any `json:"-"`
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type cachedJWKS struct {
	keysByKid map[string]*rsa.PublicKey
	expiresAt time.Time
}

// JWTCacheProvider allows delegating JWKS payload caching policy.
type JWTCacheProvider interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl time.Duration)
}

// JWKSURIResolver resolves URI dynamically for tenant/provider-specific refs.
type JWKSURIResolver interface {
	ResolveJWKSURI(ctx *rctx.Context, cfg TokenValidationConfig) (string, error)
}

var (
	jwksCache            sync.Map
	jwtCacheProvider     JWTCacheProvider
	jwtCacheProviderLock sync.RWMutex
	jwksURIResolver      JWKSURIResolver
	jwksResolverLock     sync.RWMutex
)

func SetJWTCacheProvider(provider JWTCacheProvider) {
	jwtCacheProviderLock.Lock()
	defer jwtCacheProviderLock.Unlock()
	jwtCacheProvider = provider
}

func SetJWKSURIResolver(resolver JWKSURIResolver) {
	jwksResolverLock.Lock()
	defer jwksResolverLock.Unlock()
	jwksURIResolver = resolver
}

// ParseTokenValidationConfig converts flow step config into typed runtime config.
// keyIdentifier is the step-level key_identifier (legacy/default token ref).
func ParseTokenValidationConfig(keyIdentifier string, input map[string]string) TokenValidationConfig {
	cfg := TokenValidationConfig{
		TokenRefKey:    strings.TrimSpace(keyIdentifier),
		TokenSource:    parseTokenSource(input["token.source"]),
		JWKSURI:        strings.TrimSpace(input["jwt.jwks_uri"]),
		JWKSIssuerRef:  strings.TrimSpace(input["jwt.jwks_ref"]),
		Issuer:         strings.TrimSpace(input["jwt.issuer"]),
		Audience:       strings.TrimSpace(input["jwt.audience"]),
		Algorithm:      strings.TrimSpace(input["jwt.alg"]),
		Leeway:         defaultJWTLeeway,
		Validate:       parseValidationSet(input["jwt.validate"]),
		PrefetchJWKS:   strings.EqualFold(strings.TrimSpace(input["jwt.prefetch_jwks"]), "true"),
		RequiredScopes: splitAndTrim(input["jwt.required_scopes"], ","),
		ScopeClaimKeys: splitAndTrim(input["jwt.scope_claims"], ","),
	}

	if k := strings.TrimSpace(input["token.key"]); k != "" {
		cfg.TokenRefKey = k
	}
	if cfg.JWKSURI == "" {
		cfg.JWKSURI = strings.TrimSpace(input["jwt.jwks_url"])
	}
	if cfg.Algorithm == "" {
		cfg.Algorithm = "RS256"
	}
	if len(cfg.ScopeClaimKeys) == 0 {
		cfg.ScopeClaimKeys = []string{"scope", "scp"}
	}
	if rawLeeway := strings.TrimSpace(input["jwt.leeway_seconds"]); rawLeeway != "" {
		if sec, err := strconv.Atoi(rawLeeway); err == nil && sec >= 0 {
			cfg.Leeway = time.Duration(sec) * time.Second
		}
	}
	if !cfg.Validate.Signature && !cfg.Validate.Issuer && !cfg.Validate.Audience && !cfg.Validate.Expiry && !cfg.Validate.NotBefore {
		cfg.Validate = ValidationSet{Signature: true, Issuer: true, Audience: true, Expiry: true, NotBefore: true}
	}
	return cfg
}

func parseTokenSource(raw string) TokenReadSource {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "header":
		return TokenReadFromHeader
	case "query":
		return TokenReadFromQuery
	case "cookie":
		return TokenReadFromCookie
	default:
		return TokenReadFromSlot
	}
}

func splitAndTrim(raw, sep string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseValidationSet(raw string) ValidationSet {
	if strings.TrimSpace(raw) == "" {
		return ValidationSet{Signature: true, Issuer: true, Audience: true, Expiry: true, NotBefore: true}
	}
	set := ValidationSet{}
	for item := range strings.SplitSeq(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "signature", "sig":
			set.Signature = true
		case "issuer", "iss":
			set.Issuer = true
		case "audience", "aud":
			set.Audience = true
		case "expiry", "exp":
			set.Expiry = true
		case "not_before", "nbf":
			set.NotBefore = true
		case "all", "*":
			return ValidationSet{Signature: true, Issuer: true, Audience: true, Expiry: true, NotBefore: true}
		}
	}
	return set
}

// TokenValidation builds the runtime JWT validation instruction.
func TokenValidation(tokenSlot int, cfg TokenValidationConfig) engine.Instruction {
	if cfg.PrefetchJWKS && cfg.JWKSURI != "" {
		_, _ = publicKeyFromJWKS(cfg.JWKSURI, "")
	}

	return engine.Instruction{
		Name: "TOKEN_VALIDATE",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			token := strings.TrimSpace(readToken(ctx, tokenSlot, cfg))
			token = extractBearerOrRaw(token)
			if token == "" {
				return rejectUnauthorized(ctx)
			}

			resolvedCfg := cfg
			if resolvedCfg.JWKSURI == "" && resolvedCfg.JWKSIssuerRef != "" {
				if uri, err := resolveJWKSURI(ctx, resolvedCfg); err == nil {
					resolvedCfg.JWKSURI = uri
				}
			}
			if err := validateJWT(token, resolvedCfg); err != nil {
				return rejectUnauthorized(ctx)
			}
			return s.PC + 1
		},
	}
}

func readToken(ctx *rctx.Context, tokenSlot int, cfg TokenValidationConfig) string {
	switch cfg.TokenSource {
	case TokenReadFromHeader:
		if ctx.Request == nil || cfg.TokenRefKey == "" {
			return ""
		}
		return ctx.Request.Header.Get(cfg.TokenRefKey)
	case TokenReadFromQuery:
		if ctx.Request == nil || cfg.TokenRefKey == "" {
			return ""
		}
		return ctx.Request.URL.Query().Get(cfg.TokenRefKey)
	case TokenReadFromCookie:
		if ctx.Request == nil || cfg.TokenRefKey == "" {
			return ""
		}
		c, err := ctx.Request.Cookie(cfg.TokenRefKey)
		if err != nil {
			return ""
		}
		return c.Value
	default:
		if tokenSlot < 0 || tokenSlot >= len(ctx.ByteSlots) {
			return ""
		}
		return string(ctx.ByteSlots[tokenSlot])
	}
}

func resolveJWKSURI(ctx *rctx.Context, cfg TokenValidationConfig) (string, error) {
	jwksResolverLock.RLock()
	resolver := jwksURIResolver
	jwksResolverLock.RUnlock()
	if resolver == nil {
		return "", errors.New("jwks resolver is not configured")
	}
	return resolver.ResolveJWKSURI(ctx, cfg)
}

func rejectUnauthorized(ctx *rctx.Context) int16 {
	ctx.ResponseStatus = http.StatusUnauthorized
	ctx.Write([]byte("unauthorized"))
	ctx.Failed = true
	ctx.ErrorCode = 401
	ctx.ErrorMsg = ctx.Alloc(len("unauthorized"))
	copy(ctx.ErrorMsg, "unauthorized")
	return engine.StopPlan
}

func extractBearerOrRaw(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		return strings.TrimSpace(raw[7:])
	}
	return raw
}

func validateJWT(token string, cfg TokenValidationConfig) error {
	dot1 := strings.IndexByte(token, '.')
	if dot1 <= 0 {
		return errors.New("invalid jwt format")
	}
	dot2Rel := strings.IndexByte(token[dot1+1:], '.')
	if dot2Rel <= 0 {
		return errors.New("invalid jwt format")
	}
	dot2 := dot1 + 1 + dot2Rel

	headerSeg, claimsSeg, sigSeg := token[:dot1], token[dot1+1:dot2], token[dot2+1:]
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerSeg)
	if err != nil {
		return err
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(claimsSeg)
	if err != nil {
		return err
	}

	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return err
	}
	if header.Alg != cfg.Algorithm || header.Alg != "RS256" {
		return errors.New("unsupported jwt algorithm")
	}

	var claims jwtClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return err
	}
	if err := json.Unmarshal(claimsBytes, &claims.Extra); err != nil {
		return err
	}

	now := time.Now()
	if cfg.Validate.Issuer && cfg.Issuer != "" && claims.Issuer != cfg.Issuer {
		return errors.New("issuer mismatch")
	}
	if cfg.Validate.Audience && cfg.Audience != "" && !audienceMatches(claims.Audience, cfg.Audience) {
		return errors.New("audience mismatch")
	}
	if cfg.Validate.Expiry && claims.ExpiresAt > 0 && now.After(time.Unix(claims.ExpiresAt, 0).Add(cfg.Leeway)) {
		return errors.New("token expired")
	}
	if cfg.Validate.NotBefore && claims.NotBefore > 0 && now.Before(time.Unix(claims.NotBefore, 0).Add(-cfg.Leeway)) {
		return errors.New("token not active")
	}
	if len(cfg.RequiredScopes) > 0 {
		if err := validateRequiredScopes(claims.Extra, cfg.ScopeClaimKeys, cfg.RequiredScopes); err != nil {
			return err
		}
	}

	if cfg.Validate.Signature {
		if cfg.JWKSURI == "" {
			return errors.New("jwt.jwks_uri is required")
		}
		if header.Kid == "" {
			return errors.New("missing kid")
		}
		pub, err := publicKeyFromJWKS(cfg.JWKSURI, header.Kid)
		if err != nil {
			return err
		}
		signature, err := base64.RawURLEncoding.DecodeString(sigSeg)
		if err != nil {
			return err
		}
		h := sha256.Sum256([]byte(token[:dot2]))
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], signature); err != nil {
			return err
		}
	}

	return nil
}

func validateRequiredScopes(claims map[string]any, claimKeys []string, required []string) error {
	present := map[string]struct{}{}
	for _, k := range claimKeys {
		v, ok := claims[k]
		if !ok {
			continue
		}
		extractScopesIntoSet(v, present)
	}
	for _, req := range required {
		if _, ok := present[req]; !ok {
			return fmt.Errorf("required scope missing: %s", req)
		}
	}
	return nil
}

func extractScopesIntoSet(v any, set map[string]struct{}) {
	switch s := v.(type) {
	case string:
		for scope := range strings.FieldsSeq(s) {
			set[scope] = struct{}{}
		}
	case []any:
		for _, item := range s {
			if sv, ok := item.(string); ok {
				for scope := range strings.FieldsSeq(sv) {
					set[scope] = struct{}{}
				}
			}
		}
	}
}

func audienceMatches(tokenAud any, expected string) bool {
	switch a := tokenAud.(type) {
	case string:
		return a == expected
	case []any:
		for _, v := range a {
			if s, ok := v.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}

func publicKeyFromJWKS(jwksURI, kid string) (*rsa.PublicKey, error) {
	if cachedAny, ok := jwksCache.Load(jwksURI); ok {
		cached := cachedAny.(cachedJWKS)
		if time.Now().Before(cached.expiresAt) {
			if kid == "" {
				return nil, nil
			}
			if key, exists := cached.keysByKid[kid]; exists {
				return key, nil
			}
		}
	}

	payload, err := loadJWKSBytes(jwksURI)
	if err != nil {
		return nil, err
	}

	var jwks jwksDocument
	if err := json.Unmarshal(payload, &jwks); err != nil {
		return nil, err
	}

	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if strings.ToUpper(k.Kty) != "RSA" || k.Kid == "" || k.N == "" || k.E == "" {
			continue
		}
		pub, err := buildRSAPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}

	jwksCache.Store(jwksURI, cachedJWKS{keysByKid: keys, expiresAt: time.Now().Add(defaultJWKSCacheTTL)})
	if kid == "" {
		return nil, nil
	}
	if key, ok := keys[kid]; ok {
		return key, nil
	}
	return nil, errors.New("kid not found in jwks")
}

func loadJWKSBytes(jwksURI string) ([]byte, error) {
	jwtCacheProviderLock.RLock()
	provider := jwtCacheProvider
	jwtCacheProviderLock.RUnlock()
	cacheKey := "jwks:" + jwksURI

	if provider != nil {
		if payload, ok := provider.Get(cacheKey); ok && len(payload) > 0 {
			return payload, nil
		}
	}

	resp, err := http.Get(jwksURI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("jwks fetch failed")
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if provider != nil {
		provider.Set(cacheKey, payload, defaultJWKSCacheTTL)
	}
	return payload, nil
}

func buildRSAPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eBytes {
		e = (e << 8) | int(b)
	}
	if e == 0 {
		return nil, errors.New("invalid rsa exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}
