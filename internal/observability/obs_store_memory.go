package observability

import (
	"context"
	"os"
	"sort"
	"strconv"
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

	// Instruction schema: key = ApiName
	instrSchema map[string][]InstrSchemaRow

	// Variable schema: key = ApiName
	varSchema map[string][]VarSchemaRow

	// Payload storage
	payloads      map[uint64][]PayloadRecord // key = TraceID
	payloadBytes  int64                      // current total byte usage
	payloadBudget int64                      // max bytes (auto-detected from available RAM)
	payloadOrder  []uint64                   // insertion-ordered TraceIDs for eviction
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
		accessRing:    make([]AccessLogRecord, maxAccessLog),
		traceRing:     make([]TraceRecord, maxTraces),
		snapshots:     make(map[string]MetricSnapshot),
		instrSchema:   make(map[string][]InstrSchemaRow),
		varSchema:     make(map[string][]VarSchemaRow),
		payloads:      make(map[uint64][]PayloadRecord),
		payloadBudget: detectPayloadBudget(),
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

// WriteTraceBatch persists a batch of trace records into the ring buffer.
func (m *MemObsStore) WriteTraceBatch(_ context.Context, records []TraceRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cap := len(m.traceRing)
	for _, trace := range records {
		m.traceRing[m.traceHead] = trace
		m.traceHead = (m.traceHead + 1) % cap
		if m.traceHead == 0 {
			m.traceFull = true
		}
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
		if f.AppName != "" && entry.AppName != f.AppName {
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

// UpsertInstrSchema writes or updates instruction schema rows for an API endpoint.
func (m *MemObsStore) UpsertInstrSchema(_ context.Context, rows []InstrSchemaRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(rows) > 0 {
		apiName := rows[0].ApiName
		m.instrSchema[apiName] = make([]InstrSchemaRow, len(rows))
		copy(m.instrSchema[apiName], rows)
	}
	return nil
}

// QueryInstrSchema returns instruction schema rows for the given API name.
func (m *MemObsStore) QueryInstrSchema(_ context.Context, apiName string) ([]InstrSchemaRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.instrSchema[apiName]
	if len(rows) == 0 {
		return nil, nil
	}
	result := make([]InstrSchemaRow, len(rows))
	copy(result, rows)
	return result, nil
}

// UpsertVarSchema writes or updates variable schema rows for an API.
func (m *MemObsStore) UpsertVarSchema(_ context.Context, rows []VarSchemaRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(rows) > 0 {
		apiName := rows[0].ApiName
		m.varSchema[apiName] = make([]VarSchemaRow, len(rows))
		copy(m.varSchema[apiName], rows)
	}
	return nil
}

// QueryVarSchema returns variable schema rows for the given API name.
func (m *MemObsStore) QueryVarSchema(_ context.Context, apiName string) ([]VarSchemaRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := m.varSchema[apiName]
	if len(rows) == 0 {
		return nil, nil
	}
	result := make([]VarSchemaRow, len(rows))
	copy(result, rows)
	return result, nil
}

// WritePayloadBatch persists a batch of payload records with LRU eviction.
func (m *MemObsStore) WritePayloadBatch(_ context.Context, records []PayloadRecord) error {
	if len(records) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range records {
		m.payloads[r.TraceID] = append(m.payloads[r.TraceID], r)
		m.payloadBytes += int64(len(r.Content))
	}

	// Add TraceIDs to payloadOrder for eviction tracking (only new ones).
	seen := make(map[uint64]bool)
	for _, r := range records {
		if !seen[r.TraceID] {
			seen[r.TraceID] = true
			m.payloadOrder = append(m.payloadOrder, r.TraceID)
		}
	}

	// Evict if over 80% of budget.
	if m.payloadBudget > 0 && m.payloadBytes > int64(float64(m.payloadBudget)*0.8) {
		m.evictPayloads()
	}
	return nil
}

// QueryPayloads returns all payload records for a given trace ID.
func (m *MemObsStore) QueryPayloads(_ context.Context, traceID uint64) ([]PayloadRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	recs := m.payloads[traceID]
	if len(recs) == 0 {
		return nil, nil
	}
	// Return a copy so caller can't mutate in-memory state.
	result := make([]PayloadRecord, len(recs))
	copy(result, recs)
	return result, nil
}

// evictPayloads removes the oldest trace payloads until usage drops below 60% of budget.
// Must be called with m.mu held.
func (m *MemObsStore) evictPayloads() {
	target := int64(float64(m.payloadBudget) * 0.6)
	i := 0
	for m.payloadBytes > target && i < len(m.payloadOrder) {
		traceID := m.payloadOrder[i]
		if recs, ok := m.payloads[traceID]; ok {
			for _, r := range recs {
				m.payloadBytes -= int64(len(r.Content))
			}
			delete(m.payloads, traceID)
		}
		i++
	}
	// Remove evicted entries from payloadOrder.
	m.payloadOrder = m.payloadOrder[i:]
}

// GetDistinctApps returns distinct non-empty AppName values from the access log ring
// within the given time window (fromUnixS).
func (m *MemObsStore) GetDistinctApps(_ context.Context, fromUnixS int64) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ringCap := len(m.accessRing)
	head := m.accessHead
	size := ringCap
	if !m.accessFull {
		size = head
	}

	seen := make(map[string]bool)
	for i := 0; i < size; i++ {
		idx := (head - 1 - i + ringCap) % ringCap
		entry := m.accessRing[idx]

		// Check time window
		tsS := entry.TimestampNs / 1_000_000_000
		if fromUnixS > 0 && tsS < fromUnixS {
			continue
		}

		// Only include non-empty app names
		if entry.AppName != "" && !seen[entry.AppName] {
			seen[entry.AppName] = true
		}
	}

	// Convert map to sorted slice
	apps := make([]string, 0, len(seen))
	for app := range seen {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	return apps, nil
}

// detectPayloadBudget returns the byte budget for in-memory payload storage.
// Uses 10% of available RAM (from /proc/meminfo on Linux), capped at 1 GB, min 64 MB.
// Falls back to 256 MB if /proc/meminfo is unavailable (Windows, macOS, etc.).
func detectPayloadBudget() int64 {
	const (
		minBudget     = 64 * 1024 * 1024   // 64 MB
		defaultBudget = 256 * 1024 * 1024  // 256 MB fallback
		maxBudget     = 1024 * 1024 * 1024 // 1 GB cap
		fraction      = 10                  // use 1/10 of available RAM
	)
	// Try reading /proc/meminfo (Linux only).
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return defaultBudget
	}
	// Find "MemAvailable:" line.
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		// Format: "MemAvailable:  12345678 kB"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			break
		}
		budget := (kb * 1024) / fraction
		if budget < minBudget {
			budget = minBudget
		}
		if budget > maxBudget {
			budget = maxBudget
		}
		return budget
	}
	return defaultBudget
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
