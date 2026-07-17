package steps

import (
	"bytes"
	"encoding/binary"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"strconv"
)

// ConcatStep concatenates ByteSlots[slotA] + sep + ByteSlots[slotB] into ByteSlots[result].
func ConcatStep(slotA, slotB, result int, sep string) engine.Instruction {
	sepBytes := []byte(sep)
	return engine.Instruction{
		Name: "CONCAT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			a := ctx.ByteSlots[slotA]
			b := ctx.ByteSlots[slotB]
			n := len(a) + len(sepBytes) + len(b)
			out := ctx.Alloc(n)
			written := copy(out, a)
			written += copy(out[written:], sepBytes)
			copy(out[written:], b)
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// ToLowerStep lowercases ByteSlots[src] into ByteSlots[result].
// Uses an inline ASCII loop with ctx.Alloc to avoid heap allocation.
func ToLowerStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "TO_LOWER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			out := ctx.Alloc(len(in))
			for i, c := range in {
				if c >= 'A' && c <= 'Z' {
					out[i] = c + 32
				} else {
					out[i] = c
				}
			}
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// ToUpperStep uppercases ByteSlots[src] into ByteSlots[result].
// Uses an inline ASCII loop with ctx.Alloc to avoid heap allocation.
func ToUpperStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "TO_UPPER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			out := ctx.Alloc(len(in))
			for i, c := range in {
				if c >= 'a' && c <= 'z' {
					out[i] = c - 32
				} else {
					out[i] = c
				}
			}
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// SubstringStep extracts a substring of ByteSlots[src] into ByteSlots[result].
// length == -1 means "to end of string".
func SubstringStep(src, result, start, length int) engine.Instruction {
	return engine.Instruction{
		Name: "SUBSTRING",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			s := ctx.ByteSlots[src]
			if start < 0 || start > len(s) {
				ctx.ByteSlots[result] = nil
				return state.PC + 1
			}
			end := len(s)
			if length >= 0 && start+length < end {
				end = start + length
			}
			ctx.ByteSlots[result] = s[start:end]
			return state.PC + 1
		},
	}
}

// ToIntStep parses ByteSlots[src] as int64 into IntSlots[result]. Parse errors yield 0.
func ToIntStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "TO_INT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			v, err := strconv.ParseInt(string(ctx.ByteSlots[src]), 10, 64)
			if err != nil {
				v = 0
			}
			ctx.IntSlots[result] = v
			return state.PC + 1
		},
	}
}

// AddStep stores IntSlots[slotA] + IntSlots[slotB] into IntSlots[result].
func AddStep(slotA, slotB, result int) engine.Instruction {
	return engine.Instruction{
		Name: "ADD",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.IntSlots[result] = ctx.IntSlots[slotA] + ctx.IntSlots[slotB]
			return state.PC + 1
		},
	}
}

// SubStep stores IntSlots[slotA] - IntSlots[slotB] into IntSlots[result].
func SubStep(slotA, slotB, result int) engine.Instruction {
	return engine.Instruction{
		Name: "SUB",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.IntSlots[result] = ctx.IntSlots[slotA] - ctx.IntSlots[slotB]
			return state.PC + 1
		},
	}
}

// MulStep stores IntSlots[slotA] * IntSlots[slotB] into IntSlots[result].
func MulStep(slotA, slotB, result int) engine.Instruction {
	return engine.Instruction{
		Name: "MUL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.IntSlots[result] = ctx.IntSlots[slotA] * ctx.IntSlots[slotB]
			return state.PC + 1
		},
	}
}

// DivStep stores IntSlots[slotA] / IntSlots[slotB] into IntSlots[result].
// Division by zero stops the plan.
func DivStep(slotA, slotB, result int) engine.Instruction {
	return engine.Instruction{
		Name: "DIV",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if ctx.IntSlots[slotB] == 0 {
				return engine.StopPlan
			}
			ctx.IntSlots[result] = ctx.IntSlots[slotA] / ctx.IntSlots[slotB]
			return state.PC + 1
		},
	}
}

// ByteLengthStep writes the byte-length of ByteSlots[srcSlot] into IntSlots[destSlot].
func ByteLengthStep(srcSlot, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "BYTE_LENGTH",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.IntSlots[destSlot] = int64(len(ctx.ByteSlots[srcSlot]))
			return state.PC + 1
		},
	}
}

// TrimStep removes leading/trailing whitespace from ByteSlots[src] (zero-copy sub-slice).
func TrimStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "TRIM",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ByteSlots[result] = bytes.TrimSpace(ctx.ByteSlots[src])
			return state.PC + 1
		},
	}
}

// ContainsStep writes true to BoolSlots[result] if ByteSlots[src] contains needle (baked).
func ContainsStep(src, result int, needle []byte) engine.Instruction {
	return engine.Instruction{
		Name: "CONTAINS",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.BoolSlots[result] = bytes.Contains(ctx.ByteSlots[src], needle)
			return state.PC + 1
		},
	}
}

// StartsWithStep writes true to BoolSlots[result] if ByteSlots[src] has prefix (baked).
func StartsWithStep(src, result int, prefix []byte) engine.Instruction {
	return engine.Instruction{
		Name: "STARTS_WITH",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.BoolSlots[result] = bytes.HasPrefix(ctx.ByteSlots[src], prefix)
			return state.PC + 1
		},
	}
}

// EndsWithStep writes true to BoolSlots[result] if ByteSlots[src] has suffix (baked).
func EndsWithStep(src, result int, suffix []byte) engine.Instruction {
	return engine.Instruction{
		Name: "ENDS_WITH",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.BoolSlots[result] = bytes.HasSuffix(ctx.ByteSlots[src], suffix)
			return state.PC + 1
		},
	}
}

// ReplaceStep replaces all occurrences of old (baked) with new (baked) in ByteSlots[src].
// Uses bytes.ReplaceAll â€” one allocation for the output; unavoidable when output size is unknown.
func ReplaceStep(src, result int, old, new []byte) engine.Instruction {
	return engine.Instruction{
		Name: "REPLACE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ByteSlots[result] = bytes.ReplaceAll(ctx.ByteSlots[src], old, new)
			return state.PC + 1
		},
	}
}

// SplitStep splits ByteSlots[src] on sep (baked) and stores a JSON array of strings
// into ByteSlots[result] using ctx.Alloc for zero extra heap pressure.
// Output format: ["part1","part2","part3"]
func SplitStep(src, result int, sep []byte) engine.Instruction {
	return engine.Instruction{
		Name: "SPLIT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			raw := ctx.ByteSlots[src]
			if len(raw) == 0 {
				ctx.ByteSlots[result] = []byte("[]")
				return state.PC + 1
			}
			parts := bytes.Split(raw, sep)
			// Calculate total arena allocation: 2 (brackets) + per-part: 2 quotes + len + 1 comma
			total := 2
			for _, p := range parts {
				total += len(p) + 3 // "..." + comma (last omits comma)
			}
			buf := ctx.Alloc(total)
			pos := 0
			buf[pos] = '['
			pos++
			for i, p := range parts {
				buf[pos] = '"'
				pos++
				copy(buf[pos:], p)
				pos += len(p)
				buf[pos] = '"'
				pos++
				if i < len(parts)-1 {
					buf[pos] = ','
					pos++
				}
			}
			buf[pos] = ']'
			pos++
			ctx.ByteSlots[result] = buf[:pos]
			return state.PC + 1
		},
	}
}

// IndexOfStep writes the byte offset of needle (baked) in ByteSlots[src] to IntSlots[result].
// Returns -1 if not found.
func IndexOfStep(src, result int, needle []byte) engine.Instruction {
	return engine.Instruction{
		Name: "INDEX_OF",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.IntSlots[result] = int64(bytes.Index(ctx.ByteSlots[src], needle))
			return state.PC + 1
		},
	}
}

// JoinStep joins ByteSlots[src] (a JSON array of strings) with sep (baked) into ByteSlots[result].
// Uses the same gjson packed-index trick â€” O(1) per element, arena allocation.
func JoinStep(src, result int, sep []byte) engine.Instruction {
	return engine.Instruction{
		Name: "JOIN",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			raw := ctx.ByteSlots[src]
			if len(raw) == 0 {
				ctx.ByteSlots[result] = nil
				return state.PC + 1
			}
			// Parse offsets â€” 8 bytes per element (start uint32, end uint32).
			type span struct{ s, e uint32 }
			var spans [64]span
			n := 0
			total := 0
			// Quick scan: find quoted substrings in JSON array.
			// We read byte-by-byte â€” no reflection, no GC pressure.
			in := raw
			i := 1 // skip '['
			for i < len(in) && in[i] != ']' {
				if in[i] == '"' {
					j := i + 1
					for j < len(in) && in[j] != '"' {
						if in[j] == '\\' {
							j++
						}
						j++
					}
					if n < len(spans) {
						spans[n] = span{uint32(i + 1), uint32(j)}
						total += int(spans[n].e-spans[n].s) + len(sep)
						n++
					}
					i = j + 1
				} else {
					i++
				}
			}
			if n == 0 {
				ctx.ByteSlots[result] = nil
				return state.PC + 1
			}
			if total > 0 {
				total -= len(sep) // no trailing separator
			}
			buf := ctx.Alloc(total)
			pos := 0
			for k := 0; k < n; k++ {
				copy(buf[pos:], in[spans[k].s:spans[k].e])
				pos += int(spans[k].e - spans[k].s)
				if k < n-1 {
					copy(buf[pos:], sep)
					pos += len(sep)
				}
			}
			ctx.ByteSlots[result] = buf[:pos]
			return state.PC + 1
		},
	}
}

// splitPackedIndex is shared by SplitStep-derived ops.
// It extracts element i from a packed index buf (8 bytes per element) as (start, end) indices
// into the source byte slice.
func splitPackedGet(buf []byte, i int) (start, end uint32) {
	return binary.LittleEndian.Uint32(buf[i*8:]), binary.LittleEndian.Uint32(buf[i*8+4:])
}

// SetResponseHeaderFromSlot sets a response header named `name` from ByteSlots[src].
func SetResponseHeaderFromSlot(name string, src int) engine.Instruction {
	nameBytes := []byte(name)
	return engine.Instruction{
		Name: "SET_RESPONSE_HEADER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.SetResponseHeader(nameBytes, ctx.ByteSlots[src])
			return state.PC + 1
		},
	}
}
