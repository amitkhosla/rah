# main.go Integration Guide - Pricing & Cost Quotas

## 🎯 Overview

This guide shows how to integrate the cost quota system into `cmd/rah-gateway/main.go`. The integration involves:

1. **Pricing Manager** - Load pricing from config
2. **Cost Quota Manager** - Already wired in FlowManager
3. **Cost Tracking API** - Register HTTP endpoints
4. **Daily Learning Job** - Start token metrics collection
5. **Full Request Flow** - Enforce cost budgets before LLM calls

**Current Status**: Pricing and quota managers exist but aren't wired into main.go yet.

---

## 📍 Integration Points in main.go

### 1. Add Pricing Import

**Location**: Line ~20 (after other imports)

```go
import (
    ...
    "rah/internal/pricing"
    "rah/internal/quota"
    ...
)
```

### 2. Initialize Pricing Manager (After datastore, before compiler)

**Location**: Around line 150-200 (after datastore initialization, before creating Compiler)

```go
// ─── Initialize Pricing Manager ───────────────────────────────────
log.Println("Initializing pricing manager...")

pricingMgr := pricing.NewPricingManager()

// Load pricing from config (if provided)
if err := pricingMgr.LoadFromConfig(cfgMgr.Gateway().Pricing); err != nil {
    log.Printf("Warning: failed to load pricing from config: %v", err)
}

// Load pricing from LLM model configs (as second level)
if err := pricingMgr.LoadFromLLMConfig(cfgMgr.Gateway().LLM); err != nil {
    log.Printf("Warning: failed to load pricing from llm.models[]: %v", err)
}

// Inject into Compiler (will be used by calculate_cost steps)
compiler.PricingManager = pricingMgr

log.Printf("Pricing initialized: %d models cached", len(pricingMgr.GetPricing()))
```

### 3. Load Cost Quotas (After FlowManager creation)

**Location**: Around line 200-250 (after FlowManager created, before registering routes)

```go
// ─── Initialize Cost Quotas ───────────────────────────────────────
log.Println("Initializing cost quotas...")

quotaMgr := fm.CostQuotaManager

// Load quota configurations for each tenant
// This would come from your config or a separate quota config file
// For now, example with hardcoded quotas (replace with config loading)
exampleQuotas := map[string]quota.CostQuotaConfig{
    "acme-corp": {
        DailyCostLimit:   1000.0,
        MonthlyCostLimit: 20000.0,
        Windows: []quota.QuotaWindow{
            {
                Duration: 1 * time.Hour,
                Name:     "hourly",
                Limit:    100.0,
            },
            {
                Duration: 24 * time.Hour,
                Name:     "daily",
                Limit:    1000.0,
            },
            {
                Duration: 7 * 24 * time.Hour,
                Name:     "weekly",
                Limit:    5000.0,
            },
        },
    },
    "startup-xyz": {
        DailyCostLimit:   500.0,
        MonthlyCostLimit: 10000.0,
    },
    "dev-team": {
        // No quota limits (unlimited)
    },
}

for tenantID, quotaCfg := range exampleQuotas {
    quotaMgr.RegisterQuota(tenantID, quotaCfg)
}

log.Printf("Cost quotas initialized: %d tenants", len(exampleQuotas))
```

### 4. Initialize Metrics Collector & Daily Learning Job

**Location**: Around line 250-270 (before starting HTTP server)

```go
// ─── Initialize Metrics Collector & Learning Job ──────────────────
log.Println("Initializing metrics collection...")

metricsCollector := pricing.NewMetricsCollector()

// Start daily learning job at 2 AM UTC
learningJobTime := time.Date(2000, 1, 1, 2, 0, 0, 0, time.UTC)
learningJob := pricing.NewDailyLearningJob(metricsCollector, learningJobTime)

log.Printf("Daily learning job scheduled")

// Defer cleanup
defer learningJob.Stop()
```

### 5. Register Cost Tracking API Routes

**Location**: Around line 300-350 (where other routes are registered)

```go
// ─── Register Cost Tracking API Routes ─────────────────────────────
log.Println("Registering cost tracking API endpoints...")

mux := http.NewServeMux()

// Get admin token from environment or config
adminToken := os.Getenv("ADMIN_TOKEN")
if adminToken == "" {
    log.Printf("Warning: ADMIN_TOKEN not set. Cost tracking endpoints will require header authentication.")
}

// Register cost routes
RegisterCostRoutes(mux, quotaMgr, adminToken)

log.Printf("Cost tracking endpoints registered at /api/v1/costs*")
```

### 6. Wiring into Flow Compilation

**Location**: Already done! (FlowManager.CostQuotaManager is initialized in engine/manager.go)

When the compiler compiles flows with cost steps:
```go
{"action": "enforce_cost_budget", "key_identifier": "estimated_cost_slot"}
```

The compiler will use `c.fm.CostQuotaManager`, which is already set up.

---

## 🔄 Full Request Flow (With Cost Enforcement)

```
1. Client Request arrives
   ↓
2. Tenant extracted from header/context
   ctx.TenantKey = "acme-corp"
   ↓
3. Flow execution begins
   ↓
4. [estimate_tokens step] - Estimate token count from prompt size
   IntSlots[estimated_tokens_slot] = ~500
   ↓
5. [calculate_cost step] - Look up pricing from pricingMgr
   IntSlots[estimated_cost_slot] = int64(0.00075 * 1e9)  // $0.00075
   ↓
6. [enforce_cost_budget step] ⭐ - CHECK QUOTA BEFORE LLM
   quotaMgr.CanAfford("acme-corp", 0.00075)
   ├─ Check daily window: 150.00 + 0.00075 < 1000.00 ✓
   ├─ Check hourly window: 10.00 + 0.00075 < 100.00 ✓
   └─ Result: Allowed, continue
   ↓
7. [llm_call step] - Call LLM (guaranteed affordable)
   Response includes: {usage: {prompt_tokens: 480, completion_tokens: 42}}
   ↓
8. [calculate_actual_cost step] - Use REAL token counts
   cost = (480 * 5.0 + 42 * 15.0) / 1M = $0.00120
   IntSlots[actual_cost_slot] = int64(0.00120 * 1e9)
   ↓
9. [record_cost step] ⭐ - UPDATE QUOTA WITH ACTUAL USAGE
   quotaMgr.RecordCost("acme-corp", 0.00120)
   └─ Daily window updated: 150.00 → 150.00120
   └─ Hourly window updated: 10.00 → 10.00120
   ↓
10. Response sent to client
    (cost NOT included in response body by default)
    ↓
11. AfterResponse hook
    metricsCollector.RecordTokenEstimate("gpt-4o", 500, 522)
    └─ Tracks: estimated 500 vs actual 522 for token ratio learning
    ↓
12. Flow complete
```

---

## 📊 Configuration Example (rah.yaml)

```yaml
# Gateway layout
layout:
  max_apis: 1000

# Datastore (datastores first)
datastore: ...

# Secrets (for API keys)
secrets: ...

# Cache (response caching)
cache: ...

# LLM Models
llm:
  models:
    - alias: gpt-4o
      provider: openai
      adapter: openai
      api_key_ref: env:OPENAI_API_KEY
      capabilities:
        max_context_tokens: 128000

# Pricing Configuration (NEW)
pricing:
  models:
    - model: gpt-4o
      provider: openai
      cost_per_input_token: 5.0
      cost_per_output_token: 15.0
  cache_ttl: "1h"
  allow_missing_pricing: true

# Async jobs
async:
  enabled: false

# Ingest pipeline
ingest: ...
```

---

## 🧪 Testing the Integration

### 1. Manual Test: Cost Quota Enforcement

```bash
# Create a tenant with quota
curl -X POST http://localhost:8081/tenants \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "test-tenant",
    "quotas": {
      "daily_limit": 10.0,
      "monthly_limit": 100.0
    }
  }'

# Send a request that should be allowed
curl -X POST http://localhost:8080/api/test-endpoint \
  -H "X-Tenant: test-tenant" \
  -d '{"prompt": "What is 2+2?"}'
# Expected: 200 OK (within quota)

# Send multiple requests to exceed daily quota
for i in {1..20}; do
  curl -X POST http://localhost:8080/api/test-endpoint \
    -H "X-Tenant: test-tenant" \
    -d '{"prompt": "Expensive prompt with many tokens..."}'
done
# Expected: Some succeed (200), last ones fail with 429 (Payment Required)
```

### 2. Check Cost Status

```bash
# Get all costs (admin only)
curl -H "X-Admin-Token: secret-token" \
  http://localhost:8080/api/v1/costs

# Get specific tenant costs
curl -H "X-Admin-Token: secret-token" \
  http://localhost:8080/api/v1/costs/test-tenant

# Get summary
curl -H "X-Admin-Token: secret-token" \
  http://localhost:8080/api/v1/costs/summary
```

### 3. Verify Metrics

```bash
# Check token estimation accuracy (daily)
# (Would be in dashboard/admin UI)
# Expected: actual_tokens / estimated_tokens ≈ 1.0 ± 0.1
```

---

## 🚀 Deployment Checklist

- [ ] Add pricing imports to main.go
- [ ] Initialize PricingManager after datastore
- [ ] Load pricing from config
- [ ] Register quotas for all tenants
- [ ] Initialize metrics collector and learning job
- [ ] Register cost tracking API endpoints
- [ ] Set ADMIN_TOKEN environment variable
- [ ] Test enforce_cost_budget step in flows
- [ ] Test record_cost step after LLM calls
- [ ] Verify quota enforcement (429 response when exceeded)
- [ ] Check cost tracking API responses
- [ ] Monitor daily learning job logs
- [ ] Deploy to production

---

## 🔍 Debugging Tips

### Cost not being enforced?
1. Check if `enforce_cost_budget` step is in your flow
2. Verify `key_identifier` points to correct IntSlot
3. Check if tenant quota is registered: `curl /api/v1/costs/{tenantId}`
4. Look for "Budget exceeded" error logs

### Pricing not loading?
1. Check `pricing.models[]` is correct in YAML
2. Verify cost values are > 0
3. Check logs for "Loaded X pricing entries"
4. Fallback to hardcoded defaults (check pricing_data.go)

### Quota not resetting?
1. Check window Duration matches your expectation
2. Verify QuotaWindow.Name is set
3. Check logs for "Window reset at"
4. Monitor /api/v1/costs endpoints for reset times

---

## 📝 Code Snippet: Complete Integration Function

Here's a helper function you can add to main.go:

```go
// initializeCostingSystem sets up pricing, quotas, metrics, and API routes
func initializeCostingSystem(
    fm *engine.FlowManager,
    compiler *control.Compiler,
    cfgMgr *config.Manager,
    mux *http.ServeMux,
) error {
    ctx := context.Background()

    // 1. Pricing Manager
    log.Println("Initializing pricing manager...")
    pricingMgr := pricing.NewPricingManager()
    if err := pricingMgr.LoadFromConfig(cfgMgr.Gateway().Pricing); err != nil {
        log.Printf("Warning: pricing load failed (will use defaults): %v", err)
    }
    if err := pricingMgr.LoadFromLLMConfig(cfgMgr.Gateway().LLM); err != nil {
        log.Printf("Warning: llm pricing load failed: %v", err)
    }
    compiler.PricingManager = pricingMgr

    // 2. Cost Quotas (load from config, database, or hardcode)
    log.Println("Loading cost quotas...")
    quotaMgr := fm.CostQuotaManager
    // TODO: Load from config or database

    // 3. Metrics & Learning
    log.Println("Starting metrics collection...")
    metricsCollector := pricing.NewMetricsCollector()
    learningJob := pricing.NewDailyLearningJob(metricsCollector,
        time.Date(2000, 1, 1, 2, 0, 0, 0, time.UTC))
    defer learningJob.Stop()

    // 4. Cost Tracking API
    log.Println("Registering cost tracking endpoints...")
    adminToken := os.Getenv("ADMIN_TOKEN")
    RegisterCostRoutes(mux, quotaMgr, adminToken)

    log.Printf("Costing system initialized")
    return nil
}

// In main():
// initializeCostingSystem(fm, compiler, cfgMgr, mux)
```

---

## ✅ Success Criteria

After integration, you should see:

1. ✅ Logs: "Initializing pricing manager..."
2. ✅ Logs: "Loaded X pricing entries from config"
3. ✅ Logs: "Cost quotas initialized: N tenants"
4. ✅ Logs: "Daily learning job scheduled"
5. ✅ Logs: "Cost tracking endpoints registered"
6. ✅ API: `GET /api/v1/costs` returns 401 without token
7. ✅ API: `GET /api/v1/costs?token=admin` returns cost breakdown
8. ✅ Flows: `enforce_cost_budget` returns 429 when budget exceeded
9. ✅ Flows: `record_cost` updates quota manager

---

## 🔗 Related Files

- `cmd/rah-gateway/main.go` - Main entry point (integration target)
- `cmd/rah-gateway/routes_cost.go` - Cost tracking API handlers
- `internal/pricing/manager.go` - Pricing management with caching
- `internal/quota/manager.go` - Quota enforcement with flexible windows
- `internal/engine/manager.go` - FlowManager (has CostQuotaManager)
- `internal/engine/steps/cost_budget.go` - Instruction implementations
- `internal/control/compiler.go` - Compiler (wires PricingManager)
- `config/pricing.example.yaml` - Pricing configuration example

---

**Status**: Ready for main.go integration ✅
**Estimated Time**: 1-2 hours to fully integrate and test
**Next**: Implement TenantRegistry API key overrides (Phase 4 final)
