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

func TestAlloc_FitsInPrimaryArena(t *testing.T) {
	ctx := newTestContext()
	data := []byte("hello arena")
	s := ctx.Alloc(len(data))
	copy(s, data)

	if !bytes.Equal(s, data) {
		t.Fatalf("expected %q, got %q", data, s)
	}
	if ctx.ArenaOverflowed {
		t.Fatal("ArenaOverflowed should be false for primary-arena allocation")
	}
	if ctx.extraN != 0 {
		t.Fatalf("expected no extra arenas, got extraN=%d", ctx.extraN)
	}
}

func TestAlloc_FillsPrimaryThenBorrowsExtra(t *testing.T) {
	ctx := newTestContext()

	// Consume the entire primary arena.
	chunk := ArenaBlockSize / 2
	_ = ctx.Alloc(chunk)
	_ = ctx.Alloc(chunk) // primary is now full (used == ArenaBlockSize)

	// Next alloc must borrow an extra block.
	s := ctx.Alloc(4)
	if s == nil {
		t.Fatal("Alloc returned nil after primary filled")
	}
	if !ctx.ArenaOverflowed {
		t.Fatal("ArenaOverflowed should be true after borrowing extra block")
	}
	if ctx.extraN != 1 {
		t.Fatalf("expected extraN=1, got %d", ctx.extraN)
	}
}

func TestAlloc_ExhaustAllExtraArenas(t *testing.T) {
	ctx := newTestContext()

	// Fill primary + all MaxExtraArenas blocks.
	for i := 0; i <= MaxExtraArenas; i++ {
		_ = ctx.Alloc(ArenaBlockSize)
	}

	// One more alloc must fall back to heap (make) — not panic.
	s := ctx.Alloc(16)
	if len(s) != 16 {
		t.Fatalf("expected len 16 from heap fallback, got %d", len(s))
	}
}

func TestAlloc_ValueLargerThanOneBlock(t *testing.T) {
	ctx := newTestContext()
	// Request larger than ArenaBlockSize — must fall back to heap.
	bigSize := ArenaBlockSize + 1
	s := ctx.Alloc(bigSize)
	if len(s) != bigSize {
		t.Fatalf("expected len %d, got %d", bigSize, len(s))
	}
}

// ── ReleaseOverflow ───────────────────────────────────────────────────────────

func TestReleaseOverflow_ResetsExtraArenas(t *testing.T) {
	ctx := newTestContext()

	// Borrow two extra arenas.
	_ = ctx.Alloc(ArenaBlockSize) // fills primary
	_ = ctx.Alloc(ArenaBlockSize) // borrows extra[0]
	_ = ctx.Alloc(1)              // borrows extra[1]

	if ctx.extraN < 1 {
		t.Skip("alloc did not borrow extras — test assumptions wrong")
	}

	ctx.ReleaseOverflow()

	if ctx.extraN != 0 {
		t.Fatalf("expected extraN=0 after release, got %d", ctx.extraN)
	}
	if ctx.extra[0] != nil {
		t.Fatal("extra[0] should be nil after release")
	}
	if ctx.active != &ctx.primary {
		t.Fatal("active should point back to primary after release")
	}
}

func TestReleaseOverflow_ReturnsSlotExtToPool(t *testing.T) {
	ctx := newTestContext()
	ctx.GrowByteSlots(BaseByteSlots + 1)
	if ctx.slotExt == nil {
		t.Fatal("expected slotExt to be borrowed")
	}
	ctx.ReleaseOverflow()
	if ctx.slotExt != nil {
		t.Fatal("expected slotExt to be nil after release")
	}
	if len(ctx.ByteSlots) != BaseByteSlots {
		t.Fatalf("expected ByteSlots to revert to %d, got %d", BaseByteSlots, len(ctx.ByteSlots))
	}
}

// ── GrowByteSlots ─────────────────────────────────────────────────────────────

func TestGrowByteSlots_NoopWhenAlreadyLargeEnough(t *testing.T) {
	ctx := newTestContext()
	before := len(ctx.ByteSlots)
	ctx.GrowByteSlots(before)
	if len(ctx.ByteSlots) != before {
		t.Fatalf("expected length unchanged at %d, got %d", before, len(ctx.ByteSlots))
	}
	if ctx.slotExt != nil {
		t.Fatal("slotExt should remain nil when no growth needed")
	}
}

func TestGrowByteSlots_BorrowsExtBlock(t *testing.T) {
	ctx := newTestContext()
	ctx.GrowByteSlots(BaseByteSlots + 1)

	if !ctx.SlotOverflowed {
		t.Fatal("SlotOverflowed should be true after growing")
	}
	if ctx.slotExt == nil {
		t.Fatal("slotExt should be non-nil after growing")
	}
	if len(ctx.ByteSlots) < BaseByteSlots+1 {
		t.Fatalf("ByteSlots length %d too small after grow", len(ctx.ByteSlots))
	}
}

func TestGrowByteSlots_PreservesExistingSlotValues(t *testing.T) {
	ctx := newTestContext()
	s := ctx.Alloc(3)
	copy(s, "abc")
	ctx.ByteSlots[0] = s

	ctx.GrowByteSlots(BaseByteSlots + 4)

	if !bytes.Equal(ctx.ByteSlots[0], []byte("abc")) {
		t.Fatalf("slot 0 corrupted after grow: %q", ctx.ByteSlots[0])
	}
}

// ── Sentinel helpers ──────────────────────────────────────────────────────────

func TestIsSlotOverflowRef(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want bool
	}{
		{"nil", nil, false},
		{"empty", []byte{}, false},
		{"one byte", []byte{0x00}, false},
		{"sentinel prefix only", []byte{0x00, 0xFF}, true},
		{"sentinel with key", []byte{0x00, 0xFF, 'k', 'e', 'y'}, true},
		{"not sentinel", []byte{0x01, 0xFF, 'x'}, false},
		{"regular data", []byte("hello"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSlotOverflowRef(tt.b); got != tt.want {
				t.Errorf("IsSlotOverflowRef(%v) = %v, want %v", tt.b, got, tt.want)
			}
		})
	}
}

func TestDecodeSlotOverflowKey(t *testing.T) {
	want := "slot-ov/42/7"
	b := append([]byte{0x00, 0xFF}, []byte(want)...)
	if got := DecodeSlotOverflowKey(b); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ── EncodeSlotRef / IsSlotInStore / SlotStoreKey ──────────────────────────────

func TestEncodeSlotRef_RoundTrip(t *testing.T) {
	ctx := newTestContext()
	ctx.ReqID = 99
	key := BuildSlotDataKey(ctx.ReqID, 3)

	encoded := ctx.EncodeSlotRef(key)
	if !IsSlotOverflowRef(encoded) {
		t.Fatal("encoded ref not recognised as sentinel")
	}
	if got := DecodeSlotOverflowKey(encoded); got != key {
		t.Fatalf("decoded key %q != original %q", got, key)
	}
}

func TestIsSlotInStore(t *testing.T) {
	ctx := newTestContext()
	ctx.ReqID = 1

	// Slot 0 is initially empty — not a store ref.
	if ctx.IsSlotInStore(0) {
		t.Fatal("empty slot should not be in store")
	}
	// Negative index.
	if ctx.IsSlotInStore(-1) {
		t.Fatal("negative index should return false")
	}

	// Write a sentinel into slot 0.
	key := ctx.NextSlotDataKey()
	ctx.ByteSlots[0] = ctx.EncodeSlotRef(key)

	if !ctx.IsSlotInStore(0) {
		t.Fatal("slot with sentinel should be in store")
	}
	if ctx.SlotStoreKey(0) != key {
		t.Fatalf("SlotStoreKey returned %q, want %q", ctx.SlotStoreKey(0), key)
	}
}

// ── TrackSlotKey / TakeSlotOverflowKeys ──────────────────────────────────────

func TestTakeSlotOverflowKeys_ReturnsAndClearsKeys(t *testing.T) {
	ctx := newTestContext()
	ctx.ReqID = 5
	ctx.TrackSlotKey("slot-ov/5/0")
	ctx.TrackSlotKey("slot-idx/5/100")

	keys := ctx.TakeSlotOverflowKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	// Second call must return empty (list was cleared).
	keys2 := ctx.TakeSlotOverflowKeys()
	if len(keys2) != 0 {
		t.Fatalf("expected 0 keys on second call, got %d", len(keys2))
	}
}

// ── Key builders ──────────────────────────────────────────────────────────────

func TestBuildSlotDataKey(t *testing.T) {
	got := BuildSlotDataKey(42, 7)
	want := "slot-ov/42/7"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBuildSlotIndexKey(t *testing.T) {
	got := BuildSlotIndexKey(42, 100)
	want := "slot-idx/42/100"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNextSlotDataKey_IncrementsSeq(t *testing.T) {
	ctx := newTestContext()
	ctx.ReqID = 3
	k0 := ctx.NextSlotDataKey()
	k1 := ctx.NextSlotDataKey()
	if k0 == k1 {
		t.Fatal("successive keys must be unique")
	}
	want0 := BuildSlotDataKey(3, 0)
	want1 := BuildSlotDataKey(3, 1)
	if k0 != want0 {
		t.Fatalf("k0 got %q, want %q", k0, want0)
	}
	if k1 != want1 {
		t.Fatalf("k1 got %q, want %q", k1, want1)
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
	if ctx.active != &ctx.primary {
		t.Fatal("active should point to primary after InitSlots")
	}
}
