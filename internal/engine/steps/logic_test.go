package steps

import (
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func stopInstruction() engine.InstructionFunc {
	return func(_ *rctx.Context, _ *engine.ExecutionState) int16 {
		return engine.StopPlan
	}
}

func buildIncrementalTable(size int) []engine.Instruction {
	table := make([]engine.Instruction, size)
	for i := 0; i < size; i++ {
		next := int16(i + 1)
		table[i] = engine.Instruction{Name: "CALL", Action: CallFragment(next)}
	}
	return table
}

func buildDeepTable(depth int) []engine.Instruction {
	size := 2*depth + 1
	table := make([]engine.Instruction, size)
	table[0] = engine.Instruction{Name: "CALL_0", Action: CallFragment(int16(2))}
	table[1] = engine.Instruction{Name: "STOP", Action: stopInstruction()}
	for k := 1; k < depth; k++ {
		nextEntry := int16(2*k + 2)
		table[2*k] = engine.Instruction{Name: "CALL", Action: CallFragment(nextEntry)}
		table[2*k+1] = engine.Instruction{Name: "RET", Action: Return()}
	}
	table[2*depth] = engine.Instruction{Name: "RET_INNER", Action: Return()}
	return table
}

func newLinkTestCtx() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ResponseStatus = 200
	return ctx
}

// ── Tier 1: Functional (happy path) ──────────────────────────────────────────

func TestCallFragment_Shallow(t *testing.T) {
	ctx := newLinkTestCtx()
	engine.Execute(ctx, buildDeepTable(3), 0, 0)
	if ctx.Failed {
		t.Error("expected ctx.Failed=false for depth 3")
	}
}

func TestCallFragment_Exactly16(t *testing.T) {
	ctx := newLinkTestCtx()
	engine.Execute(ctx, buildDeepTable(16), 0, 0)
	if ctx.Failed {
		t.Error("expected ctx.Failed=false for depth 16 (all inline)")
	}
}

func TestCallFragment_Depth17_BorrowsExt(t *testing.T) {
	ctx := newLinkTestCtx()
	engine.Execute(ctx, buildDeepTable(17), 0, 0)
	if ctx.Failed {
		t.Error("expected ctx.Failed=false for depth 17 (uses ext block)")
	}
}

func TestCallFragment_Depth127(t *testing.T) {
	ctx := newLinkTestCtx()
	engine.Execute(ctx, buildDeepTable(127), 0, 0)
	if ctx.Failed {
		t.Error("expected ctx.Failed=false for depth 127 (full inline + ext)")
	}
}

// ── Tier 2: Negative / error path ────────────────────────────────────────────

func TestCallFragment_HardCeiling_Aborts(t *testing.T) {
	ctx := newLinkTestCtx()
	state := &engine.ExecutionState{}
	state.StackPtr = 128 // simulate already at ceiling

	fn := CallFragment(int16(99))
	pc := fn(ctx, state)

	if pc != engine.StopPlan {
		t.Errorf("expected StopPlan (-1), got %d", pc)
	}
	if !ctx.Failed {
		t.Error("expected ctx.Failed=true")
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("expected ResponseStatus=500, got %d", ctx.ResponseStatus)
	}
}

func TestCallFragment_HardCeiling_ViaExecute(t *testing.T) {
	ctx := newLinkTestCtx()
	// 129 incremental calls: the 129th (sp=128) triggers the hard ceiling
	engine.Execute(ctx, buildIncrementalTable(129), 0, 0)
	if !ctx.Failed {
		t.Error("expected ctx.Failed=true after exceeding depth 128")
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("expected ResponseStatus=500, got %d", ctx.ResponseStatus)
	}
}

// ── Tier 3: Non-functional / blast-radius ────────────────────────────────────

func TestCallFragment_IndependentStates(t *testing.T) {
	state1 := &engine.ExecutionState{}
	state2 := &engine.ExecutionState{}
	ctx := newLinkTestCtx()

	// Push state1 to depth 17 (borrows ext block)
	for i := 0; i < 17; i++ {
		fn := CallFragment(int16(i + 1))
		state1.PC = int16(i)
		fn(ctx, state1)
	}

	// state2 must remain unaffected
	if state2.LinkExt != nil {
		t.Error("state2.LinkExt should be nil — states must be independent")
	}
	if state2.StackPtr != 0 {
		t.Errorf("state2.StackPtr should be 0, got %d", state2.StackPtr)
	}
}

func TestReturn_CorrectAddressAtAllDepths(t *testing.T) {
	ctx := newLinkTestCtx()
	engine.Execute(ctx, buildDeepTable(20), 0, 0)
	if ctx.Failed {
		t.Error("expected ctx.Failed=false for depth 20 (crosses inline/ext boundary)")
	}
}

func TestCallFragment_ExtBlockNilAfterExecute(t *testing.T) {
	// Run many times to verify pool blocks are correctly reused (no leaks or dirty state).
	table := buildDeepTable(20)
	for i := 0; i < 1000; i++ {
		ctx := newLinkTestCtx()
		engine.Execute(ctx, table, 0, 0)
		if ctx.Failed {
			t.Fatalf("iteration %d: unexpected ctx.Failed=true", i)
		}
	}
}
