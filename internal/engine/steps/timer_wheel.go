package steps

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
	"github.com/amitkhosla/rah/internal/rctx"
)

// Timer wheel constants. wheelSize is a power of 2 so slot index uses & instead of %.
// Covers 4096 seconds (~68 min) at 1-second resolution — sufficient for all gateway timeouts.
const (
	wheelSize       = 4096
	wheelMask       = wheelSize - 1
	maxWheelEntries = 1 << 19 // 524288 — global entry pool; index 0 = nil sentinel
	maxPerSlot      = 512     // max concurrent entries per slot; covers ~50K RPS Ã— 10ms avg response
)

// wheelEntry holds per-entry cancellation state. No next pointer — flat array replaces linked list.
type wheelEntry struct {
	cancelled uint32 // atomic: 0=active, 1=cancelled
}

// fireRef carries the action to execute when a wheel timer fires.
// Allocated from wheelFirePool; returned to pool after use.
const (
	wheelKindBody uint8 = 0
	wheelKindCtx  uint8 = 1
)

type fireRef struct {
	kind uint8
	body io.ReadCloser
	ctx  *rctx.Context
	gen  uint64
}

// wheelRingCell is one cell in a Vyukov MPMC ring.
// 16 bytes: seq(8) + value(4) + pad(4).
type wheelRingCell struct {
	seq   atomic.Uint64
	value uint32
	_     [4]byte
}

// wheelFreeRingT is the global free-index ring (maxWheelEntries cells).
// Must be initialised with init() before use.
type wheelFreeRingT struct {
	_      [64]byte // cache-line pad
	enqPos atomic.Uint64
	_      [56]byte
	deqPos atomic.Uint64
	_      [56]byte
	cells  [maxWheelEntries]wheelRingCell
}

func (r *wheelFreeRingT) init() {
	for i := range uint64(maxWheelEntries) {
		r.cells[i].seq.Store(i)
	}
}

func (r *wheelFreeRingT) enqueue(idx uint32) bool {
	for {
		pos := r.enqPos.Load()
		cell := &r.cells[pos&(maxWheelEntries-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos)
		if diff == 0 {
			if r.enqPos.CompareAndSwap(pos, pos+1) {
				cell.value = idx
				cell.seq.Store(pos + 1)
				return true
			}
		} else if diff < 0 {
			return false // full
		}
		// diff > 0: another goroutine advanced enqPos; retry
	}
}

func (r *wheelFreeRingT) dequeue() (uint32, bool) {
	for {
		pos := r.deqPos.Load()
		cell := &r.cells[pos&(maxWheelEntries-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos+1)
		if diff == 0 {
			if r.deqPos.CompareAndSwap(pos, pos+1) {
				data := cell.value
				cell.seq.Store(pos + maxWheelEntries)
				return data, true
			}
		} else if diff < 0 {
			return 0, false // empty
		}
		// diff > 0: another goroutine advanced deqPos; retry
	}
}

// slotPosRing is a per-slot recycled-position ring (maxPerSlot cells).
// Tick enqueues positions after processing; schedule dequeues them for reuse.
type slotPosRing struct {
	_      [64]byte // cache-line pad
	enqPos atomic.Uint64
	_      [56]byte
	deqPos atomic.Uint64
	_      [56]byte
	cells  [maxPerSlot]wheelRingCell
}

func (r *slotPosRing) init() {
	for i := range uint64(maxPerSlot) {
		r.cells[i].seq.Store(i)
	}
}

func (r *slotPosRing) enqueue(idx uint32) bool {
	for {
		pos := r.enqPos.Load()
		cell := &r.cells[pos&(maxPerSlot-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos)
		if diff == 0 {
			if r.enqPos.CompareAndSwap(pos, pos+1) {
				cell.value = idx
				cell.seq.Store(pos + 1)
				return true
			}
		} else if diff < 0 {
			return false // full
		}
		// diff > 0: another goroutine advanced enqPos; retry
	}
}

func (r *slotPosRing) dequeue() (uint32, bool) {
	for {
		pos := r.deqPos.Load()
		cell := &r.cells[pos&(maxPerSlot-1)]
		seq := cell.seq.Load()
		diff := int64(seq) - int64(pos+1)
		if diff == 0 {
			if r.deqPos.CompareAndSwap(pos, pos+1) {
				data := cell.value
				cell.seq.Store(pos + maxPerSlot)
				return data, true
			}
		} else if diff < 0 {
			return 0, false // empty
		}
		// diff > 0: another goroutine advanced deqPos; retry
	}
}

// wheelSlot is a flat-array slot. Each slot holds up to maxPerSlot concurrent entries.
// writePos is the high-water mark for fresh position allocation; posRing recycles positions.
type wheelSlot struct {
	_        [64]byte        // cache-line pad
	writePos atomic.Uint32   // next fresh position to allocate
	posRing  slotPosRing     // recycled positions returned by tick after each second
	buf      [maxPerSlot]uint32 // entry indices; 0 = empty/cancelled
}

// Package-level wheel state — singleton, initialised by StartTimerWheel.
var (
	wheelSlots    [wheelSize]wheelSlot
	wheelEntries  [maxWheelEntries]wheelEntry
	wheelFire     [maxWheelEntries]atomic.Pointer[fireRef]
	wheelFirePool = sync.Pool{New: func() any { return new(fireRef) }}
	wheelFreeRing wheelFreeRingT
	wheelCurrent  atomic.Uint32 // current slot index (updated by ticker)
)

// wheelHandle is returned by scheduleBody/scheduleCtx.
// Zero value (idx==0) means "no timer scheduled" — all operations are safe no-ops.
type wheelHandle struct {
	idx  uint32
	slot uint16
	pos  uint16
}

// cancel marks the entry as cancelled and immediately reclaims the fireRef and entry index.
// The position (pos) is always recycled by tick — never by cancel — to avoid double-enqueue.
func (h wheelHandle) cancel() {
	if h.idx == 0 {
		return
	}
	atomic.StoreUint32(&wheelEntries[h.idx].cancelled, 1)
	ref := wheelFire[h.idx].Swap(nil)
	if ref == nil {
		// tick already processed this entry — it will recycle pos via posRing.
		return
	}
	// We won the Swap race — we are responsible for ref and idx recycling.
	ref.kind, ref.body, ref.ctx, ref.gen = 0, nil, nil, 0
	wheelFirePool.Put(ref)
	// Zero buf[pos] so tick skips this position cleanly (tick still recycles pos to posRing).
	atomic.StoreUint32(&wheelSlots[h.slot].buf[h.pos], 0)
	wheelFreeRing.enqueue(h.idx)
}

// StartTimerWheel initialises the free ring and all per-slot posRings, then starts the ticker.
// Must be called exactly once before serving traffic.
// The goroutine exits when ctx is cancelled.
func StartTimerWheel(ctx context.Context) {
	wheelFreeRing.init()
	// Seed free ring with all usable indices (1..maxWheelEntries-1). Index 0 is the nil sentinel.
	for i := range uint32(maxWheelEntries - 1) {
		wheelFreeRing.enqueue(uint32(i + 1))
	}
	for i := range wheelSlots {
		wheelSlots[i].posRing.init()
	}
	go runWheelTicker(ctx)
}

func runWheelTicker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			sec := uint32(t.Unix())
			slot := sec & wheelMask
			wheelCurrent.Store(slot)
			wheelTick(slot)
		}
	}
}

// wheelTick processes all entries in the given slot.
// Temporal separation guarantees that no goroutine is concurrently writing to this slot
// (entries for slot S were scheduled >= 1 second ago; new goroutines target future slots),
// so writePos.Swap(0) is safe.
// Tick ALWAYS recycles positions back to posRing — cancel() never does, preventing double-enqueue.
func wheelTick(slot uint32) {
	s := &wheelSlots[slot]
	count := s.writePos.Swap(0)
	for i := range count {
		idx := atomic.LoadUint32(&s.buf[i])
		if idx != 0 {
			ref := wheelFire[idx].Swap(nil)
			if ref != nil {
				// We got the ref — fire if not cancelled, then recycle ref and idx.
				if atomic.LoadUint32(&wheelEntries[idx].cancelled) == 0 {
					switch ref.kind {
					case wheelKindBody:
						if ref.body != nil {
							if err := ref.body.Close(); err != nil {
								gatewaylog.Default.Debug("[TimerWheel] body close failed",
									gatewaylog.F("error", err.Error()),
								)
							}
						}
					case wheelKindCtx:
						if ref.ctx != nil {
							ref.ctx.CancelIfGeneration(ref.gen, context.DeadlineExceeded)
						}
					}
				}
				ref.kind, ref.body, ref.ctx, ref.gen = 0, nil, nil, 0
				wheelFirePool.Put(ref)
				wheelFreeRing.enqueue(idx)
				atomic.StoreUint32(&s.buf[i], 0)
			}
			// If ref==nil: cancel() already recycled idx and zeroed buf[i].
		}
		// Always recycle the position back to posRing — tick owns pos recycling.
		s.posRing.enqueue(i)
	}
}

// scheduleBody schedules a resp.Body.Close() timer.
// Returns a wheelHandle (idx==0 means pool/slot exhausted — caller falls back to time.AfterFunc).
func scheduleBody(body io.ReadCloser, dur time.Duration) wheelHandle {
	idx, ok := wheelFreeRing.dequeue()
	if !ok {
		return wheelHandle{} // entry pool exhausted
	}

	secs := uint32(dur/time.Second) + 1 // ceil to next whole second
	slotIdx := (wheelCurrent.Load() + secs) & wheelMask
	s := &wheelSlots[slotIdx]

	// Prefer a recycled position; fall back to a fresh one from the high-water mark.
	pos, ok := s.posRing.dequeue()
	if !ok {
		pos = s.writePos.Add(1) - 1
		if pos >= maxPerSlot {
			// Slot full — undo increment and return all resources.
			s.writePos.Add(^uint32(0))
			wheelFreeRing.enqueue(idx)
			return wheelHandle{}
		}
	}

	ref := wheelFirePool.Get().(*fireRef)
	ref.kind = wheelKindBody
	ref.body = body
	ref.ctx = nil
	ref.gen = 0
	atomic.StoreUint32(&wheelEntries[idx].cancelled, 0)
	wheelFire[idx].Store(ref)
	atomic.StoreUint32(&s.buf[pos], idx)

	return wheelHandle{idx: idx, slot: uint16(slotIdx), pos: uint16(pos)}
}

// scheduleCtx schedules a ctx.CancelIfGeneration(gen, DeadlineExceeded) timer.
// Returns a wheelHandle (idx==0 means pool/slot exhausted — caller falls back to time.AfterFunc).
func scheduleCtx(reqCtx *rctx.Context, gen uint64, dur time.Duration) wheelHandle {
	idx, ok := wheelFreeRing.dequeue()
	if !ok {
		return wheelHandle{} // entry pool exhausted
	}

	secs := uint32(dur/time.Second) + 1
	slotIdx := (wheelCurrent.Load() + secs) & wheelMask
	s := &wheelSlots[slotIdx]

	pos, ok := s.posRing.dequeue()
	if !ok {
		pos = s.writePos.Add(1) - 1
		if pos >= maxPerSlot {
			s.writePos.Add(^uint32(0))
			wheelFreeRing.enqueue(idx)
			return wheelHandle{}
		}
	}

	ref := wheelFirePool.Get().(*fireRef)
	ref.kind = wheelKindCtx
	ref.body = nil
	ref.ctx = reqCtx
	ref.gen = gen
	atomic.StoreUint32(&wheelEntries[idx].cancelled, 0)
	wheelFire[idx].Store(ref)
	atomic.StoreUint32(&s.buf[pos], idx)

	return wheelHandle{idx: idx, slot: uint16(slotIdx), pos: uint16(pos)}
}
