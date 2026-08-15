package steps

import (
	"bytes"
	"strconv"
	"unsafe"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

var cookieHeaderKey = []byte("Cookie")

// ExtractCookie scans the Cookie request header for a named cookie and extracts its value.
// Zero-copy: aliases into the header string memory using unsafe.Slice.
// Writes the found value into ByteSlots[destSlot], or nil if not found.
func ExtractCookie(cookieName string, destSlot int) engine.Instruction {
	nameBytes := []byte(cookieName + "=")
	return engine.Instruction{
		Name: "EXTRACT_COOKIE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.Request.Header.Get("Cookie")
			if val == "" {
				ctx.ByteSlots[destSlot] = nil
				return state.PC + 1
			}
			// Zero-copy reference into header string memory.
			raw := unsafe.Slice(unsafe.StringData(val), len(val))
			// Scan for "name=" pairs separated by "; "
			remaining := raw
			for len(remaining) > 0 {
				var pair []byte
				if i := bytes.IndexByte(remaining, ';'); i >= 0 {
					pair, remaining = remaining[:i], remaining[i+1:]
					// skip leading space after ';'
					for len(remaining) > 0 && remaining[0] == ' ' {
						remaining = remaining[1:]
					}
				} else {
					pair, remaining = remaining, nil
				}
				if bytes.HasPrefix(pair, nameBytes) {
					ctx.ByteSlots[destSlot] = pair[len(nameBytes):]
					return state.PC + 1
				}
			}
			ctx.ByteSlots[destSlot] = nil
			return state.PC + 1
		},
	}
}

// SetResponseCookie builds a Set-Cookie response header value and calls ctx.SetResponseHeader.
// Attributes are baked at compile time: Name (required), Path (default "/"), HttpOnly (bool),
// Secure (bool), MaxAge (int, 0=session), SameSite (string: "Strict"/"Lax"/"None"/empty).
// Value is read from ByteSlots[valueSlot] at runtime.
// Uses ctx.Alloc to build the header value string — no heap allocation.
func SetResponseCookie(cookieName string, valueSlot int, path string, maxAge int, httpOnly, secure bool, sameSite string) engine.Instruction {
	setCookieKey := []byte("Set-Cookie")
	// Build the static suffix (everything after the value): "; Path=/; HttpOnly; ..."
	// at instruction creation time, not per request.
	var suffix []byte
	if path != "" {
		suffix = append(suffix, "; Path="...)
		suffix = append(suffix, path...)
	}
	if maxAge > 0 {
		suffix = append(suffix, "; Max-Age="...)
		suffix = append(suffix, strconv.Itoa(maxAge)...)
	} else if maxAge < 0 {
		suffix = append(suffix, "; Max-Age=0"...)
	}
	if httpOnly {
		suffix = append(suffix, "; HttpOnly"...)
	}
	if secure {
		suffix = append(suffix, "; Secure"...)
	}
	if sameSite != "" {
		suffix = append(suffix, "; SameSite="...)
		suffix = append(suffix, sameSite...)
	}
	nameEq := []byte(cookieName + "=")
	return engine.Instruction{
		Name: "SET_RESPONSE_COOKIE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.ByteSlots[valueSlot]
			total := len(nameEq) + len(val) + len(suffix)
			buf := ctx.Alloc(total)
			n := copy(buf, nameEq)
			n += copy(buf[n:], val)
			copy(buf[n:], suffix)
			ctx.SetResponseHeader(setCookieKey, buf)
			return state.PC + 1
		},
	}
}

// SetRequestCookie appends a cookie to the Cookie request header for the upstream call.
// Reads cookie name (baked) and value from ByteSlots[valueSlot].
// Appends a HeaderMutation to MutationLog with Op=0 and key="Cookie".
// Uses ctx.Alloc to build the name=value bytes.
func SetRequestCookie(cookieName string, valueSlot int) engine.Instruction {
	nameEq := []byte(cookieName + "=")
	return engine.Instruction{
		Name: "SET_REQUEST_COOKIE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.ByteSlots[valueSlot]
			if len(val) == 0 {
				return state.PC + 1
			}
			if ctx.MutationCount >= len(ctx.MutationLog) {
				return state.PC + 1
			}
			buf := ctx.Alloc(len(nameEq) + len(val))
			n := copy(buf, nameEq)
			copy(buf[n:], val)
			ctx.MutationLog[ctx.MutationCount] = rctx.HeaderMutation{
				Key:   cookieHeaderKey,
				Value: buf,
				Op:    0, // Set
			}
			ctx.MutationCount++
			return state.PC + 1
		},
	}
}

// RemoveResponseCookie expires a cookie by setting Set-Cookie: name=; Max-Age=0; Path=/
// (or custom path if provided).
// Path is baked at compile time (default "/").
func RemoveResponseCookie(cookieName, path string) engine.Instruction {
	setCookieKey := []byte("Set-Cookie")
	if path == "" {
		path = "/"
	}
	// Build entire header value at bake time.
	header := []byte(cookieName + "=; Max-Age=0; Path=" + path)
	return engine.Instruction{
		Name: "REMOVE_RESPONSE_COOKIE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.SetResponseHeader(setCookieKey, header)
			return state.PC + 1
		},
	}
}

// CookieSlotBinding maps a cookie name to a slot index.
type CookieSlotBinding struct {
	Name []byte
	Slot int
}

// CookieFlattenStep builds a single Cookie header value string from multiple named
// slots, formatting as "name=value; name2=value2; ...". Empty slots are skipped.
func CookieFlattenStep(cookieSlots []CookieSlotBinding, dstSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "COOKIE_FLATTEN",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Calculate total length first.
			total := 0
			for _, cs := range cookieSlots {
				v := ctx.ByteSlots[cs.Slot]
				if len(v) == 0 {
					continue
				}
				if total > 0 {
					total += 2 // "; "
				}
				total += len(cs.Name) + 1 + len(v) // "name=value"
			}

			out := ctx.Alloc(total)
			pos := 0
			first := true
			for _, cs := range cookieSlots {
				v := ctx.ByteSlots[cs.Slot]
				if len(v) == 0 {
					continue
				}
				if !first {
					pos += copy(out[pos:], "; ")
				}
				pos += copy(out[pos:], cs.Name)
				out[pos] = '='
				pos++
				pos += copy(out[pos:], v)
				first = false
			}
			ctx.ByteSlots[dstSlot] = out[:pos]
			return state.PC + 1
		},
	}
}
