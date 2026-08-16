package steps

import (
	"strconv"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// SetCookieStep sets a Set-Cookie response header.
//
// The full header value is split into a pre-built prefix (name=) and suffix
// (; Path=/; ...) at compile time. Only the dynamic value portion is read from
// ByteSlots[valueSlot] at runtime. All three segments are written into an arena
// allocation — zero heap allocation on the hot path.
//
// Parameters are all baked at compile time:
//
//	namePrefix  — "name=" as []byte
//	suffix      — pre-built "; Path=/; Max-Age=3600; HttpOnly; Secure; SameSite=Lax"
//	valueSlot   — ByteSlot index holding the runtime cookie value
func SetCookieStep(namePrefix, suffix []byte, valueSlot int) engine.Instruction {
	setCookieKey := []byte("Set-Cookie")
	return engine.Instruction{
		Name: "SET_COOKIE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.ByteSlots[valueSlot]
			total := len(namePrefix) + len(val) + len(suffix)
			buf := ctx.Alloc(total)
			n := copy(buf, namePrefix)
			n += copy(buf[n:], val)
			copy(buf[n:], suffix)
			ctx.SetResponseHeader(setCookieKey, buf)
			return state.PC + 1
		},
	}
}

// RedirectStep sends an HTTP redirect response.
//
// If staticURL is non-nil (literal URL baked at compile time), it is used
// directly with no runtime allocation. If staticURL is nil, the URL is read
// from ByteSlots[urlSlot] at runtime and written into the arena.
//
// status is 301 or 302 (baked at compile time).
func RedirectStep(staticURL []byte, urlSlot int, status int) engine.Instruction {
	locationKey := []byte("Location")
	return engine.Instruction{
		Name: "REDIRECT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			var loc []byte
			if staticURL != nil {
				// Literal URL: no allocation, point directly at compile-time bytes.
				loc = staticURL
			} else {
				// Dynamic URL from slot: copy into arena so ResponseHeaders retains
				// a valid reference after ByteSlots[urlSlot] is potentially cleared.
				src := ctx.ByteSlots[urlSlot]
				buf := ctx.Alloc(len(src))
				copy(buf, src)
				loc = buf
			}
			ctx.SetResponseHeader(locationKey, loc)
			ctx.ResponseStatus = status
			return engine.StopPlan
		},
	}
}

// buildSetCookieSuffix constructs the static suffix of a Set-Cookie header
// (everything after the value) at bake time so the hot path never allocates.
//
// Format: [; Path=<p>][; Max-Age=<n>][; HttpOnly][; Secure][; SameSite=<s>][; Domain=<d>]
func BuildSetCookieSuffix(path string, maxAgeSec int, httpOnly, secure bool, sameSite, domain string) []byte {
	var buf []byte
	if path != "" {
		buf = append(buf, "; Path="...)
		buf = append(buf, path...)
	}
	if maxAgeSec > 0 {
		buf = append(buf, "; Max-Age="...)
		buf = strconv.AppendInt(buf, int64(maxAgeSec), 10)
	}
	if httpOnly {
		buf = append(buf, "; HttpOnly"...)
	}
	if secure {
		buf = append(buf, "; Secure"...)
	}
	switch sameSite {
	case "Strict", "Lax", "None":
		buf = append(buf, "; SameSite="...)
		buf = append(buf, sameSite...)
	}
	if domain != "" {
		buf = append(buf, "; Domain="...)
		buf = append(buf, domain...)
	}
	return buf
}
