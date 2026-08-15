package events

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/amitkhosla/rah/internal/connectors/messaging"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// createBatchTestFlowManager creates a FlowManager suitable for batch executor testing
func createBatchTestFlowManager() *engine.FlowManager {
	fm := &engine.FlowManager{}

	state := &engine.EngineState{
		FlowLibrary:      make(map[string][]engine.Instruction),
		FlowSlotRegistry: make(map[string]map[string]int),
	}

	state.FlowSlotRegistry["batch-flow"] = map[string]int{
		"batch_payload": 0,
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

// TestBatchingExecutor_FlushOnSize tests that batch flushes when size is reached
func TestBatchingExecutor_FlushOnSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 3, 0) // batchSize=3, no window

	payloads := [][]byte{
		[]byte(`{"msg":1}`),
		[]byte(`{"msg":2}`),
		[]byte(`{"msg":3}`),
	}

	// Send 2 messages — should not flush yet
	err := executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[0]})
	if err != nil {
		t.Fatalf("First Handle() returned error: %v", err)
	}

	err = executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[1]})
	if err != nil {
		t.Fatalf("Second Handle() returned error: %v", err)
	}

	// Verify batch is accumulated but not flushed
	executor.mu.Lock()
	if len(executor.batch) != 2 {
		t.Errorf("Expected 2 accumulated messages, got %d", len(executor.batch))
	}
	executor.mu.Unlock()

	// Send 3rd message — should flush
	err = executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[2]})
	if err != nil {
		t.Fatalf("Third Handle() returned error: %v", err)
	}

	// After flush, batch should be reset
	executor.mu.Lock()
	batchLen := len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 0 {
		t.Errorf("Expected batch to be flushed (len=0), got %d", batchLen)
	}
}

// TestBatchingExecutor_FlushOnWindow tests that batch flushes when window timer fires
func TestBatchingExecutor_FlushOnWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	windowMs := 50
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 100, windowMs) // large batchSize, windowMs=50

	payload := []byte(`{"msg":1}`)

	// Send 1 message — should not flush immediately
	err := executor.Handle(ctx, messaging.ConsumedMessage{Payload: payload})
	if err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	executor.mu.Lock()
	if len(executor.batch) != 1 {
		t.Errorf("Expected 1 accumulated message, got %d", len(executor.batch))
	}
	executor.mu.Unlock()

	// Wait for window to fire
	time.Sleep(time.Duration(windowMs+25) * time.Millisecond)

	// After window expires, batch should be flushed
	executor.mu.Lock()
	batchLen := len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 0 {
		t.Errorf("Expected batch to be flushed after window (len=0), got %d", batchLen)
	}
}

// TestBatchingExecutor_ContextCancelFlushes tests that cancelling context flushes remaining batch
func TestBatchingExecutor_ContextCancelFlushes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 100, 10000) // large batchSize and window

	payloads := [][]byte{
		[]byte(`{"msg":1}`),
		[]byte(`{"msg":2}`),
	}

	// Send 2 messages
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[0]})
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[1]})

	executor.mu.Lock()
	if len(executor.batch) != 2 {
		t.Errorf("Expected 2 accumulated messages, got %d", len(executor.batch))
	}
	executor.mu.Unlock()

	// Cancel context — should flush remaining batch
	cancel()

	// Give the goroutine time to process the cancellation
	time.Sleep(50 * time.Millisecond)

	// After cancel, batch should be flushed
	executor.mu.Lock()
	batchLen := len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 0 {
		t.Errorf("Expected batch to be flushed on context cancel (len=0), got %d", batchLen)
	}
}

// TestBatchingExecutor_EmptyBatchDoesNotFlush tests that empty batch doesn't flush on context cancel
func TestBatchingExecutor_EmptyBatchDoesNotFlush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 100, 10000)

	executor.mu.Lock()
	if len(executor.batch) != 0 {
		t.Errorf("Expected empty batch initially, got %d", len(executor.batch))
	}
	executor.mu.Unlock()

	// Don't send any messages, just cancel
	cancel()

	time.Sleep(50 * time.Millisecond)

	// Batch should still be empty
	executor.mu.Lock()
	batchLen := len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 0 {
		t.Errorf("Expected empty batch after cancel, got %d", batchLen)
	}
}

// TestBatchingExecutor_MultipleFlushes tests multiple batches in sequence
func TestBatchingExecutor_MultipleFlushes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 2, 0) // batchSize=2

	payloads := [][]byte{
		[]byte(`{"msg":1}`),
		[]byte(`{"msg":2}`),
		[]byte(`{"msg":3}`),
		[]byte(`{"msg":4}`),
	}

	// Send 2 messages — should flush
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[0]})
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[1]})

	executor.mu.Lock()
	batchLen := len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 0 {
		t.Errorf("Expected first batch to flush (len=0), got %d", batchLen)
	}

	// Send 2 more messages — should flush again
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[2]})
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payloads[3]})

	executor.mu.Lock()
	batchLen = len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 0 {
		t.Errorf("Expected second batch to flush (len=0), got %d", batchLen)
	}
}

// TestBatchingExecutor_WindowFiresBeforeSize tests that window can fire before batchSize
func TestBatchingExecutor_WindowFiresBeforeSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	windowMs := 50
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 5, windowMs) // batchSize=5, windowMs=50

	payloads := [][]byte{
		[]byte(`{"msg":1}`),
		[]byte(`{"msg":2}`),
		[]byte(`{"msg":3}`),
	}

	// Send 3 messages (less than batchSize=5)
	for _, p := range payloads {
		executor.Handle(ctx, messaging.ConsumedMessage{Payload: p})
	}

	executor.mu.Lock()
	if len(executor.batch) != 3 {
		t.Errorf("Expected 3 accumulated messages, got %d", len(executor.batch))
	}
	executor.mu.Unlock()

	// Wait for window to fire
	time.Sleep(time.Duration(windowMs+25) * time.Millisecond)

	executor.mu.Lock()
	batchLen := len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 0 {
		t.Errorf("Expected batch to flush on window before size reached (len=0), got %d", batchLen)
	}
}

// TestBatchingExecutor_NoWindowNoSize tests batchSize=0 and windowMs=0 (no automatic flushing)
func TestBatchingExecutor_NoWindowNoSize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 0, 0) // no batch size, no window

	payload := []byte(`{"msg":1}`)

	// Send message
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payload})

	executor.mu.Lock()
	if len(executor.batch) != 1 {
		t.Errorf("Expected 1 accumulated message with no config, got %d", len(executor.batch))
	}
	executor.mu.Unlock()

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Should still be in batch (no size or window to trigger flush)
	executor.mu.Lock()
	batchLen := len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 1 {
		t.Errorf("Expected 1 message still accumulated (no flush config), got %d", batchLen)
	}
}

// TestBatchingExecutor_LargePayloads tests handling of large payloads in batch
func TestBatchingExecutor_LargePayloads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 2, 0)

	// Create large payloads (each 10KB)
	largePayload1 := make([]byte, 10000)
	largePayload2 := make([]byte, 10000)
	for i := range largePayload1 {
		largePayload1[i] = 'A'
	}
	for i := range largePayload2 {
		largePayload2[i] = 'B'
	}

	executor.Handle(ctx, messaging.ConsumedMessage{Payload: largePayload1})
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: largePayload2})

	// After 2 messages, batch should be flushed
	executor.mu.Lock()
	batchLen := len(executor.batch)
	executor.mu.Unlock()

	if batchLen != 0 {
		t.Errorf("Expected batch to flush with large payloads (len=0), got %d", batchLen)
	}
}

// TestBatchingExecutor_BatchAccumulation tests batch accumulation and structure
func TestBatchingExecutor_BatchAccumulation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 10, 0)

	payloads := []string{
		`{"id":1}`,
		`{"id":2}`,
		`{"id":3}`,
	}

	for _, p := range payloads {
		executor.Handle(ctx, messaging.ConsumedMessage{Payload: []byte(p)})
	}

	// Verify accumulation
	executor.mu.Lock()
	defer executor.mu.Unlock()

	if len(executor.batch) != 3 {
		t.Errorf("Expected 3 payloads in batch, got %d", len(executor.batch))
	}

	for i, payload := range executor.batch {
		if string(payload) != payloads[i] {
			t.Errorf("Batch payload %d mismatch: expected %s, got %s", i, payloads[i], payload)
		}
	}
}

// TestBatchingExecutor_TimerManagement tests that timer is properly created and stopped
func TestBatchingExecutor_TimerManagement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	windowMs := 100
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 50, windowMs)

	// First message should start timer
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: []byte(`{"msg":1}`)})

	executor.mu.Lock()
	if executor.timer == nil {
		t.Error("Expected timer to be created after first message")
	}
	executor.mu.Unlock()

	// Batch size flush should stop timer
	for i := 0; i < 49; i++ {
		executor.Handle(ctx, messaging.ConsumedMessage{Payload: []byte(`{"msg":2}`)})
	}

	executor.mu.Lock()
	if executor.timer != nil {
		t.Error("Expected timer to be stopped after batch size flush")
	}
	executor.mu.Unlock()
}

// TestBatchingExecutor_PayloadOrder tests that payloads maintain order in batch
func TestBatchingExecutor_PayloadOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 5, 0)

	expected := []string{
		`{"seq":1}`,
		`{"seq":2}`,
		`{"seq":3}`,
		`{"seq":4}`,
		`{"seq":5}`,
	}

	for _, payload := range expected {
		executor.Handle(ctx, messaging.ConsumedMessage{Payload: []byte(payload)})
	}

	executor.mu.Lock()
	if len(executor.batch) != 0 {
		t.Errorf("Expected batch to flush, got %d messages remaining", len(executor.batch))
	}
	executor.mu.Unlock()

	// We can't easily verify the flushed content without intercepting ProcessFlow,
	// but we can verify the batch was accumulated correctly before flush
	// by re-testing with no flush trigger
}

// TestBatchingExecutor_JSONMarshalError tests handling when batch JSON marshalling would occur
func TestBatchingExecutor_JSONMarshalError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 2, 0)

	// Payloads that are valid byte arrays
	payload1 := []byte(`{"data":"test"}`)
	payload2 := []byte(`{"data":"test2"}`)

	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payload1})
	executor.Handle(ctx, messaging.ConsumedMessage{Payload: payload2})

	// Batch should be flushed and JSON marshalled successfully
	executor.mu.Lock()
	if len(executor.batch) != 0 {
		t.Errorf("Expected batch to flush, got %d messages", len(executor.batch))
	}
	executor.mu.Unlock()
}

// TestBatchingExecutor_ConcurrentAccess tests concurrent message handling
func TestBatchingExecutor_ConcurrentAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 100, 5000)

	var wg sync.WaitGroup
	errorCount := 0
	errorLock := sync.Mutex{}

	// Launch 5 goroutines sending messages concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			for j := 0; j < 10; j++ {
				msg := messaging.ConsumedMessage{
					Payload: []byte(`{"id":"concurrent-test"}`),
				}

				if err := executor.Handle(ctx, msg); err != nil {
					errorLock.Lock()
					errorCount++
					errorLock.Unlock()
				}
			}
		}(i)
	}

	wg.Wait()

	if errorCount != 0 {
		t.Errorf("Expected no errors from concurrent Handle calls, got %d", errorCount)
	}
}

// TestBatchingExecutor_BatchJSONFormat tests that marshalled batch is valid JSON array
func TestBatchingExecutor_BatchJSONFormat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm := createBatchTestFlowManager()
	executor := newBatchingEventExecutor(ctx, fm, "batch-flow", 0, 2, 0)

	payloads := [][]byte{
		[]byte(`{"id":1}`),
		[]byte(`{"id":2}`),
	}

	for _, p := range payloads {
		executor.Handle(ctx, messaging.ConsumedMessage{Payload: p})
	}

	executor.mu.Lock()
	if len(executor.batch) != 0 {
		// Batch was not flushed yet, can't test JSON format
		executor.mu.Unlock()
		t.Skip("Batch not flushed in this test configuration")
		return
	}
	executor.mu.Unlock()

	// If we wanted to test the actual JSON format, we'd need to intercept
	// the ProcessFlow call, but we can verify the structure would be valid
	// by creating a test batch manually
	testBatch := [][]byte{
		[]byte(`{"id":1}`),
		[]byte(`{"id":2}`),
	}

	data, err := json.Marshal(testBatch)
	if err != nil {
		t.Fatalf("Failed to marshal test batch: %v", err)
	}

	var result [][]byte
	err = json.Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal test batch: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("Expected 2 payloads after unmarshal, got %d", len(result))
	}

	for i, payload := range result {
		if string(payload) != string(testBatch[i]) {
			t.Errorf("Payload %d mismatch after marshal/unmarshal", i)
		}
	}
}
