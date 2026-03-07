package observability

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type KV struct {
	K string `json:"k"`
	V string `json:"v"`
}

type GatewayMetrics struct {
	RequestsTotal          uint64        `json:"requests_total"`
	Requests5xx            uint64        `json:"requests_5xx"`
	GatewayLatencyTotalNs  uint64        `json:"gateway_latency_total_ns"`
	UpstreamLatencyTotalNs uint64        `json:"upstream_latency_total_ns"`
	ClientBytesSentTotal   uint64        `json:"client_bytes_sent_total"`
	UpstreamBytesTxTotal   uint64        `json:"upstream_bytes_tx_total"`
	UpstreamBytesRxTotal   uint64        `json:"upstream_bytes_rx_total"`
	LastRequestUnixNano    int64         `json:"last_request_unix_nano"`
	DroppedExports         uint64        `json:"dropped_exports"`
	InstructionTopSlow     []NameLatency `json:"instruction_top_slow"`
	UpstreamTopSlow        []NameLatency `json:"upstream_top_slow"`
	TenantTop5xx           []TenantError `json:"tenant_top_5xx"`
	CustomMetricTop        []MetricAgg   `json:"custom_metric_top"`
}

type NameLatency struct {
	Name           string `json:"name"`
	Count          uint64 `json:"count"`
	TotalLatencyNs uint64 `json:"total_latency_ns"`
	BytesTx        uint64 `json:"bytes_tx"`
	BytesRx        uint64 `json:"bytes_rx"`
}

type TenantError struct {
	TenantID  uint16 `json:"tenant_id"`
	Errors5xx uint64 `json:"errors_5xx"`
}

type MetricAgg struct {
	Key        string `json:"key"`
	Count      uint64 `json:"count"`
	TotalValue int64  `json:"total_value"`
}

type RequestSummary struct {
	TraceID               uint64 `json:"trace_id"`
	ApiID                 uint32 `json:"api_id"`
	TenantID              uint16 `json:"tenant_id"`
	Status                int    `json:"status"`
	DurationNs            int64  `json:"duration_ns"`
	GatewayDurationNs     int64  `json:"gateway_duration_ns"`
	UpstreamDurationNs    int64  `json:"upstream_duration_ns"`
	UpstreamCalls         int    `json:"upstream_calls"`
	ClientBytesSent       int64  `json:"client_bytes_sent"`
	UpstreamBytesTx       int64  `json:"upstream_bytes_tx"`
	UpstreamBytesRx       int64  `json:"upstream_bytes_rx"`
	InstructionEventCount int    `json:"instruction_event_count"`
	StartedAtUnixNano     int64  `json:"started_at_unix_nano"`
}

type InstructionEvent struct {
	Seq        uint32 `json:"seq"`
	Name       string `json:"name"`
	PC         int16  `json:"pc"`
	DurationNs int64  `json:"duration_ns"`
	Input      []KV   `json:"input,omitempty"`
	Output     []KV   `json:"output,omitempty"`
}

type UpstreamEvent struct {
	Seq               uint32 `json:"seq"`
	Attempt           int    `json:"attempt"`
	Host              string `json:"host"`
	Status            int    `json:"status"`
	Err               string `json:"err,omitempty"`
	ConnReused        bool   `json:"conn_reused"`
	ConnIdle          bool   `json:"conn_idle"`
	DNSDurationNs     int64  `json:"dns_duration_ns"`
	ConnectDurationNs int64  `json:"connect_duration_ns"`
	TLSDurationNs     int64  `json:"tls_duration_ns"`
	TTFBNs            int64  `json:"ttfb_ns"`
	TotalNs           int64  `json:"total_ns"`
	BytesSent         int64  `json:"bytes_sent"`
	BytesReceived     int64  `json:"bytes_received"`
}

type RequestTrace struct {
	Summary        RequestSummary     `json:"summary"`
	Instructions   []InstructionEvent `json:"instructions"`
	Upstreams      []UpstreamEvent    `json:"upstreams"`
	instructionSeq uint32             `json:"-"`
	upstreamSeq    uint32             `json:"-"`
}

type counter struct {
	count   uint64
	totalNs uint64
	bytesTx uint64
	bytesRx uint64
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
}

type MetricPoint struct {
	Name  string
	Value int64
	Dims  []KV
}

type ExportSink interface {
	EmitSummary(summary RequestSummary)
	EmitTrace(trace RequestTrace)
}

type LogSink struct{}

func (LogSink) EmitSummary(summary RequestSummary) {
	log.Printf("[obs] summary trace=%d api=%d tenant=%d status=%d total_ns=%d upstream_ns=%d client_bytes=%d upstream_tx=%d upstream_rx=%d", summary.TraceID, summary.ApiID, summary.TenantID, summary.Status, summary.DurationNs, summary.UpstreamDurationNs, summary.ClientBytesSent, summary.UpstreamBytesTx, summary.UpstreamBytesRx)
}
func (LogSink) EmitTrace(trace RequestTrace) {
	log.Printf("[obs] trace_id=%d instructions=%d upstream_calls=%d", trace.Summary.TraceID, len(trace.Instructions), len(trace.Upstreams))
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
	infoLog        atomic.Bool
	traceID        atomic.Uint64
	metrics        GatewayMetrics
	droppedExports atomic.Uint64
	metricDropped  atomic.Uint64

	mu        sync.Mutex
	instr     map[string]*counter
	upstream  map[string]*counter
	tenant5xx map[uint16]uint64
	traces    []RequestTrace
	custom    map[string]*metricCounter

	exportCh   chan RequestTrace
	metricCh   chan MetricPoint
	upstreamCh chan upstreamLog
	sink       ExportSink
}

func NewFromEnv() *Telemetry {
	cfg := Config{Enabled: true, TraceMode: false, SampleRate: 0.0, MaxEvents: 128, MaxTraces: 128, InstructionTimingEnabled: true, UpstreamPhaseTimingEnabled: false, AlwaysExportSummary: true, ExportQueueSize: 4096, MetricQueueSize: 4096, InfoLogEnabled: true, InfoLogFields: []string{"api_id", "tenant_id", "status", "duration_ns", "upstream_duration_ns"}}
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
	return New(cfg)
}

func New(cfg Config) *Telemetry {
	if cfg.ExportQueueSize <= 0 {
		cfg.ExportQueueSize = 4096
	}
	if cfg.MetricQueueSize <= 0 {
		cfg.MetricQueueSize = 4096
	}
	t := &Telemetry{cfg: cfg, instr: make(map[string]*counter), upstream: make(map[string]*counter), tenant5xx: make(map[uint16]uint64), traces: make([]RequestTrace, 0, cfg.MaxTraces), custom: make(map[string]*metricCounter), exportCh: make(chan RequestTrace, cfg.ExportQueueSize), metricCh: make(chan MetricPoint, cfg.MetricQueueSize), upstreamCh: make(chan upstreamLog, cfg.ExportQueueSize), sink: LogSink{}}
	t.traceMode.Store(cfg.TraceMode)
	t.sampleRate10k.Store(uint32(cfg.SampleRate * 10000))
	t.instrEnabled.Store(cfg.InstructionTimingEnabled)
	t.phaseEnabled.Store(cfg.UpstreamPhaseTimingEnabled)
	t.alwaysExport.Store(cfg.AlwaysExportSummary)
	t.infoLog.Store(cfg.InfoLogEnabled)
	go t.exportWorker()
	go t.metricWorker()
	go t.upstreamWorker()
	return t
}

func (t *Telemetry) exportWorker() {
	for trace := range t.exportCh {
		if t.sink != nil {
			if trace.Summary.TraceID == 0 {
				t.sink.EmitSummary(trace.Summary)
			} else {
				t.sink.EmitTrace(trace)
			}
		}
		if t.infoLog.Load() {
			log.Printf("[obs.info] %s", t.formatSummary(trace.Summary))
		}
	}
}

func (t *Telemetry) upstreamWorker() {
	for u := range t.upstreamCh {
		if t.infoLog.Load() {
			log.Printf("[obs.upstream] api=%d tenant=%d seq=%d attempt=%d host=%s status=%d err=%q conn_ns=%d tls_ns=%d ttfb_ns=%d total_ns=%d bytes_tx=%d bytes_rx=%d", u.ApiID, u.TenantID, u.Event.Seq, u.Event.Attempt, u.Event.Host, u.Event.Status, u.Event.Err, u.Event.ConnectDurationNs, u.Event.TLSDurationNs, u.Event.TTFBNs, u.Event.TotalNs, u.Event.BytesSent, u.Event.BytesReceived)
		}
	}
}

func (t *Telemetry) metricWorker() {
	for p := range t.metricCh {
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
}

func (t *Telemetry) formatSummary(s RequestSummary) string {
	vals := make([]string, 0, len(t.cfg.InfoLogFields))
	for _, f := range t.cfg.InfoLogFields {
		switch f {
		case "trace_id":
			vals = append(vals, fmt.Sprintf("trace_id=%d", s.TraceID))
		case "api_id":
			vals = append(vals, fmt.Sprintf("api_id=%d", s.ApiID))
		case "tenant_id":
			vals = append(vals, fmt.Sprintf("tenant_id=%d", s.TenantID))
		case "status":
			vals = append(vals, fmt.Sprintf("status=%d", s.Status))
		case "duration_ns":
			vals = append(vals, fmt.Sprintf("duration_ns=%d", s.DurationNs))
		case "gateway_duration_ns":
			vals = append(vals, fmt.Sprintf("gateway_duration_ns=%d", s.GatewayDurationNs))
		case "upstream_duration_ns":
			vals = append(vals, fmt.Sprintf("upstream_duration_ns=%d", s.UpstreamDurationNs))
		case "upstream_calls":
			vals = append(vals, fmt.Sprintf("upstream_calls=%d", s.UpstreamCalls))
		case "client_bytes_sent":
			vals = append(vals, fmt.Sprintf("client_bytes_sent=%d", s.ClientBytesSent))
		case "upstream_bytes_tx":
			vals = append(vals, fmt.Sprintf("upstream_bytes_tx=%d", s.UpstreamBytesTx))
		case "upstream_bytes_rx":
			vals = append(vals, fmt.Sprintf("upstream_bytes_rx=%d", s.UpstreamBytesRx))
		}
	}
	return strings.Join(vals, " ")
}

func (t *Telemetry) Enabled() bool                    { return t != nil && t.cfg.Enabled }
func (t *Telemetry) ShouldTimeRequests() bool         { return t.Enabled() }
func (t *Telemetry) InstructionTimingEnabled() bool   { return t.Enabled() && t.instrEnabled.Load() }
func (t *Telemetry) UpstreamPhaseTimingEnabled() bool { return t.Enabled() && t.phaseEnabled.Load() }

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

func (t *Telemetry) StartRequest(apiID uint32, tenantID uint16) RequestTrace {
	id := t.traceID.Add(1)
	now := time.Now().UnixNano()
	return RequestTrace{Summary: RequestSummary{TraceID: id, ApiID: apiID, TenantID: tenantID, StartedAtUnixNano: now}, Instructions: make([]InstructionEvent, 0, 16), Upstreams: make([]UpstreamEvent, 0, 4)}
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

func (t *Telemetry) RecordInstruction(name string, d time.Duration) {
	if !t.InstructionTimingEnabled() {
		return
	}
	t.mu.Lock()
	c := t.instr[name]
	if c == nil {
		c = &counter{}
		t.instr[name] = c
	}
	c.count++
	c.totalNs += uint64(d)
	t.mu.Unlock()
}

func (t *Telemetry) RecordUpstream(host string, d time.Duration, bytesTx, bytesRx int64) {
	if !t.Enabled() {
		return
	}
	t.mu.Lock()
	c := t.upstream[host]
	if c == nil {
		c = &counter{}
		t.upstream[host] = c
	}
	c.count++
	c.totalNs += uint64(d)
	if bytesTx > 0 {
		c.bytesTx += uint64(bytesTx)
	}
	if bytesRx > 0 {
		c.bytesRx += uint64(bytesRx)
	}
	t.mu.Unlock()
}

func (t *Telemetry) LogUpstream(apiID uint32, tenantID uint16, event UpstreamEvent) {
	if !t.Enabled() {
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
	if status >= 500 {
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
	trace.Summary.InstructionEventCount = len(trace.Instructions)

	t.mu.Lock()
	if status >= 500 {
		t.tenant5xx[trace.Summary.TenantID]++
	}
	if t.cfg.MaxTraces <= 0 {
		t.cfg.MaxTraces = 128
	}
	if len(t.traces) >= t.cfg.MaxTraces {
		copy(t.traces, t.traces[1:])
		t.traces[len(t.traces)-1] = *trace
	} else {
		t.traces = append(t.traces, *trace)
	}
	t.mu.Unlock()

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

func (t *Telemetry) AppendInstructionEvent(trace *RequestTrace, e InstructionEvent) {
	if trace == nil {
		return
	}
	if t.cfg.MaxEvents > 0 && len(trace.Instructions) >= t.cfg.MaxEvents {
		return
	}
	trace.instructionSeq++
	e.Seq = trace.instructionSeq
	trace.Instructions = append(trace.Instructions, e)
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

func topNTenants(m map[uint16]uint64, n int) []TenantError {
	out := make([]TenantError, 0, len(m))
	for k, v := range m {
		out = append(out, TenantError{TenantID: k, Errors5xx: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Errors5xx > out[j].Errors5xx })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (t *Telemetry) Snapshot(topN int) map[string]any {
	if topN <= 0 {
		topN = 10
	}
	m := GatewayMetrics{RequestsTotal: atomic.LoadUint64(&t.metrics.RequestsTotal), Requests5xx: atomic.LoadUint64(&t.metrics.Requests5xx), GatewayLatencyTotalNs: atomic.LoadUint64(&t.metrics.GatewayLatencyTotalNs), UpstreamLatencyTotalNs: atomic.LoadUint64(&t.metrics.UpstreamLatencyTotalNs), ClientBytesSentTotal: atomic.LoadUint64(&t.metrics.ClientBytesSentTotal), UpstreamBytesTxTotal: atomic.LoadUint64(&t.metrics.UpstreamBytesTxTotal), UpstreamBytesRxTotal: atomic.LoadUint64(&t.metrics.UpstreamBytesRxTotal), LastRequestUnixNano: atomic.LoadInt64(&t.metrics.LastRequestUnixNano), DroppedExports: t.droppedExports.Load()}
	t.mu.Lock()
	m.InstructionTopSlow = topNFromMap(t.instr, topN)
	m.UpstreamTopSlow = topNFromMap(t.upstream, topN)
	m.TenantTop5xx = topNTenants(t.tenant5xx, topN)
	m.CustomMetricTop = topNMetrics(t.custom, topN)
	traces := append([]RequestTrace(nil), t.traces...)
	t.mu.Unlock()
	cfg := map[string]any{"trace_mode": t.traceMode.Load(), "trace_sample_rate": float64(t.sampleRate10k.Load()) / 10000.0, "instruction_timing_enabled": t.instrEnabled.Load(), "upstream_phase_timing_enabled": t.phaseEnabled.Load(), "always_export_summary": t.alwaysExport.Load(), "info_log_enabled": t.infoLog.Load(), "info_log_fields": t.cfg.InfoLogFields, "max_events": t.cfg.MaxEvents, "max_traces": t.cfg.MaxTraces, "export_queue_size": cap(t.exportCh), "metric_queue_size": cap(t.metricCh), "metric_dropped": t.metricDropped.Load()}
	return map[string]any{"metrics": m, "recent_traces": traces, "config": cfg, "export": map[string]any{"otel": "use sink implementation", "bigquery": "use sink implementation"}}
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
		t.infoLog.Store(*infoLogEnabled)
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
