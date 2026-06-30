package observability

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// ── InstrBatch ────────────────────────────────────────────────────────────────

// InstrBatch carries the per-instruction timing data accumulated by Execute()
// for a single request. main.go constructs one from ExecutionState fields and
// passes it to InstrSlabRing.write() after Execute() returns.
type InstrBatch struct {
	TimestampNs int64
	APIID       uint32
	VersionID   uint32
	TenantID    uint16
	EndpointID  uint8
	// Traced is 1 when this request should export a full trace. The drain
	// goroutine checks this flag and calls the trace exporter for marked slots.
	Traced uint8
	// Count is the number of valid entries in PCs / DurNs (max 64).
	Count uint8
	_pad  [3]byte //nolint:unused // cache-line alignment padding
	PCs   [64]int16
	DurNs [64]int32
}

// ── InstrSlot ─────────────────────────────────────────────────────────────────

// InstrSlot is a single pre-allocated instruction-timing record.
// Fixed at 512 bytes (8 × 64-byte cache lines) to eliminate false-sharing.
//
// Layout:
//
//	  0 –  27   header: timestamps, IDs, count, flags
//	 28 – 411   data: PCs [64]int16 (128 B) + DurNs [64]int32 (256 B)
//	412 – 415   ready uint32
//	416 – 511   _pad1 [96]byte
type InstrSlot struct {
	// header (28 bytes)
	TimestampNs int64
	APIID       uint32
	VersionID   uint32
	TenantID    uint16
	EndpointID  uint8
	Traced      uint8
	Count       uint8
	_pad0       [7]byte //nolint:unused // cache-line alignment padding

	// per-instruction data (384 bytes)
	PCs  [64]int16
	Durs [64]int32

	// ready flag + padding to 512 bytes
	ready uint32
	_pad1 [96]byte //nolint:unused // pads InstrSlot to 512 bytes; enforced by init()
}

// InstrSlotSizeCheck panics at startup if the struct drifts from 512 bytes.
func init() {
	if sz := unsafe.Sizeof(InstrSlot{}); sz != 512 {
		panic("observability: InstrSlot size changed — update _pad1 to keep it 512 bytes (current: " +
			fmt.Sprintf("%d", sz) + ")")
	}
}

// ── instrSlab ─────────────────────────────────────────────────────────────────

type instrSlab struct {
	cursor int64    // claimed slot count; writers use atomic.AddInt64
	sealed uint32   // 1 when drain is processing; writers must not claim
	_pad   [52]byte //nolint:unused // pads cursor+sealed to 64 bytes (one cache line)

	slots []InstrSlot
}

func newInstrSlab(cap int) *instrSlab {
	return &instrSlab{slots: make([]InstrSlot, cap)}
}

func (s *instrSlab) reset(usedCount int64) {
	for i := int64(0); i < usedCount; i++ {
		atomic.StoreUint32(&s.slots[i].ready, 0)
	}
	atomic.StoreInt64(&s.cursor, 0)
	atomic.StoreUint32(&s.sealed, 0)
}

// ── InstrSlabRing ─────────────────────────────────────────────────────────────

const instrSlabCount = 4

// InstrSlabRing is a lock-free rotating ring of instrSlabs, parallel to
// obsSlabRing but for instruction-timing data instead of access logs.
//
// Write path (hot, called once per request after Execute()):
//
//	atomic.Add(cursor) → field writes → StoreUint32(ready, 1)
//
// Drain path (background goroutine, every instrWindowMs):
//
//	seal active → advance → straggler guard → aggregate counters
type InstrSlabRing struct {
	slabs   [instrSlabCount]*instrSlab
	active  int32 // current active slab index; atomic
	slabCap int
	dropped uint64 // atomic
	stop    chan struct{}
	done    sync.WaitGroup

	// drainFn is called for every ready slot during drain.
	// Swapped from stubDrain to realDrain during S11 wiring.
	drainFn func(slot *InstrSlot)
}

// NewInstrSlabRing creates and starts an InstrSlabRing.
// slabCap is the number of slots per slab; pass obsComputeSlabCap() for
// consistent sizing with the access-log ring.
func NewInstrSlabRing(slabCap int) *InstrSlabRing {
	r := &InstrSlabRing{
		slabCap: slabCap,
		stop:    make(chan struct{}),
		drainFn: stubDrain,
	}
	for i := range r.slabs {
		r.slabs[i] = newInstrSlab(slabCap)
	}
	return r
}

// SetDrainFn replaces the drain handler. Call before Start() or under
// quiescence. The provided fn must be safe to call from a single goroutine.
func (r *InstrSlabRing) SetDrainFn(fn func(slot *InstrSlot)) {
	r.drainFn = fn
}

func (r *InstrSlabRing) Start() {
	r.done.Add(1)
	go r.drain()
}

func (r *InstrSlabRing) StopAndWait() {
	close(r.stop)
	r.done.Wait()
}

// InstrSlabDropped returns the total dropped batch count since creation.
func (r *InstrSlabRing) InstrSlabDropped() uint64 {
	return atomic.LoadUint64(&r.dropped)
}

// Write stamps b into the active slab. Zero allocations; two atomics on hot path.
// InstrBatch is passed by value — no pointer is retained, so the caller's
// context can be recycled immediately after this call returns.
// Only b.Count entries are copied into the slot — not the full 64-element arrays.
func (r *InstrSlabRing) Write(b InstrBatch) {
	slabIdx := atomic.LoadInt32(&r.active)
	slab := r.slabs[slabIdx]

	idx := atomic.AddInt64(&slab.cursor, 1) - 1

	if idx >= int64(r.slabCap) || atomic.LoadUint32(&slab.sealed) != 0 {
		atomic.AddUint64(&r.dropped, 1)
		return
	}

	slot := &slab.slots[idx]

	slot.TimestampNs = b.TimestampNs
	slot.APIID = b.APIID
	slot.VersionID = b.VersionID
	slot.TenantID = b.TenantID
	slot.EndpointID = b.EndpointID
	slot.Traced = b.Traced
	slot.Count = b.Count
	// Copy only the valid entries — not the full 64-element arrays.
	copy(slot.PCs[:b.Count], b.PCs[:b.Count])
	copy(slot.Durs[:b.Count], b.DurNs[:b.Count])

	atomic.StoreUint32(&slot.ready, 1)
}

// drain is the single consumer goroutine.
func (r *InstrSlabRing) drain() {
	defer r.done.Done()

	rotateTick := time.NewTicker(obsWindowMs * time.Millisecond)
	defer rotateTick.Stop()

	for {
		select {
		case <-rotateTick.C:
			r.rotateSlab()
		case <-r.stop:
			r.rotateSlab()
			return
		}
	}
}

func (r *InstrSlabRing) rotateSlab() {
	current := atomic.LoadInt32(&r.active)
	slab := r.slabs[current]

	atomic.StoreUint32(&slab.sealed, 1)

	next := (current + 1) % instrSlabCount
	atomic.StoreInt32(&r.active, next)

	count := atomic.LoadInt64(&slab.cursor)
	if count <= 0 {
		slab.reset(0)
		return
	}
	if count > int64(r.slabCap) {
		count = int64(r.slabCap)
	}

	deadline := time.Now().Add(time.Millisecond)
	for i := int64(0); i < count; i++ {
		slot := &slab.slots[i]
		for atomic.LoadUint32(&slot.ready) == 0 {
			if time.Now().After(deadline) {
				atomic.AddUint64(&r.dropped, 1)
				goto nextSlot
			}
			runtime.Gosched()
		}
		r.drainFn(slot)
	nextSlot:
	}

	slab.reset(count)
}

// stubDrain is the no-op drain function used before the real aggregator is wired.
func stubDrain(_ *InstrSlot) {}

// ComputeSlabCap returns the recommended slab slot capacity for the current host,
// using the same formula as the access-log ring so both rings are sized consistently.
func ComputeSlabCap() int { return obsComputeSlabCap() }
