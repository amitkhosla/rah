package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
	"strings"
)

// BindInput maps a request source (header/query) to a Data Slot
// internal/engine/steps/binding.go
// internal/engine/steps/condition.go

func BindInput(source, key string, slot int) engine.Instruction {
	return engine.Instruction{
		Name: "BIND_" + strings.ToUpper(source),
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			switch source {
			case "header":
				// Standard lib Header.Get is fine, it handles the map lookup
				ctx.ByteSlots[slot] = []byte(ctx.Request.Header.Get(key))
			case "query":
				ctx.ByteSlots[slot] = []byte(ctx.Request.URL.Query().Get(key))
			case "path":
				// REPLACED: Use the index-based logic instead of ctx.PathParams map
				// Note: BindPath (above) is preferred for performance
			}
			return state.PC + 1
		},
	}
}

func SwitchGate(slot int, jumpTable map[string]int16, exitID int16) engine.Instruction {
	return engine.Instruction{
		Name: "SWITCH_GATE",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// FIX: Access ByteSlots[slot] and convert to string for the jumpTable lookup
			val := string(ctx.ByteSlots[slot])
			if next, ok := jumpTable[val]; ok {
				return next
			}
			return exitID
		},
	}
}

// FIX: Added the missing shouldRetry helper
func shouldRetry(ctx *rctx.Context, condition string) bool {
	// In a production engine, the compiler would parse 'condition' into a bytecode.
	// Here, we perform a fast check on the ResponseStatus in the Context.
	if condition == "status >= 500" {
		return ctx.ResponseStatus >= 500
	}
	return false
}

// RetryGate handles the loop logic for http_calls
func RetryGate(condition string, loopStart, exitID int16) engine.Instruction {
	return engine.Instruction{
		Name: "RETRY_GATE",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Logic to evaluate condition (e.g. status >= 500)
			if shouldRetry(ctx, condition) {
				return loopStart
			}
			return exitID
		},
	}
}

// RegistryLookup links the runtime to the Tenant/Global Registry
func RegistryLookup(keySlot, metaSlot int, scope string) engine.Instruction {
	return engine.Instruction{
		Name: "REG_LOOKUP",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Logic to fetch from your RegistryManager
			return s.PC + 1
		},
	}
}

/*
import (
	"rah/internal/engine"
	"rah/internal/rctx"
	"strconv"
)

func GenericConditionStep(slotIdx int, operator string, compareTo string, onTrue, onFalse int16) engine.Instruction {
	return engine.Instruction{
		Name: "GenericCondition",
		Action: func(ctx *rctx.Context, _ engine.ExecutionState) int16 {
			val := string(ctx.ByteSlots[slotIdx])

			match := false
			switch operator {
			case "gt":
				v, _ := strconv.Atoi(val)
				limit, _ := strconv.Atoi(compareTo)
				match = v > limit
			case "lt":
				v, _ := strconv.Atoi(val)
				limit, _ := strconv.Atoi(compareTo)
				match = v < limit
			case "eq":
				match = val == compareTo
			case "exists":
				match = len(val) > 0
			}

			if match {
				return onTrue
			}
			return onFalse
		},
	}
}
*/
