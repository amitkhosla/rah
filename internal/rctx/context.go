package rctx

import (
	"io"
	"unsafe"
)

// ResponseWriter abstracts the network socket. This allows the Context
// to remain decoupled from net/http and supports easier unit testing.
type ResponseWriter interface {
	Write([]byte) (int, error)
	WriteHeader(statusCode int)
}

// HeaderMutation tracks changes for the upstream proxy without string allocations.
type HeaderMutation struct {
	Key   []byte
	Value []byte
	Op    uint8 // 0: Set, 1: Remove
}

// RouteMatch holds the result of the Radix Tree lookup.
type RouteMatch struct {
	Plan         any // Cast to *engine.Plan in the engine package
	ParamCount   int
	ParamIndices [10]int // Pairs of [start, end] offsets into ctx.Path
}

type Context struct {
	ApiId uint32
	Match RouteMatch

	// Metadata (Zero-allocation snapshots)
	Method         []byte
	Path           []byte
	RawQuery       []byte
	metadataBuffer [512]byte
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
	RequestBody   io.ReadCloser
	RequestBuffer []byte // Populated only if IsBuffered = true
	IsBuffered    bool   // Flag to toggle between Streaming and Transformation modes

	// Response Handling
	writer         ResponseWriter
	ResponseStatus int
	ResponseBuffer []byte // Collects data if IsBuffered = true
	headerSent     bool
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
		ctx.writer.WriteHeader(ctx.ResponseStatus)
		ctx.headerSent = true
	}
	return ctx.writer.Write(p)
}

// Finalize handles the "Last Mile" of the response.
// If data was buffered, it flushes it to the wire in one go.
func (ctx *Context) Finalize() {
	if ctx.IsBuffered && !ctx.headerSent {
		ctx.writer.WriteHeader(ctx.ResponseStatus)
		ctx.writer.Write(ctx.ResponseBuffer)
		ctx.headerSent = true
	}
}

// Reset clears the context for reuse in the sync.Pool.
// We pass the concrete writer here for the new request.
func (ctx *Context) Reset(w ResponseWriter) {
	ctx.writer = w
	ctx.ApiId = 0
	ctx.MutationCount = 0
	ctx.Match.Plan = nil
	ctx.Match.ParamCount = 0

	// Reset Response State
	ctx.ResponseStatus = 200
	ctx.headerSent = false
	ctx.IsBuffered = false

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
	ctx.RawQuery = nil

	for i := range ctx.ByteSlots {
		ctx.ByteSlots[i] = ctx.ByteSlots[i][:0]
	}
	for i := range ctx.IntSlots {
		ctx.IntSlots[i] = 0
	}
	for i := range ctx.BoolSlots {
		ctx.BoolSlots[i] = false
	}
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
