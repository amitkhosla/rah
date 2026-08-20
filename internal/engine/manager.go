package engine

import (
	"bytes"
	"io"
	"net/http"
	"github.com/amitkhosla/rah/internal/cache"
	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/gatewaylog"
	mqttpool "github.com/amitkhosla/rah/internal/mqtt"
	"github.com/amitkhosla/rah/internal/observability"
	"github.com/amitkhosla/rah/internal/quota"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/registry"
	"github.com/amitkhosla/rah/internal/router"
	"runtime"
	"sync"
	"sync/atomic" // needed for atomic.Int64 in OverflowMetrics
	"unsafe"
)

type ExecutionStrategy int

const (
	StrategySync ExecutionStrategy = iota
	StrategyParallel
)

// ConstantSlot is a pre-baked slot assignment for a route constant.
// Resolved once at deploy time; applied at each request with zero allocation.
type ConstantSlot struct {
	SlotIdx int
	Value   []byte // pre-allocated at bake time
}

// UpstreamUrlSource enumerates how the upstream URL is resolved for a route.
type UpstreamUrlSource uint8

const (
	UpstreamUrlSourceStatic     UpstreamUrlSource = 0 // literal URL, written to slot directly
	UpstreamUrlSourceRegistry   UpstreamUrlSource = 1 // registry URL key — resolved per-tenant
	UpstreamUrlSourceCache      UpstreamUrlSource = 2 // cache key — looked up at request time
	UpstreamUrlSourceHeader     UpstreamUrlSource = 3 // HTTP request header name
	UpstreamUrlSourceQueryParam UpstreamUrlSource = 4 // query parameter name
)

// UpstreamUrlInfo holds the baked upstream URL config for a single route.
// Stored in EngineState.RouteUpstreamUrls keyed by apiID<<8|endpointID.
type UpstreamUrlInfo struct {
	Source    UpstreamUrlSource
	SlotIdx   int    // destination ByteSlot index (resolved at bake time)
	Value     []byte // pre-allocated: literal URL (static) or key/header/param name (others)
	RegistryKeyID uint16 // pre-resolved registry KeyID (only for SourceRegistry)
}

type EngineState struct {
	Router              *router.RahRouter
	Definitions         []*ApiDefinition
	FlowLibrary         map[string][]Instruction
	FlowSlotRegistry    map[string]map[string]int // flow name → var name → slot index; populated at compile time
	RouteConstants      map[uint64][]ConstantSlot  // key: apiID<<8|endpointID; nil = no constants
	RouteUpstreamUrls   map[uint64]*UpstreamUrlInfo // key: apiID<<8|endpointID; nil = no upstream URL override
}

// GetFlowByName looks up a named flow from the library.
// Returns nil if the flow is not found.
func (s *EngineState) GetFlowByName(name string) []Instruction {
	if s.FlowLibrary == nil {
		return nil
	}
	return s.FlowLibrary[name]
}

// GetFlowSlots returns the slot name→index map for a named flow.
// Returns nil if the flow has no slot registry entry.
func (s *EngineState) GetFlowSlots(flowName string) map[string]int {
	if s.FlowSlotRegistry == nil {
		return nil
	}
	return s.FlowSlotRegistry[flowName]
}

// OverflowMetrics counts how often requests exceeded the inline arena.
// Non-zero ArenaOverflows signal that ArenaInlineSize needs tuning.
// Exposed via /debug/arena.
type OverflowMetrics struct {
	ArenaOverflows atomic.Int64 // extra arenaBlock borrowed from pool
}

// OpFlusher accepts a Batch of storage operations and dispatches them
// to the underlying data store. Submit must be safe to call concurrently.
type OpFlusher interface {
	Submit(batch rctx.Batch)
}

type FlowManager struct {
	State   atomic.Pointer[EngineState]
	// DraftState holds a compiled but not-yet-live EngineState submitted via
	// POST /sync?draft=true. Never used by the live router — only /test/execute
	// reads from it. Nil until a draft is submitted.
	DraftState atomic.Pointer[EngineState]
	Pool    sync.Pool
	Config  config.GlobalLayout

	// Limiter gates the number of requests that may be in-flight simultaneously.
	// TryAcquire is called before pool.Get; Release is deferred until after the
	// response is sent. The adaptive controller adjusts Limiter.limit each tick.
	Limiter ConcurrencyLimiter

	// limiterEnabled is true when the concurrency gate is active.
	// Set by StartController; updated atomically at runtime via PATCH /admin/concurrency.
	// Read on every request — must be a single atomic load (~1 ns).
	limiterEnabled atomic.Bool

	// liveConfig holds the current ConcurrencyConfig read by the AIMD goroutine
	// each tick and written by the PATCH handler. Always stores a
	// config.ConcurrencyConfig value (never nil) after StartController is called.
	liveConfig atomic.Value

	// LatencyRing is the two-generation rolling latency histogram that feeds
	// the adaptive controller. Record() is called post-response with
	// (clientTotal âˆ’ upstreamTime) in milliseconds.
	LatencyRing LatencyRing
	SlabMgr *cache.CacheManager
	Strategy ExecutionStrategy // Pre-determined at startup
	Metrics  OverflowMetrics
	// TxIDGen issues globally-unique transaction IDs per request.
	// Always non-nil; one instance per gateway process.
	TxIDGen *rctx.TxIDGenerator
	// RateLimitStore is a 1M-slot fixed-window counter arena (8 MB).
	// Used by the opt-in check_rate_limit step.
	RateLimitStore *CounterStore
	// CostQuotaManager tracks cost-based quotas for tenants.
	// Used by the opt-in enforce_cost_budget step.
	CostQuotaManager *quota.CostQuotaManager
	// APIKeyResolver resolves API keys with fallback chain:
	// X-API-Key header â†’ per-tenant-per-model â†’ per-tenant-default â†’ configured
	APIKeyResolver *APIKeyResolver
	// CacheExec dispatches buffered cache ops via pipeline. Nil = no batching.
	CacheExec OpFlusher
	// RegistryExec dispatches buffered registry PUT ops. Nil = no batching.
	RegistryExec OpFlusher
	// RemoteRL is the distributed rate limit provider (e.g. Redis-backed).
	// Nil when distributed rate limiting is not configured — falls back to local counters.
	RemoteRL ExternalRateLimitProvider
	// InstanceCountFn returns the number of live gateway instances in this environment.
	// Used by CheckRateLimitV2 in approximate mode with DivideByNodes=true to split
	// the per-window limit evenly across pods. Nil = assume 1 instance (no division).
	InstanceCountFn func() int
	// SpikeArrestStore holds per-bucket last-allowed timestamps for all spike_arrest steps.
	// Pre-allocated at startup; zero heap allocation per request.
	SpikeArrestStore *SpikeArrestStore
	// CircuitBreakerArena holds all named circuit breaker states (max 256).
	// Slots are allocated at bake time; state is updated atomically at request time.
	CircuitBreakerArena *CircuitBreakerArena
	// EgressLimiterStore holds per-resource outbound rate limit counters and
	// shared broadcast channels for all egress_rate_limit steps.
	EgressLimiterStore *EgressLimiterStore
	// EgressLimiterStoreRedis is the Redis-backed variant of EgressLimiterStore.
	// Used when an egress_rate_limit step sets backend: "redis".
	// Nil until first use; remoteRL is wired by the compiler when RemoteRL is set.
	EgressLimiterStoreRedis *EgressLimiterStore
	// InstrRing is the lock-free slab ring for per-instruction timing aggregation.
	// Nil when the feature is disabled. Set from main.go before serving traffic.
	InstrRing *observability.InstrSlabRing
	// MQTTPool holds MQTT broker connections keyed by broker name.
	// Populated at startup from gateway config; nil when MQTT is not configured.
	// Set on each request context via Pool.New so mqtt_publish/mqtt_call steps can access it.
	MQTTPool *mqttpool.BrokerPool
}

func NewFlowManager(maxAPIs int, cfg config.GlobalLayout) *FlowManager {
	strategy := StrategySync
	if runtime.NumCPU() > 2 {
		strategy = StrategyParallel
	}
	fm := &FlowManager{
		Config:              cfg,
		Strategy:            strategy,
		TxIDGen:             rctx.NewTxIDGenerator(),
		RateLimitStore:      NewCounterStore(1 << 20), // 1M slots = 8 MB
		CostQuotaManager:    quota.NewCostQuotaManager(),
		APIKeyResolver:      NewAPIKeyResolver(),
		SpikeArrestStore:    NewSpikeArrestStore(),
		CircuitBreakerArena: NewCircuitBreakerArena(),
		EgressLimiterStore:      NewEgressLimiterStore(),
		EgressLimiterStoreRedis: NewEgressLimiterStore(),
	}
	gatewaylog.Default.Info("startup", gatewaylog.F("fingerprint", fm.TxIDGen.Fingerprint()))

	// Initialize with an empty but valid state
	initialState := &EngineState{
		Router:            router.New(),
		Definitions:       make([]*ApiDefinition, maxAPIs),
		FlowLibrary:       make(map[string][]Instruction),
		FlowSlotRegistry:  make(map[string]map[string]int),
		RouteUpstreamUrls: make(map[uint64]*UpstreamUrlInfo),
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
		// Wire op-buffer auto-flush only when a pipeline executor is configured.
		if fm.CacheExec != nil || fm.RegistryExec != nil {
			ctx.MaxOps = rctx.DefaultMaxOps
			ctx.OnFlush = fm.flushOps
		}
		// Wire MQTT pool — nil when MQTT is not configured.
		ctx.MQTTPool = fm.MQTTPool
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

	// 2a. Inject route constants (pre-baked at deploy time, zero alloc at request time).
	// Direct array writes (~2 ns each) — no map lookup, no string key, no allocation.
	routeKey := uint64(ctx.ApiId)<<8 | uint64(ctx.EndpointId)
	if state.RouteConstants != nil {
		if slots := state.RouteConstants[routeKey]; len(slots) > 0 {
			for i := range slots {
				ctx.ByteSlots[slots[i].SlotIdx] = slots[i].Value
			}
		}
	}

	// 2b. Inject gateway-native upstream URL (if configured for this route).
	// This runs before the flow so the upstream_url slot is populated regardless
	// of whether the flow contains a url_var step.
	if state.RouteUpstreamUrls != nil {
		if info := state.RouteUpstreamUrls[routeKey]; info != nil {
			fm.injectUpstreamUrl(ctx, req, info)
		}
	}

	// 3. Assign a globally-unique transaction ID for this request.
	ctx.InternalTxID = fm.TxIDGen.Generate(ctx.Timing.StartNs)

	// 4. Metadata Extraction
	fm.Extract(ctx, req)

	// 5. Plan Execution
	Execute(ctx, endpoint.Plan, 0)

	// 5b. Engine-level error fallback: if the plan ended with an unhandled failure
	// and no step committed a >=400 response status, synthesize a generic 500.
	// Steps that intentionally fail (dpop, geo_block, etc.) set ResponseStatus >= 400,
	// so ctx.ResponseStatus < 400 prevents overriding their responses.
	if ctx.Failed && ctx.ResponseStatus < 400 {
		applyGenericError(ctx)
	}

	// 5a. Async instruction-timing aggregation: stamp into the lock-free slab ring.
	// Nil-gated so it's zero-cost when the feature is not enabled.
	if fm.InstrRing != nil && ctx.InstrCount > 0 {
		var traced uint8
		if ctx.Trace != nil {
			traced = 1
		}
		// Build InstrBatch by value — Write takes it by value too, so no
		// pointer into ctx or into this stack frame is retained. ctx can be
		// returned to the pool the moment ProcessRequest returns.
		var batch observability.InstrBatch
		batch.TimestampNs = ctx.Timing.StartNs
		batch.APIID = uint32(ctx.ApiId)
		batch.VersionID = def.VersionID
		batch.TenantID = ctx.TenantID
		batch.EndpointID = ctx.EndpointId
		batch.Traced = traced
		batch.Count = ctx.InstrCount
		copy(batch.PCs[:ctx.InstrCount], ctx.InstrPC[:ctx.InstrCount])
		copy(batch.DurNs[:ctx.InstrCount], ctx.InstrDurNs[:ctx.InstrCount])
		fm.InstrRing.Write(batch)
	}

	// 6. Flush any storage ops that accumulated but didn't hit MaxOps.
	if (fm.CacheExec != nil || fm.RegistryExec != nil) && ctx.OpCount > 0 {
		fm.flushOps(ctx)
	}
}

// ProcessFlow executes a named flow directly, bypassing HTTP routing.
// Used for WebSocket message flows and scheduled flows.
func (fm *FlowManager) ProcessFlow(ctx *rctx.Context, flowName string) {
	state := fm.State.Load()
	if state == nil {
		return
	}
	instructions := state.GetFlowByName(flowName)
	if instructions == nil {
		gatewaylog.Default.Warn("[ProcessFlow] flow not found", gatewaylog.F("flow", flowName))
		return
	}
	Execute(ctx, instructions, 0)
}

// injectUpstreamUrl writes the upstream URL for the current route into the
// destination ByteSlot before the flow executes.
//
// Resolution order matches UpstreamUrlSource:
//   static      — write the pre-baked literal URL directly (zero alloc, ~2 ns).
//   registry    — look up the per-tenant URL by pre-resolved KeyID (~2—5 ns).
//   cache       — perform a synchronous Get using info.Value as the key (~500 ns).
//   header      — read the named HTTP header (zero-copy via unsafe.Slice).
//   queryparam  — scan raw query string for the named parameter.
func (fm *FlowManager) injectUpstreamUrl(ctx *rctx.Context, req *http.Request, info *UpstreamUrlInfo) {
	dest := info.SlotIdx
	switch info.Source {
	case UpstreamUrlSourceStatic:
		// Pre-allocated at bake time; no per-request allocation.
		ctx.ByteSlots[dest] = info.Value

	case UpstreamUrlSourceRegistry:
		if val, ok := registry.GetURLByKeyID(ctx.TenantID, info.RegistryKeyID); ok {
			ctx.ByteSlots[dest] = val
		}

	case UpstreamUrlSourceCache:
		if fm.SlabMgr != nil && len(info.Value) > 0 {
			if val, ok := fm.SlabMgr.Get(ctx.TenantID, info.Value); ok {
				ctx.ByteSlots[dest] = val
			}
		}

	case UpstreamUrlSourceHeader:
		if len(info.Value) > 0 {
			val := req.Header.Get(*(*string)(unsafe.Pointer(&info.Value)))
			if len(val) > 0 {
				ctx.ByteSlots[dest] = unsafe.Slice(unsafe.StringData(val), len(val))
			}
		}

	case UpstreamUrlSourceQueryParam:
		if len(info.Value) > 0 {
			// Scan raw query for the named parameter.
			query := ctx.RawQuery
			key := info.Value
			for len(query) > 0 {
				var seg []byte
				if i := bytesIndexByte(query, '&'); i >= 0 {
					seg, query = query[:i], query[i+1:]
				} else {
					seg, query = query, nil
				}
				if len(seg) > len(key)+1 && seg[len(key)] == '=' && bytesEqual(seg[:len(key)], key) {
					raw := seg[len(key)+1:]
					s := ctx.Alloc(len(raw))
					copy(s, raw)
					ctx.ByteSlots[dest] = s
					break
				}
			}
		}
	}
}

// bytesIndexByte returns the index of c in b, or -1 if not found.
// Avoids importing bytes package just for this hot-path helper.
func bytesIndexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// bytesEqual reports whether a and b are equal.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GetContext acquires a Context from the GC-resistant pool.
// Callers must call ReturnContext when done.
func (fm *FlowManager) GetContext() *rctx.Context {
	return fm.Pool.Get().(*rctx.Context)
}

// ReturnContext records overflow metrics, releases pool-borrowed overflow
// resources, then returns the context to the pool.
// Must be called instead of pool.Put directly.
func (fm *FlowManager) ReturnContext(ctx *rctx.Context) {
	if ctx.ArenaOverflowed {
		fm.Metrics.ArenaOverflows.Add(1)
	}
	ctx.ReleaseOverflow()
	fm.Pool.Put(ctx)
}

// flushOps drains ctx.Ops, partitions them by target (cache vs registry),
// and submits each group to the appropriate executor concurrently.
// For batches that contain GETs or synchronous PUTs it blocks until the
// executor signals Done, then writes GET results back to ByteSlots.
// Async-only batches are submitted fire-and-forget (Done = nil).
// Ops are always copied so opsBase can be reused immediately.
func (fm *FlowManager) flushOps(ctx *rctx.Context) {
	if ctx.OpCount == 0 {
		return
	}

	n := ctx.OpCount
	ops := make([]rctx.StorageOp, n)
	copy(ops, ctx.Ops[:n])
	ctx.ResetOps()

	// Partition ops by target family.
	var cacheOps, registryOps []rctx.StorageOp
	for i := range ops {
		switch ops[i].Target {
		case rctx.TargetCache:
			cacheOps = append(cacheOps, ops[i])
		case rctx.TargetRegistryURL, rctx.TargetRegistryID, rctx.TargetRegistryMeta:
			registryOps = append(registryOps, ops[i])
		}
	}

	// Determine sync requirement per group.
	var cacheDone, registryDone chan struct{}

	if fm.CacheExec != nil && len(cacheOps) > 0 {
		needsSync := false
		for i := range cacheOps {
			if cacheOps[i].Type == rctx.OpGet || !cacheOps[i].Async {
				needsSync = true
				break
			}
		}
		if !needsSync {
			for i := range cacheOps {
				cacheOps[i].Key = append([]byte(nil), cacheOps[i].Key...)
				if cacheOps[i].Value != nil {
					cacheOps[i].Value = append([]byte(nil), cacheOps[i].Value...)
				}
			}
		} else {
			cacheDone = make(chan struct{}, 1)
		}
		fm.CacheExec.Submit(rctx.Batch{Ops: cacheOps, Done: cacheDone})
	}

	if fm.RegistryExec != nil && len(registryOps) > 0 {
		needsSync := false
		for i := range registryOps {
			if registryOps[i].Type == rctx.OpGet || !registryOps[i].Async {
				needsSync = true
				break
			}
		}
		if !needsSync {
			for i := range registryOps {
				registryOps[i].Key = append([]byte(nil), registryOps[i].Key...)
				if registryOps[i].Value != nil {
					registryOps[i].Value = append([]byte(nil), registryOps[i].Value...)
				}
			}
		} else {
			registryDone = make(chan struct{}, 1)
		}
		fm.RegistryExec.Submit(rctx.Batch{Ops: registryOps, Done: registryDone})
	}

	// Wait for both concurrently.
	if cacheDone != nil {
		<-cacheDone
		for i := range cacheOps {
			if cacheOps[i].Type == rctx.OpGet && cacheOps[i].Result != nil {
				if cacheOps[i].DestSlot < len(ctx.ByteSlots) {
					ctx.ByteSlots[cacheOps[i].DestSlot] = cacheOps[i].Result
				}
			}
		}
	}
	if registryDone != nil {
		<-registryDone
	}
}

// LimiterEnabled reports whether the concurrency gate is currently active.
// Called on every request — single atomic load, ~1 ns, no allocation.
func (fm *FlowManager) LimiterEnabled() bool { return fm.limiterEnabled.Load() }

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
						_, _ = writer.Write(b)
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
- RahRouter resolves base path â†’ ApiDefinition.

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

// pathHasStrPrefix reports whether the []byte path starts with the string prefix.
// Zero allocation: compares bytes directly without converting string â†’ []byte.
func pathHasStrPrefix(path []byte, prefix string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

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

	// Determine which registered basepath was matched for this request.
	// Primary path is checked first (fast path, covers 99% of traffic).
	// Alias walk only runs when an alias basepath was matched by the router.
	// Zero allocation: direct byte-by-byte prefix check avoids string conversion.
	baseLen := len(def.BaseRawPath)
	if !pathHasStrPrefix(ctx.Path, def.BaseRawPath) {
		baseLen = 0
		for _, alias := range def.AliasPaths {
			if pathHasStrPrefix(ctx.Path, alias) {
				baseLen = len(alias)
				break
			}
		}
	}
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
			// Store offsets into ctx.Path (not relPath) so BindPath can slice ctx.Path.
			// BindPath reads ctx.Match.Params[N] and writes to ctx.ByteSlots[slot],
			// matching the same pattern as BindHeader/BindQuery.
			if ctx.Match.ParamCount < len(ctx.Match.Params) {
				absStart := uint32(baseLen + start)
				absEnd := uint32(baseLen + strPos)
				ctx.Match.Params[ctx.Match.ParamCount] = rctx.ParamOffset{Start: absStart, End: absEnd}
				ctx.Match.ParamCount++
			}
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

