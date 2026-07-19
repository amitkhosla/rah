package gatewaylog

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestRingEnqueueDequeue verifies basic enqueue/dequeue operations in a single goroutine.
func TestRingEnqueueDequeue(t *testing.T) {
	r := NewRing()

	// Enqueue 100 items
	for i := 0; i < 100; i++ {
		msg := &LogBuf{b: []byte("test")}
		if !r.TryEnqueue(msg) {
			t.Fatalf("Failed to enqueue item %d", i)
		}
	}

	// Dequeue all items and verify order
	for i := 0; i < 100; i++ {
		msg := r.Dequeue()
		if msg == nil {
			t.Fatalf("Failed to dequeue item %d, got nil", i)
		}
		if string(msg.b) != "test" {
			t.Errorf("Item %d: expected 'test', got %s", i, string(msg.b))
		}
	}

	// Ring should be empty now
	if msg := r.Dequeue(); msg != nil {
		t.Error("Ring should be empty, but got a message")
	}
}

// TestRingFull verifies that TryEnqueue returns false when ring is full.
func TestRingFull(t *testing.T) {
	r := NewRing()

	// Fill the ring to capacity
	for i := 0; i < RingCap; i++ {
		msg := &LogBuf{b: []byte("full")}
		if !r.TryEnqueue(msg) {
			t.Fatalf("Failed to enqueue item %d (capacity %d)", i, RingCap)
		}
	}

	// Next enqueue should fail (ring is full)
	msg := &LogBuf{b: []byte("overflow")}
	if r.TryEnqueue(msg) {
		t.Error("TryEnqueue should return false when ring is full")
	}

	// Dequeue one item to make space
	dequeued := r.Dequeue()
	if dequeued == nil {
		t.Error("Failed to dequeue when ring was full")
	}

	// Now we should be able to enqueue again
	if !r.TryEnqueue(msg) {
		t.Error("TryEnqueue should succeed after dequeuing one item")
	}
}

// TestRingWrapAround verifies that sequence number recycling works correctly.
// This tests multiple rounds of enqueue/dequeue to ensure wrap-around doesn't break.
func TestRingWrapAround(t *testing.T) {
	r := NewRing()

	for round := 0; round < 5; round++ {
		// Enqueue 100 items
		for i := 0; i < 100; i++ {
			msg := &LogBuf{b: []byte("wrap")}
			if !r.TryEnqueue(msg) {
				t.Fatalf("Round %d: failed to enqueue item %d", round, i)
			}
		}

		// Dequeue all 100 items
		for i := 0; i < 100; i++ {
			msg := r.Dequeue()
			if msg == nil {
				t.Fatalf("Round %d: failed to dequeue item %d", round, i)
			}
			if string(msg.b) != "wrap" {
				t.Errorf("Round %d item %d: expected 'wrap', got %s", round, i, string(msg.b))
			}
		}

		// Ring should be empty
		if msg := r.Dequeue(); msg != nil {
			t.Errorf("Round %d: ring should be empty after full cycle", round)
		}
	}
}

// TestRingMPSC verifies concurrent multi-producer access with a single consumer.
// Uses -race flag to detect data races.
func TestRingMPSC(t *testing.T) {
	r := NewRing()
	numProducers := 64
	itemsPerProducer := 200
	totalItems := numProducers * itemsPerProducer

	var produceWg sync.WaitGroup
	var dequeuedCount atomic.Uint64
	var droppedCount atomic.Uint64
	var stopped atomic.Bool

	// Single consumer goroutine — Dequeue must never be called from two goroutines
	// concurrently; r.tail is a plain uint64 (correct for MPSC, not safe for MPMC).
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for !stopped.Load() {
			if msg := r.Dequeue(); msg != nil {
				if string(msg.b) != "mpsc" {
					t.Errorf("MPSC: expected 'mpsc', got %s", string(msg.b))
				}
				dequeuedCount.Add(1)
			}
		}
		// Final drain: consume anything enqueued between the last loop iteration
		// and stopped being set.
		for {
			msg := r.Dequeue()
			if msg == nil {
				return
			}
			if string(msg.b) != "mpsc" {
				t.Errorf("MPSC: expected 'mpsc', got %s", string(msg.b))
			}
			dequeuedCount.Add(1)
		}
	}()

	// Producer goroutines
	produceWg.Add(numProducers)
	for p := 0; p < numProducers; p++ {
		go func() {
			defer produceWg.Done()
			for i := 0; i < itemsPerProducer; i++ {
				msg := &LogBuf{b: []byte("mpsc")}
				if !r.TryEnqueue(msg) {
					droppedCount.Add(1)
				}
			}
		}()
	}

	// Wait for all producers, then stop and drain the consumer.
	produceWg.Wait()
	stopped.Store(true)
	<-consumerDone

	dequeued := dequeuedCount.Load()
	dropped := droppedCount.Load()
	received := dequeued + dropped

	if received != uint64(totalItems) {
		t.Errorf("MPSC: sent %d items, received %d (dequeued %d + dropped %d)",
			totalItems, received, dequeued, dropped)
	}
	if dequeued == 0 {
		t.Error("MPSC: no items were dequeued")
	}
	if dequeued > uint64(totalItems) {
		t.Errorf("MPSC: dequeued %d items but only sent %d", dequeued, totalItems)
	}
}

// TestRingDequeueEmpty verifies that Dequeue returns nil immediately on an empty ring.
func TestRingDequeueEmpty(t *testing.T) {
	r := NewRing()

	// Try dequeueing from an empty ring multiple times
	for i := 0; i < 10; i++ {
		msg := r.Dequeue()
		if msg != nil {
			t.Errorf("Dequeue %d: expected nil from empty ring, got %v", i, msg)
		}
	}
}

// TestRingConcurrentProducers verifies that multiple producers can enqueue concurrently
// without panicking or creating data races.
func TestRingConcurrentProducers(t *testing.T) {
	r := NewRing()
	numProducers := 32
	itemsPerProducer := 100

	var wg sync.WaitGroup
	wg.Add(numProducers)

	for p := 0; p < numProducers; p++ {
		go func(producerID int) {
			defer wg.Done()
			for i := 0; i < itemsPerProducer; i++ {
				msg := &LogBuf{b: []byte("concurrent")}
				r.TryEnqueue(msg) // Ignore full errors for this test
			}
		}(p)
	}

	wg.Wait()

	// Verify we can dequeue all successfully enqueued items
	count := 0
	for count < numProducers*itemsPerProducer {
		msg := r.Dequeue()
		if msg == nil {
			break
		}
		if string(msg.b) != "concurrent" {
			t.Errorf("Expected 'concurrent', got %s", string(msg.b))
		}
		count++
	}

	if count == 0 {
		t.Error("Failed to dequeue any items in concurrent producer test")
	}
}

// TestRingSequenceInitialization verifies that slots are properly initialized.
func TestRingSequenceInitialization(t *testing.T) {
	r := NewRing()

	// Verify that all slots have their sequence numbers initialized to their index
	for i := 0; i < RingCap; i++ {
		expected := uint64(i)
		actual := r.slots[i].seq.Load()
		if actual != expected {
			t.Errorf("Slot %d: expected seq=%d, got seq=%d", i, expected, actual)
		}
	}
}
