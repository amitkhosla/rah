package observability

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"rah/internal/gatewaylog"
)

const (
	defaultBatchSize  = 200
	defaultFlushEvery = 2 * time.Second
)

// ObsWriter batches access log records and flushes them to an ObsStore
// asynchronously. Traces are enqueued to a background drain goroutine.
// Metric snapshots are written immediately.
//
// WriteAccessLog is the hot path: it stamps a record into a pre-allocated slab
// slot via a single atomic.Add, with no mutex and no per-call allocation.
// A background drain goroutine rotates slabs every 10 ms and writes batches to
// the store when batchSize is reached or flushEvery elapses.
//
// EnqueueTrace is also a hot path: it enqueues a trace record to a ring buffer
// backed by a separate drain goroutine that flushes batches to WriteTraceBatch.
//
// ObsWriter is safe for concurrent use. Call Start() once before use and
// Close() (or cancel the context passed to Start()) to shut down cleanly.
type ObsWriter struct {
	store      ObsStore
	ring       *obsSlabRing
	traceRing  *traceWriteRing
	stop       chan struct{}
	enabled    atomic.Bool
}

// NewObsWriter creates an ObsWriter backed by store.
// batchSize <= 0 defaults to 200.
// flushEvery <= 0 defaults to 2 seconds.
// Slab capacity is derived from runtime.GOMAXPROCS(0); see obsComputeSlabCap.
func NewObsWriter(store ObsStore, batchSize int, flushEvery time.Duration) *ObsWriter {
	if store == nil {
		store = NoopObsStore{}
	}
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if flushEvery <= 0 {
		flushEvery = defaultFlushEvery
	}
	w := &ObsWriter{
		store:     store,
		ring:      newObsSlabRing(store, obsComputeSlabCap(), batchSize, flushEvery),
		traceRing: newTraceWriteRing(store, 4, 64, 16),
		stop:      make(chan struct{}),
	}
	w.enabled.Store(true)
	return w
}

// Start launches the background drain goroutines. Call once before the first
// WriteAccessLog or EnqueueTrace. Stops when ctx is cancelled or Close() is called.
func (w *ObsWriter) Start(ctx context.Context) {
	w.ring.start()
	w.traceRing.start()
	go func() {
		select {
		case <-ctx.Done():
			w.Close()
		case <-w.stop:
		}
	}()
}

// SetEnabled atomically sets whether access log writes are enabled.
// When disabled, WriteAccessLog returns immediately with zero cost.
func (w *ObsWriter) SetEnabled(v bool) {
	w.enabled.Store(v)
}

// WriteAccessLog stamps r into the active slab slot — no mutex, no allocation.
// Hot path cost: one atomic.Add (slot claim) + plain field writes + one
// atomic.Store (ready signal). Any records that arrive while a slab rotation
// is in progress are counted in DroppedCount; this is extremely rare at normal
// operating TPS.
//
// Fast path (when disabled): single atomic.Load, returns immediately.
func (w *ObsWriter) WriteAccessLog(r AccessLogRecord) {
	if !w.enabled.Load() {
		return
	}
	w.ring.write(r)
}

// DroppedCount returns the total number of records dropped (access logs + traces)
// due to slab-full, rotation-race, or ring-full conditions since the writer was created.
func (w *ObsWriter) DroppedCount() uint64 {
	return w.ring.dropped + w.traceRing.droppedCount()
}

// EnqueueTrace enqueues a trace record to be written asynchronously.
// Returns false if the trace ring has hit its hard limit; true otherwise.
// Hot path cost: one atomic operation to claim space in the ring.
func (w *ObsWriter) EnqueueTrace(rec TraceRecord) bool {
	if !w.enabled.Load() {
		return true
	}
	return w.traceRing.enqueue(rec)
}

// WriteMetricSnapshot persists a metric snapshot immediately (not batched).
func (w *ObsWriter) WriteMetricSnapshot(ctx context.Context, snap MetricSnapshot) {
	if err := w.store.WriteMetricSnapshot(ctx, snap); err != nil {
		gatewaylog.Default.Warn("obs.writer write metric snapshot error",
			gatewaylog.F("err", fmt.Sprintf("%v", err)))
	}
}

// Store returns the underlying ObsStore for direct queries.
func (w *ObsWriter) Store() ObsStore { return w.store }

// Close stops the background drain goroutines, performs a final flush, and
// waits for all in-flight store writes to complete. Safe to call more than once.
func (w *ObsWriter) Close() {
	select {
	case <-w.stop:
		// already stopped
	default:
		close(w.stop)
		w.ring.stopAndWait()
		w.traceRing.stopAndWait()
	}
}
