package steps

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rah/internal/apikey"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// APIKeySource indicates where the raw API key is extracted from in a request.
type APIKeySource uint8

const (
	APIKeyFromHeader APIKeySource = iota
	APIKeyFromQuery
	APIKeyFromCookie
	APIKeyFromSlot
)

// APIKeyValidationConfig holds the compiled configuration for validate_api_key.
type APIKeyValidationConfig struct {
	Source            APIKeySource
	SourceKey         string // header name, query param name, or cookie name
	RequireTenant     bool   // if true, AllowedTenants must contain ctx.TenantID
	OnFailureStatus   int    // HTTP status on auth failure (default 401)
	OnFailureBody     string // response body on failure (default "unauthorized")
	ContinueOnFailure bool   // if true, write result slot and continue instead of halting
}

// APIKeyValidationSlots holds slot indexes for the validate_api_key step.
type APIKeyValidationSlots struct {
	SourceSlot int // ByteSlot index when Source==APIKeyFromSlot; -1 = unused
	ResultSlot int // ByteSlot to write "true"/"false"; -1 = unused
}

// ParseAPIKeyValidationConfig reads the step Input map and returns a compiled config.
// Keys: apikey.source, apikey.header, apikey.query_param, apikey.cookie,
//
//	apikey.on_failure, apikey.failure_status, apikey.failure_body, apikey.require_tenant
func ParseAPIKeyValidationConfig(input map[string]string) APIKeyValidationConfig {
	cfg := APIKeyValidationConfig{
		Source:          APIKeyFromHeader,
		SourceKey:       "X-API-Key",
		OnFailureStatus: http.StatusUnauthorized,
		OnFailureBody:   "unauthorized",
	}

	switch strings.ToLower(strings.TrimSpace(input["apikey.source"])) {
	case "query":
		cfg.Source = APIKeyFromQuery
		cfg.SourceKey = strings.TrimSpace(input["apikey.query_param"])
		if cfg.SourceKey == "" {
			cfg.SourceKey = "api_key"
		}
	case "cookie":
		cfg.Source = APIKeyFromCookie
		cfg.SourceKey = strings.TrimSpace(input["apikey.cookie"])
		if cfg.SourceKey == "" {
			cfg.SourceKey = "api_key"
		}
	case "slot":
		cfg.Source = APIKeyFromSlot
	default: // "header" or empty
		cfg.Source = APIKeyFromHeader
		cfg.SourceKey = strings.TrimSpace(input["apikey.header"])
		if cfg.SourceKey == "" {
			cfg.SourceKey = "X-API-Key"
		}
	}

	if strings.ToLower(strings.TrimSpace(input["apikey.on_failure"])) == "continue" {
		cfg.ContinueOnFailure = true
	}
	if s := strings.TrimSpace(input["apikey.failure_status"]); s != "" {
		if code, err := strconv.Atoi(s); err == nil && code >= 100 && code < 600 {
			cfg.OnFailureStatus = code
		}
	}
	if b := strings.TrimSpace(input["apikey.failure_body"]); b != "" {
		cfg.OnFailureBody = b
	}
	if strings.ToLower(strings.TrimSpace(input["apikey.require_tenant"])) == "true" {
		cfg.RequireTenant = true
	}

	return cfg
}

// ValidateAPIKey returns an engine.Instruction that:
//  1. Extracts the raw API key from the request
//  2. SHA-256 hashes it and looks it up in the in-memory index
//  3. Checks enabled flag and (optionally) tenant allowlist
//  4. On success: sets ctx.CallerID = entry.AppID, ctx.CallerKey = entry.Alias
//  5. Always records timing and counters in apikey.Global
func ValidateAPIKey(slots APIKeyValidationSlots, cfg APIKeyValidationConfig) engine.Instruction {
	return engine.Instruction{
		Name: "VALIDATE_API_KEY",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			start := time.Now().UnixNano()

			rawKey := extractAPIKey(ctx, slots.SourceSlot, cfg)
			if rawKey == "" {
				apikey.Global.Attempts.Add(1)
				apikey.Global.Misses.Add(1)
				apikey.Global.RecordTiming(time.Now().UnixNano() - start)
				return apiKeyFail(s, ctx, slots, cfg)
			}

			sum := sha256.Sum256([]byte(rawKey))
			hash := hex.EncodeToString(sum[:])

			entry := apikey.LookupByHash(hash)
			if entry == nil {
				apikey.Global.Attempts.Add(1)
				apikey.Global.Misses.Add(1)
				apikey.Global.RecordTiming(time.Now().UnixNano() - start)
				return apiKeyFail(s, ctx, slots, cfg)
			}

			if !entry.Enabled {
				apikey.Global.Attempts.Add(1)
				apikey.Global.Disabled.Add(1)
				apikey.Global.RecordTiming(time.Now().UnixNano() - start)
				return apiKeyFail(s, ctx, slots, cfg)
			}

			if cfg.RequireTenant && len(entry.AllowedTenants) > 0 {
				allowed := false
				for _, tid := range entry.AllowedTenants {
					if tid == ctx.TenantID {
						allowed = true
						break
					}
				}
				if !allowed {
					apikey.Global.Attempts.Add(1)
					apikey.Global.TenantDenied.Add(1)
					apikey.Global.RecordTiming(time.Now().UnixNano() - start)
					return apiKeyFail(s, ctx, slots, cfg)
				}
			}

			// Success
			ctx.CallerID = entry.AppID
			ctx.CallerKey = entry.Alias
			apikey.Global.Attempts.Add(1)
			apikey.Global.Hits.Add(1)
			apikey.Global.RecordTiming(time.Now().UnixNano() - start)

			if slots.ResultSlot >= 0 {
				buf := ctx.Alloc(4) // len("true") == 4
				copy(buf, "true")
				ctx.ByteSlots[slots.ResultSlot] = buf
			}
			return s.PC + 1
		},
	}
}

// extractAPIKey pulls the raw key string from the appropriate source.
func extractAPIKey(ctx *rctx.Context, sourceSlot int, cfg APIKeyValidationConfig) string {
	switch cfg.Source {
	case APIKeyFromHeader:
		return ctx.Request.Header.Get(cfg.SourceKey)
	case APIKeyFromQuery:
		return ctx.Request.URL.Query().Get(cfg.SourceKey)
	case APIKeyFromCookie:
		c, err := ctx.Request.Cookie(cfg.SourceKey)
		if err != nil {
			return ""
		}
		return c.Value
	case APIKeyFromSlot:
		if sourceSlot >= 0 && sourceSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[sourceSlot]) > 0 {
			return string(ctx.ByteSlots[sourceSlot])
		}
		return ""
	}
	return ""
}

// apiKeyFail handles authentication failure. If ContinueOnFailure is set, it
// writes "false" to the result slot and returns the next PC. Otherwise it
// writes the failure response, marks the context as Failed, and returns StopPlan.
func apiKeyFail(s *engine.ExecutionState, ctx *rctx.Context, slots APIKeyValidationSlots, cfg APIKeyValidationConfig) int16 {
	if cfg.ContinueOnFailure {
		if slots.ResultSlot >= 0 {
			buf := ctx.Alloc(5) // len("false") == 5
			copy(buf, "false")
			ctx.ByteSlots[slots.ResultSlot] = buf
		}
		return s.PC + 1
	}
	body := cfg.OnFailureBody
	ctx.ResponseStatus = cfg.OnFailureStatus
	ctx.Write([]byte(body))
	ctx.Failed = true
	ctx.ErrorCode = int16(cfg.OnFailureStatus)
	ctx.ErrorMsg = ctx.Alloc(len(body))
	copy(ctx.ErrorMsg, body)
	return engine.StopPlan
}
