# Rate Limit Implementation — Session Plan

## Reading Guide

- **Phase**: sessions in the same phase can run in parallel
- **Model**: Haiku (simple, single-file) | Sonnet (multi-file, complex logic)
- **Depends on**: must be completed before this session starts
- **Parallel with**: other sessions in the same phase
- Each session references `plan_rate_limit_design.md` for full design details

---

## Dependency Graph

```
Phase 1 ─┬─ S1 (Go Types)    ──────────────────────────────────────────────────┐
          └─ S2 (TS Types)    ──────────────────────────────────────────────────┤
                                                                                │
Phase 2 ──┼─ S3  Counter Store Redesign     (needs S1)                         │
          ├─ S4  InstanceSync Count          (needs S1)                         │
          ├─ S5  Upstream URL Registry       (needs S1)                         │
          ├─ S6  BindClientIP Step           (needs S1)                         │
          ├─ S7  Rate Limit Configs UI       (needs S2)                         │
          ├─ S8  Tenant Tiers UI             (needs S2)                         │
          ├─ S9  Upstream Services UI        (needs S2)                         │
          └─ S10 Tenant Detail UI Update     (needs S2)                         │
                                                                                │
Phase 3 ──┼─ S11 Rate Limit Steps (multi-window, multiplier)  (needs S3,S4)    │
          ├─ S12 Tier System / ResolveRateLimit               (needs S3)        │
          ├─ S13 CheckUpstreamRateLimit Step                  (needs S3,S5)     │
          └─ S14 API/Endpoint Panel UI                        (needs S7,S8,S9)  │
                                                                                │
Phase 4 ──┼─ S15 Compiler Updates            (needs S11,S12,S13,S6)            │
          └─ S16 Sync API TS Updates         (needs S14)                        │
                                                                                │
Phase 5 ───── S17 Management Server          (needs S15,S16)  ─────────────────┘
```

---

## Phase 1 — Foundational Types (run S1 and S2 in parallel)

---

### S1 — Go Types: All New Structs and Interfaces
**Model**: Sonnet | **Phase**: 1 | **Parallel with**: S2

**Why Sonnet**: Multiple files, new type hierarchy, must be coherent across the codebase.
Everything in Phase 2+ depends on these types being correct.

**Files to change**:
- `internal/engine/rate_limit.go` — `RateLimitWindow`, `RateLimitConfig` (multi-window),
  `TierDef`, `UpstreamServiceDef`, multiplier types, `OnEmptyKey` enum
- `internal/config/config_types.go` — `InstanceConfig` additions (heartbeat interval)
- `internal/registry/types.go` — `TierDef` with `AllowedAPIs`, `BlockedAPIs`, `OverallConfig`
- `internal/control/types.go` — update `ApiUpdate` / `EndpointConfig` with new RL fields

**New types to define**:
```go
type RateLimitWindow struct {
    Period      string  // "1s", "5m", "1h", "1d"
    PeriodSecs  uint32  // computed from Period
    Limit       uint32  // 0 = blocked, absent config = unlimited
    BurstFactor uint32  // percent, 0 = no burst (treat as 100)
}

type OnEmptyKey uint8
const (
    OnEmptyKeyFail     OnEmptyKey = 0  // default
    OnEmptyKeySkip     OnEmptyKey = 1
    OnEmptyKeyTenant   OnEmptyKey = 2
)

type RateLimitConfig struct {
    Name            string
    Enforcement     string           // "approximate" | "strict"
    RedisUnavail    string           // "fail_open" | "fail_closed"
    Windows         []RateLimitWindow
    ExceededStatus  int
    ExceededBody    string
    ExceededCType   string
    EmitHeaders     bool
    HeaderNames     RateLimitHeaderNames
    CaseSensitive   bool
    OnEmptyKey      OnEmptyKey
}

type TierDef struct {
    Name          string
    ConfigName    string   // rate limit config for API/endpoint level
    OverallName   string   // rate limit config for cross-API overall quota
    AllowedAPIs   []string // nil = all allowed
    BlockedAPIs   []string
}

type UpstreamPattern struct {
    Pattern    string   // e.g. "https://api.openai.com/*"
    ConfigName string
}

type UpstreamServiceDef struct {
    Name       string
    Patterns   []UpstreamPattern
    Unmatched  string  // "fail_open" | "fail_closed" | "default_config"
    DefaultCfg string  // used when unmatched = "default_config"
}
```

**Also define API contracts** (REST shapes for new endpoints the UI will call):
- `GET /api/rate-limit-configs` → `RateLimitListResponse` (already exists, extend)
- `POST /api/rate-limit-configs` → accepts new `RateLimitConfig` shape
- `GET /api/tiers` → `TierListResponse`
- `POST /api/tiers` → accepts `TierDef`
- `GET /api/upstream-services` → `UpstreamServiceListResponse`
- `POST /api/upstream-services` → accepts `UpstreamServiceDef`

Document these shapes in a comment block at the top of `rate_limit.go`.

**Acceptance criteria**:
- `go build ./...` passes with no errors
- All new types exported and documented
- API contract shapes documented in code comments

---

### S2 — TypeScript Types: All New Interfaces
**Model**: Haiku | **Phase**: 1 | **Parallel with**: S1

**Why Haiku**: Single file, pure type additions, no logic.

**File to change**: `internal/studio/ui/src/types.ts`

**New types to add**:
```typescript
export interface RateLimitWindow {
  period: string          // "1s" | "5m" | "1h" | "1d"
  limit: number           // 0 = blocked
  burst_factor?: number   // percentage, e.g. 150 = 150%
}

export type OnEmptyKey = 'fail' | 'skip' | 'fallback:tenant'
export type Enforcement = 'approximate' | 'strict'
export type RedisUnavailable = 'fail_open' | 'fail_closed'

export interface RateLimitHeaderNames {
  limit?: string
  remaining?: string
  reset?: string
  retry?: string
}

export interface RateLimitConfig {
  name: string
  enforcement: Enforcement
  redis_unavailable?: RedisUnavailable
  windows: RateLimitWindow[]
  exceeded_status?: number
  exceeded_body?: string
  exceeded_content_type?: string
  emit_headers?: boolean
  header_names?: RateLimitHeaderNames
  case_sensitive?: boolean
  on_empty_key?: OnEmptyKey
}

export interface TierDef {
  name: string
  config_name?: string    // rate limit config for API/endpoint level
  overall_name?: string   // cross-API overall quota config
  allowed_apis?: string[] // undefined = all allowed
  blocked_apis?: string[]
}

export interface UpstreamPattern {
  pattern: string
  config_name: string
}

export interface UpstreamServiceDef {
  name: string
  patterns: UpstreamPattern[]
  unmatched?: 'fail_open' | 'fail_closed' | 'default_config'
  default_config?: string
}

// Update existing ApiDef and EndpointDef:
// Replace rateLimitName, rateLimitVar with:
export interface RateLimitCountBy {
  kind: 'tenant' | 'ip' | 'slot' | 'static' | 'composite'
  slot_name?: string           // for kind='slot'
  static_value?: string        // for kind='static'
  composite_slots?: string[]   // for kind='composite'
  xff_index?: number           // for kind='ip', default 0
  on_empty_key?: OnEmptyKey
  fail_fast?: boolean
}

export interface RateLimitConfigRef {
  kind: 'named' | 'dynamic'
  name?: string                // for kind='named'
  source?: 'registry' | 'cache' | 'header' | 'queryparam'
  key?: string                 // for kind='dynamic'
}
```

**Update existing types**:
- `ApiDef`: replace `rateLimitName?`, `rateLimitVar?` with `rateLimitCountBy?`, `rateLimitConfigRef?`
- `EndpointDef`: same replacement
- Keep `RateLimitRecord` and `RateLimitListResponse` — extend `RateLimitRecord` to use new `RateLimitConfig`

**Acceptance criteria**:
- `tsc --noEmit` passes with no errors
- No existing type usages broken (check imports across components)

---

## Phase 2 — Core Infrastructure (all can run in parallel after Phase 1)

---

### S3 — Counter Store Redesign
**Model**: Sonnet | **Phase**: 2 | **Depends on**: S1 | **Parallel with**: S4,S5,S6,S7,S8,S9,S10

**Why Sonnet**: Significant architecture change. Complex memory layout.
Most critical correctness requirement of the whole design.

**Files to change**:
- `internal/engine/rate_limit.go` — rewrite `CounterStore`
- New file: `internal/engine/counter_arena.go` — per-config arena types

**Part A — Tenant direct-index arena (zero collisions)**:
```go
// TenantCounterArena: direct TenantID index, zero hash collisions.
// One arena per rate limit config. TenantID (uint16) is the direct index.
type TenantCounterArena struct {
    // slots[tenantID * numWindows + windowIdx] = (epoch uint32, count uint32)
    slots     []uint64   // packed: high32=epoch, low32=count
    numWindows int
}
// Access: O(1), no hash, no collision
func (a *TenantCounterArena) Increment(tenantID uint16, windowIdx int, epoch uint32, limit uint32) (bool, uint32)
```

**Part B — IP/slot isolated hash arena**:
```go
// SlotCounterArena: FNV-1a hash into isolated per-config arena.
// Collisions only between different keys within same config.
type SlotCounterArena struct {
    slots    []uint64   // same packing as above
    size     uint32
}
// Access: O(1) amortized, isolated collisions only
func (a *SlotCounterArena) Increment(keyBytes []byte, windowIdx int, epoch uint32, limit uint32) (bool, uint32)
func (a *SlotCounterArena) CollisionRate(activeKeys uint64) float64
```

**Per-config registry**:
```go
// ConfigCounterRegistry holds one arena per registered config.
// Created at bake time, swapped atomically on config update.
type ConfigCounterRegistry struct {
    tenantArenas map[uint16]*TenantCounterArena  // configID → arena
    slotArenas   map[uint16]*SlotCounterArena    // configID → arena
}
```

**Acceptance criteria**:
- `go build ./...` passes
- Unit test: two configs never share a counter slot
- Unit test: direct tenant index for tenantID 0 and 65535 work correctly
- Unit test: collision rate metric returns plausible value

---

### S4 — InstanceSync: Instance Count Exposure
**Model**: Haiku | **Phase**: 2 | **Depends on**: S1 | **Parallel with**: S3,S5,S6,S7,S8,S9,S10

**Why Haiku**: Single file addition to an existing well-understood component.

**File to change**: `internal/control/instance_sync.go`

**What to add**:
```go
// Add to InstanceSync struct:
instanceCount atomic.Int32  // cached, updated every heartbeat

// In writeHeartbeat(), after writing own record:
func (s *InstanceSync) refreshInstanceCount(ctx context.Context) {
    keys, err := s.dsm.ListGlobalKeys(ctx, config.DomainGatewayInstances, s.cfg.EnvironmentID+"/")
    if err != nil { return }
    now := time.Now().Unix()
    alive := 0
    ttl := int64(s.cfg.HeartbeatIntervalS * 3)  // 3× heartbeat = TTL
    for _, k := range keys {
        // each record has LastHeartbeat — count only non-stale
        data, ok, err := s.dsm.GetGlobal(ctx, config.DomainGatewayInstances, k)
        if err != nil || !ok { continue }
        var rec InstanceRecord
        if json.Unmarshal(data, &rec) == nil && (now - rec.LastHeartbeat) < ttl {
            alive++
        }
    }
    if alive > 0 { s.instanceCount.Store(int32(alive)) }
}

// Expose:
func (s *InstanceSync) InstanceCount() int {
    if n := s.instanceCount.Load(); n > 0 { return int(n) }
    return 1  // safe default: assume single instance
}
```

**Acceptance criteria**:
- `go build ./...` passes
- `InstanceCount()` returns 1 when no datastore configured (safe default)
- `InstanceCount()` returns correct count when multiple instances registered

---

### S5 — Upstream URL Registry
**Model**: Sonnet | **Phase**: 2 | **Depends on**: S1 | **Parallel with**: S3,S4,S6,S7,S8,S9,S10

**Why Sonnet**: New subsystem. Pattern matching logic. Atomic snapshot pattern.

**New file**: `internal/engine/upstream_registry.go`

**What to build**:
```go
// UpstreamRegistry maps URL patterns to config IDs.
// Patterns are matched longest-first (most specific wins).
// Immutable snapshot — swapped atomically on update.
type UpstreamRegistry struct {
    patterns []compiledPattern  // sorted by specificity descending
    unmatched UnmatchedPolicy
    defaultID uint16
}

type compiledPattern struct {
    prefix   string   // e.g. "https://api.openai.com/v1/"
    exact    bool     // true if pattern ends with no wildcard
    configID uint16
}

// Match returns (configID, found). Longest match wins.
func (r *UpstreamRegistry) Match(url string) (uint16, bool)

// Global atomic snapshot — same pattern as router and registry
var UpstreamRegistryState struct {
    Active atomic.Pointer[UpstreamRegistry]
}
```

**Hash function for URL bytes** (reuse existing FNV-1a from rate_limit_step.go):
- URL → `hash([]byte(url))` → used as key in SlotCounterArena

**Acceptance criteria**:
- `go build ./...` passes
- Unit test: longer pattern wins over shorter (`/v1/*` beats `/*`)
- Unit test: unmatched policy respected (fail_open returns false, fail_closed returns true)
- Unit test: exact match beats wildcard

---

### S6 — BindClientIP Step
**Model**: Haiku | **Phase**: 2 | **Depends on**: S1 | **Parallel with**: S3,S4,S5,S7,S8,S9,S10

**Why Haiku**: New step, well-defined, small, single file plus a compiler case.

**New file**: `internal/engine/steps/bind_ip.go`

```go
// BindClientIP extracts the client IP from X-Forwarded-For or RemoteAddr,
// writes raw IP bytes (no port) into the named slot.
// xffIndex: which comma-separated XFF entry to use (0 = first/leftmost = client)
func BindClientIP(slot int, xffIndex int) engine.Instruction
```

Logic:
1. Read `X-Forwarded-For` header from ctx
2. Split by comma, trim spaces
3. Use entry at `xffIndex` (clamp to valid range)
4. If XFF absent: parse `RemoteAddr` (strip port)
5. Parse IP string → net.IP bytes → write to `ctx.ByteSlots[slot]`

**Also update**:
- `internal/control/compiler.go` — add `case "bind_client_ip"`
- `internal/control/step_descriptors.go` — palette entry with xff_index field

**Acceptance criteria**:
- `go build ./...` passes
- Unit test: XFF `"1.2.3.4, 5.6.7.8"` with index 0 → IP bytes for 1.2.3.4
- Unit test: XFF absent → RemoteAddr used
- Unit test: xffIndex out of range → last entry used (clamp)

---

### S7 — Rate Limit Configs UI Screen
**Model**: Sonnet | **Phase**: 2 | **Depends on**: S2 | **Parallel with**: S3,S4,S5,S6,S8,S9,S10

**Why Sonnet**: Large new component with multi-window editor, validation, API integration.

**New file**: `internal/studio/ui/src/components/RateLimitConfigsScreen.tsx`

**Also update**: `internal/studio/ui/src/api.ts` — add:
```typescript
export function listRateLimitConfigsFull(): Promise<RateLimitConfig[]>
export function upsertRateLimitConfigFull(cfg: RateLimitConfig): Promise<void>
export function deleteRateLimitConfig(name: string): Promise<void>
```

**UI structure**:
```
Left sidebar: list of configs (name + enforcement badge)

Right panel (selected config):
  Name, Enforcement toggle (Approximate / Strict)
  Redis unavailable (fail_open / fail_closed) — shown only for Strict
  
  Windows table:
    Period | Limit | Burst% | [remove]
    [+ Add Window]
    Period picker: 1s | 30s | 1m | 5m | 15m | 30m | 1h | 6h | 1d | custom
    
  Output section:
    Status code input (default 429)
    Body textarea with template variable hints ({retry_after}, {limit}, {window})
    Content-Type input
    Emit headers toggle
    Header name overrides (collapsible)
    
  Advanced section (collapsible):
    Case sensitive toggle
    On empty key: Fail / Skip / Fallback to tenant
```

**Acceptance criteria**:
- Can create, edit, delete a config with multiple windows
- Validation: at least one window required, limit must be >= 0, period must be valid
- Period > 60s shows Redis note ("stored in Redis for persistence")
- Round-trip: create config → reload page → config displays correctly
- TypeScript build passes

---

### S8 — Tenant Tiers UI Screen
**Model**: Sonnet | **Phase**: 2 | **Depends on**: S2 | **Parallel with**: S3,S4,S5,S6,S7,S9,S10

**Why Sonnet**: New screen, API integration, entitlement management.

**New file**: `internal/studio/ui/src/components/TenantTiersScreen.tsx`

**Add to `api.ts`**:
```typescript
export function listTiers(): Promise<TierDef[]>
export function upsertTier(tier: TierDef): Promise<void>
export function deleteTier(name: string): Promise<void>
```

**UI structure**:
```
Left sidebar: tier list (name + badge: unlimited/blocked/limited)
  Special tiers shown first: unlimited, blocked
  [+ New Tier]

Right panel (selected tier):
  Name input (read-only for built-in tiers)
  
  Rate Limit Config section:
    API/Endpoint config: [dropdown of rate limit configs] | none
    Overall (cross-API) config: [dropdown] | none
    
  Entitlement section:
    API Access: ( All APIs ) ( Allowlist ) ( Blocklist )
    If allowlist/blocklist: searchable API name chips
    
  [Save] [Delete]

Bottom panel: Tenant Assignments
  Search tenants, assign tier to selected tenant
  Shows current tier for each tenant
```

**Acceptance criteria**:
- Can create/edit/delete custom tiers
- Built-in tiers (unlimited, blocked) are shown but name is read-only
- Allowlist/blocklist is searchable with autocomplete from known API names
- Tenant assignment works (writes `id:tier` property to tenant registry)
- TypeScript build passes

---

### S9 — Upstream Services UI Screen
**Model**: Sonnet | **Phase**: 2 | **Depends on**: S2 | **Parallel with**: S3,S4,S5,S6,S7,S8,S10

**Why Sonnet**: New screen, URL pattern management, pattern testing.

**New file**: `internal/studio/ui/src/components/UpstreamServicesScreen.tsx`

**Add to `api.ts`**:
```typescript
export function listUpstreamServices(): Promise<UpstreamServiceDef[]>
export function upsertUpstreamService(svc: UpstreamServiceDef): Promise<void>
export function deleteUpstreamService(name: string): Promise<void>
```

**UI structure**:
```
Left sidebar: service list (name + pattern count)
  [+ New Service]

Right panel:
  Name input
  
  URL Patterns table:
    Pattern | Config | [remove]
    [+ Add Pattern]
    Pattern input with hint: "https://api.example.com/*"
    Config dropdown (from rate limit configs list)
    
  Unmatched URL policy:
    ( Fail open - allow without rate limiting )
    ( Fail closed - deny request )
    ( Apply default config ) → [dropdown]
    
  Pattern tester (collapsible):
    URL input → [Test] → shows which pattern matches and which config applies
    
  [Save]
```

**Acceptance criteria**:
- Can create/edit/delete upstream service definitions
- Pattern tester correctly shows longest-match winner
- Patterns sorted by specificity in the display
- TypeScript build passes

---

### S10 — Tenant Detail UI Update
**Model**: Haiku | **Phase**: 2 | **Depends on**: S2 | **Parallel with**: S3,S4,S5,S6,S7,S8,S9

**Why Haiku**: Small additions to existing `Tenants.tsx`.

**File to change**: `internal/studio/ui/src/components/Tenants.tsx`

**What to add**:
```
In tenant detail panel, add new section "Rate Limit":
  Tier: [dropdown of available tiers] ← reads from listTiers()
  Flat multiplier: [number input] % (empty = no multiplier, i.e. 100%)
  
  Display current effective tier with badge color:
    unlimited → green
    blocked → red
    custom → blue
    standard tiers → grey
```

**Acceptance criteria**:
- Tier dropdown populated from tiers API
- Saving tier writes `id:tier` property and multiplier to tenant registry
- Existing tenant detail functionality unchanged
- TypeScript build passes

---

## Phase 3 — Rate Limit Logic (run S11, S12, S13, S14 in parallel)

---

### S11 — Rate Limit Steps: Multi-Window, Multiplier, Fail-Fast
**Model**: Sonnet | **Phase**: 3 | **Depends on**: S3, S4 | **Parallel with**: S12, S13, S14

**Why Sonnet**: Core rate limiting logic rewrite. Multiple interconnected changes.

**File to change**: `internal/engine/steps/rate_limit_step.go`

**Changes**:

1. **Multi-window loop**: replace fixed per_sec/per_min with window iteration:
```go
for i, w := range config.Windows {
    epoch := uint32(now) / w.PeriodSecs
    effective := w.Limit
    if w.BurstFactor > 0 && w.BurstFactor != 100 {
        effective = uint32(uint64(w.Limit) * uint64(w.BurstFactor) / 100)
    }
    if effective == 0 && w.Limit > 0 { effective = 1 }  // floor-to-1

    ok, rem := arena.Increment(tenantID, i, epoch, effective)
    if emitHeaders && !ok { /* emit headers for this window */ }
    if !ok { ctx.ResponseStatus = config.ExceededStatus; return -1 }
}
```

2. **Multiplier application**:
```go
// Read flat multiplier from tenant meta
// Read variable multiplier from __rl_multiplier slot
// Compose multiplicatively: effective = base × flat × variable
// floor(max(1, effective)) after composition
// Exception: if base == 0, skip multiplier (blocked config stays blocked)
```

3. **fail_fast flag**: baked into instruction at compile time.
   Default `fail_fast = false`: return new signal "rejected-continue" (not -1) when exceeded.
   With `fail_fast = true`: return -1 immediately.
   Engine interprets "rejected-continue" as: set rejection state, continue executing
   subsequent steps IF they are also rate limit steps, stop at first non-RL step.

4. **on_empty_key handling**: when slot is empty, apply `OnEmptyKey` config.

5. **Header emission**: only the failing step emits headers. Passing steps emit nothing.

6. **0 = blocked fix**: when config is assigned and all windows have limit=0, deny immediately
   (not pass-through as current code does).

**Acceptance criteria**:
- `go build ./...` passes
- Unit test: multi-window all pass → allowed
- Unit test: second window fails → denied, both windows incremented
- Unit test: multiplier 0.5 on limit 100 → effective 50
- Unit test: multiplier 0.05 on limit 10 → effective 1 (floor)
- Unit test: limit 0 → blocked (not pass-through)
- Unit test: fail_fast=false → both steps count even when first fails

---

### S12 — Tier System: Entitlement and ResolveRateLimit
**Model**: Sonnet | **Phase**: 3 | **Depends on**: S3 | **Parallel with**: S11, S13, S14

**Why Sonnet**: Registry changes, multi-file, entitlement resolution logic.

**Files to change**:
- `internal/registry/registry_engine.go` — update `ResolveRateLimit`
- `internal/registry/registry_manager.go` — tier CRUD operations
- `internal/registry/types.go` — tier storage in registry

**Changes**:

1. **Tier storage**: tiers stored in registry alongside tenant properties.
   Tenant's tier: `id:tier = "premium"` (existing property mechanism).
   Tier definitions: stored in a new `TierRegistry` (name → TierDef, atomically swapped).

2. **Entitlement check** (new function):
```go
// CheckEntitlement returns (allowed bool, reason string)
// Checks: explicit per-tenant block > tier blocked_apis > tier allowed_apis
func CheckEntitlement(reg *RegistryState, tenantID uint16, apiName string) (bool, string)
```

3. **ResolveRateLimit update** — add multiplier application:
```go
// After resolving base config from tier:
flatMul := readTenantMultiplier(reg, tenantID)  // from meta:rl_multiplier
varMul  := readSlotMultiplier(ctx)               // from __rl_multiplier slot
effective = applyMultipliers(base, flatMul, varMul)
// floor-to-1 after composition (unless base == 0)
```

4. **Overall quota config**: tier's `OverallName` resolves to a config ID at bake time.
   Stored as a new field on the baked route entry.

**Acceptance criteria**:
- `go build ./...` passes
- Unit test: blocked tier → `CheckEntitlement` returns false
- Unit test: tier with `blocked_apis: ["ai_chat"]` → blocked for that API
- Unit test: explicit per-tenant block overrides tier allowance
- Unit test: multiplier 0.5 on limit 100 → 50; limit 0 unchanged
- Unit test: multiplicative composition (0.5 × 0.1 = 0.05 → floor to 1 on limit 10)

---

### S13 — CheckUpstreamRateLimit Step
**Model**: Sonnet | **Phase**: 3 | **Depends on**: S3, S5 | **Parallel with**: S11, S12, S14

**Why Sonnet**: New step, integrates counter store + upstream registry, multiple modes.

**New file**: `internal/engine/steps/rate_limit_upstream.go`

```go
// CheckUpstreamRateLimit enforces rate limiting against the upstream backend.
// urlSlot: ByteSlots index holding the upstream URL (populated by load_service_url
//          or bind_header upstream_url). -1 = use static hash baked at compile time.
// staticHash: FNV-1a hash of the static upstream URL (computed at bake time, 0 if dynamic).
func CheckUpstreamRateLimit(
    registry  *UpstreamRegistry,
    arenas    *ConfigCounterRegistry,
    urlSlot   int,
    staticHash uint32,
    emitHeaders bool,
) engine.Instruction
```

Logic:
1. Get URL hash: if staticHash != 0 → use it. Else → hash `ctx.ByteSlots[urlSlot]`.
2. Look up config ID from `UpstreamRegistry.Match(urlBytes)`
3. If not found: apply unmatched policy (fail_open = pass, fail_closed = deny)
4. Get `SlotCounterArena` for this config ID from `arenas`
5. For each window in config: `arena.Increment(urlHashBytes, windowIdx, epoch, limit)`
6. First exceeded window: set `ctx.ResponseStatus = config.ExceededStatus`, return -1
7. Emit headers only if configured AND this step fails

**Also update compiler** (preliminary):
- Add `case "check_upstream_rate_limit"` to compiler (full compiler session is S15,
  but add this case stub now so the step can be tested independently)

**Acceptance criteria**:
- `go build ./...` passes
- Unit test: static URL hash → correct config resolved and counter incremented
- Unit test: dynamic URL from slot → hash computed at runtime, correct config
- Unit test: unmatched URL → fail_open passes through, fail_closed denies
- Unit test: two different configs never share a counter slot

---

### S14 — API/Endpoint Panel UI: Two RL Sections
**Model**: Sonnet | **Phase**: 3 | **Depends on**: S7, S8, S9 | **Parallel with**: S11, S12, S13

**Why Sonnet**: Large existing component (`APIsSection.tsx`), significant refactor,
touches types, state, sync logic.

**File to change**: `internal/studio/ui/src/components/APIsSection.tsx`

**Changes**:

1. **Replace `RateLimitEditor`** with two independent sections:

```
Section 1 — Tenant Rate Limiting:
  Count by: [Tenant ▾ | IP | Slot variable | Static key | Composite]
    → IP: shows XFF index input
    → Slot: shows variable name input
    → Static: shows key value input
    → Composite: shows multi-slot list
  On empty key: [Fail ▾ | Skip | Fallback to tenant]  (shown when non-tenant)
  Fail fast: [toggle]
  
  Config: [rate limit config dropdown ▾] | [+ Create inline]
    or: Dynamic source [registry/cache/header/queryparam] + key input

  Levels: [x] Overall  [x] API  [x] Endpoint

Section 2 — Upstream Rate Limiting:
  Service: [upstream service dropdown ▾] | none
  Levels: [ ] Global  [x] API  [ ] Endpoint
```

2. **Update `syncThisApi()`**: translate new `RateLimitCountBy` + `RateLimitConfigRef`
   into the SyncPayload fields. Static key → add `__rl_key` to constants map.

3. **Update gateway snapshot loading**: map old `rate_limit` / `rate_limit_var` fields
   to new types for backwards compatibility.

4. **Upstream URL static → constants**: carry forward Session 2 translation
   (static upstream URL → `upstream_url` constant).

**Acceptance criteria**:
- Existing APIs loaded from gateway snapshot display correctly
- Both RL sections save and restore state correctly
- `syncThisApi()` sends correct payload for each Count by option
- Composite key: multiple slot names concatenated in payload
- TypeScript build passes

---

## Phase 4 — Integration (run S15 and S16 in parallel)

---

### S15 — Compiler Updates: All New Step Cases
**Model**: Sonnet | **Phase**: 4 | **Depends on**: S11, S12, S13, S6 | **Parallel with**: S16

**Why Sonnet**: Many new cases, cross-cutting, must be coherent. Largest compiler change.

**Files to change**:
- `internal/control/compiler.go` — all new step cases
- `internal/control/step_descriptors.go` — new palette entries

**New compiler cases**:
```
bind_client_ip          → BindClientIP(slot, xffIndex)
check_rate_limit        → updated CheckRateLimit (multi-window, fail_fast, on_empty_key)
check_rate_limit_ip     → CheckRateLimitSlot using IP slot (via BindClientIP)
check_rate_limit_slot   → CheckRateLimitSlot with composite key support
check_upstream_rate_limit → CheckUpstreamRateLimit (static hash or dynamic slot)
```

**Bake-time work**:
- Rate limit config name → config ID → window array baked into instruction
- Upstream URL (static) → FNV-1a hash computed once at bake time
- Tier's overall config name → overall config ID baked into route
- Multiplier slot name `__rl_multiplier` → slot index resolved at compile time

**Acceptance criteria**:
- `go build ./...` passes
- All new actions compile without errors
- Existing flows continue to compile (no regressions)
- `go test -skip=. ./internal/control/...` passes (existing tests)

---

### S16 — Sync API TypeScript Updates
**Model**: Haiku | **Phase**: 4 | **Depends on**: S14 | **Parallel with**: S15

**Why Haiku**: Extending existing `SyncPayload` and `api.ts` with new fields.

**Files to change**:
- `internal/studio/ui/src/api.ts` — extend `SyncPayload`

**What to add**:
```typescript
// In SyncPayload apis array entries:
tier?: string                            // tier name for this API
rate_limit_count_by?: RateLimitCountBy   // new count-by config
rate_limit_config_ref?: RateLimitConfigRef
upstream_service?: string                // upstream service name

// In endpoint_configs entries: same fields as above
```

**Also**: add new API functions for tiers and upstream services (stubs for now if
backend not yet complete — can use mock responses):
```typescript
export function listTiers(): Promise<TierDef[]>
export function upsertTier(tier: TierDef): Promise<void>
export function listUpstreamServices(): Promise<UpstreamServiceDef[]>
export function upsertUpstreamService(svc: UpstreamServiceDef): Promise<void>
```

**Acceptance criteria**:
- `tsc --noEmit` passes
- `SyncPayload` backwards compatible (all new fields optional)

---

## Phase 5 — Final Integration

---

### S17 — Management Server: Wire Everything Together
**Model**: Sonnet | **Phase**: 5 | **Depends on**: S15, S16

**Why Sonnet**: Most complex session. Touches many moving parts. Must wire all
new types through the sync pipeline end-to-end.

**Files to change**:
- `internal/control/management_server.go` — handle all new types in `ApplyUnifiedSync`
- `internal/control/management_server.go` — new REST handlers for tiers, upstream services

**What to wire**:

1. **New REST endpoints**:
```go
POST /tiers              → upsert TierDef → update TierRegistry atomically
GET  /tiers              → list all TierDef
DELETE /tiers/{name}     → remove tier
POST /upstream-services  → upsert UpstreamServiceDef → update UpstreamRegistry
GET  /upstream-services  → list all
DELETE /upstream-services/{name}
```

2. **ApplyUnifiedSync updates**:
- Resolve tier name → tier def → rate limit config IDs at bake time
- Resolve upstream service name → upstream pattern registry at bake time
- Bake overall quota config ID into route definition
- Bake entitlement (allowed/blocked API sets) into route

3. **Studio proxy**: add new routes to `studio/server.go` proxy pass-through
   so UI can call management server through studio's `/api/*` prefix.

4. **Bootstrap**: ensure new types are persisted and restored on restart
   (same `DataStoreManager` pattern as flows and APIs).

**Acceptance criteria**:
- `go build ./...` passes
- End-to-end: create tier via UI → assign to tenant → send request → rate limit applied
- End-to-end: create upstream service → assign to API → upstream rate limit enforced
- Restart gateway → tier and upstream service definitions restored from datastore
- `go test -skip=. ./internal/control/...` passes

---

## Session Summary Table

| Session | Description | Model | Phase | Depends on | Files |
|---------|------------|-------|-------|------------|-------|
| S1 | Go Types | Sonnet | 1 | — | 4 files |
| S2 | TS Types | Haiku | 1 | — | 1 file |
| S3 | Counter Store Redesign | Sonnet | 2 | S1 | 2 files |
| S4 | InstanceSync Count | Haiku | 2 | S1 | 1 file |
| S5 | Upstream URL Registry | Sonnet | 2 | S1 | 1 new file |
| S6 | BindClientIP Step | Haiku | 2 | S1 | 3 files |
| S7 | Rate Limit Configs UI | Sonnet | 2 | S2 | 2 files |
| S8 | Tenant Tiers UI | Sonnet | 2 | S2 | 2 files |
| S9 | Upstream Services UI | Sonnet | 2 | S2 | 2 files |
| S10 | Tenant Detail UI Update | Haiku | 2 | S2 | 1 file |
| S11 | Rate Limit Steps Rewrite | Sonnet | 3 | S3,S4 | 1 file |
| S12 | Tier System / ResolveRateLimit | Sonnet | 3 | S3 | 3 files |
| S13 | CheckUpstreamRateLimit Step | Sonnet | 3 | S3,S5 | 1 new file |
| S14 | API/Endpoint Panel UI | Sonnet | 3 | S7,S8,S9 | 1 file |
| S15 | Compiler Updates | Sonnet | 4 | S11,S12,S13,S6 | 2 files |
| S16 | Sync API TS Updates | Haiku | 4 | S14 | 1 file |
| S17 | Management Server | Sonnet | 5 | S15,S16 | 2 files |

**Haiku sessions**: S2, S4, S6, S10, S16 (5 sessions — simple, single-file, well-defined)
**Sonnet sessions**: S1, S3, S5, S7, S8, S9, S11, S12, S13, S14, S15, S17 (12 sessions)

---

## Parallel Execution by Phase

```
Phase 1:  [S1] ║ [S2]

Phase 2:  [S3] ║ [S4] ║ [S5] ║ [S6] ║ [S7] ║ [S8] ║ [S9] ║ [S10]
          (Go track)              (UI track)
          S3,S4,S5,S6 can run    S7,S8,S9,S10 can run
          fully in parallel       fully in parallel
          with each other         with each other
          AND with S7-S10         AND with S3-S6

Phase 3:  [S11] ║ [S12] ║ [S13] ║ [S14]

Phase 4:  [S15] ║ [S16]

Phase 5:  [S17]
```

Maximum parallelism in Phase 2: **8 sessions simultaneously**.
Each touches different files — no merge conflicts.

---

## Pre-Session Checklist

Before each session:
1. `git status` — confirm clean working tree
2. Read this file to identify current session
3. Read `plan_rate_limit_design.md` Section referenced in the session
4. For Go sessions: `go build ./...` must pass before starting
5. For TS sessions: `tsc --noEmit` must pass before starting
6. After session: mark `[done]` in this file, run build check

---

## Risk Notes

| Risk | Mitigation |
|------|-----------|
| S3 (counter store) is the most critical — wrong design breaks accuracy | Write unit tests before any integration |
| S11 (rate limit step rewrite) changes existing behavior (0=blocked fix) | Feature-flag or parallel code path until S17 completes |
| S14 (APIsSection refactor) is the largest UI change | Read full file before starting, test round-trip with gateway snapshot |
| S17 (management server) integrates everything — most likely to find gaps | Run all unit tests, then do manual end-to-end before marking done |
