package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
	"strconv"
)

func GenericConditionStep(slotIdx int, operator string, compareTo string, onTrue, onFalse int16) engine.Instruction {
	return engine.Instruction{
		Name: "GenericCondition",
		Action: func(ctx *rctx.Context) int16 {
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
