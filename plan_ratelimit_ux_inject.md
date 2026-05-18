# Rate Limit — API-Level Multi-Entry UX + Compiler Injection

## What This Plan Covers

End-to-end design for:
- Multi-entry rate limit configuration in the API Definition screen
- Per-row inline validation warnings
- Inline "Create Config" button
- Fixed (inline) limits vs named config per entry
- Compiler auto-inject into flow + position-aware injection
- Compiler validation pass (flow tree walk, slot dependency analysis)
- Unified rate limit step descriptor (no V1/V2 distinction)
- Flow designer marker block + warning indicators

**Does NOT cover**: counter store, tier system, upstream registry
(those are in plan_rate_limit_design.md and plan_rate_limit_sessions.md)

---

## Design Decisions (Finalized)

1. **No ctx additions** — all config baked at compile time into instruction structs.
   Cache line safety preserved. Zero new context fields.

2. **No persona modes** — system adapts to what user does, not who they are:
   - Flow has rate limit step → developer owns it, compiler respects position
   - Flow has no rate limit step → auto-inject at top from API definition
   - Both can coexist cleanly

3. **API screen = source of truth for WHAT**
   Flow designer = source of truth for WHERE

4. **Two entry types per row**:
   - **Named**: picks existing RateLimitConfig by name (reusable, shared)
   - **Fixed**: inline window + limit directly on the row (one-off, anonymous)

5. **Unified step**: one "Check Rate Limit" block in palette.
   Compiler picks V1/V2 instruction internally. Users never see version labels.

6. **Dynamic dispatch**: uses existing `ctx.QuotaGroupID uint8` + `AssignQuotaGroup`.
   No new ctx fields. Mapping baked at compile time.

7. **Skip flag**: explicit opt-out suppresses all warnings and auto-injection.

8. **Warnings are non-blocking**: deploy still works, but API screen shows
   per-row status and flow enforcement status.

---

## Data Model Changes

### Go — control/types.go

```go
// RateLimitEntryKind distinguishes inline vs named config entries.
type RateLimitEntryKind string
const (
    RLEntryNamed   RateLimitEntryKind = "named"   // references existing config by name
    RLEntryFixed   RateLimitEntryKind = "fixed"   // inline windows defined directly
    RLEntryDynamic RateLimitEntryKind = "dynamic" // config name resolved from runtime slot
)

// FixedWindow defines a single inline rate limit window.
type FixedWindow struct {
    EpochSec uint32 `json:"epoch_sec"` // 1=per-second, 60=per-minute, 3600=per-hour, 86400=per-day
    Limit    uint32 `json:"limit"`     // max requests per window
}

// DynamicMapping maps a runtime string value to a config name.
type DynamicMapping struct {
    Source   string            `json:"source"`   // e.g. "meta.tier", "header.X-Plan"
    Mappings map[string]string `json:"mappings"` // e.g. {"free":"free_rl","pro":"pro_rl"}
}

// APIRateLimitEntry is one row in the API definition's rate limit table.
type APIRateLimitEntry struct {
    Kind       RateLimitEntryKind `json:"kind"`                  // named | fixed | dynamic
    Config     string             `json:"config,omitempty"`      // named: config name
    CountBy    string             `json:"count_by"`              // tenant|ip|slot|global|static|composite
    SlotSource string             `json:"slot_source,omitempty"` // count_by=slot: which slot
    Windows    []FixedWindow      `json:"windows,omitempty"`     // fixed kind only
    Dynamic    *DynamicMapping    `json:"dynamic,omitempty"`     // dynamic kind only
}

// In ApiUpdate — replaces single RateLimitName field:
// RateLimitPolicies []APIRateLimitEntry `json:"rate_limit_policies,omitempty"`
// SkipRateLimit     bool                `json:"skip_rate_limit,omitempty"`
```

### TypeScript — types.ts additions

```typescript
export type RLEntryKind = 'named' | 'fixed' | 'dynamic'
export type RLCountBy = 'tenant' | 'ip' | 'global' | 'slot' | 'static' | 'composite'

export interface FixedWindow {
  epoch_sec: number   // 1 | 60 | 3600 | 86400
  limit: number
}

export interface DynamicMapping {
  source: string                  // e.g. "meta.tier"
  mappings: Record<string, string> // tier → config name
}

export interface APIRateLimitEntry {
  kind: RLEntryKind
  config?: string          // named
  count_by: RLCountBy
  slot_source?: string     // count_by=slot
  windows?: FixedWindow[]  // fixed
  dynamic?: DynamicMapping // dynamic
}
```

---

## Session Plan

### Reading Guide
- **Phase**: sessions in the same phase can run in parallel
- **Model**: Haiku = simple/mechanical | Sonnet = multi-file or complex logic
- **Blocking**: must complete before dependent sessions start

---

### Phase 1 — Foundation (Sequential, both must complete first)

#### S1 · Go Data Model  
**Model**: Haiku | **File**: `internal/control/types.go`  
**Task**: Add `APIRateLimitEntry`, `FixedWindow`, `DynamicMapping`, `RateLimitEntryKind`
to `types.go`. Update `ApiUpdate` to replace single `RateLimitName string` with
`RateLimitPolicies []APIRateLimitEntry` + `SkipRateLimit bool`.
Update `Endpoint` struct (in sub_compiler or wherever Endpoint is defined) to hold
`RateLimitPolicies []BakedRLPolicy` (resolved config IDs + baked windows).  
**Verify**: `go build ./internal/control/...` compiles clean.  
**Output**: Updated types, compiling.

#### S2 · TypeScript Data Model  
**Model**: Haiku | **Files**: `src/types.ts`, `src/api.ts`  
**Task**: Add TS types listed above. Update `ApiDefinition` interface to include
`rate_limit_policies: APIRateLimitEntry[]` and `skip_rate_limit: boolean`.
Update sync payload in `api.ts` `syncApi()` function to send these fields.  
**Verify**: `npx tsc --noEmit` clean.  
**Output**: Updated types + api.ts, type-check clean.

---

### Phase 2 — Parallel (start after Phase 1)

#### S3 · Step Catalog Unification  
**Model**: Haiku | **File**: `internal/control/step_descriptors.go`  
**Task**:  
1. Merge `check_rate_limit`, `check_rate_limit_v2`, `check_rate_limit_global` into
   one descriptor: `check_rate_limit` titled "Check Rate Limit".
   Fields: count_by, config (named), inline windows (fixed), dynamic source, on_empty,
   denied_label. Description must NOT mention V1/V2.  
2. Add new descriptor: `api_rate_limits` titled "API Rate Limits" category=rate-limit.
   No config fields — it is a position marker only. Description explains it injects
   the rate limits configured in the API definition at this position in the flow.  
**Verify**: build clean, `GET /meta/steps` returns updated catalog.  
**Output**: Unified descriptor + marker descriptor.

#### S4 · API Screen — Multi-Entry Rate Limit Table (Basic)  
**Model**: Sonnet | **File**: `src/components/APIsSection.tsx`  
**Task**: Replace existing single rate limit field (RateLimitV2Section or equivalent)
with a multi-entry table component. Each row has:
- Kind toggle: Named | Fixed | Dynamic (pill/tab selector, default: Named)
- **Named**: config name dropdown (from existing rate limit configs list) +
  inline "＋ Create Config" button that navigates/opens Rate Limit Configs screen.
  If config list is empty, show callout: "No configs yet. Create one to continue."
- **Fixed**: window rows (per-second / per-minute / per-hour / per-day checkboxes
  each with a limit input). At least one window required.
- Count by: dropdown (Per tenant / Per IP / Global / Per slot / Static key)
- Slot source field (shown only when count_by = slot)
- Row status icon (pending validation from S6 — leave as neutral for now)
- Remove [✕] button per row
- "＋ Add rate limit" button at bottom
- "Skip rate limiting on this endpoint" checkbox
- Help text under the section:
  "Rate limits defined here are automatically enforced in the flow.
   Place an 'API Rate Limits' block in the flow to control exact position."  
**Verify**: renders correctly, add/remove rows works, kind toggle switches fields.  
**Output**: New multi-entry rate limit section in API screen.

---

### Phase 3 — Parallel (start after S4 complete; S5 needs S1+S4, S6 needs S1)

#### S5 · API Screen — Dynamic Entry + Per-Row Status  
**Model**: Sonnet | **File**: `src/components/APIsSection.tsx`  
**Task**:  
1. **Dynamic kind UI**: when kind=Dynamic, show:
   - Source picker: dropdown of `meta.<key>` | `header.<name>` | `slot.<name>`
     with free-text input for the key name.
   - Mapping table: rows of (runtime value → config name), add/remove rows.
   - "＋ Create Config" button next to each config name in the mapping table.
2. **Per-row status** (computed client-side from available data):
   - Named: green ✓ if config exists in configs list, red ✗ if not found.
   - Fixed: green ✓ if at least one window has limit > 0.
   - Dynamic: yellow ⚠ if mapping is empty, green ✓ if ≥1 mapping + all configs found.
3. **Flow enforcement status** (static for now — always show unless skip checked):
   "Flow enforcement: check flow includes 'API Rate Limits' block or
    Check Rate Limit step. Auto-injected if absent."  
**Verify**: dynamic row renders, status icons update reactively.  
**Output**: Dynamic entry UI + status icons on all row types.

#### S6 · Backend — Management Server Multi-Entry Handling  
**Model**: Sonnet | **Files**: `internal/control/management_server.go`,
`internal/control/sub_compiler.go`  
**Task**:  
1. In `management_server.go` API update handler: parse `RateLimitPolicies` from
   `ApiUpdate`. For named entries: resolve config name → configID using existing
   rate limit config registry. For fixed entries: register an anonymous
   `RateLimitConfigV2` (deterministic name e.g. `__fixed__{apiID}_{rowIdx}`).
   For dynamic entries: build quota group mapping (tier string → configID).
2. Store resolved policies in `Endpoint.RateLimitPolicies` (baked form).
3. Remove old single `Endpoint.APIRateLimitId` handling (or keep as fallback for
   existing data — confirm with codebase state).  
**Verify**: `go build ./internal/control/...` clean. Manual sync with named config
populates Endpoint correctly (add log line for verification).  
**Output**: Backend reads + resolves multi-entry rate limit policies.

---

### Phase 4 — Parallel (start after S3 + S6 complete)

#### S7 · Compiler — Auto-Inject + Position-Aware Injection  
**Model**: Sonnet | **File**: `internal/control/sub_compiler.go` (+ compiler.go)  
**Task**:  
1. **Flow tree walk**: write helper `flowHasRateLimitStep(flowName string) bool`
   that recursively walks all steps + subflows (via `call`, `if`, `foreach`, `switch`
   nested steps) and returns true if any step type is a rate limit step
   (`check_rate_limit`, `check_rate_limit_v2`, `check_rate_limit_global`,
   `api_rate_limits`).
2. **`api_rate_limits` marker handling**: when compiler encounters step type
   `api_rate_limits`, replace it with the baked `CheckRateLimitV2` instructions
   for each entry in `Endpoint.RateLimitPolicies` at that position.
3. **Auto-inject**: if `flowHasRateLimitStep` returns false AND
   `Endpoint.SkipRateLimit` is false AND `len(Endpoint.RateLimitPolicies) > 0`:
   prepend `CheckRateLimitV2` instructions at the beginning of the compiled plan
   (before instruction index 0).
4. **Dynamic entries**: emit `AssignQuotaGroup` instruction immediately before
   the `CheckRateLimitV2` instruction when entry kind is dynamic.  
**Verify**: `go build` clean. Deploy an API with named config + no flow RL step →
   confirm instruction table has CheckRateLimitV2 at index 0.  
**Output**: Compiler injects rate limit instructions from API definition.

#### S8 · Flow Designer — API Rate Limits Marker Block  
**Model**: Sonnet | **File**: `src/components/` (flow designer component)  
**Task**: Add rendering for `api_rate_limits` block type in the flow designer palette
and canvas. The block:
- Shows with a distinct badge "API Policy" in a muted accent color
- Lists the rate limit entries from the currently viewed API definition
  (if viewed from API screen context) or shows "configured in API definition"
  if viewed standalone
- Is draggable like any other block
- Click opens the API definition's rate limit section (not inline edit)
- No config fields on the block itself
- Palette description: "Enforce rate limits configured in the API definition.
  Drag to control where in the flow enforcement happens. If absent,
  limits are auto-injected at the start of the flow."  
**Verify**: block appears in palette, can be added to flow canvas, renders correctly.  
**Output**: Marker block in flow designer.

---

### Phase 5 — Sequential (start after S7 complete)

#### S9 · Compiler — Validation Pass + Warning Generation  
**Model**: Sonnet | **File**: `internal/control/sub_compiler.go`  
**Task**: After compiling each endpoint, run a validation pass that produces
`[]RateLimitWarning` returned alongside the baked endpoint:

```go
type RLWarnCode string
const (
    RLWarnNoFlow        RLWarnCode = "no_flow"          // no flow assigned
    RLWarnNotEnforced   RLWarnCode = "not_enforced"     // RL defined, not in flow tree
    RLWarnSlotUnfilled  RLWarnCode = "slot_unfilled"    // slot needed before RL step
    RLWarnConfigMissing RLWarnCode = "config_missing"   // named config not found
)

type RateLimitWarning struct {
    Code    RLWarnCode `json:"code"`
    Message string     `json:"message"`
    Row     int        `json:"row,omitempty"` // which APIRateLimitEntry (0-indexed)
    Slot    string     `json:"slot,omitempty"`
}
```

Validation checks:
- `no_flow`: no flow assigned to endpoint
- `not_enforced`: RL policies exist + skip=false + flow tree has no RL step
- `config_missing`: named config referenced does not exist at bake time
- `slot_unfilled`: dynamic entry uses `meta.tier` but no `load_meta key=tier` step
  appears before the rate limit step in any path through the flow

Return warnings in the sync/bake response so the Studio can display them.  
**Verify**: deploy API with missing config → warning returned in response.  
**Output**: Validation pass producing typed warnings.

---

### Phase 6 — Sequential (after S9 + S5 + S8 complete)

#### S10 · API Screen — Live Warnings from Bake Response  
**Model**: Sonnet | **File**: `src/components/APIsSection.tsx`  
**Task**: Wire bake/sync response warnings into the API screen UI:
1. After sync, parse `rate_limit_warnings` from response.
2. Per-row: if warning with matching `row` index exists, show warning icon + tooltip.
3. Flow enforcement status: if `not_enforced` warning present, show:
   "⚠ Not enforced in flow — auto-inject will apply on next deploy.
    [View Flow] [Mark as Skipped]"
4. If `config_missing` warning: highlight that row's config picker in red +
   "Config not found — [Create Config]" link.  
**Verify**: sync with missing config → red row shown. Sync clean → green row.  
**Output**: Live warning display wired to backend validation.

---

### Phase 7 — Final Integration (after all above)

#### S11 · End-to-End Verification  
**Model**: Sonnet  
**Task**: Full path test:
1. Create a rate limit config via Rate Limits screen.
2. Add it to an API definition (named entry, count_by=tenant).
3. Sync — verify no warnings, instruction table has CheckRateLimitV2 at index 0.
4. Add `api_rate_limits` marker to flow, sync — verify instruction injected at
   that position instead.
5. Add second entry (fixed, 10/sec global) — verify two instructions emitted.
6. Dynamic entry: map meta.tier → configs, verify AssignQuotaGroup emitted.
7. Check skip flag: verify no instructions injected when skip=true.  
**Output**: Verified working end-to-end. Fix any integration gaps found.

---

## Session Sequence & Parallelism

```
Phase 1 (blocking):
  S1 Go Types ──────────────────────────────────────────────────────┐
  S2 TS Types ──────────────────────────────────────────────────────┤
                                                                     │
Phase 2 (parallel, needs Phase 1):                                   │
  S3 Step Catalog ──────────────────────────────────────────────┐   │
  S4 API Screen Basic Table ────────────────────────────────┐   │   │
                                                             │   │   │
Phase 3 (parallel, needs S1 + S4):                           │   │   │
  S5 Dynamic + Status Icons ───────────────────────────┐    │   │   │
  S6 Backend Management Server ──────────────────────┐  │   │   │   │
                                                      │  │   │   │   │
Phase 4 (parallel, needs S3 + S6 for S7; S8 is free): │  │   │   │   │
  S7 Compiler Auto-Inject ─────────────────────┐      │  │   │   │   │
  S8 Flow Designer Marker Block ──────────────┐ │     │  │   │   │   │
                                              │ │     │  │   │   │   │
Phase 5 (needs S7):                           │ │     │  │   │   │   │
  S9 Compiler Validation Pass ──────────┐    │ │     │  │   │   │   │
                                        │    │ │     │  │   │   │   │
Phase 6 (needs S9 + S5 + S8):           │    │ │     │  │   │   │   │
  S10 API Screen Live Warnings ────┐    │    │ │     │  │   │   │   │
                                   │    │    │ │     │  │   │   │   │
Phase 7 (needs all):               └────┴────┴─┴─────┴──┴───┴───┴───┘
  S11 End-to-End Verification
```

---

## Model Guide Per Session

| Session | Model  | Reason |
|---------|--------|--------|
| S1      | Haiku  | Struct additions, mechanical |
| S2      | Haiku  | TS interface additions, mechanical |
| S3      | Haiku  | Descriptor edits, one file |
| S4      | Sonnet | Multi-state UI component, React |
| S5      | Sonnet | Dynamic UI + reactive validation |
| S6      | Sonnet | Multi-file backend, existing code context needed |
| S7      | Sonnet | Compiler logic, recursive flow walk |
| S8      | Sonnet | Flow designer component, rendering logic |
| S9      | Sonnet | Compiler analysis, dependency graph walk |
| S10     | Sonnet | Wiring backend response to UI state |
| S11     | Sonnet | Integration, debug any gaps |

Haiku sessions (S1, S2, S3): fast, cheap, single-file changes.
Sonnet sessions: need more context but are bounded to specific files listed.

---

## Files Touched Per Session

| Session | Files |
|---------|-------|
| S1 | `internal/control/types.go`, `internal/control/sub_compiler.go` (Endpoint struct) |
| S2 | `internal/studio/ui/src/types.ts`, `internal/studio/ui/src/api.ts` |
| S3 | `internal/control/step_descriptors.go` |
| S4 | `internal/studio/ui/src/components/APIsSection.tsx` |
| S5 | `internal/studio/ui/src/components/APIsSection.tsx` |
| S6 | `internal/control/management_server.go`, `internal/control/sub_compiler.go` |
| S7 | `internal/control/sub_compiler.go`, `internal/control/compiler.go` |
| S8 | Flow designer component (identify exact file at session start) |
| S9 | `internal/control/sub_compiler.go` |
| S10 | `internal/studio/ui/src/components/APIsSection.tsx` |
| S11 | Read-only verification + targeted fixes |

---

## Key Constraints (Do Not Violate)

1. **Zero new ctx fields** — cache line safety. All config baked at compile time.
2. **No V1/V2 labels** in any user-facing string.
3. **`ctx.QuotaGroupID uint8`** is the only ctx field used for dynamic dispatch.
   It already exists. Do not add parallel fields.
4. **Fixed entry anonymous configs** use deterministic name pattern
   `__fixed__{apiID}_{rowIdx}` so re-baking is idempotent.
5. **Warnings are advisory** — bake succeeds even with warnings.
   Only missing flow (`no_flow`) is a hard error.
6. **Existing `check_rate_limit` / `check_rate_limit_v2` step types in existing flows
   must still compile correctly** — S3 unifies the descriptor but the compiler
   must still accept both type strings during migration.
