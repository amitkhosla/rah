package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// SchedulerWheel is a hashed timing wheel for scheduled task dispatch.
// 3600 slots × 1 second = 1 hour coverage at 1-second resolution.
// Separate from the engine timer wheel; does not share any state with it.
type SchedulerWheel struct {
	slots   [3600]schedulerSlot
	current atomic.Uint32
	cb      func(*ScheduledEvent) // called when a slot fires; must be non-nil
}

type schedulerSlot struct {
	mu     sync.Mutex
	events []*ScheduledEvent
}

// NewSchedulerWheel creates a new timing wheel with the given callback.
func NewSchedulerWheel(cb func(*ScheduledEvent)) *SchedulerWheel {
	return &SchedulerWheel{
		cb: cb,
	}
}

// Start launches the ticker goroutine that advances the wheel every second.
func (w *SchedulerWheel) Start(ctx context.Context) {
	go w.runTicker(ctx)
}

// Schedule arms an event in the correct slot based on its ScheduledAt time.
func (w *SchedulerWheel) Schedule(event *ScheduledEvent) {
	slot := uint32(event.ScheduledAt.Unix()) % 3600
	w.slots[slot].mu.Lock()
	w.slots[slot].events = append(w.slots[slot].events, event)
	w.slots[slot].mu.Unlock()
}

// runTicker advances the wheel every second.
func (w *SchedulerWheel) runTicker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			sec := uint32(t.Unix())
			slot := sec % 3600
			w.current.Store(slot)
			w.tick(slot)
		}
	}
}

// tick processes all events in the given slot.
func (w *SchedulerWheel) tick(slot uint32) {
	s := &w.slots[slot]
	s.mu.Lock()
	events := s.events
	s.events = nil
	s.mu.Unlock()

	// Call callback for each event outside the mutex to avoid deadlock.
	for _, event := range events {
		w.cb(event)
	}
}
