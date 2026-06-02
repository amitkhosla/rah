# Rate Limiting and Traffic Management

## Overview

RAH provides layered traffic protection:
- **Rate Limiting V2** — flexible multi-window, multi-dimension limits
- **Spike Arrest** — smooth traffic bursts
- **Circuit Breaker** — protect downstream services
- **IP Restriction** — CIDR-based allow/deny lists
- **Geo Blocking** — country-based access control

---

## Rate Limiting V2 (Recommended)

V2 configs support multiple time windows — ALL must pass for the request to proceed.

### Create a Rate Limit Config via REST API

```bash
curl -X POST http://localhost:8081/rate-limit-configs-v2 \
  -H "Content-Type: application/json" \
  -d '{
    "name": "standard-api",
    "windows": [
      {"duration": "1s",  "limit": 10},
      {"duration": "1m",  "limit": 500},
      {"duration": "1h",  "limit": 10000}
    ],
    "on_exceeded": {
      "status": 429,
      "body": "Rate limit exceeded. Please slow down."
    }
  }'
```

### Using Rate Limits in a Flow

```yaml
flow_name: api_flow
steps:
  - kind: rate_limit_v2
    config: "standard-api"
    count_by: tenant      # tenant, ip, key, global, or user
```

Count by options:
- `tenant` — Separate limit per tenant
- `ip` — Separate limit per IP address
- `key` — Separate limit per API key
- `user` — Separate limit per user (from claims)
- `global` — Single limit across all requests

---

## Rate Limit Enforcement Modes

| Mode | Description | Requires |
|---|---|---|
| `approximate` | Local in-memory counters (default) | Nothing extra |
| `strict` | Cross-pod counters via Redis | Redis datastore bound to `rate_limit_sync` |

For strict mode, add to config: `"enforcement": "strict"`

---

## API-Level Rate Limit Policies

Attach rate limits directly to an API without modifying its flow:

```yaml
apis:
  - name: my-api
    path: /api/resource
    method: GET
    flow_name: my_flow
    action: upsert
    rate_limit_policies:
      - kind: named
        name: "standard-api"
        count_by: tenant
      - kind: named
        name: "per-ip"
        count_by: ip
```

These are auto-injected at the start of the flow.

---

## Dynamic Rate Limits

Route to different configs based on tenant tier:

```yaml
rate_limit_policies:
  - kind: dynamic
    meta_key: "tier"      # use tenant's "tier" metadata value
    mapping:
      "free":       "free-limits"
      "pro":        "pro-limits"
      "enterprise": "enterprise-limits"
```

---

## Spike Arrest

Allows at most one request per interval, smoothing bursts:

```yaml
flow_name: api_flow
steps:
  - kind: spike_arrest
    interval_ms: "100"   # max 10 req/second per tenant
```

If a request arrives before the interval has elapsed, it is **queued** (not dropped). RAH serves it as soon as the interval passes.

---

## Circuit Breaker

Open the circuit after consecutive failures:

```yaml
flow_name: api_flow
steps:
  - kind: circuit_breaker
    name: "upstream-cb"
    threshold: "5"           # open after 5 failures
    timeout: "30000"         # retry after 30 seconds
    
  - kind: http.get
    url_var: upstream_url
    timeout: 5000
    
  - kind: record_circuit_outcome
    # must call after the upstream call
```

When open (tripped), requests fail fast with a 503 until the timeout expires.

---

## IP Restriction

```yaml
flow_name: api_flow
steps:
  # Allow only from specific ranges
  - kind: ip_restriction
    mode: allow
    cidrs: "10.0.0.0/8,192.168.1.0/24"
  
  # Or block specific ranges
  - kind: ip_restriction
    mode: deny
    cidrs: "1.2.3.4/32"
```

---

## Geo Blocking

Requires MaxMind GeoLite2 database configured:

```yaml
flow_name: api_flow
steps:
  # Allow only US traffic
  - kind: geo_block
    mode: allow_list
    countries: "US,CA"
  
  # Or block specific countries
  - kind: geo_block
    mode: block_list
    countries: "CN,RU"
```

---

## Body Size Limit

Prevent large payload attacks:

```yaml
flow_name: api_flow
steps:
  - kind: limit_body
    max: "1MB"      # returns 413 if exceeded
```

---

## Per-Tenant Rate Limit Overrides

Give different customers different rate limits without creating separate configurations:

```bash
# Give tenant 20% more capacity
curl -X POST http://localhost:8081/tenants/acme-corp/rate-limits \
  -d '{"scale_pct": 20}'

# Block a tenant
curl -X POST http://localhost:8081/tenants/bad-actor/rate-limits \
  -d '{"blocked": true}'

# Disable rate limits for a tenant (premium)
curl -X POST http://localhost:8081/tenants/enterprise-corp/rate-limits \
  -d '{"rl_disabled": true}'
```

---

## Rate Limit Response Headers

When enabled in config, the gateway returns:
- `X-RateLimit-Limit` — configured limit
- `X-RateLimit-Remaining` — requests remaining in current window
- `X-RateLimit-Reset` — Unix timestamp when window resets

Example response:
```
HTTP/1.1 429 Too Many Requests
X-RateLimit-Limit: 500
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1717200060
Retry-After: 47

{
  "error": "rate limit exceeded",
  "retry_after": 47,
  "limit": 500,
  "window": "fair_share"
}
```

---

## Monitoring Rate Limits

Studio Observability → Metrics → filter by API name to see 429 rates.

Access log includes status codes for all rejected requests.

Common metrics:
- `rate_limit.requests_total` — Total requests evaluated
- `rate_limit.limited_total` — Requests blocked
- `rate_limit.remaining` — Requests remaining before hitting limit

---

## Real-World Examples

### Example 1: SaaS Multi-Tenant with Tiers

**Scenario:** Free (1K/day), Pro (100K/day), Enterprise (unlimited)

```bash
# Create free tier config
curl -X POST http://localhost:8081/rate-limit-configs-v2 \
  -d '{
    "name": "free-tier",
    "windows": [
      {"duration": "1d", "limit": 1000, "count_by": "tenant", "enforcement": "strict"}
    ]
  }'

# Create pro tier config
curl -X POST http://localhost:8081/rate-limit-configs-v2 \
  -d '{
    "name": "pro-tier",
    "windows": [
      {"duration": "1d", "limit": 100000, "count_by": "tenant", "enforcement": "strict"}
    ]
  }'
```

**Flow:**
```yaml
flow_name: api_flow
steps:
  - kind: registry.lookup
    source: header
    header_name: X-Tenant-ID
  
  - kind: registry.meta
    key: tier
    var: tier
  
  - kind: conditional
    condition: "tier == 'free'"
    true_steps:
      - kind: rate_limit_v2
        config: "free-tier"
    false_steps:
      - kind: rate_limit_v2
        config: "pro-tier"
  
  - kind: http.get
    url_var: upstream_url
```

### Example 2: Public API with Per-IP Limiting

```bash
curl -X POST http://localhost:8081/rate-limit-configs-v2 \
  -d '{
    "name": "public-api",
    "windows": [
      {"duration": "1s",  "limit": 5,    "count_by": "ip", "enforcement": "approximate"},
      {"duration": "1h",  "limit": 1000, "count_by": "ip", "enforcement": "strict"}
    ]
  }'
```

**Flow:**
```yaml
flow_name: api_flow
steps:
  - kind: rate_limit_v2
    config: "public-api"
    count_by: ip
  
  - kind: http.get
    url: "https://backend.example.com"
```

### Example 3: Protect Slow Upstream

```yaml
flow_name: api_flow
steps:
  # Smooth traffic to 10 req/sec per tenant
  - kind: spike_arrest
    interval_ms: "100"
    scope: tenant
  
  # Protect payment service
  - kind: circuit_breaker
    name: "payment"
    threshold: "3"
    probe_interval: "30000"
  
  - kind: http.post
    url: "https://payment-service/charge"
    timeout: 10000
  
  - kind: record_circuit_outcome
```

### Example 4: Emergency Tenant Blocking

```bash
# Block immediately (no restart needed)
curl -X POST http://localhost:8081/tenants/compromised/rate-limits \
  -d '{"blocked": true, "comment": "Investigating abuse"}'

# Re-enable after investigation
curl -X POST http://localhost:8081/tenants/compromised/rate-limits \
  -d '{"blocked": false}'
```

---

## Troubleshooting

### All Tenants Getting 429

**Diagnosis:** Check if using `count_by: global` instead of `count_by: tenant`

**Fix:**
```bash
curl http://localhost:8081/rate-limit-configs-v2/your-config
# Verify count_by setting
```

### Requests Succeed Sometimes, Fail Other Times (Multiple Instances)

**Diagnosis:** Using `enforcement: approximate` with multiple instances. Each instance maintains its own counter.

**Fix:** Use `enforcement: strict` for important windows and ensure Redis is configured:
```bash
curl http://localhost:8081/config/datastores
# Verify rate_limit_sync is bound to Redis
```

### Circuit Breaker Not Opening

**Diagnosis:** `record_circuit_outcome()` call not executed if request fails before that point.

**Fix:** Ensure the call happens after the HTTP call.

### High Latency with Strict Rate Limiting

**Diagnosis:** Redis round-trip adds ~1–5ms. This is expected.

**Mitigation:**
1. Use `enforcement: approximate` for short windows (1s, 1m)
2. Use Redis Cluster for lower latency
3. Monitor Redis latency

---

## Best Practices

1. **Multiple windows** — Combine per-second (burst) + per-day (quota)
2. **Strict for billing** — Business-critical limits use Redis (strict)
3. **Approximate for short windows** — Per-second limits use local counters
4. **Test your limits** — Generate traffic exceeding limits and verify 429s
5. **Monitor per-tenant** — Alert when approaching limit or frequently blocked
6. **Document limits** — Make rate limits clear in API docs
7. **Provide Retry-After** — Help clients implement intelligent backoff
