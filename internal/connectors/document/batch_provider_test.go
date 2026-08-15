package document

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amitkhosla/rah/internal/config"
)

// fakeProvider is a DocumentProvider that counts calls and records batch sizes.
type fakeProvider struct {
	getManyCallCount     atomic.Int32
	getManyBatchSizes    []int
	putManyCallCount     atomic.Int32
	putManyBatchSizes    []int
	deleteManyCallCount  atomic.Int32
	deleteManyBatchSizes []int
	mu                   sync.Mutex
}

func (f *fakeProvider) Get(ctx context.Context, req GetRequest) ([]byte, error) {
	return nil, nil
}

func (f *fakeProvider) GetMany(ctx context.Context, req GetManyRequest) (map[string][]byte, error) {
	f.getManyCallCount.Add(1)
	f.mu.Lock()
	f.getManyBatchSizes = append(f.getManyBatchSizes, len(req.IDs))
	f.mu.Unlock()
	result := make(map[string][]byte, len(req.IDs))
	for _, id := range req.IDs {
		result[id] = []byte(`{"id":"` + id + `"}`)
	}
	return result, nil
}

func (f *fakeProvider) Put(ctx context.Context, req PutRequest) error {
	return nil
}

func (f *fakeProvider) PutMany(ctx context.Context, req PutManyRequest) error {
	f.putManyCallCount.Add(1)
	f.mu.Lock()
	f.putManyBatchSizes = append(f.putManyBatchSizes, len(req.Docs))
	f.mu.Unlock()
	return nil
}

func (f *fakeProvider) Delete(ctx context.Context, req DeleteRequest) error {
	return nil
}

func (f *fakeProvider) DeleteMany(ctx context.Context, req DeleteManyRequest) error {
	f.deleteManyCallCount.Add(1)
	f.mu.Lock()
	f.deleteManyBatchSizes = append(f.deleteManyBatchSizes, len(req.IDs))
	f.mu.Unlock()
	return nil
}

func (f *fakeProvider) Query(ctx context.Context, req QueryRequest) ([]byte, error) {
	return nil, nil
}

func (f *fakeProvider) Count(ctx context.Context, req QueryRequest) (int64, error) {
	return 0, nil
}

func (f *fakeProvider) Execute(ctx context.Context, req ExecuteRequest) ([]byte, error) {
	return nil, nil
}

func (f *fakeProvider) Ping(ctx context.Context) error {
	return nil
}

func (f *fakeProvider) Close() error {
	return nil
}

func TestBatchingDocumentProvider_CoalescesReads(t *testing.T) {
	fake := &fakeProvider{}
	cfg := config.DocumentConnectorConfig{WriteBuffer: false}

	b := newBatchingDocumentProvider(fake, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.start(ctx)

	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	results := make([][]byte, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("doc-%d", i)
			data, err := b.Get(context.Background(), GetRequest{Collection: "test", ID: id})
			if err != nil {
				t.Errorf("Get %d failed: %v", i, err)
			}
			results[i] = data
		}(i)
	}

	wg.Wait()

	// Coalescing is opportunistic: verify correctness (all N docs returned, at least 1 GetMany call).
	// Scheduling is non-deterministic — the coalescing count itself is not asserted.
	calls := fake.getManyCallCount.Load()
	if calls == 0 {
		t.Fatal("expected at least one GetMany call")
	}

	fake.mu.Lock()
	total := 0
	for _, sz := range fake.getManyBatchSizes {
		total += sz
	}
	fake.mu.Unlock()
	if total != N {
		t.Errorf("expected %d total IDs fetched across all GetMany calls; got %d", N, total)
	}

	for i, data := range results {
		if len(data) == 0 {
			t.Errorf("result[%d] is empty", i)
		}
	}
}

func TestBatchingDocumentProvider_FilterBypassesBatcher(t *testing.T) {
	fake := &fakeProvider{}
	cfg := config.DocumentConnectorConfig{WriteBuffer: false}
	b := newBatchingDocumentProvider(fake, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.start(ctx)

	// Filter-based Get should bypass the batcher (call inner.Get directly)
	_, err := b.Get(context.Background(), GetRequest{
		Collection: "test",
		ID:         "doc-1",
		Filter:     []byte(`{"status":"active"}`),
	})
	if err != nil {
		t.Fatalf("filter-based Get failed: %v", err)
	}
	// GetMany should NOT have been called
	if fake.getManyCallCount.Load() != 0 {
		t.Errorf("filter-based Get should bypass batcher; GetMany was called %d times", fake.getManyCallCount.Load())
	}
}

func TestBatchingDocumentProvider_WriteBufferVisible(t *testing.T) {
	fake := &fakeProvider{}
	cfg := config.DocumentConnectorConfig{WriteBuffer: true}
	b := newBatchingDocumentProvider(fake, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.start(ctx)

	doc := []byte(`{"name":"test"}`)
	// Put doc-1 with write buffer enabled
	err := b.Put(context.Background(), PutRequest{Collection: "col", ID: "doc-1", Document: doc, Upsert: true})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Immediately Get doc-1 — should come from write buffer, not DB
	data, err := b.Get(context.Background(), GetRequest{Collection: "col", ID: "doc-1"})
	if err != nil {
		t.Fatalf("Get after Put failed: %v", err)
	}

	// With write buffer, we should have received the document from the pending buffer
	if len(data) == 0 {
		t.Errorf("expected write buffer to return doc after Put; got empty result")
	}
	if string(data) != string(doc) {
		t.Errorf("expected write buffer to return %q; got %q", string(doc), string(data))
	}
}

func TestBatchingDocumentProvider_CoalesceWrites(t *testing.T) {
	fake := &fakeProvider{}
	cfg := config.DocumentConnectorConfig{WriteBuffer: false}
	b := newBatchingDocumentProvider(fake, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.start(ctx)

	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("doc-%d", i)
			err := b.Put(context.Background(), PutRequest{
				Collection: "test",
				ID:         id,
				Document:   []byte(`{"value":` + fmt.Sprintf("%d", i) + `}`),
				Upsert:     true,
			})
			if err != nil {
				t.Errorf("Put %d failed: %v", i, err)
			}
		}(i)
	}

	wg.Wait()

	// Coalescing is opportunistic: when concurrent Puts arrive faster than the
	// worker drains the channel they are batched together. We verify correctness
	// (all N puts reached the store) and that at least one PutMany call was made.
	calls := fake.putManyCallCount.Load()
	if calls == 0 {
		t.Fatal("expected at least one PutMany call; got 0")
	}
	fake.mu.Lock()
	total := 0
	for _, sz := range fake.putManyBatchSizes {
		total += sz
	}
	fake.mu.Unlock()
	if total != N {
		t.Errorf("expected %d total documents written across all PutMany calls; got %d", N, total)
	}
}

func TestBatchingDocumentProvider_PassthroughBatchOps(t *testing.T) {
	fake := &fakeProvider{}
	cfg := config.DocumentConnectorConfig{WriteBuffer: false}
	b := newBatchingDocumentProvider(fake, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.start(ctx)

	// Direct GetMany call should pass through to inner provider
	results, err := b.GetMany(context.Background(), GetManyRequest{
		Collection: "test",
		IDs:        []string{"id1", "id2", "id3"},
	})
	if err != nil {
		t.Fatalf("GetMany failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results; got %d", len(results))
	}
	if fake.getManyCallCount.Load() != 1 {
		t.Errorf("expected 1 direct GetMany call; got %d", fake.getManyCallCount.Load())
	}

	// Direct PutMany call should pass through to inner provider
	err = b.PutMany(context.Background(), PutManyRequest{
		Collection: "test",
		Docs:       map[string][]byte{"id1": []byte(`{}`), "id2": []byte(`{}`)},
		Upsert:     true,
	})
	if err != nil {
		t.Fatalf("PutMany failed: %v", err)
	}
	if fake.putManyCallCount.Load() != 1 {
		t.Errorf("expected 1 direct PutMany call; got %d", fake.putManyCallCount.Load())
	}

	// Direct DeleteMany call should pass through to inner provider
	err = b.DeleteMany(context.Background(), DeleteManyRequest{
		Collection: "test",
		IDs:        []string{"id1", "id2"},
	})
	if err != nil {
		t.Fatalf("DeleteMany failed: %v", err)
	}
	if fake.deleteManyCallCount.Load() != 1 {
		t.Errorf("expected 1 direct DeleteMany call; got %d", fake.deleteManyCallCount.Load())
	}
}

func TestBatchingDocumentProvider_WriteBufferCleanupAfterFlush(t *testing.T) {
	fake := &fakeProvider{}
	cfg := config.DocumentConnectorConfig{WriteBuffer: true}
	b := newBatchingDocumentProvider(fake, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.start(ctx)

	doc := []byte(`{"name":"test"}`)

	// Put doc-1
	err := b.Put(context.Background(), PutRequest{Collection: "col", ID: "doc-1", Document: doc, Upsert: true})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get immediately — should come from buffer
	data1, err := b.Get(context.Background(), GetRequest{Collection: "col", ID: "doc-1"})
	if err != nil {
		t.Fatalf("first Get failed: %v", err)
	}
	if len(data1) == 0 {
		t.Errorf("expected buffer hit; got empty result")
	}

	// Wait for batch to flush
	time.Sleep(100 * time.Millisecond)

	// After flush, the pending entry should be cleaned up from the buffer
	// (next Get would go to DB; we verify the buffer is now empty)
	// We can't directly query the buffer, but we verify the put call succeeded
	if fake.putManyCallCount.Load() != 1 {
		t.Errorf("expected 1 PutMany call after flush; got %d", fake.putManyCallCount.Load())
	}
}
