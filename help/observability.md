# RAH Monitoring and Observability

RAH collects comprehensive telemetry at every layer: request-level metrics, per-instruction timing, cache performance, and AI/LLM analytics. This guide shows how to monitor your gateway and build operational dashboards.

---

## What RAH Tracks

Every request flowing through RAH generates detailed observability data:

**Request-level metrics**:
- `api_name` — which API was called
- `tenant_id` — which tenant made the request
- `caller_id` — authenticated caller (JWT subject, API key, etc.)
- `method` — HTTP method (GET, POST, etc.)
- `path` — request path
- `status_code` — HTTP response status
- `total_ms` — wall-clock time from request arrival to response sent
- `gateway_ms` — time spent in RAH (excluding upstream calls)
- `upstream_ms` — time spent waiting for upstream services
- `ttfb_ms` — time-to-first-byte (for streaming responses)
- `req_bytes`, `res_bytes` — request and response payload sizes
- `cache_hit`, `cache_miss` — cache performance (if applicable)

**Per-instruction timing**:
- Each step in your flow is timed to nanosecond precision
- Identifies which step is the bottleneck (usually upstream calls)
- Useful for performance tuning and SLA enforcement

**LLM metrics** (if using AI steps):
- Model used, prompt tokens, completion tokens, total cost
- Inference latency (time model took to generate response)
- Fallback events (which model was actually used after retries)
- Input/output details (captured if observability detail logging enabled)

**Cache metrics**:
- Hits vs misses per cache key
- Eviction rate
- Memory pressure
- Key-value size distribution

**Rate limiting decisions**:
- Allowed vs rejected (429 Too Many Requests)
- Window type and remaining quota
- Which tenant/caller hit the limit

---

## Access Log — The Heartbeat

The access log is the single most important observability artifact. Every request writes one entry, showing what happened and how long it took.

### Access Log Structure

```json
{
  "timestamp": "2026-06-02T14:32:45.123Z",
  "request_id": "req_XYZ123...",
  "api_name": "user-service",
  "tenant_id": "acme-corp",
  "caller_id": "user@acme.com",
  "method": "GET",
  "path": "/api/v1/users/123",
  "status_code": 200,
  "total_ms": 42.5,
  "gateway_ms": 3.2,
  "upstream_ms": 39.1,
  "ttfb_ms": 12.4,
  "req_bytes": 256,
  "res_bytes": 8192,
  "cache_hit": true,
  "rate_limit_ok": true,
  "rate_limit_remaining": 9950,
  "error": null,
  "_order_id": "ORD-12345",      # custom log field (added by `log_field()` step)
  "_tier": "premium",             # custom log field
  "_signature": "..."             # HMAC signature (if signing enabled)
}
```

### View Access Logs in Studio

RAH Studio provides a built-in access log viewer with filtering and search:

1. Open Studio: `http://localhost:8080`
2. Navigate to **Observability** → **Access Log**
3. Filter by:
   - API name
   - Tenant
   - Status code (e.g., 500 for errors)
   - Time range
   - Custom fields

### Access Logs via REST API

```bash
# Get recent access logs
curl http://localhost:8081/observability/access-log

# Filter by API and status code
curl "http://localhost:8081/observability/access-log?api=user-service&status=500"

# Get logs for a specific tenant
curl "http://localhost:8081/observability/access-log?tenant=acme-corp"

# Pagination
curl "http://localhost:8081/observability/access-log?limit=50&offset=0"
```

Response format: JSON array of log entries.

### Persist Access Logs to PostgreSQL

For long-term storage and querying, persist logs to a database:

```yaml
datastore:
  bindings:
    obs_access_log: postgres      # use your PostgreSQL instance

observability:
  access_log:
    storage: obs_access_log
    flush_interval_sec: 1
```

Once configured, access logs are automatically written to PostgreSQL. Query them using SQL:

```sql
SELECT api_name, COUNT(*) as requests, AVG(total_ms) as avg_latency
FROM access_logs
WHERE timestamp > now() - interval '1 hour'
GROUP BY api_name
ORDER BY requests DESC;
```

### Add Custom Fields to Access Logs

Use the `log_field()` step to add business context to every log entry:

```yaml
steps:
  - order_id = header("X-Order-ID")
  - log_field("order_id", order_id)
  
  - tier = registry.meta("tier")
  - log_field("tier", tier)
  
  # Now every access log entry for this request includes these fields
```

In the access log, custom fields appear with a `_` prefix: `_order_id`, `_tier`.

---

## Request Traces — Instruction-Level Timing

A trace shows exactly which step took how long, helping you identify bottlenecks:

```
GET /api/users/123  [42ms total]
├── validate_token          1.2ms
├── registry_lookup         0.08ms
├── rate_limit_v2           0.12ms
├── cache_get               0.4ms  ← cache HIT (could have been miss)
├── http_call               38.1ms ← BOTTLENECK (upstream)
│   ├── dns_lookup          0.3ms
│   ├── tls_handshake       1.2ms
│   ├── request_sent        0.1ms
│   └── waiting_for_response 36.5ms ← the upstream server is slow
└── return                  0.02ms
```

### Enable Traces

```yaml
observability:
  traces:
    enabled: true
    sample_rate: 0.05           # sample 5% of requests
    always_trace_5xx: true      # always trace errors
    instruction_timing: true    # nanosecond-level per-step timing
```

### View Traces in Studio

1. **Observability** → **Traces**
2. Click any trace to expand and see step breakdown
3. Filter by API, tenant, status code, latency threshold

### Capture Variables in Traces

Include variable values in the trace for debugging:

```yaml
steps:
  - result = validate_token(
      header.Authorization,
      trace_vars: ["user_id", "tenant", "permissions"]
    )
```

In the trace, these variables are captured and visible in the UI.

### Via REST API

```bash
# Get recent traces
curl http://localhost:8081/observability/traces

# Filter by API and status
curl "http://localhost:8081/observability/traces?api=user-service&status=500"

# Get traces for a specific request
curl "http://localhost:8081/observability/traces?request_id=req_XYZ123"
```

---

## Metrics — Aggregated Performance Data

Metrics are computed from access logs, aggregated over rolling windows (default: 1 minute, 5 minutes, 1 hour).

### Standard Metrics

```yaml
request_rate:                  # requests per second
  1m: 1234.5 req/s
  5m: 1200.3 req/s
  1h: 1150.2 req/s

error_rate:                    # 4xx, 5xx as percentage
  1m: 2.3%
  5m: 2.1%
  1h: 1.9%

latency_percentiles:           # wall-clock time
  p50: 5.2ms
  p95: 42.1ms
  p99: 127.3ms
  p99.9: 342.1ms

cache_hit_rate:
  1m: 85.2%
  5m: 84.9%
  1h: 83.7%

upstream_latency:              # time spent in upstream services
  p50: 3.8ms
  p95: 38.2ms
  p99: 120.1ms
```

### Enable Metrics

```yaml
observability:
  metrics:
    enabled: true
    windows: ["1m", "5m", "1h"]
    breakdown_by:              # optional: aggregate by dimensions
      - api_name
      - tenant_id
      - status_code
```

### View Metrics in Studio

**Observability** → **Metrics**:
- Dashboard showing request rate, error rate, latency percentiles
- Breakdown by API, tenant, status code
- Time series graph (last 1 hour)

### Per-API Analytics

**Observability** → **APIs** → select an API:
- Request volume over time
- Error rate trend
- Latency percentile trend
- Top tenants calling this API
- Slow traces for this API

### Per-Tenant Analytics

**Tenants** → select tenant → **Observability** tab:
- Requests made by this tenant
- Error rate
- Rate limit hits
- AI token usage (if applicable)

---

## Structured Logging in Flows

Log events from within your flow logic. Logs are written to the gateway's structured log output.

### Log Statements

```yaml
steps:
  - log("Starting payment processing")
  
  - error_happened = some_condition
  - log("error", "Payment failed: {error_message}")
  
  - log("info", "Processed {item_count} items")
```

**Log levels**: `debug`, `info`, `warn`, `error`

Logs appear in the gateway output with the request context automatically attached:

```
2026-06-02 14:32:45 [INFO] req_XYZ123: Starting payment processing [tenant=acme-corp, api=payment-service]
2026-06-02 14:32:45 [ERROR] req_XYZ123: Payment failed: insufficient_funds [tenant=acme-corp, api=payment-service]
```

### Configurable Log Level

```yaml
observability:
  log_level: info    # debug | info | warn | error
```

**Per-tenant debug mode** (temporary):
```bash
curl -X POST http://localhost:8081/tenants/acme-corp/modifier \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"debug_enabled": true, "log_level": "debug"}'
```

After 1 hour, debug mode automatically resets.

---

## AI / LLM Observability

Every LLM API call is automatically instrumented.

### Tracked Metrics

- **Model used**: which model actually handled the request (useful for fallback tracking)
- **Input tokens**: tokens consumed by the prompt
- **Output tokens**: tokens generated by the model
- **Total cost**: input + output cost at model's current rates
- **Latency**: time from request to completion
- **Errors**: timeouts, rate limits, API errors

### View LLM Metrics

**Observability** → **AI/LLM**:
- Total tokens used (daily, monthly)
- Cost breakdown by model
- Error rate
- Average latency
- Top prompts (by token usage)

### Detailed Prompt/Response Capture

By default, prompts and responses are not logged (to avoid PII in logs). Enable detailed capture:

```yaml
observability:
  ai_logging:
    capture_prompts: true      # logs input prompts
    capture_responses: true    # logs model responses
    scrub_pii: true           # masks credit cards, emails, etc.
    output_file: /var/log/rah/prompts.jsonl
```

Example detailed log entry:
```json
{
  "timestamp": "2026-06-02T14:32:45.123Z",
  "request_id": "req_XYZ123",
  "model": "gpt-4",
  "input_tokens": 512,
  "output_tokens": 128,
  "cost_usd": 0.042,
  "latency_ms": 850,
  "prompt": "User question: [SCRUBBED_PII] Should I upgrade my plan?",
  "response": "Based on your usage patterns, upgrading to the Professional plan would save you 15% monthly..."
}
```

### Cost Reports

```bash
# Daily cost report for a tenant
curl http://localhost:8081/api/v1/costs/daily?tenant=acme-corp

# Monthly cost breakdown by model
curl http://localhost:8081/api/v1/costs/monthly?tenant=acme-corp&breakdown=model

# Real-time token usage
curl http://localhost:8081/api/v1/usage/tokens?tenant=acme-corp
```

---

## The Ingest Pipeline — Event Backbone

RAH uses a high-performance event pipeline to move observability data from request processing to sinks (files, databases, streaming systems). The pipeline is always on.

### Pipeline Architecture

```
Request processing
        ↓
    [Per-kind lock-free MPMC ring buffers]
        ↓
   [Fanout goroutines]
        ↓
[Worker pools per sink] → [File] (rolling log)
                        → [Redis Stream] (cross-instance)
                        → [PostgreSQL] (long-term storage)
                        → [S3] (archive)
```

### Configure Sinks

```yaml
ingest:
  enabled: true
  sinks:
    - kind: file
      path: /var/log/rah/events.jsonl
      max_size_mb: 500
      rotation_count: 10
    
    - kind: redis_stream
      address: redis.internal:6379
      stream: rah:events
      batch_size: 100
    
    - kind: postgres
      connection_string: "postgresql://observability:pwd@db:5432/rah_events"
      table: events
      batch_size: 500
```

**Event types flowing through the pipeline**:
- `access_log` — every request
- `trace` — sampled traces (instruction-level timing)
- `error` — errors and exceptions
- `rate_limit` — rate limit decisions
- `cache` — cache hits/misses
- `llm_call` — LLM API calls
- `ai_error` — AI/LLM errors

### Monitoring Pipeline Health

```bash
# Check pipeline throughput
curl http://localhost:8081/observability/ingest-stats

# Example response:
# {
#   "buffered_events": 1234,
#   "flushed_events_total": 5123456,
#   "sink_status": {
#     "file": {"status": "ok", "last_write_ms": 12},
#     "redis_stream": {"status": "ok", "queue_depth": 45}
#   }
# }
```

If pipeline is falling behind (large `buffered_events`), consider:
- Adding more worker threads: `workers_per_sink: 4`
- Increasing batch size: `batch_size: 1000`
- Disabling less-important event types

---

## Health Check Endpoint

RAH exposes a Kubernetes-compatible health check:

```bash
curl http://localhost:8081/health

# Response:
# HTTP 200
# {"status": "ok", "timestamp": "2026-06-02T14:32:45Z"}
```

Use in Kubernetes readiness / liveness probes:

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8081
  initialDelaySeconds: 10
  periodSeconds: 5

readinessProbe:
  httpGet:
    path: /health
    port: 8081
  initialDelaySeconds: 5
  periodSeconds: 5
```

---

## Prometheus Metrics Export

Export metrics in Prometheus format for scraping:

```yaml
observability:
  export:
    prometheus:
      enabled: true
      port: 9090              # optional; defaults to 8081
```

Then add RAH as a Prometheus scrape target:

```yaml
# prometheus.yml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'rah-gateway'
    static_configs:
      - targets: ['gateway-host:8081']
    metrics_path: '/metrics'
```

**Key metrics exposed**:
- `rah_requests_total` — total request count (by status, api, tenant)
- `rah_request_duration_seconds` — request latency histogram
- `rah_cache_hits_total`, `rah_cache_misses_total` — cache performance
- `rah_upstream_duration_seconds` — upstream latency
- `rah_rate_limit_rejections_total` — rate limit hits
- `rah_llm_tokens_total` — LLM token usage (by model)
- `rah_llm_cost_usd_total` — LLM cost (by model)

---

## OpenTelemetry Export

Send traces to any OTEL-compatible backend (Jaeger, Grafana Tempo, Honeycomb, Datadog, etc.) with no database required.

```yaml
observability:
  store:
    type: memory          # no postgres needed
    max_traces: 1000      # Studio UI ring buffer

  export:
    otel:
      enabled: true
      endpoint: localhost:4317   # OTEL collector address (gRPC)
      insecure: true             # set false for TLS (production)
      service_name: rah-gateway
```

When `enabled: true`, the gateway fans out every completed trace to both the in-memory store (for the Studio UI) and the OTEL exporter — the two are independent. Studio browsing, `/observability/traces`, and the access log all continue to work exactly as before.

### How it works

Trace export is **fully async and zero-cost on the hot path**. The request goroutine writes into a lock-free slab ring and returns immediately. A background drain goroutine picks up completed traces in batches and calls the stores. The OTEL store sits alongside the memory store via a fan-out — no locks, no added latency to requests.

```
Request (hot path — unchanged)
  PersistTrace() → lock-free ring enqueue → returns immediately

Drain goroutine (async, existing)
  WriteTraceBatch([]TraceRecord)
    ├── MemObsStore    → Studio UI / REST API
    └── OTELObsStore   → OTEL SDK → gRPC batch → Collector
```

### What gets exported as spans

Each trace becomes a span tree in your backend:

```
[root span]  POST /api/chat   42ms
  ├── validate_token           1.2ms   ← instruction span
  ├── registry_lookup          0.1ms
  ├── rate_limit_v2            0.1ms
  ├── llm:gpt-4o              38ms    ← LLM call span
  │     rah.model=gpt-4o
  │     rah.input_tokens=512
  │     rah.output_tokens=128
  │     rah.cost_micro=420
  └── http_call_upstream        2ms
```

**Root span attributes**: `http.method`, `http.status_code`, `rah.api_name`, `rah.tenant_id`, `rah.duration_ns`, `rah.gateway_ns`, `rah.upstream_ns`, `rah.upstream_calls`, `rah.req_bytes`, `rah.res_bytes`

**Instruction child spans**: `rah.instr_name`, `rah.instr_pc`, `rah.duration_ns`

**LLM child spans**: `rah.model`, `rah.llm_status`, `rah.input_tokens`, `rah.output_tokens`, `rah.cost_micro`, `rah.duration_ns`

Instruction span names are resolved from the compiled API schema. If a schema is not yet loaded, names fall back to `instr_<pc>`.

### Span timing

Instruction start times are reconstructed as cumulative offsets from the root span start (durations are recorded precisely, start offsets are approximate). LLM call spans use the root start time with their exact duration.

### Local development (zero infrastructure)

Run a collector as a sidecar that prints spans to stdout — no Jaeger or Tempo needed:

```bash
docker run --rm -p 4317:4317 \
  otel/opentelemetry-collector \
  --config /etc/otel/config.yaml
```

Or use the [OTEL collector contrib](https://github.com/open-telemetry/opentelemetry-collector-contrib) with a `logging` exporter to print spans directly to the terminal during development.

### Production setup (Grafana Tempo example)

```yaml
observability:
  store:
    type: memory
  export:
    otel:
      enabled: true
      endpoint: tempo.internal:4317
      insecure: false
      service_name: rah-gateway
```

The gRPC connection is persistent and batched — spans are buffered by the OTEL SDK's `BatchSpanProcessor` (up to 512 spans, flushed every 5 seconds or when the buffer fills) before being sent over a single gRPC stream.

### Note on distributed trace correlation

RAH generates its own `TraceID` (a monotonic counter) for each request. If your clients send a W3C `traceparent` header, it is currently captured as request metadata but not used as the OTEL parent context — so RAH spans appear as independent trees in your backend rather than as children of the caller's trace. Cross-service correlation via `traceparent` is a planned future addition.

---

## Prometheus + Grafana Quick Setup

### 1. Enable Prometheus Export

```yaml
observability:
  export:
    prometheus:
      enabled: true
```

### 2. Configure Prometheus Scrape

Add to `prometheus.yml`:
```yaml
scrape_configs:
  - job_name: 'rah-gateway'
    static_configs:
      - targets: ['localhost:8081']
    scrape_interval: 15s
    scrape_timeout: 5s
```

### 3. Import Grafana Dashboard

In Grafana:
1. **Dashboards** → **New** → **Import**
2. Paste the RAH dashboard JSON (available at `https://grafana.com/grafana/dashboards/rah-gateway`)
3. Select Prometheus data source
4. Click Import

### 4. Create Key Panels

**Request Rate**:
```
rate(rah_requests_total[1m])
```

**P99 Latency**:
```
histogram_quantile(0.99, rah_request_duration_seconds)
```

**Error Rate (5xx)**:
```
rate(rah_requests_total{status=~"5.."}[1m]) / rate(rah_requests_total[1m])
```

**Cache Hit Rate**:
```
rah_cache_hits_total / (rah_cache_hits_total + rah_cache_misses_total)
```

### 5. Set Up Alerts

Example alert: P99 latency exceeding 500ms:
```yaml
groups:
  - name: rah
    rules:
      - alert: RAHHighLatency
        expr: histogram_quantile(0.99, rah_request_duration_seconds) > 0.5
        for: 5m
        annotations:
          summary: "RAH P99 latency > 500ms"
          description: "Current P99: {{ $value }}s"
```

Example alert: Error rate spike:
```yaml
      - alert: RAHHighErrorRate
        expr: rate(rah_requests_total{status=~"5.."}[1m]) > 0.01
        for: 2m
        annotations:
          summary: "RAH error rate > 1%"
          description: "Current error rate: {{ $value }}"
```

---

## Common Observability Patterns

### Pattern 1: Monitor Upstream Performance

Track how much time your gateway spends waiting for upstream services:

```bash
# In Prometheus
upstream_latency_p99 = histogram_quantile(0.99, rah_upstream_duration_seconds)
gateway_latency_p99 = histogram_quantile(0.99, rah_request_duration_seconds)
gateway_overhead = gateway_latency_p99 - upstream_latency_p99
```

If `gateway_overhead` is high (e.g., > 50ms), investigate:
- Is a step doing expensive computation? (check traces)
- Is cache hitting? (check cache hit rate metric)
- Is there contention? (check worker pool depth)

### Pattern 2: Detect Rate Limit Abuse

Alert when a single tenant triggers rate limits frequently:

```
rate(rah_rate_limit_rejections_total{tenant="acme-corp"}[5m]) > 1
```

Then investigate:
- Is the tenant's traffic legitimate? (check access logs)
- Do they need a higher limit?
- Is their API key compromised?

### Pattern 3: Identify Slow APIs

Find APIs with high P99 latency:

```bash
curl "http://localhost:8081/observability/traces?sort=latency_desc&limit=10"
```

For each slow trace:
1. Look at the step breakdown — which step took longest?
2. If it's an upstream call, check if the upstream service is slow
3. If it's a computation step, profile the flow logic

### Pattern 4: Track LLM Cost

Monitor AI/LLM spending per tenant:

```bash
curl "http://localhost:8081/api/v1/costs/monthly?tenant=acme-corp&breakdown=model"
```

Set up alerts if monthly cost exceeds budget:

```yaml
observability:
  alerts:
    - name: llm_cost_exceeded
      threshold_usd: 10000
      period: monthly
      action: notify
```

---

## Troubleshooting Observability Issues

### Missing Access Logs

```
No entries in access log despite active traffic
```

**Cause**: Access logging disabled or pipeline backpressure

**Fix**:
```yaml
observability:
  access_log:
    enabled: true

ingest:
  enabled: true
  sinks:
    - kind: file
      path: /var/log/rah/events.jsonl
```

Check ingest pipeline health:
```bash
curl http://localhost:8081/observability/ingest-stats
```

### High Memory Usage in Observability

```
Gateway memory growing over time
```

**Cause**: Metrics aggregation windows too large, or ingest buffer full

**Fix**:
1. Reduce retention: `windows: ["1m", "5m"]` (drop 1h)
2. Disable detailed tracing: `instruction_timing: false`
3. Lower sample rate: `sample_rate: 0.01` (1% instead of 5%)
4. Disable detailed AI logging: `capture_prompts: false`

### Traces Not Captured

```
No traces visible in Studio
```

**Fix**:
```yaml
observability:
  traces:
    enabled: true
    sample_rate: 1.0    # 100% for testing
    always_trace_5xx: true
```

If still not working:
```bash
curl http://localhost:8081/observability/traces?limit=100
```

If the API returns data but Studio doesn't show it, restart the UI.

---

## Performance: Observability Overhead

RAH's observability system is designed for zero impact on request latency:

- **Access log**: ~50-100ns (in-memory append)
- **Metrics**: ~50ns (lock-free counter increment)
- **Ingest pipeline**: Async; doesn't block request path

Even with full observability enabled (traces, detailed AI logging), the overhead is < 1% of total latency.

For ultra-low-latency use cases, reduce sampling:

```yaml
observability:
  traces:
    sample_rate: 0.001    # 0.1% sampling (1 in 1000 requests)
  metrics:
    enabled: true         # always-on, minimal overhead
```

---

## Best Practices

1. **Always enable metrics** — the overhead is negligible and they're invaluable for debugging
2. **Sample traces appropriately** — 5% for normal load, 1% for high-traffic APIs
3. **Always trace errors** — set `always_trace_5xx: true` to capture issues
4. **Ship logs off-machine** — don't rely on local disk for observability
5. **Set up alerts on latency and error rate** — catch issues before users do
6. **Monitor the pipeline** — check `ingest-stats` to ensure events aren't dropped
7. **Use custom log fields** — add business context (order ID, user tier, etc.) for easier debugging
8. **Review traces weekly** — identify slow APIs and optimize them iteratively

---

## Further Reading

- [Prometheus Docs](https://prometheus.io/docs/) — Time-series metrics
- [OpenTelemetry](https://opentelemetry.io/) — Observability standards
- [Grafana Docs](https://grafana.com/docs/) — Dashboarding and alerting
- [SRE Book: Monitoring Distributed Systems](https://sre.google/sre-book/monitoring-distributed-systems/)
