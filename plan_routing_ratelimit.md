# Route Constants · Rate Limit Scopes · IP Restriction Fix
## Implementation Plan

### Design Constraints
- **Gateway hot path**: zero allocations, zero GC pressure. All slot writes at request time must
  be pre-indexed array assignments — no map lookups, no string keys, no conversions.
  All heavy work (key resolution, slot allocation, value encoding) happens at bake time.
- **UI — 4 views**:
  - **Code view** — driven by `dsl_parse.ts` + `dsl_serialize.ts`. Every new step needs
    both a parser case and a serializer case so round-trips are lossless.
  - **Visual/Flow view** — driven by `step_descriptors.go`. New steps appear automatically
    once a descriptor is added. Fields array controls the form rendered per block.
  - **Tree view** (FlowMap.tsx) — reads only `flow_name`, `then`, `else`, `cases` fields.
    Not affected by any step that doesn't reference other flows.
  - **Graph view** (FlowGraph.tsx) — same as Tree. Uses `buildCallGraph` from FlowMap.tsx.
    Not affected unless a new step introduces flow references.
- **Only touch files where a change is required.**
- **Each session: verify correctness before marking done.**

---

## Current State (verified by code reading)

### What exists and works
| Feature | Status |
|---------|--------|
| `check_rate_limit` step — compiler, DSL, descriptor | ✓ present |
| Rate limit `<select>` at API level — APIsSection.tsx:759 | ✓ present |
| Rate limit `<select>` at endpoint level — APIsSection.tsx:957 | ✓ present |
| `rate_limit` included in sync payload — APIsSection.tsx:365,371 | ✓ present |
| `ip_restriction` — compiler, Go step, DSL parse+serialize | ✓ present |

### What is broken
| Bug | Root Cause |
|-----|-----------|
| Rate limit dropdown appears empty, no guidance | `listRateLimitConfigs()` failure is silent (`.catch(() => {})`); no hint to create configs in Tenants tab |
| IP restriction loses `source` field on round-trip | `dsl_serialize.ts:282` doesn't emit `source`; `dsl_parse.ts` doesn't read it back |
| IP restriction loses `key_identifier` on round-trip | Serializer never emits `ip:` param; parser never reads it |

### What is missing
| Feature | Gap |
|---------|-----|
| Route-level constants | No mechanism anywhere — not in router, executor, types, UI, or deploy payload |
| `registry.url_var` (runtime key lookup) | `registry.url("key")` is compile-time only; no dynamic variant |
| IP-scoped rate limiting | `RateLimitRule` bit spec has scope field, but `CheckRateLimit` always uses TenantID in counter hash |
| Slot-scoped rate limiting | Same — scope field exists in spec, not implemented in step |

### Counter is always per-tenant
`rateLimitIndex` (rate_limit_step.go:217) always XORs `tenantID` into the hash.
Even API-level or endpoint-level configs produce per-tenant isolated counters.
Sessions 7–8 add optional IP and slot scopes as an alternative counter key.

### Tree and Graph views
`FlowMap.buildCallGraph` reads only `flow_name`, `then`, `else`, `cases` from each step.
None of the steps added in this plan carry flow references, so Tree and Graph views
require no changes in any session below.

---

## Session 1 — Fix IP Restriction Round-Trip (UI only)

**Why**: Every save/load loses `source` (IP resolution method) and `key_identifier`
(pre-resolved IP slot). Source silently resets to `X-Forwarded-For`.

**Files touched**: `dsl_serialize.ts`, `dsl_parse.ts`

**Views affected**: Code view only (parse + serialize). Visual/Flow view unaffected
(step descriptor unchanged). Tree/Graph unaffected (no flow references).

### dsl_serialize.ts — replace `ip_restriction` case (currently lines 276–283)
```ts
case 'ip_restriction': {
  let cfg: Record<string, string> = {}
  try { cfg = JSON.parse(str(step['input'])) } catch { /**/ }
  const mode  = cfg['mode'] ?? 'allow'
  const cidrs = cfg['cidrs'] ?? ''
  const fn    = mode === 'allow' ? 'ip_allow' : 'ip_deny'
  const parts: string[] = cidrs.split(',').map(c => `"${c.trim()}"`)
  const extras: string[] = []
  if (cfg['on_violation_status'] && cfg['on_violation_status'] !== '403')
    extras.push(`on_violation: ${cfg['on_violation_status']}`)
  if (cfg['source'] && cfg['source'] !== 'header.X-Forwarded-For')
    extras.push(`source: "${cfg['source']}"`)
  const ki = str(step['key_identifier'])
  if (ki) extras.push(`ip: ${ki}`)
  lines.push(`${I}${fn}(${[...parts, ...extras].join(', ')})`); break
}
```

### dsl_parse.ts — replace `ip_allow`/`ip_deny` case (currently lines 291–299)
```ts
case 'ip_allow':
case 'ip_deny': {
  const mode   = action === 'ip_allow' ? 'allow' : 'deny'
  const pos    = positional(10).filter(v => v.includes('.') || v.includes(':') || v.includes('/'))
  const cidrs  = pos.join(',') || p['cidrs'] || ''
  const status = p['on_violation'] ?? '403'
  const src    = p['source'] ?? 'header.X-Forwarded-For'
  const cfg    = JSON.stringify({ mode, cidrs, source: src,
                   on_violation_status: status, on_violation_body: 'ip not allowed' })
  const ipField = p['ip'] ? { key_identifier: p['ip'] } : {}
  return [mk({ action: 'ip_restriction', input: cfg, ...ipField })]
}
```

### Verification
1. Enter in Code view: `ip_allow("10.0.0.0/8", source: "header.X-Real-IP")`
2. Switch to Visual view — block shows. Switch back to Code — source must still be `X-Real-IP`.
3. Enter: `client_ip = client_ip()` then `ip_allow("10.0.0.0/8", ip: client_ip)`
4. Round-trip — `ip: client_ip` must still be present.
5. Enter deliberately bad CIDR `ip_allow("")` — gateway deploy should return compile error.

---

## Session 2 — Rate Limit UI Feedback (UI only)

**Why**: Dropdown shows empty with no explanation when no configs exist or gateway is down.

**Files touched**: `APIsSection.tsx` only

**Views affected**: APIs section UI only. No flow views affected.

### Changes

Add `rateLimitError` state (boolean) alongside `rateLimitConfigs`:
```ts
const [rateLimitError, setRateLimitError] = useState(false)
```

In the existing `useEffect` that calls `listRateLimitConfigs()`:
```ts
listRateLimitConfigs()
  .then(r => { setRateLimitConfigs(r.items.map(c => c.name)); setRateLimitError(false) })
  .catch(() => setRateLimitError(true))
```

Pass `rateLimitError` as a new prop to `ApiDetailPanel` and `EndpointDetailPanel`
(add it alongside the existing `rateLimitConfigs` prop in both interfaces and call sites).

In both panels, directly below the `<select>` element, add:
```tsx
{rateLimitError && (
  <p style={{ fontSize: 11, color: '#f59e0b', marginTop: 4 }}>
    Could not load configs — is the gateway running?
  </p>
)}
{!rateLimitError && rateLimitConfigs.length === 0 && (
  <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4 }}>
    No rate limit configs yet — create one in the Tenants tab.
  </p>
)}
```

### Verification
1. Stop gateway → open APIs section → warning "Could not load configs" appears under dropdown.
2. Start gateway with no rate limit configs → "No rate limit configs yet" appears.
3. Create a rate limit config in Tenants tab → reload APIs section → config name appears in dropdown.
4. Select a config → sync API → gateway state shows `rate_limit` field.

---

## Session 3 — Route Constants: Types + Payload (types + one function)

**Why**: Groundwork. No UI changes yet. Establishes the data model before UI and backend.

**Files touched**: `types.ts`, `APIsSection.tsx` (`syncThisApi` function only)

### types.ts — add `constants` to both interfaces
```ts
export interface EndpointDef {
  id: string
  subPath: string
  method: string
  flowName?: string
  rateLimitName?: string
  constants?: Record<string, string>   // NEW: pre-loaded named slots for this endpoint
}

export interface ApiDef {
  id: string
  name: string
  basePath: string
  aliasPaths?: string[]
  defaultFlow: string
  endpoints: EndpointDef[]
  rateLimitName?: string
  constants?: Record<string, string>   // NEW: pre-loaded named slots (endpoint overrides api)
}
```

### APIsSection.tsx — `syncThisApi` only: include constants in payload
In the `apisPayload` construction, add constants to both api level and each endpoint:
```ts
...(api.constants && Object.keys(api.constants).length ? { constants: api.constants } : {}),
// inside endpoint_configs map:
...(ep.constants && Object.keys(ep.constants).length ? { constants: ep.constants } : {}),
```

Also in `fetchGatewaySnapshot` hydration, read `constants` back if present:
```ts
...(ec.constants ? { constants: ec.constants as Record<string,string> } : {}),
// and for api level:
...(ga.constants ? { constants: ga.constants as Record<string,string> } : {}),
```

### Verification
Run `npx tsc --noEmit` from `internal/studio/ui/` — zero type errors.
No runtime change yet (constants field is undefined everywhere, payload unchanged).

---

## Session 4 — Route Constants: UI Editor

**Why**: Users need to add/edit/remove per-API and per-endpoint key-value constants.

**Files touched**: `APIsSection.tsx` only

**Views affected**: APIs section only. No flow views.

### Add `ConstantsEditor` component inside APIsSection.tsx
Place it before `ApiDetailPanel` in the file:
```tsx
function ConstantsEditor({
  constants,
  onChange,
}: {
  constants: Record<string, string>
  onChange: (c: Record<string, string>) => void
}) {
  const [newKey, setNewKey] = useState('')
  const [newVal, setNewVal] = useState('')
  const entries = Object.entries(constants)

  return (
    <div>
      {entries.map(([k, v]) => (
        <div key={k} style={{ display: 'flex', gap: 8, marginBottom: 4, alignItems: 'center' }}>
          <code style={{ fontSize: 11, minWidth: 100, color: 'var(--accent)' }}>{k}</code>
          <span style={{ fontSize: 11, color: 'var(--muted)' }}>=</span>
          <input
            className="input"
            style={{ flex: 1, padding: '2px 8px', fontSize: 12 }}
            value={v}
            onChange={e => onChange({ ...constants, [k]: e.target.value })}
          />
          <button
            className="btn muted"
            style={{ padding: '2px 8px', fontSize: 11, marginTop: 0 }}
            onClick={() => { const c = { ...constants }; delete c[k]; onChange(c) }}
          >×</button>
        </div>
      ))}
      <div style={{ display: 'flex', gap: 6, marginTop: 6 }}>
        <input
          className="input"
          placeholder="name"
          style={{ width: 100, padding: '2px 8px', fontSize: 12 }}
          value={newKey}
          onChange={e => setNewKey(e.target.value)}
        />
        <input
          className="input"
          placeholder="value"
          style={{ flex: 1, padding: '2px 8px', fontSize: 12 }}
          value={newVal}
          onChange={e => setNewVal(e.target.value)}
        />
        <button
          className="btn muted"
          style={{ padding: '2px 8px', fontSize: 11, marginTop: 0 }}
          onClick={() => {
            if (!newKey.trim()) return
            onChange({ ...constants, [newKey.trim()]: newVal.trim() })
            setNewKey(''); setNewVal('')
          }}
        >+ Add</button>
      </div>
    </div>
  )
}
```

### Add handlers in main component
```ts
function handleUpdateApiConstants(apiId: string, c: Record<string, string>) {
  setApis(apis.map(a => a.id === apiId ? { ...a, constants: c } : a))
}
function handleUpdateEndpointConstants(apiId: string, epId: string, c: Record<string, string>) {
  setApis(apis.map(a => a.id !== apiId ? a : {
    ...a,
    endpoints: a.endpoints.map(e => e.id === epId ? { ...e, constants: c } : e),
  }))
}
```

### Wire into panels
Pass `constants={api.constants ?? {}}` and `onUpdateConstants={...}` props to both
`ApiDetailPanel` and `EndpointDetailPanel`. Add `<Section label="Route Constants">` in
each panel with `<ConstantsEditor>`. Add the new props to both interface definitions.

### Verification
1. Add constant `url_key = payments_url` on an API — appears in list immediately.
2. Edit value — updates in place.
3. Remove — gone.
4. Add same on endpoint — shows separately.
5. Sync to gateway — inspect payload in browser network tab: both `api.constants`
   and `endpoint_configs[].constants` present in JSON body.

---

## Session 5 — Route Constants: Go Backend (zero-alloc hot path)

**Why**: Constants must be injected into ByteSlots before flow execution with zero
allocation at request time. All slot indices resolved at bake time.

**Files touched**:
- `internal/studio/server.go` (parse constants from sync payload)
- `internal/control/types.go` (add Constants to API/endpoint step config if not present)
- `internal/engine/manager.go` (store baked constant slot assignments)
- `internal/engine/executor.go` (inject constants into slots at dispatch)

**Do NOT touch**: router package (lock-free hot path stays unchanged).

### Design — zero-alloc injection

At **bake time** (sync handler), for each API/endpoint with constants:
1. Call `compiler.AllocConstantSlots(constants map[string]string) []ConstantSlot`
   which returns `[]ConstantSlot{SlotIdx int; Value []byte}` — pre-allocated byte slices.
2. Store this slice in `FlowManager` keyed by `(apiID, endpointKey string)`.

At **request time** (executor dispatch, before first instruction):
```go
for _, c := range constants {
    ctx.ByteSlots[c.SlotIdx] = c.Value   // direct array write — zero alloc, ~2ns each
}
```

### internal/control/types.go
Add `Constants map[string]string` to whichever struct holds per-API compile config
(check file first — add only if not present).

### internal/studio/server.go
In the sync handler, after parsing each api entry, read `constants` JSON field into
`map[string]string`. Pass it to the compiler/manager for baking.

### internal/engine/manager.go
```go
// ConstantSlot is a pre-baked slot assignment for a route constant.
// Resolved once at deploy time; applied at each request with zero allocation.
type ConstantSlot struct {
    SlotIdx int
    Value   []byte  // pre-allocated at bake time
}

// Add to FlowManager:
RouteConstants map[string][]ConstantSlot  // key: "apiID:endpointKey" or "apiID:"
```

Constants from endpoint override same-named constants from API. Merge at bake time:
1. Start with API constants.
2. Overlay endpoint constants.
3. Allocate slot for each key via `fm.Compiler.AllocSlot(key)`.
4. Store `[]ConstantSlot` in `RouteConstants`.

### internal/engine/executor.go
After route match resolves `ctx.APIRateLimitId` and `ctx.EndpointRateLimitId` (which
already happens before flow dispatch), inject constants:
```go
routeKey := buildRouteKey(ctx.APIId, ctx.EndpointKey)
if slots, ok := fm.RouteConstants[routeKey]; ok {
    for _, c := range slots {
        ctx.ByteSlots[c.SlotIdx] = c.Value
    }
}
```
`buildRouteKey` uses a pre-formatted string — or better, use a `uint64` packed key
`(uint32(apiID) << 32) | uint32(endpointIDHash)` to avoid string allocation.

### Verification
1. Deploy API with `constants: {"url_key": "payments_url"}`.
2. Add `log("url_key_log", url_key)` step to the flow.
3. Make a request — observability shows `url_key_log = payments_url`.
4. Run `go test -bench=. ./internal/engine/` — no new allocations vs baseline.
5. Check `go build ./cmd/rah-gateway/` compiles cleanly.

---

## Session 6 — `registry.url_var` Step (all 4 views + Go backend)

**Why**: Enables flows to look up a service URL by a runtime key name (from route
constant or any slot). Needed to make a single `call_upstream` flow reusable across
APIs with different URL keys.

**Files touched**:
- `internal/registry/registry_engine.go` (add `GetURLByKeyName`)
- `internal/engine/steps/registry.go` (add `LoadServiceURLVar`)
- `internal/control/compiler.go` (add `load_service_url_var` case)
- `internal/control/step_descriptors.go` (add descriptor → Visual/Flow view)
- `internal/studio/ui/src/utils/dsl_parse.ts` (add `registry.url_var` → Code view)
- `internal/studio/ui/src/utils/dsl_serialize.ts` (add serializer case → Code view)

**Tree/Graph views**: unaffected — `load_service_url_var` carries no flow references.

### Gateway runtime — zero-copy key read

In `LoadServiceURLVar`, the key name comes from `ctx.ByteSlots[keySlot]` which is
`[]byte`. Convert to string without allocation using the same pattern as `RegistryLookup`:
```go
keyName := *(*string)(unsafe.Pointer(&ctx.ByteSlots[keySlot]))
```
The radix walk (`reg.URLs.Keys.Lookup(keyName)`) is ~50–100 ns, zero alloc (reads
immutable snapshot). Acceptable for a config lookup that runs once per request.

### registry_engine.go — add after `GetURLByKeyID`
```go
// GetURLByKeyName resolves a service URL by key name at runtime.
// Uses a radix tree walk (~50–100 ns) instead of the 2–5 ns matrix read.
// Only use when the key name is not known at compile time (e.g. route constants).
func GetURLByKeyName(reg *TenantRegistry, tenantID uint16, keyName string) ([]byte, bool) {
    if reg == nil {
        return nil, false
    }
    raw, found := reg.URLs.Keys.Lookup(keyName)
    if !found {
        return nil, false
    }
    return GetURLByKeyID(tenantID, uint16(raw))
}
```

### engine/steps/registry.go — add after `LoadServiceURL`
```go
// LoadServiceURLVar loads a service URL using a runtime key name from keySlot.
// The key name (e.g. "payments_url") must already be in ctx.ByteSlots[keySlot].
// Hot-path cost: ~50–100 ns (radix walk on immutable snapshot). Zero allocations.
// Use only when the key is not known at compile time. Prefer LoadServiceURL (~2–5 ns)
// when the key is static.
func LoadServiceURLVar(keySlot int, destSlot int) engine.Instruction {
    return engine.Instruction{
        Name: "LOAD_SERVICE_URL_VAR",
        Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
            if keySlot >= len(ctx.ByteSlots) || len(ctx.ByteSlots[keySlot]) == 0 {
                return s.PC + 1
            }
            // Zero-copy string view — no heap allocation.
            keyName := *(*string)(unsafe.Pointer(&ctx.ByteSlots[keySlot]))
            reg := registry.State.Active.Load()
            if val, ok := registry.GetURLByKeyName(reg, ctx.TenantID, keyName); ok {
                ctx.ByteSlots[destSlot] = val
            }
            return s.PC + 1
        },
    }
}
```

### compiler.go — add case after `"load_service_url"`
```go
case "load_service_url_var":
    keySlot, err := c.getSlot(step.KeyIdentifier)
    if err != nil {
        return err
    }
    destSlot, err := c.getSlot(step.As)
    if err != nil {
        return err
    }
    c.GlobalTable = append(c.GlobalTable, steps.LoadServiceURLVar(keySlot, destSlot))
```

### step_descriptors.go — add descriptor after `load_service_url` entry
```go
{
    Type:        "load_service_url_var",
    Title:       "Load Service URL (Dynamic Key)",
    Category:    "registry",
    Capability:  "service-url",
    Description: "Load a service URL using a key name from a slot (e.g. set via a route constant or earlier step). ~50–100 ns vs 2–5 ns for static Load Service URL. Use only when the key differs per API.",
    Defaults:    map[string]string{"key_identifier": "url_key", "as": "upstream_url"},
    Fields: []StepField{
        sf("key_identifier", "Key name slot", "Variable holding the URL key name at runtime (e.g. 'payments_url')", "url_key"),
        sf("as", "Store as", "Variable to save the resolved URL into", "upstream_url"),
    },
},
```

### dsl_parse.ts — add case after `registry.url`
```ts
case 'registry.url_var':
    return [mk({ action: 'load_service_url_var',
                 key_identifier: unquote(positional(1)[0] ?? ''), ...withAs })]
```

### dsl_serialize.ts — add case after `load_service_url`
```ts
case 'load_service_url_var':
    lines.push(`${I}${as_ ? as_+' = ' : ''}registry.url_var(${str(step['key_identifier'])})`); break
```

### Verification
1. **Code view**: Type `upstream_url = registry.url_var(url_key)` — parses without error.
   Switch to Visual view — block "Load Service URL (Dynamic Key)" appears with correct fields.
   Switch back to Code — `upstream_url = registry.url_var(url_key)` still present (lossless).
2. **Tree/Graph view**: Open either view — no new nodes appear (expected; no flow references).
3. **Go**: `go build ./cmd/rah-gateway/` passes. `go vet ./...` clean.
4. **Runtime**: Deploy flow with `upstream_url = registry.url_var(url_key)` + route constant
   `url_key = primary`. Make request — correct upstream called. Check no new allocs in bench.

---

## Session 7 — IP-Scoped Rate Limiting

**Why**: All current rate limiting is per-tenant. Need a way to limit by source IP
regardless of which tenant alias was used.

**Files touched**:
- `internal/engine/steps/rate_limit_step.go` (add IP scope branch)
- `internal/control/compiler.go` (read `scope` + `ip_slot` from step input)
- `internal/studio/ui/src/utils/dsl_parse.ts` (extend `rate_limit` case)
- `internal/studio/ui/src/utils/dsl_serialize.ts` (extend `check_rate_limit` case)

**step_descriptors.go**: no change needed — the `check_rate_limit` descriptor's
`input` field already accepts JSON and the description covers optional params.

**Tree/Graph views**: unaffected.

### Gateway runtime — hash IP bytes without allocation

Do NOT convert IP bytes to string. Hash the raw `[]byte` directly:
```go
func ipHash(ip []byte) uint64 {
    var h uint64 = 14695981039346656037
    for _, b := range ip {
        h ^= uint64(b)
        h *= 1099511628211
    }
    return h
}
```
This is ~5 ns for an IPv4 address, zero alloc.

### rate_limit_step.go — add IPScoped variant

Add a new exported function `CheckRateLimitIP` alongside `CheckRateLimit`.
Do NOT modify `CheckRateLimit` — it must remain unchanged for existing flows.

```go
// CheckRateLimitIP enforces a rate limit keyed by source IP rather than TenantID.
// ipSlot: ByteSlots index holding the client IP (set by bind_client_ip).
// The IP bytes are hashed directly — no string conversion, no allocation.
// Returns 429 when the limit is exceeded.
func CheckRateLimitIP(store *engine.CounterStore, ipSlot int, syncPolicy uint8,
    remoteRL engine.ExternalRateLimitProvider, emitQuotaHeaders bool) engine.Instruction {
    return engine.Instruction{
        Name: "CHECK_RATE_LIMIT_IP",
        Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
            // Resolve rate limit config from API/endpoint (same as tenant path).
            reg := registry.State.Active.Load()
            resolved, blocked := registry.ResolveRateLimit(reg, ctx.CallerID,
                ctx.TenantID, ctx.APIRateLimitId, ctx.EndpointRateLimitId)
            if blocked {
                ctx.ResponseStatus = 403
                return -1
            }
            if resolved.PerSec == 0 && resolved.PerMin == 0 {
                return s.PC + 1
            }

            // Hash source IP bytes — zero alloc.
            var ipBytes []byte
            if ipSlot >= 0 && ipSlot < len(ctx.ByteSlots) {
                ipBytes = ctx.ByteSlots[ipSlot]
            }
            h := ipHash32(ipBytes)  // returns uint32

            now := uint32(time.Now().Unix())

            if resolved.PerSec > 0 {
                bf := resolved.BurstFactor
                if bf == 0 { bf = 100 }
                effectiveSec := uint32(uint64(resolved.PerSec) * uint64(bf) / 100)
                if effectiveSec == 0 { effectiveSec = 1 }
                idx := rateLimitIndexIP(store, h, ctx.APIRateLimitId, now, 0)
                ok, _ := store.FixedWindowEpoch(idx, now, effectiveSec)
                if !ok {
                    ctx.ResponseStatus = 429
                    return -1
                }
            }
            if resolved.PerMin > 0 {
                epochMin := now / 60
                idx := rateLimitIndexIP(store, h, ctx.APIRateLimitId, epochMin, 1)
                ok, _ := store.FixedWindowEpoch(idx, epochMin, resolved.PerMin)
                if !ok {
                    ctx.ResponseStatus = 429
                    return -1
                }
            }
            return s.PC + 1
        },
    }
}

func ipHash32(ip []byte) uint32 {
    h := uint32(2166136261)
    for _, b := range ip {
        h ^= uint32(b)
        h *= 16777619
    }
    return h
}

func rateLimitIndexIP(store *engine.CounterStore, ipHash, apiRLId uint32, epoch uint32, windowType uint8) uint32 {
    h := uint64(ipHash)*2654435761 ^
        uint64(apiRLId)*2246822519 ^
        uint64(epoch)*1000003 ^
        uint64(windowType)*2166136261
    return uint32(h) % uint32(len(store.Arena))
}
```

### compiler.go — extend `check_rate_limit` case

After existing logic, read `scope` from `step.Input`:
```go
case "check_rate_limit":
    // existing quota group logic ...
    scope := step.Input["scope"]
    if scope == "ip" {
        ipSlotName := strings.TrimSpace(step.Input["ip_slot"])
        ipSlot := -1
        if ipSlotName != "" {
            ipSlot, _ = c.getSlot(ipSlotName)
        }
        c.GlobalTable = append(c.GlobalTable, steps.CheckRateLimitIP(
            c.fm.RateLimitStore, ipSlot, syncPolicy, c.fm.RemoteRL, emitQuotaHeaders))
    } else {
        // existing path unchanged
        c.GlobalTable = append(c.GlobalTable, steps.CheckRateLimit(...))
    }
```

### dsl_parse.ts — extend `rate_limit` case
```ts
case 'rate_limit': {
  const scope  = p['scope'] ?? ''
  const ipSlot = p['ip'] ?? ''
  const groups = p['groups'] ?? ''
  if (scope === 'ip') {
    const input = JSON.stringify({ scope: 'ip', ip_slot: ipSlot })
    return [mk({ action: 'check_rate_limit', input })]
  }
  if (groups) return [mk({ action: 'check_rate_limit', input: groups })]
  return [mk({ action: 'check_rate_limit' })]
}
```

### dsl_serialize.ts — extend `check_rate_limit` case
```ts
case 'check_rate_limit': {
  const inputStr = str(step['input'])
  let cfg: Record<string, string> = {}
  try { cfg = JSON.parse(inputStr) } catch { /**/ }
  if (cfg['scope'] === 'ip') {
    const ipPart = cfg['ip_slot'] ? `, ip: ${cfg['ip_slot']}` : ''
    lines.push(`${I}rate_limit(scope: ip${ipPart})`)
  } else if (inputStr && !cfg['scope']) {
    lines.push(`${I}rate_limit(groups: ${q(inputStr)})`)
  } else {
    lines.push(`${I}rate_limit()`)
  }
  break
}
```

### Verification
1. **Code view**: `client_ip = client_ip()` then `rate_limit(scope: ip, ip: client_ip)`.
   Round-trip — both lines preserved exactly.
2. **Visual view**: Block shows as "Check Rate Limit" (descriptor unchanged). Input field
   holds the JSON. No regression on existing `rate_limit()` rendering.
3. **Go compile**: `go build ./cmd/rah-gateway/` passes. `go vet ./...` clean.
4. **Runtime test**: Two tenant aliases, same client IP, limit 2/sec.
   Send 3 requests — third returns 429 regardless of tenant alias used.
5. **Alloc test**: `go test -run=^$ -bench=BenchmarkCheckRateLimitIP -benchmem ./internal/engine/steps/`
   — `0 allocs/op`.

---

## Session 8 — Slot-Scoped Rate Limiting

**Why**: Limit by any runtime value (user ID, client ID, device ID) — not tenant, not IP.

**Files touched**: `rate_limit_step.go`, `compiler.go`, `dsl_parse.ts`, `dsl_serialize.ts`

**Pattern**: Same as Session 7 — add `CheckRateLimitSlot`, extend compiler case and DSL.

The key difference from IP scope: the slot value is an arbitrary string, not an IP.
Hash the raw bytes of `ctx.ByteSlots[scopeSlot]` — same `ipHash32` function works for
any byte slice. Rename it `bytesHash32` in a follow-up or reuse as-is.

### DSL
```
user_id = header("X-User-ID")
rate_limit(scope: slot, key: user_id)
```

### Parser addition (inside `rate_limit` case, after `scope === 'ip'` branch):
```ts
if (scope === 'slot') {
  const keySlot = p['key'] ?? ''
  const input = JSON.stringify({ scope: 'slot', key_slot: keySlot })
  return [mk({ action: 'check_rate_limit', input })]
}
```

### Serializer addition (inside `check_rate_limit` case, after `scope === 'ip'` branch):
```ts
if (cfg['scope'] === 'slot') {
  const keyPart = cfg['key_slot'] ? `, key: ${cfg['key_slot']}` : ''
  lines.push(`${I}rate_limit(scope: slot${keyPart})`)
}
```

### Verification
1. **Code view**: `user_id = header("X-User-ID")` + `rate_limit(scope: slot, key: user_id)` — round-trip clean.
2. **Go compile + vet** clean.
3. **Runtime**: Header `X-User-ID: alice`, limit 2/sec. Three requests for alice across two
   different tenant aliases → third is 429. Requests for `X-User-ID: bob` unaffected.
4. **Alloc test**: 0 allocs/op.

---

## Execution Order

```
Session 1 ─── IP restriction fix (UI only)        ← safe to do first, independent
Session 2 ─── Rate limit UI feedback (UI only)    ← independent
Session 3 ─── Route constants types               ← must precede 4, 5, 6
Session 4 ─── Route constants UI editor           ← needs 3
Session 5 ─── Route constants Go backend          ← needs 3; can run in parallel with 4
Session 6 ─── registry.url_var                    ← needs 5
Session 7 ─── IP-scoped rate limit                ← independent of sessions 1–6
Session 8 ─── Slot-scoped rate limit              ← needs 7
```

Sessions 1 and 2 are fully independent and can be done in any order.
Sessions 7 and 8 are independent of sessions 3–6 but 8 requires 7.
