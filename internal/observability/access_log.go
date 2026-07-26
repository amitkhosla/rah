package observability

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"github.com/amitkhosla/rah/internal/ingest"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	enabled     bool    // mirrors ObsAccessLogConfig.Enabled; default true
	sampleRate  float64 // 0.0â€“1.0; 0 means "not set" â†’ treated as 1.0
}

// AccessLogEntry is the snapshot sent to the drain goroutine.
// The struct itself is pooled: drain resets and returns it after writing.
type AccessLogEntry struct {
	// Identity
	ApiName   string
	ApiID     uint32
	TenantKey string
	TenantID  uint16
	CallerKey string // API key alias; empty when no key auth was used
	CallerID  uint32 // App ID (ctx.CallerID); 0 when no key auth was used

	// Request
	Method   string
	Path     string
	ReqBytes int64 // Content-Length from client (-1 if unknown)

	// Response
	Status   int
	ResBytes int64 // bytes written to client

	// Timings (all in nanoseconds)
	Time       int64 // Unix nanoseconds at request completion (captured in Snapshot)
	TotalNs    int64 // end-to-end (handler entry â†’ last byte sent)
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
	e.CallerKey = ""
	e.CallerID = 0
	e.Method = ""
	e.Path = ""
	e.ReqBytes = 0
	e.Status = 0
	e.ResBytes = 0
	e.TotalNs = 0
	e.Time = 0
	e.GatewayNs = 0
	e.UpstreamNs = 0
	e.TTFBNs = 0
	e.Extra = e.Extra[:0]
	e.Insights = e.Insights[:0]
}

// AccessLogger writes structured access logs asynchronously.
// Snapshot() is called post-Finalize and never blocks the request goroutine.
type AccessLogger struct {
	ch              chan *AccessLogEntry
	pool            sync.Pool
	cfg             atomic.Pointer[accessLogConfig]
	dropped         atomic.Uint64
	signingKey      atomic.Pointer[[]byte]      // nil = no signing; set via SetSigningKey
	snapshotCounter atomic.Uint64               // used for counter-based sampling
	pipeline        atomic.Pointer[ingest.Pipeline] // nil until wired via SetPipeline

	// Lifecycle management
	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{}
	running bool
}

// SetPipeline wires the ingest pipeline into the access logger so that
// drain() emits KindAccessLog events in addition to the text log line.
// Safe to call at any time; takes effect on the next drained entry.
func (l *AccessLogger) SetPipeline(p *ingest.Pipeline) {
	l.pipeline.Store(p)
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
	l.cfg.Store(&accessLogConfig{enabled: true, sampleRate: 1.0})
	l.stopCh = make(chan struct{})
	l.doneCh = make(chan struct{})
	l.running = true
	go l.drainLoop(l.stopCh, l.doneCh)
	return l
}

// SetExtraFields atomically replaces the extra field list.
func (l *AccessLogger) SetExtraFields(fields []ExtraField) {
	old := l.cfg.Load()
	cp := make([]ExtraField, len(fields))
	copy(cp, fields)
	l.cfg.Store(&accessLogConfig{extraFields: cp, insights: old.insights, enabled: old.enabled, sampleRate: old.sampleRate})
}

// SetInsights atomically replaces the insight thresholds.
func (l *AccessLogger) SetInsights(ins InsightConfig) {
	old := l.cfg.Load()
	l.cfg.Store(&accessLogConfig{extraFields: old.extraFields, insights: ins, enabled: old.enabled, sampleRate: old.sampleRate})
}

// GetConfig returns current extra fields and insight config.
func (l *AccessLogger) GetConfig() ([]ExtraField, InsightConfig) {
	c := l.cfg.Load()
	cp := make([]ExtraField, len(c.extraFields))
	copy(cp, c.extraFields)
	return cp, c.insights
}

// UpdateConfig wires the ObsAccessLogConfig.Enabled and SampleRate fields into
// the access logger. Must be called after NewAccessLogger, before the server
// starts accepting requests.
//
// Backward-compat rule: if neither Enabled nor SampleRate is explicitly set by
// the caller (both are zero-values), access logging remains on (Enabled=true,
// SampleRate=1.0). To explicitly disable, pass enabled=false.
func (l *AccessLogger) UpdateConfig(enabled bool, sampleRate float64) {
	old := l.cfg.Load()
	rate := sampleRate
	if rate <= 0 {
		rate = 1.0 // treat 0 as "not configured" â†’ full sampling
	}
	if rate > 1.0 {
		rate = 1.0
	}
	l.cfg.Store(&accessLogConfig{
		extraFields: old.extraFields,
		insights:    old.insights,
		enabled:     enabled,
		sampleRate:  rate,
	})
}

// shouldSample returns true if this request should be included in the access log.
// Uses a fast atomic counter â€” no rand, no allocation, no mutex.
func (l *AccessLogger) shouldSample(rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1.0 {
		return true
	}
	n := l.snapshotCounter.Add(1)
	threshold := uint64(rate * 10000)
	return (n % 10000) < threshold
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
	callerKey string,
	callerID uint32,
	method, path string,
	status int,
	totalNs, gatewayNs, upstreamNs, ttfbNs, reqBytes, resBytes int64,
	req *http.Request,
	runtimeExtra ...KV,
) {
	cfg := l.cfg.Load()

	// Gate on Enabled flag (default true for backward compat).
	if !cfg.enabled {
		return
	}
	// Counter-based sampling â€” no rand, no allocation.
	if !l.shouldSample(cfg.sampleRate) {
		return
	}

	// Get a pooled entry and populate scalar fields.
	entry := l.pool.Get().(*AccessLogEntry)
	entry.reset()

	entry.Time = time.Now().UnixNano()
	entry.ApiName = apiName
	entry.ApiID = apiID
	entry.TenantKey = tenantKey
	entry.TenantID = tenantID
	entry.CallerKey = callerKey
	entry.CallerID = callerID
	entry.Method = method
	entry.Path = path
	entry.Status = status
	entry.TotalNs = totalNs
	entry.GatewayNs = gatewayNs
	entry.UpstreamNs = upstreamNs
	entry.TTFBNs = ttfbNs
	entry.ReqBytes = reqBytes
	entry.ResBytes = resBytes

	// Resolve extra fields from the original request â€” safe because req is still
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
		// Channel full â€” return entry to pool rather than leaking it.
		l.pool.Put(entry)
		l.dropped.Add(1)
	}
}

// DroppedCount returns how many log entries were dropped due to a full channel.
func (l *AccessLogger) DroppedCount() uint64 {
	return l.dropped.Load()
}

// Stop gracefully stops the drain goroutine, draining any remaining entries
// before exiting. The channel l.ch remains open and can be written to after Stop() returns.
// Stop is idempotent; calling it multiple times is safe.
func (l *AccessLogger) Stop() {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return
	}
	stopCh := l.stopCh
	doneCh := l.doneCh
	l.running = false
	l.mu.Unlock()
	close(stopCh)
	<-doneCh
}

// Restart starts the drain goroutine again after a Stop().
// The channel l.ch continues to be used; no new channel is created.
// Restart is idempotent; calling it multiple times (or when already running) is safe.
func (l *AccessLogger) Restart() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running {
		return
	}
	l.stopCh = make(chan struct{})
	l.doneCh = make(chan struct{})
	l.running = true
	go l.drainLoop(l.stopCh, l.doneCh)
}

// SetSigningKey installs a key for HMAC-SHA256 tamper-evidence on log lines.
// Each line gains a trailing sig=<hex64> field computed over the rest of the line.
// Pass nil or empty to disable signing. The key is copied internally.
func (l *AccessLogger) SetSigningKey(key []byte) {
	if len(key) == 0 {
		l.signingKey.Store(nil)
		return
	}
	cp := make([]byte, len(key))
	copy(cp, key)
	l.signingKey.Store(&cp)
}

func (l *AccessLogger) drainLoop(stopCh <-chan struct{}, doneCh chan struct{}) {
	defer close(doneCh)

	// 64 KB buffer: at ~200 bytes/line this batches ~320 lines per syscall.
	// bufio auto-flushes when full; the ticker handles low-traffic flushing.
	// Writing directly to os.Stderr avoids the global log.Print mutex entirely,
	// and decouples access logs from log.SetOutput redirections (e.g. ingest).
	out := bufio.NewWriterSize(os.Stderr, 1<<16)
	ticker := time.NewTicker(100 * time.Millisecond) // flush at most 100ms stale at low traffic; 1ms caused 1000 wakeups/sec idle overhead
	defer func() {
		ticker.Stop()
		_ = out.Flush()
	}()

	var sb strings.Builder
	for {
		select {
		case entry, ok := <-l.ch:
			if !ok {
				return
			}
			sb.Reset()
			sb.WriteString("[access]")
			writeKV(&sb, "time", time.Unix(0, entry.Time).UTC().Format(time.RFC3339))

			// API identity â€” prefer name over internal ID
			if entry.ApiName != "" {
				writeKV(&sb, "api", entry.ApiName)
			} else {
				writeKVUint(&sb, "api_id", uint64(entry.ApiID))
			}

			// Tenant identity â€” prefer key over internal ID
			if entry.TenantKey != "" {
				writeKV(&sb, "tenant", entry.TenantKey)
			} else if entry.TenantID != 0 {
				writeKVUint(&sb, "tenant_id", uint64(entry.TenantID))
			}

			// Caller identity â€” omit when no API key auth was used
			if entry.CallerKey != "" {
				writeKV(&sb, "caller_key", entry.CallerKey)
			}
			if entry.CallerID != 0 {
				writeKVUint(&sb, "caller_id", uint64(entry.CallerID))
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

			// HMAC-SHA256 tamper-evidence: sign the full line and append sig=<hex>.
			// Signing happens in the async drain goroutine â€” allocation here is acceptable.
			if kp := l.signingKey.Load(); kp != nil {
				mac := hmac.New(sha256.New, *kp)
				mac.Write([]byte(sb.String()))
				writeKV(&sb, "sig", hex.EncodeToString(mac.Sum(nil)))
			}

			sb.WriteByte('\n')
			_, _ = out.WriteString(sb.String()) // copies to bufio buffer â€” no syscall in the common case

			// Emit to ingest pipeline BEFORE reset so fields are still populated.
			if p := l.pipeline.Load(); p != nil {
				emitAccessLogEvent(p, entry)
			}

			// Reset and return to pool â€” slice backing arrays are preserved.
			entry.reset()
			l.pool.Put(entry)

		case <-stopCh:
			// Drain remaining entries before exiting
			ticker.Stop()
			for {
				select {
				case entry, ok := <-l.ch:
					if !ok {
						_ = out.Flush()
						return
					}
					sb.Reset()
					sb.WriteString("[access]")
					writeKV(&sb, "time", time.Unix(0, entry.Time).UTC().Format(time.RFC3339))

					// API identity â€” prefer name over internal ID
					if entry.ApiName != "" {
						writeKV(&sb, "api", entry.ApiName)
					} else {
						writeKVUint(&sb, "api_id", uint64(entry.ApiID))
					}

					// Tenant identity â€” prefer key over internal ID
					if entry.TenantKey != "" {
						writeKV(&sb, "tenant", entry.TenantKey)
					} else if entry.TenantID != 0 {
						writeKVUint(&sb, "tenant_id", uint64(entry.TenantID))
					}

					// Caller identity â€” omit when no API key auth was used
					if entry.CallerKey != "" {
						writeKV(&sb, "caller_key", entry.CallerKey)
					}
					if entry.CallerID != 0 {
						writeKVUint(&sb, "caller_id", uint64(entry.CallerID))
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

					// HMAC-SHA256 tamper-evidence: sign the full line and append sig=<hex>.
					// Signing happens in the async drain goroutine â€” allocation here is acceptable.
					if kp := l.signingKey.Load(); kp != nil {
						mac := hmac.New(sha256.New, *kp)
						mac.Write([]byte(sb.String()))
						writeKV(&sb, "sig", hex.EncodeToString(mac.Sum(nil)))
					}

					sb.WriteByte('\n')
					_, _ = out.WriteString(sb.String()) // copies to bufio buffer â€” no syscall in the common case

					// Emit to ingest pipeline BEFORE reset so fields are still populated.
					if p := l.pipeline.Load(); p != nil {
						emitAccessLogEvent(p, entry)
					}

					// Reset and return to pool â€” slice backing arrays are preserved.
					entry.reset()
					l.pool.Put(entry)
				default:
					_ = out.Flush()
					return
				}
			}

		case <-ticker.C:
			_ = out.Flush() // one syscall per ms at low traffic; noop when buffer is empty
		}
	}
}

// emitAccessLogEvent marshals entry as JSON and emits a KindAccessLog event
// into the ingest pipeline. Runs in the drain goroutine â€” allocations are fine.
// AccessLogEntry fields do not have JSON tags; json.Marshal will use field names as-is.
func emitAccessLogEvent(p *ingest.Pipeline, entry *AccessLogEntry) {
	n := p.NumSinksForKind(ingest.KindAccessLog)
	if n == 0 {
		return
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}
	e := ingest.Event{
		Kind:        ingest.KindAccessLog,
		TenantID:    entry.TenantID,
		APIID:       entry.ApiID,
		Level:       "info",
		TimestampNs: entry.TotalNs, // TotalNs is request duration; no absolute timestamp on entry
	}
	e.SetPayload(payload, n)
	p.Emit(e)
}

// ConfigHandler handles GET/POST for the log configuration.
//
//	GET  /config/log  â†’ returns current extra fields, insight config, dropped count
//	POST /config/log  â†’ replaces configuration
func (l *AccessLogger) ConfigHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		fields, ins := l.GetConfig()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"extra_fields": fields,
			"insights":     ins,
			"dropped":      l.DroppedCount(),
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

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
