package engine

// ctx_pool.go — GC-resistant lock-free-read context pool.
//
// Design goals (vs sync.Pool):
//   sync.Pool is cleared on every GC cycle. Under high RPS, GC runs frequently
//   enough that the pool is perpetually cold â†’ ~10.6 KB allocation per request â†’
//   triggers more GC â†’ death spiral observed at ~19K RPS.
//
// This pool holds all contexts in a fixed-size array. The array is a single GC
// root; all Context pointers inside it are strong references that GC cannot clear.
//
// Get — fully lock-free:
//   1. Atomically CAS the stack top from n to n-1.
//   2. Return items[n-1] — the slot guaranteed written by the matching Put.
//   3. If pool empty (n==0), allocate a fresh Context (rare under steady load).
//
// Put — brief mutex (protects slot-index assignment only):
//   1. Under lock: write items[count], then count++, release lock.
//   Mutex is held for ~2 instructions. At GOMAXPROCS=2 (GCP n2-std-2) it almost
//   never contends. Get never waits on it.
//
// Memory ordering guarantee (Go sync/atomic model):
//   Put:  items[n] = ctx  THEN  count.Store(n+1)
//   Get:  count.CAS(n, n-1) succeeds only after observing Put's Store(n+1).
//   By Go's memory model, all writes before an atomic store are visible to any
//   goroutine that observes that store — so items[n-1] is safe to read without
//   an additional fence.

import (
	"github.com/amitkhosla/rah/internal/rctx"
	"sync"
	"sync/atomic"
)

const ctxPoolCap = 64

// ctxPool is a GC-resistant pool for *rctx.Context values.
// Zero value is not usable; construct with newCtxPool.
type ctxPool struct {
	items [ctxPoolCap]*rctx.Context // single GC root; all items strongly referenced
	count atomic.Int32              // stack top: 0 = empty, ctxPoolCap = full
	mu    sync.Mutex                // guards slot-write + count increment in Put only
	newFn func() *rctx.Context
}

func newCtxPool(newFn func() *rctx.Context) *ctxPool {
	p := &ctxPool{newFn: newFn}
	// Pre-warm: fill all slots so the pool is immediately hot.
	for i := range p.items {
		p.items[i] = newFn()
	}
	p.count.Store(ctxPoolCap)
	return p
}

// Get returns a pooled Context (lock-free when pool is warm) or allocates one.
func (p *ctxPool) Get() *rctx.Context {
	for {
		n := p.count.Load()
		if n == 0 {
			return p.newFn() // pool empty — allocate; rare under steady load
		}
		if p.count.CompareAndSwap(n, n-1) {
			// Slot n-1 was written by Put before count was incremented.
			// Go memory model: our CAS observed Put's atomic store, so
			// the prior non-atomic write to items[n-1] is also visible.
			return p.items[n-1]
		}
		// Another goroutine won the CAS; retry with updated count.
	}
}

// Put returns a Context to the pool. Takes a brief mutex only to assign a slot
// index before incrementing the counter that makes it visible to Get.
func (p *ctxPool) Put(ctx *rctx.Context) {
	p.mu.Lock()
	n := p.count.Load()
	if n < ctxPoolCap {
		p.items[n] = ctx  // write before incrementing count
		p.count.Store(n + 1) // now visible to Get
	}
	// if full: drop — GC will collect this context
	p.mu.Unlock()
}
