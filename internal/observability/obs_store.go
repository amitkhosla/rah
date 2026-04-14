package observability

import "context"

// MetricSnapshot is a pre-aggregated metric record for one time window + dimension.
type MetricSnapshot struct {
	Timestamp int64   // unix seconds, start of bucket
	Window    string  // "1m" | "5m" | "1h"
	Dimension string  // "api:payment-api" | "tenant:acme" | "gateway"
	ReqTotal  int64
	Req5xx    int64
	LatP50Ms  float64
	LatP95Ms  float64
	LatP99Ms  float64
	BytesIn   int64
	BytesOut  int64
}

// AccessLogRecord is a single persisted access log entry.
type AccessLogRecord struct {
	TimestampNs int64             `json:"timestamp_ns"`
	ApiName     string            `json:"api_name"`
	TenantID    uint16            `json:"tenant_id"`
	TenantKey   string            `json:"tenant_key"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Status      int               `json:"status"`
	TotalMs     float64           `json:"total_ms"`
	GatewayMs   float64           `json:"gateway_ms"`
	UpstreamMs  float64           `json:"upstream_ms"`
	TTFBMs      float64           `json:"ttfb_ms"`
	ReqBytes    int64             `json:"req_bytes"`
	ResBytes    int64             `json:"res_bytes"`
	Extra       map[string]string `json:"extra,omitempty"` // customer-configured extra fields
}

// TraceRecord is a persisted request trace (sampled or error).
type TraceRecord struct {
	TraceID   uint64  `json:"trace_id"`
	Timestamp int64   `json:"timestamp"` // unix seconds
	ApiName   string  `json:"api_name"`
	TenantID  uint16  `json:"tenant_id"`
	Status    int     `json:"status"`
	TotalMs   float64 `json:"total_ms"`
	Payload   []byte  `json:"payload,omitempty"` // JSON-encoded RequestTrace
}

// AccessLogFilter filters access log queries.
type AccessLogFilter struct {
	ApiName   string
	TenantKey string
	Status    int   // 0 = any; 500 = only 5xx etc.
	FromUnixS int64
	ToUnixS   int64
	Limit     int  // default 100, max 1000
}

// MetricsFilter filters metric snapshot queries.
type MetricsFilter struct {
	Window    string // "1m" | "5m" | "1h"
	Dimension string // prefix match: "api:" | "tenant:" | "gateway"
	FromUnixS int64
	ToUnixS   int64
}

// TraceFilter filters trace queries.
type TraceFilter struct {
	ApiName   string
	TenantID  uint16
	MinMs     float64
	FromUnixS int64
	ToUnixS   int64
	Limit     int
}

// ObsStore is the persistence interface for observability data.
// All write methods must be safe for concurrent use.
// Implementations must handle the case where the underlying store is unavailable
// (return error, never panic).
type ObsStore interface {
	// WriteAccessLog persists a batch of access log records.
	WriteAccessLog(ctx context.Context, records []AccessLogRecord) error

	// WriteMetricSnapshot persists a metric snapshot.
	WriteMetricSnapshot(ctx context.Context, snap MetricSnapshot) error

	// WriteTrace persists a sampled or error trace.
	WriteTrace(ctx context.Context, trace TraceRecord) error

	// QueryAccessLog returns access log entries matching the filter.
	QueryAccessLog(ctx context.Context, f AccessLogFilter) ([]AccessLogRecord, error)

	// QueryMetrics returns metric snapshots matching the filter.
	QueryMetrics(ctx context.Context, f MetricsFilter) ([]MetricSnapshot, error)

	// QueryTraces returns trace records matching the filter.
	QueryTraces(ctx context.Context, f TraceFilter) ([]TraceRecord, error)

	// Close releases any held resources.
	Close() error
}

// NoopObsStore discards all writes and returns empty results for reads.
// Used when observability persistence is not configured.
type NoopObsStore struct{}

func (NoopObsStore) WriteAccessLog(_ context.Context, _ []AccessLogRecord) error { return nil }
func (NoopObsStore) WriteMetricSnapshot(_ context.Context, _ MetricSnapshot) error { return nil }
func (NoopObsStore) WriteTrace(_ context.Context, _ TraceRecord) error              { return nil }
func (NoopObsStore) QueryAccessLog(_ context.Context, _ AccessLogFilter) ([]AccessLogRecord, error) {
	return nil, nil
}
func (NoopObsStore) QueryMetrics(_ context.Context, _ MetricsFilter) ([]MetricSnapshot, error) {
	return nil, nil
}
func (NoopObsStore) QueryTraces(_ context.Context, _ TraceFilter) ([]TraceRecord, error) {
	return nil, nil
}
func (NoopObsStore) Close() error { return nil }
