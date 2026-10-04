package scheduler

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// paddedU32 is an atomic Uint32 padded to one CPU cache line (64 bytes).
// Prevents false sharing between adjacent head/tail counters.
type paddedU32 struct {
	atomic.Uint32
	_ [60]byte // 4 (Uint32 internal uint32) + 60 = 64
}

const wheelSlotCap = 64 // must be power of two; max events per second-slot

// wheelCell is one entry in a slot's MPSC ring.
// The seq field is the Vyukov sequence number; epoch and ptr are written under
// the seq protocol so no mutex is required.
// Padded to 64 bytes to prevent false sharing between adjacent cells.
type wheelCell struct {
	seq   atomic.Uint32   // Vyukov sequence number
	epoch uint32          // hour epoch written by producer; read by consumer after seq check
	ptr   *ScheduledEvent // event pointer written by producer; read by consumer after seq check
	_     [48]byte        // 4 (seq) + 4 (epoch) + 8 (ptr) + 48 = 64
}

// wheelSlot is the MPSC ring for one second-aligned slot.
// head and tail are on separate cache lines to eliminate producer/consumer false sharing.
type wheelSlot struct {
	head  paddedU32 // incremented by producers (Schedule)
	tail  paddedU32 // incremented by consumer (tick)
	cells [wheelSlotCap]wheelCell
}

// SchedulerWheel is a hashed timing wheel for scheduled task dispatch.
// 3600 slots × 1 second = 1 hour coverage at 1-second resolution.
type SchedulerWheel struct {
	slots    [3600]wheelSlot
	dispatch *DispatchRing
}

// NewSchedulerWheel creates a new timing wheel that pushes fired events into dispatch.
func NewSchedulerWheel(dispatch *DispatchRing) *SchedulerWheel {
	w := &SchedulerWheel{dispatch: dispatch}
	for s := range w.slots {
		for i := range w.slots[s].cells {
			w.slots[s].cells[i].seq.Store(uint32(i))
		}
	}
	return w
}

// Start launches the ticker goroutine that advances the wheel every second.
func (w *SchedulerWheel) Start(ctx context.Context) {
	go w.runTicker(ctx)
}

// Schedule arms event in the correct slot using a lock-free MPSC push.
// Returns true on success, false if the slot ring is full.
// Called by the loader and UpsertSchedule (multiple producers).
func (w *SchedulerWheel) Schedule(event *ScheduledEvent) bool {
	slot := &w.slots[uint32(event.ScheduledAt.Unix())%3600]
	for {
		pos := slot.head.Load()
		idx := pos & (wheelSlotCap - 1)
		cell := &slot.cells[idx]
		seq := cell.seq.Load()
		diff := int32(seq) - int32(pos)
		switch {
		case diff == 0:
			if slot.head.CompareAndSwap(pos, pos+1) {
				cell.epoch = event.Epoch
				cell.ptr = event
				cell.seq.Store(pos + 1) // publish to consumer
				return true
			}
		case diff < 0:
			return false // ring full
		default:
			runtime.Gosched()
		}
	}
}

// runTicker advances the wheel every second.
func (w *SchedulerWheel) runTicker(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			var panicMsg string
			switch v := r.(type) {
			case string:
				panicMsg = v
			default:
				panicMsg = "non-string panic"
			}
			gatewaylog.Default.Error("[Scheduler] wheel panic recovered, restarting ticker",
				gatewaylog.F("panic", panicMsg),
			)
			if ctx.Err() == nil {
				go w.runTicker(ctx)
			}
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			w.tick(uint32(t.Unix()) % 3600)
		}
	}
}

// tick drains all events from the given slot and pushes them into the dispatch ring.
// Called only by the ticker goroutine (single consumer) — no CAS on tail.
// Zero allocations on this path.
func (w *SchedulerWheel) tick(slotIdx uint32) {
	slot := &w.slots[slotIdx]
	currentEpoch := uint32(time.Now().Unix() / 3600)
	for {
		pos := slot.tail.Load()
		idx := pos & (wheelSlotCap - 1)
		cell := &slot.cells[idx]
		seq := cell.seq.Load()
		diff := int32(seq) - int32(pos+1)
		switch {
		case diff == 0:
			ev := cell.ptr
			cell.ptr = nil
			slot.tail.Store(pos + 1)           // plain store: single consumer
			cell.seq.Store(pos + wheelSlotCap) // recycle slot for next wrap-around
			if ev.Epoch == currentEpoch {
				if !w.dispatch.TryPush(ev) {
					gatewaylog.Default.Warn("[Scheduler] dispatch ring full, dropping event",
						gatewaylog.F("name", ev.Name),
					)
					globalEventPool.Put(ev)
				}
			} else {
				globalEventPool.Put(ev) // stale: from a previous hour's slot collision
			}
		case diff < 0:
			return // slot is empty
		default:
			runtime.Gosched()
		}
	}
}
