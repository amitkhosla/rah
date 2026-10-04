package engine

import (
	"sync"

	"github.com/amitkhosla/rah/internal/rctx"
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
// Allocated on goroutine stack. LinkExt is the only pointer field;
// GC scans it once per cycle but the block contains no Go pointers.
type ExecutionState struct {
	// LinkStack stores return addresses for fragment calls (inline, fits in first cache line).
	LinkStack [16]int16
	// StackPtr tracks current call depth. int16 to safely hold values up to 128 (the hard ceiling).
	StackPtr  int16
	PC        int16
	IsStopped bool

	// ErrorHandlerPC holds the instruction address to jump to on step failure (e.g., rollback on tx body failure).
	// 0 means no error handler is set. Set by BeginTx to the rollback instruction address.
	ErrorHandlerPC int16

	// slotValueThreshold overrides rctx.SlotValueThreshold when non-zero.
	slotValueThreshold int

	// LinkExt is a pool-borrowed overflow block for call depths 16–127.
	// 8-byte pointer in ExecutionState regardless of block size — no cost for depth-0/shallow flows.
	// Nil on the common path (depth ≤ 15). Pre-borrowed by Execute when MaxCallDepth > 15;
	// lazily borrowed by CallFragment otherwise. Returned to pool after the flow loop.
	LinkExt *[112]int16

	TraceAttrs     [24][2]string
	traceAttrCount int8
}

// linkStackExtPool holds [112]int16 overflow blocks for deep fragment nesting (depths 16–127).
// Pool object is 224 bytes (≤ 4 cache lines). Only touched for flows that cross depth 15.
var linkStackExtPool = sync.Pool{New: func() any { return new([112]int16) }}

// BorrowLinkExt returns a zeroed [112]int16 block for link-stack depths 16–127.
func BorrowLinkExt() *[112]int16 {
	b := linkStackExtPool.Get().(*[112]int16)
	*b = [112]int16{} // zero dirty pool block before first use
	return b
}

// ReturnLinkExt returns an overflow block to the pool.
func ReturnLinkExt(b *[112]int16) { linkStackExtPool.Put(b) }

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
// maxCallDepth is the compile-time maximum fragment call nesting depth for this flow.
// When > 15, Execute pre-borrows LinkExt before the loop (eliminates the nil check
// inside CallFragment for flows known to cross depth 15). Pass 0 when unknown.
// Per-instruction timing is written into ctx.InstrPC / ctx.InstrDurNs /
// ctx.InstrCount so callers can hand them off to instrSlabRing.Write without
// paying the cost of returning a large struct by value.
func Execute(ctx *rctx.Context, table []Instruction, startID int16, maxCallDepth int8) {
	pc := startID
	tableLen := int16(len(table))

	// ExecutionState lives on this goroutine's stack (no GC pressure).
	var state ExecutionState
	if maxCallDepth > 15 {
		state.LinkExt = BorrowLinkExt()
	}

	for pc >= 0 && pc < tableLen {
		state.PC = pc
		current := table[pc]
		shouldMeasure := ctx.Obs != nil && ctx.Obs.InstructionTimingEnabled()
		var started time.Time
		if shouldMeasure {
			started = time.Now()
		}
		pc = current.Action(ctx, &state)

		// If the step failed and an error handler is set, redirect to it instead of stopping.
		if pc == StopPlan && ctx.Failed && state.ErrorHandlerPC != 0 {
			pc = state.ErrorHandlerPC
			state.ErrorHandlerPC = 0
		}

		// Accumulate PC and timing into ctx (not state) so Execute can return
		// void — avoids copying the ~1200-byte ExecutionState on every request.
		// AppendInstr handles both inline (first 64) and overflow chain cases.
		if shouldMeasure {
			ns := time.Since(started).Nanoseconds()
			if ns <= 0 {
				ns = 1
			}
			ctx.AppendInstr(state.PC, int32(ns))
			state.traceAttrCount = 0
		} else {
			ctx.AppendInstr(state.PC, 0)
		}

		if pc == StopPlan {
			break
		}
	}

	// Return borrowed link-stack overflow block. Inline (not defer) so state stays on goroutine stack.
	if state.LinkExt != nil {
		ReturnLinkExt(state.LinkExt)
		state.LinkExt = nil
	}
}
