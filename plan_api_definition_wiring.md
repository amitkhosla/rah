# API Definition Wiring — Session Plan

## Status Key
- `[done]` — completed in a prior session
- `[todo]` — not yet started
- `[skip]` — deferred / out of scope

## Context (read before each session)

### What already works end-to-end
- **Pre-set variables / constants**: `ApiDef.constants` → `SyncPayload.apis[].constants`
  → `ApiUpdate.constants` → `AllocConstantSlots` → `RouteConstants` → `ctx.ByteSlots` injection at request time.
  Customer names variables; compiler assigns slot numbers. Slot numbers never reach the UI.
- **Rate limit (named)**: `rateLimitName` → `SyncPayload.apis[].rate_limit` → `ApiUpdate.RateLimitName`
  → `GetRateLimitConfigId` → `ctx.APIRateLimitId / ctx.EndpointRateLimitId` at bake time.
  `CheckRateLimit` step uses these IDs at request time.

### What is silently dropped (exists in UI/SyncPayload but NOT in Go ApiUpdate)
- `upstream_url: {source, value}` — present in `SyncPayload` but Go `ApiUpdate` has no such field.
  The gateway JSON-decodes the sync body and silently ignores unknown fields.
- `rate_limit_var: {source, key}` — same situation. UI stores it, Go ignores it.

### Key files
| File | Role |
|------|------|
| `internal/studio/ui/src/types.ts` | `ApiDef`, `EndpointDef`, `UpstreamUrlConfig`, `RateLimitVar` |
| `internal/studio/ui/src/components/APIsSection.tsx` | All UI + `syncThisApi()` |
| `internal/studio/ui/src/api.ts` | `SyncPayload`, `syncFlows()` |
| `internal/control/types.go` | Go `ApiUpdate`, `EndpointConfig` |
| `internal/control/management_server.go` | `ApplyUnifiedSync`, `AllocConstantSlots` |
| `internal/control/compiler.go` | Slot assignment, instruction compilation |
| `internal/engine/steps/rate_limit_step.go` | `CheckRateLimit`, `CheckRateLimitIP`, `CheckRateLimitSlot` |

---

## Session 1 — Pre-set Variables UX Polish [todo]

**Scope**: UI only. No Go changes. No gateway contract changes.

**What to fix in `APIsSection.tsx`**:
1. `ConstantsEditor`: add validation — empty key → show inline error, don't add the row.
2. `ConstantsEditor`: add validation — duplicate key → inline warning ("overrides existing key `X`").
3. `ConstantsEditor`: remove the existing placeholder row or "long *" display issue the user noticed.
   - In the `ConstantsEditor`, check if the asterisk comes from a default `value` entry with key `""`.
   - Ensure initial state is an empty map `{}`, not a map with a blank key.
4. Add a tooltip or help line: "These values are injected before the flow runs. Reference them in
   flow steps using the key name (e.g., `as: "service_code"`)."
5. Confirm: endpoint-level constants section says "Overrides API-level values for the same key."

**Files changed**: `APIsSection.tsx` only.

**Acceptance criteria**:
- Adding a constant with a blank key shows an error, does not insert a row.
- Duplicate key shows a warning.
- Existing constants from a gateway snapshot round-trip correctly (load → display → sync back unchanged).
- No slot numbers appear anywhere in the constants UI.

**Session complexity**: Small. ~30 lines changed. No risk.

---

## Session 2 — Upstream URL: Static Source → Constants Translation [todo]

**Scope**: UI only (`APIsSection.tsx`). No Go changes.

**Design**: Static upstream URL (`source: 'static', value: 'https://...'`) is stored in
`api.upstreamUrl` but silently ignored by Go. The fastest zero-gateway-change path: translate it
into a constant entry `upstream_url = "https://..."` inside `syncThisApi()` before building
the SyncPayload. The compiler will then inject it into `ctx.ByteSlots` at request time.
The flow's `http_call` step must reference `url_var: "upstream_url"` to consume it.

**What to change in `syncThisApi()` in `APIsSection.tsx`**:

```typescript
// For API-level upstream_url
let apiConstants = { ...(api.constants ?? {}) }
if (api.upstreamUrl?.source === 'static' && api.upstreamUrl.value) {
  apiConstants['upstream_url'] = api.upstreamUrl.value
}

// For endpoint-level upstream_url
let epConstants = { ...(ep.constants ?? {}) }
if (ep.upstreamUrl?.source === 'static' && ep.upstreamUrl.value) {
  epConstants['upstream_url'] = ep.upstreamUrl.value
}
```

**Also**: Add a help note in `UpstreamUrlEditor` for `source === 'static'`:
> "Stored as a pre-set variable named `upstream_url`. In your flow's `http_call` step, set
> `url_var: upstream_url` to use it."

**Files changed**: `APIsSection.tsx` only.

**Acceptance criteria**:
- Set source=Static, value=`https://api.example.com`. Click Sync. Gateway receives
  `constants: {"upstream_url": "https://api.example.com"}` (verify via gateway logs or snapshot).
- If a constant named `upstream_url` already exists manually in the constants editor, the static
  upstream_url value **overwrites** it (last-wins, static source wins).
- Non-static sources: no constants entry added (handled in Session 3).

**Session complexity**: Small. ~20 lines in `syncThisApi()` + help text. No risk.

---

## Session 3 — Upstream URL: Dynamic Sources → Prepend Flow Steps [todo]

**Scope**: UI only (`APIsSection.tsx`). No Go changes.

**Design**: For non-static upstream URL sources, studio prepends a step to each affected flow
before syncing. The prepended step reads the URL from the selected source into slot `upstream_url`.
This MUST NOT mutate the saved flow in local state — only the payload sent to the gateway.

**Source → prepended step mapping**:
| source | Prepended step |
|--------|---------------|
| `registry` | `{action: "load_service_url", key: "<value>", as: "upstream_url"}` |
| `cache`    | `{action: "cache_get", key: "<value>", as: "upstream_url"}` |
| `header`   | `{action: "bind_header", key: "<value>", as: "upstream_url"}` |
| `queryparam` | `{action: "bind_query_param", key: "<value>", as: "upstream_url"}` |

**What to change in `syncThisApi()`**:

```typescript
function buildUpstreamPrependStep(cfg: UpstreamUrlConfig): SyncStep | null {
  if (!cfg.value || cfg.source === 'static') return null
  const actionMap: Record<string, string> = {
    registry:   'load_service_url',
    cache:      'cache_get',
    header:     'bind_header',
    queryparam: 'bind_query_param',
  }
  const action = actionMap[cfg.source]
  if (!action) return null
  return { action, key: cfg.value, as: 'upstream_url' }
}
```

When building `flowsPayload`, check: does any API or endpoint in this sync have a dynamic
upstream_url pointing at a given flow name? If yes, prepend the step to that flow's instructions
in the payload only (do not call `setApis` / `onLoadFlow`).

**Scoping rule**: Endpoint-level `upstreamUrl` takes precedence over API-level `upstreamUrl`
for the flow used by that endpoint. If both exist, use the endpoint-level one.

**Files changed**: `APIsSection.tsx` only.

**Acceptance criteria**:
- Set source=Header, key=`X-Backend-URL`. Sync. The flow payload sent to gateway has
  `{action: "bind_header", key: "X-Backend-URL", as: "upstream_url"}` as the first instruction.
- The saved flow (in local state, visible in Flow Designer) does NOT show the prepended step.
- Syncing twice in a row produces the same gateway state (idempotent).
- Static source: no prepend step, only constants entry (Session 2 behavior).

**Session complexity**: Medium. Requires careful flow payload construction logic. ~50 lines. Low risk (payload-only transform).

---

## Session 4 — Rate Limit UI Redesign: Two Separate Dimensions [todo]

**Scope**: UI only (`types.ts`, `APIsSection.tsx`). No Go changes.

**Problem with current UI**: A single `RateLimitEditor` conflates two orthogonal choices:
- **Dimension A — WHAT to count** (the rate limit key / who is being limited):
  tenant (default), IP, or any runtime value (slot variable)
- **Dimension B — WHICH config to apply** (limits: per_sec, per_min, burst):
  a named config, or a config name read dynamically from registry/cache/header/queryparam

Currently, "dynamic source" in the UI addresses Dimension B only. Dimension A (count by IP, etc.)
has no UI at all — it is only expressible by adding `check_rate_limit_ip` step in the flow designer.

**New UI design for `RateLimitEditor`**:

```
┌─ Rate Limiting ─────────────────────────────────────────────────────┐
│  Count by:  [ Per tenant ▾ ]    ← Dimension A                       │
│  Config:    [ standard      ▾ ] [ + Create ]  ← Dimension B (named) │
└─────────────────────────────────────────────────────────────────────┘
```

**Dimension A — "Count by" options**:
| Label | Action injected | Notes |
|-------|----------------|-------|
| Per tenant (default) | `check_rate_limit` | No extra setup |
| Per IP | `bind_client_ip` + `check_rate_limit_ip` | Needs XFF index field (see Session 5) |
| Per variable | `check_rate_limit_slot` | Shows a text field "Variable name" |
| None (inherit) | — | For endpoint-level: inherit from API |

**Dimension B — "Config" options**:
| Label | Behavior |
|-------|---------|
| None | No rate limiting applied (removes both RL steps) |
| Named config | Dropdown of existing configs + quick-create |
| From registry key | Read config name from registry at runtime |
| From header | Read config name from request header at runtime |
| From cache key | Read config name from cache at runtime |
| From query param | Read config name from query param at runtime |

**Types changes in `types.ts`**:
```typescript
// Replace current RateLimitVar with two new types:
export type RateLimitCountBy =
  | { kind: 'tenant' }
  | { kind: 'ip'; xffIndex?: number }          // Session 5 adds xffIndex
  | { kind: 'slot'; variableName: string }

export type RateLimitConfigSource =
  | { kind: 'named'; name: string }
  | { kind: 'dynamic'; source: 'registry'|'cache'|'header'|'queryparam'; key: string }

// Update ApiDef and EndpointDef:
// OLD: rateLimitName?: string; rateLimitVar?: RateLimitVar
// NEW: rateLimitCountBy?: RateLimitCountBy; rateLimitConfig?: RateLimitConfigSource
```

**`syncThisApi()` translation** (Dimension A drives which step action to use;
Dimension B drives `rate_limit` / `rate_limit_var` in SyncPayload):
- The studio does NOT auto-inject RL steps into flows. That remains the user's job in Flow Designer.
  Instead, the UI shows a note: "Add a `check_rate_limit` step to your flow to activate this config."
- Dimension A selection is persisted in API definition so the UI remembers it, but it is
  translated to a flow step hint / documentation — the actual step choice stays with the flow.
  EXCEPTION: see Session 5 for `bind_client_ip` auto-injection.
- Dimension B: `named` → `rate_limit: name` in SyncPayload; `dynamic` → `rate_limit_var: {source, key}`.

**Migration of existing data**: `ApiDef.rateLimitName` → `rateLimitConfig: {kind:'named', name}`.
`ApiDef.rateLimitVar` → `rateLimitConfig: {kind:'dynamic', source, key}`. `rateLimitCountBy` defaults to `{kind:'tenant'}`.

**Files changed**: `types.ts` (type changes), `APIsSection.tsx` (editor rewrite + migration + syncThisApi).

**Acceptance criteria**:
- Existing APIs loaded from gateway snapshot display the correct config name in Dimension B.
- Changing Dimension B to "named" and saving a config name round-trips through gateway sync.
- The `rate_limit_var` field in SyncPayload is populated when Dimension B is "dynamic".
- Dimension A selection persists when navigating away and back.
- No TypeScript errors (`npm run build` passes or `tsc --noEmit` passes).

**Session complexity**: Large. Touches types + editor + sync. Do NOT combine with other sessions.
Read `APIsSection.tsx` lines 780–962 (RateLimitEditor) and `types.ts` before starting.

---

## Session 5 — XFF IP Index + bind_client_ip Auto-Injection [todo]

**Scope**: UI (`APIsSection.tsx`) + verify gateway step exists. Possible gateway addition.

**Prerequisite**: Session 4 must be done (new `RateLimitCountBy` type with `xffIndex`).

**What to add to the UI**:
- When Dimension A = "Per IP", show a number input: "X-Forwarded-For index (0 = first IP)"
  with default 0. Store as `rateLimitCountBy: {kind: 'ip', xffIndex: 0}`.

**Studio auto-injection in `syncThisApi()`**:
When Dimension A = "Per IP" (at either API or endpoint level), studio prepends to the relevant
flow's instructions (payload-only, same pattern as Session 3):
```json
{"action": "bind_client_ip", "xff_index": 0, "as": "client_ip"}
```
Then the flow's `check_rate_limit_ip` step can read `client_ip` slot.

**Gateway step verification — before coding**:
1. `grep -r "bind_client_ip" internal/` — check if the step exists.
2. If it exists: use it. If not: this session must add it to `internal/engine/steps/`.

**`bind_client_ip` step spec** (if it needs to be added):
```go
// BindClientIP extracts the client IP from X-Forwarded-For or RemoteAddr,
// writing the raw IP bytes (no port) into the named slot.
// xffIndex: which comma-separated entry to use (0 = first/leftmost, -1 = last/rightmost).
func BindClientIP(slot int, xffIndex int) engine.Instruction
```
Also needs compiler support in `compiler.go`: `action: "bind_client_ip"` → compile to this instruction.

**Files changed**: `APIsSection.tsx` (UI + prepend logic). Conditionally: `internal/engine/steps/bind_ip.go` (new file) + `internal/control/compiler.go` (new case) + `internal/control/step_descriptors.go` (palette entry).

**Acceptance criteria**:
- Select "Per IP", set XFF index = 0. Sync. Flow payload has `bind_client_ip` as first step.
- If `bind_client_ip` step already exists in gateway: no Go changes needed, session is UI-only.
- TypeScript build passes.

**Session complexity**: Medium (UI side). Go side: Low if step exists, Medium-High if it must be added.

---

## Session 6 — Global (Tenant-Agnostic) Rate Limiting [todo]

**Scope**: Gateway (Go) + UI. Highest complexity. Save for last.

**Problem**: All current counter keys include `TenantID`. There is no way to express
"count all requests regardless of tenant" with the existing counter hash functions.

**Design**:
- Add `Dimension A` option: "Global (all tenants)" in the UI.
- Add new instruction `CheckRateLimitGlobal` in `internal/engine/steps/rate_limit_step.go`.
  Counter key: `hash(apiRLId, endpointRLId, epoch, windowType)` — no TenantID.
- Add compiler case for `action: "check_rate_limit_global"`.
- Wire through `syncThisApi()`: when Dimension A = "global", indicate via a new SyncPayload field
  (e.g., `rate_limit_mode: "global"`) → Go `ApiUpdate` gets a new `RateLimitMode string` field.

**Go files to change**:
- `internal/control/types.go`: add `RateLimitMode string` to `ApiUpdate` and `EndpointConfig`.
- `internal/control/management_server.go`: read `RateLimitMode`, bake into a new `RouteRLMode` table.
- `internal/engine/api_definition.go`: add `RateLimitMode uint8` to `Endpoint`.
- `internal/engine/steps/rate_limit_step.go`: add `CheckRateLimitGlobal`.
- `internal/control/compiler.go`: add `check_rate_limit_global` case.
- `internal/studio/ui/src/api.ts`: add `rate_limit_mode?: string` to SyncPayload api entry.
- `internal/studio/ui/src/components/APIsSection.tsx`: wire Dimension A "global" to new field.

**Files changed**: 6 Go files + 2 TS files. This is the most invasive session.

**Acceptance criteria**:
- Send `rate_limit_mode: "global"` in SyncPayload. Gateway compiles correctly (no panic).
- Two different tenants hitting the same API share the same counter bucket.
- Existing tenant-scoped and IP-scoped rate limiting is unaffected.
- `go build ./...` passes.

**Session complexity**: Large. Do not combine with anything else.

---

## Session 7 — Gateway-Native Upstream URL Support [todo]

**Scope**: Go gateway + UI. Future enhancement once Sessions 2-3 are proven in production.

**Problem**: Sessions 2-3 use studio-side transforms (constants + flow prepend). This works but
has a limitation: if a user edits the flow in Flow Designer, they may accidentally remove the
prepended step. A gateway-native approach is more robust.

**Design**:
- Add `UpstreamUrl *UpstreamUrlConfig` to Go `ApiUpdate` and `EndpointConfig` in `types.go`.
- In `management_server.go`, bake the config into a new `RouteUpstreamUrl` table (keyed by `apiID<<8|endpointID`).
- In `engine/manager.go`, after injecting route constants, also inject the upstream URL:
  if source=static → write value to `ctx.ByteSlots[upstreamUrlSlot]`;
  if source=registry → issue a `load_service_url` call; etc.
- Remove the studio-side transforms from Sessions 2-3 once gateway-native is live.

**Files changed**: `types.go`, `management_server.go`, `engine/manager.go`, `api_definition.go`.

**Session complexity**: Large. Only do this if Sessions 2-3 prove insufficient.

---

## Session Order Recommendation

```
Session 1 (pre-set vars polish) → Session 2 (static upstream URL) →
Session 3 (dynamic upstream URL) → Session 4 (rate limit redesign) →
Session 5 (XFF + bind_client_ip) → Session 6 (global RL) → Session 7 (gateway-native upstream URL)
```

Sessions 1-3: UI only, zero risk, can be done in any order relative to each other.
Session 4: Large UI refactor. Read relevant code carefully before starting.
Sessions 5-7: Increasing gateway involvement. Verify build passes after each.

---

## Pre-Session Checklist (run before each session)

1. `git status` — confirm clean working tree or known uncommitted changes.
2. Read this file to find the current `[todo]` session.
3. Read the "Files changed" list for that session.
4. Read the specific file sections mentioned in "Acceptance criteria".
5. For Go sessions: `go build ./...` should pass before you start.
6. After the session: mark the session `[done]` in this file.
