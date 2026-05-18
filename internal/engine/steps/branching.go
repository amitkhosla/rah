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

// LogFieldStep records a named slot value for inclusion in the request access log.
// Name is baked at compile time; the value is read from ByteSlots[srcSlot] post-response.
// Zero-allocation: ExtraLogFields is an inline array in Context.
func LogFieldStep(name string, srcSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "LOG_FIELD",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if ctx.ExtraLogCount < uint8(len(ctx.ExtraLogFields)) {
				ctx.ExtraLogFields[ctx.ExtraLogCount] = rctx.LogFieldEntry{Name: name, Slot: srcSlot}
				ctx.ExtraLogCount++
			}
			return state.PC + 1
		},
	}
}

// SetRequestHeader injects a header into the upstream request before proxying.
// name is baked at compile time; the value is read from ByteSlots[valueSlot] at runtime.
// Zero-allocation: MutationLog is pre-allocated in the pool.
func SetRequestHeader(name string, valueSlot int) engine.Instruction {
	keyBytes := []byte(name)
	return engine.Instruction{
		Name: "SET_REQUEST_HEADER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if ctx.MutationCount < len(ctx.MutationLog) {
				ctx.MutationLog[ctx.MutationCount] = rctx.HeaderMutation{
					Key:   keyBytes,
					Value: ctx.ByteSlots[valueSlot],
				}
				ctx.MutationCount++
			}
			return state.PC + 1
		},
	}
}

// SetRequestBody stages a request body for the next http_call. The body is read from
// ByteSlots[srcSlot] at runtime; the Content-Type is baked at compile time.
// This does NOT send the request — it only stages for the next http_call step.
func SetRequestBody(srcSlot int, contentType []byte) engine.Instruction {
	return engine.Instruction{
		Name: "SET_REQUEST_BODY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.StagedRequestBody = ctx.ByteSlots[srcSlot]
			ctx.StagedContentType = contentType
			return state.PC + 1
		},
	}
}

// BindRequestURL writes the request URL path (and optionally query string) to ByteSlots[dstSlot].
// includeQuery=true appends ?rawQuery. Zero-copy for path (aliases ctx.Path);
// one ctx.Alloc if query is appended.
func BindRequestURL(dstSlot int, includeQuery bool) engine.Instruction {
	return engine.Instruction{
		Name: "BIND_REQUEST_URL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			path := ctx.Path
			if !includeQuery || len(ctx.RawQuery) == 0 {
				ctx.ByteSlots[dstSlot] = path
				return state.PC + 1
			}
			// path + "?" + rawQuery — one arena allocation
			buf := ctx.Alloc(len(path) + 1 + len(ctx.RawQuery))
			n := copy(buf, path)
			buf[n] = '?'
			copy(buf[n+1:], ctx.RawQuery)
			ctx.ByteSlots[dstSlot] = buf
			return state.PC + 1
		},
	}
}

// CopyHeader reads srcHeader from the incoming request and stages it as dstHeader
// in the MutationLog for the next upstream call. Both names are baked at compile time.
// Zero-copy: uses unsafe.Slice to reference the header string without allocating.
func CopyHeader(srcHeader, dstHeader string) engine.Instruction {
	dstKey := []byte(dstHeader)
	return engine.Instruction{
		Name: "COPY_HEADER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.Request.Header.Get(srcHeader)
			if val == "" || ctx.MutationCount >= len(ctx.MutationLog) {
				return state.PC + 1
			}
			ctx.MutationLog[ctx.MutationCount] = rctx.HeaderMutation{
				Key:   dstKey,
				Value: unsafe.Slice(unsafe.StringData(val), len(val)),
				Op:    0, // Set
			}
			ctx.MutationCount++
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
