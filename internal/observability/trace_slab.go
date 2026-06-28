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
	_pad0    [3]byte
	ready    uint32 // atomic: StoreUint32(1) by writer after all fields written

	// --- header fields (isHeader=1) ---
	Count             uint16 // number of instruction slots that follow this header
	Status            uint16 // HTTP status code
	ApiID             uint32
	TraceID           uint64
	TenantID          uint16
	_pad1             [6]byte
	DurationNs        int64
	GatewayDurationNs int64
	StartedAtUnixNano int64
	PathLen           uint8
	MethodLen         uint8
	_pad2             [6]byte
	Path              [128]byte
	Method            [8]byte

	// --- instruction fields (isHeader=0) ---
	PC      int16
	StepIdx int16
	DurNs   int32
	NameLen uint8
	_pad3   [7]byte
	Name    [32]byte

	// --- padding to 512 bytes ---
	_pad4 [264]byte
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

	// traceMaxInstrsPerReq caps how many instruction slots one trace can claim.
	// Flows with more instructions than this have their tail truncated in the slab
	// (the Instructions field on the reconstructed RequestTrace is still complete
	// because it was already populated on the heap before Write is called).
	traceMaxInstrsPerReq = 128
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
// n = len(trace.Instructions); claims n+1 slots (1 header + n instruction slots).
// If the trace has no instructions, claims 1 header-only slot.
// Zero allocations on the hot path.
func (r *TraceSlabRing) Write(trace *RequestTrace) {
	if trace == nil {
		return
	}

	n := len(trace.Instructions)
	if n > traceMaxInstrsPerReq {
		n = traceMaxInstrsPerReq
	}
	need := int64(n + 1) // +1 for header slot

	slabIdx := atomic.LoadInt32(&r.active)
	slab := r.slabs[slabIdx]

	// Claim a contiguous block of slots.
	end := atomic.AddInt64(&slab.cursor, need)
	base := end - need

	// Slab full or sealed — drop and return.
	if end > int64(r.slabCap) || atomic.LoadUint32(&slab.sealed) != 0 {
		atomic.AddUint64(&r.dropped, 1)
		return
	}

	// 1. Write instruction slots first (base+1 … base+n), NO ready flag yet.
	for i := 0; i < n; i++ {
		slot := &slab.slots[base+1+int64(i)]
		slot.isHeader = 0
		ev := &trace.Instructions[i]
		slot.PC = ev.PC
		slot.StepIdx = ev.StepIdx
		slot.DurNs = int32(ev.DurationNs)
		nl := copy(slot.Name[:], ev.Name)
		slot.NameLen = uint8(nl)
		// ready intentionally NOT set yet
	}

	// 2. Write header slot LAST — signals the entire block is complete.
	hdr := &slab.slots[base]
	hdr.isHeader = 1
	hdr.Count = uint16(n)
	hdr.TraceID = trace.Summary.TraceID
	hdr.TenantID = trace.Summary.TenantID
	hdr.ApiID = trace.Summary.ApiID
	hdr.Status = uint16(trace.Summary.Status)
	hdr.DurationNs = trace.Summary.DurationNs
	hdr.GatewayDurationNs = trace.Summary.GatewayDurationNs
	hdr.StartedAtUnixNano = trace.Summary.StartedAtUnixNano
	hdr.PathLen = uint8(copy(hdr.Path[:], trace.Summary.Path))
	hdr.MethodLen = uint8(copy(hdr.Method[:], trace.Summary.Method))
	// Signal: all instruction slots are written, header is now complete.
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

		n := int(slot.Count)
		end := i + 1 + int64(n)
		if end > count {
			// Block extends beyond sealed count — partial, skip.
			i = end
			continue
		}

		t := reconstructTrace(slot, slab.slots[i+1:end])
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

// reconstructTrace builds a RequestTrace from a header slot and its instruction slots.
// Called in the drain goroutine — allocations here are acceptable.
func reconstructTrace(hdr *TraceSlot, instrSlots []TraceSlot) RequestTrace {
	t := RequestTrace{
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
		Instructions: make([]InstructionEvent, 0, len(instrSlots)),
	}
	for i := range instrSlots {
		s := &instrSlots[i]
		t.Instructions = append(t.Instructions, InstructionEvent{
			Seq:        uint32(i),
			PC:         s.PC,
			StepIdx:    s.StepIdx,
			DurationNs: int64(s.DurNs),
			Name:       string(s.Name[:s.NameLen]),
		})
	}
	return t
}
