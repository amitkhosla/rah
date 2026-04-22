package engine

import (
	"rah/internal/observability"
	"rah/internal/rctx"
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
}

// Execute runs a compiled instruction table using absolute jumps.
// It assumes each Instruction.Action returns the ABSOLUTE next PC.
func Execute(ctx *rctx.Context, table []Instruction, startID int16) {
	pc := startID
	tableLen := int16(len(table))

	// ExecutionState lives on stack (no GC pressure)
	state := ExecutionState{}

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
			// Extremely fast instructions can appear as 0ns on some platforms.
			// Preserve attribution by recording a minimal non-zero duration.
			if duration <= 0 {
				duration = time.Nanosecond
			}
			ctx.Obs.RecordInstruction(current.Name, duration)
		}
		if shouldMeasure && ctx.Trace != nil && ctx.Obs != nil {
			var outputKVs []observability.KV
			if state.traceAttrCount > 0 {
				outputKVs = make([]observability.KV, 0, int(state.traceAttrCount))
				for i := int8(0); i < state.traceAttrCount; i++ {
					outputKVs = append(outputKVs, observability.KV{K: state.TraceAttrs[i][0], V: state.TraceAttrs[i][1]})
				}
			}
			state.traceAttrCount = 0
			ctx.Obs.AppendInstructionEvent(ctx.Trace, observability.InstructionEvent{
				Name:       current.Name,
				PC:         state.PC,
				DurationNs: duration.Nanoseconds(),
				Output:     outputKVs,
			})
		}

		if pc == StopPlan {
			break
		}
	}
}
