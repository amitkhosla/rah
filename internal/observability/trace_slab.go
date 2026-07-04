package observability

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// ── TraceSlot ─────────────────────────────────────────────────────────────────
//
// Fixed at 512 bytes (8 × 64-byte cache lines) to eliminate false-sharing.
// Each slot is either a header (isHeader=1) or an instruction (isHeader=0).
//
// Write protocol:
//   - Instruction slots are written FIRST (plain field stores).
//   - Header slot is written LAST; atomic.StoreUint32(&slot.ready, 1) on the
//     header signals that the entire block (header + count instructions) is safe
//     to read.
//
// Layout (byte offsets):
//
//	  0 –   7   common: isHeader, _pad0, ready
//	  8 – 215   header fields (valid when isHeader=1)
//	216 – 263   instruction fields (valid when isHeader=0)
//	264 – 511   _pad4 (padding to 512 bytes)
type TraceSlot struct {
	// --- common (8 bytes) ---
	isHeader uint8
	_pad0    [3]byte //nolint:unused // cache-line alignment padding
	ready    uint32 // atomic: StoreUint32(1) by writer after all fields written

	// --- header fields (isHeader=1) ---
	Count             uint16 // number of instruction slots that follow this header
	Status            uint16 // HTTP status code
	ApiID             uint32
	TraceID           uint64
	TenantID          uint16
	_pad1             [6]byte //nolint:unused // cache-line alignment padding
	DurationNs        int64
	GatewayDurationNs int64
	StartedAtUnixNano int64
	PathLen           uint8
	MethodLen         uint8
	_pad2             [6]byte //nolint:unused // cache-line alignment padding
	Path              [128]byte
	Method            [8]byte

	// --- instruction fields (isHeader=0) ---
	PC      int16
	StepIdx int16
	DurNs   int32
	NameLen uint8
	_pad3   [7]byte  //nolint:unused // cache-line alignment padding
	Name    [32]byte

	// --- padding to 512 bytes ---
	_pad4 [264]byte //nolint:unused // pads TraceSlot to 512 bytes; enforced by init()
}

func init() {
	if sz := unsafe.Sizeof(TraceSlot{}); sz != 512 {
		panic(fmt.Sprintf("observability: TraceSlot size changed — update _pad4 to keep it 512 bytes (current: %d)", sz))
	}
}

// ── traceSlab ─────────────────────────────────────────────────────────────────

type traceSlab struct {
	cursor int64    // claimed slot count; writers use atomic.AddInt64
	sealed uint32   // 1 when drain is processing; writers must not claim
	_pad   [52]byte // pad cursor+sealed to 64 bytes (one cache line)

	slots []TraceSlot
}

func newTraceSlab(cap int) *traceSlab {
	return &traceSlab{slots: make([]TraceSlot, cap)}
}

func (s *traceSlab) reset(usedCount int64) {
	for i := int64(0); i < usedCount; i++ {
		s.slots[i].ready = 0
		s.slots[i].isHeader = 0
	}
	atomic.StoreInt64(&s.cursor, 0)
	atomic.StoreUint32(&s.sealed, 0)
}

// ── TraceSlabRing ─────────────────────────────────────────────────────────────
//
// Lock-free rotating ring of traceSlabs.
//
// Write path (hot, per traced request):
//
//	atomic.Add(n+1) → write n instruction slots → write header slot → StoreUint32(ready,1)
//
// Drain path (background goroutine, every traceWindowMs):
//
//	seal active → advance → wait for in-flight ready flags → reconstruct traces →
//	atomically swap snapshot pointer
//
// Snapshot path (infrequent — Prometheus scrape / Studio UI):
//
//	atomic.Pointer load → return slice copy (no lock)
const (
	traceSlabCount = 4
	traceWindowMs  = 10 // rotation interval, matches obsWindowMs

)

// TraceSlabRing is a lock-free rotating slab ring for traced request data.
type TraceSlabRing struct {
	slabs   [traceSlabCount]*traceSlab
	active  int32  // current active slab index; atomic
	slabCap int
	dropped uint64 // atomic — blocks dropped due to slab full or sealed

	// snapshot is an atomically swapped pointer to the most recent []RequestTrace.
	// Drain goroutine writes it; Snapshot() reads it lock-free.
	snapshot atomic.Pointer[[]RequestTrace]

	stop chan struct{}
	done sync.WaitGroup
}

// NewTraceSlabRing creates and starts a TraceSlabRing.
// slabCap is the number of slots per slab; use ComputeSlabCap() for consistent
// sizing with the access-log and instr rings.
func NewTraceSlabRing(slabCap int) *TraceSlabRing {
	r := &TraceSlabRing{
		slabCap: slabCap,
		stop:    make(chan struct{}),
	}
	for i := range r.slabs {
		r.slabs[i] = newTraceSlab(slabCap)
	}
	empty := make([]RequestTrace, 0)
	r.snapshot.Store(&empty)
	return r
}

func (r *TraceSlabRing) Start() {
	r.done.Add(1)
	go r.drain()
}

func (r *TraceSlabRing) StopAndWait() {
	close(r.stop)
	r.done.Wait()
}

// Dropped returns the total number of trace blocks dropped due to slab full/sealed.
func (r *TraceSlabRing) Dropped() uint64 { return atomic.LoadUint64(&r.dropped) }

// Write stamps a traced request into the active slab.
// Write stamps a traced request into the active slab (header-only mode).
// Claims exactly 1 slot — instruction timing is persisted separately via
// ObsWriter.PersistTrace / enqueueWithInstr; no per-instruction sub-slots here.
// Zero allocations on the hot path.
func (r *TraceSlabRing) Write(trace *RequestTrace) {
	if trace == nil {
		return
	}

	// Header-only: claim exactly 1 slot.
	need := int64(1)

	slabIdx := atomic.LoadInt32(&r.active)
	slab := r.slabs[slabIdx]

	// Claim the slot.
	end := atomic.AddInt64(&slab.cursor, need)
	base := end - need

	// Slab full or sealed — drop and return.
	if end > int64(r.slabCap) || atomic.LoadUint32(&slab.sealed) != 0 {
		atomic.AddUint64(&r.dropped, 1)
		return
	}

	// Write header slot — Count=0 signals no instruction sub-slots follow.
	hdr := &slab.slots[base]
	hdr.isHeader = 1
	hdr.Count = 0
	hdr.TraceID = trace.Summary.TraceID
	hdr.TenantID = trace.Summary.TenantID
	hdr.ApiID = trace.Summary.ApiID
	hdr.Status = uint16(trace.Summary.Status)
	hdr.DurationNs = trace.Summary.DurationNs
	hdr.GatewayDurationNs = trace.Summary.GatewayDurationNs
	hdr.StartedAtUnixNano = trace.Summary.StartedAtUnixNano
	hdr.PathLen = uint8(copy(hdr.Path[:], trace.Summary.Path))
	hdr.MethodLen = uint8(copy(hdr.Method[:], trace.Summary.Method))
	// Signal: header is complete.
	atomic.StoreUint32(&hdr.ready, 1)
}

// Snapshot returns the most recent batch of reconstructed RequestTraces.
// Lock-free: does a single atomic pointer load.
func (r *TraceSlabRing) Snapshot() []RequestTrace {
	p := r.snapshot.Load()
	if p == nil {
		return nil
	}
	src := *p
	out := make([]RequestTrace, len(src))
	copy(out, src)
	return out
}

// drain is the single consumer goroutine.
func (r *TraceSlabRing) drain() {
	defer r.done.Done()
	tick := time.NewTicker(traceWindowMs * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			r.rotateSlab()
		case <-r.stop:
			r.rotateSlab()
			return
		}
	}
}

func (r *TraceSlabRing) rotateSlab() {
	current := atomic.LoadInt32(&r.active)
	slab := r.slabs[current]

	// Seal: block new claims on this slab.
	atomic.StoreUint32(&slab.sealed, 1)

	// Advance active so writers immediately move to the next slab.
	next := int32((int(current) + 1) % traceSlabCount)
	atomic.StoreInt32(&r.active, next)

	count := atomic.LoadInt64(&slab.cursor)
	if count <= 0 {
		slab.reset(0)
		return
	}
	if count > int64(r.slabCap) {
		count = int64(r.slabCap)
	}

	// Reconstruct traces from sealed slab.
	// Walk slot by slot; when we find a ready header, consume it + its instructions.
	deadline := time.Now().Add(time.Millisecond)
	var traces []RequestTrace

	for i := int64(0); i < count; {
		slot := &slab.slots[i]

		// Wait for the header ready flag (straggler guard).
		for atomic.LoadUint32(&slot.ready) == 0 {
			if time.Now().After(deadline) {
				atomic.AddUint64(&r.dropped, 1)
				goto done
			}
			runtime.Gosched()
		}

		if slot.isHeader != 1 {
			// Orphaned instruction slot (e.g. after a partial drop) — skip.
			i++
			continue
		}

		// Count=0 in header-only mode; instruction sub-slots are not written here.
		n := int(slot.Count)
		end := i + 1 + int64(n)
		if end > count {
			// Block extends beyond sealed count — partial, skip.
			i = end
			continue
		}

		t := reconstructTrace(slot)
		traces = append(traces, t)
		i = end
	}

done:
	slab.reset(count)

	if len(traces) == 0 {
		return
	}

	// Merge new traces into snapshot: append to existing, keep last slabCap entries.
	prev := r.snapshot.Load()
	var merged []RequestTrace
	if prev != nil {
		merged = append(*prev, traces...)
	} else {
		merged = traces
	}
	if len(merged) > r.slabCap {
		merged = merged[len(merged)-r.slabCap:]
	}
	r.snapshot.Store(&merged)
}

// reconstructTrace builds a RequestTrace from a header slot.
// In header-only mode (Count=0), no instruction sub-slots are read.
// Called in the drain goroutine — allocations here are acceptable.
func reconstructTrace(hdr *TraceSlot) RequestTrace {
	return RequestTrace{
		Summary: RequestSummary{
			TraceID:           hdr.TraceID,
			TenantID:          hdr.TenantID,
			ApiID:             hdr.ApiID,
			Status:            int(hdr.Status),
			DurationNs:        hdr.DurationNs,
			GatewayDurationNs: hdr.GatewayDurationNs,
			StartedAtUnixNano: hdr.StartedAtUnixNano,
			Path:              string(hdr.Path[:hdr.PathLen]),
			Method:            string(hdr.Method[:hdr.MethodLen]),
		},
	}
}
