package observability

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// ExtraField is a customer-configured column added to every access log line.
// Source must be "header", "query", or "path". Key is the header/param name.
type ExtraField struct {
	Name   string `json:"name"`
	Source string `json:"source"` // "header" | "query" | "path"
	Key    string `json:"key"`
}

// InsightConfig defines thresholds that add derived flag fields to log lines.
// Any threshold <= 0 disables that insight.
type InsightConfig struct {
	SlowRequestThresholdMs float64 `json:"slow_request_threshold_ms"` // adds slow=true
	UpstreamDominatedPct   float64 `json:"upstream_dominated_pct"`    // 0-100; adds upstream_dominated=true
}

// accessLogConfig is stored atomically so updates never block the hot path.
type accessLogConfig struct {
	extraFields []ExtraField
	insights    InsightConfig
}

// AccessLogEntry is the snapshot sent to the drain goroutine.
// The struct itself is pooled: drain resets and returns it after writing.
type AccessLogEntry struct {
	// Identity
	ApiName   string
	ApiID     uint32
	TenantKey string
	TenantID  uint16

	// Request
	Method   string
	Path     string
	ReqBytes int64 // Content-Length from client (-1 if unknown)

	// Response
	Status   int
	ResBytes int64 // bytes written to client

	// Timings (all in nanoseconds)
	TotalNs    int64 // end-to-end (handler entry → last byte sent)
	GatewayNs  int64 // TotalNs minus upstream time
	UpstreamNs int64 // total time spent waiting on upstream calls
	TTFBNs     int64 // time from handler entry to first byte sent to client

	// Configurable extras
	Extra []KV // resolved from request headers / query params

	// Derived insights
	Insights []KV
}

// reset zeros all fields while keeping slice backing arrays for reuse.
func (e *AccessLogEntry) reset() {
	e.ApiName = ""
	e.ApiID = 0
	e.TenantKey = ""
	e.TenantID = 0
	e.Method = ""
	e.Path = ""
	e.ReqBytes = 0
	e.Status = 0
	e.ResBytes = 0
	e.TotalNs = 0
	e.GatewayNs = 0
	e.UpstreamNs = 0
	e.TTFBNs = 0
	e.Extra = e.Extra[:0]
	e.Insights = e.Insights[:0]
}

// AccessLogger writes structured access logs asynchronously.
// Snapshot() is called post-Finalize and never blocks the request goroutine.
type AccessLogger struct {
	ch      chan *AccessLogEntry
	pool    sync.Pool
	cfg     atomic.Pointer[accessLogConfig]
	dropped atomic.Uint64
}

// NewAccessLogger creates an AccessLogger with a buffered drain channel.
func NewAccessLogger(queueSize int) *AccessLogger {
	if queueSize <= 0 {
		queueSize = 8192
	}
	l := &AccessLogger{
		ch: make(chan *AccessLogEntry, queueSize),
	}
	l.pool = sync.Pool{
		New: func() any {
			return &AccessLogEntry{
				Extra:    make([]KV, 0, 4),
				Insights: make([]KV, 0, 4),
			}
		},
	}
	l.cfg.Store(&accessLogConfig{})
	go l.drain()
	return l
}

// SetExtraFields atomically replaces the extra field list.
func (l *AccessLogger) SetExtraFields(fields []ExtraField) {
	old := l.cfg.Load()
	cp := make([]ExtraField, len(fields))
	copy(cp, fields)
	l.cfg.Store(&accessLogConfig{extraFields: cp, insights: old.insights})
}

// SetInsights atomically replaces the insight thresholds.
func (l *AccessLogger) SetInsights(ins InsightConfig) {
	old := l.cfg.Load()
	l.cfg.Store(&accessLogConfig{extraFields: old.extraFields, insights: ins})
}

// GetConfig returns current extra fields and insight config.
func (l *AccessLogger) GetConfig() ([]ExtraField, InsightConfig) {
	c := l.cfg.Load()
	cp := make([]ExtraField, len(c.extraFields))
	copy(cp, c.extraFields)
	return cp, c.insights
}

// Snapshot captures request data and enqueues a log entry non-blocking.
// Must be called AFTER ctx.Finalize() and BEFORE ctx is returned to pool.
//
// req remains valid here: in Go's net/http, *http.Request lives until the
// ServeHTTP goroutine returns. Snapshot() is called before that, so
// req.Header.Get() is safe. Header values reflect the original client request
// (mutations are tracked separately in ctx.MutationLog for upstream calls).
func (l *AccessLogger) Snapshot(
	apiName string,
	apiID uint32,
	tenantKey string,
	tenantID uint16,
	method, path string,
	status int,
	totalNs, gatewayNs, upstreamNs, ttfbNs, reqBytes, resBytes int64,
	req *http.Request,
	runtimeExtra ...KV,
) {
	cfg := l.cfg.Load()

	// Get a pooled entry and populate scalar fields.
	entry := l.pool.Get().(*AccessLogEntry)
	entry.reset()

	entry.ApiName = apiName
	entry.ApiID = apiID
	entry.TenantKey = tenantKey
	entry.TenantID = tenantID
	entry.Method = method
	entry.Path = path
	entry.Status = status
	entry.TotalNs = totalNs
	entry.GatewayNs = gatewayNs
	entry.UpstreamNs = upstreamNs
	entry.TTFBNs = ttfbNs
	entry.ReqBytes = reqBytes
	entry.ResBytes = resBytes

	// Resolve extra fields from the original request — safe because req is still
	// valid at this point (handler goroutine has not returned yet).
	if req != nil && len(cfg.extraFields) > 0 {
		var queryVals url.Values // parsed lazily, at most once per request
		for _, f := range cfg.extraFields {
			var val string
			switch f.Source {
			case "header":
				val = req.Header.Get(f.Key)
			case "query":
				if queryVals == nil {
					queryVals, _ = url.ParseQuery(req.URL.RawQuery)
				}
				if vs := queryVals[f.Key]; len(vs) > 0 {
					val = vs[0]
				}
			}
			if val != "" {
				entry.Extra = append(entry.Extra, KV{K: f.Name, V: val})
			}
		}
	}

	// Append runtime extra fields emitted by log_field steps during flow execution.
	entry.Extra = append(entry.Extra, runtimeExtra...)

	// Derive insights from thresholds.
	ins := cfg.insights
	if ins.SlowRequestThresholdMs > 0 && float64(totalNs)/1e6 > ins.SlowRequestThresholdMs {
		entry.Insights = append(entry.Insights, KV{K: "slow", V: "true"})
	}
	if status >= 500 {
		entry.Insights = append(entry.Insights, KV{K: "error", V: "true"})
	}
	if ins.UpstreamDominatedPct > 0 && totalNs > 0 {
		pct := float64(upstreamNs) / float64(totalNs) * 100
		if pct > ins.UpstreamDominatedPct {
			entry.Insights = append(entry.Insights, KV{K: "upstream_dominated", V: "true"})
		}
	}

	select {
	case l.ch <- entry:
	default:
		// Channel full — return entry to pool rather than leaking it.
		l.pool.Put(entry)
		l.dropped.Add(1)
	}
}

// DroppedCount returns how many log entries were dropped due to a full channel.
func (l *AccessLogger) DroppedCount() uint64 {
	return l.dropped.Load()
}

func (l *AccessLogger) drain() {
	var sb strings.Builder
	for entry := range l.ch {
		sb.Reset()
		sb.WriteString("[access]")

		// API identity — prefer name over internal ID
		if entry.ApiName != "" {
			writeKV(&sb, "api", entry.ApiName)
		} else {
			writeKVUint(&sb, "api_id", uint64(entry.ApiID))
		}

		// Tenant identity — prefer key over internal ID
		if entry.TenantKey != "" {
			writeKV(&sb, "tenant", entry.TenantKey)
		} else if entry.TenantID != 0 {
			writeKVUint(&sb, "tenant_id", uint64(entry.TenantID))
		}

		writeKV(&sb, "method", entry.Method)
		writeKV(&sb, "path", entry.Path)
		writeKVInt(&sb, "status", int64(entry.Status))

		// Timing breakdown
		writeKVFloat(&sb, "total_ms", float64(entry.TotalNs)/1e6)
		writeKVFloat(&sb, "gateway_ms", float64(entry.GatewayNs)/1e6)
		writeKVFloat(&sb, "upstream_ms", float64(entry.UpstreamNs)/1e6)
		writeKVFloat(&sb, "ttfb_ms", float64(entry.TTFBNs)/1e6)

		// Bytes
		writeKVInt(&sb, "req_bytes", entry.ReqBytes)
		writeKVInt(&sb, "res_bytes", entry.ResBytes)

		// Customer-configured extra fields
		for _, kv := range entry.Extra {
			writeKV(&sb, kv.K, kv.V)
		}

		// Insights (derived flags)
		for _, kv := range entry.Insights {
			writeKV(&sb, kv.K, kv.V)
		}

		log.Print(sb.String())

		// Reset and return to pool — slice backing arrays are preserved.
		entry.reset()
		l.pool.Put(entry)
	}
}

// ConfigHandler handles GET/POST for the log configuration.
//
//	GET  /config/log  → returns current extra fields, insight config, dropped count
//	POST /config/log  → replaces configuration
func (l *AccessLogger) ConfigHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		fields, ins := l.GetConfig()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"extra_fields": fields,
			"insights":     ins,
			"dropped":      l.DroppedCount(),
		})

	case http.MethodPost:
		var payload struct {
			ExtraFields []ExtraField   `json:"extra_fields"`
			Insights    *InsightConfig `json:"insights"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if payload.ExtraFields != nil {
			l.SetExtraFields(payload.ExtraFields)
		}
		if payload.Insights != nil {
			l.SetInsights(*payload.Insights)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- allocation-free string building helpers ----

func writeKV(sb *strings.Builder, k, v string) {
	sb.WriteByte(' ')
	sb.WriteString(k)
	sb.WriteByte('=')
	sb.WriteString(v)
}

func writeKVInt(sb *strings.Builder, k string, v int64) {
	sb.WriteByte(' ')
	sb.WriteString(k)
	sb.WriteByte('=')
	appendInt(sb, v)
}

func writeKVUint(sb *strings.Builder, k string, v uint64) {
	sb.WriteByte(' ')
	sb.WriteString(k)
	sb.WriteByte('=')
	appendUint(sb, v)
}

func writeKVFloat(sb *strings.Builder, k string, v float64) {
	sb.WriteByte(' ')
	sb.WriteString(k)
	sb.WriteByte('=')
	// 3 decimal places without fmt.Sprintf
	if v < 0 {
		sb.WriteByte('-')
		v = -v
	}
	intPart := int64(v)
	frac := int64((v-float64(intPart))*1000 + 0.5)
	appendInt(sb, intPart)
	sb.WriteByte('.')
	switch {
	case frac < 10:
		sb.WriteString("00")
	case frac < 100:
		sb.WriteByte('0')
	}
	appendUint(sb, uint64(frac))
}

func appendInt(sb *strings.Builder, v int64) {
	if v < 0 {
		sb.WriteByte('-')
		v = -v
	}
	appendUint(sb, uint64(v))
}

func appendUint(sb *strings.Builder, v uint64) {
	if v == 0 {
		sb.WriteByte('0')
		return
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	sb.Write(buf[pos:])
}
