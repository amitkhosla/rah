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

// RetryGate handles the loop logic for http_calls using a pre-compiled ConditionFunc.
// condFn is built by CompileCondition at bake time; evaluated on every retry check.
func RetryGate(condFn ConditionFunc, loopStart, exitID int16) engine.Instruction {
	return engine.Instruction{
		Name: "RETRY_GATE",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if condFn(ctx) {
				return loopStart
			}
			return exitID
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
