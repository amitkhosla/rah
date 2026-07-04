package observability

import (
	"context"
	"testing"
)

func TestMemObsStorePayloads(t *testing.T) {
	store := NewMemObsStore(100, 50)
	store.payloadBudget = 1000 // small budget for testing

	records := []PayloadRecord{
		{TraceID: 1, Kind: 1, Seq: 0, Content: make([]byte, 400)},
		{TraceID: 2, Kind: 1, Seq: 0, Content: make([]byte, 400)},
	}
	_ = store.WritePayloadBatch(context.Background(), records)

	got, err := store.QueryPayloads(context.Background(), 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("expected 1 record for traceID 1, got %d err %v", len(got), err)
	}

	// Add enough to trigger eviction (budget=1000, 80% = 800; we have 800 bytes, add more)
	more := []PayloadRecord{
		{TraceID: 3, Kind: 1, Seq: 0, Content: make([]byte, 400)},
	}
	_ = store.WritePayloadBatch(context.Background(), more)

	// After eviction (target 60% = 600), traceID 1 should be evicted (oldest).
	evicted, _ := store.QueryPayloads(context.Background(), 1)
	if len(evicted) != 0 {
		t.Errorf("expected traceID 1 to be evicted, still has %d records", len(evicted))
	}
	// traceID 3 should still be present.
	kept, _ := store.QueryPayloads(context.Background(), 3)
	if len(kept) == 0 {
		t.Error("expected traceID 3 to be retained after eviction")
	}
}
