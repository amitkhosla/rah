package rctx

import (
	"bytes"
	"io"
	"net/http"
	"rah/internal/observability"
	"sync/atomic"
	"unsafe"
)

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
	Obs             *observability.Telemetry
	Trace           *observability.RequestTrace
	RequestStartNs  int64
	UpstreamTimeNs  int64
	UpstreamCalls   int32
	ClientBytesSent int64
	UpstreamBytesTx int64
	UpstreamBytesRx int64

	// detachedFromPool prevents the request goroutine from returning this
	// context to the pool when work is moved to a background goroutine.
	detachedFromPool atomic.Bool

	// ... existing fields ...

	// MEMORY BUFFER REQUIREMENTS:
	// 1. BorrowedChunks: A slice to track 4KB chunks leased from the Global Bank.
	// 2. CurrentWriteChunk: A pointer to the active chunk for ns-level writes.
	// 3. ShardID: Assigned at start to ensure we return memory to the same CPU shard.

	// BorrowedChunks [][]byte
	// CurrentWriteChunk []byte
	// ShardID int
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
		ctx.Writer.WriteHeader(ctx.ResponseStatus)
		n, _ := ctx.Writer.Write(ctx.ResponseBuffer)
		if n > 0 {
			ctx.ClientBytesSent += int64(n)
		}
		ctx.headerSent = true
	}
}

// Reset clears the context for reuse in the sync.Pool.
// We pass the concrete writer here for the new request.
func (ctx *Context) Reset(w ResponseWriter) {
	ctx.Writer = w
	ctx.Request = nil
	ctx.ResHeaderCount = 0
	ctx.ResponseStatus = 0
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
	ctx.UpstreamTimeNs = 0
	ctx.UpstreamCalls = 0
	ctx.ClientBytesSent = 0
	ctx.UpstreamBytesTx = 0
	ctx.UpstreamBytesRx = 0

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

	for i := range ctx.ByteSlots {
		ctx.ByteSlots[i] = nil
	}
	for i := range ctx.IntSlots {
		ctx.IntSlots[i] = 0
	}
	for i := range ctx.BoolSlots {
		ctx.BoolSlots[i] = false
	}
	ctx.scratchIdx = 0
	// Optional: reset the slice length to 0 to be consistent with others
	ctx.ScratchBuffer = ctx.ScratchBuffer[:0]
	// MEMORY CLEANUP LOGIC:
	// 1. Loop through BorrowedChunks and return each to the Global Memory Bank.
	// 2. MUST happen before the Context returns to the sync.Pool to unblock
	//    other waiting requests immediately.
	// 3. Set BorrowedChunks to nil/empty without deallocating backing array
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
