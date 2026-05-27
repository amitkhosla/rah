package steps

// DPoP (Demonstrating Proof of Possession) validation — RFC 9449.
//
// DPoP binds an access token to a specific client key pair so that stolen
// bearer tokens cannot be replayed from another host. Each request carries:
//
//  1. Authorization: DPoP <access-token>
//  2. DPoP: <proof-JWT>
//
// The proof JWT is a short-lived, single-use JWT signed by the client's
// private key.  Its header embeds the matching public key (as a JWK).
// The gateway verifies:
//   - Proof signature using the embedded public key
//   - typ = "dpop+jwt"
//   - htm = HTTP method of this request
//   - htu = URI of this request (without fragment)
//   - iat within max_age_seconds (default 60)
//   - jti has not been seen before (replay prevention — datastore-backed)
//   - ath = base64url(SHA256(access_token)) if access token slot is provided
//   - cnf.jkt in access token matches thumbprint of the proof JWK (if present)

import (
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"rah/internal/datastore"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// dpopProofHeader is the parsed header of a DPoP proof JWT.
// The embedded JWK is the client's public key.
type dpopProofHeader struct {
	Typ string          `json:"typ"` // must be "dpop+jwt"
	Alg string          `json:"alg"`
	JWK json.RawMessage `json:"jwk"` // embedded public key (no private fields)
}

// dpopProofClaims are the required claims in the DPoP proof body.
type dpopProofClaims struct {
	JTI string `json:"jti"` // unique token ID (replay prevention)
	HTM string `json:"htm"` // HTTP method
	HTU string `json:"htu"` // HTTP URI (path + query, no fragment)
	IAT int64  `json:"iat"` // issued-at (unix seconds)
	ATH string `json:"ath"` // SHA-256 hash of the access token (optional)
}

// DPoPConfig is the bake-time configuration for validate_dpop.
type DPoPConfig struct {
	// DPoPHeader is the request header containing the proof JWT (default "DPoP").
	DPoPHeader string
	// MaxAgeSec is the maximum age of the proof iat claim in seconds (default 60).
	MaxAgeSec int64
	// MatchQuery controls whether the htu check includes the query string.
	// false = only path is compared (safer for URLs with dynamic tokens in query).
	// true = path + query must match exactly.
	MatchQuery bool
	// RequireATH, when true, requires the ath claim to be present and valid.
	// Enable when the access token slot is also provided.
	RequireATH bool
	// CheckCNFJKT, when true, verifies cnf.jkt in the access token matches the proof key.
	CheckCNFJKT bool
	// OnFailureStatus is the HTTP status on validation failure (default 401).
	OnFailureStatus int
	// OnFailureBody is the response body on failure (default "invalid dpop proof").
	OnFailureBody string
	// ContinueOnFailure writes the result to ResultSlot instead of halting.
	ContinueOnFailure bool
}

// DPoPSlots holds runtime slot indexes for validate_dpop.
type DPoPSlots struct {
	// AccessToken is the slot holding the raw access token string.
	// Used for ath computation and cnf.jkt check. -1 = not provided.
	AccessToken int
	// Result is the slot to write "true"/"false" in continue mode. -1 = not used.
	Result int
	// CNFJkt is the slot to write the verified JWK thumbprint into. -1 = not used.
	CNFJkt int
}

// ParseDPoPConfig converts a step Input map into a DPoPConfig.
func ParseDPoPConfig(input map[string]string) DPoPConfig {
	cfg := DPoPConfig{
		DPoPHeader:      "DPoP",
		MaxAgeSec:       60,
		OnFailureStatus: http.StatusUnauthorized,
		OnFailureBody:   "invalid dpop proof",
	}
	if v := strings.TrimSpace(input["dpop.header"]); v != "" {
		cfg.DPoPHeader = v
	}
	if v := strings.TrimSpace(input["dpop.max_age_seconds"]); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			cfg.MaxAgeSec = int64(n)
		}
	}
	cfg.MatchQuery = strings.EqualFold(strings.TrimSpace(input["dpop.match_query"]), "true")
	cfg.RequireATH = strings.EqualFold(strings.TrimSpace(input["dpop.require_ath"]), "true")
	cfg.CheckCNFJKT = strings.EqualFold(strings.TrimSpace(input["dpop.check_cnf_jkt"]), "true")
	cfg.ContinueOnFailure = strings.EqualFold(strings.TrimSpace(input["dpop.on_failure"]), "continue")
	if v := strings.TrimSpace(input["dpop.failure_status"]); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.OnFailureStatus = n
		}
	}
	if v := strings.TrimSpace(input["dpop.failure_body"]); v != "" {
		cfg.OnFailureBody = v
	}
	return cfg
}

// ValidateDPoP builds a DPoP proof validation instruction.
//
// Step config keys:
//
//	dpop.header           — header name holding the proof JWT (default "DPoP")
//	dpop.max_age_seconds  — max age of the iat claim (default 60)
//	dpop.match_query      — include query string in htu check (default false)
//	dpop.require_ath      — require ath claim (default false)
//	dpop.check_cnf_jkt    — verify cnf.jkt against proof key thumbprint (default false)
//	dpop.on_failure       — "stop" (default) or "continue"
//	dpop.failure_status   — HTTP status on failure (default 401)
//	dpop.failure_body     — body on failure (default "invalid dpop proof")
//
// Slot config keys:
//
//	dpop.access_token_var — slot holding the raw access token (for ath + cnf.jkt)
//	dpop.result_var       — slot to write "true"/"false" in continue mode
//	dpop.cnf_jkt_var      — slot to write the verified JWK thumbprint on success
func ValidateDPoP(slots DPoPSlots, cfg DPoPConfig, store datastore.KeyValueStore) engine.Instruction {
	return engine.Instruction{
		Name: "VALIDATE_DPOP",
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

			if ctx.Request == nil {
				return stopFail()
			}

			proofToken := strings.TrimSpace(ctx.Request.Header.Get(cfg.DPoPHeader))
			if proofToken == "" {
				return stopFail()
			}

			// Parse proof JWT (header, claims, signing input) without verifying signature yet.
			hBytes, cBytes, signingInput, sigB64, err := splitJWT(proofToken)
			if err != nil {
				return stopFail()
			}

			var proofHdr dpopProofHeader
			if err := json.Unmarshal(hBytes, &proofHdr); err != nil {
				return stopFail()
			}

			// typ MUST be "dpop+jwt" (case-insensitive per RFC 9449 §4.3)
			if !strings.EqualFold(proofHdr.Typ, "dpop+jwt") {
				return stopFail()
			}
			if !isSupportedAlg(proofHdr.Alg) {
				return stopFail()
			}

			// Parse the embedded JWK from the proof header.
			proofPub, err := parseEmbeddedJWK(proofHdr.JWK)
			if err != nil {
				return stopFail()
			}

			// Verify the proof signature using the embedded public key.
			sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
			if err != nil {
				return stopFail()
			}
			if err := verifyJWTSignature(proofHdr.Alg, []byte(signingInput), sigBytes, proofPub); err != nil {
				return stopFail()
			}

			// Parse proof claims.
			var claims dpopProofClaims
			if err := json.Unmarshal(cBytes, &claims); err != nil {
				return stopFail()
			}

			// jti must be present.
			if claims.JTI == "" {
				return stopFail()
			}

			// htm must match request method.
			if !strings.EqualFold(claims.HTM, ctx.Request.Method) {
				return stopFail()
			}

			// htu must match request URI.
			reqURI := requestURI(ctx.Request, cfg.MatchQuery)
			if !htuMatches(claims.HTU, reqURI) {
				return stopFail()
			}

			// iat must be within window.
			now := time.Now().Unix()
			age := now - claims.IAT
			if age < 0 {
				age = -age // tolerate minor clock skew
			}
			if age > cfg.MaxAgeSec {
				return stopFail()
			}

			// jti replay check — write to store; if already present, it's a replay.
			if store != nil {
				tenant := datastore.Tenant(ctx.TenantKey)
				jtiKey := "jti:" + claims.JTI
				if _, found, _ := store.Get(context.Background(), tenant, jtiKey); found {
					return stopFail() // replay
				}
				ttl := time.Duration(cfg.MaxAgeSec+5) * time.Second
				if exp, ok := store.(datastore.ExpiringStore); ok {
					_ = exp.PutWithTTL(context.Background(), tenant, jtiKey, []byte("1"), ttl)
				} else {
					_ = store.Put(context.Background(), tenant, jtiKey, []byte("1"))
				}
			}
			// If store is nil, skip replay check (no store configured).

			// ath check — SHA256(access_token) must match.
			if slots.AccessToken >= 0 && slots.AccessToken < len(ctx.ByteSlots) {
				rawToken := string(ctx.ByteSlots[slots.AccessToken])
				if rawToken != "" {
					h := sha256.Sum256([]byte(rawToken))
					expected := base64.RawURLEncoding.EncodeToString(h[:])
					if claims.ATH == "" {
						if cfg.RequireATH {
							return stopFail()
						}
					} else if claims.ATH != expected {
						return stopFail()
					}
				}
			}

			// cnf.jkt check — access token's cnf.jkt must match the proof key thumbprint.
			if cfg.CheckCNFJKT && slots.AccessToken >= 0 && slots.AccessToken < len(ctx.ByteSlots) {
				rawToken := string(ctx.ByteSlots[slots.AccessToken])
				if jkt, err := extractCNFJkt(rawToken); err == nil && jkt != "" {
					thumbprint, err := JWKThumbprintSHA256(proofPub)
					if err != nil || thumbprint != jkt {
						return stopFail()
					}
				}
			}

			// Success — write outputs.
			if slots.Result >= 0 && slots.Result < len(ctx.ByteSlots) {
				ctx.ByteSlots[slots.Result] = []byte("true")
			}
			if slots.CNFJkt >= 0 && slots.CNFJkt < len(ctx.ByteSlots) {
				if thumbprint, err := JWKThumbprintSHA256(proofPub); err == nil {
					ctx.ByteSlots[slots.CNFJkt] = []byte(thumbprint)
				}
			}
			return s.PC + 1
		},
	}
}

// splitJWT splits a JWT into (headerBytes, claimsBytes, signingInput, sigB64, error).
func splitJWT(token string) ([]byte, []byte, string, string, error) {
	dot1 := strings.IndexByte(token, '.')
	if dot1 <= 0 {
		return nil, nil, "", "", errors.New("invalid jwt")
	}
	dot2Rel := strings.IndexByte(token[dot1+1:], '.')
	if dot2Rel <= 0 {
		return nil, nil, "", "", errors.New("invalid jwt")
	}
	dot2 := dot1 + 1 + dot2Rel

	hBytes, err := base64.RawURLEncoding.DecodeString(token[:dot1])
	if err != nil {
		return nil, nil, "", "", err
	}
	cBytes, err := base64.RawURLEncoding.DecodeString(token[dot1+1 : dot2])
	if err != nil {
		return nil, nil, "", "", err
	}
	return hBytes, cBytes, token[:dot2], token[dot2+1:], nil
}

// parseEmbeddedJWK parses the JWK embedded in a DPoP proof header.
// Private key fields are rejected.
func parseEmbeddedJWK(raw json.RawMessage) (crypto.PublicKey, error) {
	if len(raw) == 0 {
		return nil, errors.New("no jwk in dpop proof header")
	}
	var k jwkKey
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, err
	}
	// Reject private key material.
	var privateCheck struct {
		D string `json:"d"`
	}
	if json.Unmarshal(raw, &privateCheck) == nil && privateCheck.D != "" {
		return nil, errors.New("dpop jwk must not contain private key")
	}
	switch strings.ToUpper(k.Kty) {
	case "RSA":
		return buildRSAPublicKey(k.N, k.E)
	case "EC":
		return buildECPublicKey(k.Crv, k.X, k.Y)
	case "OKP":
		if strings.ToUpper(k.Crv) != "ED25519" {
			return nil, fmt.Errorf("unsupported okp curve: %q", k.Crv)
		}
		return buildEdDSAPublicKey(k.X)
	default:
		return nil, fmt.Errorf("unsupported kty: %q", k.Kty)
	}
}

// requestURI returns the effective URI to compare against the htu claim.
// If matchQuery is false, only scheme+host+path is included (fragment is always excluded).
func requestURI(r *http.Request, matchQuery bool) string {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	path := r.URL.Path
	if matchQuery && r.URL.RawQuery != "" {
		return scheme + "://" + host + path + "?" + r.URL.RawQuery
	}
	return scheme + "://" + host + path
}

// htuMatches compares the htu claim to the request URI.
// Strips trailing slashes for comparison and ignores fragments.
func htuMatches(htu, reqURI string) bool {
	// Strip fragment from htu (RFC 9449 §4.2 says htu MUST NOT contain fragment)
	if idx := strings.IndexByte(htu, '#'); idx >= 0 {
		htu = htu[:idx]
	}
	return strings.TrimRight(htu, "/") == strings.TrimRight(reqURI, "/")
}

// extractCNFJkt extracts the cnf.jkt claim from a JWT without signature verification.
// Returns empty string if not present.
func extractCNFJkt(token string) (string, error) {
	token = extractBearerOrRaw(token)
	_, cBytes, _, err := ParseJWTUnsafe(token)
	if err != nil {
		return "", err
	}
	var claims struct {
		CNF cnfClaim `json:"cnf"`
	}
	if err := json.Unmarshal(cBytes, &claims); err != nil {
		return "", err
	}
	return claims.CNF.JKT, nil
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}
