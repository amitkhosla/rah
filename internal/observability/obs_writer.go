package observability

import (
	"context"
	"fmt"
	"sync"
	"time"

	"rah/internal/gatewaylog"
)

const (
	defaultBatchSize  = 200
	defaultFlushEvery = 2 * time.Second
)

// ObsWriter batches access log records and flushes them to an ObsStore
// asynchronously.  Traces and metric snapshots are written immediately because
// they are far less frequent.
//
// ObsWriter is safe for concurrent use.  It must be started with Start() and
// stopped by cancelling the context passed to Start().
type ObsWriter struct {
	store      ObsStore
	mu         sync.Mutex
	accessBuf  []AccessLogRecord
	batchSize  int
	flushEvery time.Duration
	stopCh     chan struct{}
	flushTick  *time.Ticker
}

// NewObsWriter creates an ObsWriter that writes to store.
// batchSize <= 0 defaults to 200.
// flushEvery <= 0 defaults to 2 seconds.
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
	return &ObsWriter{
		store:      store,
		batchSize:  batchSize,
		flushEvery: flushEvery,
		accessBuf:  make([]AccessLogRecord, 0, batchSize),
		stopCh:     make(chan struct{}),
	}
}

// Start launches the background flush goroutine.  It returns immediately.
// The goroutine stops when ctx is cancelled or Close() is called.
// Start must be called exactly once.
func (w *ObsWriter) Start(ctx context.Context) {
	w.flushTick = time.NewTicker(w.flushEvery)
	go w.loop(ctx)
}

// WriteAccessLog adds r to the batch buffer.  If the buffer reaches batchSize
// it is flushed synchronously in the caller's goroutine to apply back-pressure.
// This is the only write path that batches; it is designed to be called from
// the request hot-path where individual records arrive at high throughput.
func (w *ObsWriter) WriteAccessLog(r AccessLogRecord) {
	w.mu.Lock()
	w.accessBuf = append(w.accessBuf, r)
	full := len(w.accessBuf) >= w.batchSize
	var batch []AccessLogRecord
	if full {
		batch = w.accessBuf
		w.accessBuf = make([]AccessLogRecord, 0, w.batchSize)
	}
	w.mu.Unlock()

	if full {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.store.WriteAccessLog(ctx, batch); err != nil {
			gatewaylog.Default.Warn("obs.writer access log flush error", gatewaylog.F("err", fmt.Sprintf("%v", err)))
		}
	}
}

// WriteTrace persists a trace record immediately (not batched).
// Errors are logged but not returned; the caller should not block on tracing.
func (w *ObsWriter) WriteTrace(ctx context.Context, trace TraceRecord) {
	if err := w.store.WriteTrace(ctx, trace); err != nil {
		gatewaylog.Default.Warn("obs.writer write trace error", gatewaylog.F("err", fmt.Sprintf("%v", err)))
	}
}

// WriteMetricSnapshot persists a metric snapshot immediately (not batched).
func (w *ObsWriter) WriteMetricSnapshot(ctx context.Context, snap MetricSnapshot) {
	if err := w.store.WriteMetricSnapshot(ctx, snap); err != nil {
		gatewaylog.Default.Warn("obs.writer write metric snapshot error", gatewaylog.F("err", fmt.Sprintf("%v", err)))
	}
}

// Store returns the underlying ObsStore so callers can execute queries directly.
func (w *ObsWriter) Store() ObsStore { return w.store }

// Close stops the background goroutine and performs a final flush.
// It is safe to call Close more than once.
func (w *ObsWriter) Close() {
	select {
	case <-w.stopCh:
		// already stopped
	default:
		close(w.stopCh)
	}
}

// loop is the background goroutine started by Start.
func (w *ObsWriter) loop(ctx context.Context) {
	defer w.flushTick.Stop()
	for {
		select {
		case <-w.flushTick.C:
			w.flush(ctx)
		case <-ctx.Done():
			w.flush(ctx) // final flush before exit
			return
		case <-w.stopCh:
			w.flush(context.Background()) // final flush with fresh context
			return
		}
	}
}

// flush drains the access log buffer and writes it to the store.
func (w *ObsWriter) flush(ctx context.Context) {
	w.mu.Lock()
	if len(w.accessBuf) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.accessBuf
	w.accessBuf = make([]AccessLogRecord, 0, w.batchSize)
	w.mu.Unlock()

	flushCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := w.store.WriteAccessLog(flushCtx, batch); err != nil {
		gatewaylog.Default.Warn("obs.writer periodic flush error", gatewaylog.Fint("records", int64(len(batch))), gatewaylog.F("err", fmt.Sprintf("%v", err)))
	}
}
