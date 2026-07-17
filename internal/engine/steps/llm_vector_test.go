package steps

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/vectorstore"
)

// â”€â”€â”€ Mock VectorStore â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

type mockVectorStore struct {
	// SearchFn is called by Search; if nil, returns empty results.
	SearchFn func(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]vectorstore.SearchResult, error)
	// UpsertFn is called by Upsert; if nil, returns nil error.
	UpsertFn func(ctx context.Context, collection string, items []vectorstore.VectorItem) error
	// LastUpsertCollection is the collection passed to the most recent Upsert call.
	LastUpsertCollection string
	// LastUpsertItems are the items passed to the most recent Upsert call.
	LastUpsertItems []vectorstore.VectorItem
}

func (m *mockVectorStore) Search(ctx context.Context, collection string, vector []float32, topK int, minScore float32, filter map[string]any) ([]vectorstore.SearchResult, error) {
	if m.SearchFn != nil {
		return m.SearchFn(ctx, collection, vector, topK, minScore, filter)
	}
	return nil, nil
}

func (m *mockVectorStore) Upsert(ctx context.Context, collection string, items []vectorstore.VectorItem) error {
	m.LastUpsertCollection = collection
	m.LastUpsertItems = items
	if m.UpsertFn != nil {
		return m.UpsertFn(ctx, collection, items)
	}
	return nil
}

func (m *mockVectorStore) Delete(ctx context.Context, collection string, ids []string) error {
	return nil
}

func (m *mockVectorStore) Kind() string { return "mock" }
func (m *mockVectorStore) Close() error { return nil }

// â”€â”€â”€ Helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func makeVectorCtx() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ResponseStatus = 200
	return ctx
}

func makeVectorState() *engine.ExecutionState {
	return &engine.ExecutionState{PC: 0}
}

// encodeVec marshals a []float64 as JSON (simulates embed_text output).
func encodeVec(vec []float64) []byte {
	b, _ := json.Marshal(vec)
	return b
}

// â”€â”€â”€ VectorSearch Tests â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestVectorSearch_Success(t *testing.T) {
	wantResults := []vectorstore.SearchResult{
		{ID: "doc1", Score: 0.95, Content: "chunk one", Metadata: nil},
		{ID: "doc2", Score: 0.88, Content: "chunk two", Metadata: nil},
	}
	store := &mockVectorStore{
		SearchFn: func(_ context.Context, _ string, _ []float32, _ int, _ float32, _ map[string]any) ([]vectorstore.SearchResult, error) {
			return wantResults, nil
		},
	}

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.1, 0.2, 0.3})

	cfg := VectorSearchConfig{
		VectorSlot:        0,
		ResultSlot:        1,
		CollectionSlot:    -1,
		CountSlot:         0,
		TopK:              5,
		MinScore:          0,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorSearch(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1=%d, got %d (failed=%v, errorMsg=%s)", state.PC+1, nextPC, ctx.Failed, ctx.ErrorMsg)
	}
	if ctx.Failed {
		t.Fatalf("expected no failure, got Failed=true errorMsg=%s", ctx.ErrorMsg)
	}

	// Check ResultSlot has JSON
	var gotResults []vectorstore.SearchResult
	if err := json.Unmarshal(ctx.ByteSlots[1], &gotResults); err != nil {
		t.Fatalf("result slot not valid JSON: %v", err)
	}
	if len(gotResults) != 2 {
		t.Fatalf("expected 2 results, got %d", len(gotResults))
	}

	// Check CountSlot
	if ctx.IntSlots[0] != 2 {
		t.Errorf("expected CountSlot=2, got %d", ctx.IntSlots[0])
	}
}

func TestVectorSearch_EmptyVectorSlot_Noop(t *testing.T) {
	store := &mockVectorStore{}
	ctx := makeVectorCtx()
	// ByteSlots[0] is empty (nil) â€” no vector

	cfg := VectorSearchConfig{
		VectorSlot:        0,
		ResultSlot:        1,
		CollectionSlot:    -1,
		CountSlot:         -1,
		TopK:              5,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorSearch(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1 (no-op), got %d", nextPC)
	}
	if ctx.Failed {
		t.Error("expected no failure on empty vector slot")
	}
	if ctx.ByteSlots[1] != nil {
		t.Errorf("expected result slot to remain nil, got %v", ctx.ByteSlots[1])
	}
}

func TestVectorSearch_CollectionFromSlot(t *testing.T) {
	var gotCollection string
	store := &mockVectorStore{
		SearchFn: func(_ context.Context, collection string, _ []float32, _ int, _ float32, _ map[string]any) ([]vectorstore.SearchResult, error) {
			gotCollection = collection
			return []vectorstore.SearchResult{}, nil
		},
	}

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.1, 0.2})
	ctx.ByteSlots[2] = []byte("my-collection") // CollectionSlot=2

	cfg := VectorSearchConfig{
		VectorSlot:        0,
		ResultSlot:        1,
		CollectionSlot:    2,
		CountSlot:         -1,
		TopK:              5,
		Store:             store,
		DefaultCollection: "default-coll", // should NOT be used
	}

	state := makeVectorState()
	instr := VectorSearch(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v)", nextPC, ctx.Failed)
	}
	if gotCollection != "my-collection" {
		t.Errorf("expected collection 'my-collection', got %q", gotCollection)
	}
}

func TestVectorSearch_StoreError_StopsExecution(t *testing.T) {
	store := &mockVectorStore{
		SearchFn: func(_ context.Context, _ string, _ []float32, _ int, _ float32, _ map[string]any) ([]vectorstore.SearchResult, error) {
			return nil, errors.New("connection refused")
		},
	}

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.5, 0.6})

	cfg := VectorSearchConfig{
		VectorSlot:        0,
		ResultSlot:        1,
		CollectionSlot:    -1,
		CountSlot:         -1,
		TopK:              5,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorSearch(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", nextPC)
	}
	if !ctx.Failed {
		t.Error("expected Failed=true")
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected ResponseStatus=502, got %d", ctx.ResponseStatus)
	}
}

func TestVectorSearch_CountSlotNegative_Skipped(t *testing.T) {
	store := &mockVectorStore{
		SearchFn: func(_ context.Context, _ string, _ []float32, _ int, _ float32, _ map[string]any) ([]vectorstore.SearchResult, error) {
			return []vectorstore.SearchResult{{ID: "x", Score: 0.9, Content: "text"}}, nil
		},
	}

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.1})

	// Record initial IntSlots so we can verify they don't change
	initialInts := make([]int64, len(ctx.IntSlots))
	copy(initialInts, ctx.IntSlots)

	cfg := VectorSearchConfig{
		VectorSlot:        0,
		ResultSlot:        1,
		CollectionSlot:    -1,
		CountSlot:         -1, // skip count write
		TopK:              5,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorSearch(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v)", nextPC, ctx.Failed)
	}
	// No IntSlot should have changed
	for i, v := range ctx.IntSlots {
		if v != initialInts[i] {
			t.Errorf("IntSlots[%d] changed unexpectedly (CountSlot=-1 should skip)", i)
		}
	}
}

func TestVectorSearch_NoCollection_StopsExecution(t *testing.T) {
	store := &mockVectorStore{}
	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.1, 0.2})

	cfg := VectorSearchConfig{
		VectorSlot:        0,
		ResultSlot:        1,
		CollectionSlot:    -1,
		CountSlot:         -1,
		TopK:              5,
		Store:             store,
		DefaultCollection: "", // no default
	}

	state := makeVectorState()
	instr := VectorSearch(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", nextPC)
	}
	if !ctx.Failed {
		t.Error("expected Failed=true")
	}
	if ctx.ResponseStatus != 400 {
		t.Errorf("expected ResponseStatus=400, got %d", ctx.ResponseStatus)
	}
}

// â”€â”€â”€ VectorUpsert Tests â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestVectorUpsert_Success(t *testing.T) {
	store := &mockVectorStore{}

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.1, 0.2, 0.3})
	ctx.ByteSlots[1] = []byte("my chunk text")
	ctx.ByteSlots[2] = []byte("item-abc-123") // ID slot

	cfg := VectorUpsertConfig{
		VectorSlot:        0,
		ContentSlot:       1,
		IDSlot:            2,
		MetadataSlot:      -1,
		CollectionSlot:    -1,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorUpsert(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1=%d, got %d (failed=%v, errorMsg=%s)", state.PC+1, nextPC, ctx.Failed, ctx.ErrorMsg)
	}
	if ctx.Failed {
		t.Fatalf("expected no failure, got Failed=true errorMsg=%s", ctx.ErrorMsg)
	}
	if store.LastUpsertCollection != "docs" {
		t.Errorf("expected collection 'docs', got %q", store.LastUpsertCollection)
	}
	if len(store.LastUpsertItems) != 1 {
		t.Fatalf("expected 1 item upserted, got %d", len(store.LastUpsertItems))
	}
	item := store.LastUpsertItems[0]
	if item.ID != "item-abc-123" {
		t.Errorf("expected ID 'item-abc-123', got %q", item.ID)
	}
	if item.Content != "my chunk text" {
		t.Errorf("expected content 'my chunk text', got %q", item.Content)
	}
	if len(item.Vector) != 3 {
		t.Errorf("expected vector length 3, got %d", len(item.Vector))
	}
}

func TestVectorUpsert_EmptyVectorSlot_Noop(t *testing.T) {
	store := &mockVectorStore{}
	ctx := makeVectorCtx()
	// ByteSlots[0] is empty (nil) â€” no vector

	cfg := VectorUpsertConfig{
		VectorSlot:        0,
		ContentSlot:       1,
		IDSlot:            -1,
		MetadataSlot:      -1,
		CollectionSlot:    -1,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorUpsert(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1 (no-op), got %d", nextPC)
	}
	if ctx.Failed {
		t.Error("expected no failure on empty vector slot")
	}
	if store.LastUpsertCollection != "" {
		t.Error("expected store.Upsert not to be called")
	}
}

func TestVectorUpsert_AutoGeneratesID(t *testing.T) {
	store := &mockVectorStore{}

	content := []byte("deterministic content")
	expectedSum := sha256.Sum256(content)
	expectedID := fmt.Sprintf("%x", expectedSum)

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.5})
	ctx.ByteSlots[1] = content

	cfg := VectorUpsertConfig{
		VectorSlot:        0,
		ContentSlot:       1,
		IDSlot:            -1, // auto-generate
		MetadataSlot:      -1,
		CollectionSlot:    -1,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorUpsert(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v)", nextPC, ctx.Failed)
	}
	if len(store.LastUpsertItems) != 1 {
		t.Fatalf("expected 1 item, got %d", len(store.LastUpsertItems))
	}
	gotID := store.LastUpsertItems[0].ID
	if gotID != expectedID {
		t.Errorf("expected ID %q, got %q", expectedID, gotID)
	}
}

func TestVectorUpsert_ExplicitID(t *testing.T) {
	store := &mockVectorStore{}

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.1})
	ctx.ByteSlots[1] = []byte("some content")
	ctx.ByteSlots[3] = []byte("explicit-id-xyz")

	cfg := VectorUpsertConfig{
		VectorSlot:        0,
		ContentSlot:       1,
		IDSlot:            3,
		MetadataSlot:      -1,
		CollectionSlot:    -1,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorUpsert(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v)", nextPC, ctx.Failed)
	}
	if len(store.LastUpsertItems) == 0 {
		t.Fatal("expected item to be upserted")
	}
	if store.LastUpsertItems[0].ID != "explicit-id-xyz" {
		t.Errorf("expected ID 'explicit-id-xyz', got %q", store.LastUpsertItems[0].ID)
	}
}

func TestVectorUpsert_WithMetadata(t *testing.T) {
	store := &mockVectorStore{}

	metaJSON := []byte(`{"source":"wiki","page":42}`)

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.1, 0.2})
	ctx.ByteSlots[1] = []byte("chunk with meta")
	ctx.ByteSlots[4] = metaJSON // MetadataSlot=4

	cfg := VectorUpsertConfig{
		VectorSlot:        0,
		ContentSlot:       1,
		IDSlot:            -1,
		MetadataSlot:      4,
		CollectionSlot:    -1,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorUpsert(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v)", nextPC, ctx.Failed)
	}
	if len(store.LastUpsertItems) == 0 {
		t.Fatal("expected item to be upserted")
	}
	meta := store.LastUpsertItems[0].Metadata
	if meta == nil {
		t.Fatal("expected metadata to be set, got nil")
	}
	if src, ok := meta["source"].(string); !ok || src != "wiki" {
		t.Errorf("expected metadata source='wiki', got %v", meta["source"])
	}
}

func TestVectorUpsert_StoreError_StopsExecution(t *testing.T) {
	store := &mockVectorStore{
		UpsertFn: func(_ context.Context, _ string, _ []vectorstore.VectorItem) error {
			return errors.New("write failed")
		},
	}

	ctx := makeVectorCtx()
	ctx.ByteSlots[0] = encodeVec([]float64{0.3, 0.4})
	ctx.ByteSlots[1] = []byte("content")

	cfg := VectorUpsertConfig{
		VectorSlot:        0,
		ContentSlot:       1,
		IDSlot:            -1,
		MetadataSlot:      -1,
		CollectionSlot:    -1,
		Store:             store,
		DefaultCollection: "docs",
	}

	state := makeVectorState()
	instr := VectorUpsert(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", nextPC)
	}
	if !ctx.Failed {
		t.Error("expected Failed=true")
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected ResponseStatus=502, got %d", ctx.ResponseStatus)
	}
}
