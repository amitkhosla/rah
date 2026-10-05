package scheduler

import (
	"runtime"
	"sync/atomic"
)

// paddedU64 is an atomic Uint64 padded to 64 bytes to prevent false sharing.
type paddedU64 struct {
	atomic.Uint64
	_ [56]byte // 8 bytes (Uint64 internal field) + 56 = 64
}

// dispatchCell is one slot in the dispatch ring.
// Padded to 64 bytes to avoid false sharing between adjacent cells.
type dispatchCell struct {
	seq atomic.Uint64   // Vyukov sequence number
	ptr *ScheduledEvent // written before seq is published; safe under seq protocol
	_   [48]byte        // 8 (seq) + 8 (ptr) + 48 = 64
}

// DispatchRing is a lock-free MPMC bounded queue for *ScheduledEvent.
// The timing wheel is the single producer; the worker pool has multiple consumers.
// Capacity must be a power of two.
type DispatchRing struct {
	enqPos  paddedU64
	deqPos  paddedU64
	mask    uint64
	cells   []dispatchCell
	NotifyCh chan struct{}
}

// NewDispatchRing creates a DispatchRing with the given capacity (must be power of two).
// Panics if cap is 0 or not a power of two.
func NewDispatchRing(cap uint64) *DispatchRing {
	if cap == 0 || cap&(cap-1) != 0 {
		panic("dispatch ring capacity must be a power of two")
	}
	cells := make([]dispatchCell, cap)
	for i := range cells {
		cells[i].seq.Store(uint64(i))
	}
	return &DispatchRing{mask: cap - 1, cells: cells, NotifyCh: make(chan struct{}, 1)}
}

// TryPush attempts to push an event into the ring.
// Called by the timing wheel (single producer).
// Returns true if successful, false if the ring is full.
func (r *DispatchRing) TryPush(ev *ScheduledEvent) bool {
	for {
		pos := r.enqPos.Load()
		cell := &r.cells[pos&r.mask]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos)
		switch {
		case diff == 0:
			if r.enqPos.CompareAndSwap(pos, pos+1) {
				cell.ptr = ev
				cell.seq.Store(pos + 1)
				select {
				case r.NotifyCh <- struct{}{}:
				default:
				}
				return true
			}
		case diff < 0:
			return false // ring full
		default:
			runtime.Gosched()
		}
	}
}

// TryPop attempts to pop an event from the ring.
// Called by worker goroutines (multiple consumers).
// Returns the event and true if successful, nil and false if the ring is empty.
func (r *DispatchRing) TryPop() *ScheduledEvent {
	for {
		pos := r.deqPos.Load()
		cell := &r.cells[pos&r.mask]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos+1)
		switch {
		case diff == 0:
			if r.deqPos.CompareAndSwap(pos, pos+1) {
				ev := cell.ptr
				cell.ptr = nil
				cell.seq.Store(pos + r.mask + 1)
				return ev
			}
		case diff < 0:
			return nil // ring empty
		default:
			runtime.Gosched()
		}
	}
}
