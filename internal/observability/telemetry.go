package observability

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"rah/internal/apikey"
	"rah/internal/gatewaylog"
)

type KV struct {
	K string `json:"k"`
	V string `json:"v"`
}

type GatewayMetrics struct {
	RequestsTotal          uint64               `json:"requests_total"`
	Requests2xx            uint64               `json:"requests_2xx"`
	Requests3xx            uint64               `json:"requests_3xx"`
	Requests4xx            uint64               `json:"requests_4xx"`
	Requests5xx            uint64               `json:"requests_5xx"`
	GatewayLatencyTotalNs  uint64               `json:"gateway_latency_total_ns"`
	UpstreamLatencyTotalNs uint64               `json:"upstream_latency_total_ns"`
	ClientBytesSentTotal   uint64               `json:"client_bytes_sent_total"`
	UpstreamBytesTxTotal   uint64               `json:"upstream_bytes_tx_total"`
	UpstreamBytesRxTotal   uint64               `json:"upstream_bytes_rx_total"`
	LastRequestUnixNano    int64                `json:"last_request_unix_nano"`
	DroppedExports         uint64               `json:"dropped_exports"`
	InstructionTopSlow     []NameLatency        `json:"instruction_top_slow"`
	UpstreamTopSlow        []NameLatency        `json:"upstream_top_slow"`
	TenantTopErrors        []TenantStatusCounts `json:"tenant_top_errors"`
	CustomMetricTop        []MetricAgg          `json:"custom_metric_top"`
	CacheStats             []CacheStat          `json:"cache_stats"`
}

type NameLatency struct {
	Name           string `json:"name"`
	Count          uint64 `json:"count"`
	TotalLatencyNs uint64 `json:"total_latency_ns"`
	BytesTx        uint64 `json:"bytes_tx"`
	BytesRx        uint64 `json:"bytes_rx"`
}

// CacheStat is a snapshot of hit/miss and latency stats for one cache instruction.
type CacheStat struct {
	Name      string  `json:"name"`
	Hits      uint64  `json:"hits"`
	Misses    uint64  `json:"misses"`
	HitRate   float64 `json:"hit_rate"`   // 0.0–1.0
	AvgHitNs  uint64  `json:"avg_hit_ns"` // mean store latency on a hit
	AvgMissNs uint64  `json:"avg_miss_ns"`// mean store latency on a miss
}

type TenantStatusCounts struct {
	TenantID    uint16 `json:"tenant_id"`
	Requests2xx uint64 `json:"requests_2xx"`
	Requests3xx uint64 `json:"requests_3xx"`
	Requests4xx uint64 `json:"requests_4xx"`
	Requests5xx uint64 `json:"requests_5xx"`
}

type MetricAgg struct {
	Key        string `json:"key"`
	Count      uint64 `json:"count"`
	TotalValue int64  `json:"total_value"`
}

type RequestSummary struct {
	TraceID               uint64 `json:"trace_id"`
	ApiID                 uint32 `json:"api_id"`
	ApiVersionID          uint32 `json:"api_version_id"`
	TenantID              uint16 `json:"tenant_id"`
	Method                string `json:"method"`
	Path                  string `json:"path"`
	Status                int    `json:"status"`
	DurationNs            int64  `json:"duration_ns"`
	GatewayDurationNs     int64  `json:"gateway_duration_ns"`
	UpstreamDurationNs    int64  `json:"upstream_duration_ns"`
	UpstreamCalls         int    `json:"upstream_calls"`
	ClientBytesSent       int64  `json:"client_bytes_sent"`
	UpstreamBytesTx       int64  `json:"upstream_bytes_tx"`
	UpstreamBytesRx       int64  `json:"upstream_bytes_rx"`
	StartedAtUnixNano  int64  `json:"started_at_unix_nano"`
}

type InstructionEvent struct {
	Seq        uint32 `json:"seq"`
	Name       string `json:"name"`
	PC         int16  `json:"pc"`
	StepIdx    int16  `json:"step_idx"`
	DurationNs int64  `json:"duration_ns"`
	Input      []KV   `json:"input,omitempty"`
	Output     []KV   `json:"output,omitempty"`
}

type UpstreamEvent struct {
	Seq               uint32            `json:"seq"`
	Attempt           int               `json:"attempt"`
	Host              string            `json:"host"`
	URL               string            `json:"url"`
	Model             string            `json:"model,omitempty"`
	Status            int               `json:"status"`
	Err               string            `json:"err,omitempty"`
	ConnReused        bool              `json:"conn_reused"`
	ConnIdle          bool              `json:"conn_idle"`
	DNSDurationNs     int64             `json:"dns_duration_ns"`
	ConnectDurationNs int64             `json:"connect_duration_ns"`
	TLSDurationNs     int64             `json:"tls_duration_ns"`
	TTFBNs            int64             `json:"ttfb_ns"`
	TotalNs           int64             `json:"total_ns"`
	BytesSent         int64             `json:"bytes_sent"`
	BytesReceived     int64             `json:"bytes_received"`
	RequestHeaders    map[string]string `json:"req_headers,omitempty"`
	ResponseHeaders   map[string]string `json:"res_headers,omitempty"`
	ResponseBody      string            `json:"res_body,omitempty"`
}

type RequestTrace struct {
	Summary        RequestSummary    `json:"summary"`
	Upstreams      []UpstreamEvent   `json:"upstreams"`
	RequestHeaders map[string]string `json:"request_headers,omitempty"`
	QueryString    string            `json:"query_string,omitempty"`
	upstreamSeq    uint32            `json:"-"`
}

type counter struct {
	count   uint64
	totalNs uint64
	bytesTx uint64
	bytesRx uint64
}

// cacheCounter tracks hit/miss counts and store-call latency per cache instruction name.
// Kept under t.mu (same lock as instr) — reads are snapshot-only, no hot-path contention.
type cacheCounter struct {
	hits    uint64
	misses  uint64
	hitNs   uint64 // cumulative store latency for hits
	missNs  uint64 // cumulative store latency for misses
}

// apiStat tracks per-API request counts and latency using atomics.
// Indexed directly by ApiID in Telemetry.apiStats; no map, no GC pressure.
type apiStat struct {
	count          atomic.Uint64
	totalLatencyNs atomic.Int64
	bytesTx        atomic.Int64
	bytesRx        atomic.Int64
}

type metricCounter struct {
	count uint64
	total int64
}

type Config struct {
	Enabled                    bool
	TraceMode                  bool
	SampleRate                 float64
	MaxEvents                  int
	MaxTraces                  int
	InstructionTimingEnabled   bool
	UpstreamPhaseTimingEnabled bool
	AlwaysExportSummary        bool
	ExportQueueSize            int
	MetricQueueSize            int
	InfoLogEnabled             bool
	InfoLogFields              []string
	// TraceHeaderNames lists the request header names to capture per trace.
	// Default: ["Content-Type", "Accept"]. Auth headers are always excluded.
	// Override with RAH_TRACE_CAPTURE_HEADERS (comma-separated, or "*" for all safe headers).
	TraceHeaderNames []string
}

type MetricPoint struct {
	Name  string
	Value int64
	Dims  []KV
}

type ExportSink interface {
	EmitSummary(summary RequestSummary, tenantName string)
	EmitTrace(trace RequestTrace)
}

type LogSink struct{}

func (LogSink) EmitSummary(summary RequestSummary, tenantName string) {
	var tenantField gatewaylog.Field
	if tenantName != "" {
		tenantField = gatewaylog.F("tenant", tenantName)
	} else {
		tenantField = gatewaylog.Fint("tenant_id", int64(summary.TenantID))
	}
	gatewaylog.Default.Info("obs summary",
		gatewaylog.Fint("trace", int64(summary.TraceID)),
		gatewaylog.Fint("api", int64(summary.ApiID)),
		tenantField,
		gatewaylog.Fint("status", int64(summary.Status)),
		gatewaylog.Fint("total_ns", summary.DurationNs),
		gatewaylog.Fint("upstream_ns", summary.UpstreamDurationNs),
		gatewaylog.Fint("client_bytes", summary.ClientBytesSent),
		gatewaylog.Fint("upstream_tx", summary.UpstreamBytesTx),
		gatewaylog.Fint("upstream_rx", summary.UpstreamBytesRx),
	)
}
func (LogSink) EmitTrace(trace RequestTrace) {
	gatewaylog.Default.Info("obs trace",
		gatewaylog.Fint("trace_id", int64(trace.Summary.TraceID)),
		gatewaylog.Fint("upstream_calls", int64(len(trace.Upstreams))),
	)
}

type upstreamLog struct {
	ApiID    uint32
	TenantID uint16
	Event    UpstreamEvent
}

type Telemetry struct {
	cfg            Config
	traceMode      atomic.Bool
	sampleRate10k  atomic.Uint32
	instrEnabled   atomic.Bool
	phaseEnabled   atomic.Bool
	alwaysExport   atomic.Bool
	reqSummaryLog  atomic.Bool
	traceID        atomic.Uint64
	metrics        GatewayMetrics
	droppedExports atomic.Uint64
	metricDropped  atomic.Uint64

	traceRing *TraceSlabRing // lock-free per-request trace writer

	// Background goroutine lifecycle — Stop/Start are idempotent and safe to call
	// concurrently. workerStop is closed to signal workers; data channels stay open.
	lifecycleMu   sync.Mutex
	workerRunning bool
	workerStop    chan struct{}
	workerDone    sync.WaitGroup

	mu        sync.Mutex
	custom    map[string]*metricCounter
	instrTimings map[string]*counter // per-name aggregation, updated by drain goroutine

	captureHeaders []string // immutable after New(); no sync needed
	captureAll     bool     // true when RAH_TRACE_CAPTURE_HEADERS=*

	exportCh   chan RequestTrace
	metricCh   chan MetricPoint
	upstreamCh chan upstreamLog
	sink       ExportSink

	// apiStats is indexed by ApiID for zero-alloc, lock-free per-API request counting.
	// Grows under apiStatsMu only when a higher ApiID is first seen (rare, at deploy time).
	apiStats   atomic.Pointer[[]apiStat]
	apiStatsMu sync.Mutex

	// Metrics is the window-based per-API metrics aggregator. Always non-nil.
	Metrics *MetricsAggregator

	// tenantTracer provides per-tenant trace sample rate overrides.
	// Nil by default — call SetTenantTracer to wire in the registry manager.
	tenantTracer TenantTracer

	// tenantNamer resolves TenantID → human-readable name in async workers.
	// Nil by default — call SetTenantNamer to wire in the registry manager.
	tenantNamer TenantNamer
}

func NewFromEnv() *Telemetry {
	cfg := Config{Enabled: true, TraceMode: false, SampleRate: 0.0, MaxEvents: 128, MaxTraces: 128, InstructionTimingEnabled: true, UpstreamPhaseTimingEnabled: false, AlwaysExportSummary: true, ExportQueueSize: 4096, MetricQueueSize: 4096, InfoLogEnabled: true, InfoLogFields: []string{"api_id", "tenant_id", "status", "duration_ns", "upstream_duration_ns"}, TraceHeaderNames: []string{"Content-Type", "Accept"}}
	if v := strings.TrimSpace(os.Getenv("RAH_OBS_ENABLED")); v != "" {
		cfg.Enabled = v != "0" && strings.ToLower(v) != "false"
	}
	if v := strings.TrimSpace(os.Getenv("RAH_TRACE_MODE")); v != "" {
		cfg.TraceMode = v == "1" || strings.ToLower(v) == "true"
	}
	if v := strings.TrimSpace(os.Getenv("RAH_TRACE_SAMPLE_RATE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			cfg.SampleRate = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("RAH_TRACE_MAX_EVENTS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxEvents = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("RAH_OBS_UPSTREAM_PHASE_TIMING")); v != "" {
		cfg.UpstreamPhaseTimingEnabled = v == "1" || strings.ToLower(v) == "true"
	}
	if v := strings.TrimSpace(os.Getenv("RAH_OBS_INSTRUCTION_TIMING")); v != "" {
		cfg.InstructionTimingEnabled = v != "0" && strings.ToLower(v) != "false"
	}
	if v := strings.TrimSpace(os.Getenv("RAH_OBS_EXPORT_SUMMARY")); v != "" {
		cfg.AlwaysExportSummary = v != "0" && strings.ToLower(v) != "false"
	}
	if v := strings.TrimSpace(os.Getenv("RAH_OBS_INFO_LOG")); v != "" {
		cfg.InfoLogEnabled = v != "0" && strings.ToLower(v) != "false"
	}
	if v := strings.TrimSpace(os.Getenv("RAH_OBS_INFO_LOG_FIELDS")); v != "" {
		parts := strings.Split(v, ",")
		cfg.InfoLogFields = cfg.InfoLogFields[:0]
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.InfoLogFields = append(cfg.InfoLogFields, p)
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv("RAH_TRACE_CAPTURE_HEADERS")); v != "" {
		if v == "*" {
			cfg.TraceHeaderNames = []string{"*"}
		} else {
			parts := strings.Split(v, ",")
			cfg.TraceHeaderNames = cfg.TraceHeaderNames[:0]
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					cfg.TraceHeaderNames = append(cfg.TraceHeaderNames, p)
				}
			}
		}
	}
	return New(cfg)
}

func New(cfg Config) *Telemetry {
	if cfg.ExportQueueSize <= 0 {
		cfg.ExportQueueSize = 4096
	}
	if cfg.MetricQueueSize <= 0 {
		cfg.MetricQueueSize = 4096
	}
	slabCap := ComputeSlabCap()
	ring := NewTraceSlabRing(slabCap)
	ring.Start()
	agg := NewMetricsAggregator(nil)
	t := &Telemetry{cfg: cfg, Metrics: agg, traceRing: ring, custom: make(map[string]*metricCounter), instrTimings: make(map[string]*counter), exportCh: make(chan RequestTrace, cfg.ExportQueueSize), metricCh: make(chan MetricPoint, cfg.MetricQueueSize), upstreamCh: make(chan upstreamLog, cfg.ExportQueueSize), sink: LogSink{}}
	if len(cfg.TraceHeaderNames) == 1 && cfg.TraceHeaderNames[0] == "*" {
		t.captureAll = true
	} else {
		t.captureHeaders = append([]string(nil), cfg.TraceHeaderNames...)
	}
	// Seed trace counter from crypto/rand so IDs are unique per process instance
	// and never collide with rows from a previous container run in Postgres.
	// Same approach as rctx.TxIDGenerator — entropy-seeded, not time-based.
	var seed [8]byte
	if _, err := rand.Read(seed[:]); err == nil {
		t.traceID.Store(binary.LittleEndian.Uint64(seed[:]))
	}
	t.traceMode.Store(cfg.TraceMode)
	t.sampleRate10k.Store(uint32(cfg.SampleRate * 10000))
	t.instrEnabled.Store(cfg.InstructionTimingEnabled)
	t.phaseEnabled.Store(cfg.UpstreamPhaseTimingEnabled)
	t.alwaysExport.Store(cfg.AlwaysExportSummary)
	t.reqSummaryLog.Store(cfg.InfoLogEnabled)
	// Pre-allocate 64 slots — covers most deployments without a single grow.
	initial := make([]apiStat, 64)
	t.apiStats.Store(&initial)
	t.workerStop = make(chan struct{})
	t.workerRunning = true
	t.workerDone.Add(3)
	go t.runExportWorker(t.workerStop)
	go t.runMetricWorker(t.workerStop)
	go t.runUpstreamWorker(t.workerStop)
	return t
}

// Stop stops the TraceSlabRing drain goroutine and the three worker goroutines,
// draining any pending items before returning. Idempotent — safe to call when
// already stopped or never started.
func (t *Telemetry) Stop() {
	t.lifecycleMu.Lock()
	if !t.workerRunning {
		t.lifecycleMu.Unlock()
		return
	}
	t.workerRunning = false
	t.traceRing.StopAndWait()
	close(t.workerStop)
	t.lifecycleMu.Unlock()
	t.workerDone.Wait()
}

// Start restarts the background goroutines after Stop(). Idempotent — safe to
// call when already running.
func (t *Telemetry) Start() {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	if t.workerRunning {
		return
	}
	t.workerStop = make(chan struct{})
	t.traceRing.Start()
	t.workerDone.Add(3)
	go t.runExportWorker(t.workerStop)
	go t.runMetricWorker(t.workerStop)
	go t.runUpstreamWorker(t.workerStop)
	t.workerRunning = true
}

func (t *Telemetry) handleExport(trace RequestTrace) {
	tenantName := t.resolveTenantName(trace.Summary.TenantID)
	if t.sink != nil {
		if trace.Summary.TraceID == 0 {
			t.sink.EmitSummary(trace.Summary, tenantName)
		} else {
			t.sink.EmitTrace(trace)
		}
	}
	if t.reqSummaryLog.Load() {
		bp := summaryBufPool.Get().(*[]byte)
		*bp = appendSummaryFields((*bp)[:0], t.cfg.InfoLogFields, trace.Summary, tenantName)
		gatewaylog.Default.Info(string(*bp))
		summaryBufPool.Put(bp)
	}
}

func (t *Telemetry) runExportWorker(stopCh <-chan struct{}) {
	defer t.workerDone.Done()
	for {
		select {
		case trace := <-t.exportCh:
			t.handleExport(trace)
		case <-stopCh:
			for {
				select {
				case trace := <-t.exportCh:
					t.handleExport(trace)
				default:
					return
				}
			}
		}
	}
}

func (t *Telemetry) handleUpstream(u upstreamLog) {
	tenantName := t.resolveTenantName(u.TenantID)
	var tenantField gatewaylog.Field
	if tenantName != "" {
		tenantField = gatewaylog.F("tenant", tenantName)
	} else {
		tenantField = gatewaylog.Fint("tenant_id", int64(u.TenantID))
	}
	gatewaylog.Default.Info("upstream",
		gatewaylog.Fint("api", int64(u.ApiID)),
		tenantField,
		gatewaylog.Fint("call", int64(u.Event.Seq)),
		gatewaylog.Fint("attempt", int64(u.Event.Attempt)),
		gatewaylog.F("url", u.Event.URL),
		gatewaylog.Fint("status", int64(u.Event.Status)),
		gatewaylog.Ffloat("connect_ms", float64(u.Event.ConnectDurationNs)/1e6),
		gatewaylog.Ffloat("ttfb_ms", float64(u.Event.TTFBNs)/1e6),
		gatewaylog.Ffloat("total_ms", float64(u.Event.TotalNs)/1e6),
		gatewaylog.Fint("bytes_tx", u.Event.BytesSent),
		gatewaylog.Fint("bytes_rx", u.Event.BytesReceived),
		gatewaylog.F("err", fmt.Sprintf("%q", u.Event.Err)),
	)
}

func (t *Telemetry) runUpstreamWorker(stopCh <-chan struct{}) {
	defer t.workerDone.Done()
	for {
		select {
		case u := <-t.upstreamCh:
			t.handleUpstream(u)
		case <-stopCh:
			for {
				select {
				case u := <-t.upstreamCh:
					t.handleUpstream(u)
				default:
					return
				}
			}
		}
	}
}

func (t *Telemetry) handleMetric(p MetricPoint) {
	key := p.Name
	if len(p.Dims) > 0 {
		b := strings.Builder{}
		b.WriteString(key)
		for _, kv := range p.Dims {
			b.WriteByte('|')
			b.WriteString(kv.K)
			b.WriteByte('=')
			b.WriteString(kv.V)
		}
		key = b.String()
	}
	t.mu.Lock()
	mc := t.custom[key]
	if mc == nil {
		mc = &metricCounter{}
		t.custom[key] = mc
	}
	mc.count++
	mc.total += p.Value
	t.mu.Unlock()
}

func (t *Telemetry) runMetricWorker(stopCh <-chan struct{}) {
	defer t.workerDone.Done()
	for {
		select {
		case p := <-t.metricCh:
			t.handleMetric(p)
		case <-stopCh:
			for {
				select {
				case p := <-t.metricCh:
					t.handleMetric(p)
				default:
					return
				}
			}
		}
	}
}

// summaryBufPool holds reusable []byte buffers for zero-alloc summary formatting.
var summaryBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 256); return &b }}

// appendSummaryFields builds a logfmt-style key=value string into buf using
// strconv.Append* — no fmt.Sprintf, no intermediate []string, no strings.Join.
func appendSummaryFields(buf []byte, fields []string, s RequestSummary, tenantName string) []byte {
	first := true
	for _, f := range fields {
		var key string
		var val int64
		var uval uint64
		var sval string
		unsigned := false
		switch f {
		case "trace_id":
			key, uval, unsigned = "trace_id", s.TraceID, true
		case "api_id":
			key, val = "api_id", int64(s.ApiID)
		case "tenant":
			// Prefer human-readable name; fall back to numeric ID.
			if tenantName != "" {
				key, sval = "tenant", tenantName
			} else {
				key, val = "tenant_id", int64(s.TenantID)
			}
		case "tenant_id":
			key, val = "tenant_id", int64(s.TenantID)
		case "status":
			key, val = "status", int64(s.Status)
		case "duration_ns":
			key, val = "duration_ns", s.DurationNs
		case "gateway_duration_ns":
			key, val = "gateway_duration_ns", s.GatewayDurationNs
		case "upstream_duration_ns":
			key, val = "upstream_duration_ns", s.UpstreamDurationNs
		case "upstream_calls":
			key, val = "upstream_calls", int64(s.UpstreamCalls)
		case "client_bytes_sent":
			key, val = "client_bytes_sent", s.ClientBytesSent
		case "upstream_bytes_tx":
			key, val = "upstream_bytes_tx", s.UpstreamBytesTx
		case "upstream_bytes_rx":
			key, val = "upstream_bytes_rx", s.UpstreamBytesRx
		default:
			continue
		}
		if key == "" {
			continue
		}
		if !first {
			buf = append(buf, ' ')
		}
		first = false
		buf = append(buf, key...)
		buf = append(buf, '=')
		if sval != "" {
			buf = append(buf, sval...)
		} else if unsigned {
			buf = strconv.AppendUint(buf, uval, 10)
		} else {
			buf = strconv.AppendInt(buf, val, 10)
		}
	}
	return buf
}

func (t *Telemetry) Enabled() bool                    { return t != nil && t.cfg.Enabled }
func (t *Telemetry) ShouldTimeRequests() bool         { return t.Enabled() }
func (t *Telemetry) InstructionTimingEnabled() bool   { return t.Enabled() && t.instrEnabled.Load() }
func (t *Telemetry) UpstreamPhaseTimingEnabled() bool { return t.Enabled() && t.phaseEnabled.Load() }

// TenantTracer resolves per-tenant trace sampling overrides.
// *registry.RegistryManager satisfies this interface via structural typing.
type TenantTracer interface {
	// TenantTraceSampleRate returns the override sample rate for a tenant.
	// Returns (0, false) if the tenant has no override or is not found.
	// Returns (1.0, true) if DebugEnabled is set for the tenant.
	TenantTraceSampleRate(tenantID uint16) (rate float64, hasOverride bool)
}

// SetTenantTracer wires a per-tenant trace override resolver into Telemetry.
// Pass nil to disable per-tenant overrides (falls back to global sampling).
func (t *Telemetry) SetTenantTracer(tt TenantTracer) {
	t.tenantTracer = tt
}

// TenantNamer resolves a numeric TenantID to a human-readable name.
// *registry.RegistryManager satisfies this interface via structural typing.
type TenantNamer interface {
	TenantName(tenantID uint16) string
}

// SetTenantNamer wires a TenantID → name resolver into Telemetry.
// Resolution happens in the async export/upstream workers, not the hot path.
func (t *Telemetry) SetTenantNamer(tn TenantNamer) {
	t.tenantNamer = tn
}

// resolveTenantName returns the human-readable name for tenantID, or an empty
// string when tenantID is 0 or no namer is configured.
func (t *Telemetry) resolveTenantName(tenantID uint16) string {
	if tenantID == 0 || t.tenantNamer == nil {
		return ""
	}
	return t.tenantNamer.TenantName(tenantID)
}

func (t *Telemetry) ShouldTrace() bool {
	if !t.Enabled() || !t.traceMode.Load() {
		return false
	}
	r := t.sampleRate10k.Load()
	if r == 0 {
		return false
	}
	return uint32(time.Now().UnixNano()%10000) < r
}

// ShouldTraceTenant is like ShouldTrace but applies a per-tenant override when
// one is configured. If tenantID is 0 (not yet resolved) or no tenantTracer is
// set, it falls through to the global ShouldTrace decision.
func (t *Telemetry) ShouldTraceTenant(tenantID uint16) bool {
	if tenantID != 0 && t.tenantTracer != nil {
		if rate, ok := t.tenantTracer.TenantTraceSampleRate(tenantID); ok {
			return t.shouldSampleAt(rate)
		}
	}
	return t.ShouldTrace()
}

// shouldSampleAt returns true if the request should be sampled at the given rate
// (0.0–1.0). Uses the same time-modulo approach as ShouldTrace for consistency.
func (t *Telemetry) shouldSampleAt(rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1.0 {
		return true
	}
	threshold := uint32(rate * 10000)
	return uint32(time.Now().UnixNano()%10000) < threshold
}

func (t *Telemetry) StartRequest(apiID uint32, apiVersionID uint32, tenantID uint16, method, path string) RequestTrace {
	id := t.traceID.Add(1)
	now := time.Now().UnixNano()
	return RequestTrace{Summary: RequestSummary{TraceID: id, ApiID: apiID, ApiVersionID: apiVersionID, TenantID: tenantID, Method: method, Path: path, StartedAtUnixNano: now}, Upstreams: make([]UpstreamEvent, 0, 4)}
}

// CaptureRequestHeaders records the incoming request headers (and query string)
// onto the trace. Only headers listed in TraceHeaderNames are captured.
// Authorization and X-API-Key are never captured regardless of configuration.
// Call this immediately after StartRequest, only when ctx.Trace != nil.
func (t *Telemetry) CaptureRequestHeaders(trace *RequestTrace, r *http.Request, queryString string) {
	if trace == nil || r == nil {
		return
	}
	if queryString != "" {
		trace.QueryString = queryString
	}
	if !t.captureAll && len(t.captureHeaders) == 0 {
		return
	}

	// Build a set of names to capture for fast lookup.
	headers := make(map[string]string, 8)
	if t.captureAll {
		for name, vals := range r.Header {
			lower := strings.ToLower(name)
			if lower == "authorization" || lower == "x-api-key" || lower == "cookie" {
				continue // always skip sensitive auth headers
			}
			if len(vals) > 0 {
				headers[name] = vals[0]
			}
		}
	} else {
		for _, name := range t.captureHeaders {
			lower := strings.ToLower(name)
			if lower == "authorization" || lower == "x-api-key" || lower == "cookie" {
				continue
			}
			if v := r.Header.Get(name); v != "" {
				headers[name] = v
			}
		}
	}
	if len(headers) > 0 {
		trace.RequestHeaders = headers
	}
}

func (t *Telemetry) QueueMetric(point MetricPoint) {
	if !t.Enabled() {
		return
	}
	select {
	case t.metricCh <- point:
	default:
		t.metricDropped.Add(1)
	}
}

// RecordCacheOp records a single cache GET outcome: whether it was a hit or miss,
// and how long the underlying store call took (excluding slot writes and overhead).
// name should be the instruction name, e.g. "cache_get" or "cache_get_global".
// RecordCacheOp is a no-op. Cache stats are now aggregated via the ingest pipeline.
func (t *Telemetry) RecordCacheOp(_ string, _ bool, _ int64) {}

// RecordInstruction is a no-op. Per-instruction aggregation is now handled
// lock-free via instrSlabRing (see internal/observability/instr_slab.go).
func (t *Telemetry) RecordInstruction(_ string, _ time.Duration) {}

// RecordInstrTiming accumulates per-instruction-name timing for InstructionTopSlow.
// Called from the instrSlabRing drain goroutine after PC→name resolution.
// Safe to call concurrently; protected by t.mu (drain is single-threaded, but
// Snapshot reads concurrently).
func (t *Telemetry) RecordInstrTiming(name string, durationNs int64) {
	if !t.Enabled() || durationNs <= 0 {
		return
	}
	t.mu.Lock()
	c := t.instrTimings[name]
	if c == nil {
		c = &counter{}
		t.instrTimings[name] = c
	}
	c.count++
	c.totalNs += uint64(durationNs)
	t.mu.Unlock()
}

// RecordUpstream is a no-op. Upstream stats are aggregated via the ingest pipeline.
func (t *Telemetry) RecordUpstream(_ string, _ time.Duration, _, _ int64) {}

func (t *Telemetry) LogUpstream(apiID uint32, tenantID uint16, event UpstreamEvent) {
	if !t.Enabled() || !gatewaylog.Default.ShouldLog(gatewaylog.INFO) {
		return
	}
	select {
	case t.upstreamCh <- upstreamLog{ApiID: apiID, TenantID: tenantID, Event: event}:
	default:
		t.droppedExports.Add(1)
	}
}

func (t *Telemetry) FinishRequest(trace *RequestTrace, status int, total, gateway, upstream time.Duration, upstreamCalls int, clientBytesSent, upstreamBytesTx, upstreamBytesRx int64) {
	if !t.Enabled() {
		return
	}
	atomic.AddUint64(&t.metrics.RequestsTotal, 1)
	switch status / 100 {
	case 2:
		atomic.AddUint64(&t.metrics.Requests2xx, 1)
	case 3:
		atomic.AddUint64(&t.metrics.Requests3xx, 1)
	case 4:
		atomic.AddUint64(&t.metrics.Requests4xx, 1)
	case 5:
		atomic.AddUint64(&t.metrics.Requests5xx, 1)
	}
	atomic.AddUint64(&t.metrics.GatewayLatencyTotalNs, uint64(gateway))
	atomic.AddUint64(&t.metrics.UpstreamLatencyTotalNs, uint64(upstream))
	if clientBytesSent > 0 {
		atomic.AddUint64(&t.metrics.ClientBytesSentTotal, uint64(clientBytesSent))
	}
	if upstreamBytesTx > 0 {
		atomic.AddUint64(&t.metrics.UpstreamBytesTxTotal, uint64(upstreamBytesTx))
	}
	if upstreamBytesRx > 0 {
		atomic.AddUint64(&t.metrics.UpstreamBytesRxTotal, uint64(upstreamBytesRx))
	}
	atomic.StoreInt64(&t.metrics.LastRequestUnixNano, time.Now().UnixNano())

	summary := RequestSummary{Status: status, DurationNs: total.Nanoseconds(), GatewayDurationNs: gateway.Nanoseconds(), UpstreamDurationNs: upstream.Nanoseconds(), UpstreamCalls: upstreamCalls, ClientBytesSent: clientBytesSent, UpstreamBytesTx: upstreamBytesTx, UpstreamBytesRx: upstreamBytesRx}
	if trace == nil {
		if t.alwaysExport.Load() {
			t.enqueueExport(RequestTrace{Summary: summary})
		}
		return
	}
	trace.Summary.Status = status
	trace.Summary.DurationNs = summary.DurationNs
	trace.Summary.GatewayDurationNs = summary.GatewayDurationNs
	trace.Summary.UpstreamDurationNs = summary.UpstreamDurationNs
	trace.Summary.UpstreamCalls = summary.UpstreamCalls
	trace.Summary.ClientBytesSent = summary.ClientBytesSent
	trace.Summary.UpstreamBytesTx = summary.UpstreamBytesTx
	trace.Summary.UpstreamBytesRx = summary.UpstreamBytesRx
	// Per-tenant/API status + latency — lock-free via MetricsAggregator COW map.
	t.Metrics.Record(trace.Summary.TenantID, trace.Summary.ApiID, status, total.Nanoseconds())

	// Lock-free write: claims 1 header slot (header-only mode, no instruction sub-slots).
	t.traceRing.Write(trace)

	t.QueueMetric(MetricPoint{Name: "requests", Value: 1, Dims: []KV{{K: "api", V: strconv.FormatUint(uint64(trace.Summary.ApiID), 10)}, {K: "tenant", V: strconv.FormatUint(uint64(trace.Summary.TenantID), 10)}, {K: "status", V: strconv.Itoa(status)}}})
	t.enqueueExport(*trace)
}

func (t *Telemetry) enqueueExport(trace RequestTrace) {
	select {
	case t.exportCh <- trace:
	default:
		t.droppedExports.Add(1)
	}
}

func (t *Telemetry) AppendUpstreamEvent(trace *RequestTrace, e UpstreamEvent) {
	if trace == nil {
		return
	}
	if t.cfg.MaxEvents > 0 && len(trace.Upstreams) >= t.cfg.MaxEvents {
		return
	}
	trace.upstreamSeq++
	e.Seq = trace.upstreamSeq
	trace.Upstreams = append(trace.Upstreams, e)
}

// RecordRequest records per-API request stats using atomic operations.
// Called post-response (not on the critical latency path).
// apiID is used as a direct slice index — no map, no string keys, no GC overhead.
func (t *Telemetry) RecordRequest(apiID uint32, totalNs, reqBytes, resBytes int64) {
	if !t.Enabled() || apiID == 0 {
		return
	}
	idx := int(apiID)
	s := t.apiStats.Load()
	if s != nil && idx < len(*s) {
		p := &(*s)[idx]
		p.count.Add(1)
		if totalNs > 0 {
			p.totalLatencyNs.Add(totalNs)
		}
		if reqBytes > 0 {
			p.bytesTx.Add(reqBytes)
		}
		if resBytes > 0 {
			p.bytesRx.Add(resBytes)
		}
		return
	}
	// Grow the slice — only triggered when a new higher-ID API is first seen (rare, at deploy time).
	t.apiStatsMu.Lock()
	defer t.apiStatsMu.Unlock()
	s = t.apiStats.Load() // re-read under lock; another goroutine may have grown it
	if s != nil && idx < len(*s) {
		p := &(*s)[idx]
		p.count.Add(1)
		if totalNs > 0 {
			p.totalLatencyNs.Add(totalNs)
		}
		if reqBytes > 0 {
			p.bytesTx.Add(reqBytes)
		}
		if resBytes > 0 {
			p.bytesRx.Add(resBytes)
		}
		return
	}
	newCap := max(idx+32, 64) // grow in chunks to amortise future allocations
	newSlice := make([]apiStat, newCap)
	if s != nil {
		copy(newSlice, *s)
	}
	t.apiStats.Store(&newSlice)
	p := &newSlice[idx]
	p.count.Add(1)
	if totalNs > 0 {
		p.totalLatencyNs.Add(totalNs)
	}
	if reqBytes > 0 {
		p.bytesTx.Add(reqBytes)
	}
	if resBytes > 0 {
		p.bytesRx.Add(resBytes)
	}
}

// APITop returns per-API request stats sorted by request count descending.
// nameResolver maps ApiID → API name; entries where the resolver returns "" are skipped.
// Intended for infrequent snapshot reads (UI polling), not hot path.
func (t *Telemetry) APITop(topN int, nameResolver func(uint32) string) []NameLatency {
	if topN <= 0 {
		topN = 20
	}
	s := t.apiStats.Load()
	if s == nil || len(*s) == 0 || nameResolver == nil {
		return nil
	}
	out := make([]NameLatency, 0, topN)
	for i := range *s {
		p := &(*s)[i]
		cnt := p.count.Load()
		if cnt == 0 {
			continue
		}
		name := nameResolver(uint32(i))
		if name == "" {
			continue
		}
		out = append(out, NameLatency{
			Name:           name,
			Count:          cnt,
			TotalLatencyNs: uint64(p.totalLatencyNs.Load()),
			BytesTx:        uint64(p.bytesTx.Load()),
			BytesRx:        uint64(p.bytesRx.Load()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

func topNFromMap(m map[string]*counter, n int) []NameLatency {
	out := make([]NameLatency, 0, len(m))
	for k, v := range m {
		out = append(out, NameLatency{Name: k, Count: v.count, TotalLatencyNs: v.totalNs, BytesTx: v.bytesTx, BytesRx: v.bytesRx})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalLatencyNs > out[j].TotalLatencyNs })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func cacheStatsSlice(m map[string]*cacheCounter) []CacheStat {
	out := make([]CacheStat, 0, len(m))
	for name, c := range m {
		total := c.hits + c.misses
		hitRate := 0.0
		if total > 0 {
			hitRate = float64(c.hits) / float64(total)
		}
		avgHitNs := uint64(0)
		if c.hits > 0 {
			avgHitNs = c.hitNs / c.hits
		}
		avgMissNs := uint64(0)
		if c.misses > 0 {
			avgMissNs = c.missNs / c.misses
		}
		out = append(out, CacheStat{
			Name: name, Hits: c.hits, Misses: c.misses,
			HitRate: hitRate, AvgHitNs: avgHitNs, AvgMissNs: avgMissNs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return (out[i].Hits + out[i].Misses) > (out[j].Hits + out[j].Misses)
	})
	return out
}

func topNMetrics(m map[string]*metricCounter, n int) []MetricAgg {
	out := make([]MetricAgg, 0, len(m))
	for k, v := range m {
		out = append(out, MetricAgg{Key: k, Count: v.count, TotalValue: v.total})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalValue > out[j].TotalValue })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// tenantTopFromAgg builds per-tenant status counts from MetricsAggregator's COW map.
// Groups by TenantID (aggregating across all APIs for that tenant), sorts by total
// requests descending, returns top-n. No lock held — reads atomic pointer snapshot.
func tenantTopFromAgg(agg *MetricsAggregator, n int) []TenantStatusCounts {
	mp := agg.metrics.Load()
	if mp == nil || len(*mp) == 0 {
		return nil
	}
	byTenant := make(map[uint16]*TenantStatusCounts, len(*mp))
	for _, m := range *mp {
		tc := byTenant[m.TenantID]
		if tc == nil {
			tc = &TenantStatusCounts{TenantID: m.TenantID}
			byTenant[m.TenantID] = tc
		}
		tc.Requests2xx += uint64(m.Count2xx.Load())
		tc.Requests3xx += uint64(m.CountOther.Load()) // 1xx/3xx lumped in CountOther
		tc.Requests4xx += uint64(m.Count4xx.Load())
		tc.Requests5xx += uint64(m.Count5xx.Load())
	}
	out := make([]TenantStatusCounts, 0, len(byTenant))
	for _, v := range byTenant {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		ti := out[i].Requests2xx + out[i].Requests3xx + out[i].Requests4xx + out[i].Requests5xx
		tj := out[j].Requests2xx + out[j].Requests3xx + out[j].Requests4xx + out[j].Requests5xx
		return ti > tj
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (t *Telemetry) Snapshot(topN int) map[string]any {
	if topN <= 0 {
		topN = 10
	}
	m := GatewayMetrics{
		RequestsTotal:          atomic.LoadUint64(&t.metrics.RequestsTotal),
		Requests2xx:            atomic.LoadUint64(&t.metrics.Requests2xx),
		Requests3xx:            atomic.LoadUint64(&t.metrics.Requests3xx),
		Requests4xx:            atomic.LoadUint64(&t.metrics.Requests4xx),
		Requests5xx:            atomic.LoadUint64(&t.metrics.Requests5xx),
		GatewayLatencyTotalNs:  atomic.LoadUint64(&t.metrics.GatewayLatencyTotalNs),
		UpstreamLatencyTotalNs: atomic.LoadUint64(&t.metrics.UpstreamLatencyTotalNs),
		ClientBytesSentTotal:   atomic.LoadUint64(&t.metrics.ClientBytesSentTotal),
		UpstreamBytesTxTotal:   atomic.LoadUint64(&t.metrics.UpstreamBytesTxTotal),
		UpstreamBytesRxTotal:   atomic.LoadUint64(&t.metrics.UpstreamBytesRxTotal),
		LastRequestUnixNano:    atomic.LoadInt64(&t.metrics.LastRequestUnixNano),
		DroppedExports:         t.droppedExports.Load(),
	}
	m.TenantTopErrors = tenantTopFromAgg(t.Metrics, topN)
	t.mu.Lock()
	m.CustomMetricTop = topNMetrics(t.custom, topN)
	m.InstructionTopSlow = topNFromMap(t.instrTimings, topN)
	t.mu.Unlock()
	traces := t.traceRing.Snapshot() // lock-free atomic pointer load
	cfg := map[string]any{"trace_mode": t.traceMode.Load(), "trace_sample_rate": float64(t.sampleRate10k.Load()) / 10000.0, "instruction_timing_enabled": t.instrEnabled.Load(), "upstream_phase_timing_enabled": t.phaseEnabled.Load(), "always_export_summary": t.alwaysExport.Load(), "info_log_enabled": t.reqSummaryLog.Load(), "info_log_fields": t.cfg.InfoLogFields, "max_events": t.cfg.MaxEvents, "max_traces": t.cfg.MaxTraces, "export_queue_size": cap(t.exportCh), "metric_queue_size": cap(t.metricCh), "metric_dropped": t.metricDropped.Load()}
	return map[string]any{"metrics": m, "recent_traces": traces, "config": cfg, "export": map[string]any{"otel": "use sink implementation", "bigquery": "use sink implementation"}, "api_key_stats": apikey.Global.Snapshot()}
}

func (t *Telemetry) UpdateConfig(traceMode *bool, sampleRate *float64, instructionTiming *bool, upstreamPhaseTiming *bool, alwaysExportSummary *bool, infoLogEnabled *bool, infoLogFields []string) {
	if traceMode != nil {
		t.traceMode.Store(*traceMode)
	}
	if sampleRate != nil {
		v := *sampleRate
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		t.sampleRate10k.Store(uint32(v * 10000))
	}
	if instructionTiming != nil {
		t.instrEnabled.Store(*instructionTiming)
	}
	if upstreamPhaseTiming != nil {
		t.phaseEnabled.Store(*upstreamPhaseTiming)
	}
	if alwaysExportSummary != nil {
		t.alwaysExport.Store(*alwaysExportSummary)
	}
	if infoLogEnabled != nil {
		t.reqSummaryLog.Store(*infoLogEnabled)
	}
	if len(infoLogFields) > 0 {
		t.cfg.InfoLogFields = append([]string(nil), infoLogFields...)
	}
}

func (t *Telemetry) DebugHandler(w http.ResponseWriter, r *http.Request) {
	if !t.Enabled() {
		http.Error(w, "observability disabled", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(t.Snapshot(20))
	case http.MethodPost, http.MethodPatch:
		var payload struct {
			TraceMode           *bool    `json:"trace_mode"`
			TraceSampleRate     *float64 `json:"trace_sample_rate"`
			InstructionTiming   *bool    `json:"instruction_timing_enabled"`
			UpstreamPhaseTiming *bool    `json:"upstream_phase_timing_enabled"`
			AlwaysExportSummary *bool    `json:"always_export_summary"`
			InfoLogEnabled      *bool    `json:"info_log_enabled"`
			InfoLogFields       []string `json:"info_log_fields"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		t.UpdateConfig(payload.TraceMode, payload.TraceSampleRate, payload.InstructionTiming, payload.UpstreamPhaseTiming, payload.AlwaysExportSummary, payload.InfoLogEnabled, payload.InfoLogFields)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(t.Snapshot(20))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
