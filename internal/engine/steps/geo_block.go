package steps

// geo_block â€” Country-based request geo-blocking.
//
// Checks the client IP address against a per-tenant country allow/block list
// using a MaxMind GeoLite2-Country database. Operates in two modes:
//
//   - block_list (default): blocks the request if the country IS in the list.
//   - allow_list: blocks the request if the country is NOT in the list.
//
// The mmdb reader is hot-swapped atomically; no locks in the request path.
// Private/loopback IPs can be allowed unconditionally via geo.allow_private=true.
//
// Config keys:
//
//	geo.mode             â€” "block_list" (default) or "allow_list"
//	geo.countries        â€” comma-separated ISO 3166-1 alpha-2 codes, e.g. "CN,RU,KP"
//	geo.trusted_proxy    â€” "true" to trust X-Forwarded-For / X-Real-IP headers
//	geo.allow_private    â€” "false" to block private/loopback IPs (default: true = allow)
//	geo.failure_status   â€” HTTP status when blocked (default 403)
//	geo.failure_body     â€” response body when blocked (default "access denied")
//	geo.on_block         â€” "stop" (default) or "continue" (tag mode: write result to slot)
//	geo.result_var       â€” slot name to write "blocked"/"allowed" into when on_block=continue

import (
	"net"
	"net/http"
	"strings"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/geo"
	"github.com/amitkhosla/rah/internal/rctx"
)

// GeoBlockConfig is the bake-time compiled config for geo_block.
type GeoBlockConfig struct {
	Mode            string          // "block_list" (default) or "allow_list"
	Countries       map[string]bool // ISO codes, uppercase
	TrustedProxy    bool            // use X-Forwarded-For
	AllowPrivate    bool            // pass through private/loopback IPs
	OnBlockStatus   int             // default 403
	OnBlockBody     string          // default "access denied"
	ContinueOnBlock bool            // tag mode: write result to slot, don't halt
	ResultSlot      int             // slot index for ContinueOnBlock mode (-1 = unused)
}

// ParseGeoBlockConfig converts a step Input map into a GeoBlockConfig.
// resultSlot is the pre-resolved ByteSlot index for geo.result_var (-1 if not set).
func ParseGeoBlockConfig(input map[string]string, resultSlot int) GeoBlockConfig {
	cfg := GeoBlockConfig{
		Mode:          "block_list",
		Countries:     make(map[string]bool),
		OnBlockStatus: http.StatusForbidden,
		OnBlockBody:   "access denied",
		ResultSlot:    resultSlot,
		AllowPrivate:  true,
	}
	if v := strings.TrimSpace(input["geo.mode"]); v != "" {
		cfg.Mode = v
	}
	for _, c := range strings.Split(input["geo.countries"], ",") {
		c = strings.TrimSpace(strings.ToUpper(c))
		if len(c) == 2 {
			cfg.Countries[c] = true
		}
	}
	cfg.TrustedProxy = strings.EqualFold(strings.TrimSpace(input["geo.trusted_proxy"]), "true")
	cfg.AllowPrivate = !strings.EqualFold(strings.TrimSpace(input["geo.allow_private"]), "false")
	if v := strings.TrimSpace(input["geo.failure_status"]); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.OnBlockStatus = n
		}
	}
	if v := strings.TrimSpace(input["geo.failure_body"]); v != "" {
		cfg.OnBlockBody = v
	}
	cfg.ContinueOnBlock = strings.EqualFold(strings.TrimSpace(input["geo.on_block"]), "continue")
	return cfg
}

// GeoBlock builds the geo-blocking instruction.
func GeoBlock(manager *geo.Manager, cfg GeoBlockConfig) engine.Instruction {
	return engine.Instruction{
		Name: "GEO_BLOCK",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			blockResult := func(blocked bool) int16 {
				if cfg.ContinueOnBlock {
					if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
						if blocked {
							ctx.ByteSlots[cfg.ResultSlot] = []byte("blocked")
						} else {
							ctx.ByteSlots[cfg.ResultSlot] = []byte("allowed")
						}
					}
					return s.PC + 1
				}
				if blocked {
					status := cfg.OnBlockStatus
					body := cfg.OnBlockBody
					ctx.ResponseStatus = status
					ctx.Write([]byte(body))
					ctx.Failed = true
					ctx.ErrorCode = int16(status)
					ctx.ErrorMsg = ctx.Alloc(len(body))
					copy(ctx.ErrorMsg, body)
					return engine.StopPlan
				}
				return s.PC + 1
			}

			if ctx.Request == nil {
				// No request: block in allow_list mode (no IP = not in allowed list).
				return blockResult(cfg.Mode == "allow_list")
			}

			ip := extractClientIP(ctx.Request, cfg.TrustedProxy)
			if ip == "" {
				return blockResult(cfg.Mode == "allow_list")
			}

			cc := manager.Lookup(ip)

			// Private IPs.
			if cc == "_private" {
				return blockResult(!cfg.AllowPrivate)
			}

			// DB not loaded or IP not in database.
			if cc == "" {
				if manager.OnMissingAllow() {
					return blockResult(false)
				}
				return blockResult(true) // fail-closed
			}

			inList := cfg.Countries[cc]
			var blocked bool
			if cfg.Mode == "allow_list" {
				blocked = !inList // allow_list: block if NOT in list
			} else {
				blocked = inList // block_list: block if IN list
			}
			return blockResult(blocked)
		},
	}
}

// extractClientIP extracts the real client IP from the request.
// When trustedProxy is true, X-Forwarded-For and X-Real-IP headers are
// consulted first (in that order) before falling back to RemoteAddr.
func extractClientIP(r *http.Request, trustedProxy bool) string {
	if trustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first IP (original client).
			if idx := strings.IndexByte(xff, ','); idx >= 0 {
				xff = xff[:idx]
			}
			return strings.TrimSpace(xff)
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
