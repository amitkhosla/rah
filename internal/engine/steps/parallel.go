package steps

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// ── No-op ResponseWriter for branch contexts ──────────────────────────────────

// branchWriter is a singleton no-op rctx.ResponseWriter.
// Branch contexts must not write to the parent HTTP response — all their output
// stays in their own slots. The noop writer satisfies the interface contract
// without any memory allocation per branch.
type branchWriterT struct{}

func (branchWriterT) Write(p []byte) (int, error) { return len(p), nil }
func (branchWriterT) WriteHeader(_ int)            {}
func (branchWriterT) Header() http.Header          { return make(http.Header) }

var branchWriter rctx.ResponseWriter = branchWriterT{}

// ── Branch context pool ───────────────────────────────────────────────────────

// branchCtxPool pools rctx.Context objects for parallel branch execution.
// Reuse avoids per-request allocation of the ~3 KB Context struct.
var branchCtxPool = sync.Pool{
	New: func() any {
		ctx := new(rctx.Context)
		ctx.InitSlots() // wire inline ByteSlots/IntSlots/BoolSlots once
		return ctx
	},
}

// acquireBranchCtx checks out a branch context from the pool, resets it, and
// copies read-only fields + slot values from the parent context.
//
// C1: slot *values* are deep-copied so the branch owns its backing memory and
// cannot corrupt the parent's inline arena.
func acquireBranchCtx(parent *rctx.Context) *rctx.Context {
	bc := branchCtxPool.Get().(*rctx.Context)
	bc.Reset(branchWriter)

	// Propagate identity fields that branches need for registry/cache calls.
	bc.TenantID = parent.TenantID
	bc.Request = parent.Request // shared read-only; branches must not mutate
	bc.ResponseStatus = parent.ResponseStatus

	// C1: copy slot values (not the slice headers) so each branch gets its own
	// independent backing. Parent slots may point into parent's inline arena.
	for i, v := range parent.ByteSlots {
		if len(v) > 0 {
			cp := make([]byte, len(v))
			copy(cp, v)
			bc.ByteSlots[i] = cp
		}
	}
	copy(bc.IntSlots, parent.IntSlots)
	copy(bc.BoolSlots, parent.BoolSlots)
	return bc
}

// releaseBranchCtx returns a branch context to the pool after cleanup.
func releaseBranchCtx(bc *rctx.Context) {
	bc.ReleaseOverflow()
	bc.Reset(branchWriter)
	branchCtxPool.Put(bc)
}

// ── Worker pool ───────────────────────────────────────────────────────────────

// workerPool is a goroutine pool that spawns workers on demand up to maxSize.
// Workers stay alive for idleTimeout to service back-to-back requests without
// re-creating goroutines; they exit after idleTimeout of inactivity so no
// goroutines are held at idle.
type workerPool struct {
	tasks       chan func()
	sem         chan struct{} // guards max live goroutines
	idleTimeout time.Duration
}

const workerIdleTimeout = 5 * time.Second

// globalWorkerPool is shared by all ParallelStep invocations. Workers are
// spawned lazily: none exist at startup; up to 128 may run under load; all
// exit within workerIdleTimeout of the last submitted task.
var globalWorkerPool = newWorkerPool(128)

func newWorkerPool(maxSize int) *workerPool {
	return &workerPool{
		tasks:       make(chan func(), 1024),
		sem:         make(chan struct{}, maxSize),
		idleTimeout: workerIdleTimeout,
	}
}

// Submit enqueues f and ensures at least one worker is running.
// If maxSize workers are already live, an existing worker will drain f.
// Blocks only when the 1024-slot queue is full (back-pressure).
func (p *workerPool) Submit(f func()) {
	p.tasks <- f
	select {
	case p.sem <- struct{}{}: // got a slot — spawn a worker
		go p.run()
	default: // max workers already live; one will pick up the task
	}
}

func (p *workerPool) run() {
	defer func() { <-p.sem }()
	timer := time.NewTimer(p.idleTimeout)
	defer timer.Stop()
	for {
		select {
		case task := <-p.tasks:
			func() {
				defer func() { recover() }() // C4: absorb worker panics
				task()
			}()
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(p.idleTimeout)
		case <-timer.C:
			return // idle timeout — release slot
		}
	}
}

// ── Branch execution ──────────────────────────────────────────────────────────

// executeBranchTable runs a pre-compiled instruction table against bc.
// It mirrors the core loop of engine.Execute without observability hooks
// to keep hot-path overhead minimal.
func executeBranchTable(bc *rctx.Context, table []engine.Instruction) {
	state := engine.ExecutionState{}
	tableLen := int16(len(table))
	for state.PC >= 0 && state.PC < tableLen {
		instr := &table[state.PC]
		if instr.Action == nil {
			state.PC++
			continue
		}
		nextPC := instr.Action(bc, &state)
		if nextPC == engine.StopPlan || nextPC == engine.StopCancelled {
			bc.Failed = true
			return
		}
		state.PC = nextPC
	}
}

// ── Result type ───────────────────────────────────────────────────────────────

type branchResult struct {
	bc        *rctx.Context
	branchIdx int
	failed    bool
}

// ── ParallelStep ─────────────────────────────────────────────────────────────

// ParallelStep returns an engine.Instruction that executes multiple pre-compiled
// instruction sub-tables concurrently and waits for all of them to complete (or
// for the timeout / fail_fast cancellation to fire).
//
// subTables[i] is the flat instruction slice for branch i, compiled independently
// by the control-plane compiler. Each branch gets its own rctx.Context (C1).
//
// Concurrency guarantees:
//
//	C1: each branch owns its slot backing (values deep-copied at fork)
//	C2: wg.Wait() in the drainer goroutine before any context release
//	C3: sync.Once wraps cancelCh close — no close-of-closed-channel panic
//	C4: defer recover() inside every worker goroutine
//	C5: resultCh buffered to N — workers never block on send
func ParallelStep(subTables [][]engine.Instruction, timeoutMs uint32, failFast bool) engine.Instruction {
	if timeoutMs == 0 {
		timeoutMs = 3000
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	return engine.Instruction{
		Name: "PARALLEL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			n := len(subTables)
			if n == 0 {
				return state.PC + 1
			}

			// C5: buffered so workers never block even if we stop collecting early.
			resultCh := make(chan branchResult, n)

			// C3: closed at most once regardless of how many branches fail.
			cancelCh := make(chan struct{})
			var cancelOnce sync.Once
			cancelFn := func() { cancelOnce.Do(func() { close(cancelCh) }) }

			// C2: tracks all n goroutines so the drainer can wait before releasing.
			var wg sync.WaitGroup
			wg.Add(n)

			for i, table := range subTables {
				i, table := i, table // capture loop variables
				bc := acquireBranchCtx(ctx)

				globalWorkerPool.Submit(func() {
					defer wg.Done() // C2: always signal completion

					panicked := false
					func() {
						defer func() {
							if r := recover(); r != nil { // C4: catch branch panics
								panicked = true
							}
						}()

						// Honour cancellation before starting work.
						select {
						case <-cancelCh:
							bc.Failed = true
							return
						default:
						}

						executeBranchTable(bc, table)
					}()

					failed := panicked || bc.Failed || atomic.LoadInt32(&bc.Cancelled) != 0
					if failFast && failed {
						cancelFn()
					}
					resultCh <- branchResult{branchIdx: i, failed: failed, bc: bc}
				})
			}

			// Collect results with timeout.
			timer := time.NewTimer(timeout)
			defer timer.Stop()

			results := make([]branchResult, 0, n)
			collected := 0
			anyFailed := false

		collect:
			for collected < n {
				select {
				case r := <-resultCh:
					results = append(results, r)
					collected++
					if r.failed {
						anyFailed = true
					}
					if failFast && anyFailed {
						cancelFn()
						break collect
					}
				case <-timer.C:
					cancelFn()
					break collect
				}
			}

			// Drain remaining goroutines that did not complete (timeout / fail_fast).
			// C2: the drainer waits for ALL goroutines before releasing their contexts
			// to prevent any use-after-return of the pooled Context objects.
			remaining := n - collected
			if remaining > 0 {
				go func() {
					wg.Wait() // C2: wait for every goroutine to finish
					for i := 0; i < remaining; i++ {
						r := <-resultCh
						releaseBranchCtx(r.bc)
					}
				}()
			}

			// Release collected branch contexts now that we are done reading them.
			for _, r := range results {
				releaseBranchCtx(r.bc)
			}

			if anyFailed && failFast {
				ctx.Failed = true
				return engine.StopPlan
			}

			return state.PC + 1
		},
	}
}
