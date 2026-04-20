package steps

import (
	"bytes"
	"io"
	"strings"

	"github.com/tidwall/gjson"
	"rah/internal/engine"
	"rah/internal/rctx"
	"unsafe"
)

// TenantSwitch handles multi-option paths.
func TenantSwitch(ctx *rctx.Context) int16 {
	tenantType := string(ctx.ByteSlots[0]) // Retrieve from optimized slot

	switch tenantType {
	case "GOLD":
		return 1 // Move to next (Gold-specific logic)
	case "SILVER":
		return 5 // Jump +5 steps to skip Gold logic and land on Silver logic
	default:
		return 10 // Jump +10 to skip all and land on the Finalize step
	}
}

// IfElseAuth handles a simple true/false branch.
func IfElseAuth(ctx *rctx.Context) int16 {
	isAuthenticated := ctx.BoolSlots[1]

	if isAuthenticated {
		return 1 // Continue to next step
	}
	return 2 // Jump +2 to skip the "Success" step and land on "Unauthorized"
}

func BindPath(paramIdx int, slotIdx int) engine.Instruction {
	return engine.Instruction{
		Name: "BIND_PATH",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// FIX: Check bounds and use the Struct-based Params array
			if paramIdx < ctx.Match.ParamCount {
				p := ctx.Match.Params[paramIdx]
				// Zero-allocation extraction from the shared Path buffer
				ctx.ByteSlots[slotIdx] = ctx.Path[p.Start:p.End]
			}
			return state.PC + 1
		},
	}
}

func BindHeader(key string, slot int) engine.Instruction {
	return engine.Instruction{
		Name: "BIND_HEADER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.Request.Header.Get(key)
			if len(val) > 0 {
				// Zero-copy reference into http.Request.Header memory.
				// The slice is valid as long as req.Header is not modified (safe within request lifetime).
				ctx.ByteSlots[slot] = unsafe.Slice(unsafe.StringData(val), len(val))
			} else {
				ctx.ByteSlots[slot] = nil
			}
			return state.PC + 1
		},
	}
}

func BindQuery(key string, slot int) engine.Instruction {
	keyBytes := []byte(key) // captured once at instruction creation, not per-request
	return engine.Instruction{
		Name: "BIND_QUERY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := scanQuery(ctx.RawQuery, keyBytes)
			if len(val) > 0 {
				s := ctx.Alloc(len(val))
				copy(s, val)
				ctx.ByteSlots[slot] = s
			} else {
				ctx.ByteSlots[slot] = nil
			}
			return state.PC + 1
		},
	}
}

// BindBody reads the request body (buffering it on first call) and extracts a
// JSON field into the given ByteSlot. If the field is absent the slot is set
// to nil (falsy).
//
// jsonPath may contain "||"-separated alternatives (e.g.
// "messages.#(role==\"user\").content||message"). The first path that resolves
// to a non-empty string wins. This lets a single step handle both Anthropic
// wire format (messages array) and simple {"message":"..."} bodies without
// requiring callers to know which format is incoming.
func BindBody(jsonPath string, slot int) engine.Instruction {
	paths := strings.Split(jsonPath, "||")
	return engine.Instruction{
		Name: "BIND_BODY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Buffer the request body on first access (idempotent: body is already
			// drained into ctx.RequestBuffer after the first BindBody call in a flow).
			body := ctx.RequestBuffer
			if len(body) == 0 && ctx.Request != nil && ctx.Request.Body != nil {
				data, err := io.ReadAll(io.LimitReader(ctx.Request.Body, 4<<20))
				if err != nil || len(data) == 0 {
					ctx.ByteSlots[slot] = nil
					return state.PC + 1
				}
				ctx.RequestBuffer = data
				body = data
			}
			if len(body) == 0 {
				ctx.ByteSlots[slot] = nil
				return state.PC + 1
			}
			for _, p := range paths {
				res := gjson.GetBytes(body, strings.TrimSpace(p))
				if res.Exists() && res.String() != "" {
					str := res.String()
					s := ctx.Alloc(len(str))
					copy(s, str)
					ctx.ByteSlots[slot] = s
					return state.PC + 1
				}
			}
			ctx.ByteSlots[slot] = nil
			return state.PC + 1
		},
	}
}

// scanQuery finds the raw value for key in a query string like "a=1&b=2&c=3".
// Returns a slice directly into the raw query bytes — zero allocation.
// No URL-decoding is applied; values are raw as received from the wire.
func scanQuery(query, key []byte) []byte {
	for len(query) > 0 {
		var pair []byte
		if i := bytes.IndexByte(query, '&'); i >= 0 {
			pair, query = query[:i], query[i+1:]
		} else {
			pair, query = query, nil
		}
		if j := bytes.IndexByte(pair, '='); j >= 0 {
			if bytes.Equal(pair[:j], key) {
				return pair[j+1:]
			}
		}
	}
	return nil
}
