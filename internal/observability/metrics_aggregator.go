package observability

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/ingest"
)

// LatencyBuckets are the fixed upper bounds in milliseconds.
// A request is counted in the first bucket where its duration <= bound.
var LatencyBuckets = [6]int64{1, 5, 25, 100, 500, 2000} // ms

// APIMetrics holds aggregated metrics for a single API within a time window.
// All counters are updated atomically.
type APIMetrics struct {
	APIID       uint32
	TenantID    uint16
	WindowStart int64 // Unix seconds, start of window
	Count2xx    atomic.Int64
	Count4xx    atomic.Int64
	Count5xx    atomic.Int64
	CountOther  atomic.Int64
	Latency     [len(LatencyBuckets) + 1]atomic.Int64 // +1 for >2000ms bucket
	TotalNs     atomic.Int64                           // for average calculation
}

// APIMetricSnapshot is the JSON-serializable snapshot of APIMetrics for KindMetric events.
type APIMetricSnapshot struct {
	APIID        uint32  `json:"api_id"`
	TenantID     uint16  `json:"tenant_id"`
	WindowStart  int64   `json:"window_start"`
	WindowEnd    int64   `json:"window_end"`
	Count2xx     int64   `json:"count_2xx"`
	Count4xx     int64   `json:"count_4xx"`
	Count5xx     int64   `json:"count_5xx"`
	CountOther   int64   `json:"count_other"`
	TotalCount   int64   `json:"total_count"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	// Latency histogram: bucket upper bounds + counts
	LatencyBounds [len(LatencyBuckets)]int64     `json:"latency_bounds_ms"`
	LatencyCounts [len(LatencyBuckets) + 1]int64 `json:"latency_counts"`
}

// MetricsAggregator collects per-API metrics and flushes them at window boundaries.
// Safe for concurrent use: copy-on-write atomic pointer for the map; atomics guard per-entry counters.
type MetricsAggregator struct {
	writeMu sync.Mutex                            // held only during new-key insertion
	metrics atomic.Pointer[map[uint64]*APIMetrics] // COW; hot path reads via Load(), no lock

	pipeline atomic.Pointer[ingest.Pipeline]
	windows  []time.Duration // flush intervals
	stopCh   chan struct{}
}

// NewMetricsAggregator creates a MetricsAggregator that flushes at the given windows.
// If windows is empty, a default 1-minute window is used.
func NewMetricsAggregator(windows []time.Duration) *MetricsAggregator {
	if len(windows) == 0 {
		windows = []time.Duration{time.Minute}
	}
	a := &MetricsAggregator{
		windows: windows,
		stopCh:  make(chan struct{}),
	}
	initial := make(map[uint64]*APIMetrics)
	a.metrics.Store(&initial)
	return a
}

// SetPipeline wires the ingest pipeline into the aggregator.
// Safe to call before or after Start(); atomic store, no lock needed.
func (a *MetricsAggregator) SetPipeline(p *ingest.Pipeline) {
	a.pipeline.Store(p)
}

// Record records a single request outcome. Called from the hot path.
// durationNs is the total request duration in nanoseconds. statusCode is HTTP status.
func (a *MetricsAggregator) Record(tenantID uint16, apiID uint32, statusCode int, durationNs int64) {
	key := uint64(tenantID)<<32 | uint64(apiID)

	m := (*a.metrics.Load())[key]  // zero-lock read

	if m == nil {
		m = a.getOrCreate(key, apiID, tenantID)
	}

	// Update status counters.
	switch statusCode / 100 {
	case 2:
		m.Count2xx.Add(1)
	case 4:
		m.Count4xx.Add(1)
	case 5:
		m.Count5xx.Add(1)
	default:
		m.CountOther.Add(1)
	}

	// Update latency histogram.
	durationMs := durationNs / 1_000_000
	bucket := len(LatencyBuckets) // default: last bucket (>2000ms)
	for i, bound := range LatencyBuckets {
		if durationMs <= bound {
			bucket = i
			break
		}
	}
	m.Latency[bucket].Add(1)
	m.TotalNs.Add(durationNs)
}

// getOrCreate atomically creates or retrieves an APIMetrics entry using copy-on-write.
// This is called only on cache misses (new keys), not on the hot path.
func (a *MetricsAggregator) getOrCreate(key uint64, apiID uint32, tenantID uint16) *APIMetrics {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	// Re-check under write lock (another goroutine may have inserted)
	old := a.metrics.Load()
	if m := (*old)[key]; m != nil {
		return m
	}
	m := &APIMetrics{
		APIID:       apiID,
		TenantID:    tenantID,
		WindowStart: time.Now().Unix(),
	}
	// Copy-on-write: copy map, insert, store atomically
	newMap := make(map[uint64]*APIMetrics, len(*old)+1)
	for k, v := range *old {
		newMap[k] = v
	}
	newMap[key] = m
	a.metrics.Store(&newMap)
	return m
}

// Start launches a background flush goroutine for each configured window.
func (a *MetricsAggregator) Start() {
	for _, w := range a.windows {
		go a.flushLoop(w)
	}
}

// Stop signals all background goroutines to stop.
func (a *MetricsAggregator) Stop() {
	close(a.stopCh)
}

func (a *MetricsAggregator) flushLoop(window time.Duration) {
	ticker := time.NewTicker(window)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.flush(window)
		case <-a.stopCh:
			return
		}
	}
}

func (a *MetricsAggregator) flush(window time.Duration) {
	p := a.pipeline.Load()
	if p == nil {
		return
	}

	now := time.Now().Unix()

	// Swap the map under write lock — callers that arrive mid-flush will
	// create entries in the new map. The old map is processed below without
	// holding the lock.
	a.writeMu.Lock()
	oldPtr := a.metrics.Load()
	newMap := make(map[uint64]*APIMetrics, len(*oldPtr))
	a.metrics.Store(&newMap)
	a.writeMu.Unlock()
	old := *oldPtr

	for _, m := range old {
		snap := snapshotMetrics(m, now)
		payload, err := json.Marshal(snap)
		if err != nil {
			continue
		}
		n := p.NumSinksForKind(ingest.KindMetric)
		if n == 0 {
			continue
		}
		e := ingest.Event{
			Kind:        ingest.KindMetric,
			TenantID:    m.TenantID,
			APIID:       m.APIID,
			TimestampNs: now * 1_000_000_000,
		}
		e.SetPayload(payload, n)
		p.Emit(e)
	}
}

func snapshotMetrics(m *APIMetrics, windowEnd int64) APIMetricSnapshot {
	snap := APIMetricSnapshot{
		APIID:         m.APIID,
		TenantID:      m.TenantID,
		WindowStart:   m.WindowStart,
		WindowEnd:     windowEnd,
		Count2xx:      m.Count2xx.Load(),
		Count4xx:      m.Count4xx.Load(),
		Count5xx:      m.Count5xx.Load(),
		CountOther:    m.CountOther.Load(),
		LatencyBounds: LatencyBuckets,
	}
	snap.TotalCount = snap.Count2xx + snap.Count4xx + snap.Count5xx + snap.CountOther
	totalNs := m.TotalNs.Load()
	if snap.TotalCount > 0 {
		snap.AvgLatencyMs = float64(totalNs/1_000_000) / float64(snap.TotalCount)
	}
	for i := range m.Latency {
		snap.LatencyCounts[i] = m.Latency[i].Load()
	}
	return snap
}
