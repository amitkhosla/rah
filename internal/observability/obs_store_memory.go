package observability

import (
	"context"
	"strings"
	"sync"
	"time"
)

// MemObsStore is an in-memory ObsStore backed by circular ring buffers.
// It is the default store for single-node deployments with no external DB.
// Data is lost on process restart. Safe for concurrent use.
type MemObsStore struct {
	mu sync.RWMutex

	// Access log ring
	accessRing []AccessLogRecord
	accessHead int // next write index (wraps)
	accessFull bool

	// Trace ring
	traceRing []TraceRecord
	traceHead int
	traceFull bool

	// Metric snapshots: key = window+"|"+dimension+"|"+bucketStr
	snapshots map[string]MetricSnapshot
}

// NewMemObsStore creates a MemObsStore.
// maxAccessLog: capacity of access log ring (default 10000 if <= 0)
// maxTraces: capacity of trace ring (default 500 if <= 0)
func NewMemObsStore(maxAccessLog, maxTraces int) *MemObsStore {
	if maxAccessLog <= 0 {
		maxAccessLog = 10000
	}
	if maxTraces <= 0 {
		maxTraces = 500
	}
	return &MemObsStore{
		accessRing: make([]AccessLogRecord, maxAccessLog),
		traceRing:  make([]TraceRecord, maxTraces),
		snapshots:  make(map[string]MetricSnapshot),
	}
}

// WriteAccessLog persists a batch of access log records into the ring buffer.
func (m *MemObsStore) WriteAccessLog(_ context.Context, records []AccessLogRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cap := len(m.accessRing)
	for _, r := range records {
		m.accessRing[m.accessHead] = r
		m.accessHead = (m.accessHead + 1) % cap
		if m.accessHead == 0 {
			m.accessFull = true
		}
	}
	return nil
}

// WriteMetricSnapshot stores a metric snapshot, keyed by window+dimension+bucket.
// Evicts entries older than 2 hours on each write.
func (m *MemObsStore) WriteMetricSnapshot(_ context.Context, snap MetricSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := snap.Window + "|" + snap.Dimension + "|" + itoa(snap.Timestamp)
	m.snapshots[key] = snap

	// Evict entries older than 2 hours
	cutoff := time.Now().Unix() - 7200
	for k, v := range m.snapshots {
		if v.Timestamp < cutoff {
			delete(m.snapshots, k)
		}
	}
	return nil
}

// WriteTrace persists a sampled or error trace into the ring buffer.
func (m *MemObsStore) WriteTrace(_ context.Context, trace TraceRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cap := len(m.traceRing)
	m.traceRing[m.traceHead] = trace
	m.traceHead = (m.traceHead + 1) % cap
	if m.traceHead == 0 {
		m.traceFull = true
	}
	return nil
}

// QueryAccessLog returns access log entries matching the filter, newest first.
func (m *MemObsStore) QueryAccessLog(_ context.Context, f AccessLogFilter) ([]AccessLogRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	ringCap := len(m.accessRing)
	head := m.accessHead
	size := ringCap
	if !m.accessFull {
		size = head
	}

	var results []AccessLogRecord
	for i := 0; i < size && len(results) < limit; i++ {
		idx := (head - 1 - i + ringCap) % ringCap
		entry := m.accessRing[idx]

		if f.ApiName != "" && !strings.HasPrefix(entry.ApiName, f.ApiName) {
			continue
		}
		if f.TenantKey != "" && entry.TenantKey != f.TenantKey {
			continue
		}
		if f.Status > 0 {
			bucket := (f.Status / 100) * 100
			if entry.Status < bucket || entry.Status >= bucket+100 {
				continue
			}
		}
		tsS := entry.TimestampNs / 1_000_000_000
		if f.FromUnixS > 0 && tsS < f.FromUnixS {
			continue
		}
		if f.ToUnixS > 0 && tsS > f.ToUnixS {
			continue
		}
		results = append(results, entry)
	}
	return results, nil
}

// QueryMetrics returns metric snapshots matching the filter, sorted by Timestamp ascending.
func (m *MemObsStore) QueryMetrics(_ context.Context, f MetricsFilter) ([]MetricSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []MetricSnapshot
	for _, snap := range m.snapshots {
		if f.Window != "" && snap.Window != f.Window {
			continue
		}
		if f.Dimension != "" && !strings.HasPrefix(snap.Dimension, f.Dimension) {
			continue
		}
		if f.FromUnixS > 0 && snap.Timestamp < f.FromUnixS {
			continue
		}
		if f.ToUnixS > 0 && snap.Timestamp > f.ToUnixS {
			continue
		}
		results = append(results, snap)
	}

	// Sort by Timestamp ascending
	sortMetricsByTimestamp(results)
	return results, nil
}

// QueryTraces returns trace records matching the filter, newest first.
func (m *MemObsStore) QueryTraces(_ context.Context, f TraceFilter) ([]TraceRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	ringCap := len(m.traceRing)
	head := m.traceHead
	size := ringCap
	if !m.traceFull {
		size = head
	}

	var results []TraceRecord
	for i := 0; i < size && len(results) < limit; i++ {
		idx := (head - 1 - i + ringCap) % ringCap
		entry := m.traceRing[idx]

		if f.ApiName != "" && !strings.HasPrefix(entry.ApiName, f.ApiName) {
			continue
		}
		if f.TenantID != 0 && entry.TenantID != f.TenantID {
			continue
		}
		if f.MinMs > 0 && entry.TotalMs < f.MinMs {
			continue
		}
		if f.FromUnixS > 0 && entry.Timestamp < f.FromUnixS {
			continue
		}
		if f.ToUnixS > 0 && entry.Timestamp > f.ToUnixS {
			continue
		}
		results = append(results, entry)
	}
	return results, nil
}

// Close is a no-op for the in-memory store.
func (m *MemObsStore) Close() error { return nil }

// itoa converts an int64 to string without importing strconv at package level.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 20)
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// sortMetricsByTimestamp sorts a slice of MetricSnapshot by Timestamp ascending
// using a simple insertion sort (slices are typically small).
func sortMetricsByTimestamp(s []MetricSnapshot) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Timestamp < s[j-1].Timestamp; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
