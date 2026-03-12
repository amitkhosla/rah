package steps

import (
	"bytes"
	"rah/internal/engine"
	"rah/internal/rctx"
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
			out := make([]byte, len(a)+len(sepBytes)+len(b))
			n := copy(out, a)
			n += copy(out[n:], sepBytes)
			copy(out[n:], b)
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// ToLowerStep lowercases ByteSlots[src] into ByteSlots[result].
func ToLowerStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "TO_LOWER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ByteSlots[result] = bytes.ToLower(ctx.ByteSlots[src])
			return state.PC + 1
		},
	}
}

// ToUpperStep uppercases ByteSlots[src] into ByteSlots[result].
func ToUpperStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "TO_UPPER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ByteSlots[result] = bytes.ToUpper(ctx.ByteSlots[src])
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
