package engine

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"rah/internal/cache"
	"rah/internal/config"
	"rah/internal/rctx"
	"rah/internal/router"
	"runtime"
	"sync"
	"sync/atomic" // needed for atomic.Int64 in OverflowMetrics
)

type ExecutionStrategy int

const (
	StrategySync ExecutionStrategy = iota
	StrategyParallel
)

type EngineState struct {
	Router      *router.RahRouter
	Definitions []*ApiDefinition
	FlowLibrary map[string][]Instruction
}

// OverflowMetrics counts how often requests exceeded the inline arena.
// Non-zero ArenaOverflows signal that ArenaInlineSize needs tuning.
// Exposed via /debug/arena.
type OverflowMetrics struct {
	ArenaOverflows atomic.Int64 // extra arenaBlock borrowed from pool
}

type FlowManager struct {
	State   atomic.Pointer[EngineState]
	Pool    sync.Pool
	Config  config.GlobalLayout
	SlabMgr *cache.CacheManager
	Strategy ExecutionStrategy // Pre-determined at startup
	Metrics  OverflowMetrics
	// TxIDGen issues globally-unique transaction IDs per request.
	// Always non-nil; one instance per gateway process.
	TxIDGen *rctx.TxIDGenerator
	// RateLimitStore is a 1M-slot fixed-window counter arena (8 MB).
	// Used by the opt-in check_rate_limit step.
	RateLimitStore *CounterStore
}

func NewFlowManager(maxAPIs int, cfg config.GlobalLayout) *FlowManager {
	strategy := StrategySync
	if runtime.NumCPU() > 2 {
		strategy = StrategyParallel
	}
	fm := &FlowManager{
		Config:         cfg,
		Strategy:       strategy,
		TxIDGen:        rctx.NewTxIDGenerator(),
		RateLimitStore: NewCounterStore(1 << 20), // 1M slots = 8 MB
	}
	log.Printf("instance fingerprint: %s", fm.TxIDGen.Fingerprint())

	// Initialize with an empty but valid state
	initialState := &EngineState{
		Router:      router.New(),
		Definitions: make([]*ApiDefinition, maxAPIs),
		FlowLibrary: make(map[string][]Instruction),
	}
	fm.State.Store(initialState)

	fm.Pool.New = func() any {
		ctx := &rctx.Context{
			// MutationLog and ResponseHeaders are one-time pool allocations
			// (not per-request) — acceptable make() here.
			MutationLog:     make([]rctx.HeaderMutation, 0, 16),
			ResponseHeaders: make([]rctx.HeaderMutation, 32),
		}
		// Wire ByteSlots/IntSlots/BoolSlots to inline base arrays.
		// Zero heap allocations for slot infrastructure.
		ctx.InitSlots()
		return ctx
	}
	return fm
}

func (fm *FlowManager) ProcessRequest(ctx *rctx.Context, req *http.Request) {
	state := fm.State.Load()

	// 1. Initial Validation
	apiId := ctx.ApiId
	if apiId == 0 || int(apiId) >= len(state.Definitions) {
		ctx.ResponseStatus = 404
		return
	}

	def := state.Definitions[apiId]
	if def == nil {
		ctx.ResponseStatus = 404
		return
	}

	// 2. Stage 2 Routing
	endpoint := fm.resolveSubPath(ctx, def)
	if endpoint == nil {
		return
	}

	// 3. Assign a globally-unique transaction ID for this request.
	ctx.InternalTxID = fm.TxIDGen.Generate(ctx.Timing.StartNs)

	// 4. Metadata Extraction
	fm.Extract(ctx, req)

	// 5. Plan Execution
	Execute(ctx, endpoint.Plan, 0)
}

// ReturnContext records overflow metrics, releases pool-borrowed overflow
// resources, then returns the context to the pool.
// Must be called instead of Pool.Put directly.
func (fm *FlowManager) ReturnContext(ctx *rctx.Context) {
	if ctx.ArenaOverflowed {
		fm.Metrics.ArenaOverflows.Add(1)
	}
	ctx.ReleaseOverflow()
	fm.Pool.Put(ctx)
}

// RunInBackground detaches a context from the request lifecycle and executes
// task in a background goroutine. The context is returned to the pool only
// after task completes.
func (fm *FlowManager) RunInBackground(ctx *rctx.Context, task func(*rctx.Context)) {
	ctx.MarkDetachedFromPool()
	go func() {
		defer fm.ReturnContext(ctx)
		task(ctx)
	}()
}

func (fm *FlowManager) Extract(ctx *rctx.Context, req *http.Request) {
	ctx.Request = req
	ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)
	ctx.RequestBody = req.Body
	ctx.MaxBodySize = fm.Config.DefaultLimits.MaxBodySize
}

// SetState replaces the entire routing and execution logic atomically.
// This is called by the ManagementServer after a "Bake" is complete.
func (fm *FlowManager) SetState(newState *EngineState) {
	fm.State.Store(newState)
}

func (fm *FlowManager) ReadBodyToBuffer(ctx *rctx.Context) error {
	ctx.RequestBuffer = ctx.RequestBuffer[:0]
	limitReader := io.LimitReader(ctx.Request.Body, int64(cap(ctx.RequestBuffer)))
	buf := bytes.NewBuffer(ctx.RequestBuffer)
	_, err := io.Copy(buf, limitReader)
	ctx.RequestBuffer = buf.Bytes()
	return err
}

// ExecuteMultiWrite handles the chunked broadcast.
// If Strategy is Sync, it loops through writers.
// If Strategy is Parallel, it uses goroutines for each writer.
func (fm *FlowManager) ExecuteMultiWrite(ctx *rctx.Context, writers []io.Writer) error {
	chunkSize := 32 * 1024
	if cap(ctx.ScratchBuffer) < chunkSize {
		// fallback or handle error
	}

	buf := ctx.ScratchBuffer[:chunkSize]

	for {
		n, err := ctx.Request.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if fm.Strategy == StrategySync {
				for _, w := range writers {
					if _, werr := w.Write(chunk); werr != nil {
						return werr
					}
				}
			} else {
				var wg sync.WaitGroup
				for _, w := range writers {
					wg.Add(1)
					go func(writer io.Writer, b []byte) {
						defer wg.Done()
						writer.Write(b)
					}(w, chunk)
				}
				wg.Wait()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

/*
resolveSubPath performs Stage 2 routing inside an ApiDefinition.

Architecture Overview:

Stage 1:
- RahRouter resolves base path → ApiDefinition.

Stage 2:
- SubArena radix traversal resolves:
  - Static segments (priority)
  - Dynamic path params (fallback)
  - HTTP method differentiation
  - Strict vs non-strict trailing slash

Performance Characteristics:
- Lock-free
- No heap allocations
- Zero-copy path param extraction
- O(path length) traversal

Behavior:
- Returns 404 if path does not match.
- Returns 405 if method unsupported.
- Returns endpoint execution plan if matched.
*/

func (fm *FlowManager) resolveSubPath(
	ctx *rctx.Context,
	def *ApiDefinition,
) *Endpoint {

	if len(def.SubArena) == 0 {
		ctx.ResponseStatus = 404
		return nil
	}

	mIdx := MethodToIdx(ctx.Method)

	currIdx := uint32(0)

	baseLen := len(def.BaseRawPath)
	relPath := ctx.Path[baseLen:]

	strPos := 0
	if len(relPath) > 0 && relPath[0] == '/' {
		strPos = 1
	}

	pathLen := len(relPath)

	for strPos < pathLen {

		char := relPath[strPos]
		curr := &def.SubArena[currIdx]

		nextIdx := curr.FindChildIdx(char, def.SubArena)
		if nextIdx != 0 {
			currIdx = nextIdx
			strPos += int(def.SubArena[nextIdx].PrefixLen)
			if strPos < pathLen && relPath[strPos] == '/' {
				strPos++
			}
			continue
		}

		if curr.HasParamChild {
			start := strPos
			for strPos < pathLen && relPath[strPos] != '/' {
				strPos++
			}
			slot := curr.ParamSlot
			ctx.ByteSlots[slot] = relPath[start:strPos]
			currIdx = curr.ParamChildIdx
			if strPos < pathLen && relPath[strPos] == '/' {
				strPos++
			}
			continue
		}

		ctx.ResponseStatus = 404
		return nil
	}

	node := &def.SubArena[currIdx]

	if node.AllowedMethods&(1<<mIdx) == 0 {
		ctx.ResponseStatus = 405
		return nil
	}

	if node.StrictMethods&(1<<mIdx) != 0 {
		if len(relPath) > 0 && relPath[len(relPath)-1] == '/' {
			ctx.ResponseStatus = 404
			return nil
		}
	}

	ep := &def.Endpoints[node.EndpointIdx[mIdx]]
	ctx.EndpointId = ep.EndpointId
	ctx.APIRateLimitId = ep.APIRateLimitId
	ctx.EndpointRateLimitId = ep.EndpointRateLimitId
	return ep
}

