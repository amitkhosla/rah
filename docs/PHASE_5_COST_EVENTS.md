# Phase 5: Cost Events via Ingest Framework (COMPLETE ✅)

## Overview

Refactored cost tracking to use the existing ingest framework instead of storing cost history in the gateway. Cost events are now emitted to configurable sinks (HTTP, Redis, S3, etc.) and consumed by external analytics services.

## Architecture Change

### Before (Phase 4)
```
Gateway (main.go)
├─ PricingManager (initialized)
├─ MetricsCollector (initialized)
├─ DailyLearningJob (background goroutine)
├─ Cost quota enforcement
├─ Cost tracking API routes (/api/v1/costs/*)
└─ Cost history storage (in-memory)
```

### After (Phase 5)
```
Gateway (main.go)
├─ Cost quota enforcement (local, fast)
├─ Emit CostRecord event (deferred, via ingest pipeline)
└─ No cost history storage

         ↓

Ingest Pipeline
├─ Routes KindCostRecord to configured sinks
└─ Supports HTTP, Redis, S3, File, stdout

         ↓

External Analytics Service
├─ Consumes cost events
├─ Stores in time-series DB
└─ Provides query APIs
```

## Changes Made

### 1. **ingest/types.go** - Added Cost Event Kind
```go
const (
    KindCostRecord EventKind = "cost_record"
)
```

### 2. **engine/steps/cost_budget.go** - New Structures
Added two new types:

```go
type CostEventPayload struct {
    TenantKey string  `json:"tenant_key"`
    APIKey    string  `json:"api_key,omitempty"`
    Cost      float64 `json:"cost"`
    Model     string  `json:"model,omitempty"`
    Timestamp int64   `json:"timestamp_ns"`
}

type RecordCostConfig struct {
    QuotaManager   *quota.CostQuotaManager
    IngestPipeline *ingest.Pipeline
    CostSlot       int
}
```

### 3. **engine/steps/cost_budget.go** - Updated RecordCost Function
```go
func RecordCost(cfg RecordCostConfig) engine.Instruction
```

Changed signature from single param to config struct.

**Behavior**:
- Phase 1: Update quota manager (in-memory, local)
- Phase 2: Schedule cost event emission (deferred, via AfterResponse hook)

Cost events are emitted **after HTTP response is sent**, ensuring:
- Request latency unaffected by analytics pipeline
- No blocking on slow sinks
- Fire-and-forget semantics

### 4. **control/compiler.go** - Injected Pipeline
```go
case "record_cost":
    cfg := steps.RecordCostConfig{
        QuotaManager:   c.fm.CostQuotaManager,
        IngestPipeline: c.IngestPipeline,
        CostSlot:       costSlot,
    }
    c.GlobalTable = append(c.GlobalTable, steps.RecordCost(cfg))
```

### 5. **cmd/rah-gateway/main.go** - Removed Obsolete Code
- ❌ Removed: `PricingManager` initialization (lines 161-169)
- ❌ Removed: `MetricsCollector` initialization (lines 174)
- ❌ Removed: `DailyLearningJob` (lines 176)
- ❌ Removed: `compiler.PricingManager` assignment (line 248)
- ❌ Removed: import of `pricing` package
- Added: Comment explaining deferred cost event emission

### 6. **cmd/rah-gateway/routes_cost.go** - Deleted
Entire file removed. Cost history APIs now provided by external analytics service.

### 7. **config/cost-analytics.example.yaml** - New Config
Comprehensive example showing:
- Cost event routing to HTTP endpoint + S3 archive
- Event structure and payload format
- Analytics service responsibilities
- Environment variables needed
- Graceful degradation patterns

## Event Flow

```
Request Processing (Hot Path):
  1. EnforceCostBudget (pre-check)
  2. [LLM Call]
  3. RecordCost (quota update + schedule event)
       → ctx.AfterResponse.append(emitCostEvent)

Response Sent

AfterResponse Hooks (Deferred):
  → Emit CostRecord event to ingest pipeline
       {
         "tenant_id": 1,
         "kind": "cost_record",
         "model": "gpt-4o",
         "ts_ns": 1680000000000000000,
         "payload": {
           "tenant_key": "startup-daily",
           "cost": 0.087,
           "model": "gpt-4o",
           "timestamp_ns": 1680000000000000000
         }
       }

  → Ingest Pipeline routes to configured sinks:
       - HTTP → Analytics Service (primary)
       - S3 → Cost Archive (backup)
       - stdout → Debugging (optional)

Analytics Service:
  → Consumes from HTTP sink
  → Stores in time-series DB
  → Provides APIs: /costs/{tenantId}, /costs/breakdown, etc.
```

## Cost Event Payload Structure

```json
{
  "tenant_key": "enterprise-monthly",
  "api_key": "sk-...",  // Optional: which key was used
  "cost": 0.087,        // USD
  "model": "gpt-4o",
  "timestamp_ns": 1680000000000000000
}
```

## Configuration Example

```yaml
ingest:
  enabled: true
  kinds:
    - kind: cost_record
      ring_size: 131072
      sinks: [analytics_http, cost_archive]

  sinks:
    # Real-time analytics
    - name: analytics_http
      kind: http
      url: "{{ env 'ANALYTICS_SERVICE_URL' }}/api/ingest"
      format: ndjson

    # Long-term archive
    - name: cost_archive
      kind: s3
      bucket: cost-events-archive
      prefix: "gateway/{{ date }}/cost_record"
      format: ndjson
```

## Graceful Degradation

- ✅ If analytics service unavailable: events dropped (non-blocking)
- ✅ If pricing config missing: zero-cost fallback
- ✅ If quotas missing: unlimited (no enforcement)
- ✅ If ingest pipeline disabled: cost recording skipped (local quotas still work)
- ✅ Gateway continues operating in all scenarios

## Key Improvements

| Aspect | Before | After |
|--------|--------|-------|
| **Cost Storage** | In gateway memory | External service |
| **Event Route** | None | Configurable sinks |
| **Analytics** | Limited API | Full time-series DB |
| **Scaling** | Single process | Distributed |
| **Latency** | Blocking (if slow) | Deferred (non-blocking) |
| **Code in main.go** | Bloated | Minimal |
| **Pricing Mgmt** | Hardcoded | Config-based |

## Files Changed

1. ✅ `internal/ingest/types.go` - Added KindCostRecord
2. ✅ `internal/engine/steps/cost_budget.go` - Refactored RecordCost
3. ✅ `internal/control/compiler.go` - Injected pipeline
4. ✅ `cmd/rah-gateway/main.go` - Removed obsolete code
5. ✅ `cmd/rah-gateway/routes_cost.go` - Deleted (moved to analytics service)
6. ✅ `config/cost-analytics.example.yaml` - New comprehensive config

## Testing

**Code Compiles**: ✅
```bash
go build -o bin/rah-gateway ./cmd/rah-gateway/
```

**No cost-related errors** in:
- `internal/ingest`
- `internal/engine/steps`
- `internal/control`

## Next Steps (Optional - Not in Phase 5)

1. **Implement Analytics Service** (separate repo):
   - HTTP endpoint to consume cost events
   - Time-series DB integration (InfluxDB, Prometheus, TimescaleDB)
   - Query APIs: `/costs/{tenantId}`, `/costs/by-model`, `/costs/gateway-total`

2. **Studio Integration**:
   - Call analytics service APIs instead of gateway cost routes
   - Display cost breakdown by tenant/model/time

3. **Monitoring**:
   - Alert on quota overspend
   - Track analytics service health
   - Monitor event sink backpressure

## Documentation

- See `config/cost-analytics.example.yaml` for complete configuration
- See `internal/ingest/` for pipeline documentation
- Cost events follow same pattern as LLM events in ingest framework

## Rollback Plan

If needed, can revert by:
1. Restoring `routes_cost.go` from git history
2. Re-adding pricing/metrics initialization to main.go
3. Changing RecordCost back to single-param function

However, new ingest-based approach is preferred (cleaner architecture).
