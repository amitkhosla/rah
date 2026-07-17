package rctx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"github.com/amitkhosla/rah/internal/mqtt"
	"github.com/amitkhosla/rah/internal/observability"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// nanotime returns the current time in nanoseconds.
// Kept as a thin wrapper so it can be swapped in tests.
func nanotime() int64 { return time.Now().UnixNano() }

// ResponseWriter abstracts the network socket. This allows the Context
// to remain decoupled from net/http and supports easier unit testing.
type ResponseWriter interface {
	Write([]byte) (int, error)
	WriteHeader(statusCode int)
	Header() http.Header
}

// HeaderMutation tracks changes for the upstream proxy without string allocations.
type HeaderMutation struct {
	Key   []byte
	Value []byte
	Op    uint8 // 0: Set, 1: Remove
}

// LogFieldEntry records a named slot value to be written into the access log Extra slice.
// Name is a static string baked at compile time; Slot is a ByteSlot index read at request time.
type LogFieldEntry struct {
	Name string
	Slot int
}

// RequestTiming holds per-request timing counters and byte metrics.
// It is embedded by value in Context so all fields are contiguous in memory
// (64 bytes = exactly one cache line) with zero pointer indirection.
// Reset is a single memclr: ctx.Timing = RequestTiming{}
type RequestTiming struct {
	StartNs         int64 // request start â€” set by FlowManager before Execute
	FirstByteSentNs int64 // when first byte was written to client (TTFB)
	LastByteSentNs  int64 // when last byte was written to client (transfer complete)
	UpstreamTimeNs  int64 // total upstream latency (atomic-added per call)
	UpstreamCalls   int32 // upstream calls made this request
	_               int32 // alignment pad â†’ 64 bytes total, one cache line
	ClientBytesSent int64 // bytes written to client
	UpstreamBytesTx int64 // bytes sent upstream
	UpstreamBytesRx int64 // bytes received from upstream
}

// internal/rctx/context.go

type ParamOffset struct {
	Start uint32
	End   uint32
}

type RouteMatch struct {
	Plan       any
	ParamCount int
	// We limit to 10 params per API. This is a hard-coded architectural limit
	// that ensures the struct size remains constant and cache-friendly.
	Params [10]ParamOffset
}

type Context struct {
	ApiId uint32
	Match RouteMatch

	// Metadata (Zero-allocation snapshots)
	Method        []byte
	Path          []byte
	RemainingPath []byte

	RawQuery       []byte
	metadataBuffer [1024]byte
	overflowBuffer []byte

	// Typed Slots (The Logic Arena)
	ByteSlots [][]byte
	IntSlots  []int64
	BoolSlots []bool

	// Proxy State
	MutationLog   []HeaderMutation
	MutationCount int

	// Staged body for the next http_call. Set by set_request_body, consumed and
	// cleared by http_call. Zero cost when unused (nil slice header).
	StagedRequestBody []byte
	StagedContentType []byte

	// MutationFences marks the MutationLog start index per http_call slot (max 8
	// concurrent calls). Enables each call to apply only its own mutations.
	MutationFences [8]int8

	// Access log extra fields â€” populated by log_field steps during flow execution.
	// Read post-response to append named slot values to the access log.
	ExtraLogFields [8]LogFieldEntry
	ExtraLogCount  uint8

	// Request Body & Buffering
	MaxBodySize   int64
	Request       *http.Request
	RequestBody   io.ReadCloser
	RequestBuffer []byte // Populated only if IsBuffered = true
	ScratchBuffer []byte // Physical RAM for Merges/Small Temp Objects
	scratchIdx    int    // Cursor for ScratchBuffer
	IsBuffered    bool   // Flag to toggle between Streaming and Transformation modes

	// Response Handling
	Writer          ResponseWriter
	ResponseStatus  int
	ResponseBuffer  []byte // Collects data if IsBuffered = true
	headerSent      bool
	ResponseHeaders []HeaderMutation // Pre-allocated in Pool
	ResHeaderCount      int
	StreamResponseBody  bool // set by compiler: true = http_call streams body directly to client
	TenantID  uint16
	TenantKey string // human-readable tenant identifier (set by registry_lookup)

	// CallerID identifies the consumer (AppKey ID in future; always 0 today).
	// Reserved to avoid a structural rewrite when AppKey auth is introduced.
	CallerID uint32

	// CallerKey is the API key alias set by validate_api_key on success.
	// Human-readable, used in access logs. Complements CallerID (AppID).
	CallerKey string

	// TestMode is true when this context is being executed by the test runner
	// (POST /test/execute). Steps must NOT write to live cache/registry/datastore;
	// writes are either suppressed or go to a test-namespaced key.
	TestMode bool
	// TestRunID is a unique identifier for this test execution run.
	// Used as the namespace prefix for test-mode writes: __test__{TestRunID}:{key}
	// Set by the test runner before Execute() is called.
	TestRunID string

	// Routing identity â€” set by the engine at request time, zero cost
	// (plain struct field assignments).
	APIRateLimitId      uint16 // rate limit config for this API (set by resolveSubPath)
	EndpointRateLimitId uint16 // rate limit config for this endpoint; 0 = inherit API level
	EndpointId          uint8  // which sub-route matched within this API (0â€“255)
	QuotaGroupID        uint8  // quota group (Phase 3)

	// Obs holds all per-request timing counters embedded by value â€” zero indirection,
	// single cache line. Tel and Trace are nil-gated optional subsystems.
	Timing RequestTiming
	Obs    *observability.Telemetry
	Trace *observability.RequestTrace

	// MQTTPool is the global MQTT broker pool (set by FlowManager at startup).
	// Shared across all requests; read-only after initialization.
	MQTTPool *mqtt.BrokerPool

	// detachedFromPool prevents the request goroutine from returning this
	// context to the pool when work is moved to a background goroutine.
	detachedFromPool atomic.Bool

	// â”€â”€ Arena allocator â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	// arenaUsed is the number of bytes consumed in arenaInline.
	arenaUsed int32

	// arenaExt is a single pool-borrowed 4KB block used when arenaInline fills.
	// Nil in the common case (most requests fit in 1KB inline).
	arenaExt *arenaBlock

	// ArenaOverflowed is true if any pool-borrowed or heap fallback was needed.
	ArenaOverflowed bool

	// Failed is set to true by any step that encounters a non-recoverable error.
	// It is distinct from intentional stops (early_return sets Failed=false).
	// Cleared by on_error:continue wrappers or on_error:jump wrappers.
	Failed bool

	// Cancelled is set atomically to 1 when the client disconnects mid-flow.
	// Steps check this at IO boundaries and return StopCancelled (-2).
	// Use atomic.LoadInt32/StoreInt32 â€” never read directly.
	Cancelled int32

	// parallelForkActive is true during the parallel fan-out window.
	// Debug guard: any attempt to write to shared arena while this is set
	// indicates a missing BranchContext isolation.
	parallelForkActive bool
	// ErrorCode is an application-level error code set by the failing step.
	// 0 means no error. Typical values mirror HTTP status codes (401, 503) or
	// custom gateway codes (1001-1999).
	ErrorCode int16
	// ErrorMsg points into the arena (zero allocation). Set by the failing step.
	// Cleared together with Failed by error wrappers.
	ErrorMsg []byte

	// InternalTxID is a globally-unique transaction ID assigned per request
	// by FlowManager via TxIDGenerator. Use rctx.FormatTxID to format.
	InternalTxID [2]uint64

	// InstrPC / InstrDurNs / InstrCount accumulate per-instruction timing
	// during Execute(). Stored here (not on ExecutionState) so Execute() can
	// return void, avoiding a ~1200-byte struct copy + GC pointer scan per
	// request. InstrCount is zeroed in Reset(); the arrays are overwritten
	// in-place so they need no explicit clear.
	InstrPC    [64]int16
	InstrDurNs [64]int32
	InstrCount uint8
	// InstrOverflow is the linked chain of instruction slots beyond the first 64.
	// Nil in the common case. Each block holds up to 64 more (PC, durNs) pairs.
	InstrOverflow *InstrBlock

	// LLMCalls is the linked chain of LLM call entries captured during this request.
	// Nil when no LLM calls were made. Read post-Execute by PersistTrace to build LLMCallRows.
	LLMCalls *observability.LLMCallBlock

	// AfterResponse holds zero-allocation callbacks invoked by the gateway
	// after the HTTP response is committed. Used by ingest steps to emit
	// events (e.g. the final response body) without blocking the caller.
	// Nil slice is safe; the gateway checks len before ranging.
	AfterResponse []func()

	// â”€â”€ Timeout / context.Context implementation â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	// Grouped into one cache-line block to prevent false-sharing from
	// sync.Once.mu. Only touched during upstream calls â€” never in the executor
	// hot path. Byte layout: 24+8+16+4+4+8+1 = 65 bytes (compiler pads to 72).
	requestDeadline    time.Time     // 24 bytes: deadline set per upstream call
	doneChan           chan struct{}  // 8 bytes: closed on cancel/timeout; nil = no timeout
	cancelOnce         sync.Once     // 16 bytes: ensures doneChan closed exactly once
	cancelState        int32         // 4 bytes: 0=none 1=DeadlineExceeded 2=Canceled
	timedOut           int32         // 4 bytes: 1 if deadline exceeded (read by access log)
	generation         atomic.Uint64 // 8 bytes: guards stale timer callbacks after pool reuse
	hadUpstreamTimeout bool          // 1 byte: true if SetUpstreamTimeout called this req; gates Reset cleanup

	// â”€â”€ Inline slot headers (no heap allocation) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	// ByteSlots / IntSlots / BoolSlots are slice headers that point into these
	// arrays. No make() required; GC correctly scans the typed []byte elements.
	// Declared last so they do not displace hot scalar fields from cache lines.
	byteSlotBase [BaseByteSlots][]byte
	intSlotBase  [BaseIntSlots]int64
	boolSlotBase [BaseBoolSlots]bool

	// arenaInline is the 1KB always-inline arena for slot data. Raw bytes only
	// â€” no Go pointers â€” so GC never scans its contents. Declared last (large).
	arenaInline [ArenaInlineSize]byte

	// â”€â”€ Op buffer â€” per-request storage operation queue â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
	// opKeysBuf is a dedicated inline buffer for constructing op keys at runtime
	// (prefix + extracted value). Kept separate from arenaInline so key construction
	// does not compete with slot data. Reset by ResetOps().
	opKeysBuf  [512]byte
	opKeysUsed int32

	// opsBase is the inline backing array; Ops is a slice header over it.
	// len(Ops) == OpCount always. MaxOps and OnFlush are set by FlowManager
	// at request start and survive Reset() (pool-level config).
	opsBase  [DefaultMaxOps]StorageOp // never heap-allocated
	Ops      []StorageOp              // slice header: opsBase[:OpCount]
	OpCount  int                      // valid op count; always == len(Ops)
	MaxOps   int                      // 0 = no auto-flush; set from config
	OnFlush  func(ctx *Context)       // called when OpCount >= MaxOps; nil = disabled
}

// flushResponseHeaders copies any headers set via SetResponseHeader to the
// underlying http.ResponseWriter. Must be called before WriteHeader â€” after
// WriteHeader is called, header changes have no effect in net/http.
func (ctx *Context) flushResponseHeaders() {
	if ctx.ResHeaderCount == 0 {
		return
	}
	h := ctx.Writer.Header()
	for i := 0; i < ctx.ResHeaderCount; i++ {
		m := ctx.ResponseHeaders[i]
		if m.Op == 1 {
			h.Del(string(m.Key))
		} else {
			h.Set(string(m.Key), string(m.Value))
		}
	}
}

// Write is the universal entry point for all instructions.
// It handles the "Write Once" constraint and switches behavior based on IsBuffered.
func (ctx *Context) Write(p []byte) (n int, err error) {
	if ctx.IsBuffered {
		// Flavor 2: Collecting for transformation/inspection
		ctx.ResponseBuffer = append(ctx.ResponseBuffer, p...)
		return len(p), nil
	}

	// Flavor 1: Direct Streaming
	if !ctx.headerSent {
		if ctx.Timing.FirstByteSentNs == 0 {
			ctx.Timing.FirstByteSentNs = nanotime()
		}
		ctx.flushResponseHeaders()
		ctx.Writer.WriteHeader(ctx.ResponseStatus)
		ctx.headerSent = true
	}
	n, err = ctx.Writer.Write(p)
	if n > 0 {
		ctx.Timing.ClientBytesSent += int64(n)
		ctx.Timing.LastByteSentNs = nanotime()
	}
	return n, err
}

// Finalize handles the "Last Mile" of the response.
// If data was buffered, it flushes it to the wire in one go.
func (ctx *Context) Finalize() {
	if ctx.headerSent {
		return
	}
	if ctx.Timing.FirstByteSentNs == 0 {
		ctx.Timing.FirstByteSentNs = nanotime()
	}
	ctx.flushResponseHeaders()
	ctx.Writer.WriteHeader(ctx.ResponseStatus)
	if ctx.IsBuffered && len(ctx.ResponseBuffer) > 0 {
		n, _ := ctx.Writer.Write(ctx.ResponseBuffer)
		if n > 0 {
			ctx.Timing.ClientBytesSent += int64(n)
		}
	}
	ctx.headerSent = true
	ctx.Timing.LastByteSentNs = nanotime()
}

// InitSlots wires the public ByteSlots / IntSlots / BoolSlots slice headers
// to the inline base arrays. Called once from Pool.New â€” no heap allocation.
// arenaUsed=0 and arenaExt=nil are already the zero values.
func (ctx *Context) InitSlots() {
	ctx.ByteSlots = ctx.byteSlotBase[:BaseByteSlots]
	ctx.IntSlots = ctx.intSlotBase[:BaseIntSlots]
	ctx.BoolSlots = ctx.boolSlotBase[:BaseBoolSlots]
	ctx.Ops = ctx.opsBase[:0]
	ctx.doneChan = make(chan struct{}) // pre-allocate for context.Context impl
}

// â”€â”€ context.Context implementation â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// Deadline implements context.Context.
func (c *Context) Deadline() (time.Time, bool) {
	if c.requestDeadline.IsZero() {
		return time.Time{}, false
	}
	return c.requestDeadline, true
}

// Done implements context.Context. Returns nil when no timeout is active â€”
// net/http (and context.propagateCancel) treat nil as "never cancel", skipping
// the watcher goroutine on every upstream call. doneChan is returned only when
// a deadline is armed so the timer callback can abort the in-flight request.
func (c *Context) Done() <-chan struct{} {
	if c.requestDeadline.IsZero() {
		return nil
	}
	return c.doneChan
}

// Err implements context.Context.
func (c *Context) Err() error {
	switch atomic.LoadInt32(&c.cancelState) {
	case 1:
		return context.DeadlineExceeded
	case 2:
		return context.Canceled
	default:
		return nil
	}
}

// Value implements context.Context. Delegates to the inbound request context
// so trace spans and other values set by middleware are still propagated.
func (c *Context) Value(key any) any {
	if c.Request != nil {
		return c.Request.Context().Value(key)
	}
	return nil
}

// SetUpstreamTimeout arms the per-call deadline on ctx and returns the current
// generation token that the caller must capture before launching time.AfterFunc.
// The caller passes the token to CancelIfGeneration so stale callbacks are no-ops.
//
//	capturedGen := ctx.SetUpstreamTimeout(200 * time.Millisecond)
//	timer = time.AfterFunc(200*time.Millisecond, func() {
//	    ctx.CancelIfGeneration(capturedGen, context.DeadlineExceeded)
//	})
func (c *Context) SetUpstreamTimeout(d time.Duration) uint64 {
	c.hadUpstreamTimeout = true
	c.requestDeadline = time.Now().Add(d)
	if c.doneChan == nil {
		c.doneChan = make(chan struct{})
	}
	return c.generation.Load()
}

// ClearUpstreamTimeout resets the per-call deadline after the upstream call
// completes (or when the request-level deadline timer is stopped).
func (c *Context) ClearUpstreamTimeout() {
	c.requestDeadline = time.Time{}
}

// CancelIfGeneration calls Cancel(err) only if the current generation matches
// capturedGen. Use this from time.AfterFunc callbacks to avoid acting on a
// context that has already been returned to the pool and reused.
func (c *Context) CancelIfGeneration(capturedGen uint64, err error) {
	if c.generation.Load() != capturedGen {
		return
	}
	c.Cancel(err)
}

// TimedOut reports whether the upstream call exceeded its deadline.
// Read by the access log after Execute() returns.
func (c *Context) TimedOut() bool {
	return atomic.LoadInt32(&c.timedOut) != 0
}

// Cancel marks the context as cancelled with the given error and closes doneChan.
// Safe to call from any goroutine; closes doneChan exactly once.
func (c *Context) Cancel(err error) {
	state := int32(2) // Canceled
	if err == context.DeadlineExceeded {
		state = 1
	}
	c.cancelOnce.Do(func() {
		atomic.StoreInt32(&c.cancelState, state)
		if err == context.DeadlineExceeded {
			atomic.StoreInt32(&c.timedOut, 1)
		}
		if c.doneChan != nil {
			close(c.doneChan)
		}
	})
}

// Alloc carves n bytes from the arena without any heap allocation in the
// common case (value fits in 1KB inline arena). Falls back to a single
// pool-borrowed 4KB ext block, then to make() only for values > 4KB (rare).
//
// The returned slice is valid until ReleaseOverflow is called.
func (ctx *Context) Alloc(n int) []byte {
	// Fast path: fits in inline arena (no pool, no GC).
	end := int(ctx.arenaUsed) + n
	if end <= ArenaInlineSize {
		s := ctx.arenaInline[ctx.arenaUsed:end:end]
		ctx.arenaUsed = int32(end)
		return s
	}

	// Borrow single 4KB ext block from pool when inline fills.
	if ctx.arenaExt == nil {
		b := arenaPool.Get().(*arenaBlock)
		b.used = 0
		ctx.arenaExt = b
		ctx.ArenaOverflowed = true
	}
	b := ctx.arenaExt
	end2 := int(b.used) + n
	if end2 <= ArenaBlockSize {
		s := b.buf[b.used:end2:end2]
		b.used = int32(end2)
		return s
	}

	// Value > 4KB â€” heap fallback (very rare). Execution is never blocked.
	ctx.ArenaOverflowed = true
	return make([]byte, n)
}

// AllocOpKey carves n bytes from opKeysBuf for op key construction.
// Falls back to arenaInline when opKeysBuf is exhausted (rare).
// Never heap-allocates in the common case.
func (ctx *Context) AllocOpKey(n int) []byte {
	end := int(ctx.opKeysUsed) + n
	if end <= len(ctx.opKeysBuf) {
		s := ctx.opKeysBuf[ctx.opKeysUsed:end]
		ctx.opKeysUsed = int32(end)
		return s
	}
	return ctx.Alloc(n)
}

// ReleaseOverflow returns the pool-borrowed ext block (if any) back to the
// pool. Must be called before Pool.Put so the block is available immediately.
func (ctx *Context) ReleaseOverflow() {
	if ctx.arenaExt != nil {
		ctx.arenaExt.used = 0
		arenaPool.Put(ctx.arenaExt)
		ctx.arenaExt = nil
	}
}

// Reset clears the context for reuse in the sync.Pool.
// We pass the concrete writer here for the new request.
// ReleaseOverflow must have been called before Pool.Put (done by
// FlowManager.ReturnContext) so Reset only needs to reset arenaUsed.
func (ctx *Context) Reset(w ResponseWriter) {
	ctx.Writer = w
	ctx.Request = nil
	ctx.ResHeaderCount = 0
	ctx.ApiId = 0
	ctx.MutationCount = 0
	ctx.ExtraLogCount = 0
	ctx.Match.Plan = nil
	ctx.Match.ParamCount = 0

	// Reset Response State
	ctx.ResponseStatus = 200
	ctx.headerSent = false
	ctx.IsBuffered = false
	ctx.StreamResponseBody = false
	ctx.detachedFromPool.Store(false)
	ctx.TenantKey = ""
	ctx.TenantID = 0
	ctx.CallerID = 0
	ctx.CallerKey = ""
	ctx.TestMode = false
	ctx.TestRunID = ""
	ctx.APIRateLimitId = 0
	ctx.EndpointRateLimitId = 0
	ctx.EndpointId = 0
	ctx.QuotaGroupID = 0
	ctx.Timing = RequestTiming{} // single memclr â€” all 8 timing fields zeroed at once
	ctx.Obs = nil
	ctx.Trace = nil
	ctx.ArenaOverflowed = false
	ctx.Failed = false
	ctx.Cancelled = 0
	ctx.parallelForkActive = false
	ctx.StagedRequestBody = nil
	ctx.StagedContentType = nil
	// MutationFences is [8]int8 â€” zeroed implicitly via the explicit zero below.
	ctx.MutationFences = [8]int8{}
	ctx.ErrorCode = 0
	ctx.ErrorMsg = nil
	ctx.InternalTxID = [2]uint64{}
	ctx.InstrCount = 0
	ctx.InstrOverflow = nil
	ctx.LLMCalls = nil
	ctx.AfterResponse = ctx.AfterResponse[:0] // keep capacity, drop closures

	// Reset timeout / context.Context state â€” only when a timeout was actually
	// armed this request. Static and no-upstream flows skip this block entirely,
	// saving 5 atomic ops (2 loads + Add + 2 stores) per request.
	if ctx.hadUpstreamTimeout {
		ctx.hadUpstreamTimeout = false
		// Read cancelState/timedOut BEFORE zeroing â€” needed to decide if
		// doneChan was closed and must be replaced.
		needNewChan := ctx.doneChan == nil ||
			atomic.LoadInt32(&ctx.cancelState) != 0 ||
			atomic.LoadInt32(&ctx.timedOut) != 0
		// Increment generation so any in-flight timer callback is a no-op
		// if it fires after ctx is returned to the pool.
		ctx.generation.Add(1)
		ctx.requestDeadline = time.Time{}
		ctx.cancelOnce = sync.Once{}
		atomic.StoreInt32(&ctx.cancelState, 0)
		atomic.StoreInt32(&ctx.timedOut, 0)
		if needNewChan {
			ctx.doneChan = make(chan struct{})
		}
	}

	// Reset inline arena â€” one integer write, all slot data is implicitly gone.
	// arenaExt is already nil after ReleaseOverflow in ReturnContext.
	ctx.arenaUsed = 0

	// Clean up body streams
	if ctx.RequestBody != nil {
		_ = ctx.RequestBody.Close()
		ctx.RequestBody = nil
	}

	// Reset slices but keep capacity for efficiency.
	// If buffers grew abnormally large (>64KB), we nil them to avoid memory hangovers.
	const maxHangover = 64 * 1024
	if cap(ctx.ResponseBuffer) > maxHangover {
		ctx.ResponseBuffer = nil
	} else {
		ctx.ResponseBuffer = ctx.ResponseBuffer[:0]
	}

	if cap(ctx.RequestBuffer) > maxHangover {
		ctx.RequestBuffer = nil
	} else {
		ctx.RequestBuffer = ctx.RequestBuffer[:0]
	}

	ctx.Method = nil
	ctx.Path = nil
	ctx.RemainingPath = nil
	ctx.RawQuery = nil

	// Nil inline slot bases â€” arena memory is already logically freed above.
	for i := range ctx.byteSlotBase {
		ctx.byteSlotBase[i] = nil
	}
	for i := range ctx.intSlotBase {
		ctx.intSlotBase[i] = 0
	}
	for i := range ctx.boolSlotBase {
		ctx.boolSlotBase[i] = false
	}
	// Re-point public slice headers at the (now-zeroed) inline bases.
	ctx.ByteSlots = ctx.byteSlotBase[:BaseByteSlots]
	ctx.IntSlots = ctx.intSlotBase[:BaseIntSlots]
	ctx.BoolSlots = ctx.boolSlotBase[:BaseBoolSlots]

	ctx.scratchIdx = 0
	ctx.ScratchBuffer = ctx.ScratchBuffer[:0]

	// Reset op buffer â€” keep MaxOps and OnFlush (pool-level config).
	ctx.opKeysUsed = 0
	ctx.ResetOps()
}

// MarkDetachedFromPool signals that this context is still in use by async work
// and must not be returned to the pool by the request goroutine.
func (ctx *Context) MarkDetachedFromPool() {
	ctx.detachedFromPool.Store(true)
}

// ShouldReturnToPool reports whether the request goroutine can safely put this
// context back in the pool.
func (ctx *Context) ShouldReturnToPool() bool {
	return !ctx.detachedFromPool.Load()
}

func (ctx *Context) SnapshotMetadata(method, path, query string) {
	methodLen := len(method)
	pathLen := len(path)
	totalNeeded := methodLen + pathLen

	var storage []byte
	if totalNeeded <= len(ctx.metadataBuffer) {
		storage = ctx.metadataBuffer[:]
	} else {
		if cap(ctx.overflowBuffer) < totalNeeded {
			ctx.overflowBuffer = make([]byte, totalNeeded)
		}
		storage = ctx.overflowBuffer[:totalNeeded]
	}

	copy(storage[0:], method)
	ctx.Method = storage[0:methodLen]

	copy(storage[methodLen:], path)
	ctx.Path = storage[methodLen : methodLen+pathLen]

	if len(query) > 0 {
		ctx.RawQuery = unsafe.Slice(unsafe.StringData(query), len(query))
	} else {
		ctx.RawQuery = nil
	}
}

// MethodString converts the method byte slice back to a string for standard lib compatibility.
func (ctx *Context) MethodString() string {
	return unsafe.String(unsafe.SliceData(ctx.Method), len(ctx.Method))
}

func (ctx *Context) GetBodyReader() io.Reader {
	if ctx.IsBuffered {
		return bytes.NewReader(ctx.RequestBuffer)
	}
	return ctx.RequestBody // Or ctx.Request.Body
}

func (ctx *Context) SetResponseHeader(key []byte, value []byte) {
	if ctx.ResHeaderCount < len(ctx.ResponseHeaders) {
		ctx.ResponseHeaders[ctx.ResHeaderCount] = HeaderMutation{
			Key:   key,
			Value: value,
			Op:    0, // Set
		}
		ctx.ResHeaderCount++
	}
}

// GetWriter returns the underlying http.ResponseWriter
func (ctx *Context) GetWriter() http.ResponseWriter {
	return ctx.Writer // Assuming your ResponseWriter interface wraps the standard one
}

// FinalizeHeaders sends only the status and headers, allowing for a streaming body
func (ctx *Context) FinalizeHeaders() {
	if ctx.headerSent {
		return
	}

	h := ctx.Writer.Header()
	for i := 0; i < ctx.ResHeaderCount; i++ {
		m := ctx.ResponseHeaders[i]
		h.Set(string(m.Key), string(m.Value))
	}

	ctx.Writer.WriteHeader(ctx.ResponseStatus)
	ctx.headerSent = true
}

// Add these to internal/rctx/context.go

// GetCollection abstracts fetching arrays of data for loops
func (ctx *Context) GetCollection(key string) [][]byte {
	switch key {
	case "cookies":
		// Zero-alloc cookie extraction logic
		return [][]byte{} // Implementation here
	case "headers":
		// Return all values for a specific header
		return [][]byte{}
	default:
		return nil
	}
}

func (ctx *Context) SetSlot(idx int, val []byte) {
	if idx < len(ctx.ByteSlots) {
		ctx.ByteSlots[idx] = val
	}
}

func (ctx *Context) SetInt(idx int, val int64) {
	if idx < len(ctx.IntSlots) {
		ctx.IntSlots[idx] = val
	}
}

func (ctx *Context) GetInt(idx int) int64 {
	if idx < len(ctx.IntSlots) {
		return ctx.IntSlots[idx]
	}
	return 0
}
