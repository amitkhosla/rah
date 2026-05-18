# Rate Limiting — Full Design Document

## 1. Goals

- Support 50K tenants, 6K+ APIs without per-tenant per-API configuration
- Two independent protection concerns: tenant fairness and upstream backend protection
- Configurable windows, enforcement modes, output, and error behavior
- Zero cross-config counter collisions
- Scalable from single-pod to large multi-pod deployments

---

## 2. Core Concepts

### 2.1 Two Independent Dimensions

```
Tenant rate limiting   →  Protects YOUR system from a tenant over-consuming
Upstream rate limiting →  Protects THEIR backend from being overwhelmed
```

Both can be active simultaneously. Each has independent counters, independent configs,
independent response codes. Neither implies the other. Customer configures both explicitly.

### 2.2 The Rate Limit Key (Dynamic Parameter)

The rate limit counter key is any value extractable from the request. Not a fixed dimension.

Built-in shortcuts:
- **Tenant**: TenantID is always available — no slot needed
- **IP**: bind_client_ip step populates a slot → rate limit by that slot
- **Upstream URL**: load upstream URL into a slot → rate limit by that slot
- **Static key**: a fixed string added as a constant → rate limit by that slot

General case: any slot value populated by any flow step before the rate limit step.

### 2.3 Composite Keys

Multiple slot values concatenated before hashing:
```
rate_limit_by: [device_id, tenant_id]   →  hash(concat(device_id, tenant_id))
```
UI presents as logical variable list. System handles concatenation. Customer never sees the hash.

### 2.4 Case Sensitivity

Keys are case-sensitive by default. Per-config option:
```
case_sensitive: false   →  key normalized to lowercase before hashing
```

### 2.5 Tier-Based Scalability

At 50K tenants and 6K APIs, per-tenant per-API configuration is impossible (300M combinations).
The only viable model:

```
~10 tier definitions  (admin-managed, rarely changes)
50K tenant → tier     (one property per tenant, set at onboarding/upgrade)
6K API → default tier (set at API creation)
```

Changing a tier definition propagates instantly to all tenants on that tier.

---

## 3. Rate Limit Config Model

### 3.1 Multi-Window Config

Configs are named reusable definitions. Each config supports multiple independent windows:

```yaml
name: "premium_limits"
enforcement: "approximate"      # "approximate" | "strict"
redis_unavailable: "fail_open"  # strict mode only: "fail_open" | "fail_closed"
windows:
  - period: "1s"   limit: 500   burst_factor: 150   # 150% burst on per-second
  - period: "5m"   limit: 5000                       # no burst on 5-minute
  - period: "1h"   limit: 50000
  - period: "1d"   limit: 500000
exceeded_status:       429
exceeded_body:         '{"error":"rate_limit_exceeded","retry_after":"{retry_after}"}'
exceeded_content_type: "application/json"
emit_headers:          true
header_names:
  limit:     "X-RateLimit-Limit"
  remaining: "X-RateLimit-Remaining"
  reset:     "X-RateLimit-Reset"
  retry:     "Retry-After"
case_sensitive:        true
```

**Rules**:
- At least one window required. Both period and limit required per window.
- `burst_factor` is per-window and optional. Default: no burst (100%).
- Burst applies only to the window it is defined on. It does not propagate to other windows.
- `per_sec = 0` in a config means blocked (zero requests allowed). This is distinct from
  no config assigned (unlimited).
- All windows check and increment independently. First exceeded window triggers the response.
  All windows always increment regardless of which one fired.

### 3.2 Window Storage by Duration

| Window duration | Counter location | Reason |
|----------------|-----------------|--------|
| ≤ 60s | Local in-process (per-config arena) | Per-request Redis round-trip unacceptable |
| > 60s | Redis ASYNC pipeline | Must survive pod restarts; consistency across pods |

In approximate mode, local windows use instance-count division.
In strict mode, ALL windows use Redis regardless of duration.

### 3.3 Enforcement Modes

**Approximate** (local in-process):
- Configured limit is divided by running instance count: `per_pod_limit = limit / N`
- Instance count read from existing `InstanceSync` heartbeat infrastructure
- Heartbeat interval: 5s, TTL: 15s (3× heartbeat — industry standard for API gateways)
- Instance count cached locally for 5s. Up to 5s stale — accepted approximate trade-off.
- Pod restart loses local counters for that pod. First window allows fresh burst. Accepted.
- Long windows (> 60s) route to Redis ASYNC even in approximate mode (for persistence).

**Strict** (Redis shared counter):
- Uses existing `RedisRateLimitProvider` (Lua INCR + pipeline batcher, 256-command batches,
  1ms deadline, 5ms timeout, fail-open on Redis unavailability).
- Exact enforcement across all pods. Counter survives pod restarts.
- `redis_unavailable` controls behavior when Redis is unreachable:
  - `fail_open` (default for most): allow traffic, accept inaccuracy
  - `fail_closed`: deny traffic, prioritize correctness

### 3.4 Template Variables in Output

Available in `exceeded_body` and header values:
```
{retry_after}   seconds until the exceeded window resets
{limit}         configured limit of the window that fired
{window}        "second" | "minute" | "5minute" | "hour" | "day" etc.
```

---

## 4. Tenant Rate Limiting

### 4.1 Tier Definitions

Small set (~5–20) of named tier records. Each defines:

```yaml
name: "premium"
rate_limit_config: "premium_limits"     # named config from Section 3
overall_config: "premium_overall"       # cross-API total quota (optional)
allowed_apis: "all"                     # or list of API names
blocked_apis: []                        # explicit API blocks for this tier
exceeded_status: 429
exceeded_body: '{"error":"quota_exceeded"}'
```

Special tiers:
- **`unlimited`**: `rate_limit_config: none` → check step passes through
- **`blocked`**: `rate_limit_config: blocked_config` (per_sec=0) OR `allowed_apis: none`

### 4.2 Three Levels of Tenant Rate Limiting

```
Level 1 — Overall tenant quota (cross-API)
  Counter key: hash(tenantID, overall_config_id, epoch)
  Checked before any API-specific limit.
  Configurable as daily/monthly volume caps (long window → Redis ASYNC).

Level 2 — API-level quota
  Counter key: hash(tenantID, api_config_id, epoch)
  Defined on the API definition. All endpoints share this counter.

Level 3 — Endpoint-level quota
  Counter key: hash(tenantID, endpoint_config_id, epoch)
  Most specific. Defined on the endpoint definition.
```

All three levels are checked independently. Any one exhausted → 429.

### 4.3 Tenant Tier Assignment

One property per tenant in the registry:
```
id:tier = "premium"
```

Tier resolution at request time: tenant identified → tier property read (~2ns) →
config ID from tier definition → counters checked.

### 4.4 Multipliers

Two types of multiplier can apply simultaneously:

**Flat tenant multiplier** (persistent, stored as tenant property):
```
meta:rl_multiplier = "0.5"    ← 50% of tier limits (trial mode, degraded plan)
```

**Variable-driven multiplier** (runtime, set by flow step):
```
A flow step reads an env variable or request attribute and writes to __rl_multiplier slot.
Example: env=npr → __rl_multiplier = "0.1"
```

**Composition**: multiplicative. Both apply together:
```
effective_limit = configured_limit × flat_multiplier × variable_multiplier
```

**Floor rule**: if effective_limit < 1 after multiplication, floor to 1 (minimum 1 req/window).
Exception: if base config has `per_sec = 0` (blocked), multiplier cannot change this.
`0 × anything = 0`. Multiplier cannot unblock a blocked config.

**Multiplier precision**: percentage in UI (e.g., `50` = 50%), float internally (`0.5`).
Valid range: `0.01` to any positive value (> 100% allowed for promotional boosts).
Log warning emitted when multiplication results in floor-to-1 correction.

### 4.5 Override Hierarchy

Resolution order at request time:

```
1. Explicit per-tenant exception block
   → blocked: 403 immediately

2. Tier entitlement (allowed_apis / blocked_apis)
   → not entitled: 403 immediately

3. Resolve effective config:
   base = tier's rate_limit_config
   apply flat tenant multiplier (meta:rl_multiplier)
   apply variable-driven multiplier (__rl_multiplier slot if set)
   effective_limit = floor(max(1, base × flat × variable))

4. Check overall tenant quota (Level 1)
   → exhausted: 429 with overall config's exceeded_status/body

5. Check API-level quota (Level 2)
   → exhausted: 429 with API config's exceeded_status/body

6. Check endpoint-level quota (Level 3)
   → exhausted: 429 with endpoint config's exceeded_status/body

7. Execute flow
```

Explicit per-tenant block always overrides tier entitlement (most specific wins).
Config change (tier upgrade/downgrade) takes effect instantly on next request.
Downgrade may cause immediate rate limiting for the current epoch — expected, documented.

### 4.6 Pre-Auth Rate Limiting

Before tenant identification, `TenantID = 0`. `check_rate_limit` with TenantID=0 uses a
shared anonymous bucket — all unauthenticated traffic competes for one counter.
Valid use case (global pre-auth cap) but IP-based is recommended for pre-auth scenarios.

Customer controls entirely via flow design. No system enforcement.

### 4.7 Unauthenticated Requests

No implicit fallback or default protection. Customer must explicitly add rate limit steps.
UI provides recommended flow template (see Section 7).

---

## 5. Upstream Rate Limiting

### 5.1 Purpose

Protects backend services from being overwhelmed regardless of which tenant is calling.
Counter key contains URL hash, not tenant ID. Completely independent from tenant limits.

```yaml
name: "openai_api"
pattern: "https://api.openai.com/*"
enforcement: "strict"
redis_unavailable: "fail_open"
windows:
  - period: "1m"   limit: 1000
exceeded_status:       502
exceeded_body:         '{"error":"upstream_at_capacity"}'
exceeded_content_type: "application/json"
emit_headers:          false    # default off for upstream limits
```

### 5.2 Three Levels of Upstream Rate Limiting

```
Level 1 — Global upstream quota (across all APIs using this upstream)
  Counter key: hash(urlHash, global_config_id, epoch)
  All APIs calling the same upstream share one counter.

Level 2 — Per-API upstream quota
  Counter key: hash(urlHash, api_config_id, epoch)
  Each API's calls to the upstream are counted independently.

Level 3 — Per-endpoint upstream quota
  Counter key: hash(urlHash, endpoint_config_id, epoch)
  Most granular upstream protection.
```

### 5.3 URL Pattern Registry

Admin-managed table mapping URL patterns to upstream configs:

```yaml
upstream_registry:
  unmatched: "fail_open"    # "fail_open" | "fail_closed" | "default_config"
  patterns:
    - pattern: "https://api.openai.com/v1/*"   config: "openai_v1_strict"
    - pattern: "https://api.openai.com/*"      config: "openai_general"
    - pattern: "https://db.internal/*"         config: "internal_db"
    - pattern: "*"                             config: "conservative_default"  # catch-all
```

**Pattern matching**: longest match wins (most specific pattern takes precedence).
Consistent with router path matching behavior.

**Static upstream URLs**: URL hash computed at bake time. Zero runtime cost.

**Dynamic upstream URLs**: URL read from slot at request time → FNV-1a hash computed
(~10ns) → pattern matched against registry → config ID resolved → counter checked.

**Unknown URL** (no pattern match): behavior per `unmatched` setting. Default: `fail_open`
(request proceeds without upstream rate limiting). Customer registers catch-all `*` for
strict control.

### 5.4 Retry Handling

Each retry attempt is a real call to the backend. Each attempt counts against the upstream
rate limit when a config is configured. No upstream rate limit configured → retries proceed
without any rate limit check.

No special retry-specific rate limit mechanism. Retries use the same config as initial calls.

### 5.5 Tenant Quota and Upstream Failures

If upstream rate limit fires (502), tenant quota counter remains incremented (no rollback).
Customer controls this via flow ordering:
```
# Upstream failure consumes tenant quota:
  check_tenant_rate_limit → check_upstream_rate_limit → http_call

# Upstream failure does NOT consume tenant quota:
  check_upstream_rate_limit → check_tenant_rate_limit → http_call
```

---

## 6. Counter Architecture

### 6.1 Per-Config Isolated Arenas

Each rate limit config gets its own completely independent counter space.
Different configs never share counter slots. Cross-config collisions: zero by construction.

```
Config "premium_limits"    → Arena A  [isolated]
Config "free_limits"       → Arena B  [isolated]
Config "ip_protection"     → Arena C  [isolated]
Config "openai_upstream"   → Arena D  [isolated]
```

### 6.2 Tenant-Based Configs — Direct Indexing (Zero Collisions)

TenantID is `uint16` (max 65536). Direct index — no hashing:

```
slot = arena_base + (tenantID × num_windows) + window_index
```

Each slot stores `(epoch uint32, count uint32)` = 8 bytes.

Memory per tenant-based config:
```
65536 tenants × 3 windows × 8 bytes = 1.5MB per config
20 configs → 30MB total  (trivial for a gateway process)
```

Zero collisions. O(1) access. No hash computation.

### 6.3 IP/Slot-Based Configs — Isolated Hash Arenas

IP addresses and slot values cannot be directly indexed (unbounded key space).
Hash within the config's own isolated arena:

```
slot = arena_base + (hash(keyBytes) % arena_size)
```

Collisions only occur between different IPs/slots within the same config — semantically
related traffic. Much less damaging than cross-config collisions.

**Arena sizing**: `arena_size = 4 × peak_concurrent_unique_keys_for_this_config`
Set per config by the operator based on expected traffic. Default: 16K slots.

**Collision rate metric** emitted per IP/slot-based config:
```
estimated_collision_rate = active_keys² / (2 × arena_size)
```
Alert threshold: > 0.5%. Operator increases arena size or switches to strict (Redis) mode.

### 6.4 Empty Key Handling

When the slot used as the rate limit key is empty (step didn't populate it):

```yaml
on_empty_key: "fail"              # default — deny with configured status/body
on_empty_key: "skip"              # allow through without rate limiting
on_empty_key: "fallback:tenant"   # fall back to tenant-based rate limiting
```

### 6.5 Multi-Step Execution Behavior

**Within one rate limit step** (multiple windows):
All windows always increment. First exceeded window triggers the rejection response.
Subsequent windows still run and increment even after a window fires.

**Between multiple rate limit steps** (e.g., tenant check → IP check → upstream check):
Default: all steps run and increment their counters regardless of prior failures.
First step to exceed its limit determines the response (first-fail-wins for response code/body).
Only the failing step emits rate limit headers to the caller.

Opt-out via `fail_fast: true` on a rate limit step:
```yaml
check_rate_limit:
  config: "tenant_limits"
  fail_fast: true    # stop immediately if this step fails — subsequent steps don't run
```

---

## 7. Recommended Flow Template (UI Default)

```
Step 1: bind_client_ip              ← capture IP before auth
Step 2: check_rate_limit_ip         ← pre-auth IP protection (optional)
Step 3: token_validation            ← identify tenant
Step 4: check_rate_limit            ← tenant quota (overall → API → endpoint)
Step 5: [business logic]
Step 6: check_upstream_rate_limit   ← just before upstream call
Step 7: http_call
Step 8: return
```

Customer can deviate freely. Template is guidance, not enforcement.
Compiler warns (A5 — todo list) when a consuming step appears before its producer step.

---

## 8. Internal / System Calls

No special bypass mechanism. Internal callers are just callers with an identifier that maps
to a high-limit or unlimited config:

```
Internal service sends:  X-Internal-Token: <key>
Flow step:               bind_header  key: X-Internal-Token  as: caller_type
Rate limit step:         check_rate_limit_by_slot  slot: caller_type
                         → resolves to unlimited config → passes through
```

For flows exclusively serving internal traffic (health checks, admin):
simply do not add rate limit steps to those flows.

---

## 9. UI Structure

Three distinct management screens, each with its own concept:

```
Settings
├── Tenant Tiers          ← tier definitions (windows, allowed APIs, exceeded config)
│   └── Tier Assignments  ← per-tenant tier selection
├── Upstream Services     ← URL pattern registry + upstream rate limit configs
└── Rate Limit Configs    ← shared named configs (windows, enforcement, output)
```

API / Endpoint definition panel has two independent rate limiting sections:
```
┌─ Tenant Rate Limiting ──────────────────────────────────────┐
│  Count by:   [ Per tenant ▾ ]  on_empty_key: [ fail ▾ ]    │
│  Config:     [ premium_limits ▾ ] [ + Create ]              │
│  Levels:     [x] Overall  [x] API  [x] Endpoint             │
└─────────────────────────────────────────────────────────────┘

┌─ Upstream Rate Limiting ────────────────────────────────────┐
│  Service:    [ openai_api ▾ ]  (from Upstream Services)     │
│  Levels:     [ ] Global  [x] API  [ ] Endpoint              │
└─────────────────────────────────────────────────────────────┘
```

---

## 10. Corner Case Decision Reference

| # | Corner Case | Decision |
|---|------------|---------|
| A1 | Empty slot key | `on_empty_key` configurable: fail (default) / skip / fallback:tenant |
| A2 | Composite keys | Concatenation. UI shows logical variables. System concatenates. |
| A3 | Case sensitivity | Case-sensitive default. `case_sensitive: false` opt-in per config. |
| A4 | Long keys | Non-issue. FNV-1a hashes any byte slice. |
| A5 | Step ordering validation | UI + compiler layer. Todo list. |
| B1 | Zero vs none | `0` = blocked. No config assigned = unlimited. |
| B2 | Window model | Multi-window array. At least one required. Each: period + limit + optional burst. |
| B3 | Burst scope | Per-window. Customer-controlled. Does not propagate across windows. |
| B4 | Algorithm | Fixed-window for v1. Sliding window deferred. |
| B5 | Config change | Instant effect. No grace period. |
| B6 | Warm-up | No warm-up. Accepted behavior. Restart loses local counters. |
| C1 | Multi-step counting | Default: all steps run and count. `fail_fast: true` to stop early. |
| C1a | Multi-window counting | All windows always increment. First exceeded fires response. |
| C2 | Check ordering | Customer-controlled via step placement. UI template as guidance. |
| C3 | Same slot, different configs | Valid. Independent counters per config ID. |
| C4 | Pre-auth rate limit | TenantID=0 = shared anonymous bucket. Allowed. Customer-driven. |
| C5 | Unauthenticated requests | No implicit fallback. Explicit configuration only. |
| D1 | Retry counting | Each retry counts when config set. No config = no check. |
| D2 | Multiple upstream calls | Natural — two steps, two calls. No design change. |
| D3 | Unknown upstream URL | Fail-open by default. Catch-all pattern for strict control. |
| D4 | URL pattern ambiguity | Longest match wins. Consistent with router. |
| D5 | Upstream failure + tenant quota | No rollback. Customer controls via step ordering. |
| E1 | Multiplier → sub-1 | Floor to 1 with log warning. Explicit blocking uses blocked tier. |
| E2 | Multiplier on blocked config | Zero × anything = zero. Multiplier cannot unblock. |
| E3 | Exception block vs tier | Explicit block always wins. Most specific takes precedence. |
| E4 | Tier change mid-flight | Instant effect. Downgrade may cause immediate rate limiting. |
| E5 | Multiple multipliers | Multiplicative composition. Subject to floor-to-1 rule. |
| E6 | Multiplier precision | Percentage in UI. Float internally. Range: 0.01 to any positive. |
| F1 | Distributed counters | Approximate ÷ instance count. Strict = Redis shared. |
| F2 | Internal/system calls | Identifier variable → high-limit config. No bypass mechanism. |
| F3 | Rate limit headers | Fully customer-configured. Only failing step emits headers. Defaults provided. |
| F4 | Counter isolation | Per-config isolated arenas. Tenant = direct index. IP/slot = isolated hash. |

---

## 11. What Needs to Be Built

### Gateway (Go) — New or Changed

| Component | Change | Priority |
|-----------|--------|---------|
| `RateLimitConfig` type | Multi-window array, enforcement mode, output config, `on_empty_key` | High |
| Counter store | Per-config isolated arenas, tenant direct indexing, IP/slot hash arenas | High |
| `CheckRateLimit` step | Multi-window loop, multiplier application, `fail_fast` flag, header emit control | High |
| `CheckRateLimitSlot` step | Composite key support (multi-slot concat), case normalization | High |
| Tier definition type | `allowed_apis`, `blocked_apis`, overall quota config | High |
| `ResolveRateLimit` | Read tenant multiplier, apply flat × variable multiplier, floor-to-1 | High |
| `InstanceSync` | Add instance count read to heartbeat loop, expose `InstanceCount()` | Medium |
| Upstream URL registry | URL pattern table, longest-match lookup, unmatched policy | Medium |
| `CheckUpstreamRateLimit` step | URL hash from slot, isolated arena, pattern registry lookup | Medium |
| `BindClientIP` step | XFF index selection, write to named slot | Medium |
| Collision rate metric | Emit per IP/slot-based arena, alert threshold | Medium |
| `CheckRateLimitGlobal` step | Counter key without tenant ID (global tenant-agnostic) | Low |

### Studio UI — New or Changed

| Component | Change |
|-----------|--------|
| Rate Limit Configs screen | Multi-window editor, enforcement mode, output config |
| Tenant Tiers screen | Tier definition with windows, allowed APIs, multiplier |
| Upstream Services screen | URL pattern registry, upstream rate limit configs |
| Tenant detail | Tier assignment, flat multiplier field |
| API/Endpoint panel | Two independent RL sections (tenant + upstream) |
| Flow template | Recommended step order shown on new flow creation |

---

## 12. Deferred Items

| Item | Reason deferred |
|------|----------------|
| Sliding window / token bucket algorithms | Fixed-window covers v1 needs. Design separately. |
| A5 — Step ordering compiler validation | Broader than rate limiting. Separate session. |
| Per-tenant per-API override matrix | Very few tenants need this. Tier + exception block covers 99%. |
| Warm-up / cold start protection | Solved by mode selection (approximate vs strict). |
| Rate limit by upstream URL (as tenant billing) | Novel concept. Design separately after upstream RL is live. |
| Global (tenant-agnostic) rate limiting UI | `CheckRateLimitGlobal` step is low priority. |
