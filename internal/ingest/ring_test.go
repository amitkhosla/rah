package ingest

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestRingPushPopSingleProducer(t *testing.T) {
	ring := NewRing(16)
	n := 10

	for i := 0; i < n; i++ {
		e := Event{Kind: EventKind(string(rune('A' + rune(i))))}
		if !ring.TryPush(e) {
			t.Fatalf("TryPush failed at iteration %d", i)
		}
	}

	for i := 0; i < n; i++ {
		e, ok := ring.Pop()
		if !ok {
			t.Fatalf("Pop failed at iteration %d", i)
		}
		expected := EventKind(string(rune('A' + rune(i))))
		if e.Kind != expected {
			t.Errorf("iteration %d: got kind %q, want %q", i, e.Kind, expected)
		}
	}

	if ring.Len() != 0 {
		t.Errorf("ring not empty after consuming all items; Len()=%d", ring.Len())
	}
}

func TestRingFullReturnsFalse(t *testing.T) {
	ring := NewRing(4)

	for i := 0; i < 4; i++ {
		e := Event{Kind: EventKind("fill")}
		if !ring.TryPush(e) {
			t.Fatalf("TryPush failed on iteration %d (should have space)", i)
		}
	}

	e := Event{Kind: EventKind("overflow")}
	if ring.TryPush(e) {
		t.Error("TryPush returned true on full ring, want false")
	}
}

func TestRingEmptyReturnsFalse(t *testing.T) {
	ring := NewRing(8)
	e, ok := ring.Pop()
	if ok {
		t.Error("Pop from empty ring returned true, want false")
	}
	if e != (Event{}) {
		t.Errorf("Pop from empty ring returned non-zero event %v", e)
	}
}

func TestRingWrapAround(t *testing.T) {
	ring := NewRing(8)
	capacity := 8

	for batch := 0; batch < 3; batch++ {
		for i := 0; i < capacity; i++ {
			e := Event{
				Kind:  EventKind("batch" + string(rune('0'+rune(batch)))),
				APIID: uint32(i),
			}
			if !ring.TryPush(e) {
				t.Fatalf("batch %d iteration %d: TryPush failed", batch, i)
			}
		}

		for i := 0; i < capacity; i++ {
			e, ok := ring.Pop()
			if !ok {
				t.Fatalf("batch %d iteration %d: Pop failed", batch, i)
			}
			expected := EventKind("batch" + string(rune('0'+rune(batch))))
			if e.Kind != expected {
				t.Errorf("batch %d iteration %d: got kind %q, want %q", batch, i, e.Kind, expected)
			}
			if e.APIID != uint32(i) {
				t.Errorf("batch %d iteration %d: got APIID %d, want %d", batch, i, e.APIID, i)
			}
		}

		if ring.Len() != 0 {
			t.Errorf("batch %d: ring not empty after consuming; Len()=%d", batch, ring.Len())
		}
	}
}

func TestRingConcurrentMPMC(t *testing.T) {
	t.Parallel()
	ring := NewRing(1024)
	numProducers := 4
	numConsumers := 4
	eventsPerProducer := 10000 / numProducers
	totalEvents := numProducers * eventsPerProducer

	var produced, consumed atomic.Int64

	var wg sync.WaitGroup

	for p := 0; p < numProducers; p++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()
			for i := 0; i < eventsPerProducer; i++ {
				e := Event{
					Kind:     EventKind("concurrent"),
					APIID:    uint32(producerID*eventsPerProducer + i),
					TenantID: uint16(producerID),
				}
				for !ring.TryPush(e) {
					// Retry on full ring
				}
				produced.Add(1)
			}
		}(p)
	}

	for c := 0; c < numConsumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for consumed.Load() < int64(totalEvents) {
				if _, ok := ring.Pop(); ok {
					consumed.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	if produced.Load() != int64(totalEvents) {
		t.Errorf("produced count wrong: got %d, want %d", produced.Load(), totalEvents)
	}
	if consumed.Load() != int64(totalEvents) {
		t.Errorf("consumed count wrong: got %d, want %d", consumed.Load(), totalEvents)
	}
}

func TestRingLenApproximate(t *testing.T) {
	ring := NewRing(16)

	if ring.Len() != 0 {
		t.Errorf("empty ring Len() = %d, want 0", ring.Len())
	}

	for i := 0; i < 5; i++ {
		e := Event{Kind: EventKind("test")}
		ring.TryPush(e)
	}

	len := ring.Len()
	if len <= 0 {
		t.Errorf("after pushing 5 items, Len() = %d, want > 0", len)
	}

	ring.Pop()
	len = ring.Len()
	if len < 4 {
		t.Errorf("after popping 1 of 5, Len() = %d, want >= 4", len)
	}

	for i := 0; i < 4; i++ {
		ring.Pop()
	}

	if ring.Len() != 0 {
		t.Errorf("after consuming all, Len() = %d, want 0", ring.Len())
	}
}
