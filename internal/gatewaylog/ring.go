package gatewaylog

import "sync/atomic"

const (
	// RingCap is the maximum number of slots in the ring buffer.
	// Must be a power of 2 for the mask trick to work.
	RingCap = 1 << 13 // 8192 slots
	ringMask = RingCap - 1
)

// ringSlot represents a single slot in the MPSC ring buffer.
// The seq field is used as a handshake between writers and the single reader.
type ringSlot struct {
	seq atomic.Uint64 // sequence number for CAS-based handshake
	msg *LogBuf       // the log buffer pointer
}

// Ring is a lock-free, wait-free Multi-Producer Single-Consumer (MPSC) ring buffer
// based on the Vyukov algorithm. Writers use CAS on head, the single reader uses tail.
// Head and tail are on separate cache lines to avoid false sharing.
type Ring struct {
	_    [64]byte      // padding: head on its own cache line
	head atomic.Uint64 // claim position for writers (CAS-based)
	_    [56]byte      // padding: head and tail are 64-byte apart
	tail uint64        // consume position for single reader (no atomic needed)
	_    [56]byte      // padding
	slots [RingCap]ringSlot
}

// NewRing creates and initializes a new MPSC ring buffer.
// All slot sequence numbers are initialized to their index so they start as "free".
func NewRing() *Ring {
	r := &Ring{}
	// Initialize all slots with seq = index (they start as free)
	for i := 0; i < RingCap; i++ {
		r.slots[i].seq.Store(uint64(i))
	}
	return r
}

// TryEnqueue attempts to enqueue a log buffer into the ring without blocking.
// Returns true if successful, false if the ring is full.
// Multiple goroutines can call this concurrently.
func (r *Ring) TryEnqueue(msg *LogBuf) bool {
	for {
		// Load current head position
		head := r.head.Load()
		slot := &r.slots[head&ringMask]

		// Check the slot's sequence number to determine its state:
		// diff == 0: slot is free (seq == head), proceed
		// diff < 0: ring is full (seq < head), return false
		// diff > 0: another writer is ahead (seq > head), retry
		seq := slot.seq.Load()
		diff := int64(seq) - int64(head)

		if diff < 0 {
			// Ring is full, non-blocking return
			return false
		}

		if diff > 0 {
			// Another writer is still writing to this position, retry
			continue
		}

		// diff == 0: slot is free, try to claim this position via CAS
		if r.head.CompareAndSwap(head, head+1) {
			// We successfully claimed the slot, write the message and signal
			slot.msg = msg
			slot.seq.Store(head + 1) // Signal: reader may now consume
			return true
		}
		// CAS failed, another writer claimed first, retry
	}
}

// Dequeue removes and returns the next ready log buffer from the ring.
// Returns nil if no buffer is ready. Must only be called from a single goroutine.
// This is the "consumer" side of the MPSC pattern.
func (r *Ring) Dequeue() *LogBuf {
	slot := &r.slots[r.tail&ringMask]

	// Check if the slot is ready for consumption (seq == tail+1)
	if slot.seq.Load() != r.tail+1 {
		// Nothing ready yet
		return nil
	}

	// Slot is ready, extract the message
	msg := slot.msg
	slot.msg = nil // Clear reference to allow GC

	// Recycle the slot for future wrap-around by setting seq = tail + RingCap
	slot.seq.Store(r.tail + uint64(RingCap))

	// Advance tail
	r.tail++

	return msg
}
