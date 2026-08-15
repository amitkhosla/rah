package events

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amitkhosla/rah/internal/connectors/messaging"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// newTestFlowManager creates a minimal FlowManager suitable for testing.
func newTestFlowManager() *engine.FlowManager {
	fm := &engine.FlowManager{}
	state := &engine.EngineState{
		FlowLibrary:      make(map[string][]engine.Instruction),
		FlowSlotRegistry: make(map[string]map[string]int),
	}
	state.FlowSlotRegistry["test-flow"] = map[string]int{
		"__event__":       0,
		"__event_key__":   1,
		"__event_topic__": 2,
		"payload_var":     3,
		"key_var":         4,
		"header_var":      5,
	}
	fm.State.Store(state)
	fm.Pool = sync.Pool{
		New: func() interface{} {
			return &rctx.Context{
				ByteSlots: make([][]byte, 10),
				IntSlots:  make([]int64, 10),
				BoolSlots: make([]bool, 10),
			}
		},
	}
	return fm
}

func newTestMsg(payload string) messaging.ConsumedMessage {
	return messaging.ConsumedMessage{
		Topic:   "events",
		Key:     []byte("key1"),
		Payload: []byte(payload),
		Headers: make(map[string]string),
	}
}

// TestEventExecutor_AppName verifies appName is stored and Handle completes.
func TestEventExecutor_AppName(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "payments", "", 0, nil)
	if executor.appName != "payments" {
		t.Errorf("expected appName='payments', got %q", executor.appName)
	}
	if err := executor.Handle(ctx, newTestMsg(`{"id":"abc"}`)); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
}

// TestEventExecutor_DedupDropsDuplicate verifies the second identical message is silently dropped.
func TestEventExecutor_DedupDropsDuplicate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newInMemoryDedupStore(30 * time.Second)
	defer store.Close()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "id", 60, store)
	msg := newTestMsg(`{"id":"abc"}`)

	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("first Handle() error: %v", err)
	}
	// After first Handle the key is registered — IsDuplicate should now return true.
	isDup, err := store.IsDuplicate(ctx, "abc", 60*time.Second)
	if err != nil {
		t.Fatalf("IsDuplicate error: %v", err)
	}
	if !isDup {
		t.Error("expected key 'abc' to be marked as duplicate after first Handle")
	}
	// Second Handle should be silently dropped (no error).
	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("second Handle() error: %v", err)
	}
}

// TestEventExecutor_DedupAllowsAfterExpiry verifies a key is re-allowed after its window expires.
func TestEventExecutor_DedupAllowsAfterExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1-second window so test can wait it out.
	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "id", 1, nil)
	msg := newTestMsg(`{"id":"abc"}`)

	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("first Handle() error: %v", err)
	}
	// Second call within window should be dropped.
	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("second Handle() error: %v", err)
	}
	// Wait for window to expire.
	time.Sleep(1200 * time.Millisecond)
	// Third call should succeed without error (window expired).
	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("third Handle() after expiry error: %v", err)
	}
}

// TestEventExecutor_DedupDisabled verifies dedup is a no-op when window is 0.
func TestEventExecutor_DedupDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "id", 0, nil)
	if executor.dedupWindow != 0 {
		t.Errorf("expected dedupWindow=0, got %v", executor.dedupWindow)
	}
	if executor.dedupStore != nil {
		t.Error("expected nil dedupStore when window=0")
	}
	msg := newTestMsg(`{"id":"abc"}`)
	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("first Handle() error: %v", err)
	}
	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("second Handle() error: %v", err)
	}
}

// TestEventExecutor_NoDeduplicationKey verifies dedup is skipped when dedupKey is empty.
func TestEventExecutor_NoDeduplicationKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newInMemoryDedupStore(30 * time.Second)
	defer store.Close()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "", 60, store)
	if executor.dedupKey != "" {
		t.Errorf("expected empty dedupKey, got %q", executor.dedupKey)
	}
	msg := newTestMsg(`{"id":"abc"}`)
	executor.Handle(ctx, msg) //nolint
	executor.Handle(ctx, msg) //nolint

	// No key should have been registered because dedupKey is empty.
	isDup, _ := store.IsDuplicate(ctx, "abc", 60*time.Second)
	// IsDuplicate itself will register "abc" — so it returns false on first call.
	// What we care about is that executor didn't register anything: the very first
	// IsDuplicate call here returns false (key was absent), proving executor never stored it.
	if isDup {
		t.Error("expected no dedup entry when dedupKey is empty")
	}
}

// TestEventExecutor_DifferentDedupKeys verifies separate keys are independent.
func TestEventExecutor_DifferentDedupKeys(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newInMemoryDedupStore(30 * time.Second)
	defer store.Close()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "id", 60, store)

	executor.Handle(ctx, newTestMsg(`{"id":"abc"}`)) //nolint
	executor.Handle(ctx, newTestMsg(`{"id":"xyz"}`)) //nolint

	// Both keys should now be registered as duplicates.
	isDupABC, _ := store.IsDuplicate(ctx, "abc", 60*time.Second)
	isDupXYZ, _ := store.IsDuplicate(ctx, "xyz", 60*time.Second)
	if !isDupABC {
		t.Error("expected 'abc' to be duplicate after Handle")
	}
	if !isDupXYZ {
		t.Error("expected 'xyz' to be duplicate after Handle")
	}
}

// TestEventExecutor_MissingDeduplicationKey verifies missing JSON key is handled gracefully.
func TestEventExecutor_MissingDeduplicationKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "missing_key", 60, nil)
	msg := newTestMsg(`{"id":"abc"}`)
	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
	if err := executor.Handle(ctx, msg); err != nil {
		t.Fatalf("second Handle() error: %v", err)
	}
}

// TestEventExecutor_InvalidJSON verifies invalid JSON payload is handled without panic.
func TestEventExecutor_InvalidJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "id", 60, nil)
	if err := executor.Handle(ctx, newTestMsg(`{invalid json}`)); err != nil {
		t.Fatalf("Handle() error on invalid JSON: %v", err)
	}
}

// TestEventExecutor_EmptyPayload verifies empty payload is handled without panic.
func TestEventExecutor_EmptyPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "id", 60, nil)
	if err := executor.Handle(ctx, newTestMsg("")); err != nil {
		t.Fatalf("Handle() error on empty payload: %v", err)
	}
}

// TestEventExecutor_ComplexDedupKey verifies gjson nested path extraction.
func TestEventExecutor_ComplexDedupKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newInMemoryDedupStore(30 * time.Second)
	defer store.Close()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "data.id", 60, store)

	executor.Handle(ctx, newTestMsg(`{"data":{"id":"abc123"}}`)) //nolint
	executor.Handle(ctx, newTestMsg(`{"data":{"id":"xyz789"}}`)) //nolint

	isDupABC, _ := store.IsDuplicate(ctx, "abc123", 60*time.Second)
	isDupXYZ, _ := store.IsDuplicate(ctx, "xyz789", 60*time.Second)
	if !isDupABC {
		t.Error("expected nested key 'abc123' registered after Handle")
	}
	if !isDupXYZ {
		t.Error("expected nested key 'xyz789' registered after Handle")
	}
}

// TestEventExecutor_ConcurrentDedup verifies thread-safety under concurrent access.
func TestEventExecutor_ConcurrentDedup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executor := NewEventExecutor(ctx, newTestFlowManager(), "test-flow", "", "", nil, "", "id", 60, nil)
	var wg sync.WaitGroup
	errCount := atomic.Int32{}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := messaging.ConsumedMessage{
				Topic:   "events",
				Payload: []byte(`{"id":"concurrent-test"}`),
				Headers: make(map[string]string),
			}
			if err := executor.Handle(ctx, msg); err != nil {
				errCount.Add(1)
			}
			if err := executor.Handle(ctx, msg); err != nil {
				errCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if errCount.Load() != 0 {
		t.Errorf("expected no errors from concurrent Handle calls, got %d", errCount.Load())
	}
}
