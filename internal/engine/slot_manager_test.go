package engine

import (
	"fmt"
	"rah/internal/rctx"
	"sync"
	"testing"
)

// ── mockStore ─────────────────────────────────────────────────────────────────

// mockStore is an in-memory SlotOverflowStore for testing.
type mockStore struct {
	mu   sync.Mutex
	data map[string][]byte
	// putErr, when non-nil, is returned by SlotPut.
	putErr error
}

func newMockStore() *mockStore {
	return &mockStore{data: make(map[string][]byte)}
}

func (m *mockStore) SlotPut(key string, value []byte) error {
	if m.putErr != nil {
		return m.putErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[key] = cp
	return nil
}

func (m *mockStore) SlotGet(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return v, nil
}

func (m *mockStore) SlotDelete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// newState builds an ExecutionState with an optional overflow store.
func newState(store rctx.SlotOverflowStore) *ExecutionState {
	return &ExecutionState{SlotOverflow: store}
}

// newCtx returns an initialised context with the given ReqID.
func newCtx(reqID uint64) *rctx.Context {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ReqID = reqID
	return ctx
}

// ── Case 1: arena (value fits in a single arena block) ────────────────────────

func TestWriteReadSlot_ArenaPath(t *testing.T) {
	ctx := newCtx(1)
	s := newState(nil) // no overflow store needed

	data := []byte("inline arena value")
	s.WriteSlot(ctx, 0, data)

	// Slot must NOT have been sent to a store sentinel.
	if rctx.IsSlotOverflowRef(ctx.ByteSlots[0]) {
		t.Fatal("expected arena storage, got store sentinel")
	}

	got := s.ReadSlot(ctx, 0)
	if string(got) != string(data) {
		t.Fatalf("got %q, want %q", got, data)
	}
}

func TestWriteReadSlot_ArenaPath_MultipleSlots(t *testing.T) {
	ctx := newCtx(1)
	s := newState(nil)

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

// ── Case 2: data overflow — value too large for arena, spilled to DataStore ──

func TestWriteReadSlot_DataOverflow_SpillsToStore(t *testing.T) {
	store := newMockStore()
	ctx := newCtx(7)
	s := newState(store)

	// Create a value larger than one arena block.
	big := make([]byte, rctx.ArenaBlockSize+1)
	for i := range big {
		big[i] = byte(i % 256)
	}

	s.WriteSlot(ctx, 0, big)

	// ByteSlots[0] should hold a store-reference sentinel.
	if !rctx.IsSlotOverflowRef(ctx.ByteSlots[0]) {
		t.Fatal("expected store sentinel after data overflow")
	}

	// ReadSlot must transparently retrieve from the store.
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

func TestWriteSlot_DataOverflow_NoStore_FallsBackToHeap(t *testing.T) {
	ctx := newCtx(1)
	s := newState(nil) // no store configured

	big := make([]byte, rctx.ArenaBlockSize+1)
	s.WriteSlot(ctx, 0, big)

	// Without a store the value must still be accessible (heap fallback).
	got := s.ReadSlot(ctx, 0)
	if len(got) != len(big) {
		t.Fatalf("expected heap fallback len %d, got %d", len(big), len(got))
	}
}

func TestWriteSlot_DataOverflow_StoreWriteFails_FallsBackToHeap(t *testing.T) {
	store := newMockStore()
	store.putErr = fmt.Errorf("disk full")
	ctx := newCtx(3)
	s := newState(store)

	big := make([]byte, rctx.ArenaBlockSize+1)
	for i := range big {
		big[i] = 0xAB
	}
	s.WriteSlot(ctx, 0, big)

	// Store write failed → value should be on heap, readable directly.
	got := s.ReadSlot(ctx, 0)
	if len(got) != len(big) {
		t.Fatalf("expected fallback len %d, got %d", len(big), len(got))
	}
}

// ── Case 3: index overflow — slot index beyond ByteSlots capacity ─────────────

func TestWriteReadSlot_IndexOverflow_SpillsToStore(t *testing.T) {
	store := newMockStore()
	ctx := newCtx(42)
	s := newState(store)

	overflowIdx := len(ctx.ByteSlots) + 5 // definitely out of range
	data := []byte("index-overflow value")

	s.WriteSlot(ctx, overflowIdx, data)

	// The store must contain the entry.
	expectedKey := ctx.SlotIndexStoreKey(overflowIdx)
	v, err := store.SlotGet(expectedKey)
	if err != nil {
		t.Fatalf("expected key %q in store: %v", expectedKey, err)
	}
	if string(v) != string(data) {
		t.Fatalf("store value %q, want %q", v, data)
	}

	// ReadSlot must retrieve it transparently.
	got := s.ReadSlot(ctx, overflowIdx)
	if string(got) != string(data) {
		t.Fatalf("ReadSlot got %q, want %q", got, data)
	}
}

func TestWriteSlot_IndexOverflow_NoStore_IsNoop(t *testing.T) {
	ctx := newCtx(1)
	s := newState(nil)

	overflowIdx := len(ctx.ByteSlots) + 1
	s.WriteSlot(ctx, overflowIdx, []byte("lost"))

	got := s.ReadSlot(ctx, overflowIdx)
	if got != nil {
		t.Fatalf("expected nil from index overflow with no store, got %q", got)
	}
}

// ── TrackSlotKey / TakeSlotOverflowKeys integration ──────────────────────────

func TestWriteSlot_DataOverflow_TracksKey(t *testing.T) {
	store := newMockStore()
	ctx := newCtx(10)
	s := newState(store)

	big := make([]byte, rctx.ArenaBlockSize+1)
	s.WriteSlot(ctx, 0, big)

	keys := ctx.TakeSlotOverflowKeys()
	if len(keys) == 0 {
		t.Fatal("expected at least one tracked key after data overflow")
	}
}

func TestWriteSlot_IndexOverflow_TracksKey(t *testing.T) {
	store := newMockStore()
	ctx := newCtx(11)
	s := newState(store)

	overflowIdx := len(ctx.ByteSlots) + 2
	s.WriteSlot(ctx, overflowIdx, []byte("track me"))

	keys := ctx.TakeSlotOverflowKeys()
	if len(keys) == 0 {
		t.Fatal("expected at least one tracked key after index overflow")
	}
	expectedKey := ctx.SlotIndexStoreKey(overflowIdx)
	found := false
	for _, k := range keys {
		if k == expectedKey {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected key %q in tracked keys %v", expectedKey, keys)
	}
}

// ── ReadSlot edge cases ───────────────────────────────────────────────────────

func TestReadSlot_EmptySlot_ReturnsNil(t *testing.T) {
	ctx := newCtx(1)
	s := newState(nil)
	if got := s.ReadSlot(ctx, 0); got != nil {
		t.Fatalf("expected nil for unset slot, got %q", got)
	}
}

func TestReadSlot_IndexOverflow_NoStore_ReturnsNil(t *testing.T) {
	ctx := newCtx(1)
	s := newState(nil)
	got := s.ReadSlot(ctx, len(ctx.ByteSlots)+99)
	if got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestReadSlot_SentinelWithNoStore_ReturnsNil(t *testing.T) {
	ctx := newCtx(5)
	s := newState(nil)
	// Manually place a sentinel in slot 0 (unusual but must not panic).
	ctx.ByteSlots[0] = ctx.EncodeSlotRef("orphan-key")
	got := s.ReadSlot(ctx, 0)
	if got != nil {
		t.Fatalf("expected nil when store is nil but sentinel present, got %q", got)
	}
}
