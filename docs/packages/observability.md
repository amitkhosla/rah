# Observability Package

## Purpose
Provides **telemetry, metrics, and tracing** for RAH. Collects latency data, request summaries, instruction-level timing, and error analytics. Enables performance monitoring and debugging without blocking request execution.

## Files
- **telemetry.go**: Main Telemetry/Observer with metrics aggregation and export
- **telemetry_test.go**: Test coverage for metric collection

## Key Types

### Metrics
- **GatewayMetrics**: Aggregate system metrics
  - RequestsTotal: Total request count
  - Requests5xx: 5xx error count
  - GatewayLatencyTotalNs: Total time in gateway
  - UpstreamLatencyTotalNs: Total time waiting for upstream
  - ClientBytesSentTotal, UpstreamBytesTxTotal, UpstreamBytesRxTotal: Data volume
  - InstructionTopSlow: Top-N slowest instructions
  - UpstreamTopSlow: Top-N slowest upstreams
  - TenantTop5xx: Top tenants by error rate

- **NameLatency**: Per-instruction or per-upstream metrics
  - Name: Instruction or upstream name
  - Count: Number of calls
  - TotalLatencyNs: Sum of latencies
  - BytesTx, BytesRx: Data volume

- **TenantError**: Errors by tenant
  - TenantID: Tenant identifier
  - Errors5xx: 5xx error count

- **MetricAgg**: Custom metric aggregation
  - Key: Metric identifier
  - Count: Occurrences
  - TotalValue: Aggregated value

### Request Tracing
- **RequestSummary**: Per-request trace metadata
  - TraceID, ApiID, TenantID: Identifiers
  - Status, Duration: Response metadata
  - GatewayDurationNs, UpstreamDurationNs: Latency breakdown
  - UpstreamCalls: Number of backend calls
  - BytesSent/Rx: Data volume
  - StartedAtUnixNano: Request start time

- **InstructionEvent**: Per-instruction trace
  - Seq: Sequence number
  - Name, PC: Instruction identifier
  - DurationNs: Execution time
  - Input, Output: KV metadata

- **KV**: Key-value pair for metadata
  - K, V: String key and value

## Responsibilities
1. **Metric Collection**: Aggregate latency, throughput, error counts
2. **Per-Request Tracing**: Record detailed execution trace
3. **Instruction Timing**: Measure individual instruction duration
4. **Upstream Tracking**: Track upstream call latency and errors
5. **Tenant Analytics**: Errors and metrics per tenant
6. **Export**: Expose metrics via HTTP endpoint
7. **Conditional Recording**: Only record when enabled to minimize overhead

## Dependencies
- **rctx**: Reads context metadata (TenantID, ApiID, Status)
- **engine**: Records instruction timing events
- **No external dependencies**: Uses only stdlib (JSON, HTTP, atomic)

## Metric Collection Flow
```
Request arrives
    ↓
Start: RequestSummary.StartedAtUnixNano
    ↓
Execute instructions
    ↓
Each instruction (if shouldMeasure):
  - Record InstructionEvent
  - Update NameLatency for this instruction
    ↓
Upstream call (if any):
  - Record upstream latency
  - Update UpstreamTopSlow
    ↓
Response sent
    ↓
Calculate:
  - GatewayLatencyTotalNs = end - start
  - UpstreamLatencyTotalNs = sum of upstream calls
    ↓
Update GatewayMetrics:
  - RequestsTotal++
  - If Status >= 500: Requests5xx++
  - Aggregate latency
    ↓
Optional: Export to tracing system
```

## Key Metrics

### Request-Level
- Total latency (gateway)
- Upstream latency (backend calls)
- Error rate (5xx errors)
- Data volume (bytes sent/received)
- Tenant error rates

### Instruction-Level
- Per-instruction count and latency
- Top-N slowest instructions
- Data volume per instruction

### Upstream-Level
- Per-upstream latency
- Top-N slowest upstreams
- Upstream error rates

## Performance Notes
- **Atomic operations**: Uses atomic counters (no locks on hot path)
- **Optional recording**: `InstructionTimingEnabled()` controls overhead
- **Top-N aggregation**: Uses efficient heap or sliding window
- **Lazy export**: Metrics exported on-demand via HTTP, not pushed
- **Zero-copy KV**: Key-value pairs as strings, not allocations

## Metric Export Endpoint
```
GET /metrics

Response: JSON
{
  "requests_total": 1000000,
  "requests_5xx": 150,
  "gateway_latency_total_ns": 50000000000,
  "upstream_latency_total_ns": 30000000000,
  "instruction_top_slow": [
    {
      "name": "http_call",
      "count": 100,
      "total_latency_ns": 5000000000
    },
    ...
  ],
  "upstream_top_slow": [
    {
      "name": "user-service:8080",
      "count": 100,
      "total_latency_ns": 5000000000
    },
    ...
  ],
  "tenant_top_5xx": [
    {"tenant_id": 1, "errors_5xx": 50},
    ...
  ]
}
```

## Tracing Integration
If trace recorder available:
- RecordRequestStart(): Begin request trace
- AppendInstructionEvent(): Add instruction event to trace
- RecordRequestEnd(): Finalize trace
- Export trace to external system (Jaeger, Datadog, etc.)

## Configuration
- **InstructionTimingEnabled()**: Boolean flag to enable instruction-level timing
- **TraceEnabled()**: Boolean flag to enable request tracing
- **MetricsInterval**: Interval for metric aggregation
- **TopNSize**: Size of top-N lists (e.g., top 10 slowest instructions)
