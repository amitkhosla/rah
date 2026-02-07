package engine

import "rah/internal/rctx"

// Instruction is the fundamental execution unit.
type Instruction struct {
	Name   string
	Action func(ctx *rctx.Context) int16
}

// Run is the hot-path loop that navigates the instruction array.
func Run(plan []Instruction, ctx *rctx.Context) {
	pc := 0
	planLen := len(plan)
	for pc < planLen {
		offset := plan[pc].Action(ctx)

		if offset == StopPlan {
			break
		}

		pc += int(offset)
	}
}
