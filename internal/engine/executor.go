package engine

import (
	"rah/internal/observability"
	"rah/internal/rctx"
	"strconv"
	"time"
)

// StopPlan is a sentinel value. When an action returns this,
// the execution loop terminates immediately.
const StopPlan int16 = -1

// ExecutionState represents the "Control Plane".
// It is allocated on the GOROUTINE STACK, not the heap.
// This means the Garbage Collector (GC) never touches this object.
type ExecutionState struct {
	// LinkStack stores absolute IDs for function-call returns.
	// Index 0 might be the return for Fragment A, Index 1 for Fragment B, etc.
	LinkStack [16]int16
	// StackPtr tracks our current depth in the LinkStack.
	StackPtr  int8
	PC        int16
	IsStopped bool

	// SlotOverflow is the DataStore-backed overflow store for slot values that
	// exceed arena capacity or slot indices beyond in-memory limits.
	// Nil when no overflow store is configured — the common case.
	// Set by FlowManager before Execute; propagated to sub-flows via FlowRegistry.
	SlotOverflow rctx.SlotOverflowStore
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
// store may be nil; it is set on ExecutionState so instructions can access it.
func Execute(ctx *rctx.Context, table []Instruction, startID int16, store rctx.SlotOverflowStore) {
	pc := startID
	tableLen := int16(len(table))

	// ExecutionState lives on stack (no GC pressure)
	state := ExecutionState{SlotOverflow: store}

	for pc >= 0 && pc < tableLen {
		state.PC = pc // Keep PC in sync for instructions that use it
		current := table[pc]
		shouldMeasure := ctx.Obs != nil && (ctx.Trace != nil || ctx.Obs.InstructionTimingEnabled())
		var started time.Time
		if shouldMeasure {
			started = time.Now()
		}
		pc = current.Action(ctx, &state)
		var duration time.Duration
		if shouldMeasure {
			duration = time.Since(started)
			ctx.Obs.RecordInstruction(current.Name, duration)
		}
		slot0Len := 0
		if len(ctx.ByteSlots) > 0 {
			slot0Len = len(ctx.ByteSlots[0])
		}
		if shouldMeasure && ctx.Trace != nil && ctx.Obs != nil {
			ctx.Obs.AppendInstructionEvent(ctx.Trace, observability.InstructionEvent{
				Name:       current.Name,
				PC:         state.PC,
				DurationNs: duration.Nanoseconds(),
				Input:      []observability.KV{{K: "response_status", V: strconv.Itoa(ctx.ResponseStatus)}, {K: "slot0_len", V: strconv.Itoa(slot0Len)}},
				Output:     []observability.KV{{K: "next_pc", V: strconv.Itoa(int(pc))}},
			})
		}

		if pc == StopPlan {
			break
		}
	}
}
