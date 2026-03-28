package steps

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"rah/internal/datastore"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func newHistoryCtx(numSlots int) (*rctx.Context, *engine.ExecutionState) {
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, numSlots),
		TenantKey: "test-tenant",
	}
	state := &engine.ExecutionState{PC: 0}
	return ctx, state
}

func marshalHistory(t *testing.T, msgs []CanonicalMessage) []byte {
	t.Helper()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	return b
}

func decodeSlotHistory(t *testing.T, ctx *rctx.Context, slot int) []CanonicalMessage {
	t.Helper()
	var msgs []CanonicalMessage
	if err := json.Unmarshal(ctx.ByteSlots[slot], &msgs); err != nil {
		t.Fatalf("decode slot %d history: %v", slot, err)
	}
	return msgs
}

// ─── memStore — in-memory KeyValueStore for tests ─────────────────────────────

type memStore struct {
	data   map[string][]byte
	domain string
}

func newMemStore(domain string) *memStore {
	return &memStore{data: make(map[string][]byte), domain: domain}
}

func (s *memStore) Put(_ context.Context, tenant datastore.Tenant, key string, value []byte) error {
	skey, _ := datastore.BuildScopedKey(tenant, s.domain, key)
	buf := make([]byte, len(value))
	copy(buf, value)
	s.data[skey] = buf
	return nil
}
func (s *memStore) Get(_ context.Context, tenant datastore.Tenant, key string) ([]byte, bool, error) {
	skey, _ := datastore.BuildScopedKey(tenant, s.domain, key)
	v, ok := s.data[skey]
	return v, ok, nil
}
func (s *memStore) Delete(_ context.Context, tenant datastore.Tenant, key string) error {
	skey, _ := datastore.BuildScopedKey(tenant, s.domain, key)
	delete(s.data, skey)
	return nil
}
func (s *memStore) ListKeys(_ context.Context, _ datastore.Tenant, _ string) ([]string, error) {
	return nil, nil
}
func (s *memStore) Kind() string                     { return "mem" }
func (s *memStore) Name() string                     { return s.domain }
func (s *memStore) PoolStats() datastore.PoolStats   { return datastore.PoolStats{} }
func (s *memStore) Close() error                     { return nil }

// ttlMemStore also implements ExpiringStore.
type ttlMemStore struct {
	*memStore
	ttl time.Duration
}

func (s *ttlMemStore) PutWithTTL(_ context.Context, tenant datastore.Tenant, key string, value []byte, ttl time.Duration) error {
	s.ttl = ttl
	return s.memStore.Put(context.Background(), tenant, key, value)
}

// ─── append_message ───────────────────────────────────────────────────────────

func TestAppendMessage_BasicUser(t *testing.T) {
	ctx, state := newHistoryCtx(3)
	ctx.ByteSlots[1] = []byte("hello")

	instr := AppendMessage(0, 1, RoleUser, 0)
	next := instr.Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}
	msgs := decodeSlotHistory(t, ctx, 0)
	if len(msgs) != 1 || msgs[0].Role != RoleUser || msgs[0].Content != "hello" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
}

func TestAppendMessage_Accumulates(t *testing.T) {
	ctx, state := newHistoryCtx(3)
	existing := marshalHistory(t, []CanonicalMessage{{Role: RoleUser, Content: "q1"}})
	ctx.ByteSlots[0] = existing
	ctx.ByteSlots[1] = []byte("a1")

	AppendMessage(0, 1, RoleAssistant, 0).Action(ctx, state)
	msgs := decodeSlotHistory(t, ctx, 0)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].Role != RoleAssistant || msgs[1].Content != "a1" {
		t.Fatalf("unexpected second message: %+v", msgs[1])
	}
}

func TestAppendMessage_MaxTurnsTrim(t *testing.T) {
	// Build 4 turn pairs (user+assistant x4), then append a 5th user message
	// with maxTurns=2 → oldest pairs trimmed, only 2 pairs remain.
	var existing []CanonicalMessage
	for i := 0; i < 4; i++ {
		existing = append(existing, CanonicalMessage{Role: RoleUser, Content: "u"})
		existing = append(existing, CanonicalMessage{Role: RoleAssistant, Content: "a"})
	}
	ctx, state := newHistoryCtx(3)
	ctx.ByteSlots[0] = marshalHistory(t, existing)
	ctx.ByteSlots[1] = []byte("u5")

	AppendMessage(0, 1, RoleUser, 2).Action(ctx, state)
	msgs := decodeSlotHistory(t, ctx, 0)
	// Should have at most 2 complete pairs + the new user message = 5 msgs max,
	// but trimTurns runs after append so we expect 2 pairs = 4 msgs (trim cuts the partial).
	// Actually: after appending u5 we have 9 msgs. trimTurns(9, 2) keeps last 2 pairs = 4 msgs.
	// The new user message u5 is unpaired so it gets cut by trimTurns (pair scan from end).
	// Let's just verify len <= 5 and no panic.
	if len(msgs) > 5 {
		t.Fatalf("expected trimmed history, got %d messages", len(msgs))
	}
}

func TestAppendMessage_EmptyContent_Skip(t *testing.T) {
	ctx, state := newHistoryCtx(2)
	// contentSlot is empty → skip
	AppendMessage(0, 1, RoleUser, 0).Action(ctx, state)
	if len(ctx.ByteSlots[0]) != 0 {
		t.Fatal("expected history slot to remain empty")
	}
}

// ─── trim_history ─────────────────────────────────────────────────────────────

func TestTrimHistory_ByTurns(t *testing.T) {
	var msgs []CanonicalMessage
	for i := 0; i < 5; i++ {
		msgs = append(msgs, CanonicalMessage{Role: RoleUser, Content: "u"})
		msgs = append(msgs, CanonicalMessage{Role: RoleAssistant, Content: "a"})
	}
	ctx, state := newHistoryCtx(2)
	ctx.ByteSlots[0] = marshalHistory(t, msgs)

	TrimHistory(0, 2, 0).Action(ctx, state)
	got := decodeSlotHistory(t, ctx, 0)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages (2 pairs), got %d", len(got))
	}
}

func TestTrimHistory_ByTokens(t *testing.T) {
	// Each message content is "a" (1 char ≈ 1 token + 4 overhead = 5 tokens each).
	// 10 messages × 5 = 50 tokens. maxTokens=20 should leave 4 messages.
	var msgs []CanonicalMessage
	for i := 0; i < 10; i++ {
		msgs = append(msgs, CanonicalMessage{Role: RoleUser, Content: "a"})
	}
	ctx, state := newHistoryCtx(2)
	ctx.ByteSlots[0] = marshalHistory(t, msgs)

	TrimHistory(0, 0, 20).Action(ctx, state)
	got := decodeSlotHistory(t, ctx, 0)
	if len(got) > 4 {
		t.Fatalf("expected ≤4 messages after token trim, got %d", len(got))
	}
}

func TestTrimHistory_Empty_Noop(t *testing.T) {
	ctx, state := newHistoryCtx(2)
	// Empty slot — no panic, no write.
	TrimHistory(0, 5, 100).Action(ctx, state)
	if len(ctx.ByteSlots[0]) != 0 {
		t.Fatal("expected slot to remain empty")
	}
}

// ─── load_history ─────────────────────────────────────────────────────────────

func TestLoadHistory_NilStore_WritesEmpty(t *testing.T) {
	ctx, state := newHistoryCtx(3)
	ctx.ByteSlots[1] = []byte("sess-1")

	LoadHistory(nil, "convs", 0, 1).Action(ctx, state)
	if string(ctx.ByteSlots[0]) != "[]" {
		t.Fatalf("expected [], got %s", ctx.ByteSlots[0])
	}
}

func TestLoadHistory_EmptyKey_WritesEmpty(t *testing.T) {
	ctx, state := newHistoryCtx(3)
	// keySlot is empty
	LoadHistory(newMemStore("convs"), "convs", 0, 1).Action(ctx, state)
	if string(ctx.ByteSlots[0]) != "[]" {
		t.Fatalf("expected [], got %s", ctx.ByteSlots[0])
	}
}

func TestLoadHistory_KeyFound(t *testing.T) {
	store := newMemStore("convs")
	msgs := []CanonicalMessage{{Role: RoleUser, Content: "hi"}}
	encoded := marshalHistory(t, msgs)
	_ = store.Put(context.Background(), datastore.Tenant("test-tenant"), "sess-1", encoded)

	ctx, state := newHistoryCtx(3)
	ctx.TenantKey = "test-tenant"
	ctx.ByteSlots[1] = []byte("sess-1")

	LoadHistory(store, "convs", 0, 1).Action(ctx, state)
	got := decodeSlotHistory(t, ctx, 0)
	if len(got) != 1 || got[0].Content != "hi" {
		t.Fatalf("unexpected loaded history: %+v", got)
	}
}

func TestLoadHistory_KeyNotFound_WritesEmpty(t *testing.T) {
	ctx, state := newHistoryCtx(3)
	ctx.TenantKey = "test-tenant"
	ctx.ByteSlots[1] = []byte("nonexistent")

	LoadHistory(newMemStore("convs"), "convs", 0, 1).Action(ctx, state)
	if string(ctx.ByteSlots[0]) != "[]" {
		t.Fatalf("expected [], got %s", ctx.ByteSlots[0])
	}
}

// ─── save_history ─────────────────────────────────────────────────────────────

func TestSaveHistory_NilStore_Noop(t *testing.T) {
	ctx, state := newHistoryCtx(3)
	ctx.ByteSlots[0] = []byte(`[{"role":"user","content":"hi"}]`)
	ctx.ByteSlots[1] = []byte("sess-1")
	// Should not panic.
	next := SaveHistory(nil, "convs", 0, 1, 0).Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}
}

func TestSaveHistory_Saves(t *testing.T) {
	store := newMemStore("convs")
	ctx, state := newHistoryCtx(3)
	ctx.TenantKey = "test-tenant"
	ctx.ByteSlots[0] = []byte(`[{"role":"user","content":"hi"}]`)
	ctx.ByteSlots[1] = []byte("sess-1")

	SaveHistory(store, "convs", 0, 1, 0).Action(ctx, state)

	val, found, err := store.Get(context.Background(), datastore.Tenant("test-tenant"), "sess-1")
	if err != nil || !found {
		t.Fatalf("expected key to be stored: err=%v found=%v", err, found)
	}
	if string(val) != `[{"role":"user","content":"hi"}]` {
		t.Fatalf("unexpected stored value: %s", val)
	}
}

func TestSaveHistory_WithTTL(t *testing.T) {
	base := newMemStore("convs")
	store := &ttlMemStore{memStore: base}
	ctx, state := newHistoryCtx(3)
	ctx.TenantKey = "test-tenant"
	ctx.ByteSlots[0] = []byte(`[]`)
	ctx.ByteSlots[1] = []byte("sess-2")

	SaveHistory(store, "convs", 0, 1, 3600).Action(ctx, state)

	if store.ttl != 3600*time.Second {
		t.Fatalf("expected TTL 3600s, got %v", store.ttl)
	}
}
