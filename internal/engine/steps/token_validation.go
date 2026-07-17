package steps

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultJWTLeeway    = 30 * time.Second
	defaultJWKSCacheTTL = 12 * time.Hour
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

// TokenValidationSlots holds runtime slot indexes for token_validation.
// -1 means "not configured â€” use static value from TokenValidationConfig".
type TokenValidationSlots struct {
	Token          int
	JWKSURI        int
	Algorithm      int
	Leeway         int
	ValidateSet    int // comma-sep string â†’ parsed into ValidationSet at runtime
	Issuer         int
	Audience       int
	RequiredScopes int // comma-sep string â†’ split at runtime
	ScopeClaimKeys int
	OnFailureMode  int // "stop" or "continue"
	FailureStatus  int // string â†’ parsed as int
	FailureBody    int
	ResultSuccess  int // value to write to Result slot on success
	ResultFailure  int // value to write to Result slot on failure
	Result         int // where to write result value
	Claims         int // where to write claims JSON on success
	Subject        int // where to write jwt "sub" claim on success
	ClientID       int // where to write client_id / azp / appid claim on success
	ScopesOut      int // where to write comma-sep parsed scopes on success
	CustomClaimsVars map[string]int // per-claim key â†’ slot for expected value
}

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
	PrefetchJWKS         bool
	JWKSTimeout          time.Duration // timeout for JWKS endpoint fetch (default 10s)
	JWKSCacheTTL         time.Duration // how long to cache the JWKS public keys (default 12h)
	JWKSRetryMaxAttempts int           // total attempts for JWKS fetch (default 2)
	JWKSRetryBackoff     time.Duration // base backoff between JWKS retries (default 200ms)

	RequiredScopes []string
	ScopeClaimKeys []string // default: scope, scp

	OnFailureStatus   int               // default 401
	OnFailureBody     string            // default "unauthorized"
	ContinueOnFailure bool              // resolved from jwt.on_failure static value
	ResultSuccess     string            // default "true"
	ResultFailure     string            // default "false"
	CustomClaims      map[string]string // static custom claim checks
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
	Kty string `json:"kty"` // "RSA", "EC", "OKP"
	Kid string `json:"kid"`
	// RSA fields
	N string `json:"n"`
	E string `json:"e"`
	// EC fields
	Crv string `json:"crv"` // "P-256", "P-384", "P-521"
	X   string `json:"x"`
	Y   string `json:"y"`
	// OKP fields (EdDSA / Ed25519)
	// Crv is shared; X holds the public key bytes for OKP
}

// cnfClaim holds the Confirmation claim from an access token (RFC 7800 / RFC 9449).
type cnfClaim struct {
	JKT string `json:"jkt"` // JWK SHA-256 Thumbprint of the DPoP key
}

type cachedJWKS struct {
	keysByKid map[string]crypto.PublicKey // RSA, ECDSA, or ed25519 public keys
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

// FlushJWKSCache removes the cached JWKS document for a specific issuer URI,
// forcing the next token validation to re-fetch it. Use after key rotation.
func FlushJWKSCache(issuerURI string) {
	jwksCache.Delete(issuerURI)
}

// FlushAllJWKSCaches removes all cached JWKS documents across all issuers.
func FlushAllJWKSCaches() {
	jwksCache.Range(func(key, _ any) bool {
		jwksCache.Delete(key)
		return true
	})
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
		PrefetchJWKS:         strings.EqualFold(strings.TrimSpace(input["jwt.prefetch_jwks"]), "true"),
		JWKSTimeout:          10 * time.Second,
		JWKSRetryMaxAttempts: 2,
		JWKSRetryBackoff:     200 * time.Millisecond,
		RequiredScopes: splitAndTrim(input["jwt.required_scopes"], ","),
		ScopeClaimKeys: splitAndTrim(input["jwt.scope_claims"], ","),
	}
	if v := strings.TrimSpace(input["jwt.jwks_timeout_ms"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.JWKSTimeout = time.Duration(n) * time.Millisecond
		}
	}
	if v := strings.TrimSpace(input["jwt.jwks_retry_max_attempts"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.JWKSRetryMaxAttempts = n
		}
	}
	if v := strings.TrimSpace(input["jwt.jwks_retry_backoff_ms"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.JWKSRetryBackoff = time.Duration(n) * time.Millisecond
		}
	}
	if v := strings.TrimSpace(input["jwt.jwks_cache_ttl_seconds"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.JWKSCacheTTL = time.Duration(n) * time.Second
		}
	}
	if cfg.JWKSCacheTTL <= 0 {
		cfg.JWKSCacheTTL = defaultJWKSCacheTTL
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

	cfg.OnFailureStatus = func() int {
		if v := strings.TrimSpace(input["jwt.failure_status"]); v != "" {
			if n, e := strconv.Atoi(v); e == nil {
				return n
			}
		}
		return 401
	}()
	cfg.OnFailureBody = func() string {
		if v := strings.TrimSpace(input["jwt.failure_body"]); v != "" {
			return v
		}
		return "unauthorized"
	}()
	cfg.ContinueOnFailure = strings.EqualFold(strings.TrimSpace(input["jwt.on_failure"]), "continue")
	cfg.ResultSuccess = func() string {
		if v := strings.TrimSpace(input["jwt.result_success"]); v != "" {
			return v
		}
		return "true"
	}()
	cfg.ResultFailure = func() string {
		if v := strings.TrimSpace(input["jwt.result_failure"]); v != "" {
			return v
		}
		return "false"
	}()
	cfg.CustomClaims = parseJSONStringMap(input["jwt.custom_claims"])

	return cfg
}

func parseJSONStringMap(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
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
func TokenValidation(slots TokenValidationSlots, cfg TokenValidationConfig) engine.Instruction {
	if cfg.PrefetchJWKS && cfg.JWKSURI != "" {
		_, _ = publicKeyFromJWKS(cfg.JWKSURI, "", cfg.JWKSTimeout, cfg.JWKSCacheTTL, cfg.JWKSRetryMaxAttempts, cfg.JWKSRetryBackoff)
	}

	return engine.Instruction{
		Name: "TOKEN_VALIDATE",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Helper to read a slot value
			resolve := func(slotIdx int) (string, bool) {
				if slotIdx < 0 || slotIdx >= len(ctx.ByteSlots) {
					return "", false
				}
				v := string(ctx.ByteSlots[slotIdx])
				return v, v != ""
			}

			// Read token
			token := strings.TrimSpace(readToken(ctx, slots.Token, cfg))
			token = extractBearerOrRaw(token)
			if token == "" {
				// Check dynamic on-failure mode for empty token case
				emptyContinue := cfg.ContinueOnFailure
				if v, ok := resolve(slots.OnFailureMode); ok {
					emptyContinue = strings.EqualFold(v, "continue")
				}
				if emptyContinue {
					failureVal := cfg.ResultFailure
					if failureVal == "" {
						failureVal = "false"
					}
					if v, ok := resolve(slots.ResultFailure); ok {
						failureVal = v
					}
					if slots.Result >= 0 && slots.Result < len(ctx.ByteSlots) {
						ctx.ByteSlots[slots.Result] = []byte(failureVal)
					}
					return s.PC + 1
				}
				emptyStatus := cfg.OnFailureStatus
				if emptyStatus == 0 {
					emptyStatus = 401
				}
				if v, ok := resolve(slots.FailureStatus); ok {
					if n, e := strconv.Atoi(v); e == nil {
						emptyStatus = n
					}
				}
				emptyBody := cfg.OnFailureBody
				if emptyBody == "" {
					emptyBody = "unauthorized"
				}
				if v, ok := resolve(slots.FailureBody); ok {
					emptyBody = v
				}
				ctx.ResponseStatus = emptyStatus
				ctx.Write([]byte(emptyBody))
				ctx.Failed = true
				ctx.ErrorCode = int16(emptyStatus)
				ctx.ErrorMsg = ctx.Alloc(len(emptyBody))
				copy(ctx.ErrorMsg, emptyBody)
				return engine.StopPlan
			}

			// Build resolved config (override statics with runtime variable values)
			resolvedCfg := cfg

			if v, ok := resolve(slots.JWKSURI); ok {
				resolvedCfg.JWKSURI = v
			}
			if v, ok := resolve(slots.Algorithm); ok {
				resolvedCfg.Algorithm = v
			}
			if v, ok := resolve(slots.Issuer); ok {
				resolvedCfg.Issuer = v
			}
			if v, ok := resolve(slots.Audience); ok {
				resolvedCfg.Audience = v
			}
			if v, ok := resolve(slots.RequiredScopes); ok {
				resolvedCfg.RequiredScopes = splitAndTrim(v, ",")
			}
			if v, ok := resolve(slots.ScopeClaimKeys); ok {
				resolvedCfg.ScopeClaimKeys = splitAndTrim(v, ",")
			}
			if v, ok := resolve(slots.Leeway); ok {
				if sec, e := strconv.Atoi(v); e == nil {
					resolvedCfg.Leeway = time.Duration(sec) * time.Second
				}
			}
			if v, ok := resolve(slots.ValidateSet); ok {
				resolvedCfg.Validate = parseValidationSet(v)
			}

			// Dynamic on-failure mode
			continueOnFailure := resolvedCfg.ContinueOnFailure
			if v, ok := resolve(slots.OnFailureMode); ok {
				continueOnFailure = strings.EqualFold(v, "continue")
			}

			// Dynamic failure status + body
			failureStatus := resolvedCfg.OnFailureStatus
			if failureStatus == 0 {
				failureStatus = 401
			}
			if v, ok := resolve(slots.FailureStatus); ok {
				if n, e := strconv.Atoi(v); e == nil {
					failureStatus = n
				}
			}
			failureBody := resolvedCfg.OnFailureBody
			if failureBody == "" {
				failureBody = "unauthorized"
			}
			if v, ok := resolve(slots.FailureBody); ok {
				failureBody = v
			}

			// Dynamic result values
			successVal := resolvedCfg.ResultSuccess
			if successVal == "" {
				successVal = "true"
			}
			if v, ok := resolve(slots.ResultSuccess); ok {
				successVal = v
			}
			failureVal := resolvedCfg.ResultFailure
			if failureVal == "" {
				failureVal = "false"
			}
			if v, ok := resolve(slots.ResultFailure); ok {
				failureVal = v
			}

			// Dynamic custom claim expected values (merge over static map)
			if len(slots.CustomClaimsVars) > 0 {
				merged := make(map[string]string, len(resolvedCfg.CustomClaims)+len(slots.CustomClaimsVars))
				for k, v := range resolvedCfg.CustomClaims {
					merged[k] = v
				}
				for k, slotIdx := range slots.CustomClaimsVars {
					if v, ok := resolve(slotIdx); ok {
						merged[k] = v
					}
				}
				resolvedCfg.CustomClaims = merged
			}

			// Resolve JWKS URI via resolver if needed
			if resolvedCfg.JWKSURI == "" && resolvedCfg.JWKSIssuerRef != "" {
				if uri, err := resolveJWKSURI(ctx, resolvedCfg); err == nil {
					resolvedCfg.JWKSURI = uri
				}
			}

			claims, err := validateJWTWithClaims(token, resolvedCfg)
			if err != nil {
				// Failure path
				if continueOnFailure {
					if slots.Result >= 0 && slots.Result < len(ctx.ByteSlots) {
						ctx.ByteSlots[slots.Result] = []byte(failureVal)
					}
					return s.PC + 1
				}
				ctx.ResponseStatus = failureStatus
				ctx.Write([]byte(failureBody))
				ctx.Failed = true
				ctx.ErrorCode = int16(failureStatus)
				ctx.ErrorMsg = ctx.Alloc(len(failureBody))
				copy(ctx.ErrorMsg, failureBody)
				return engine.StopPlan
			}

			// Success path
			if slots.Result >= 0 && slots.Result < len(ctx.ByteSlots) {
				ctx.ByteSlots[slots.Result] = []byte(successVal)
			}
			if slots.Claims >= 0 && slots.Claims < len(ctx.ByteSlots) {
				if b, e := json.Marshal(claims.Extra); e == nil {
					ctx.ByteSlots[slots.Claims] = b
				}
			}
			if slots.Subject >= 0 && slots.Subject < len(ctx.ByteSlots) {
				if sub, ok := claims.Extra["sub"].(string); ok && sub != "" {
					ctx.ByteSlots[slots.Subject] = []byte(sub)
				}
			}
			if slots.ClientID >= 0 && slots.ClientID < len(ctx.ByteSlots) {
				for _, k := range []string{"client_id", "azp", "appid"} {
					if v, ok := claims.Extra[k].(string); ok && v != "" {
						ctx.ByteSlots[slots.ClientID] = []byte(v)
						break
					}
				}
			}
			if slots.ScopesOut >= 0 && slots.ScopesOut < len(ctx.ByteSlots) {
				present := map[string]struct{}{}
				for _, k := range resolvedCfg.ScopeClaimKeys {
					if v, ok := claims.Extra[k]; ok {
						extractScopesIntoSet(v, present)
					}
				}
				if len(present) > 0 {
					scopeList := make([]string, 0, len(present))
					for sc := range present {
						scopeList = append(scopeList, sc)
					}
					sort.Strings(scopeList)
					ctx.ByteSlots[slots.ScopesOut] = []byte(strings.Join(scopeList, ","))
				}
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

func validateJWTWithClaims(token string, cfg TokenValidationConfig) (*jwtClaims, error) {
	dot1 := strings.IndexByte(token, '.')
	if dot1 <= 0 {
		return nil, errors.New("invalid jwt format")
	}
	dot2Rel := strings.IndexByte(token[dot1+1:], '.')
	if dot2Rel <= 0 {
		return nil, errors.New("invalid jwt format")
	}
	dot2 := dot1 + 1 + dot2Rel

	headerSeg, claimsSeg, sigSeg := token[:dot1], token[dot1+1:dot2], token[dot2+1:]
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerSeg)
	if err != nil {
		return nil, err
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(claimsSeg)
	if err != nil {
		return nil, err
	}

	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, err
	}
	if header.Alg != cfg.Algorithm {
		return nil, fmt.Errorf("jwt alg mismatch: token has %q, config expects %q", header.Alg, cfg.Algorithm)
	}
	if !isSupportedAlg(header.Alg) {
		return nil, fmt.Errorf("unsupported jwt algorithm: %q", header.Alg)
	}

	var claims jwtClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(claimsBytes, &claims.Extra); err != nil {
		return nil, err
	}

	now := time.Now()
	if cfg.Validate.Issuer && cfg.Issuer != "" && claims.Issuer != cfg.Issuer {
		return nil, errors.New("issuer mismatch")
	}
	if cfg.Validate.Audience && cfg.Audience != "" && !audienceMatches(claims.Audience, cfg.Audience) {
		return nil, errors.New("audience mismatch")
	}
	if cfg.Validate.Expiry && claims.ExpiresAt > 0 && now.After(time.Unix(claims.ExpiresAt, 0).Add(cfg.Leeway)) {
		return nil, errors.New("token expired")
	}
	if cfg.Validate.NotBefore && claims.NotBefore > 0 && now.Before(time.Unix(claims.NotBefore, 0).Add(-cfg.Leeway)) {
		return nil, errors.New("token not active")
	}
	if len(cfg.RequiredScopes) > 0 {
		if err := validateRequiredScopes(claims.Extra, cfg.ScopeClaimKeys, cfg.RequiredScopes); err != nil {
			return nil, err
		}
	}

	// Custom claim checks
	for claimKey, expected := range cfg.CustomClaims {
		actual := fmt.Sprintf("%v", claims.Extra[claimKey])
		if actual != expected {
			return nil, fmt.Errorf("claim %q: expected %q got %q", claimKey, expected, actual)
		}
	}

	if cfg.Validate.Signature {
		if cfg.JWKSURI == "" {
			return nil, errors.New("jwt.jwks_uri is required")
		}
		if header.Kid == "" {
			return nil, errors.New("missing kid in jwt header")
		}
		pub, err := publicKeyFromJWKS(cfg.JWKSURI, header.Kid, cfg.JWKSTimeout, cfg.JWKSCacheTTL, cfg.JWKSRetryMaxAttempts, cfg.JWKSRetryBackoff)
		if err != nil {
			return nil, err
		}
		signature, err := base64.RawURLEncoding.DecodeString(sigSeg)
		if err != nil {
			return nil, err
		}
		if err := verifyJWTSignature(header.Alg, []byte(token[:dot2]), signature, pub); err != nil {
			return nil, err
		}
	}

	return &claims, nil
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

func publicKeyFromJWKS(jwksURI, kid string, timeout time.Duration, cacheTTL time.Duration, retryMaxAttempts int, retryBackoff time.Duration) (crypto.PublicKey, error) {
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

	payload, err := loadJWKSBytes(jwksURI, timeout, retryMaxAttempts, retryBackoff)
	if err != nil {
		return nil, err
	}

	var jwks jwksDocument
	if err := json.Unmarshal(payload, &jwks); err != nil {
		return nil, err
	}

	keys := make(map[string]crypto.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kid == "" {
			continue
		}
		switch strings.ToUpper(k.Kty) {
		case "RSA":
			if k.N == "" || k.E == "" {
				continue
			}
			pub, err := buildRSAPublicKey(k.N, k.E)
			if err != nil {
				continue
			}
			keys[k.Kid] = pub
		case "EC":
			pub, err := buildECPublicKey(k.Crv, k.X, k.Y)
			if err != nil {
				continue
			}
			keys[k.Kid] = pub
		case "OKP":
			if strings.ToUpper(k.Crv) != "ED25519" || k.X == "" {
				continue
			}
			pub, err := buildEdDSAPublicKey(k.X)
			if err != nil {
				continue
			}
			keys[k.Kid] = pub
		}
	}

	jwksCache.Store(jwksURI, cachedJWKS{keysByKid: keys, expiresAt: time.Now().Add(cacheTTL)})
	if kid == "" {
		return nil, nil
	}
	if key, ok := keys[kid]; ok {
		return key, nil
	}
	return nil, errors.New("kid not found in jwks")
}

func loadJWKSBytes(jwksURI string, timeout time.Duration, retryMaxAttempts int, retryBackoff time.Duration) ([]byte, error) {
	jwtCacheProviderLock.RLock()
	provider := jwtCacheProvider
	jwtCacheProviderLock.RUnlock()
	cacheKey := "jwks:" + jwksURI

	if provider != nil {
		if payload, ok := provider.Get(cacheKey); ok && len(payload) > 0 {
			return payload, nil
		}
	}

	if retryMaxAttempts < 1 {
		retryMaxAttempts = 1
	}
	jwksClient := &http.Client{Timeout: timeout}
	backoff := retryBackoff
	var payload []byte
	var lastErr error
	for attempt := 1; attempt <= retryMaxAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(backoff)
			backoff *= 2
		}
		var resp *http.Response
		resp, lastErr = jwksClient.Get(jwksURI)
		if lastErr != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = errors.New("jwks fetch failed: " + resp.Status)
			continue
		}
		payload, lastErr = io.ReadAll(resp.Body)
		resp.Body.Close()
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
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

func buildECPublicKey(crv, xB64, yB64 string) (*ecdsa.PublicKey, error) {
	if xB64 == "" || yB64 == "" {
		return nil, errors.New("ec key missing x or y")
	}
	var curve elliptic.Curve
	switch strings.ToUpper(crv) {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported ec curve: %q", crv)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(xB64)
	if err != nil {
		return nil, err
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(yB64)
	if err != nil {
		return nil, err
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	pub := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("ec point not on curve")
	}
	return pub, nil
}

func buildEdDSAPublicKey(xB64 string) (ed25519.PublicKey, error) {
	keyBytes, err := base64.RawURLEncoding.DecodeString(xB64)
	if err != nil {
		return nil, err
	}
	if len(keyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("ed25519: expected %d bytes, got %d", ed25519.PublicKeySize, len(keyBytes))
	}
	return ed25519.PublicKey(keyBytes), nil
}

// isSupportedAlg returns true for JWT algorithms RAH can verify.
func isSupportedAlg(alg string) bool {
	switch alg {
	case "RS256", "RS384", "RS512",
		"ES256", "ES384", "ES512",
		"EdDSA":
		return true
	}
	return false
}

// verifyJWTSignature dispatches to the correct signature verification algorithm.
// signingInput is header_b64.claims_b64 (the bytes that were signed).
func verifyJWTSignature(alg string, signingInput, sig []byte, pub crypto.PublicKey) error {
	switch alg {
	case "RS256":
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return errors.New("RS256: key is not RSA")
		}
		h := sha256.Sum256(signingInput)
		return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, h[:], sig)
	case "RS384":
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return errors.New("RS384: key is not RSA")
		}
		h := sha512.Sum384(signingInput)
		return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA384, h[:], sig)
	case "RS512":
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return errors.New("RS512: key is not RSA")
		}
		h := sha512.Sum512(signingInput)
		return rsa.VerifyPKCS1v15(rsaPub, crypto.SHA512, h[:], sig)
	case "ES256":
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("ES256: key is not EC")
		}
		h := sha256.Sum256(signingInput)
		if !verifyECDSARaw(ecPub, h[:], sig) {
			return errors.New("ES256: signature verification failed")
		}
		return nil
	case "ES384":
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("ES384: key is not EC")
		}
		h := sha512.Sum384(signingInput)
		if !verifyECDSARaw(ecPub, h[:], sig) {
			return errors.New("ES384: signature verification failed")
		}
		return nil
	case "ES512":
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("ES512: key is not EC")
		}
		h := sha512.Sum512(signingInput)
		if !verifyECDSARaw(ecPub, h[:], sig) {
			return errors.New("ES512: signature verification failed")
		}
		return nil
	case "EdDSA":
		edPub, ok := pub.(ed25519.PublicKey)
		if !ok {
			return errors.New("EdDSA: key is not Ed25519")
		}
		if !ed25519.Verify(edPub, signingInput, sig) {
			return errors.New("EdDSA: signature verification failed")
		}
		return nil
	default:
		return fmt.Errorf("unsupported algorithm: %q", alg)
	}
}

// verifyECDSARaw verifies a raw (r||s) encoded ECDSA signature (JWT format, not DER).
func verifyECDSARaw(pub *ecdsa.PublicKey, hash, sig []byte) bool {
	keyLen := (pub.Curve.Params().BitSize + 7) / 8
	if len(sig) != 2*keyLen {
		return false
	}
	r := new(big.Int).SetBytes(sig[:keyLen])
	s := new(big.Int).SetBytes(sig[keyLen:])
	return ecdsa.Verify(pub, hash, r, s)
}

// JWKThumbprintSHA256 computes the RFC 7638 JWK thumbprint for a public key.
// This is used by DPoP to match the cnf.jkt claim in access tokens.
func JWKThumbprintSHA256(pub crypto.PublicKey) (string, error) {
	var members string
	switch k := pub.(type) {
	case *rsa.PublicKey:
		// Lexicographic order: e, kty, n
		nB64 := base64.RawURLEncoding.EncodeToString(k.N.Bytes())
		eBytes := big.NewInt(int64(k.E)).Bytes()
		eB64 := base64.RawURLEncoding.EncodeToString(eBytes)
		members = fmt.Sprintf(`{"e":%q,"kty":"RSA","n":%q}`, eB64, nB64)
	case *ecdsa.PublicKey:
		var crv string
		switch k.Curve {
		case elliptic.P256():
			crv = "P-256"
		case elliptic.P384():
			crv = "P-384"
		case elliptic.P521():
			crv = "P-521"
		default:
			return "", errors.New("unsupported ec curve for thumbprint")
		}
		byteLen := (k.Curve.Params().BitSize + 7) / 8
		xBytes := make([]byte, byteLen)
		yBytes := make([]byte, byteLen)
		k.X.FillBytes(xBytes)
		k.Y.FillBytes(yBytes)
		xB64 := base64.RawURLEncoding.EncodeToString(xBytes)
		yB64 := base64.RawURLEncoding.EncodeToString(yBytes)
		// Lexicographic order: crv, kty, x, y
		members = fmt.Sprintf(`{"crv":%q,"kty":"EC","x":%q,"y":%q}`, crv, xB64, yB64)
	case ed25519.PublicKey:
		xB64 := base64.RawURLEncoding.EncodeToString([]byte(k))
		// Lexicographic order: crv, kty, x
		members = fmt.Sprintf(`{"crv":"Ed25519","kty":"OKP","x":%q}`, xB64)
	default:
		return "", errors.New("unsupported key type for thumbprint")
	}
	h := sha256.Sum256([]byte(members))
	return base64.RawURLEncoding.EncodeToString(h[:]), nil
}

// ParseJWTUnsafe parses a JWT without verifying the signature.
// Used internally by DPoP to extract the embedded JWK from the proof header.
func ParseJWTUnsafe(token string) (headerBytes, claimsBytes []byte, signingInput string, err error) {
	dot1 := strings.IndexByte(token, '.')
	if dot1 <= 0 {
		return nil, nil, "", errors.New("invalid jwt format")
	}
	dot2Rel := strings.IndexByte(token[dot1+1:], '.')
	if dot2Rel <= 0 {
		return nil, nil, "", errors.New("invalid jwt format")
	}
	dot2 := dot1 + 1 + dot2Rel
	hBytes, err := base64.RawURLEncoding.DecodeString(token[:dot1])
	if err != nil {
		return nil, nil, "", err
	}
	cBytes, err := base64.RawURLEncoding.DecodeString(token[dot1+1 : dot2])
	if err != nil {
		return nil, nil, "", err
	}
	return hBytes, cBytes, token[:dot2], nil
}
