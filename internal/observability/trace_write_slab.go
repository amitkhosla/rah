package observability

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"rah/internal/gatewaylog"
)

// ── sizing constants ──────────────────────────────────────────────────────────

const (
	// traceSlabSlots is the number of slots per slab in the linked-slab chain.
	traceSlabSlots = 512

	// traceBatchSize is the default number of records to accumulate before
	// flushing to the store.
	traceBatchSize = 50

	// traceMaxSleepDur is the maximum adaptive sleep between drain iterations.
	traceMaxSleepDur = 2 * time.Second

	// traceMinSleepDur is the minimum sleep after a productive drain cycle.
	traceMinSleepDur = 10 * time.Millisecond

	// traceSleepMid is the sleep used when records were found but below half capacity.
	traceSleepMid = 100 * time.Millisecond

	// traceWakeSignalFraction triggers a wakeC signal when this fraction of
	// traceSlabSlots slots have been claimed (gives drain an early heads-up).
	traceWakeSignalFraction = traceSlabSlots / 2

	// traceStraggleDeadline is the maximum time drain will spin waiting for a
	// claimed-but-not-yet-ready slot before counting it as dropped.
	traceStraggleDeadline = time.Millisecond
)

// ── pooledTraceRec ────────────────────────────────────────────────────────────

// pooledTraceRec is borrowed from recPool and stored in traceWriteSlot.
// instrPCBuf and instrDurBuf are inline backing arrays for rec.InstrPCs and
// rec.InstrDursNs — no heap allocation for ≤64 instructions.
type pooledTraceRec struct {
	rec         TraceRecord
	instrPCBuf  [64]int16       // inline backing for rec.InstrPCs
	instrDurBuf [64]int32       // inline backing for rec.InstrDursNs
	payloads    []PayloadRecord // LLM and upstream payload records for this trace
}

// buildLLMPayloads walks the LLMCallBlock chain and builds PayloadRecords.
// Request JSON is scrubbed with ScrubJSON. Response bytes are left as-is
// (they are already parsed JSON from the provider — no sensitive fields expected
// in the response body since API keys are only in request headers).
// Content format per record: [4B req_len big-endian][req_bytes][4B res_len big-endian][res_bytes]
func buildLLMPayloads(traceID uint64, head *LLMCallBlock) []PayloadRecord {
	if head == nil {
		return nil
	}
	var records []PayloadRecord
	for b := head; b != nil; b = b.Next {
		for i := uint8(0); i < b.Count; i++ {
			e := &b.Entries[i]
			scrubbedReq := ScrubJSON(e.ReqBytes)
			// Content: [4B req_len][req_bytes][4B res_len][res_bytes]
			totalLen := 4 + len(scrubbedReq) + 4 + len(e.ResBytes)
			content := make([]byte, totalLen)
			reqLen := uint32(len(scrubbedReq))
			content[0] = byte(reqLen >> 24)
			content[1] = byte(reqLen >> 16)
			content[2] = byte(reqLen >> 8)
			content[3] = byte(reqLen)
			copy(content[4:], scrubbedReq)
			resLen := uint32(len(e.ResBytes))
			off := 4 + len(scrubbedReq)
			content[off] = byte(resLen >> 24)
			content[off+1] = byte(resLen >> 16)
			content[off+2] = byte(resLen >> 8)
			content[off+3] = byte(resLen)
			copy(content[off+4:], e.ResBytes)
			records = append(records, PayloadRecord{
				TraceID: traceID,
				Kind:    1, // LLM
				Seq:     e.Seq,
				PC:      e.PC,
				Content: content,
			})
		}
	}
	return records
}

// ── traceWriteSlot ────────────────────────────────────────────────────────────

// traceWriteSlot is one 64-byte cache line in the slab.
// The init() function below panics if the struct size drifts from 64 bytes.
type traceWriteSlot struct {
	ptr   atomic.Pointer[pooledTraceRec] // 8 bytes
	ready uint32                          // 4 bytes
	_pad  [52]byte                        //nolint:unused // pad to 64 bytes
}

func init() {
	if sz := unsafe.Sizeof(traceWriteSlot{}); sz != 64 {
		panic("observability: traceWriteSlot size changed — update _pad to keep it 64 bytes (current: " +
			fmt.Sprintf("%d", sz) + ")")
	}
}

// ── traceWriteSlab ────────────────────────────────────────────────────────────

// traceWriteSlab is one node in the linked slab chain.
// The cursor and sealed flag occupy the first 64 bytes (one cache line) so
// writers accessing the cursor never share a cache line with slot data.
type traceWriteSlab struct {
	// First cache line: hot writer state.
	cursor int64    // atomic — claimed slot count
	sealed uint32   // atomic — 1 = drain is processing this slab
	_pad   [52]byte //nolint:unused // pads cursor+sealed to 64 bytes (one cache line)

	next  atomic.Pointer[traceWriteSlab]   // next slab when this one is full
	slots [traceSlabSlots]traceWriteSlot
}

func init() {
	// Verify that the cache-line portion of traceWriteSlab (cursor+sealed+_pad)
	// is exactly 64 bytes. The offset of next must therefore be 64.
	type headerOnly struct {
		cursor int64
		sealed uint32
		_pad   [52]byte
	}
	if sz := unsafe.Sizeof(headerOnly{}); sz != 64 {
		panic("observability: traceWriteSlab header (cursor+sealed+_pad) size changed — " +
			"update _pad to keep it 64 bytes (current: " + fmt.Sprintf("%d", sz) + ")")
	}
}

// resetSlab clears all mutable state so the slab can be reused.
// Called only by the drain goroutine after a slab has been fully consumed.
func resetSlab(s *traceWriteSlab) {
	// Clear slot state for all slots that could have been used.
	count := atomic.LoadInt64(&s.cursor)
	if count > traceSlabSlots {
		count = traceSlabSlots
	}
	for i := int64(0); i < count; i++ {
		s.slots[i].ptr.Store(nil)
		atomic.StoreUint32(&s.slots[i].ready, 0)
	}
	s.next.Store(nil)
	atomic.StoreInt64(&s.cursor, 0)
	atomic.StoreUint32(&s.sealed, 0)
}

// ── slabPool ──────────────────────────────────────────────────────────────────

// slabPool pre-allocates slabs and grows on demand up to maxSlabs.
// mu protects the free list; it is only held during slab hand-off (not on the hot path).
type slabPool struct {
	mu         sync.Mutex
	free       []*traceWriteSlab
	totalAlloc int // total slabs ever allocated
	maxSlabs   int // hard cap; get() returns nil above this
	warnAt     int // log warning when totalAlloc exceeds this
}

// get returns a ready-to-use slab from the pool, allocating a new one if the
// free list is empty and the hard limit has not been reached.
// Returns nil when the hard limit is hit.
func (p *slabPool) get() *traceWriteSlab {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.free) > 0 {
		s := p.free[len(p.free)-1]
		p.free = p.free[:len(p.free)-1]
		return s
	}

	if p.totalAlloc >= p.maxSlabs {
		gatewaylog.Default.Warn("trace-write-ring: slab hard limit reached; traces will be dropped",
			gatewaylog.Fint("max_slabs", int64(p.maxSlabs)),
		)
		return nil
	}

	s := new(traceWriteSlab)
	p.totalAlloc++

	if p.totalAlloc > p.warnAt {
		gatewaylog.Default.Warn("trace-write-ring: slab count exceeds warn threshold",
			gatewaylog.Fint("total_alloc", int64(p.totalAlloc)),
			gatewaylog.Fint("warn_at", int64(p.warnAt)),
		)
	}

	return s
}

// put returns a slab to the free list. Called only by the drain goroutine.
func (p *slabPool) put(s *traceWriteSlab) {
	p.mu.Lock()
	p.free = append(p.free, s)
	p.mu.Unlock()
}

// ── traceWriteRing ────────────────────────────────────────────────────────────

// traceWriteRing is an expandable linked-slab ring for async trace persistence.
// Writers claim slots via atomic AddInt64; the drain goroutine is the sole
// consumer and the only goroutine that advances head or returns slabs to the pool.
type traceWriteRing struct {
	head    atomic.Pointer[traceWriteSlab] // drain reads from here
	tail    atomic.Pointer[traceWriteSlab] // writers append here
	mu      sync.Mutex                     // protects tail advancement only
	pool    slabPool
	recPool sync.Pool // elements: *pooledTraceRec
	wakeC   chan struct{}
	dropped uint64 // atomic
	store   ObsStore
	stop    chan struct{}
	done    sync.WaitGroup
}

// newTraceWriteRing creates a traceWriteRing and pre-allocates initialSlabs
// slabs into the pool. The pool will grow on demand up to maxSlabs; a warning
// is logged when totalAlloc exceeds warnAt.
func newTraceWriteRing(store ObsStore, initialSlabs, maxSlabs, warnAt int) *traceWriteRing {
	r := &traceWriteRing{
		pool: slabPool{
			maxSlabs: maxSlabs,
			warnAt:   warnAt,
		},
		wakeC: make(chan struct{}, 1),
		store: store,
		stop:  make(chan struct{}),
	}
	r.recPool.New = func() any { return &pooledTraceRec{} }

	// Pre-allocate the initial slabs directly (bypass pool.get so totalAlloc is accurate).
	r.pool.mu.Lock()
	for i := 0; i < initialSlabs; i++ {
		if r.pool.totalAlloc >= maxSlabs {
			break
		}
		r.pool.free = append(r.pool.free, new(traceWriteSlab))
		r.pool.totalAlloc++
	}
	r.pool.mu.Unlock()

	// Bootstrap: get the first slab and set both head and tail to it.
	first := r.pool.get()
	if first == nil {
		// Should not happen with a sane initialSlabs > 0.
		first = new(traceWriteSlab)
	}
	r.head.Store(first)
	r.tail.Store(first)
	return r
}

// enqueue places rec into the next available slot.
// Returns true on success, false when the hard slab limit is reached and the
// record must be dropped.
// Hot path: two atomics (cursor Add + ready Store) on the common case.
func (r *traceWriteRing) enqueue(rec TraceRecord) bool {
	t := r.recPool.Get().(*pooledTraceRec)
	t.rec = rec

	for {
		slab := r.tail.Load()
		idx := atomic.AddInt64(&slab.cursor, 1) - 1

		if idx < traceSlabSlots {
			// Slot claimed — write and signal.
			slot := &slab.slots[idx]
			slot.ptr.Store(t)
			atomic.StoreUint32(&slot.ready, 1)

			// Signal drain at 50% fill so it can start draining before the
			// slab is full. Non-blocking: if channel already has a token, skip.
			if idx == traceWakeSignalFraction-1 {
				select {
				case r.wakeC <- struct{}{}:
				default:
				}
			}
			return true
		}

		// Slab full: try to advance the tail to a new slab.
		if !r.advanceTail(slab) {
			// Hard limit hit — drop.
			atomic.AddUint64(&r.dropped, 1)
			r.recPool.Put(t)
			return false
		}
		// Retry with the new tail slab.
	}
}

// enqueueWithInstr places rec plus per-instruction timing into the next available
// slot using the inline buffers inside pooledTraceRec — no heap allocation.
// Returns true on success, false when the hard slab limit is reached.
func (r *traceWriteRing) enqueueWithInstr(rec TraceRecord, instr InstrSnapshot, llmCalls *LLMCallBlock) bool {
	t := r.recPool.Get().(*pooledTraceRec)
	n := int(instr.N)
	if n > 64 {
		n = 64
	}
	copy(t.instrPCBuf[:n], instr.PCs[:n])
	copy(t.instrDurBuf[:n], instr.Durs[:n])
	t.rec = rec
	if n > 0 {
		t.rec.InstrPCs = t.instrPCBuf[:n]    // points into inline buf — no heap alloc
		t.rec.InstrDursNs = t.instrDurBuf[:n] // points into inline buf — no heap alloc
	} else {
		t.rec.InstrPCs = nil
		t.rec.InstrDursNs = nil
	}
	t.payloads = buildLLMPayloads(rec.TraceID, llmCalls)

	for {
		slab := r.tail.Load()
		idx := atomic.AddInt64(&slab.cursor, 1) - 1

		if idx < traceSlabSlots {
			slot := &slab.slots[idx]
			slot.ptr.Store(t)
			atomic.StoreUint32(&slot.ready, 1)

			if idx == traceWakeSignalFraction-1 {
				select {
				case r.wakeC <- struct{}{}:
				default:
				}
			}
			return true
		}

		if !r.advanceTail(slab) {
			atomic.AddUint64(&r.dropped, 1)
			r.recPool.Put(t)
			return false
		}
	}
}

// advanceTail is called when a slab is full. It acquires the tail mutex,
// allocates a new slab from the pool, seals the full slab, links the new
// slab in, and updates tail.
// Returns false when the pool has hit the hard limit.
func (r *traceWriteRing) advanceTail(full *traceWriteSlab) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Another goroutine may have already advanced past this slab.
	if r.tail.Load() != full {
		return true // tail already moved; caller should retry
	}

	newSlab := r.pool.get()
	if newSlab == nil {
		return false // hard limit
	}

	atomic.StoreUint32(&full.sealed, 1)
	full.next.Store(newSlab)
	r.tail.Store(newSlab)
	return true
}

// droppedCount returns the total number of records dropped due to pool exhaustion.
func (r *traceWriteRing) droppedCount() uint64 {
	return atomic.LoadUint64(&r.dropped)
}

// start launches the background drain goroutine.
func (r *traceWriteRing) start() {
	r.done.Add(1)
	go r.drain()
}

// stopAndWait signals the drain goroutine to stop and waits for it to finish
// draining and flushing all remaining records.
func (r *traceWriteRing) stopAndWait() {
	close(r.stop)
	r.done.Wait()
}

// drainSlab collects all ready slots from slab into batch and payloadBatch.
// It handles the straggler guard: a writer that claimed a slot just before the
// drain pass may still be writing. The guard spins up to traceStraggleDeadline
// before skipping (counting the record as dropped).
//
// Returns true when the slab is fully drained (sealed and all claimed slots
// have been processed).
func (r *traceWriteRing) drainSlab(slab *traceWriteSlab, batch *[]TraceRecord, payloadBatch *[]PayloadRecord) (fullyDrained bool) {
	count := atomic.LoadInt64(&slab.cursor)
	if count > traceSlabSlots {
		count = traceSlabSlots
	}
	if count <= 0 {
		sealed := atomic.LoadUint32(&slab.sealed) == 1
		return sealed
	}

	deadline := time.Now().Add(traceStraggleDeadline)

	for i := int64(0); i < count; i++ {
		slot := &slab.slots[i]

		// Straggler guard: spin until the writer marks the slot ready.
		for atomic.LoadUint32(&slot.ready) == 0 {
			if time.Now().After(deadline) {
				// Writer is taking too long — count as dropped and move on.
				atomic.AddUint64(&r.dropped, 1)
				goto nextSlot
			}
			runtime.Gosched()
		}

		{
			t := slot.ptr.Load()
			if t != nil {
				rec := t.rec
				// If InstrPCs/InstrDursNs point into t's inline buffers, copy them
				// to heap-allocated slices before returning t to the pool — otherwise
				// the batch would alias memory that gets reused.
				if n := len(rec.InstrPCs); n > 0 {
					pcs := make([]int16, n)
					copy(pcs, rec.InstrPCs)
					rec.InstrPCs = pcs
					durs := make([]int32, n)
					copy(durs, rec.InstrDursNs)
					rec.InstrDursNs = durs
				}
				*batch = append(*batch, rec)
				if len(t.payloads) > 0 {
					*payloadBatch = append(*payloadBatch, t.payloads...)
					t.payloads = nil // allow GC before pool return
				}
				// Return the wrapper to the pool and clear the slot.
				r.recPool.Put(t)
				slot.ptr.Store(nil)
				atomic.StoreUint32(&slot.ready, 0)
			}
		}

	nextSlot:
	}

	return atomic.LoadUint32(&slab.sealed) == 1 && count == traceSlabSlots
}

// drain is the single consumer goroutine. It uses an adaptive sleep strategy:
//   - Fires immediately when wakeC receives a signal.
//   - Backs off exponentially (up to traceMaxSleepDur) when there is nothing to drain.
//   - Uses a short poll (traceMinSleepDur) when records are flowing steadily.
//   - Uses traceSleepMid when some records were found but the batch is not yet full.
func (r *traceWriteRing) drain() {
	defer r.done.Done()

	batch := make([]TraceRecord, 0, traceBatchSize*2)
	payloadBatch := make([]PayloadRecord, 0, traceBatchSize*4)
	sleep := traceMinSleepDur
	emptyCycles := 0

	ticker := time.NewTicker(sleep)
	defer ticker.Stop()

	flushTick := time.NewTicker(2 * time.Second) // periodic flush regardless of batch size
	defer flushTick.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = r.store.WriteTraceBatch(ctx, batch)
		if len(payloadBatch) > 0 {
			_ = r.store.WritePayloadBatch(ctx, payloadBatch)
			payloadBatch = payloadBatch[:0]
		}
		cancel()
		batch = batch[:0]
	}

	resetTicker := func(d time.Duration) {
		ticker.Reset(d)
		sleep = d
	}

	for {
		select {
		case <-r.stop:
			// Drain remaining records and flush before exiting.
			r.drainAll(&batch, &payloadBatch)
			flush()
			return

		case <-r.wakeC:
			// Received an early wake signal; drain immediately.
			r.drainAll(&batch, &payloadBatch)
			if len(batch) >= traceBatchSize {
				flush()
			}
			resetTicker(traceMinSleepDur)
			emptyCycles = 0

		case <-ticker.C:
			prevLen := len(batch)
			r.drainAll(&batch, &payloadBatch)
			added := len(batch) - prevLen

			if added == 0 {
				emptyCycles++
				// Exponential backoff: double sleep up to cap.
				newSleep := sleep * 2
				if newSleep > traceMaxSleepDur {
					newSleep = traceMaxSleepDur
				}
				resetTicker(newSleep)
			} else if added < traceBatchSize/2 {
				// Some records but not filling up — use medium sleep.
				resetTicker(traceSleepMid)
				emptyCycles = 0
			} else {
				// Healthy throughput — keep minimum sleep.
				resetTicker(traceMinSleepDur)
				emptyCycles = 0
			}

			if len(batch) >= traceBatchSize {
				flush()
			}

		case <-flushTick.C:
			r.drainAll(&batch, &payloadBatch)
			flush()
			resetTicker(traceMinSleepDur)
			emptyCycles = 0
		}

		_ = emptyCycles // used implicitly in backoff logic above
	}
}

// drainAll walks the slab chain starting from head, draining ready slots into
// batch and payloadBatch, advancing head when a slab is fully consumed.
// This is called only from the drain goroutine.
func (r *traceWriteRing) drainAll(batch *[]TraceRecord, payloadBatch *[]PayloadRecord) {
	for {
		current := r.head.Load()
		fullyDrained := r.drainSlab(current, batch, payloadBatch)

		if !fullyDrained {
			// Current slab still has writers in flight or is not yet sealed.
			return
		}

		// Slab is fully drained. Advance head if a next slab is linked.
		next := current.next.Load()
		if next == nil {
			// No next slab yet; we are caught up.
			return
		}

		// Advance head to next slab and return current to pool.
		r.head.Store(next)
		resetSlab(current)
		r.pool.put(current)
		// Continue loop to drain next slab immediately.
	}
}
