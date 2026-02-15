package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
)

// ComplexLogicGate evaluates a pre-parsed RPN condition against slots.
func NewComplexLogicGate(condition string, thenID, elseID int16, slotMap map[string]int) engine.Instruction {
	// Pre-parse the condition into an RPN stack of Slot IDs and Operators
	// Example: "A && B" becomes [SlotA, SlotB, OP_AND]
	rpnStack := parseToRPN(condition, slotMap)

	return engine.Instruction{
		Name: "IF_GATE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if evaluateRPN(ctx, rpnStack) {
				return thenID
			}
			return elseID
		},
	}
}

const (
	OpAnd = -1
	OpOr  = -2
)

func evaluateRPN(ctx *rctx.Context, stack []int) bool {
	var results []bool
	for _, val := range stack {
		switch val {
		case OpAnd:
			l, r := results[len(results)-2], results[len(results)-1]
			results = results[:len(results)-2]
			results = append(results, l && r)
		case OpOr:
			l, r := results[len(results)-2], results[len(results)-1]
			results = results[:len(results)-2]
			results = append(results, l || r)
		default:
			// Treat non-empty/non-zero byte slot as "true"
			results = append(results, len(ctx.ByteSlots[val]) > 0)
		}
	}
	return results[0]
}

// Simple RPN parser (Stub - would typically involve a Shunting-Yard algorithm)
func parseToRPN(cond string, slotMap map[string]int) []int {
	// Implementation of Shunting-Yard would go here to convert
	// string logic to the integer stack used in evaluateRPN
	return []int{}
}

// NewComplexLogicGate creates an instruction that evaluates boolean logic
// against ByteSlots using a pre-compiled RPN stack.

func executeLogicPlan(ctx *rctx.Context, plan []int) bool {
	// Logic implementation:
	// Positive numbers = Slot IDs (Check if non-empty)
	// -1 = AND, -2 = OR
	var stack []bool
	for _, op := range plan {
		if op >= 0 {
			stack = append(stack, len(ctx.ByteSlots[op]) > 0)
		} else if op == -1 { // AND
			l, r := stack[len(stack)-2], stack[len(stack)-1]
			stack = stack[:len(stack)-2]
			stack = append(stack, l && r)
		} // ... handle OR (-2) etc.
	}
	return stack[0]
}

func executeLogic(ctx *rctx.Context, plan []int) bool {
	var stack []bool
	for _, op := range plan {
		if op >= 0 {
			// Check if the slot has data (exists/true)
			stack = append(stack, len(ctx.ByteSlots[op]) > 0)
		} else {
			// Handle Operators: -1 (AND), -2 (OR)
			b := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			a := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if op == -1 {
				stack = append(stack, a && b)
			}
			if op == -2 {
				stack = append(stack, a || b)
			}
		}
	}
	return stack[0]
}

func LoopGate(source string, valueSlot int, iterSlot int, bodyStart int16, exitID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		items := ctx.GetCollection(source)
		idx := ctx.GetInt(iterSlot)

		if idx >= int64(len(items)) {
			ctx.SetInt(iterSlot, 0) // Reset for future calls
			return exitID           // JUMP OUT
		}

		ctx.SetSlot(valueSlot, items[idx])
		ctx.SetInt(iterSlot, idx+1)
		return bodyStart // JUMP INTO BODY
	}
}

func LoopRepeat(gateID int16, iterSlot int) engine.InstructionFunc {
	return func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
		ctx.IntSlots[iterSlot]++ // Increment the iterator
		return gateID            // Jump back to the LoopGate check
	}
}

func CallFragment(entryID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		// Push the NEXT instruction after this one onto the return stack
		state.LinkStack[state.StackPtr] = state.PC + 1
		state.StackPtr++
		return entryID // JUMP to shared flow
	}
}

func Return() engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		state.StackPtr--
		return state.LinkStack[state.StackPtr] // JUMP BACK
	}
}

// internal/engine/steps/logic.go

func InternalJump(targetID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		return targetID // Simple absolute jump back to the LoopGate
	}
}
