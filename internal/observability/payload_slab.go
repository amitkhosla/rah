package observability

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	payloadSlabSize  = 4 * 1024 * 1024 // 4 MB per slab
	payloadMaxSlabs  = 32               // 128 MB max
	payloadWarnSlabs = 8                // warn at 8 slabs
)

// ── PayloadRef ────────────────────────────────────────────────────────────────

// PayloadRef is a 16-byte pointer into the payloadSlabRing.
// Store this in a TraceRecord instead of a []byte to avoid keeping HTTP objects alive.
type PayloadRef struct {
	SlabID      uint64
	StartOffset uint32
	Length      uint32
}

// ── payloadSlab ───────────────────────────────────────────────────────────────

// payloadSlab holds 4 MB of raw bytes.
// The first cache line (64 bytes) is the hot writer state;
// the next cache line holds the atomic.Pointer to the next slab.
//
// Layout of the first cache line (64 bytes total):
//
//	cursor  int64   (8 bytes)
//	sealed  uint32  (4 bytes)
//	[4 bytes alignment padding before id]
//	id      uint64  (8 bytes)
//	_pad    [40]byte
type payloadSlab struct {
	// First cache line: hot writer state (must be 64 bytes — verified by init())
	cursor int64    // atomic — byte cursor (bytes claimed so far)
	sealed uint32   // atomic — 1 = full/draining
	id     uint64   // immutable slab ID (monotonically increasing, set before publishing)
	_pad   [40]byte //nolint:unused // pad to 64 bytes (accounts for 4-byte align gap before id)

	next atomic.Pointer[payloadSlab]
	data [payloadSlabSize]byte
}

func init() {
	type hdr struct {
		cursor int64
		sealed uint32
		id     uint64
		_pad   [40]byte
	}
	if sz := unsafe.Sizeof(hdr{}); sz != 64 {
		panic(fmt.Sprintf(
			"observability: payloadSlab header size changed — update _pad to keep 64 bytes (current: %d)", sz))
	}
}

// resetPayloadSlab zeroes the control fields so a recycled slab is ready to use.
// Does NOT zero s.data — that's 4 MB and too expensive; data is only valid in [offset, offset+length).
func resetPayloadSlab(s *payloadSlab) {
	atomic.StoreInt64(&s.cursor, 0)
	atomic.StoreUint32(&s.sealed, 0)
	s.id = 0
	s.next.Store(nil)
}

// ── payloadSlabPool ───────────────────────────────────────────────────────────

type payloadSlabPool struct {
	mu         sync.Mutex
	free       []*payloadSlab
	totalAlloc int
	maxSlabs   int
	warnAt     int
}

// get returns a free slab from the pool or allocates a new one.
// Returns nil when the hard limit has been reached.
func (p *payloadSlabPool) get() *payloadSlab {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.free) > 0 {
		s := p.free[len(p.free)-1]
		p.free = p.free[:len(p.free)-1]
		return s
	}

	if p.totalAlloc >= p.maxSlabs {
		return nil
	}

	p.totalAlloc++
	if p.totalAlloc == p.warnAt {
		// Log to stderr; avoids importing a logger dependency.
		fmt.Printf("observability: payloadSlabPool WARNING: %d slabs allocated (warn threshold)\n", p.warnAt)
	}
	return &payloadSlab{}
}

// put returns a used slab to the pool after resetting its control fields.
func (p *payloadSlabPool) put(s *payloadSlab) {
	resetPayloadSlab(s)
	p.mu.Lock()
	p.free = append(p.free, s)
	p.mu.Unlock()
}

// ── payloadSlabRing ───────────────────────────────────────────────────────────

// payloadSlabRing is an expandable linked-slab ring for variable-size byte payloads.
//
// Write path (hot, per captured payload):
//
//	Claim(size) → get slabID + offset → WriteAt(slabID, offset, data)
//
// Read path (drain / query):
//
//	ReadPayload(ref) → returns a slice into slab.data (caller must copy if needed)
type payloadSlabRing struct {
	head   atomic.Pointer[payloadSlab]
	tail   atomic.Pointer[payloadSlab]
	mu     sync.Mutex
	nextID uint64 // accessed via atomic.AddUint64
	pool   payloadSlabPool
	// dropped counts payloads that could not be claimed due to the hard slab limit.
	dropped uint64 // atomic
}

// newPayloadSlabRing creates a ring with the given slab limits.
// The first slab (id=1) is allocated immediately so Claim can proceed without locking.
func newPayloadSlabRing(maxSlabs, warnAt int) *payloadSlabRing {
	r := &payloadSlabRing{}
	r.pool.maxSlabs = maxSlabs
	r.pool.warnAt = warnAt

	// Allocate first slab with id=1.
	first := r.pool.get()
	if first == nil {
		// maxSlabs == 0 is a misconfiguration; panic early.
		panic("observability: newPayloadSlabRing: maxSlabs must be > 0")
	}
	r.nextID = 1
	first.id = 1

	r.head.Store(first)
	r.tail.Store(first)
	return r
}

// Claim atomically reserves totalSize bytes from the tail slab.
// Returns (slabID, startOffset, true) on success.
// Returns (0, 0, false) when the hard slab limit is exhausted.
//
// The caller should subsequently call WriteAt(slabID, startOffset, data) to
// populate the reserved region.
func (r *payloadSlabRing) Claim(totalSize int64) (slabID uint64, startOffset uint32, ok bool) {
	for {
		slab := r.tail.Load()

		end := atomic.AddInt64(&slab.cursor, totalSize)
		start := end - totalSize

		if end > payloadSlabSize {
			// This slab is full; advance the tail and retry.
			if !r.advanceTail(slab) {
				// Hard limit reached.
				atomic.AddUint64(&r.dropped, 1)
				return 0, 0, false
			}
			// Retry on the new tail.
			continue
		}

		// Successful claim.
		return slab.id, uint32(start), true
	}
}

// WriteAt finds the slab with the given ID and copies p into slab.data[offset:].
// If the slab has already been recycled (head advanced past it), the write is silently dropped.
func (r *payloadSlabRing) WriteAt(slabID uint64, offset uint32, p []byte) {
	slab := r.head.Load()
	for slab != nil {
		if slab.id == slabID {
			copy(slab.data[offset:], p)
			return
		}
		slab = slab.next.Load()
	}
	// Slab not found — it was recycled before the write arrived; silently drop.
}

// ReadPayload returns the raw bytes described by ref.
// The returned slice points directly into slab memory; callers must copy it
// if they need the data to outlive the next drain cycle.
// Returns nil if the slab has already been recycled.
func (r *payloadSlabRing) ReadPayload(ref PayloadRef) []byte {
	slab := r.head.Load()
	for slab != nil {
		if slab.id == ref.SlabID {
			end := ref.StartOffset + ref.Length
			if uint32(len(slab.data)) < end {
				return nil
			}
			return slab.data[ref.StartOffset:end]
		}
		slab = slab.next.Load()
	}
	return nil
}

// Dropped returns the total number of payloads dropped due to the slab limit.
func (r *payloadSlabRing) Dropped() uint64 { return atomic.LoadUint64(&r.dropped) }

// advanceTail appends a new slab and advances tail past the full slab.
// Returns false if the pool hard limit has been reached.
func (r *payloadSlabRing) advanceTail(full *payloadSlab) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Re-check: another goroutine may have already advanced past full.
	if r.tail.Load() != full {
		return true // tail was already moved; caller should retry
	}

	newSlab := r.pool.get()
	if newSlab == nil {
		return false // hard limit
	}

	// Assign a monotonically increasing ID.
	newSlab.id = atomic.AddUint64(&r.nextID, 1)

	// Seal the full slab so no new claims land on it.
	atomic.StoreUint32(&full.sealed, 1)

	// Link and publish new slab.
	full.next.Store(newSlab)
	r.tail.Store(newSlab)

	return true
}

// DrainHead recycles the head slab back to the pool, advancing head to the next slab.
// This is called by the drain goroutine after all references to the head slab's
// data have been processed. Returns false if there is only one slab remaining
// (head == tail) and it should not be recycled.
func (r *payloadSlabRing) DrainHead() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	head := r.head.Load()
	tail := r.tail.Load()
	if head == tail {
		// Never recycle the active tail slab.
		return false
	}

	next := head.next.Load()
	if next == nil {
		return false
	}

	r.head.Store(next)
	r.pool.put(head)
	return true
}
