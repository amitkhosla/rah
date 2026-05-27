package steps

// Token revocation — jti blocklist check (RFC 7519 §4.1.7 / RFC 7009 companion).
//
// check_token_revoked looks up the JWT's jti claim in a datastore-backed blocklist.
// The management API writes to the blocklist when a token is explicitly revoked.
// This step is inserted after token_validation in flows that require hard revocation.
//
// The step reads the jti from a slot (written there by token_validation's claims_var),
// not from the raw token, to avoid re-parsing.
//
// Datastore key format:  "revoked:jti:<jti>"
// Value:                 "1" (presence is all that matters)
// TTL:                   set when writing, aligned with the original token's exp.

import (
	"context"
	"strings"
	"time"

	"rah/internal/datastore"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// TokenRevokeConfig is the bake-time config for check_token_revoked.
type TokenRevokeConfig struct {
	// DatastoreName is the named datastore to use for the blocklist (default "default").
	DatastoreName string
	// KeyPrefix is the prefix for blocklist keys (default "revoked:jti:").
	KeyPrefix string
	// OnFailureStatus is the HTTP status when the token is revoked (default 401).
	OnFailureStatus int
	// OnFailureBody is the response body when revoked (default "token revoked").
	OnFailureBody string
	// ContinueOnFailure writes the result to ResultSlot instead of halting.
	ContinueOnFailure bool
}

// TokenRevokeSlots holds runtime slot indexes for check_token_revoked.
type TokenRevokeSlots struct {
	// JTI is the slot holding the jti claim string. Typically written by token_validation
	// when jwt.claims_var is set (parse the jti from the claims JSON) or a dedicated extract step.
	JTI int
	// Result is the slot to write "true"/"false" in continue mode. -1 = not used.
	Result int
}

// ParseTokenRevokeConfig converts a step Input map into a TokenRevokeConfig.
func ParseTokenRevokeConfig(input map[string]string) TokenRevokeConfig {
	cfg := TokenRevokeConfig{
		DatastoreName:   "default",
		KeyPrefix:       "revoked:jti:",
		OnFailureStatus: 401,
		OnFailureBody:   "token revoked",
	}
	if v := strings.TrimSpace(input["revoke.datastore"]); v != "" {
		cfg.DatastoreName = v
	}
	if v := strings.TrimSpace(input["revoke.key_prefix"]); v != "" {
		cfg.KeyPrefix = v
	}
	if v := strings.TrimSpace(input["revoke.failure_status"]); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.OnFailureStatus = n
		}
	}
	if v := strings.TrimSpace(input["revoke.failure_body"]); v != "" {
		cfg.OnFailureBody = v
	}
	cfg.ContinueOnFailure = strings.EqualFold(strings.TrimSpace(input["revoke.on_failure"]), "continue")
	return cfg
}

// CheckTokenRevoked builds the jti blocklist check instruction.
//
// Step config keys:
//
//	revoke.datastore      — named datastore for the blocklist (default "default")
//	revoke.key_prefix     — key prefix (default "revoked:jti:")
//	revoke.on_failure     — "stop" (default) or "continue"
//	revoke.failure_status — HTTP status when revoked (default 401)
//	revoke.failure_body   — body when revoked (default "token revoked")
//
// Slot config keys:
//
//	key_identifier        — slot holding the jti claim value (required)
//	revoke.result_var     — slot to write "true" (not revoked) / "false" (revoked) in continue mode
func CheckTokenRevoked(ds datastore.KeyValueStore, slots TokenRevokeSlots, cfg TokenRevokeConfig) engine.Instruction {
	return engine.Instruction{
		Name: "CHECK_TOKEN_REVOKED",
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

			if slots.JTI < 0 || slots.JTI >= len(ctx.ByteSlots) {
				return s.PC + 1 // no jti slot configured — skip check
			}
			jti := strings.TrimSpace(string(ctx.ByteSlots[slots.JTI]))
			if jti == "" {
				return s.PC + 1 // no jti value — skip check (token may not have jti)
			}

			key := cfg.KeyPrefix + jti
			tenant := datastore.Tenant(ctx.TenantKey)
			val, found, err := ds.Get(context.Background(), tenant, key)
			if err != nil {
				// Datastore error — fail closed (treat as revoked) for security.
				return stopFail()
			}
			if found && len(val) > 0 {
				// jti found in blocklist — token is revoked.
				return stopFail()
			}

			// Not revoked.
			if slots.Result >= 0 && slots.Result < len(ctx.ByteSlots) {
				ctx.ByteSlots[slots.Result] = []byte("true")
			}
			return s.PC + 1
		},
	}
}

// RevokeToken writes a jti to the blocklist with a TTL aligned to the token's expiry.
// This is called by the management plane when a token is explicitly revoked.
// ttl is the remaining lifetime of the token (exp - now). If zero or negative, a
// conservative 24-hour TTL is used.
func RevokeToken(ds datastore.KeyValueStore, tenantKey, jti, keyPrefix string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	key := keyPrefix + jti
	tenant := datastore.Tenant(tenantKey)
	if exp, ok := ds.(datastore.ExpiringStore); ok {
		return exp.PutWithTTL(context.Background(), tenant, key, []byte("1"), ttl)
	}
	return ds.Put(context.Background(), tenant, key, []byte("1"))
}
