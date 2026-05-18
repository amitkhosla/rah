package steps

import (
	"sync"
	"sync/atomic"
	"time"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// noopResponseWriter is a do-nothing ResponseWriter used for branch contexts.
// Branches must not write to the parent response — all output is collected
// in the branch's own slots and the parent merges results after the join.
type noopResponseWriter struct{}

func (noopResponseWriter) Write(p []byte) (int, error)    { return len(p), nil }
func (noopResponseWriter) WriteHeader(statusCode int)     {}
func (noopResponseWriter) Header() interface{ Set(string, string) } {
	// Return a no-op header map (satisfies http.Header-compatible interface).
	return noopHeader{}
}

// noopHeader satisfies the Header() return type required by rctx.ResponseWriter.
// rctx.ResponseWriter.Header() returns http.Header, so we need to provide that.
type noopHeader struct{}

func (noopHeader) Set(key, value string) {}

// branchResponseWriter wraps our noopResponseWriter to satisfy rctx.ResponseWriter.
type branchResponseWriter struct{}

func (branchResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (branchResponseWriter) WriteHeader(statusCode int)  {}
func (branchResponseWriter) Header() interface {
	Add(key, value string)
	Del(key string)
	Get(key string) string
	Set(key, value string)
	Values(key string) []string
} {
	return noopHTTPHeader{}
}

// noopHTTPHeader satisfies http.Header's interface (enough for rctx.Reset).
type noopHTTPHeader struct{}

func (noopHTTPHeader) Add(key, value string)   {}
func (noopHTTPHeader) Del(key string)          {}
func (noopHTTPHeader) Get(key string) string   { return "" }
func (noopHTTPHeader) Set(key, value string)   {}
func (noopHTTPHeader) Values(key string) []string { return nil }

// ── Branch context pool ───────────────────────────────────────────────────────

// branchWriter is the singleton no-op writer handed to branch rctx.Context instances.
var branchWriter rctx.ResponseWriter = (*branchWriterImpl)(nil)

// branchWriterImpl is a zero-size type that satisfies rctx.ResponseWriter.
// A nil pointer to it is valid because none of its methods dereference the receiver.
type branchWriterImpl struct{}

func (*branchWriterImpl) Write(p []byte) (int, error) { return len(p), nil }
func (*branchWriterImpl) WriteHeader(statusCode int)  {}
func (*branchWriterImpl) Header() interface {
	Add(key, value string)
	Del(key string)
	Get(key string) string
	Set(key, value string)
	Values(key string) []string
} {
	panic("branchWriterImpl.Header() must not be called")
}

// branchCtxPool pools rctx.Context objects for branch execution.
// Each branch gets a fully-reset context so it does not share arena or slot
// backing with the parent — satisfying C1 (own ByteSlots backing).
var branchCtxPool = sync.Pool{
	New: func() any {
		ctx := new(rctx.Context)
		// Wire inline slot arrays once — they never move.
		ctx.InitSlots()
		return ctx
	},
}

// acquireBranchCtx returns a branch context pre-populated with read-only
// fields copied from the parent (TenantID, Request, ResponseStatus) and
// independent slot backing (C1: copy values, not slice headers).
func acquireBranchCtx(parent *rctx.Context) *rctx.Context {
	bc := branchCtxPool.Get().(*rctx.Context)
	// Reset re-wires ByteSlots/IntSlots/BoolSlots to the inline base arrays,
	// zeroes all fields, and accepts a no-op writer for the branch.
	bc.Reset(noopWriter{})

	// Copy read-only identity from parent.
	bc.TenantID = parent.TenantID
	bc.Request = parent.Request // shared read-only; branches must not mutate it
	bc.ResponseStatus = parent.ResponseStatus

	// C1: copy slot *values* (not the slice headers themselves) so each branch
	// has its own independent backing. parent.ByteSlots[i] may point into the
	// parent's inline arena; we copy bytes into the branch's own heap slice.
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

// releaseBranchCtx returns a branch context to the pool after clearing it.
func releaseBranchCtx(bc *rctx.Context) {
	bc.ReleaseOverflow()
	bc.Reset(noopWriter{})
	branchCtxPool.Put(bc)
}

// noopWriter is a zero-size ResponseWriter used as the branch's writer.
// It satisfies rctx.ResponseWriter; branch writes are silently discarded.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error)         { return len(p), nil }
func (noopWriter) WriteHeader(statusCode int)           {}
func (noopWriter) Header() interface {
	Add(key, value string)
	Del(key string)
	Get(key string) string
	Set(key, value string)
	Values(key string) []string
} {
	return noopHTTPHeader2{}
}

type noopHTTPHeader2 struct{}

func (noopHTTPHeader2) Add(key, value string)      {}
func (noopHTTPHeader2) Del(key string)             {}
func (noopHTTPHeader2) Get(key string) string      { return "" }
func (noopHTTPHeader2) Set(key, value string)      {}
func (noopHTTPHeader2) Values(key string) []string { return nil }

// ── Worker pool ───────────────────────────────────────────────────────────────

type workerPool struct {
	tasks chan func()
}

// globalWorkerPool is the package-level goroutine pool shared by all
// ParallelStep invocations. 128 workers provide concurrency for typical
// gateway workloads without unbounded thread growth.
var globalWorkerPool = newWorkerPool(128)

func newWorkerPool(size int) *workerPool {
	p := &workerPool{tasks: make(chan func(), 1024)}
	for i := 0; i < size; i++ {
		go func() {
			for task := range p.tasks {
				func() {
					defer func() { recover() }() // C4: absorb any worker panic
					task()
				}()
			}
		}()
	}
	return p
}

// Submit enqueues a task. Blocks when the queue is full (back-pressure).
func (p *workerPool) Submit(f func()) {
	p.tasks <- f
}

// ── Branch execution ──────────────────────────────────────────────────────────

// executeBranchTable runs a pre-compiled instruction table against bc.
// It mirrors the inner loop of engine.Execute but without observability hooks
// so it stays allocation-free for the common case.
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
// instruction sub-tables concurrently and waits for all of them to finish (or
// for the timeout / fail_fast cancellation to fire).
//
// subTables[i] is the instruction slice for branch i, compiled independently
// by the compiler. Each branch gets its own rctx.Context (C1 isolation).
//
// Concurrency rules honoured:
//   C1: each branch owns its slot backing (copy values at fork)
//   C2: wg.Wait() in the drainer before any release — prevent use-after-return
//   C3: sync.Once wraps cancelCh close — prevent close-of-closed-channel panic
//   C4: defer recover() inside every worker goroutine
//   C5: resultCh is buffered to N (number of branches)
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

			// C5: resultCh buffered to N so workers never block on send.
			resultCh := make(chan branchResult, n)

			// C3: cancelCh closed at most once.
			cancelCh := make(chan struct{})
			var cancelOnce sync.Once
			cancelFn := func() { cancelOnce.Do(func() { close(cancelCh) }) }

			// C2: WaitGroup so the drainer knows when all goroutines are done.
			var wg sync.WaitGroup
			wg.Add(n)

			for i, table := range subTables {
				i, table := i, table // capture loop variables
				bc := acquireBranchCtx(ctx)

				globalWorkerPool.Submit(func() {
					defer wg.Done() // C2: always signal, even on panic

					// C4: catch panics in branch execution
					panicked := false
					func() {
						defer func() {
							if r := recover(); r != nil {
								panicked = true
							}
						}()

						// Honour cancellation before we start work.
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

			// Drain goroutines that did not complete (timeout / fail_fast early exit).
			// C2: start a background goroutine that waits for ALL goroutines to finish
			// before releasing their branch contexts — prevents use-after-return.
			remaining := n - collected
			if remaining > 0 {
				go func() {
					wg.Wait() // C2: all n goroutines must finish before any release
					for i := 0; i < remaining; i++ {
						r := <-resultCh
						releaseBranchCtx(r.bc)
					}
				}()
			}

			// Release branch contexts for the branches we already collected.
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
