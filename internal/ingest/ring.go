package ingest

import (
	"runtime"
	"sync/atomic"
)

// paddedCounter is an atomic counter padded to one CPU cache line (64 bytes)
// to eliminate false-sharing between the enqueue and dequeue positions.
type paddedCounter struct {
	atomic.Uint64
	_ [56]byte // 8 (Uint64 internal) + 56 = 64 bytes
}

// ringSlot is one cell in the ring.  The sequence number is stored atomically;
// the event payload is written under the sequence-number protocol so no mutex
// is required.
type ringSlot struct {
	seq   atomic.Uint64
	event Event
}

// Ring is a lock-free MPMC bounded queue implemented with Vyukov's
// sequence-number algorithm.  TryPush and Pop are both non-blocking: they
// return immediately if the ring is full/empty instead of parking the caller.
//
// All operations use only atomic loads and a single CompareAndSwap — no mutex
// is taken on any path.
type Ring struct {
	enqPos paddedCounter
	deqPos paddedCounter
	mask   uint64
	slots  []ringSlot
}

// NewRing creates a ring with capacity rounded up to the nearest power of two.
func NewRing(capacity int) *Ring {
	sz := nextPow2(uint64(capacity))
	sz = max(sz, 2)
	slots := make([]ringSlot, sz)
	for i := range slots {
		slots[i].seq.Store(uint64(i))
	}
	return &Ring{mask: sz - 1, slots: slots}
}

// TryPush enqueues e.  Returns true on success, false if the ring is full.
// Multiple producers may call TryPush concurrently without any external lock.
func (r *Ring) TryPush(e Event) bool {
	for {
		pos := r.enqPos.Load()
		slot := &r.slots[pos&r.mask]
		seq := slot.seq.Load()
		diff := int64(seq) - int64(pos)
		switch {
		case diff == 0:
			// Slot is ready for this position; try to claim it.
			if r.enqPos.CompareAndSwap(pos, pos+1) {
				slot.event = e
				// Publish: advance seq so consumers can see the item.
				slot.seq.Store(pos + 1)
				return true
			}
			// Another producer claimed it; retry.
		case diff < 0:
			// Ring is full — this slot still holds an unconsumed item.
			return false
		default:
			// diff > 0: producer just ahead of us; give the scheduler a hint
			// and retry (rare contention path).
			runtime.Gosched()
		}
	}
}

// Pop dequeues an event.  Returns (event, true) on success, (zero, false) if empty.
// Multiple consumers may call Pop concurrently without any external lock.
func (r *Ring) Pop() (Event, bool) {
	for {
		pos := r.deqPos.Load()
		slot := &r.slots[pos&r.mask]
		seq := slot.seq.Load()
		diff := int64(seq) - int64(pos+1)
		switch {
		case diff == 0:
			// Item is ready; try to claim this dequeue position.
			if r.deqPos.CompareAndSwap(pos, pos+1) {
				e := slot.event
				// Recycle slot: advance seq by the ring size so the next
				// wrap-around enqueue can use it.
				slot.seq.Store(pos + r.mask + 1)
				return e, true
			}
			// Another consumer claimed it; retry.
		case diff < 0:
			// Ring is empty — no item at this position yet.
			return Event{}, false
		default:
			// diff > 0: consumer just ahead; yield and retry.
			runtime.Gosched()
		}
	}
}

// Len returns an approximate fill level.  It is only accurate in the absence
// of concurrent mutations; use for diagnostics only.
func (r *Ring) Len() int {
	enq := r.enqPos.Load()
	deq := r.deqPos.Load()
	if enq > deq {
		return int(enq - deq)
	}
	return 0
}

// nextPow2 rounds n up to the next power of two.
func nextPow2(n uint64) uint64 {
	if n == 0 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	n++
	return n
}
