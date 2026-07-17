package observability

import (
	"context"
	"encoding/json"
)

// MetricSnapshot is a pre-aggregated metric record for one time window + dimension.
type MetricSnapshot struct {
	Timestamp int64
	Window    string
	Dimension string
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
	ConnSetupMs float64           `json:"conn_setup_ms,omitempty"`
	TransferMs  float64           `json:"transfer_ms,omitempty"`
	ReqBytes    int64             `json:"req_bytes"`
	ResBytes    int64             `json:"res_bytes"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// TraceRecord is a persisted request trace.
//
// V1 traces (memory, Redis) populate Payload with the full JSON blob.
// V2 traces (Postgres) leave Payload nil and populate typed fields; they are
// identified by InstrPCs != nil.  Stores that do not support V2 silently
// ignore the typed fields.
type TraceRecord struct {
	// Core fields — used by all stores.
	TraceID   uint64          `json:"trace_id"`
	Timestamp int64           `json:"timestamp"` // unix seconds
	ApiName   string          `json:"api_name"`
	TenantID  uint16          `json:"tenant_id"`
	Status    int             `json:"status"`
	TotalMs   float64         `json:"total_ms"`
	Payload   json.RawMessage `json:"payload,omitempty"` // v1 only

	// V2 typed fields — written to obs_traces_v2 (Postgres only).
	// A nil InstrPCs slice means this is a v1 record.
	Method        string    `json:"method,omitempty"`
	ApiVersionID  uint32    `json:"api_version_id,omitempty"`
	EndpointID    uint8     `json:"endpoint_id,omitempty"`
	DurationNs    int64     `json:"duration_ns,omitempty"`
	GatewayNs     int64     `json:"gateway_ns,omitempty"`
	UpstreamNs    int64     `json:"upstream_ns,omitempty"`
	ReqBytes      int64     `json:"req_bytes,omitempty"`
	ResBytes      int64     `json:"res_bytes,omitempty"`
	UpstreamCalls uint16    `json:"upstream_calls,omitempty"`
	PhaseDurs     [10]int32 `json:"phase_durs,omitempty"`
	InstrPCs      []int16   `json:"instr_pcs,omitempty"`    // nil ⇒ v1
	InstrDursNs   []int32   `json:"instr_durs_ns,omitempty"`
	LLMCalls      []LLMCallRow `json:"llm_calls,omitempty"`
}

// LLMCallRow is one LLM call within a trace, written to obs_llm_calls.
// Raw request/response bytes are stored separately via LLMCallEntry/LLMCallBlock.
type LLMCallRow struct {
	PC           int16  `json:"pc"`
	Seq          uint8  `json:"seq"`             // call index within this trace
	ModelName    string `json:"model_name"`
	Status       uint16 `json:"status"`
	InputTokens  uint32 `json:"input_tokens"`
	OutputTokens uint32 `json:"output_tokens"`
	CostMicro    uint32 `json:"cost_micro"`      // cost in millionths of USD
	DurationNs   int64  `json:"duration_ns"`
}

// InstrSchemaRow describes one instruction PC in a compiled API endpoint.
// Written once at bake time; joined at read time to resolve PC → name.
type InstrSchemaRow struct {
	ApiName    string `json:"api_name"`
	ApiHash    uint64 `json:"api_hash"` // version fingerprint for cache invalidation
	EndpointID uint8  `json:"endpoint_id"`
	PC         int16  `json:"pc"`
	StepType   string `json:"step_type"`
	StepName   string `json:"step_name"`
}

// VarSchemaRow maps a stable var_id (= slot index) to a human-readable variable
// name within a compiled API version. Written once at bake time; joined at read
// time to annotate trace variable values.
type VarSchemaRow struct {
	ApiName  string `json:"api_name"`
	ApiHash  uint64 `json:"api_hash"`            // version fingerprint; used to invalidate stale schema
	VarID    uint16 `json:"var_id"`              // == slot index, stable per API version
	VarName  string `json:"var_name"`            // human-readable name (from flow YAML, e.g. "auth_header")
	StepType string `json:"step_type,omitempty"` // step kind that writes this var (informational)
}

// AccessLogFilter filters access log queries.
type AccessLogFilter struct {
	ApiName   string
	TenantKey string
	Status    int
	FromUnixS int64
	ToUnixS   int64
	Limit     int
}

// MetricsFilter filters metric snapshot queries.
type MetricsFilter struct {
	Window    string
	Dimension string
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

// PayloadRecord holds one captured LLM or upstream API call payload.
type PayloadRecord struct {
	TraceID uint64
	Kind    uint8  // 1=LLM, 2=upstream API
	Seq     uint8  // call sequence within the trace (0-based)
	PC      int16  // instruction PC that made the call
	Content []byte // raw wire bytes (length-header format)
}

// InstrSnapshot is a zero-allocation copy of per-instruction timing from ctx.
// Passed to PersistTrace to avoid heap allocation.
type InstrSnapshot struct {
	PCs  [64]int16
	Durs [64]int32
	N    uint8
}

// ObsStore is the persistence interface for observability data.
// All write methods must be safe for concurrent use.
type ObsStore interface {
	// WriteAccessLog persists a batch of access log records.
	WriteAccessLog(ctx context.Context, records []AccessLogRecord) error

	// WriteMetricSnapshot persists a metric snapshot.
	WriteMetricSnapshot(ctx context.Context, snap MetricSnapshot) error

	// WriteTraceBatch persists a batch of trace records.
	// V2 stores (Postgres) write typed columns; V1 stores iterate and persist each record's Payload.
	WriteTraceBatch(ctx context.Context, records []TraceRecord) error

	// UpsertInstrSchema writes or updates instruction schema rows for an API endpoint.
	// Called once at bake time when an API is compiled.
	UpsertInstrSchema(ctx context.Context, rows []InstrSchemaRow) error

	// QueryAccessLog returns access log entries matching the filter.
	QueryAccessLog(ctx context.Context, f AccessLogFilter) ([]AccessLogRecord, error)

	// QueryMetrics returns metric snapshots matching the filter.
	QueryMetrics(ctx context.Context, f MetricsFilter) ([]MetricSnapshot, error)

	// QueryTraces returns trace records matching the filter.
	QueryTraces(ctx context.Context, f TraceFilter) ([]TraceRecord, error)

	// QueryInstrSchema returns instruction schema rows for the given API name.
	QueryInstrSchema(ctx context.Context, apiName string) ([]InstrSchemaRow, error)

	// UpsertVarSchema writes or updates variable schema rows for a compiled API.
	UpsertVarSchema(ctx context.Context, rows []VarSchemaRow) error

	// QueryVarSchema returns the variable schema rows for the given API name.
	QueryVarSchema(ctx context.Context, apiName string) ([]VarSchemaRow, error)

	// WritePayloadBatch persists a batch of payload records.
	// Implementations may be no-ops (Redis, noop).
	WritePayloadBatch(ctx context.Context, payloads []PayloadRecord) error

	// QueryPayloads returns all payload records for a given trace ID.
	QueryPayloads(ctx context.Context, traceID uint64) ([]PayloadRecord, error)

	// Close releases any held resources.
	Close() error
}

// NoopObsStore discards all writes and returns empty results for reads.
// Used when observability persistence is not configured.
type NoopObsStore struct{}

func (NoopObsStore) WriteAccessLog(_ context.Context, _ []AccessLogRecord) error   { return nil }
func (NoopObsStore) WriteMetricSnapshot(_ context.Context, _ MetricSnapshot) error  { return nil }
func (NoopObsStore) WriteTraceBatch(_ context.Context, _ []TraceRecord) error       { return nil }
func (NoopObsStore) UpsertInstrSchema(_ context.Context, _ []InstrSchemaRow) error  { return nil }
func (NoopObsStore) QueryAccessLog(_ context.Context, _ AccessLogFilter) ([]AccessLogRecord, error) {
	return nil, nil
}
func (NoopObsStore) QueryMetrics(_ context.Context, _ MetricsFilter) ([]MetricSnapshot, error) {
	return nil, nil
}
func (NoopObsStore) QueryTraces(_ context.Context, _ TraceFilter) ([]TraceRecord, error) {
	return nil, nil
}
func (NoopObsStore) QueryInstrSchema(_ context.Context, _ string) ([]InstrSchemaRow, error) {
	return nil, nil
}
func (NoopObsStore) UpsertVarSchema(_ context.Context, _ []VarSchemaRow) error { return nil }
func (NoopObsStore) QueryVarSchema(_ context.Context, _ string) ([]VarSchemaRow, error) {
	return nil, nil
}
func (NoopObsStore) WritePayloadBatch(_ context.Context, _ []PayloadRecord) error { return nil }
func (NoopObsStore) QueryPayloads(_ context.Context, _ uint64) ([]PayloadRecord, error) {
	return nil, nil
}
func (NoopObsStore) Close() error { return nil }
