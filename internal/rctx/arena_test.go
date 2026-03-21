package rctx

import (
	"bytes"
	"testing"
)

// newTestContext returns a zero-value Context with slots and arena initialised,
// matching what FlowManager.Pool.New produces.
func newTestContext() *Context {
	ctx := &Context{}
	ctx.InitSlots()
	return ctx
}

// ── Alloc ─────────────────────────────────────────────────────────────────────

func TestAlloc_FitsInInlineArena(t *testing.T) {
	ctx := newTestContext()
	data := []byte("hello inline arena")
	s := ctx.Alloc(len(data))
	copy(s, data)

	if !bytes.Equal(s, data) {
		t.Fatalf("expected %q, got %q", data, s)
	}
	if ctx.ArenaOverflowed {
		t.Fatal("ArenaOverflowed should be false for inline-arena allocation")
	}
	if ctx.arenaExt != nil {
		t.Fatal("arenaExt should be nil — no pool block borrowed")
	}
}

func TestAlloc_FillsInlineThenBorrowsExtBlock(t *testing.T) {
	ctx := newTestContext()

	// Consume the entire inline arena.
	chunk := ArenaInlineSize / 2
	_ = ctx.Alloc(chunk)
	_ = ctx.Alloc(chunk) // inline is now full (used == ArenaInlineSize)

	// Next alloc must borrow the ext block.
	s := ctx.Alloc(4)
	if s == nil {
		t.Fatal("Alloc returned nil after inline arena filled")
	}
	if !ctx.ArenaOverflowed {
		t.Fatal("ArenaOverflowed should be true after borrowing ext block")
	}
	if ctx.arenaExt == nil {
		t.Fatal("arenaExt should be non-nil after borrowing ext block")
	}
}

func TestAlloc_ValueLargerThanExtBlock(t *testing.T) {
	ctx := newTestContext()
	// Fill inline arena first.
	_ = ctx.Alloc(ArenaInlineSize)
	// Request larger than ArenaBlockSize — must fall back to heap.
	bigSize := ArenaBlockSize + 1
	s := ctx.Alloc(bigSize)
	if len(s) != bigSize {
		t.Fatalf("expected len %d from heap fallback, got %d", bigSize, len(s))
	}
}

func TestAlloc_LargeValueInEmptyContext_HeapFallback(t *testing.T) {
	ctx := newTestContext()
	// A value that doesn't fit in inline AND doesn't fit in ext block.
	// Goes: inline → ext (borrowed) → ext full → heap.
	huge := ArenaBlockSize + 100
	s := ctx.Alloc(huge)
	if len(s) != huge {
		t.Fatalf("expected len %d, got %d", huge, len(s))
	}
}

// ── ReleaseOverflow ───────────────────────────────────────────────────────────

func TestReleaseOverflow_ReturnsExtBlockToPool(t *testing.T) {
	ctx := newTestContext()

	// Borrow the ext block.
	_ = ctx.Alloc(ArenaInlineSize) // fills inline
	_ = ctx.Alloc(4)               // borrows ext

	if ctx.arenaExt == nil {
		t.Skip("alloc did not borrow ext block — test assumptions wrong")
	}

	ctx.ReleaseOverflow()

	if ctx.arenaExt != nil {
		t.Fatal("arenaExt should be nil after release")
	}
}

func TestReleaseOverflow_NoopWhenNoExtBlock(t *testing.T) {
	ctx := newTestContext()
	// Should not panic when arenaExt is nil.
	ctx.ReleaseOverflow()
	if ctx.arenaExt != nil {
		t.Fatal("arenaExt should remain nil")
	}
}

// ── InitSlots / Reset ─────────────────────────────────────────────────────────

func TestInitSlots_SetsSliceHeaders(t *testing.T) {
	ctx := newTestContext()
	if len(ctx.ByteSlots) != BaseByteSlots {
		t.Fatalf("expected %d byte slots, got %d", BaseByteSlots, len(ctx.ByteSlots))
	}
	if len(ctx.IntSlots) != BaseIntSlots {
		t.Fatalf("expected %d int slots, got %d", BaseIntSlots, len(ctx.IntSlots))
	}
	if len(ctx.BoolSlots) != BaseBoolSlots {
		t.Fatalf("expected %d bool slots, got %d", BaseBoolSlots, len(ctx.BoolSlots))
	}
	if ctx.arenaUsed != 0 {
		t.Fatalf("arenaUsed should be 0 after InitSlots, got %d", ctx.arenaUsed)
	}
	if ctx.arenaExt != nil {
		t.Fatal("arenaExt should be nil after InitSlots")
	}
}

func TestReset_ClearsArena(t *testing.T) {
	ctx := newTestContext()
	_ = ctx.Alloc(100)
	if ctx.arenaUsed != 100 {
		t.Fatalf("expected arenaUsed=100, got %d", ctx.arenaUsed)
	}
	ctx.Reset(nil)
	if ctx.arenaUsed != 0 {
		t.Fatalf("expected arenaUsed=0 after Reset, got %d", ctx.arenaUsed)
	}
}

func TestReset_ClearsInternalTxID(t *testing.T) {
	ctx := newTestContext()
	ctx.InternalTxID = [2]uint64{0xDEADBEEF, 0xCAFEBABE}
	ctx.Reset(nil)
	if ctx.InternalTxID != ([2]uint64{}) {
		t.Fatalf("InternalTxID not cleared after Reset: %v", ctx.InternalTxID)
	}
}
