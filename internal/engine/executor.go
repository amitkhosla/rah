package engine

import "rah/internal/rctx"

// StopPlan is a sentinel value. When an action returns this,
// the execution loop terminates immediately.
const StopPlan int16 = -1

// ExecutionState represents the "Control Plane".
// It is allocated on the GOROUTINE STACK, not the heap.
// This means the Garbage Collector (GC) never touches this object.
type ExecutionState struct {
	// LinkStack stores absolute IDs for function-call returns.
	// Index 0 might be the return for Fragment A, Index 1 for Fragment B, etc.
	LinkStack [4]int16
	// StackPtr tracks our current depth in the LinkStack.
	StackPtr  int8
	PC        int16
	IsStopped bool
}

// InstructionFunc represents the logic of a single instruction.
// ctx: The "Data Plane" (request data, slots, buffers)
// state: The "Control Plane" (program counter, return stack)
// Returns: The ABSOLUTE ID of the next instruction to execute.
type InstructionFunc func(ctx *rctx.Context, state *ExecutionState) int16

// Instruction is a single unit of work in the flattened GlobalTable.
type Instruction struct {
	Name string
	// Action takes the Data Plane (ctx) and Control Plane (state).
	// It returns the absolute ID of the NEXT instruction to execute.
	Action InstructionFunc
}

// Execute runs a compiled instruction table using absolute jumps.
// It assumes each Instruction.Action returns the ABSOLUTE next PC.
func Execute(ctx *rctx.Context, table []Instruction, startID int16) {
	pc := startID
	tableLen := int16(len(table))

	// ExecutionState lives on stack (no GC pressure)
	var state ExecutionState

	for pc >= 0 && pc < tableLen {
		state.PC = pc // Keep PC in sync for instructions that use it

		pc = table[pc].Action(ctx, &state)

		if pc == StopPlan {
			break
		}
	}
}
