package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
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
			ctx.ByteSlots[slot] = []byte(ctx.Request.Header.Get(key))
			return state.PC + 1
		},
	}
}

func BindQuery(key string, slot int) engine.Instruction {
	return engine.Instruction{
		Name: "BIND_QUERY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ByteSlots[slot] = []byte(ctx.Request.URL.Query().Get(key))
			return state.PC + 1
		},
	}
}
