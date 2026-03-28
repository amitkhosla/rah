package steps

import (
	"encoding/json"
	"rah/internal/engine"
	"rah/internal/rctx"
	"strings"
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
	if len(stack) == 0 {
		return false
	}
	var results []bool
	for _, val := range stack {
		switch val {
		case OpAnd:
			if len(results) < 2 {
				return false
			}
			l, r := results[len(results)-2], results[len(results)-1]
			results = results[:len(results)-2]
			results = append(results, l && r)
		case OpOr:
			if len(results) < 2 {
				return false
			}
			l, r := results[len(results)-2], results[len(results)-1]
			results = results[:len(results)-2]
			results = append(results, l || r)
		default:
			// Treat non-empty byte slot as "true"; out-of-range slot = false
			if val >= 0 && val < len(ctx.ByteSlots) {
				results = append(results, len(ctx.ByteSlots[val]) > 0)
			} else {
				results = append(results, false)
			}
		}
	}
	if len(results) == 0 {
		return false
	}
	return results[0]
}

// parseToRPN converts a simple boolean condition string into a postfix (RPN)
// integer stack. Variable names are replaced with their slot IDs from slotMap.
// Supports: single variables, && (AND), || (OR), left-to-right evaluation.
// Parentheses are stripped (no precedence grouping beyond left-to-right).
//
// Examples:
//
//	"header.X-Admin"           → [slotID]
//	"header.X-Admin && query.role" → [slotA, slotB, OpAnd]
func parseToRPN(cond string, slotMap map[string]int) []int {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return nil
	}
	tokens := strings.Fields(cond)
	var result []int
	var pendingOp *int
	for _, tok := range tokens {
		tok = strings.Trim(tok, "()")
		switch tok {
		case "&&":
			op := OpAnd
			pendingOp = &op
		case "||":
			op := OpOr
			pendingOp = &op
		default:
			idx, ok := slotMap[tok]
			if !ok {
				continue // unknown variable — skip
			}
			result = append(result, idx)
			if pendingOp != nil {
				result = append(result, *pendingOp)
				pendingOp = nil
			}
		}
	}
	return result
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

// LoopGateSlot iterates over a JSON array stored in ctx.ByteSlots[sourceSlot].
// Each iteration writes the raw JSON element to ctx.ByteSlots[valueSlot].
// Uses iterSlot (IntSlot) as the loop counter. Resets to 0 on exit.
func LoopGateSlot(sourceSlot int, valueSlot int, iterSlot int, bodyStart int16, exitID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		raw := ctx.ByteSlots[sourceSlot]
		if len(raw) == 0 {
			return exitID
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
			return exitID
		}
		idx := ctx.GetInt(iterSlot)
		if idx >= int64(len(items)) {
			ctx.SetInt(iterSlot, 0)
			return exitID
		}
		ctx.SetSlot(valueSlot, []byte(items[idx]))
		ctx.SetInt(iterSlot, idx+1)
		return bodyStart
	}
}

// WhileGate loops while ctx.BoolSlots[condSlot] is true, up to maxIter times.
// iterSlot (IntSlot) tracks the iteration count; reset to 0 on exit.
// maxIter <= 0 defaults to 100 to prevent infinite loops.
func WhileGate(condSlot int, iterSlot int, maxIter int, bodyStart int16, exitID int16) engine.InstructionFunc {
	limit := int64(maxIter)
	if limit <= 0 {
		limit = 100
	}
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		iter := ctx.GetInt(iterSlot)
		cond := condSlot >= 0 && condSlot < len(ctx.BoolSlots) && ctx.BoolSlots[condSlot]
		if !cond || iter >= limit {
			ctx.SetInt(iterSlot, 0)
			return exitID
		}
		ctx.SetInt(iterSlot, iter+1)
		return bodyStart
	}
}

// WhileRepeat jumps back to the WhileGate check without incrementing the counter
// (WhileGate itself handles counting).
func WhileRepeat(gateID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
		return gateID
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
