package steps

import (
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// TripCircuitInstruction returns an instruction that opens a named circuit breaker.
// The openDurationMs parameter specifies how long to hold the circuit open;
// 0 means use the circuit's configured default duration.
//
// At runtime, the circuit is immediately transitioned to Open state.
// If the circuit does not exist, it is created with default thresholds and the
// given override duration.
func TripCircuitInstruction(name string, openDurationMs uint32) engine.Instruction {
	return engine.Instruction{
		Name: "TRIP_CIRCUIT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			engine.TripNamedCircuit(name, openDurationMs)
			return state.PC + 1
		},
	}
}

// CheckCircuitInstruction returns an instruction that writes the current circuit state
// into ctx.ByteSlots[resultSlot]. The written state is one of:
//   - "closed"    — circuit is in Closed state (normal operation)
//   - "open"      — circuit is in Open state (tripped, requests blocked)
//   - "half_open" — circuit is in HalfOpen state (recovery probe phase)
//
// If the circuit does not exist (unknown name), it is treated as "closed".
// No heap allocation is performed; output bytes are allocated from the request arena.
func CheckCircuitInstruction(name string, resultSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "CHECK_CIRCUIT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			var stateStr string

			// Derive the state string. IsNamedCircuitOpen handles the Open→HalfOpen
			// auto-probe transition; GetNamedCircuitState reads the post-transition value
			// so we correctly distinguish "half_open" from "closed".
			if engine.IsNamedCircuitOpen(name) {
				stateStr = "open"
			} else if engine.GetNamedCircuitState(name) == engine.CBStateHalfOpen {
				stateStr = "half_open"
			} else {
				stateStr = "closed"
			}

			// Write the state string to the slot using arena allocation.
			stateBytes := ctx.Alloc(len(stateStr))
			copy(stateBytes, stateStr)
			ctx.ByteSlots[resultSlot] = stateBytes

			return state.PC + 1
		},
	}
}

// ResetCircuitInstruction returns an instruction that force-closes a named circuit breaker.
// The circuit is transitioned to Closed state, and all counters (failure and success)
// are reset. If the circuit does not exist, the instruction succeeds as a no-op.
func ResetCircuitInstruction(name string) engine.Instruction {
	return engine.Instruction{
		Name: "RESET_CIRCUIT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			engine.ResetNamedCircuit(name)
			return state.PC + 1
		},
	}
}
