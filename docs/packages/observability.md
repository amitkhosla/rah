# Observability Package

## Purpose
Provides **telemetry, metrics, request tracing, access logging, and detail logging** for RAH. All subsystems are designed to never block request execution: writes are async, counters are atomic, and every drain path has backpressure-safe drops.

## Files
| File | Role |
|------|------|
| `telemetry.go` | Core `Telemetry` struct, per-API stats (`RecordRequest`/`APITop`), snapshot, config |
| `access_log.go` | `AccessLogger` — pool-based async structured log to stdout |
| `detail_log.go` | `WriteDetailLog` — full JSONL trace to a file (optional, togglable at runtime) |
| `obs_store.go` | `ObsStore` interface + `AccessLogRecord`, `TraceRecord`, `MetricSnapshot`, filters |
| `obs_store_memory.go` | `MemObsStore` — ring-buffer in-memory store (default) |
| `obs_store_postgres.go` | `PostgresObsStore` — persistent store |
| `obs_store_redis.go` | `RedisObsStore` — Redis/Dragonfly store |
| `obs_factory.go` | `NewObsStoreFromParams` — creates correct store from config |
| `obs_writer.go` | `ObsWriter` — batches access log writes; passes traces/snapshots through immediately |
| `obs_handler.go` | `ObsHandler` + `RegisterObsRoutes` — REST API on the management port |
| `obs_prometheus.go` | Prometheus metrics export (optional) |
| `telemetry_test.go` | Unit tests |

---

## Subsystem Map

```
HTTP Request
    │
    ├─► AccessLogger.Snapshot()       ── async drain ──► stdout ([access] log line)
    │
    ├─► Telemetry.FinishRequest()     ── atomic counters ──► GatewayMetrics
    │       └─► RecordRequest()       ── lock-free per-API stats (apiStats slice)
    │
    ├─► ObsWriter.WriteAccessLog()    ── batch buffer ──► ObsStore (memory/pg/redis)
    │       └─► WriteTrace()          ── immediate ──────► ObsStore
    │
    └─► WriteDetailLog()              ── locked file write ──► JSONL file (optional)

Management port (8081) REST API
    ├─► GET  /observability/metrics         → Telemetry.Snapshot(20)
    ├─► GET  /observability/access-log      → ObsStore.QueryAccessLog()
    ├─► GET  /observability/traces          → ObsStore.QueryTraces()
    ├─► GET/PUT /observability/detail-log   → detail log config
    ├─► GET  /observability/apis            → persisted agg OR Telemetry.APITop()
    ├─► GET  /observability/apis/{name}     → per-API traces + access log
    └─► GET  /observability/tenants/{alias} → per-tenant access log
```

---

## Key Types

### RequestTiming (rctx/context.go — 64 bytes, one cache line)
Populated during request execution; read after `Finalize()` to produce log/trace data.
```go
type RequestTiming struct {
    StartNs         int64  // request handler entry
    FirstByteSentNs int64  // TTFB (first Write() call)
    LastByteSentNs  int64  // transfer complete (last Write() or Finalize())
    UpstreamTimeNs  int64  // total time in upstream calls
    UpstreamCalls   int32  // number of upstream calls
    _               int32  // alignment pad
    ClientBytesSent int64  // total bytes written to client
    UpstreamBytesTx int64  // bytes sent to upstream
    UpstreamBytesRx int64  // bytes received from upstream
}
```
`LastByteSentNs` is updated on every `Write()` call in `rctx.Context` and finalized in `Finalize()`. Used to compute `transferMs = (LastByteSentNs - FirstByteSentNs) / 1e6`.

### AccessLogRecord (obs_store.go)
The record persisted to ObsStore and served by the REST API.
```go
type AccessLogRecord struct {
    TimestampNs int64             // unix nanoseconds
    ApiName     string
    TenantID    uint16
    TenantKey   string
    Method      string
    Path        string
    Status      int
    TotalMs     float64           // true client-visible latency (routing+flow+finalize)
    GatewayMs   float64           // TotalMs minus upstream time
    UpstreamMs  float64
    TTFBMs      float64
    ConnSetupMs float64           // TCP+TLS setup (new connections only; omitted for keep-alive)
    TransferMs  float64           // first→last byte sent to client
    ReqBytes    int64
    ResBytes    int64
    Extra       map[string]string // customer-configured extra fields
}
```

### InstructionEvent (telemetry.go)
Per-instruction timing entry appended to a `RequestTrace`.
```go
type InstructionEvent struct {
    Seq        uint32  // monotonic within the trace; 0 for gateway phases
    Name       string  // instruction name OR gateway phase name (e.g. GATEWAY_PHASE_ROUTING)
    PC         int16   // instruction PC (≥0) or -1 for gateway phases
    DurationNs int64
    Input      []KV    // captured input values (optional)
    Output     []KV    // captured output values / notes (optional)
}
```
`PC == -1` is the convention for gateway infrastructure phases appended by `main.go` (not by the instruction executor). The UI uses this to separate flow steps from pre/post phases.

### Gateway Phase Names
Appended to `ctx.Trace.Instructions` by `main.go` after request execution:
```
GATEWAY_PHASE_CONN_SETUP           — TCP+TLS accept-to-handler time (new connections)
GATEWAY_PHASE_ROUTING              — routing lookup + context setup
GATEWAY_PHASE_PROCESS_REQUEST      — full flow execution (total)
GATEWAY_PHASE_RESIDUAL             — flow executor overhead (processDuration − Σ instruction times); PC=-1, shown inside flow window
GATEWAY_PHASE_FINALIZE_RESPONSE    — flush response headers/body to client
GATEWAY_PHASE_RESPONSE_TRANSFER    — first→last byte sent (from RequestTiming)
GATEWAY_PHASE_AFTER_RESPONSE_HOOKS — post-response hooks
GATEWAY_PHASE_ACCESS_LOG_SNAPSHOT  — access log ring-buffer update
GATEWAY_PHASE_TELEMETRY_FINISH     — metrics atomic updates
GATEWAY_PHASE_ACCESS_LOG_ENQUEUE   — ObsWriter async enqueue
```

### UpstreamEvent (telemetry.go)
Per-upstream-call detail, appended to `RequestTrace.Upstreams`.
```go
type UpstreamEvent struct {
    Seq               uint32
    Attempt           int
    Host, URL, Model  string
    Status            int
    Err               string
    ConnReused        bool
    ConnIdle          bool
    DNSDurationNs     int64
    ConnectDurationNs int64
    TLSDurationNs     int64
    TTFBNs            int64
    TotalNs           int64
    BytesSent         int64
    BytesReceived     int64
}
```

---

## Telemetry Struct

`Telemetry` is the central metrics hub. Created once at startup via `NewFromEnv()`.

### Config (env vars)
| Env Var | Default | Description |
|---------|---------|-------------|
| `RAH_OBS_ENABLED` | `true` | Enable/disable all observability |
| `RAH_TRACE_MODE` | `false` | Enable sampled/error tracing |
| `RAH_TRACE_SAMPLE_RATE` | `0.0` | Trace sample rate (0.0–1.0) |
| `RAH_TRACE_MAX_EVENTS` | `128` | Max instruction events per trace |
| `RAH_OBS_INSTRUCTION_TIMING` | `true` | Enable per-instruction timing |
| `RAH_OBS_UPSTREAM_PHASE_TIMING` | `false` | Enable upstream DNS/connect/TLS phase timing |
| `RAH_OBS_EXPORT_SUMMARY` | `true` | Always export request summary |
| `RAH_OBS_INFO_LOG` | `true` | Emit `[obs.info]` log line per request |
| `RAH_OBS_INFO_LOG_FIELDS` | `api_id,tenant_id,status,duration_ns,upstream_duration_ns` | Fields for info log |

Runtime config can be updated via `PATCH /debug/obs` (the `Telemetry.DebugHandler`).

### Per-API Stats: RecordRequest / APITop
Zero-alloc, lock-free per-API request counting. Called post-response in `main.go`:
```go
obs.RecordRequest(ctx.ApiId, clientTotal.Nanoseconds(), req.ContentLength, ctx.Timing.ClientBytesSent)
```
Internally: `apiStats` is an `atomic.Pointer[[]apiStat]` slice indexed directly by `ApiID`. Hot path = 1 atomic pointer load + 4 atomic adds. Grows under `apiStatsMu` only when a new higher `ApiID` is first seen (rare, at deploy time). Pre-allocated to 64 slots at startup.

`APITop(topN, nameResolver)` builds a `[]NameLatency` sorted by count. `nameResolver` is `registry.GetNameByID` passed from `main.go`; entries where the resolver returns `""` are skipped (unknown/inactive APIs).

### Snapshot()
`Snapshot(topN)` returns:
```json
{
  "metrics": { /* GatewayMetrics — atomic reads */ },
  "recent_traces": [ /* last MaxTraces RequestTrace entries */ ],
  "config": { /* current runtime config */ },
  "export": { /* otel/bigquery sink notes */ }
}
```
Called by `MetricsHandler` (REST) and `DebugHandler` (debug port).

---

## AccessLogger

Pool-based async structured log to stdout. Never blocks the request goroutine.

**`Snapshot()`** — called post-`Finalize()`, pre-pool-return in `main.go`:
- Gets a pooled `AccessLogEntry`, fills all scalar fields
- Resolves customer-configured `ExtraField`s from request headers/query params (parsed lazily)
- Applies `InsightConfig` thresholds: `slow=true`, `error=true`, `upstream_dominated=true`
- Sends to `chan *AccessLogEntry` (buffered 8192); drops and returns to pool if full

**`drain()` goroutine** formats the entry as a `[access] key=value ...` log line using allocation-free `strings.Builder` helpers (`writeKVFloat`, `writeKVInt`, etc.), then resets and returns the entry to the pool.

**Config endpoints** (`/config/log` on the management mux):
- `GET` — returns `extra_fields`, `insights`, `dropped` count
- `POST` — replaces `extra_fields` and/or `insights` atomically

---

## ObsStore / ObsWriter

### ObsStore Interface
```go
type ObsStore interface {
    WriteAccessLog(ctx, []AccessLogRecord) error
    WriteMetricSnapshot(ctx, MetricSnapshot) error
    WriteTrace(ctx, TraceRecord) error
    QueryAccessLog(ctx, AccessLogFilter) ([]AccessLogRecord, error)
    QueryMetrics(ctx, MetricsFilter) ([]MetricSnapshot, error)
    QueryTraces(ctx, TraceFilter) ([]TraceRecord, error)
    Close() error
}
```
`NoopObsStore` (default if not configured) discards all writes and returns nil for reads.

### Implementations
| Type | Params | Notes |
|------|--------|-------|
| `memory` | `MaxAccessLog` (default 10000), `MaxTraces` (default 500) | Ring buffer; lost on restart |
| `postgres` | `DSN` (full connection string) | Persistent |
| `redis` / `dragonfly` | `DSN` (host:port), `Password`, `PoolSize` (default 10) | Persistent |

Created via `NewObsStoreFromParams(ctx, ObsStoreParams)` in `obs_factory.go`. Always falls back to `MemObsStore` on connection error.

### ObsWriter
Batches access log writes; passes traces and metric snapshots through immediately.
- Batch size: 200 records (or flush every 2 seconds, whichever comes first)
- If batch is full during `WriteAccessLog()`, caller flushes synchronously (back-pressure)
- Background goroutine started by `Start(ctx)`, stopped by `Close()` (both trigger a final flush)
- Errors are logged (`[obs.writer]`) but not propagated to callers

### Query Filters
```go
AccessLogFilter{ApiName, TenantKey, Status, FromUnixS, ToUnixS, Limit /* default 100, max 1000 */}
TraceFilter{ApiName, TenantID, MinMs, FromUnixS, ToUnixS, Limit}
MetricsFilter{Window, Dimension, FromUnixS, ToUnixS}
```
`parseFrom(s)` in `obs_handler.go` accepts either a duration string (`"1h"`, `"30m"`) or RFC3339 timestamp.

---

## Detail Log

Optional JSONL file for full payload troubleshooting (e.g., complete LLM prompts/responses).

**Enable at startup:**
```bash
export RAH_OBS_DETAIL_LOG_FILE=/app/logs/detailLogging.log
```

**Toggle at runtime** via `PUT /observability/detail-log`:
```json
{ "enabled": true, "path": "/app/logs/detailLogging.log" }
```

**Extract from Docker container:**
```bash
docker cp <container>:/app/logs/detailLogging.log ./detailLogging.log
```

**Write a record** (called from engine steps or main.go):
```go
observability.WriteDetailLog(map[string]any{
    "event": "llm_response",
    "api":   "smart-chat",
    "body":  responseBody,
})
```
Auto-adds `ts` (RFC3339Nano) if not present. File is created on first write; append mode.

---

## REST API (management port 8081)

Registered by `RegisterObsRoutes(mux, writer, obs, nameResolver)`.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/observability/metrics` | `Telemetry.Snapshot(20)` — gateway metrics, recent traces, config |
| `GET` | `/observability/access-log` | Persisted access log. Params: `api`, `tenant`, `status`, `from`, `limit` |
| `GET` | `/observability/traces` | Persisted traces. Params: `api`, `tenant_id`, `min_ms`, `from`, `limit` |
| `GET` | `/observability/detail-log` | Detail log config (enabled, path) |
| `PUT` | `/observability/detail-log` | Update detail log config: `{"enabled":true,"path":"..."}` |
| `GET` | `/observability/apis` | Per-API stats. Primary: persisted access log (last 1h). Fallback: `Telemetry.APITop()` |
| `GET` | `/observability/apis/{name}` | Recent traces + access log for a specific API |
| `GET` | `/observability/tenants/{alias}` | Recent access log for a specific tenant |

### `/observability/apis` data source logic
1. If `ObsStore` has persisted data for the last hour → aggregate by `ApiName`, sort by request count, return with `"source": "access_log"`
2. If no persisted data (store is noop or ring is empty) → `Telemetry.APITop(20, nameResolver)` using in-memory atomic stats, returns `"source": "telemetry_api_stats"`

This avoids the old bug where the fallback used `UpstreamTopSlow` (which contains LLM model names, not user-facing API names).

### `/observability/metrics` filtering
Supports `?api=<name>` and `?tenant=<id>` query params to filter `InstructionTopSlow` and `UpstreamTopSlow` lists before returning the snapshot.

---

## Data Flow in main.go

```go
// After routing:
routingDuration := time.Since(reqStart)
ctx.Trace.Instructions = append(ctx.Trace.Instructions, InstructionEvent{
    Name: "GATEWAY_PHASE_ROUTING", PC: -1, DurationNs: routingDuration.Nanoseconds(),
})

// Flow execution:
processDuration := ...  // time.Since after fm.Process()

// RESIDUAL = executor overhead not attributable to individual instructions
var flowAttributedNs int64
for _, e := range ctx.Trace.Instructions {
    if e.DurationNs > 0 && e.PC >= 0 {  // only real flow instructions
        flowAttributedNs += e.DurationNs
    }
}
if residualNs := processDuration.Nanoseconds() - flowAttributedNs; residualNs > 0 {
    ctx.Trace.Instructions = append(ctx.Trace.Instructions, InstructionEvent{
        Name: "GATEWAY_PHASE_RESIDUAL", PC: -1, DurationNs: residualNs,
    })
}

// After ctx.Finalize():
clientTotal := time.Since(reqStart)  // TRUE client-visible latency (routing+flow+finalize)
transferMs = (LastByteSentNs - FirstByteSentNs) / 1e6

// Connection setup (TCP+TLS; filtered to new connections only):
// connSetupMs suppressed when reqStart - acceptTime > 3s (keep-alive reuse)

// Post-response:
obs.FinishRequest(&ctx.Trace, status, clientTotal, gatewayDuration, upstreamDuration, ...)
obs.RecordRequest(ctx.ApiId, clientTotal.Nanoseconds(), req.ContentLength, ctx.Timing.ClientBytesSent)
obsLogger.Snapshot(apiName, apiID, tenantKey, tenantID, method, path, status, ...)
obsWriter.WriteAccessLog(AccessLogRecord{...ConnSetupMs: connSetupMs, TransferMs: transferMs})
```

---

## Performance Notes
- **Hot path**: All counters use `atomic.Add`; no locks on request goroutine
- **Per-API stats**: Single pointer load + 4 atomic adds; zero GC; grows only at deploy time
- **AccessLogger**: Pool-based, async drain; drops are counted, never block
- **ObsWriter**: Batches 200 records; back-pressure via synchronous flush when full
- **Detail log**: Mutex-protected append; off by default; intended for debug sessions only
- **`sync.Pool` cold start**: First request after idle allocates a new `rctx.Context` (several KB of inline arrays), causing 10–15ms routing on first hit. Normal. Subsequent requests use pooled context → nanoseconds.

## Common Navigation
- Add a new gateway phase → `main.go` (append `InstructionEvent{PC: -1}`) + `GATEWAY_PHASE_LABELS` in `Observability.tsx`
- Change access log fields → `AccessLogRecord` in `obs_store.go` + serialization in `main.go`
- Change access log stdout format → `AccessLogger.drain()` in `access_log.go`
- Add extra fields to access log → `AccessLogger.SetExtraFields()` (POST `/config/log`)
- Change ObsStore backend → `ObsStoreParams.Type` in startup config
- Query access log from UI → `ObsHandler.AccessLogHandler` → `ObsStore.QueryAccessLog`
