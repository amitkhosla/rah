package steps

import (
	"encoding/base64"
	"encoding/hex"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// urlQuerySafe is a lookup table for URL query-safe characters (RFC 3986 unreserved).
// Avoids per-byte branching on conditional expressions.
var urlQuerySafe [256]bool

func init() {
	for c := 'A'; c <= 'Z'; c++ {
		urlQuerySafe[c] = true
	}
	for c := 'a'; c <= 'z'; c++ {
		urlQuerySafe[c] = true
	}
	for c := '0'; c <= '9'; c++ {
		urlQuerySafe[c] = true
	}
	urlQuerySafe['-'] = true
	urlQuerySafe['_'] = true
	urlQuerySafe['.'] = true
	urlQuerySafe['~'] = true
}

// hexDigits is used by urlEncodeStep for the percent-encoding output.
const hexDigits = "0123456789ABCDEF"

// Base64EncodeStep encodes ByteSlots[src] into ByteSlots[result] using base64.
// enc must be one of base64.StdEncoding, base64.URLEncoding, base64.RawURLEncoding.
func Base64EncodeStep(src, result int, enc *base64.Encoding) engine.Instruction {
	return engine.Instruction{
		Name: "BASE64_ENCODE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			out := ctx.Alloc(enc.EncodedLen(len(in)))
			enc.Encode(out, in)
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// Base64DecodeStep decodes ByteSlots[src] (base64) into ByteSlots[result].
// Silently clears the result slot on decode error.
func Base64DecodeStep(src, result int, enc *base64.Encoding) engine.Instruction {
	return engine.Instruction{
		Name: "BASE64_DECODE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			buf := ctx.Alloc(enc.DecodedLen(len(in)))
			n, err := enc.Decode(buf, in)
			if err != nil {
				ctx.ByteSlots[result] = nil
				return state.PC + 1
			}
			ctx.ByteSlots[result] = buf[:n]
			return state.PC + 1
		},
	}
}

// HexEncodeStep encodes ByteSlots[src] as lowercase hex into ByteSlots[result].
func HexEncodeStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "HEX_ENCODE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			out := ctx.Alloc(hex.EncodedLen(len(in)))
			hex.Encode(out, in)
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// HexDecodeStep decodes ByteSlots[src] (hex string) into ByteSlots[result].
// Silently clears result on decode error.
func HexDecodeStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "HEX_DECODE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			buf := ctx.Alloc(hex.DecodedLen(len(in)))
			n, err := hex.Decode(buf, in)
			if err != nil {
				ctx.ByteSlots[result] = nil
				return state.PC + 1
			}
			ctx.ByteSlots[result] = buf[:n]
			return state.PC + 1
		},
	}
}

// URLEncodeStep percent-encodes ByteSlots[src] (RFC 3986 unreserved pass-through).
// Two-pass: count encoded length, then write â€” avoids over-allocation.
func URLEncodeStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "URL_ENCODE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			// First pass: count output length.
			n := 0
			for _, b := range in {
				if urlQuerySafe[b] {
					n++
				} else {
					n += 3 // %XX
				}
			}
			out := ctx.Alloc(n)
			pos := 0
			for _, b := range in {
				if urlQuerySafe[b] {
					out[pos] = b
					pos++
				} else {
					out[pos] = '%'
					out[pos+1] = hexDigits[b>>4]
					out[pos+2] = hexDigits[b&0xF]
					pos += 3
				}
			}
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// URLDecodeStep decodes percent-encoded ByteSlots[src] into ByteSlots[result].
// '+' is decoded as space (application/x-www-form-urlencoded convention).
func URLDecodeStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "URL_DECODE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			// Output is always <= input length.
			out := ctx.Alloc(len(in))
			pos := 0
			for i := 0; i < len(in); {
				b := in[i]
				if b == '+' {
					out[pos] = ' '
					pos++
					i++
				} else if b == '%' && i+2 < len(in) {
					h := unhex(in[i+1])
					l := unhex(in[i+2])
					if h >= 0 && l >= 0 {
						out[pos] = byte(h<<4 | l)
						pos++
						i += 3
						continue
					}
					// Invalid sequence â€” pass through verbatim.
					out[pos] = b
					pos++
					i++
				} else {
					out[pos] = b
					pos++
					i++
				}
			}
			ctx.ByteSlots[result] = out[:pos]
			return state.PC + 1
		},
	}
}

// unhex converts an ASCII hex character to its nibble value (0-15), or -1 if invalid.
func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
