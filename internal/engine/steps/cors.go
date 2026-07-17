package steps

import (
	"bytes"
	"unsafe"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// Pre-baked header name/value bytes â€” allocated once at package init.
// Zero cost at request time: no string conversion, no per-request alloc.
var (
	corsHdrACAllowOrigin      = []byte("Access-Control-Allow-Origin")
	corsHdrACAllowMethods     = []byte("Access-Control-Allow-Methods")
	corsHdrACAllowHeaders     = []byte("Access-Control-Allow-Headers")
	corsHdrACExposeHeaders    = []byte("Access-Control-Expose-Headers")
	corsHdrACMaxAge           = []byte("Access-Control-Max-Age")
	corsHdrACAllowCredentials = []byte("Access-Control-Allow-Credentials")
	corsHdrVary               = []byte("Vary")

	corsValWildcard  = []byte("*")
	corsValTrue      = []byte("true")
	corsValVaryOrigin = []byte("Origin")
	corsMethodOPTIONS = []byte("OPTIONS")
)

// CORSStep handles Cross-Origin Resource Sharing for both preflight and simple requests.
//
// Design: all configuration is captured at compile time as pre-baked []byte values.
// No allocations occur on the hot path.
//
//   - Wildcard origin (allowedOrigins nil/empty): sets Access-Control-Allow-Origin: *
//   - Specific origins: compares request Origin against the allowlist; on match reflects
//     the request Origin header via unsafe.Slice â€” zero copy into request header memory.
//   - Unknown origin: no CORS headers set, continues normally.
//   - OPTIONS preflight: sets 204 status + CORS headers then halts (StopPlan).
//   - Other methods with Origin: sets CORS headers, continues to next instruction.
//   - No Origin header: instant no-op, continues.
//
// Typical hot-path cost: ~40â€“80 ns (1 header lookup + 4â€“7 SetResponseHeader calls).
// No allocations for wildcard; zero-copy reflect for specific origin lists.
func CORSStep(
	allowedOrigins [][]byte, // nil/empty = wildcard (*); non-empty = explicit allowlist
	methodsValue   []byte,   // e.g. []byte("GET, POST, PUT, DELETE, PATCH, OPTIONS")
	headersValue   []byte,   // e.g. []byte("Content-Type, Authorization")
	exposeValue    []byte,   // nil when not configured
	maxAgeValue    []byte,   // e.g. []byte("86400"); nil to omit header
	credentials    bool,
) engine.Instruction {
	return engine.Instruction{
		Name: "CORS",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Fast path: absent Origin â†’ not a cross-origin request, nothing to do.
			origin := ctx.Request.Header.Get("Origin")
			if len(origin) == 0 {
				return s.PC + 1
			}

			// Determine which origin value to write into the response.
			var originValue []byte
			if len(allowedOrigins) == 0 {
				// Wildcard: static bytes, zero cost.
				originValue = corsValWildcard
			} else {
				// Zero-copy view into the request header string â€” no alloc.
				originBytes := unsafe.Slice(unsafe.StringData(origin), len(origin))
				for _, allowed := range allowedOrigins {
					if bytes.Equal(allowed, originBytes) {
						originValue = originBytes // reflects caller's own Origin
						break
					}
				}
				if originValue == nil {
					// Not in allowlist: skip CORS, continue.
					return s.PC + 1
				}
			}

			// Write CORS headers â€” all name/value []byte slices are pre-baked.
			ctx.SetResponseHeader(corsHdrACAllowOrigin, originValue)
			ctx.SetResponseHeader(corsHdrACAllowMethods, methodsValue)
			ctx.SetResponseHeader(corsHdrACAllowHeaders, headersValue)
			if len(exposeValue) > 0 {
				ctx.SetResponseHeader(corsHdrACExposeHeaders, exposeValue)
			}
			if len(maxAgeValue) > 0 {
				ctx.SetResponseHeader(corsHdrACMaxAge, maxAgeValue)
			}
			if credentials {
				ctx.SetResponseHeader(corsHdrACAllowCredentials, corsValTrue)
			}
			// Vary: Origin is required when reflecting a specific allowed origin to
			// prevent proxy caches from serving wrong-origin responses.
			if len(allowedOrigins) > 0 {
				ctx.SetResponseHeader(corsHdrVary, corsValVaryOrigin)
			}

			// Preflight (OPTIONS): respond 204 and halt â€” no upstream call needed.
			if bytes.Equal(ctx.Method, corsMethodOPTIONS) {
				ctx.ResponseStatus = 204
				return engine.StopPlan
			}

			return s.PC + 1
		},
	}
}
