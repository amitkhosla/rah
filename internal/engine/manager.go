package engine

import (
	"bytes"
	"io"
	"net/http"
	"rah/internal/cache"
	"rah/internal/config"
	"rah/internal/rctx"
	"rah/internal/router" // Added import
	"runtime"
	"sync"
	"sync/atomic" // Added import
)

// MEMORY BANK REQUIREMENTS:
// 1. Use a sharded channel (chan []byte) to store 4KB slices.
// 2. Sharding should be based on runtime.NumCPU() to prevent lock contention.
// 3. Initial allocation happens ONCE at startup.

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

type FlowManager struct {
	State          atomic.Pointer[EngineState] // The core change
	HeaderRegistry *HeaderRegistry
	Pool           sync.Pool
	Config         config.GlobalLayout
	SlabMgr        *cache.CacheManager
	Strategy       ExecutionStrategy // Pre-determined at startup
	// Bank *MemoryBank
}

func NewFlowManager(maxAPIs int, cfg config.GlobalLayout) *FlowManager {
	strategy := StrategySync
	if runtime.NumCPU() > 2 {
		strategy = StrategyParallel
	}
	fm := &FlowManager{
		HeaderRegistry: NewHeaderRegistry(),
		Config:         cfg,
		Strategy:       strategy,
	}

	// Initialize with an empty but valid state
	initialState := &EngineState{
		Router:      router.New(),
		Definitions: make([]*ApiDefinition, maxAPIs),
		FlowLibrary: make(map[string][]Instruction),
	}
	fm.State.Store(initialState)

	fm.Pool.New = func() any {
		return &rctx.Context{
			ByteSlots:       make([][]byte, cfg.MaxBytesSlots),
			IntSlots:        make([]int64, cfg.MaxIntsSlots),
			BoolSlots:       make([]bool, cfg.MaxBoolsSlots),
			MutationLog:     make([]rctx.HeaderMutation, 0, 16),
			ResponseHeaders: make([]rctx.HeaderMutation, 32),
		}
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

	// 2. Stage 2 Routing (Extracted)
	endpoint := fm.resolveSubPath(ctx, def)
	if endpoint == nil {
		// ctx.ResponseStatus is set inside resolveSubPath
		return
	}

	// 3. Metadata Extraction
	fm.Extract(ctx, req)

	// 4. Plan Execution
	Execute(ctx, endpoint.Plan, 0)
	// MEMORY ASSIGNMENT:
	// Before processing, assign a ShardID to the context to minimize
	// cross-CPU cache bouncing during memory retrieval.
}

// RunInBackground detaches a context from the request lifecycle and executes
// task in a background goroutine. The context is returned to the pool only
// after task completes.
func (fm *FlowManager) RunInBackground(ctx *rctx.Context, task func(*rctx.Context)) {
	ctx.MarkDetachedFromPool()
	go func() {
		defer fm.Pool.Put(ctx)
		task(ctx)
	}()
}

func (fm *FlowManager) Extract(ctx *rctx.Context, req *http.Request) {
	ctx.Request = req
	ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)
	lookup := fm.HeaderRegistry.Current.Load().Map
	for key, values := range req.Header {
		if slotIdx, ok := lookup[key]; ok {
			ctx.ByteSlots[slotIdx] = []byte(values[0])
		}
	}
	ctx.RequestBody = req.Body
	ctx.MaxBodySize = fm.Config.DefaultLimits.MaxBodySize
}

// SetState replaces the entire routing and execution logic atomically.
// This is called by the ManagementServer after a "Bake" is complete.
func (fm *FlowManager) SetState(newState *EngineState) {
	fm.State.Store(newState)
}

func (fm *FlowManager) ReadBodyToBuffer(ctx *rctx.Context) error {
	// 1. Reset the buffer to 0 length but keep capacity
	ctx.RequestBuffer = ctx.RequestBuffer[:0]

	// 2. Use io.CopyN or ReadAll with a LimitReader
	// This reads from the wire directly into our pre-allocated Context buffer
	limitReader := io.LimitReader(ctx.Request.Body, int64(cap(ctx.RequestBuffer)))

	// Efficiently append to the pre-allocated slice
	buf := bytes.NewBuffer(ctx.RequestBuffer)
	_, err := io.Copy(buf, limitReader)
	ctx.RequestBuffer = buf.Bytes()

	return err
}

// internal/engine/streaming.go

// ExecuteMultiWrite handles the chunked broadcast.
// If Strategy is Sync, it loops through writers.
// If Strategy is Parallel, it uses goroutines for each writer.
func (fm *FlowManager) ExecuteMultiWrite(ctx *rctx.Context, writers []io.Writer) error {
	// We use a small chunk buffer (e.g., 32KB) to keep memory footprint low
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
				// Parallel Fan-out logic using WaitGroups for larger machines
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
/*
resolveSubPath performs Stage 2 routing.

Behavior:
- 404 if path not found.
- 405 if path found but method not allowed.
- Strict slash enforced per method.
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

	return &def.Endpoints[node.EndpointIdx[mIdx]]
}
