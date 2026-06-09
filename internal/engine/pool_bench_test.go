package engine

// pool_bench_test.go — compares three pool strategies for rctx.Context (~10.6 KB):
//
//   sync.Pool     — built-in; per-P local lists (very fast), but GC clears idle
//                   items every cycle. Under high RPS, pool is often cold → allocs.
//
//   chanPool      — buffered channel; GC-resistant (strong ref), but every Get/Put
//                   acquires the channel's internal mutex. Slow under concurrency.
//
//   atomicPool    — lock-free array of atomic.Pointer slots; GC-resistant (strong
//                   ref) AND no locks. Each slot is cache-line padded to prevent
//                   false sharing. Get scans L→R, Put scans R→L to reduce collision.
//
// Run all benchmarks:
//   go test -bench=. -benchmem -count=3 ./internal/engine/
//
// Key columns:
//   ns/op       — average time per Get+Put pair
//   B/op        — bytes allocated per operation (0 = pool always hit; non-zero = allocs)
//   allocs/op   — integer-rounded allocation count per op
//
// NOTE: allocs/op rounds to 0 for small fractions. Always check B/op alongside it.
// Example: at 1 alloc per 100 ops, allocs/op=0 but B/op≈106 (10.6KB÷100).

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"rah/internal/rctx"
)

// ── Context constructor ───────────────────────────────────────────────────────

// newBenchContext matches the allocation pattern in FlowManager.Pool.New:
// two heap slices + inline slot init. The struct itself (~10.6 KB) escapes to heap.
func newBenchContext() *rctx.Context {
	ctx := &rctx.Context{
		MutationLog:     make([]rctx.HeaderMutation, 0, 16),
		ResponseHeaders: make([]rctx.HeaderMutation, 32),
	}
	ctx.InitSlots()
	return ctx
}

// ── Pool 1: channel-based (lock-based, GC-resistant) ─────────────────────────
//
// Kept for reference. The channel provides strong references so pool items
// survive GC cycles (0 B/op under GC pressure). However, every Get and Put
// acquires the channel's internal mutex — bad under high concurrency.

type chanPool struct {
	ch    chan *rctx.Context
	newFn func() *rctx.Context
}

func newChanPool(size int, newFn func() *rctx.Context) *chanPool {
	p := &chanPool{ch: make(chan *rctx.Context, size), newFn: newFn}
	for i := 0; i < size; i++ {
		p.ch <- newFn()
	}
	return p
}

func (p *chanPool) Get() *rctx.Context {
	select {
	case ctx := <-p.ch:
		return ctx
	default:
		return p.newFn()
	}
}

func (p *chanPool) Put(ctx *rctx.Context) {
	select {
	case p.ch <- ctx:
	default:
	}
}

// ── Pool 2: lock-free atomic pool (GC-resistant, no locks) ───────────────────
//
// Each slot is an atomic.Pointer[rctx.Context] padded to one CPU cache line
// (64 bytes) to prevent false sharing between adjacent slots.
//
// Without padding: 8 slots share one 64-byte cache line. When goroutines A and B
// hit "different" slots that share a line, every write by A invalidates B's
// cached copy of that line — forcing a round-trip to main memory (~200 cycles).
// This is false sharing: logically independent operations become serialized at
// the hardware level.
//
// With 64-byte padding: each slot occupies its own cache line. Goroutines on
// different slots never share a cache line — true independence.
//
// Get (left→right scan):
//   1. Load slot: cheap read (~4 cycles, no bus lock) to skip nil slots fast.
//   2. Swap(nil): atomic read-modify-write (~25 cycles, implicit LOCK on x86)
//      only when slot appears non-nil. May lose race → continue scanning.
//   Under a warm pool the first slot hit is usually slot 0: two atomic ops total.
//
// Put (right→left scan):
//   1. CompareAndSwap(nil, ctx): deposit into the first empty slot found.
//   Reverse direction reduces head-on collision with concurrent Gets.
//
// GC safety: slots hold *rctx.Context pointers — strong references. The GC
// cannot clear them regardless of how frequently it runs. 0 B/op under any
// GC pressure level.

const atomicPoolSlots = 64

type paddedSlot struct {
	v atomic.Pointer[rctx.Context]
	_ [56]byte // pad to 64-byte cache line; atomic.Pointer occupies 8 bytes
}

type atomicPool struct {
	slots [atomicPoolSlots]paddedSlot
	newFn func() *rctx.Context
}

func newAtomicPool(size int, newFn func() *rctx.Context) *atomicPool {
	p := &atomicPool{newFn: newFn}
	n := size
	if n > atomicPoolSlots {
		n = atomicPoolSlots
	}
	for i := 0; i < n; i++ {
		p.slots[i].v.Store(newFn()) // pre-warm
	}
	return p
}

func (p *atomicPool) Get() *rctx.Context {
	for i := range p.slots {
		// Cheap Load first: skip nil slots without touching the bus lock.
		// x86: MOV ~4 cycles vs XCHG-with-LOCK ~25 cycles.
		if p.slots[i].v.Load() == nil {
			continue
		}
		// Attempt to claim. Another goroutine may have beaten us — if Swap
		// returns nil, we lost the race; continue to the next slot.
		if ctx := p.slots[i].v.Swap(nil); ctx != nil {
			return ctx
		}
	}
	return p.newFn() // all slots empty — allocate
}

func (p *atomicPool) Put(ctx *rctx.Context) {
	for i := atomicPoolSlots - 1; i >= 0; i-- {
		if p.slots[i].v.CompareAndSwap(nil, ctx) {
			return
		}
	}
	// All slots occupied — drop; GC will collect this context.
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func prewarmSyncPool(pool *sync.Pool, n int) {
	tmp := make([]*rctx.Context, n)
	for i := range tmp {
		tmp[i] = pool.Get().(*rctx.Context)
	}
	for _, c := range tmp {
		pool.Put(c)
	}
}

// ── Section 1: warm pool, no GC pressure ─────────────────────────────────────
//
// All pools pre-warmed. Measures raw Get+Put throughput without interference.
//
// Expected ranking: sync.Pool ≫ atomicPool > chanPool
//   sync.Pool: per-P local lists, zero contention when one goroutine per P.
//   atomicPool: 2 atomic ops (Load + Swap) for a warm hit — no lock, very fast.
//   chanPool: channel mutex on every Get and Put — slowest.

func BenchmarkSyncPool(b *testing.B) {
	pool := &sync.Pool{New: func() any { return newBenchContext() }}
	prewarmSyncPool(pool, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := pool.Get().(*rctx.Context)
		pool.Put(ctx)
	}
}

func BenchmarkAtomicPool(b *testing.B) {
	pool := newAtomicPool(64, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := pool.Get()
		pool.Put(ctx)
	}
}

func BenchmarkChanPool(b *testing.B) {
	pool := newChanPool(64, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := pool.Get()
		pool.Put(ctx)
	}
}

// ── Section 2: GC pressure ────────────────────────────────────────────────────
//
// runtime.GC() every 100 ops simulates high-RPS conditions where allocation
// rate triggers frequent GC cycles.
//
// sync.Pool: GC clears idle pool items. Next Get() after GC must call New().
//   Expect non-zero B/op (≈ 10.6KB ÷ 100 ops ≈ 106 B/op).
//   allocs/op rounds to 0 at this frequency — check B/op for the real story.
//
// atomicPool + chanPool: slots are strongly referenced; GC has zero effect.
//   Expect 0 B/op regardless of GC frequency.
//
// NOTE: the ns/op numbers include the GC pause itself (runtime.GC() is not free).
// The meaningful signal is B/op, not ns/op, for this set of benchmarks.

func BenchmarkSyncPoolGCPressure(b *testing.B) {
	pool := &sync.Pool{New: func() any { return newBenchContext() }}
	prewarmSyncPool(pool, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%100 == 0 {
			runtime.GC() // clears sync.Pool; forces New() on next Get()
		}
		ctx := pool.Get().(*rctx.Context)
		pool.Put(ctx)
	}
}

func BenchmarkAtomicPoolGCPressure(b *testing.B) {
	pool := newAtomicPool(64, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%100 == 0 {
			runtime.GC() // slots hold strong refs — pool unaffected
		}
		ctx := pool.Get()
		pool.Put(ctx)
	}
}

func BenchmarkChanPoolGCPressure(b *testing.B) {
	pool := newChanPool(64, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%100 == 0 {
			runtime.GC() // channel holds strong refs — pool unaffected
		}
		ctx := pool.Get()
		pool.Put(ctx)
	}
}

// ── Section 3: parallel (concurrent goroutines, no GC pressure) ──────────────
//
// b.RunParallel spawns GOMAXPROCS goroutines, each calling Get+Put in a tight loop.
// This is the closest approximation to the gateway's concurrent request handling.
//
// Expected ranking: sync.Pool ≫ atomicPool ≫ chanPool
//   sync.Pool: per-P local lists — GOMAXPROCS goroutines access different lists,
//     minimal cross-P stealing, extremely low contention.
//   atomicPool: cache-line-padded slots — different goroutines hit different slots,
//     no false sharing, CAS contention is low when pool is warm.
//   chanPool: single mutex for all goroutines — all contend on the same lock.

func BenchmarkSyncPoolParallel(b *testing.B) {
	pool := &sync.Pool{New: func() any { return newBenchContext() }}
	prewarmSyncPool(pool, 512)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx := pool.Get().(*rctx.Context)
			pool.Put(ctx)
		}
	})
}

func BenchmarkAtomicPoolParallel(b *testing.B) {
	pool := newAtomicPool(atomicPoolSlots, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx := pool.Get()
			pool.Put(ctx)
		}
	})
}

func BenchmarkChanPoolParallel(b *testing.B) {
	pool := newChanPool(512, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx := pool.Get()
			pool.Put(ctx)
		}
	})
}

// ── Section 4: parallel + GC pressure (the real gateway scenario) ─────────────
//
// Concurrent goroutines + frequent GC: this is the 19K RPS cliff scenario.
//
// sync.Pool: GC wipes per-P local lists AND the victim cache. All goroutines
//   that Get() after a GC must call New(). Expect non-zero B/op.
//
// atomicPool: slots survive GC (strong refs). All goroutines always hit warm
//   slots. Expect 0 B/op. The CAS overhead is the only cost.
//
// chanPool: also GC-resistant (0 B/op) but slower due to mutex contention.
//
// On n2-std-2 (GOMAXPROCS=2), the parallel GC benchmarks are the most
// representative of production conditions.

func BenchmarkSyncPoolParallelGCPressure(b *testing.B) {
	pool := &sync.Pool{New: func() any { return newBenchContext() }}
	prewarmSyncPool(pool, 512)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%100 == 0 {
				runtime.GC()
			}
			ctx := pool.Get().(*rctx.Context)
			pool.Put(ctx)
			i++
		}
	})
}

func BenchmarkAtomicPoolParallelGCPressure(b *testing.B) {
	pool := newAtomicPool(atomicPoolSlots, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%100 == 0 {
				runtime.GC()
			}
			ctx := pool.Get()
			pool.Put(ctx)
			i++
		}
	})
}

func BenchmarkChanPoolParallelGCPressure(b *testing.B) {
	pool := newChanPool(512, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%100 == 0 {
				runtime.GC()
			}
			ctx := pool.Get()
			pool.Put(ctx)
			i++
		}
	})
}

// ── Section 5: false sharing demonstration ────────────────────────────────────
//
// Shows the penalty of NOT padding slots to cache-line size.
// Two versions of atomicPool: one padded (64-byte slots), one unpadded (8-byte).
// Under parallel access, unpadded slots cause false sharing: goroutines hitting
// "different" slots that share a cache line invalidate each other's L1/L2 entries.
// Expected: unpaddedAtomicPool significantly slower under b.RunParallel.

type unpaddedSlot struct {
	v atomic.Pointer[rctx.Context] // 8 bytes; 8 slots share one 64-byte cache line
}

type unpaddedAtomicPool struct {
	slots [atomicPoolSlots]unpaddedSlot
	newFn func() *rctx.Context
}

func newUnpaddedAtomicPool(size int, newFn func() *rctx.Context) *unpaddedAtomicPool {
	p := &unpaddedAtomicPool{newFn: newFn}
	n := size
	if n > atomicPoolSlots {
		n = atomicPoolSlots
	}
	for i := 0; i < n; i++ {
		p.slots[i].v.Store(newFn())
	}
	return p
}

func (p *unpaddedAtomicPool) Get() *rctx.Context {
	for i := range p.slots {
		if p.slots[i].v.Load() == nil {
			continue
		}
		if ctx := p.slots[i].v.Swap(nil); ctx != nil {
			return ctx
		}
	}
	return p.newFn()
}

func (p *unpaddedAtomicPool) Put(ctx *rctx.Context) {
	for i := atomicPoolSlots - 1; i >= 0; i-- {
		if p.slots[i].v.CompareAndSwap(nil, ctx) {
			return
		}
	}
}

func BenchmarkAtomicPoolParallelPadded(b *testing.B) {
	pool := newAtomicPool(atomicPoolSlots, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx := pool.Get()
			pool.Put(ctx)
		}
	})
}

func BenchmarkAtomicPoolParallelUnpadded(b *testing.B) {
	pool := newUnpaddedAtomicPool(atomicPoolSlots, newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx := pool.Get()
			pool.Put(ctx)
		}
	})
}

// ── Section 6: ctxPool (array-backed, lock-free Get, mutex-on-Put) ────────────
//
// Design: fixed [ctxPoolCap]*rctx.Context array = one GC root.
// All context pointers are strong references; GC cannot clear them.
//
// Get: CAS atomic counter n→n-1, return items[n-1]. No lock. No scan.
//   Falls back to newFn() only when count==0 (pool empty).
//
// Put: brief mutex → write items[count] → count++ → unlock.
//   Mutex guards the slot-index assignment so two concurrent Puts
//   never write to the same slot. Held for ~2 instructions.
//
// Memory ordering: Put writes items[n] BEFORE atomic Store(n+1).
//   Get's CAS observes the Store, which synchronizes the prior item write.
//   Correct on all platforms by Go's sync/atomic memory model.
//
// Expected vs prior pools:
//   Warm single:      slower than sync.Pool (CAS vs per-P list), faster than atomicPool (1 CAS vs 64-slot scan)
//   GC pressure:      0 B/op (strong array refs survive GC) — like chanPool/atomicPool
//   Parallel warm:    faster than chanPool (no channel mutex), faster than atomicPool (no scan)
//   Parallel+GC:      0 B/op + lower ns/op than atomicPool (single CAS per Get)

func BenchmarkCtxPool(b *testing.B) {
	pool := newCtxPool(newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := pool.Get()
		pool.Put(ctx)
	}
}

func BenchmarkCtxPoolGCPressure(b *testing.B) {
	pool := newCtxPool(newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%100 == 0 {
			runtime.GC()
		}
		ctx := pool.Get()
		pool.Put(ctx)
	}
}

func BenchmarkCtxPoolParallel(b *testing.B) {
	pool := newCtxPool(newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx := pool.Get()
			pool.Put(ctx)
		}
	})
}

func BenchmarkCtxPoolParallelGCPressure(b *testing.B) {
	pool := newCtxPool(newBenchContext)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%100 == 0 {
				runtime.GC()
			}
			ctx := pool.Get()
			pool.Put(ctx)
			i++
		}
	})
}
