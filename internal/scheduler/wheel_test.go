package scheduler

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// makeEvent is a helper that creates an event with the current epoch.
func makeEvent(name string) *ScheduledEvent {
	now := time.Now()
	ev := globalEventPool.Get()
	ev.Name = name
	ev.ScheduledAt = now
	ev.Epoch = uint32(now.Unix() / 3600)
	return ev
}

// TestWheelScheduleAndTick tests basic scheduling and tick dispatch.
func TestWheelScheduleAndTick(t *testing.T) {
	dispatch := NewDispatchRing(64)
	w := NewSchedulerWheel(dispatch)

	ev := makeEvent("test-event")
	if !w.Schedule(ev) {
		t.Fatal("Schedule() returned false, expected true")
	}

	slotIdx := uint32(ev.ScheduledAt.Unix()) % 3600
	w.tick(slotIdx)

	popped := dispatch.TryPop()
	if popped == nil {
		t.Fatal("TryPop() returned nil, expected event")
		return
	}
	if popped.Name != "test-event" {
		t.Errorf("popped.Name = %q, want %q", popped.Name, "test-event")
	}

	if dispatch.TryPop() != nil {
		t.Fatal("second TryPop() returned non-nil, expected empty ring")
	}

	globalEventPool.Put(popped)
}

// TestWheelEpochGuard tests that events with stale epochs are dropped.
func TestWheelEpochGuard(t *testing.T) {
	dispatch := NewDispatchRing(64)
	w := NewSchedulerWheel(dispatch)

	now := time.Now()
	ev := globalEventPool.Get()
	ev.Name = "stale-event"
	ev.ScheduledAt = now
	ev.Epoch = 0 // deliberately stale epoch

	if !w.Schedule(ev) {
		t.Fatal("Schedule() returned false, expected true")
	}

	slotIdx := uint32(now.Unix()) % 3600
	w.tick(slotIdx)

	if dispatch.TryPop() != nil {
		t.Fatal("TryPop() returned non-nil for stale epoch, expected nil (dropped)")
	}
}

// TestWheelSlotIsolation tests that ticking one slot does not affect other slots.
func TestWheelSlotIsolation(t *testing.T) {
	dispatch := NewDispatchRing(64)
	w := NewSchedulerWheel(dispatch)

	now := time.Now()
	currentSlot := uint32(now.Unix()) % 3600
	otherSlot := (currentSlot + 1) % 3600

	ev := makeEvent("isolated-event")
	if !w.Schedule(ev) {
		t.Fatal("Schedule() returned false, expected true")
	}

	// Tick the other slot (not the one the event is in)
	w.tick(otherSlot)

	// Event should still not be in dispatch
	if dispatch.TryPop() != nil {
		t.Fatal("TryPop() returned non-nil, expected nil (wrong slot ticked)")
	}

	// Tick the correct slot
	w.tick(currentSlot)

	popped := dispatch.TryPop()
	if popped == nil {
		t.Fatal("TryPop() returned nil after correct tick, expected event")
		return
	}
	if popped.Name != "isolated-event" {
		t.Errorf("popped.Name = %q, want %q", popped.Name, "isolated-event")
	}

	globalEventPool.Put(popped)
}

// TestWheelRingFull tests that scheduling more than wheelSlotCap events returns false.
func TestWheelRingFull(t *testing.T) {
	dispatch := NewDispatchRing(256)
	w := NewSchedulerWheel(dispatch)

	// Use the start of the current hour as the scheduled time.
	// This ensures Unix() % 3600 == 0, mapping to slot 0.
	now := time.Now()
	hourStart := time.Unix((now.Unix()/3600)*3600, 0)
	currentEpoch := uint32(now.Unix() / 3600)

	// Schedule wheelSlotCap (64) events into slot 0.
	scheduled := 0
	for i := 0; i < wheelSlotCap; i++ {
		ev := globalEventPool.Get()
		ev.Name = fmt.Sprintf("event-%d", i)
		ev.ScheduledAt = hourStart
		ev.Epoch = currentEpoch
		if w.Schedule(ev) {
			scheduled++
		} else {
			t.Fatalf("Schedule() returned false at event %d, expected all 64 to succeed", i)
			globalEventPool.Put(ev)
		}
	}

	if scheduled != wheelSlotCap {
		t.Errorf("scheduled %d events, expected %d", scheduled, wheelSlotCap)
	}

	// The 65th event should fail (ring full).
	ev65 := globalEventPool.Get()
	ev65.Name = "event-65"
	ev65.ScheduledAt = hourStart
	ev65.Epoch = currentEpoch
	if w.Schedule(ev65) {
		t.Fatal("Schedule() returned true for 65th event, expected false (ring full)")
	}

	globalEventPool.Put(ev65)
}

// TestNoAllocOnTick verifies that tick() makes zero allocations on empty slot.
func TestNoAllocOnTick(t *testing.T) {
	dispatch := NewDispatchRing(64)
	w := NewSchedulerWheel(dispatch)

	emptySlot := uint32(1) // slot 1, no events scheduled

	allocs := testing.AllocsPerRun(100, func() {
		w.tick(emptySlot)
	})

	if allocs > 0 {
		t.Errorf("tick() on empty slot: got %.0f allocs, want 0", allocs)
	}
}

// TestWheelConcurrentSchedule verifies that concurrent producers can schedule
// into the same slot without data races (run with -race flag).
func TestWheelConcurrentSchedule(t *testing.T) {
	dispatch := NewDispatchRing(512)
	w := NewSchedulerWheel(dispatch)

	now := time.Now()
	slot := uint32(now.Unix()) % 3600
	epoch := uint32(now.Unix() / 3600)

	// 10 goroutines each scheduling 4 events = 40 events (within wheelSlotCap=64)
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				ev := globalEventPool.Get()
				ev.Name = fmt.Sprintf("event-%d-%d", id, i)
				ev.ScheduledAt = now
				ev.Epoch = epoch
				w.Schedule(ev)
			}
		}(g)
	}
	wg.Wait()

	// Drain and count dispatched events
	w.tick(slot)
	count := 0
	for dispatch.TryPop() != nil {
		count++
	}

	if count != 40 {
		t.Errorf("expected 40 dispatched events, got %d", count)
	}
}
