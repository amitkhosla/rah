package engine

import (
	"rah/internal/rctx"
	"testing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newState builds an ExecutionState (no overflow store in new design).
func newState() *ExecutionState {
	return &ExecutionState{}
}

// newCtx returns an initialised context.
func newCtx() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	return ctx
}

// ── Case 1: arena path (value ≤ SlotValueThreshold) ──────────────────────────

func TestWriteReadSlot_ArenaPath(t *testing.T) {
	ctx := newCtx()
	s := newState()

	data := []byte("inline arena value")
	s.WriteSlot(ctx, 0, data)

	got := s.ReadSlot(ctx, 0)
	if string(got) != string(data) {
		t.Fatalf("got %q, want %q", got, data)
	}
	if ctx.ArenaOverflowed {
		t.Fatal("ArenaOverflowed should be false for small value")
	}
}

func TestWriteReadSlot_ArenaPath_MultipleSlots(t *testing.T) {
	ctx := newCtx()
	s := newState()

	s.WriteSlot(ctx, 0, []byte("first"))
	s.WriteSlot(ctx, 1, []byte("second"))
	s.WriteSlot(ctx, 2, []byte("third"))

	for i, want := range []string{"first", "second", "third"} {
		got := s.ReadSlot(ctx, i)
		if string(got) != want {
			t.Errorf("slot %d: got %q, want %q", i, got, want)
		}
	}
}

// ── Case 2: heap path (value > SlotValueThreshold) ───────────────────────────

func TestWriteReadSlot_HeapPath_LargeValue(t *testing.T) {
	ctx := newCtx()
	s := newState()

	// Create a value larger than SlotValueThreshold.
	big := make([]byte, rctx.SlotValueThreshold+1)
	for i := range big {
		big[i] = byte(i % 256)
	}

	s.WriteSlot(ctx, 0, big)

	// Value is on heap — ArenaOverflowed is set.
	if !ctx.ArenaOverflowed {
		t.Fatal("ArenaOverflowed should be true for large value")
	}

	got := s.ReadSlot(ctx, 0)
	if len(got) != len(big) {
		t.Fatalf("retrieved len %d, want %d", len(got), len(big))
	}
	for i := range big {
		if got[i] != big[i] {
			t.Fatalf("byte mismatch at index %d", i)
		}
	}
}

func TestWriteReadSlot_BoundaryValue_AtThreshold(t *testing.T) {
	ctx := newCtx()
	s := newState()

	// Exactly at threshold — should go into arena, not heap.
	data := make([]byte, rctx.SlotValueThreshold)
	for i := range data {
		data[i] = 0xAB
	}
	s.WriteSlot(ctx, 0, data)

	if ctx.ArenaOverflowed {
		t.Fatal("value at threshold boundary should fit in arena")
	}
	got := s.ReadSlot(ctx, 0)
	if len(got) != len(data) {
		t.Fatalf("retrieved len %d, want %d", len(got), len(data))
	}
}

// ── ReadSlot edge cases ───────────────────────────────────────────────────────

func TestReadSlot_EmptySlot_ReturnsNil(t *testing.T) {
	ctx := newCtx()
	s := newState()
	if got := s.ReadSlot(ctx, 0); got != nil {
		t.Fatalf("expected nil for unset slot, got %q", got)
	}
}

func TestReadSlot_NegativeIndex_ReturnsNil(t *testing.T) {
	ctx := newCtx()
	s := newState()
	if got := s.ReadSlot(ctx, -1); got != nil {
		t.Fatalf("expected nil for negative index, got %q", got)
	}
}

func TestReadSlot_OutOfRange_ReturnsNil(t *testing.T) {
	ctx := newCtx()
	s := newState()
	outOfRange := len(ctx.ByteSlots) + 99
	got := s.ReadSlot(ctx, outOfRange)
	if got != nil {
		t.Fatalf("expected nil for out-of-range index, got %q", got)
	}
}

func TestWriteSlot_OutOfRange_IsNoop(t *testing.T) {
	ctx := newCtx()
	s := newState()

	outOfRange := len(ctx.ByteSlots) + 1
	s.WriteSlot(ctx, outOfRange, []byte("lost"))

	got := s.ReadSlot(ctx, outOfRange)
	if got != nil {
		t.Fatalf("expected nil from out-of-range write, got %q", got)
	}
}

// ── slotValueThreshold override ───────────────────────────────────────────────

func TestWriteSlot_ThresholdOverride(t *testing.T) {
	ctx := newCtx()
	s := &ExecutionState{slotValueThreshold: 4} // tiny threshold

	// 5-byte value exceeds the 4-byte override → heap.
	data := []byte("hello")
	s.WriteSlot(ctx, 0, data)

	if !ctx.ArenaOverflowed {
		t.Fatal("ArenaOverflowed should be set with tiny threshold override")
	}
	got := s.ReadSlot(ctx, 0)
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}
