package steps

import (
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// JsonMergeSource describes one input slot and how it contributes to the merged output.
type JsonMergeSource struct {
	Slot int
	Key  string // wrap key; empty string means spread top-level fields
}

// JsonMerge merges N JSON slot values into a single JSON object written to targetSlot.
// Sources with empty or null slot values are skipped.
// When Key is non-empty, wraps the value: {"key": <value>}.
// When Key is empty, spreads top-level fields directly into the output.
func JsonMerge(sources []JsonMergeSource, targetSlot int) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		est := 2
		for _, s := range sources {
			v := ctx.ByteSlots[s.Slot]
			if len(v) == 0 {
				continue
			}
			est += len(s.Key) + 4 + len(v) + 2
		}

		buf := ctx.Alloc(est)
		n := 0
		buf[n] = '{'
		n++
		first := true

		for _, src := range sources {
			val := ctx.ByteSlots[src.Slot]
			if len(val) == 0 {
				continue
			}
			isNull := len(val) == 4 && val[0] == 'n' && val[1] == 'u' && val[2] == 'l' && val[3] == 'l'
			if isNull {
				continue
			}

			if src.Key == "" {
				inner := jsonSpreadInner(val)
				if len(inner) == 0 {
					continue
				}
				if !first {
					buf[n] = ','
					n++
				}
				n += copy(buf[n:], inner)
				first = false
			} else {
				if !first {
					buf[n] = ','
					n++
				}
				buf[n] = '"'
				n++
				n += copy(buf[n:], src.Key)
				buf[n] = '"'
				n++
				buf[n] = ':'
				n++
				n += copy(buf[n:], val)
				first = false
			}
		}

		buf[n] = '}'
		n++
		ctx.ByteSlots[targetSlot] = buf[:n]
		return state.PC + 1
	}
}

// jsonSpreadInner strips the outer braces from a JSON object and returns the inner content.
// Returns nil if val is not a non-empty JSON object.
func jsonSpreadInner(val []byte) []byte {
	s, e := 0, len(val)-1
	for s < len(val) && isJSONSpace(val[s]) {
		s++
	}
	for e > s && isJSONSpace(val[e]) {
		e--
	}
	if s >= len(val) || val[s] != '{' || val[e] != '}' {
		return nil
	}
	inner := val[s+1 : e]
	for _, b := range inner {
		if !isJSONSpace(b) {
			return inner
		}
	}
	return nil
}

func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
