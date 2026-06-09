package engine

import (
	"rah/internal/observability"
	"rah/internal/rctx"
	"time"
)

// StopPlan is a sentinel value. When an action returns this,
// the execution loop terminates immediately.
const StopPlan int16 = -1

// StopCancelled is returned by steps when the client disconnects mid-flow.
// The execute loop treats any negative PC as a stop signal; main.go checks
// ctx.Cancelled to distinguish this from a flow error and returns 499.
const StopCancelled int16 = -2

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

	// slotValueThreshold overrides rctx.SlotValueThreshold when non-zero.
	// Set by FlowManager from Config.SlotValueThreshold; 0 means use default.
	slotValueThreshold int

	TraceAttrs     [24][2]string
	traceAttrCount int8

}

func (s *ExecutionState) AddTraceAttr(k, v string) {
	if s.traceAttrCount < int8(len(s.TraceAttrs)) {
		s.TraceAttrs[s.traceAttrCount] = [2]string{k, v}
		s.traceAttrCount++
	}
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
	// StepIdx is the index of the user-defined flow step that emitted this
	// instruction. -1 means a system/infrastructure instruction (auto-bind,
	// RET, STOP, GOTO). Set at compile time; zero runtime cost.
	StepIdx int16
}

// Execute runs a compiled instruction table using absolute jumps.
// Per-instruction timing is written into ctx.InstrPC / ctx.InstrDurNs /
// ctx.InstrCount so callers can hand them off to instrSlabRing.Write without
// paying the cost of returning a large struct by value.
func Execute(ctx *rctx.Context, table []Instruction, startID int16) {
	pc := startID
	tableLen := int16(len(table))

	// ExecutionState lives on this goroutine's stack (no GC pressure).
	var state ExecutionState

	for pc >= 0 && pc < tableLen {
		state.PC = pc
		current := table[pc]
		shouldTrace := ctx.Obs != nil && ctx.Trace != nil
		shouldMeasure := ctx.Obs != nil && (shouldTrace || ctx.Obs.InstructionTimingEnabled())
		var started time.Time
		if shouldMeasure {
			started = time.Now()
		}
		pc = current.Action(ctx, &state)

		// Accumulate PC and timing into ctx (not state) so Execute can return
		// void — avoids copying the ~1200-byte ExecutionState on every request.
		if ctx.InstrCount < 64 {
			ctx.InstrPC[ctx.InstrCount] = state.PC
			if shouldMeasure {
				ns := time.Since(started).Nanoseconds()
				if ns <= 0 {
					ns = 1
				}
				ctx.InstrDurNs[ctx.InstrCount] = int32(ns)
				if shouldTrace {
					var outputKVs []observability.KV
					if state.traceAttrCount > 0 {
						outputKVs = make([]observability.KV, 0, int(state.traceAttrCount))
						for i := int8(0); i < state.traceAttrCount; i++ {
							outputKVs = append(outputKVs, observability.KV{K: state.TraceAttrs[i][0], V: state.TraceAttrs[i][1]})
						}
					}
					state.traceAttrCount = 0
					ctx.Obs.AppendInstructionEvent(ctx.Trace, observability.InstructionEvent{
						PC:         state.PC,
						StepIdx:    current.StepIdx,
						DurationNs: ns,
						Output:     outputKVs,
					})
				}
			} else {
				ctx.InstrDurNs[ctx.InstrCount] = 0
			}
			ctx.InstrCount++
		}

		if pc == StopPlan {
			break
		}
	}
}
