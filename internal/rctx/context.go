package rctx

import (
	"bytes"
	"io"
	"net/http"
	"rah/internal/observability"
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
	ResHeaderCount  int
	TenantID        uint16
	TenantKey       string // human-readable tenant identifier (set by registry_lookup)
	Obs             *observability.Telemetry
	Trace           *observability.RequestTrace
	RequestStartNs  int64
	FirstByteSentNs int64 // when first byte was written to client — used for TTFB
	UpstreamTimeNs  int64
	UpstreamCalls   int32
	ClientBytesSent int64
	UpstreamBytesTx int64
	UpstreamBytesRx int64

	// detachedFromPool prevents the request goroutine from returning this
	// context to the pool when work is moved to a background goroutine.
	detachedFromPool atomic.Bool

	// ── Arena allocator ──────────────────────────────────────────────────────
	// active points to the current arena block being written into.
	// Starts as &primary; advances to extra[n] when primary fills.
	active *arenaBlock

	// extraN is the number of pool-borrowed overflow arena blocks in use.
	extraN int32

	// Overflow metrics — read by FlowManager.ReturnContext before Pool.Put.
	// True if any extra arena block or slot extension was needed this request.
	ArenaOverflowed bool
	SlotOverflowed  bool

	// slotExt is borrowed from slotExtPool when byte-slot count exceeds
	// BaseByteSlots. Nil for the vast majority of requests.
	slotExt *slotExtBlock

	// extra holds up to MaxExtraArenas pool-borrowed 4KB blocks.
	// GC sees these as pointer fields but they are nil until overflow occurs.
	extra [MaxExtraArenas]*arenaBlock

	// ── Inline slot headers (no heap allocation) ─────────────────────────────
	// ByteSlots / IntSlots / BoolSlots are slice headers that point into these
	// arrays. No make() required; GC correctly scans the typed []byte elements.
	// Declared last so they do not displace hot scalar fields from cache lines.
	byteSlotBase [BaseByteSlots][]byte
	intSlotBase  [BaseIntSlots]int64
	boolSlotBase [BaseBoolSlots]bool

	// primary is the inline 4KB arena for slot data. Raw bytes only — no Go
	// pointers — so GC never scans its contents. Declared last (largest field).
	primary arenaBlock
}

// flushResponseHeaders copies any headers set via SetResponseHeader to the
// underlying http.ResponseWriter. Must be called before WriteHeader — after
// WriteHeader is called, header changes have no effect in net/http.
func (ctx *Context) flushResponseHeaders() {
	if ctx.ResHeaderCount == 0 {
		return
	}
	h := ctx.Writer.Header()
	for i := 0; i < ctx.ResHeaderCount; i++ {
		m := ctx.ResponseHeaders[i]
		h.Set(string(m.Key), string(m.Value))
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
		if ctx.FirstByteSentNs == 0 {
			ctx.FirstByteSentNs = nanotime()
		}
		ctx.flushResponseHeaders()
		ctx.Writer.WriteHeader(ctx.ResponseStatus)
		ctx.headerSent = true
	}
	n, err = ctx.Writer.Write(p)
	if n > 0 {
		ctx.ClientBytesSent += int64(n)
	}
	return n, err
}

// Finalize handles the "Last Mile" of the response.
// If data was buffered, it flushes it to the wire in one go.
func (ctx *Context) Finalize() {
	if ctx.IsBuffered && !ctx.headerSent {
		if ctx.FirstByteSentNs == 0 {
			ctx.FirstByteSentNs = nanotime()
		}
		ctx.flushResponseHeaders()
		ctx.Writer.WriteHeader(ctx.ResponseStatus)
		n, _ := ctx.Writer.Write(ctx.ResponseBuffer)
		if n > 0 {
			ctx.ClientBytesSent += int64(n)
		}
		ctx.headerSent = true
	}
}

// InitSlots wires the public ByteSlots / IntSlots / BoolSlots slice headers
// to the inline base arrays and initialises the arena. Called once from
// Pool.New — no heap allocation, no make().
func (ctx *Context) InitSlots() {
	ctx.ByteSlots = ctx.byteSlotBase[:BaseByteSlots]
	ctx.IntSlots = ctx.intSlotBase[:BaseIntSlots]
	ctx.BoolSlots = ctx.boolSlotBase[:BaseBoolSlots]
	ctx.active = &ctx.primary
}

// Alloc carves n bytes from the arena without any heap allocation in the
// common case. Falls back to pool-borrowed extra blocks when the primary
// fills, then to make() only if all extra blocks are also exhausted (rare).
//
// The returned slice is valid until ReleaseOverflow is called.
func (ctx *Context) Alloc(n int) []byte {
	b := ctx.active
	end := int(b.used) + n
	if end <= ArenaBlockSize {
		s := b.buf[b.used:end:end]
		b.used = int32(end)
		return s
	}

	// Primary arena full — borrow next block from pool.
	if ctx.extraN < MaxExtraArenas {
		nb := arenaPool.Get().(*arenaBlock)
		nb.used = 0
		ctx.extra[ctx.extraN] = nb
		ctx.extraN++
		ctx.active = nb
		ctx.ArenaOverflowed = true
		if n <= ArenaBlockSize {
			s := nb.buf[0:n:n]
			nb.used = int32(n)
			return s
		}
	}

	// All extra arenas exhausted or value larger than one block.
	// TODO: route to DataStore (disk / GCS) for true spill-to-storage.
	// For now fall back to heap so execution is never blocked.
	ctx.ArenaOverflowed = true
	return make([]byte, n)
}

// GrowByteSlots borrows a slotExtBlock from the pool, copies the existing
// base slot headers into it, and re-points ByteSlots at the larger backing
// array. Instruction code using ctx.ByteSlots[i] requires no changes.
// Called by the compiler/executor when a flow needs more than BaseByteSlots.
func (ctx *Context) GrowByteSlots(needed int) {
	if needed <= len(ctx.ByteSlots) {
		return // already large enough
	}
	if ctx.slotExt == nil {
		ctx.slotExt = slotExtPool.Get().(*slotExtBlock)
		copy(ctx.slotExt.slots[:], ctx.byteSlotBase[:])
		ctx.SlotOverflowed = true
	}
	if needed <= ExtByteSlots {
		ctx.ByteSlots = ctx.slotExt.slots[:needed]
	}
	// Beyond ExtByteSlots: grow the extension slice via append (heap, very rare).
	// TODO: chain a second slotExtBlock from pool instead.
}

// ReleaseOverflow returns all pool-borrowed arena blocks and the slot
// extension (if any) back to their respective pools. Must be called before
// Pool.Put so that borrowed resources are available to other requests
// immediately rather than sitting idle inside the pool context.
func (ctx *Context) ReleaseOverflow() {
	for i := int32(0); i < ctx.extraN; i++ {
		ctx.extra[i].used = 0
		arenaPool.Put(ctx.extra[i])
		ctx.extra[i] = nil
	}
	ctx.extraN = 0
	ctx.active = &ctx.primary

	if ctx.slotExt != nil {
		// Zero slot-ext before returning so the next borrower gets a clean block.
		for i := range ctx.slotExt.slots {
			ctx.slotExt.slots[i] = nil
		}
		for i := range ctx.slotExt.ints {
			ctx.slotExt.ints[i] = 0
		}
		for i := range ctx.slotExt.bools {
			ctx.slotExt.bools[i] = false
		}
		slotExtPool.Put(ctx.slotExt)
		ctx.slotExt = nil
		ctx.ByteSlots = ctx.byteSlotBase[:BaseByteSlots]
	}
}

// Reset clears the context for reuse in the sync.Pool.
// We pass the concrete writer here for the new request.
// ReleaseOverflow must have been called before Pool.Put (done by
// FlowManager.ReturnContext) so Reset only needs to reset the primary arena.
func (ctx *Context) Reset(w ResponseWriter) {
	ctx.Writer = w
	ctx.Request = nil
	ctx.ResHeaderCount = 0
	ctx.ApiId = 0
	ctx.MutationCount = 0
	ctx.Match.Plan = nil
	ctx.Match.ParamCount = 0

	// Reset Response State
	ctx.ResponseStatus = 200
	ctx.headerSent = false
	ctx.IsBuffered = false
	ctx.detachedFromPool.Store(false)
	ctx.Trace = nil
	ctx.Obs = nil
	ctx.RequestStartNs = 0
	ctx.FirstByteSentNs = 0
	ctx.TenantKey = ""
	ctx.UpstreamTimeNs = 0
	ctx.UpstreamCalls = 0
	ctx.ClientBytesSent = 0
	ctx.UpstreamBytesTx = 0
	ctx.UpstreamBytesRx = 0
	ctx.ArenaOverflowed = false
	ctx.SlotOverflowed = false

	// Reset primary arena — one integer write, all slot data is implicitly gone.
	ctx.primary.used = 0
	ctx.active = &ctx.primary

	// Clean up body streams
	if ctx.RequestBody != nil {
		ctx.RequestBody.Close()
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

	// Nil inline slot bases — arena memory is already logically freed above.
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
