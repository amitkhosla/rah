package observability

import (
	"github.com/amitkhosla/rah/internal/ingest"
)

// CacheEventHandler is a simple event handler that processes cache events
// emitted into the ingest pipeline. Instead of using a Sink (which requires
// serialization/deserialization), we pass the raw Event directly to the aggregator.
//
// This is used as a wrapper that can be passed to ingest.RunConsumer or called
// directly when events are created from within the gateway.
type CacheEventHandler struct {
	tel *Telemetry
}

// NewCacheEventHandler creates a handler that forwards cache events to Telemetry.
func NewCacheEventHandler(tel *Telemetry) *CacheEventHandler {
	return &CacheEventHandler{tel: tel}
}

// Handle processes a single cache event from the ingest pipeline.
func (h *CacheEventHandler) Handle(e ingest.Event) {
	h.tel.handleCacheEvent(e)
}

// CacheEventMiddleware wraps the Emitter interface to intercept cache events
// and route them directly to the aggregator in addition to the pipeline.
// This allows cache events to be aggregated in-memory without requiring
// a separate sink consumer thread.
type CacheEventMiddleware struct {
	emitter ingest.Emitter
	handler *CacheEventHandler
}

// NewCacheEventMiddleware creates a middleware that intercepts cache events.
func NewCacheEventMiddleware(emitter ingest.Emitter, handler *CacheEventHandler) *CacheEventMiddleware {
	return &CacheEventMiddleware{
		emitter: emitter,
		handler: handler,
	}
}

// Emit sends an event into the pipeline and also handles cache events immediately.
func (m *CacheEventMiddleware) Emit(e ingest.Event) {
	// Route to the pipeline
	if m.emitter != nil {
		m.emitter.Emit(e)
	}
	// Also handle cache events immediately (zero-allocation, in-process)
	if e.Kind == ingest.KindCacheHit || e.Kind == ingest.KindCacheMiss {
		m.handler.Handle(e)
	}
}
