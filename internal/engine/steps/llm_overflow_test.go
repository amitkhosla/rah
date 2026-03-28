package steps

import (
	"context"
	"encoding/json"
	"testing"

	"rah/internal/datastore"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// ─── helpers (mirrors llm_history_test.go; defined locally to avoid import) ──

// overflowCtx creates a Context with numByteSlots byte slots and numIntSlots int slots.
func overflowCtx(numByteSlots, numIntSlots int) (*rctx.Context, *engine.ExecutionState) {
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, numByteSlots),
		IntSlots:  make([]int64, numIntSlots),
		TenantKey: "test-tenant",
	}
	state := &engine.ExecutionState{PC: 0}
	return ctx, state
}

func marshalOverflowHistory(t *testing.T, msgs []CanonicalMessage) []byte {
	t.Helper()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	return b
}

func decodeOverflowSlot(t *testing.T, ctx *rctx.Context, slot int) []CanonicalMessage {
	t.Helper()
	raw := ctx.ByteSlots[slot]
	if len(raw) == 0 {
		return nil
	}
	var msgs []CanonicalMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("decode slot %d: %v", slot, err)
	}
	return msgs
}

// ─── in-memory store (duplicate of llm_history_test.go's memStore) ───────────

type ovMemStore struct {
	data   map[string][]byte
	domain string
}

func newOvMemStore(domain string) *ovMemStore {
	return &ovMemStore{data: make(map[string][]byte), domain: domain}
}

func (s *ovMemStore) Put(_ context.Context, tenant datastore.Tenant, key string, value []byte) error {
	skey, _ := datastore.BuildScopedKey(tenant, s.domain, key)
	buf := make([]byte, len(value))
	copy(buf, value)
	s.data[skey] = buf
	return nil
}
func (s *ovMemStore) Get(_ context.Context, tenant datastore.Tenant, key string) ([]byte, bool, error) {
	skey, _ := datastore.BuildScopedKey(tenant, s.domain, key)
	v, ok := s.data[skey]
	return v, ok, nil
}
func (s *ovMemStore) Delete(_ context.Context, tenant datastore.Tenant, key string) error {
	skey, _ := datastore.BuildScopedKey(tenant, s.domain, key)
	delete(s.data, skey)
	return nil
}
func (s *ovMemStore) ListKeys(_ context.Context, _ datastore.Tenant, _ string) ([]string, error) {
	return nil, nil
}
func (s *ovMemStore) Kind() string                     { return "mem" }
func (s *ovMemStore) Name() string                     { return s.domain }
func (s *ovMemStore) PoolStats() datastore.PoolStats   { return datastore.PoolStats{} }
func (s *ovMemStore) Close() error                     { return nil }

// helper to read stored overflow from the mem-store directly.
func readStoreOverflow(t *testing.T, store *ovMemStore, tenant, key string) []CanonicalMessage {
	t.Helper()
	raw, found, err := store.Get(context.Background(), datastore.Tenant(tenant), key)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if !found {
		return nil
	}
	var msgs []CanonicalMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("unmarshal stored overflow: %v", err)
	}
	return msgs
}

// buildTurns creates n user+assistant pairs.
func buildTurns(n int) []CanonicalMessage {
	msgs := make([]CanonicalMessage, 0, n*2)
	for i := 0; i < n; i++ {
		msgs = append(msgs, CanonicalMessage{Role: RoleUser, Content: "user turn"})
		msgs = append(msgs, CanonicalMessage{Role: RoleAssistant, Content: "assistant turn"})
	}
	return msgs
}

// ─── OverflowHistory tests ────────────────────────────────────────────────────

// TestOverflowHistory_FitsWithinBudget: all turns fit → no overflow stored, history unchanged.
func TestOverflowHistory_FitsWithinBudget(t *testing.T) {
	store := newOvMemStore("convs")
	// 2 turns, each msg ~9 chars → ~6 tokens + 4 = 10 tokens per msg → 40 tokens for 4 msgs.
	// Budget of 200 tokens → everything fits.
	msgs := buildTurns(2)
	ctx, state := overflowCtx(4, 2)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("sess-fit")

	cfg := OverflowHistoryConfig{
		HistorySlot:  0,
		KeySlot:      1,
		OverflowSlot: -1,
		MaxTokens:    200,
		Domain:       "convs",
		Store:        store,
	}
	next := OverflowHistory(cfg).Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}

	// History should be unchanged.
	got := decodeOverflowSlot(t, ctx, 0)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages unchanged, got %d", len(got))
	}

	// Nothing stored in overflow.
	stored := readStoreOverflow(t, store, "test-tenant", "sess-fit:overflow")
	if len(stored) != 0 {
		t.Fatalf("expected no overflow stored, got %d messages", len(stored))
	}
}

// TestOverflowHistory_ExceedsBudget: oldest turns moved to store, history trimmed.
func TestOverflowHistory_ExceedsBudget(t *testing.T) {
	store := newOvMemStore("convs")
	// 5 turns = 10 messages; each ~9 chars → ~6 tok + 4 = 10 tok each → 100 tokens total.
	// Keep only 2 turns (40 tokens); overflow = 3 turns (60 tokens).
	msgs := buildTurns(5)
	ctx, state := overflowCtx(4, 2)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("sess-trim")

	cfg := OverflowHistoryConfig{
		HistorySlot:  0,
		KeySlot:      1,
		OverflowSlot: -1,
		MaxTurns:     2, // keep last 2 pairs
		Domain:       "convs",
		Store:        store,
	}
	OverflowHistory(cfg).Action(ctx, state)

	// History slot should have only 2 pairs = 4 messages.
	got := decodeOverflowSlot(t, ctx, 0)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages in history, got %d", len(got))
	}
	// Overflow in store: 3 pairs = 6 messages.
	stored := readStoreOverflow(t, store, "test-tenant", "sess-trim:overflow")
	if len(stored) != 6 {
		t.Fatalf("expected 6 overflow messages stored, got %d", len(stored))
	}
}

// TestOverflowHistory_NilStore_JustTrims: Store nil → trim in place, no panic.
func TestOverflowHistory_NilStore_JustTrims(t *testing.T) {
	msgs := buildTurns(4)
	ctx, state := overflowCtx(4, 2)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("sess-nilstore")

	cfg := OverflowHistoryConfig{
		HistorySlot:  0,
		KeySlot:      1,
		OverflowSlot: -1,
		MaxTurns:     1,
		Domain:       "convs",
		Store:        nil,
	}
	OverflowHistory(cfg).Action(ctx, state)

	got := decodeOverflowSlot(t, ctx, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (1 pair) after trim, got %d", len(got))
	}
}

// TestOverflowHistory_ExistingOverflow: new overflow appended after existing in store.
func TestOverflowHistory_ExistingOverflow(t *testing.T) {
	store := newOvMemStore("convs")
	// Pre-seed overflow with 1 turn.
	existing := buildTurns(1)
	_ = store.Put(context.Background(), datastore.Tenant("test-tenant"), "sess-accum:overflow",
		marshalOverflowHistory(t, existing))

	// Now 3 more turns come in; keep 1, overflow 2.
	msgs := buildTurns(3)
	ctx, state := overflowCtx(4, 2)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("sess-accum")

	cfg := OverflowHistoryConfig{
		HistorySlot:  0,
		KeySlot:      1,
		OverflowSlot: -1,
		MaxTurns:     1,
		Domain:       "convs",
		Store:        store,
	}
	OverflowHistory(cfg).Action(ctx, state)

	// Overflow in store: 1 (existing) + 2 (new) = 3 pairs = 6 messages.
	stored := readStoreOverflow(t, store, "test-tenant", "sess-accum:overflow")
	if len(stored) != 6 {
		t.Fatalf("expected 6 overflow messages (1 existing + 2 new pairs), got %d", len(stored))
	}
	// Oldest should still be first.
	if stored[0].Content != existing[0].Content {
		t.Fatalf("expected existing overflow first, got %+v", stored[0])
	}
}

// TestOverflowHistory_OverflowSlotDriven: reads token overflow count from IntSlot.
// Uses a very large overflow_tokens value (9999) to force all messages into overflow.
func TestOverflowHistory_OverflowSlotDriven(t *testing.T) {
	store := newOvMemStore("convs")
	msgs := buildTurns(3) // 6 messages
	ctx, state := overflowCtx(4, 4)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("sess-slot")
	ctx.IntSlots[2] = 9999 // very large → remove all messages

	cfg := OverflowHistoryConfig{
		HistorySlot:  0,
		KeySlot:      1,
		OverflowSlot: 2,
		Domain:       "convs",
		Store:        store,
	}
	OverflowHistory(cfg).Action(ctx, state)

	// All messages moved to overflow → history slot is empty.
	got := decodeOverflowSlot(t, ctx, 0)
	if len(got) != 0 {
		t.Fatalf("expected 0 remaining messages (all overflowed), got %d", len(got))
	}
	// All 6 messages stored in overflow.
	stored := readStoreOverflow(t, store, "test-tenant", "sess-slot:overflow")
	if len(stored) != 6 {
		t.Fatalf("expected 6 overflow messages stored, got %d", len(stored))
	}
}

// TestOverflowHistory_OverflowSlotZero_Noop: overflow slot value 0 → nothing to move.
func TestOverflowHistory_OverflowSlotZero_Noop(t *testing.T) {
	store := newOvMemStore("convs")
	msgs := buildTurns(3)
	ctx, state := overflowCtx(4, 4)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("sess-zero")
	ctx.IntSlots[0] = 0 // no overflow tokens

	cfg := OverflowHistoryConfig{
		HistorySlot:  0,
		KeySlot:      1,
		OverflowSlot: 0,
		Domain:       "convs",
		Store:        store,
	}
	OverflowHistory(cfg).Action(ctx, state)

	// History unchanged, nothing stored.
	got := decodeOverflowSlot(t, ctx, 0)
	if len(got) != 6 {
		t.Fatalf("expected 6 messages unchanged, got %d", len(got))
	}
	stored := readStoreOverflow(t, store, "test-tenant", "sess-zero:overflow")
	if len(stored) != 0 {
		t.Fatalf("expected no overflow stored, got %d messages", len(stored))
	}
}

// ─── LoadOverflowHistory tests ────────────────────────────────────────────────

// TestLoadOverflowHistory_NoOverflow: overflow key not in store → history unchanged.
func TestLoadOverflowHistory_NoOverflow(t *testing.T) {
	store := newOvMemStore("convs")
	msgs := buildTurns(2)
	ctx, state := overflowCtx(4, 2)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("sess-noov")

	cfg := LoadOverflowHistoryConfig{
		HistorySlot: 0,
		KeySlot:     1,
		Domain:      "convs",
		Store:       store,
	}
	next := LoadOverflowHistory(cfg).Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}
	got := decodeOverflowSlot(t, ctx, 0)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages unchanged, got %d", len(got))
	}
}

// TestLoadOverflowHistory_Exists: overflow prepended to current history.
func TestLoadOverflowHistory_Exists(t *testing.T) {
	store := newOvMemStore("convs")
	overflow := buildTurns(3) // 6 messages (oldest)
	current := buildTurns(2)  // 4 messages (newest)

	_ = store.Put(context.Background(), datastore.Tenant("test-tenant"), "sess-load:overflow",
		marshalOverflowHistory(t, overflow))

	ctx, state := overflowCtx(4, 2)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, current)
	ctx.ByteSlots[1] = []byte("sess-load")

	cfg := LoadOverflowHistoryConfig{
		HistorySlot: 0,
		KeySlot:     1,
		Domain:      "convs",
		Store:       store,
	}
	LoadOverflowHistory(cfg).Action(ctx, state)

	got := decodeOverflowSlot(t, ctx, 0)
	// 6 overflow + 4 current = 10 messages total, overflow first.
	if len(got) != 10 {
		t.Fatalf("expected 10 messages (overflow + current), got %d", len(got))
	}
	// First messages should be from overflow.
	if got[0].Content != overflow[0].Content {
		t.Fatalf("expected overflow messages first, got %+v", got[0])
	}
}

// TestLoadOverflowHistory_MaxTurns: limits how many overflow turns are prepended.
func TestLoadOverflowHistory_MaxTurns(t *testing.T) {
	store := newOvMemStore("convs")
	overflow := buildTurns(5) // 10 messages; only want last 2 pairs = 4 messages
	current := buildTurns(1)  // 2 messages

	_ = store.Put(context.Background(), datastore.Tenant("test-tenant"), "sess-maxturn:overflow",
		marshalOverflowHistory(t, overflow))

	ctx, state := overflowCtx(4, 2)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, current)
	ctx.ByteSlots[1] = []byte("sess-maxturn")

	cfg := LoadOverflowHistoryConfig{
		HistorySlot: 0,
		KeySlot:     1,
		MaxTurns:    2, // keep only last 2 pairs from overflow
		Domain:      "convs",
		Store:       store,
	}
	LoadOverflowHistory(cfg).Action(ctx, state)

	got := decodeOverflowSlot(t, ctx, 0)
	// 2 pairs (4 msgs) from overflow + 1 pair (2 msgs) current = 6 messages.
	if len(got) != 6 {
		t.Fatalf("expected 6 messages (2 overflow pairs + 1 current pair), got %d", len(got))
	}
}

// TestLoadOverflowHistory_NilStore_Noop: nil store → noop, no panic.
func TestLoadOverflowHistory_NilStore_Noop(t *testing.T) {
	msgs := buildTurns(2)
	ctx, state := overflowCtx(4, 2)
	ctx.ByteSlots[0] = marshalOverflowHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("sess-nilstore")

	cfg := LoadOverflowHistoryConfig{
		HistorySlot: 0,
		KeySlot:     1,
		Domain:      "convs",
		Store:       nil,
	}
	next := LoadOverflowHistory(cfg).Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}
	got := decodeOverflowSlot(t, ctx, 0)
	if len(got) != 4 {
		t.Fatalf("expected history unchanged (4 messages), got %d", len(got))
	}
}
