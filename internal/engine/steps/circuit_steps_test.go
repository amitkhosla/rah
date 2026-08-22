package steps

import (
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// Helper to run an instruction and return the new PC.
func runInstr(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

// ─── Tier 1: Functional Tests ──────────────────────────────────────────────

// TestTripCircuitInstruction verifies that TripCircuitInstruction opens a circuit.
func TestTripCircuitInstruction(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	name := "test-trip-01"
	instr := TripCircuitInstruction(name, 100)

	// Circuit should not be open before trip.
	if engine.IsNamedCircuitOpen(name) {
		t.Errorf("circuit should not be open before trip")
	}

	// Run the instruction.
	pc := runInstr(instr, ctx)
	if pc != 1 {
		t.Errorf("PC: got %d, want 1", pc)
	}

	// Circuit should be open after trip.
	if !engine.IsNamedCircuitOpen(name) {
		t.Errorf("circuit should be open after trip")
	}
}

// TestCheckCircuitInstruction_Open verifies CheckCircuitInstruction writes "open"
// when the circuit is open.
func TestCheckCircuitInstruction_Open(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	name := "test-check-open-01"
	resultSlot := 0

	// Trip the circuit first.
	engine.TripNamedCircuit(name, 100)

	// Run CheckCircuitInstruction.
	instr := CheckCircuitInstruction(name, resultSlot)
	pc := runInstr(instr, ctx)
	if pc != 1 {
		t.Errorf("PC: got %d, want 1", pc)
	}

	// Verify slot contains "open".
	state := string(ctx.ByteSlots[resultSlot])
	if state != "open" {
		t.Errorf("slot state: got %q, want %q", state, "open")
	}
}

// TestCheckCircuitInstruction_Closed verifies CheckCircuitInstruction writes "closed"
// when the circuit is closed (or unknown).
func TestCheckCircuitInstruction_Closed(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	name := "test-check-closed-01"
	resultSlot := 0

	// Run CheckCircuitInstruction on unknown circuit.
	instr := CheckCircuitInstruction(name, resultSlot)
	pc := runInstr(instr, ctx)
	if pc != 1 {
		t.Errorf("PC: got %d, want 1", pc)
	}

	// Verify slot contains "closed".
	state := string(ctx.ByteSlots[resultSlot])
	if state != "closed" {
		t.Errorf("slot state: got %q, want %q", state, "closed")
	}
}

// TestResetCircuitInstruction verifies that ResetCircuitInstruction closes a circuit.
func TestResetCircuitInstruction(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	name := "test-reset-01"

	// Trip the circuit first.
	engine.TripNamedCircuit(name, 100)
	if !engine.IsNamedCircuitOpen(name) {
		t.Errorf("circuit should be open after trip")
	}

	// Run ResetCircuitInstruction.
	instr := ResetCircuitInstruction(name)
	pc := runInstr(instr, ctx)
	if pc != 1 {
		t.Errorf("PC: got %d, want 1", pc)
	}

	// Circuit should be closed after reset.
	if engine.IsNamedCircuitOpen(name) {
		t.Errorf("circuit should be closed after reset")
	}
}

// ─── Tier 2: Negative Tests ───────────────────────────────────────────────

// TestCheckCircuitInstruction_UnknownName verifies that CheckCircuitInstruction
// handles unknown circuit names gracefully (treats as "closed", no panic).
func TestCheckCircuitInstruction_UnknownName(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	name := "nonexistent-circuit-99999"
	resultSlot := 0

	// This should not panic or error.
	instr := CheckCircuitInstruction(name, resultSlot)
	pc := runInstr(instr, ctx)
	if pc != 1 {
		t.Errorf("PC: got %d, want 1", pc)
	}

	// Unknown circuit should be treated as "closed".
	state := string(ctx.ByteSlots[resultSlot])
	if state != "closed" {
		t.Errorf("unknown circuit state: got %q, want %q", state, "closed")
	}
}

// TestResetCircuitInstruction_UnknownName verifies that ResetCircuitInstruction
// handles unknown circuit names gracefully (no panic, no-op).
func TestResetCircuitInstruction_UnknownName(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	name := "nonexistent-circuit-88888"

	// This should not panic or error.
	instr := ResetCircuitInstruction(name)
	pc := runInstr(instr, ctx)
	if pc != 1 {
		t.Errorf("PC: got %d, want 1", pc)
	}
}

// ─── Tier 3: Non-Functional Tests ─────────────────────────────────────────

// TestCircuitSteps_BlastRadius verifies that operations on one circuit do not
// affect other circuits (isolation).
func TestCircuitSteps_BlastRadius(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	circuitA := "test-blast-radius-circuit-a"
	circuitB := "test-blast-radius-circuit-b"

	// Trip circuit A.
	engine.TripNamedCircuit(circuitA, 100)

	// Check that circuit B is still closed.
	if engine.IsNamedCircuitOpen(circuitB) {
		t.Errorf("circuit B should not be open (isolation failed)")
	}

	// Verify via CheckCircuitInstruction for circuit B.
	instr := CheckCircuitInstruction(circuitB, 0)
	runInstr(instr, ctx)
	state := string(ctx.ByteSlots[0])
	if state != "closed" {
		t.Errorf("circuit B state: got %q, want %q", state, "closed")
	}
}

// TestCircuitSteps_MultipleSlots verifies that CheckCircuitInstruction can write
// to different result slots independently.
func TestCircuitSteps_MultipleSlots(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	circuitA := "test-multi-slots-a"
	circuitB := "test-multi-slots-b"
	slotA := 0
	slotB := 1

	// Trip circuit A, leave B closed.
	engine.TripNamedCircuit(circuitA, 100)

	// Check both circuits into separate slots.
	instrA := CheckCircuitInstruction(circuitA, slotA)
	runInstr(instrA, ctx)

	instrB := CheckCircuitInstruction(circuitB, slotB)
	runInstr(instrB, ctx)

	// Verify each slot has the correct state.
	stateA := string(ctx.ByteSlots[slotA])
	stateB := string(ctx.ByteSlots[slotB])

	if stateA != "open" {
		t.Errorf("slot A state: got %q, want %q", stateA, "open")
	}
	if stateB != "closed" {
		t.Errorf("slot B state: got %q, want %q", stateB, "closed")
	}
}

// TestCircuitSteps_TripResetTrip verifies that a circuit can be reset and re-tripped.
func TestCircuitSteps_TripResetTrip(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	name := "test-trip-reset-trip"

	// First trip.
	engine.TripNamedCircuit(name, 100)
	if !engine.IsNamedCircuitOpen(name) {
		t.Errorf("circuit should be open after first trip")
	}

	// Reset.
	instr := ResetCircuitInstruction(name)
	runInstr(instr, ctx)
	if engine.IsNamedCircuitOpen(name) {
		t.Errorf("circuit should be closed after reset")
	}

	// Re-trip.
	engine.TripNamedCircuit(name, 100)
	if !engine.IsNamedCircuitOpen(name) {
		t.Errorf("circuit should be open after re-trip")
	}
}
