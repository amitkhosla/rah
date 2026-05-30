package steps

import (
	"encoding/json"
	"strings"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// ExtractJWTClaimConfig configures the extract_jwt_claim instruction.
type ExtractJWTClaimConfig struct {
	// TokenHeader is the header name to read the token from (e.g. "Authorization").
	// Mutually exclusive with TokenSlot.
	TokenHeader string
	// TokenSlot is the ByteSlot index holding a pre-extracted token string.
	// Used when key_identifier is "var.xxx". -1 = not used.
	TokenSlot int
	// ClaimName is the JWT claim to extract (e.g. "sub", "scope", "client_id").
	ClaimName string
	// DefaultValue is written to OutputSlot when token is absent, malformed,
	// or the claim is missing. Empty string by default.
	DefaultValue string
	// OutputSlot is the ByteSlot index to write the extracted claim value into.
	OutputSlot int
	// StripBearer strips a leading "Bearer " prefix from the header value.
	StripBearer bool
}

// ExtractJWTClaim returns an Instruction that parses a JWT and writes one named
// claim value into a ByteSlot without signature verification.
//
// If the token is missing, malformed, or the claim is absent, DefaultValue is
// written instead. Never fails the request.
func ExtractJWTClaim(cfg ExtractJWTClaimConfig) engine.Instruction {
	return engine.Instruction{
		Name: "extract_jwt_claim[" + cfg.ClaimName + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.OutputSlot < 0 || cfg.OutputSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}

			// Read raw token string.
			var raw string
			if cfg.TokenHeader != "" && ctx.Request != nil {
				raw = strings.TrimSpace(ctx.Request.Header.Get(cfg.TokenHeader))
			} else if cfg.TokenSlot >= 0 && cfg.TokenSlot < len(ctx.ByteSlots) {
				raw = string(ctx.ByteSlots[cfg.TokenSlot])
			}

			if cfg.StripBearer {
				raw = strings.TrimPrefix(raw, "Bearer ")
				raw = strings.TrimPrefix(raw, "bearer ")
			}

			writeDefault := func() int16 {
				if cfg.DefaultValue != "" {
					d := ctx.Alloc(len(cfg.DefaultValue))
					copy(d, cfg.DefaultValue)
					ctx.ByteSlots[cfg.OutputSlot] = d
				}
				return state.PC + 1
			}

			if raw == "" {
				return writeDefault()
			}

			// Parse JWT without signature verification.
			_, claimsBytes, _, err := ParseJWTUnsafe(raw)
			if err != nil {
				return writeDefault()
			}

			// Unmarshal claims and extract named claim.
			var claims map[string]json.RawMessage
			if err := json.Unmarshal(claimsBytes, &claims); err != nil {
				return writeDefault()
			}

			rawClaim, ok := claims[cfg.ClaimName]
			if !ok {
				return writeDefault()
			}

			// Coerce to string representation.
			var result string
			if err := json.Unmarshal(rawClaim, &result); err != nil {
				// Not a JSON string — use raw bytes (handles numbers, booleans).
				result = strings.Trim(string(rawClaim), `"`)
			}

			if result == "" {
				return writeDefault()
			}

			out := ctx.Alloc(len(result))
			copy(out, result)
			ctx.ByteSlots[cfg.OutputSlot] = out

			return state.PC + 1
		},
	}
}
