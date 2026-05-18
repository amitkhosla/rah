╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
 RAH Studio — Comprehensive Improvement Plan

 ▎ All sessions are self-sufficient. Each produces a complete, working change. No session is left half-built.
 ▎ Gateway latency path is never touched. All gateway additions are compile-time or trace-only (zero overhead when disabled).

 ---
 Section 1 — Verified Capability Inventory

 Verified by reading actual source code. No guesses.

 Flow Designer

 ┌─────────────────────────────────┬──────────┬────────────────────────────┬────────────────────────────────────────────────────────────────────────┐
 │           Capability            │  Status  │           Files            │                                 Notes                                  │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Palette Visual Groups (11       │ ✅       │ FlowDesigner.tsx:28-142    │ INFERENCE, KNOWLEDGE, MEMORY, TOOLS, SECURITY, ROUTING, DATA, REQUEST, │
 │ categories)                     │ Exists   │                            │  CACHE, RESPONSE, OBSERVABILITY                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Palette Expert mode (searchable │ ✅       │ FlowDesigner.tsx:1382-1447 │ Filters by name, category, type                                        │
 │  all blocks)                    │ Exists   │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Visual / Expert toggle          │ ✅       │ FlowDesigner.tsx:1345-1380 │ mode: 'visual' | 'expert' state                                        │
 │                                 │ Exists   │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Palette section collapsibility  │ ❌       │ —                          │ Sections are static, no accordion                                      │
 │                                 │ Missing  │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Sidebar collapse (icon-only     │ ❌       │ App.tsx:224-299            │ Sidebar always visible, no toggle                                      │
 │ mode)                           │ Missing  │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ If/Else as droppable inline     │ ❌ Bug   │ FlowDesigner.tsx:240-249   │ handleDrop always appends to end; renderIfBody:318-350 uses text       │
 │ branches                        │          │                            │ inputs for flow names only                                             │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Inline nested steps in if/else  │ ❌       │ —                          │ FlowStep is flat Record<string,string>; no then_steps/else_steps       │
 │                                 │ Missing  │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Flow Map (sub-flow hierarchy    │ ❌       │ —                          │ No visual graph; sub-flow refs are collected in App.tsx:39-54 but      │
 │ view)                           │ Missing  │                            │ never visualized                                                       │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Sub-flow navigation (click to   │ ❌       │ —                          │                                                                        │
 │ open)                           │ Missing  │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Per-step "Capture in trace"     │ ✅       │ FlowDesigner.tsx:562-633   │ Emits trace_capture instruction; connected to as / destination field   │
 │ checkbox                        │ Exists   │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Per-step "Log result as" field  │ ✅       │ FlowDesigner.tsx:562-633   │ Emits log_field instruction with custom access log field name          │
 │                                 │ Exists   │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Step reordering (move up/down)  │ ✅       │ FlowDesigner.tsx           │ Buttons on each step card                                              │
 │                                 │ Exists   │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ Step groups (label a range of   │ ✅       │ FlowDesigner.tsx           │ StepGroup interface in types.ts                                        │
 │ steps)                          │ Exists   │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ "My Flows" section in palette   │ ✅       │ FlowDesigner.tsx:1392-1406 │ Shows saved flows as call blocks                                       │
 │                                 │ Exists   │                            │                                                                        │
 ├─────────────────────────────────┼──────────┼────────────────────────────┼────────────────────────────────────────────────────────────────────────┤
 │ JSON preview panel              │ ✅       │ FlowDesigner.tsx           │ Shows compiled expandSteps output                                      │
 │                                 │ Exists   │                            │                                                                        │
 └─────────────────────────────────┴──────────┴────────────────────────────┴────────────────────────────────────────────────────────────────────────┘

 Observability — Data Captured

 ┌─────────────────────────────┬────────────────────────────────────────────────────┬────────────────────────────┬──────────────────────────────────┐
 │         Data Field          │                Captured in Gateway                 │      Exposed via API       │           Shown in UI            │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Request total duration (ns) │ ✅ RequestSummary.DurationNs                       │ ✅                         │ ✅ Total ms column               │
 │                             │                                                    │ /api/observability/traces  │                                  │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Gateway-only duration       │ ✅ RequestSummary.GatewayDurationNs                │ ✅                         │ ✅ Gateway ms column             │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Upstream duration           │ ✅ RequestSummary.UpstreamDurationNs               │ ✅                         │ ✅ Upstream ms column            │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ HTTP method + path          │ ✅ RequestSummary                                  │ ✅                         │ ✅                               │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ HTTP status                 │ ✅ RequestSummary.Status                           │ ✅                         │ ✅                               │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Tenant ID                   │ ✅ RequestSummary.TenantID                         │ ✅                         │ ✅                               │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ API ID / name               │ ✅ RequestSummary.ApiID                            │ ✅                         │ ✅                               │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Per-instruction name        │ ✅ InstructionEvent.Name                           │ ✅                         │ ✅ (TraceTimeline)               │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Per-instruction duration    │ ✅ InstructionEvent.DurationNs                     │ ✅                         │ ✅ (TraceTimeline bar)           │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Per-step output variables   │ ✅ InstructionEvent.Output[]KV — populated when    │ ✅                         │ ✅ InstructionEventRow shows KV  │
 │ (KV)                        │ trace_capture instruction runs                     │                            │ pairs (capped 300 chars)         │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Per-step INPUT variables    │ ✅ InstructionEvent.Input[]KV — struct field       │ ✅                         │ ⚠️ Shown in UI but never         │
 │                             │ exists                                             │                            │ populated by any step            │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │                             │                                                    │                            │ ❌ TraceTimeline shows           │
 │ Upstream full URL           │ ✅ UpstreamEvent.URL                               │ ✅                         │ host+status only — URL not       │
 │                             │                                                    │                            │ displayed                        │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Upstream HTTP status        │ ✅ UpstreamEvent.Status                            │ ✅                         │ ✅                               │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Upstream bytes              │ ✅ UpstreamEvent.BytesSent/BytesReceived           │ ✅                         │ ❌ Not shown in trace view       │
 │ sent/received               │                                                    │                            │                                  │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Upstream DNS/TCP/TLS/TTFB   │ ✅ UpstreamEvent (all phase fields)                │ ✅                         │ ⚠️ Shown only in expanded access │
 │ timing                      │                                                    │                            │  log row, not in trace view      │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Upstream REQUEST headers    │ ❌ Not captured                                    │ ❌                         │ ❌                               │
 │ sent                        │                                                    │                            │                                  │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Incoming request headers    │ ❌ Not captured in trace                           │ ❌                         │ ❌                               │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Incoming query params       │ ❌ Not captured in trace                           │ ❌                         │ ❌                               │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Response body sent to       │ ❌ Not captured (only slot value if trace_capture  │ ❌                         │ ❌                               │
 │ client                      │ used on return step)                               │                            │                                  │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Cache hit/miss per          │ ✅ CacheStat                                       │ ✅                         │ ✅ (Settings panel)              │
 │ instruction                 │                                                    │                            │                                  │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ LLM prompt/system/response  │ ✅ via trace_capture KV on llm_call                │ ✅                         │ ✅ RouterTraceSummary panel      │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Per-instruction semantic    │ ✅ BlockView heuristics                            │ —                          │ ✅ (LLM flows only)              │
 │ block grouping              │                                                    │                            │                                  │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Per-step flow origin (which │ ❌ Not in InstructionEvent                         │ ❌                         │ ❌                               │
 │  step index)                │                                                    │                            │                                  │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Request TTFB to client      │ ✅ AccessLogRecord.TTFBMs                          │ ✅                         │ ✅ (expanded access log row)     │
 ├─────────────────────────────┼────────────────────────────────────────────────────┼────────────────────────────┼──────────────────────────────────┤
 │ Extra log fields            │ ✅ AccessLogRecord.Extra (from headers/QPs)        │ ✅                         │ ✅ (log field selector in        │
 │ (user-configured)           │                                                    │                            │ settings)                        │
 └─────────────────────────────┴────────────────────────────────────────────────────┴────────────────────────────┴──────────────────────────────────┘

 Observability — UI Panels

 ┌─────────────────────────────────────────────┬───────────┬─────────────────────────────────────────────────────────────────────────────────────────┐
 │                    Panel                    │  Status   │                                          Notes                                          │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ Stat cards (total req, error rate, avg      │ ✅ Exists │ Observability.tsx stat cards                                                            │
 │ latency, TPS)                               │           │                                                                                         │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ API performance table                       │ ✅ Exists │ Per-API request count + avg latency                                                     │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ Access log table (filterable, expandable)   │ ✅ Exists │ Shows timing breakdown on expand                                                        │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ Trace list (sampled traces)                 │ ✅ Exists │ Filterable by API, tenant, status, min_ms                                               │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ TraceTimeline (Gantt-like instruction bar   │ ✅ Exists │ Observability.tsx:1347-1498                                                             │
 │ chart)                                      │           │                                                                                         │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ BlockView (semantic LLM instruction         │ ✅ Exists │ Observability.tsx:1240-1324, only useful for AI router flows                            │
 │ grouping)                                   │           │                                                                                         │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ RouterTraceSummary (LLM prompt/response)    │ ✅ Exists │ Observability.tsx:1079-1106                                                             │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ Upstream details in trace (full URL,        │ ❌        │ UpstreamEvent data exists but not displayed                                             │
 │ headers, bytes)                             │ Missing   │                                                                                         │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ Incoming request details (headers, QPs)     │ ❌        │ Not captured in telemetry                                                               │
 │                                             │ Missing   │                                                                                         │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ Flow-aligned trace view (match trace to     │ ❌        │ No step-to-instruction mapping                                                          │
 │ flow steps)                                 │ Missing   │                                                                                         │
 ├─────────────────────────────────────────────┼───────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
 │ Per-step variable selection in trace        │ ⚠️        │ "Capture in trace" checkbox exists, but user cannot specify which additional variables  │
 │                                             │ Partial   │ to capture per step                                                                     │
 └─────────────────────────────────────────────┴───────────┴─────────────────────────────────────────────────────────────────────────────────────────┘

 ---
 Section 2 — Session Map

 Sessions are ordered by dependency. Each is self-contained.

 UI Sessions (no backend changes):
   [UI-1] Sidebar Collapse
   [UI-2] Palette Unification (remove toggle, add accordion sections)
   [UI-3] If/Else Inline Branches + flattenForDeploy
   [UI-4] Flow Map Panel

 Observability Sessions:
   [OBS-1] Show Upstream URL + Bytes in Trace View       — UI only, data already exists
   [OBS-2] Incoming Request Headers in Traces            — small gateway + UI
   [OBS-3] Outgoing Request Headers + Response Capture   — small gateway + UI
   [OBS-4] Flow-Aligned Trace View                       — small gateway metadata + UI redesign
   [OBS-5] Per-Step Variable Selector                    — UI only

 ---
 Session [UI-1]: Sidebar Collapse

 Goal: Left sidebar collapses to 48px icon-only mode. Main canvas gets full width.

 Files to change:
 - internal/studio/ui/src/App.tsx
 - CSS (inline styles or index.css)

 What to build:

 1. State: Add const [sidebarCollapsed, setSidebarCollapsed] = useState(false) near other state hooks.
 2. Icon map: Define a NAV_ICONS: Record<TabId, string> mapping each tab to an emoji/symbol:
 dashboard → '⊞', flows → '⛶', apis → '⬡', ai → '⬡',
 deploy → '↑', gateway → '◉', observability → '⊛', tenants → '☰', settings → '⚙'
 3. Sidebar rendering (current: App.tsx lines 224-299):
   - Pass width: sidebarCollapsed ? 48 : 180 inline (or via CSS class toggle)
   - When collapsed: sidebar items render as icon-only (no text), section labels hidden, "+ New Flow" hidden, accent picker hidden
   - Connection status badge: show only a ● dot when collapsed (green/red/yellow)
   - Add a collapse toggle button at the bottom of the sidebar:
       - Collapsed: » (expand arrow)
     - Expanded: « (collapse arrow)
 4. Tooltip on hover when collapsed: use title attribute on the button element for native browser tooltip (simplest approach, no library needed).
 5. CSS transition: transition: width 0.2s ease on the sidebar element.

 Verification:
 - Toggle button collapses/expands sidebar
 - All nav items reachable from icon-only mode
 - Main content area fills the vacated space
 - Connection status still visible when collapsed
 - No layout breakage on any tab

 ---
 Session [UI-2]: Palette Unification

 Goal: Remove the Visual/Expert toggle. Replace with a single collapsible-section palette that serves both use cases.

 Files to change:
 - internal/studio/ui/src/components/FlowDesigner.tsx

 What to build:

 1. Remove the mode: 'visual' | 'expert' useState hook (line 173) and the toggle button block (lines 1345-1380).
 2. Add const [collapsedSections, setCollapsedSections] = useState<Set<string>>(new Set()) to track which sections are collapsed. Default: all open.
 3. Replace the entire palette body (lines 1382-1481) with a single unified renderer:
   - Search input (same as Expert mode's filter, line 1385-1390)
   - "My Flows" section at the top (same as Expert mode, lines 1392-1406)
   - For each VISUAL_GROUP: render an accordion section:
       - Clickable header row: {group.icon} {group.label} + collapse arrow ▶/▼
     - When collapsed: show only the header (no blocks inside)
     - When expanded: show all recipe blocks (using the same drag payload as current Visual mode)
     - collapsed state toggled by clicking header; update collapsedSections set
 4. Search behavior: when filter is non-empty:
   - Suppress the collapsedSections state — always show all matching blocks regardless of section collapse state
   - Show only blocks whose title, wraps, or description includes the search term (case-insensitive)
   - Show section header even if only some blocks match
 5. Each block item shows:
   - Primary: friendly recipe title (current behavior)
   - Secondary in smaller muted text: the action type (recipe.wraps) — this is the "Expert mode" info power users want
 6. "My Flows" section stays at top, unchanged from current Expert mode behavior.

 Key lines to understand before implementing:
 - VISUAL_GROUPS defined: lines 28-142 (11 groups, each with label + icon + recipes[])
 - recipeToBlock(): lines 194-209 (converts recipe to PaletteBlock for drag payload)
 - Current Expert mode block rendering: lines 1412-1445 (reference for block item style)
 - Current Visual mode recipe rendering: lines 1448-1481 (reference for drag handler)

 Verification:
 - All palette blocks remain draggable with correct action types
 - Search filters across all sections simultaneously
 - Section collapse state persists across drag operations
 - Power users can see action type names on each block
 - "My Flows" section always visible at top

 ---
 Session [UI-3]: If/Else Inline Branches

 Goal: Users can drag steps from the palette directly into if/else branches. Infinite nesting supported. Gateway format unchanged — a flattenForDeploy()
 transform runs before deploy.

 Files to change:
 - internal/studio/ui/src/types.ts
 - internal/studio/ui/src/utils/expressions.ts
 - internal/studio/ui/src/utils/flatten.ts (new file)
 - internal/studio/ui/src/components/FlowDesigner.tsx
 - internal/studio/ui/src/App.tsx

 Architecture decision: Studio stores then_steps: FlowStep[] / else_steps: FlowStep[] internally. At deploy time, flattenForDeploy() converts these to
 auto-named fragment flows (__auto_{flowName}_{stepIdx}_then) and rewrites the if step to use string then/else references. The gateway receives only the
 existing flat format. No backend changes.

 Step 1 — types.ts (line 78)

 Replace flat FlowStep type:
 // OLD:
 export type FlowStep = { action: string } & Record<string, string>

 // NEW:
 export interface FlowStep {
   action: string
   then_steps?: FlowStep[]   // Studio-only: inline then branch steps
   else_steps?: FlowStep[]   // Studio-only: inline else branch steps
   [key: string]: unknown    // all other step params (strings, numbers, etc.)
 }

 Step 2 — expressions.ts (around line 136-166)

 In expandSteps(), the loop does Object.entries(step) and calls String(v ?? ''). This breaks when v is a FlowStep[]. Fix: skip nested arrays.

 In the entries loop, before processing:
 if (k === 'then_steps' || k === 'else_steps') continue

 After the step is processed and pushed to result, also recurse:
 const pushed = result[result.length - 1]
 if (step.then_steps) pushed.then_steps = expandSteps(step.then_steps)
 if (step.else_steps) pushed.else_steps = expandSteps(step.else_steps)

 Step 3 — New file: utils/flatten.ts

 This function converts the hierarchical Studio format to the flat gateway format at deploy time.

 import type { FlowStep } from '../types'
 // Import FlowUpdate type from types or define locally

 interface FlatFlowUpdate {
   name: string
   instructions: FlowStep[]
   action: 'upsert'
 }

 /**
  * Converts a potentially-hierarchical flow (with then_steps/else_steps)
  * into a flat array of flow updates for the gateway.
  * Auto-generates fragment flow names for each inline branch.
  */
 export function flattenForDeploy(flowName: string, steps: FlowStep[]): FlatFlowUpdate[] {
   const fragments: FlatFlowUpdate[] = []

   function flattenSteps(steps: FlowStep[], contextName: string): FlowStep[] {
     return steps.map((step, idx) => {
       if (step.action !== 'if') return step

       const thenSteps = step.then_steps ?? []
       const elseSteps = step.else_steps ?? []

       // Already has string refs (old format) — leave unchanged
       if (thenSteps.length === 0 && elseSteps.length === 0) return step

       const thenFragName = `__auto_${contextName}_${idx}_then`
       const elseFragName = `__auto_${contextName}_${idx}_else`

       // Recursively flatten nested branches (handles infinite nesting)
       if (thenSteps.length > 0) {
         const flatThen = flattenSteps(thenSteps, `${contextName}_${idx}_then`)
         fragments.push({ name: thenFragName, instructions: flatThen, action: 'upsert' })
       }
       if (elseSteps.length > 0) {
         const flatElse = flattenSteps(elseSteps, `${contextName}_${idx}_else`)
         fragments.push({ name: elseFragName, instructions: flatElse, action: 'upsert' })
       }

       // Return a flat if step with string then/else references
       const { then_steps: _, else_steps: __, ...rest } = step as Record<string, unknown>
       return {
         ...rest,
         action: 'if',
         ...(thenSteps.length > 0 ? { then: thenFragName } : {}),
         ...(elseSteps.length > 0 ? { else: elseFragName } : {}),
       } as FlowStep
     })
   }

   const flatMain = flattenSteps(steps, flowName)
   return [{ name: flowName, instructions: flatMain, action: 'upsert' }, ...fragments]
 }

 Step 4 — FlowDesigner.tsx changes

 4a. New state: Add const [nestedExpanded, setNestedExpanded] = useState<Set<string>>(new Set())

 4b. New mutation helpers (add near the existing updateStep at line 259):

 function updateBranchStep(parentIdx: number, branch: 'then_steps' | 'else_steps', ni: number, key: string, value: unknown) {
   setSteps(steps.map((s, idx) => {
     if (idx !== parentIdx) return s
     const arr = [...((s[branch] as FlowStep[]) ?? [])]
     arr[ni] = { ...arr[ni], [key]: value }
     return { ...s, [branch]: arr }
   }))
 }

 function removeBranchStep(parentIdx: number, branch: 'then_steps' | 'else_steps', ni: number) {
   setSteps(steps.map((s, idx) => {
     if (idx !== parentIdx) return s
     const arr = ((s[branch] as FlowStep[]) ?? []).filter((_, j) => j !== ni)
     return { ...s, [branch]: arr }
   }))
 }

 function addToBranch(parentIdx: number, branch: 'then_steps' | 'else_steps', b: PaletteBlock) {
   setSteps(steps.map((s, idx) => {
     if (idx !== parentIdx) return s
     const arr = [...((s[branch] as FlowStep[]) ?? []), { action: b.type, ...b.defaults }]
     return { ...s, [branch]: arr }
   }))
 }

 4c. New renderBranch() function (add before renderIfBody):

 function renderBranch(label: string, isElse: boolean, branchSteps: FlowStep[], parentIdx: number, branch: 'then_steps' | 'else_steps') {
   const [localDragOver, setLocalDragOver] = useState(false)
   const borderColor = isElse ? '#ef4444' : '#22c55e'
   const labelColor = isElse ? '#ef4444' : '#22c55e'

   return (
     <div style={{ marginTop: 8, borderRadius: 6, border: `1px solid rgba(255,255,255,0.08)`, borderLeft: `3px solid ${borderColor}` }}>
       <div style={{ fontSize: 11, fontWeight: 700, padding: '4px 10px', color: labelColor, background: 'rgba(255,255,255,0.03)', letterSpacing: '0.06em'
  }}>
         {label}
       </div>
       {branchSteps.map((ns, ni) => renderNestedStepCard(ns, ni, parentIdx, branch))}
       <div
         style={{
           margin: '6px 8px',
           padding: '7px 10px',
           border: `1.5px dashed ${localDragOver ? borderColor : 'rgba(255,255,255,0.15)'}`,
           borderRadius: 5,
           fontSize: 11,
           color: localDragOver ? borderColor : 'var(--muted)',
           background: localDragOver ? `${borderColor}10` : 'transparent',
           cursor: 'default',
           textAlign: 'center',
           transition: 'border-color 0.15s, background 0.15s, color 0.15s',
         }}
         onDragOver={e => { e.preventDefault(); e.stopPropagation(); setLocalDragOver(true) }}
         onDragLeave={e => { e.stopPropagation(); setLocalDragOver(false) }}
         onDrop={e => {
           e.preventDefault()
           e.stopPropagation()  // ← prevents canvas handleDrop from firing
           setLocalDragOver(false)
           const raw = e.dataTransfer.getData('application/json')
           if (!raw) return
           const b: PaletteBlock = JSON.parse(raw)
           addToBranch(parentIdx, branch, b)
         }}
       >
         {branchSteps.length === 0 ? '+ Drop step here' : '+ Drop another step'}
       </div>
     </div>
   )
 }

 4d. New renderNestedStepCard() function:

 function renderNestedStepCard(step: FlowStep, ni: number, parentIdx: number, branch: 'then_steps' | 'else_steps') {
   const key = `${parentIdx}-${branch}-${ni}`
   const isExp = nestedExpanded.has(key)
   const defs = fieldMap[step.action as string] ?? {}
   // Create a curried updateFn for this nested step
   const updateFn = (k: string, v: string) => updateBranchStep(parentIdx, branch, ni, k, v)
   // For now, use renderGenericBody with updateFn — full body renderer refactor is optional in this session
   return (
     <div key={key} style={{ margin: '4px 8px', borderRadius: 5, border: '1px solid rgba(255,255,255,0.07)', background: 'rgba(255,255,255,0.02)' }}>
       <div
         style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px', cursor: 'pointer', userSelect: 'none' }}
         onClick={() => setNestedExpanded(prev => { const s = new Set(prev); s.has(key) ? s.delete(key) : s.add(key); return s })}
       >
         <span style={{ fontSize: 10 }}>{isExp ? '▼' : '▶'}</span>
         <strong style={{ fontSize: 12, flex: 1 }}>{ni + 1}. {step.action as string}</strong>
         <button
           className="btn muted step-remove"
           style={{ fontSize: 11 }}
           onClick={e => { e.stopPropagation(); removeBranchStep(parentIdx, branch, ni) }}
         >×</button>
       </div>
       {isExp && (
         <div style={{ padding: '0 8px 8px' }}>
           {Object.entries(defs).map(([k, def]) => (
             <div key={k} className="field-row">
               <label className="field-label">{def.label ?? k}</label>
               <input className="input" placeholder={def.placeholder ?? k}
                 value={(step[k] as string) ?? ''}
                 onChange={e => updateFn(k, e.target.value)} />
             </div>
           ))}
           {Object.keys(defs).length === 0 && Object.entries(step).filter(([k]) => k !== 'action').map(([k, v]) => (
             <div key={k} className="field-row">
               <label className="field-label">{k}</label>
               <input className="input" value={String(v ?? '')} onChange={e => updateFn(k, e.target.value)} />
             </div>
           ))}
         </div>
       )}
     </div>
   )
 }

 Note: For this session, nested step bodies use a simplified generic editor. Full rich editors (like renderCacheBody, renderTokenValidationBody) inside
 branches can be added in a follow-up session if needed.

 4e. Update renderIfBody (replace lines 318-350):

 function renderIfBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
   const thenSteps = (step.then_steps as FlowStep[]) ?? []
   const elseSteps = (step.else_steps as FlowStep[]) ?? []
   return (
     <div className="step-body">
       {fieldInput(i, 'condition', (step['condition'] as string) ?? '', defs['condition'], { smart: true })}
       {renderBranch('✓ THEN', false, thenSteps, i, 'then_steps')}
       {renderBranch('✗ ELSE', true,  elseSteps, i, 'else_steps')}
     </div>
   )
 }

 Step 5 — App.tsx: Wire flattenForDeploy

 Find the place where the deploy payload's flows array is built (the syncFlows or deploy call). Replace direct use of savedFlows with the result of
 flattenForDeploy:

 import { flattenForDeploy } from './utils/flatten'
 // In deploy handler:
 const allFlowUpdates = savedFlows.flatMap(sf => flattenForDeploy(sf.name, sf.steps))
 // Use allFlowUpdates instead of savedFlows.map(sf => { name, instructions, action })

 Also update collectFlowRefs (App.tsx:39-54) to recurse into then_steps/else_steps:
 function collectFlowRefs(steps: FlowStep[]): string[] {
   const refs: string[] = []
   for (const step of steps) {
     if (step['then']) refs.push(step['then'] as string)
     if (step['else']) refs.push(step['else'] as string)
     if (step['flow_name']) refs.push(step['flow_name'] as string)
     // Recurse into inline branches
     if (step.then_steps) refs.push(...collectFlowRefs(step.then_steps))
     if (step.else_steps) refs.push(...collectFlowRefs(step.else_steps))
     // ...existing cases handling
   }
   return refs.filter(Boolean)
 }

 Verification:
 1. Drag "cache_get" onto Then branch → step appears inside Then, NOT appended to main list
 2. Drag "token_validation" onto Else branch → appears inside Else
 3. Deploy → JSON preview should show then_steps/else_steps in Studio format; actual payload sent to gateway should have __auto_* fragment flows with
 string then/else references
 4. Existing flows with then: "flow_name" text still work unchanged

 ---
 Session [UI-4]: Flow Map Panel

 Goal: A visual hierarchy showing which flows call which other flows. Click any node to navigate to that flow in the designer.

 Files to change:
 - New: internal/studio/ui/src/components/FlowMap.tsx
 - internal/studio/ui/src/App.tsx (add tab or panel)

 What to build:

 1. Data structure: Build a call graph from savedFlows.
 // Node: a flow name
 // Edge: flow A has a 'call' step that references flow B, or
 //       flow A has an 'if' step with then/else pointing to flow B
 function buildCallGraph(savedFlows: SavedFlow[]): Map<string, string[]> {
   const graph = new Map<string, string[]>()
   for (const flow of savedFlows) {
     const refs = collectFlowRefs(flow.steps)  // reuse existing function from App.tsx
     graph.set(flow.name, [...new Set(refs)])
   }
   return graph
 }

 2. Entry points: Flows that are never referenced by any other flow are "root" flows.

 3. Tree rendering (recursive React component):
 FlowMapNode({ name, callGraph, onNavigate, depth, visited })
   Shows: [flow icon] {name} + step count + [open ↗ button]
   Children: callGraph.get(name) ?? [] → render sub-nodes (with depth limit + cycle detection via `visited` set)

 4. Panel location: Add a "Flow Map" toggle button at the top of the FlowDesigner panel (similar to how the JSON preview toggle works). Clicking it
 toggles a panel that appears alongside or above the canvas showing the call graph.

 5. Interaction: Clicking a node calls onNavigate(flowName) which:
 - Sets the active flow name in App.tsx to that flow
 - Loads that flow's steps into the designer
 - Switches to the 'flows' tab if not already there

 Verification:
 - Flow map shows correct hierarchy for flows created in the designer
 - Clicking a node navigates to that flow
 - Cycles in the graph (flow A calls flow B calls flow A) are detected and shown as a ⟳ indicator without infinite recursion
 - Works correctly with auto-generated __auto_* fragment flows (optionally hide these with a toggle)

 ---
 Session [OBS-1]: Show Upstream URL + Bytes in Trace View

 Goal: UpstreamEvent already captures full URL, bytes sent/received, and all timing phases. None of this is shown in the TraceTimeline. This session
 surfaces it.

 Files to change:
 - internal/studio/ui/src/components/Observability.tsx

 What's already captured (verified in telemetry.go):
 - UpstreamEvent.URL (string) — full URL
 - UpstreamEvent.BytesSent / BytesReceived (int64) — bytes
 - UpstreamEvent.DNSDurationNs, ConnectDurationNs, TLSDurationNs, TTFBNs — phase timing
 - UpstreamEvent.ConnReused, ConnIdle (bool) — connection reuse
 - UpstreamEvent.Attempt (int) — retry number
 - UpstreamEvent.Err (string) — error message

 These fields are already in TracePayloadEvent interface (or can be added without backend changes since they come through the JSON payload).

 What to build:

 In TraceTimeline (Observability.tsx:1347-1498), when rendering upstream events:
 1. Show the full URL below the upstream event bar (truncated to 60 chars with expand)
 2. Show bytes sent/received as ↑ 1.2KB ↓ 4.5KB
 3. Show retry number if attempt > 0 as retry #2
 4. Show error if err is non-empty (in red)
 5. On hover/expand: show full timing breakdown (DNS, TCP, TLS, TTFB) as a mini-bar or text list

 Also verify that TracePayloadEvent in the UI interface includes these fields. If it only has { name, duration_ns, status, output }, add the
 upstream-specific fields:
 // In Observability.tsx type definitions (~line 60-80):
 interface TracePayloadEvent {
   seq?: number
   name: string
   duration_ns?: number
   total_ns?: number
   status?: number
   output?: Array<{ k: string; v: string }>
   // Add these (upstream-only):
   url?: string
   bytes_sent?: int64
   bytes_received?: int64
   dns_ns?: number
   connect_ns?: number
   tls_ns?: number
   ttfb_ns?: number
   conn_reused?: boolean
   attempt?: number
   err?: string
 }

 Check telemetry.go to verify the JSON field names that UpstreamEvent uses — match them exactly.

 Verification:
 - Upstream events in the trace timeline show the full URL
 - Retry information visible when present
 - Errors shown in red
 - Bytes and phase timing available on expand

 ---
 Session [OBS-2]: Incoming Request Headers in Traces

 Goal: When a request is traced, capture the incoming HTTP headers and make them available in the trace. Show them in the trace expanded view as "What
 the client sent".

 Files to change:
 - internal/observability/telemetry.go — add RequestHeaders to the trace payload
 - internal/studio/ui/src/components/Observability.tsx — display incoming headers

 Gateway change (minimal, zero hot-path cost):

 In telemetry.go, the TracePayload or RequestSummary struct (verify exact name) that is exported via /api/observability/traces:

 Add a RequestHeaders map[string]string field. Populate it ONLY inside the tracing code path (when obs.TraceEnabled() is true for this request). Standard
  pattern already used in telemetry — the existing StartRequest() or equivalent function receives the *http.Request and should have access to headers at
 that point.

 Important: Add a config flag to control which headers are captured (to avoid leaking auth tokens by default):
 - Default: capture Content-Type, Accept, method, path, query string only
 - Config option RAH_TRACE_CAPTURE_HEADERS (comma-separated list, or * for all, default: Content-Type,Accept)
 - Auth headers (Authorization, X-API-Key) are NEVER captured unless explicitly opted in

 UI changes (Observability.tsx):

 In the trace expanded row (lines 971-1046), add an "Incoming Request" section before the BlockView/Timeline:
 - Shows: method, path, query params (already available via summary), then the captured headers
 - Renders as a compact key-value table
 - Section collapsible

 Verification:
 - Trace shows incoming Content-Type and Accept headers
 - Auth headers NOT shown (excluded by default)
 - When RAH_TRACE_CAPTURE_HEADERS=* configured, all headers appear
 - Performance: no overhead when trace_mode=false

 ---
 Session [OBS-3]: Outgoing Request Headers + Response Capture

 Goal: When http_call steps run during a traced request, capture the headers sent to the upstream and the response received. Show them in the trace view.

 Files to change:
 - internal/observability/telemetry.go — add fields to UpstreamEvent
 - internal/engine/steps/http.go — populate new fields when tracing
 - internal/studio/ui/src/components/Observability.tsx — display outgoing headers + response

 Gateway change:

 In telemetry.go, add to UpstreamEvent:
 RequestHeaders  map[string]string `json:"req_headers,omitempty"`
 ResponseHeaders map[string]string `json:"res_headers,omitempty"`
 ResponseBody    string            `json:"res_body,omitempty"` // truncated to 1KB

 In internal/engine/steps/http.go, in the HTTP call execution path, when tracing is active:
 - Before the call: capture the headers being sent
 - After the call: capture the response status, headers, and first 1KB of body
 - These are added to the UpstreamEvent that gets queued

 This is in the Execute() method of the http_call step. The tracing active check already exists in this file — the pattern is to check a flag before
 doing the extra work.

 UI changes (Observability.tsx):

 In the upstream event expanded view (which [OBS-1] will have added), show:
 - "Headers sent" section: key-value table of outgoing headers
 - "Response received" section: status + response headers + body preview (truncated, expandable)

 Verification:
 - Upstream call shows headers that were sent (e.g., Content-Type, custom headers from headers_json field)
 - Authorization header is shown as Authorization: *** (redacted) — always redact auth
 - Response body preview shown (first 1KB), with "show full" for longer responses
 - No overhead when tracing is disabled

 ---
 Session [OBS-4]: Flow-Aligned Trace View

 Goal: When viewing a trace, show it structured as the flow steps the user built (not raw instruction names). Each flow step shows its total time, and if
  it took a branch, show which branch was taken.

 This is the "observability view like the flow map" the user asked for.

 Files to change:
 - internal/engine/executor.go — add StepIdx int16 to Instruction (compile-time metadata)
 - internal/control/compiler.go — set StepIdx when emitting instructions in compileStep
 - internal/observability/telemetry.go — pass StepIdx through to InstructionEvent
 - internal/studio/ui/src/components/Observability.tsx — new "Flow View" trace panel

 Gateway change — Instruction metadata (zero runtime overhead):

 In internal/engine/executor.go, find the Instruction struct. Add:
 StepIdx int16 `json:"-"`  // Source flow step index (-1 if not from a user step)

 In internal/control/compiler.go, in compileStep(step StepConfig, ...), each c.GlobalTable = append(c.GlobalTable, ...) call emits an instruction. Before
  appending, set the instruction's StepIdx to the step's position in the flow. Since bakeFlow is called with a flow []StepConfig and iterates with index
 i, the index is available.

 Pass StepIdx through to InstructionEvent:
 type InstructionEvent struct {
   Seq      uint32
   Name     string
   PC       int16
   StepIdx  int16  // ADD THIS
   DurationNs int64
   Input    []KV
   Output   []KV
 }

 The StepIdx is set at trace time from the instruction's metadata. Since it's set at compile time, there is zero runtime computation — it's just a read.

 UI changes (Observability.tsx):

 Add a new "Flow View" tab/toggle to the trace expanded row alongside the existing "Timeline" and "Blocks" views:

 The Flow View:
 1. Groups InstructionEvent items by StepIdx
 2. For each step group: shows step name (instruction names → friendly name via existing DISPLAY_NAMES map), total duration (sum of all instructions for
 that step index), and a bar
 3. If the step is an if step: shows which branch was taken (detectable from which instructions ran)
 4. Sub-flow calls (call instruction): shown as a collapsible section with the sub-flow's step list
 5. Upstream calls that happened within a step: shown nested under that step

 This gives the user the flow-level timing overview they need.

 Verification:
 - "Flow View" tab appears on trace expanded view
 - Steps match the flow designer's step sequence
 - Each step shows combined duration for all its instructions
 - If/else shows which branch was taken
 - Upstream calls nested under the http_call step that triggered them

 ---
 Session [OBS-5]: Per-Step Variable Selector

 Goal: Users can configure, per flow, which variables they want to see in traces. Instead of the current binary "Capture in trace" checkbox, provide a
 selector to choose specific input and output variables per step.

 Context: The existing "Capture in trace" checkbox (FlowDesigner.tsx:562-633) works for the step's primary output (as field). But users often want to
 trace multiple variables, or trace an input variable that was set 3 steps earlier.

 Files to change:
 - internal/studio/ui/src/components/FlowDesigner.tsx
 - internal/studio/ui/src/types.ts (if step labels/config needs extending)

 What to build:

 Replace the current "Capture in trace" checkbox with an expandable "Trace Configuration" panel at the bottom of each step:

 ┌─ Trace & Log ──────────────────────────────────────────────┐
 │  Log output as:  [field_name___]  (access log field name)  │
 │  Capture for trace:                                         │
 │  [+ Add variable]                                           │
 │  • var.valid_hit         [read] [delete]                    │
 │  • var.upstream_url      [read] [delete]                    │
 └────────────────────────────────────────────────────────────┘

 Implementation:
 1. Store per-step trace variable list in a new stepTraceVars: Record<string, string[]> state in FlowDesigner (key = step index, value = variable names
 to trace)
 2. On compile/save: for each step with trace vars, emit a trace_capture instruction for each variable. The trace_capture step already accepts a slot
 index — use getSlot(varName) style lookup.
 3. The "Add variable" picker uses slotsUpTo(i) to show available variables from prior steps

 Important consideration: Ask the user (in the UI) whether they want to trace ONLY on error, ONLY on slow requests (>threshold), or always. These map to
 the existing sample_rate config. Add a step-level hint:
 Capture mode: [Always] [On error] [On slow (>___ms)]

 This is a UI-only change — the existing trace_capture instruction and sample_rate mechanism handle the filtering.

 Verification:
 - Each step can have multiple trace variables configured
 - Variables available for selection are only those set by prior steps (smart dropdown)
 - Configured variables appear in trace view alongside the step's data
 - Old "Capture in trace" behavior (single checkbox) still works for backward compat

 ---
 Section 3 — What Each Session Needs From Context (for future Claude sessions)

 When executing a session, the implementing agent needs to read:

 ┌─────────┬──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
 │ Session │                                                      Must Read Before Implementing                                                       │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ UI-1    │ App.tsx full (sidebar section lines 224-299)                                                                                             │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ UI-2    │ FlowDesigner.tsx lines 28-142, 168-175, 1345-1481                                                                                        │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ UI-3    │ types.ts:78, expressions.ts:122-169, FlowDesigner.tsx:240-350, App.tsx deploy handler                                                    │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ UI-4    │ App.tsx:39-54 (collectFlowRefs), FlowDesigner.tsx canvas area, types.ts (SavedFlow)                                                      │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ OBS-1   │ Observability.tsx:1347-1498 (TraceTimeline), telemetry.go (UpstreamEvent struct fields + JSON tags)                                      │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ OBS-2   │ telemetry.go (StartRequest + RequestSummary), Observability.tsx:971-1046 (trace expanded row)                                            │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ OBS-3   │ engine/steps/http.go (Execute method), telemetry.go (UpstreamEvent), Observability.tsx (upstream rendering)                              │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ OBS-4   │ engine/executor.go (Instruction struct), control/compiler.go (compileStep + bakeFlow), telemetry.go (InstructionEvent),                  │
 │         │ Observability.tsx (trace tabs)                                                                                                           │
 ├─────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ OBS-5   │ FlowDesigner.tsx:562-633 (existing obs footer), types.ts (StepLabels pattern), compiler.go (trace_capture emission)                      │
 └─────────┴──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘

 ---
 Section 4 — What Is NOT Changing

 To be explicit about scope boundaries:

 - Gateway hot path: Zero changes to request processing. All additions are either compile-time (StepIdx in Instruction) or trace-only (only active when
 trace_mode=true, disabled for production).
 - Gateway compiler logic: Only OBS-4 adds compile-time metadata. No behavior changes.
 - Existing flow format for gateway: UI-3's flattenForDeploy ensures the gateway continues to receive flat string references.
 - Existing observability API contracts: All additions are additive (new fields, never removing).
 - Backend flow storage: Flows continue to be stored as SavedFlow (with the new optional then_steps/else_steps). Old flows load without change.

 ---
 ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
 Section 5 — Flow Designer Phase 2: Navigation, Sub-flows & Graph View

 ▎ Every session is self-sufficient. Start a FRESH conversation per session. Read ONLY the listed files.
 ▎ Each session leaves the system in a fully working state — nothing half-built.
 ▎ Gateway and backend are never touched unless the session explicitly says so.
 ▎ Current linear editor and all existing flows remain working throughout.

 Already completed (do NOT re-implement):
   OBS-4 (StepIdx): executor.go:55 has StepIdx, compiler.go:139-142 tags it in bakeFlow,
                    telemetry.go:92 emits it, Observability.tsx:62 receives it.
   FlowView:        Observability.tsx:1771 — FlowView component fully built, groups by step_idx,
                    shows branch detection and per-step timing. Defaults to this view when data present.

 ---
 HOW TO USE THIS PLAN — BUDGET & MODEL GUIDANCE

 Context cost is driven by two things: (1) how many files you load, (2) how long the conversation runs.
 The single biggest saving is starting a fresh conversation for every session.

 Model choice per session:
   [HAIKU OK] — single file, <30 lines to change, fully mechanical. Set with:
                 /model claude-haiku-4-5-20251001
   [SONNET]   — multi-file or requires reasoning about side-effects. Default model.

 Per-session discipline:
   1. Open a new conversation.
   2. Paste the session block from this plan as your first message (it is self-contained).
   3. Claude reads only the listed files — do not ask it to explore the codebase.
   4. After the change verify manually: cd internal/studio/ui && npm run build, then check in browser.
   5. Do not continue into a different session — start a new conversation.

 ---
 Session [FIX-1]: Restore then/else inputs in the if step editor    [HAIKU OK]

 Goal: renderIfBody (FlowDesigner.tsx) only shows the condition field. The then and else
 string flow-name fields are invisible, making old flows look broken and preventing users
 from typing a flow name without using inline drop zones. Fix: when a branch has no inline
 steps show the string text input; when it has inline steps show the branch editor.
 The two modes are mutually exclusive per branch — no data is lost either way.

 Files to read:
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 431–441   renderIfBody
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 259–261   updateStep (confirm spreads all keys)
   internal/control/step_descriptors.go                lines 312–321   if step field definitions

 File to change:
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 431–441 only

 Current code:
   function renderIfBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
     const thenSteps = (step.then_steps as FlowStep[]) ?? []
     const elseSteps = (step.else_steps as FlowStep[]) ?? []
     return (
       <div className="step-body">
         {fieldInput(i, 'condition', (step['condition'] as string) ?? '', defs['condition'], { smart: true })}
         {renderBranch('✓ THEN', false, thenSteps, i, 'then_steps')}
         {renderBranch('✗ ELSE', true,  elseSteps, i, 'else_steps')}
       </div>
     )
   }

 Replace with:
   function renderIfBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
     const thenSteps = (step.then_steps as FlowStep[]) ?? []
     const elseSteps = (step.else_steps as FlowStep[]) ?? []
     return (
       <div className="step-body">
         {fieldInput(i, 'condition', (step['condition'] as string) ?? '', defs['condition'], { smart: true })}
         {thenSteps.length > 0
           ? renderBranch('✓ THEN', false, thenSteps, i, 'then_steps')
           : fieldInput(i, 'then', (step['then'] as string) ?? '', defs['then'])}
         {elseSteps.length > 0
           ? renderBranch('✗ ELSE', true, elseSteps, i, 'else_steps')
           : fieldInput(i, 'else', (step['else'] as string) ?? '', defs['else'])}
       </div>
     )
   }

 Why this is safe:
   - updateStep spreads all existing keys: { ...s, [key]: value } — no data loss.
   - defs['then'] and defs['else'] come from the schema API. If schema not yet loaded,
     fieldInput handles undefined def gracefully (uses the key name as label).
   - Existing steps with then_steps / else_steps inline content: renderBranch still shown.
   - Existing steps with then: "flow_name" string: now visible and editable again.

 Verification:
   1. New if step → condition + two text inputs (then, else) appear.
   2. Type a flow name in then → value persists on collapse/expand.
   3. Load an old flow with then: "some_flow" → value shows in the input (not blank).
   4. Drop a step into the THEN branch drop zone (if inline branch UI present) →
      branch editor shows, text input gone for that branch. Else branch still shows text input.
   5. Leaving else blank is fine — no validation error.

 Do NOT touch: renderSwitchBody, renderBranch, addToBranch, or any other function.

 ---
 Session [NAV-1]: Breadcrumb navigation when drilling into sub-flows   [SONNET]

 Goal: When a user clicks a flow in FlowMap or presses Open ↗ (NAV-2), a breadcrumb appears
 above the canvas. Each breadcrumb entry is clickable and jumps directly to that flow in the
 stack. Back-stack is maintained so navigating to parent restores its steps exactly.

 Context — what already exists (verify by reading files before coding):
   App.tsx line 172: navigateToDesigner(name?) — loads a flow by name, no stack tracking yet.
   App.tsx line 368: onNavigateToFlow={navigateToDesigner} — wired to FlowDesigner.
   App.tsx line 378: onNavigateToDesigner={navigateToDesigner} — second call site.
   FlowDesigner.tsx line 14: onNavigateToFlow?: (name: string) => void — prop exists.
   FlowMap.tsx line 166: onNavigate: (name: string) => void — already calls onNavigateToFlow.
   Adding a second optional parameter to navigateToDesigner is backward-compatible with both
   call sites at lines 368 and 378 since they pass only name.

 Files to read:
   internal/studio/ui/src/App.tsx                lines 93–182   state + navigateToDesigner
   internal/studio/ui/src/App.tsx                lines 360–385  FlowDesigner JSX props
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 1–16    Props interface
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 155–175 state declarations area

 Files to change:
   internal/studio/ui/src/App.tsx
   internal/studio/ui/src/components/FlowDesigner.tsx

 Step 1 — App.tsx: breadcrumb stack state.

 Add after existing state declarations (around line 115):
   const [flowNavStack, setFlowNavStack] = useState<Array<{ name: string; steps: FlowStep[] }>>([])

 Replace navigateToDesigner (lines 172–182) with:
   function navigateToDesigner(name?: string, pushCurrent = false) {
     if (name) {
       if (pushCurrent && flowName.trim()) {
         setFlowNavStack(prev => [...prev, { name: flowName, steps: [...steps] }])
       } else if (!pushCurrent) {
         setFlowNavStack([])
       }
       const saved = savedFlows.find(f => f.name === name)
       setFlowName(name)
       setSteps(saved?.steps ?? [])
     } else {
       setFlowNavStack([])
       setFlowName('')
       setSteps([])
     }
     setTab('flows')
   }

 Add navigateToStackIndex (jumps directly to any breadcrumb level):
   function navigateToStackIndex(targetIdx: number) {
     // targetIdx is index in flowNavStack; restore that entry and truncate stack above it.
     const entry = flowNavStack[targetIdx]
     if (!entry) return
     setFlowNavStack(prev => prev.slice(0, targetIdx))
     setFlowName(entry.name)
     setSteps(entry.steps)
   }

 Update the FlowDesigner JSX to pass:
   navStack={flowNavStack}
   onNavigateToStackIndex={navigateToStackIndex}
   onNavigateToFlow={name => navigateToDesigner(name, true)}
   (The line 368 onNavigateToFlow call is replaced by the new arrow above.)

 Step 2 — FlowDesigner.tsx: add props and render breadcrumb.

 Add to Props interface:
   navStack?: Array<{ name: string }>
   onNavigateToStackIndex?: (idx: number) => void

 Find the flow name input (search for value={flowName} input). Just above it render:
   {(navStack?.length ?? 0) > 0 && (
     <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 8,
                   fontSize: 11, flexWrap: 'wrap' }}>
       {navStack!.map((entry, idx) => (
         <span key={idx} style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
           <button
             style={{ background: 'none', border: 'none', padding: '2px 4px', cursor: 'pointer',
                      color: 'var(--accent)', fontSize: 11, fontFamily: 'inherit',
                      borderRadius: 3, textDecoration: 'underline' }}
             onClick={() => onNavigateToStackIndex?.(idx)}
           >
             {entry.name}
           </button>
           <span style={{ color: 'var(--muted)' }}>›</span>
         </span>
       ))}
       <span style={{ color: 'var(--fg)', fontWeight: 600 }}>{flowName}</span>
     </div>
   )}

 Why clicking entry at idx works: navigateToStackIndex(idx) restores that entry's steps
 and slices the stack to [...stack.slice(0, idx)] — removing that entry and everything above it.
 So clicking "main" in "main › auth › jwt" correctly goes back to main with main's steps,
 and the stack becomes empty (correct — main is the root).

 Verification:
   1. Click a flow in FlowMap → breadcrumb shows "parent › current".
   2. Click parent in breadcrumb → restores parent's steps, breadcrumb clears.
   3. Three levels deep: "main › auth › jwt" → clicking "main" goes directly to main.
   4. Clicking a flow from the sidebar (not via FlowMap) → breadcrumb clears (top-level nav).
   5. All existing flows load, save, and deploy unchanged.

 Do NOT touch: saveCurrentFlow, apis state, deploy logic, or Observability.

 ---
 Session [NAV-2]: if/else and switch show branch targets as clickable links   [HAIKU OK]

 Goal: In the if step editor, when then/else contain a flow name, show an "Open ↗" button
 beside the text input. Clicking it drills into that flow. Same for switch case targets.
 A "not found" badge shows when the referenced flow doesn't exist in savedFlows.

 Prerequisites: FIX-1 and NAV-1 must be complete.

 Files to read:
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 1–16      Props (confirm onNavigateToFlow)
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 431–450   renderIfBody after FIX-1
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 443–491   renderSwitchBody

 File to change:
   internal/studio/ui/src/components/FlowDesigner.tsx

 Add FlowRefInput helper function (add just before renderIfBody, inside the component):

   function FlowRefInput({ stepIdx, fieldKey, value, label, def }: {
     stepIdx: number; fieldKey: string; value: string; label: string; def?: FieldDef
   }) {
     const exists = savedFlows.some(f => f.name === value)
     return (
       <div className="field-row">
         <label className="field-label">{label}</label>
         <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
           <input className="input" style={{ flex: 1 }}
             value={value}
             placeholder={def?.placeholder ?? fieldKey}
             onChange={e => updateStep(stepIdx, fieldKey, e.target.value)} />
           {value && exists && onNavigateToFlow && (
             <button
               style={{ padding: '4px 8px', fontSize: 11, whiteSpace: 'nowrap', cursor: 'pointer',
                        background: 'rgba(var(--accent-rgb,87,181,255),0.12)',
                        border: '1px solid var(--accent)', borderRadius: 4, color: 'var(--accent)' }}
               onClick={() => onNavigateToFlow!(value)}
               title={`Open flow: ${value}`}
             >Open ↗</button>
           )}
           {value && !exists && (
             <span style={{ fontSize: 10, color: '#f59e0b', whiteSpace: 'nowrap' }}>not found</span>
           )}
         </div>
         {def?.description && <span className="field-desc">{def.description}</span>}
       </div>
     )
   }

 In renderIfBody: replace the two fieldInput calls (added in FIX-1) with FlowRefInput:
   {thenSteps.length > 0
     ? renderBranch('✓ THEN', false, thenSteps, i, 'then_steps')
     : <FlowRefInput stepIdx={i} fieldKey="then" value={(step['then'] as string) ?? ''}
         label="Then → flow" def={defs['then']} />}
   {elseSteps.length > 0
     ? renderBranch('✗ ELSE', true, elseSteps, i, 'else_steps')
     : <FlowRefInput stepIdx={i} fieldKey="else" value={(step['else'] as string) ?? ''}
         label="Else → flow" def={defs['else']} />}

 In renderSwitchBody: find the case-flow <input> (second input in switch-case-row).
 After that input, add an inline navigate button:
   {c.flow && savedFlows.some(f => f.name === c.flow) && onNavigateToFlow && (
     <button
       style={{ padding: '3px 7px', fontSize: 11, cursor: 'pointer',
                background: 'rgba(var(--accent-rgb,87,181,255),0.12)',
                border: '1px solid var(--accent)', borderRadius: 4, color: 'var(--accent)' }}
       onClick={() => onNavigateToFlow!(c.flow)}
       title={`Open flow: ${c.flow}`}
     >↗</button>
   )}

 Why FlowRefInput accesses savedFlows/updateStep/onNavigateToFlow:
   All three are in scope — savedFlows is a prop, updateStep and onNavigateToFlow are
   defined in the same component. FlowRefInput is defined inside the component, so it
   closes over them. This is the same pattern used by renderCacheBody and renderHttpCallBody.

 Verification:
   1. If step, then="some_flow" (exists) → "Open ↗" button appears, clicking it navigates.
   2. If step, then="missing_flow" (not in savedFlows) → "not found" badge, no button.
   3. If step, then="" → no badge, no button.
   4. Switch case with valid flow target → ↗ per case row.
   5. Typing in the input still updates the step. No regression on existing editors.

 Do NOT touch: renderBranch, renderCacheBody, renderHttpCallBody, or any other editor.

 ---
 Session [SUB-1]: Multi-select steps in the canvas   [HAIKU OK]

 Goal: Add checkboxes to step cards. When steps are checked, an action bar appears with
 a count and an "Extract as sub-flow…" button. This session is UI-only — no extraction
 logic. The button does nothing until SUB-2.

 Files to read:
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 155–180   state declarations
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 263–270   removeStep (to add clearSelection)
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 1633–1665 step card rendering loop

 File to change:
   internal/studio/ui/src/components/FlowDesigner.tsx

 Step 1 — Add selection state near other useState hooks (around line 175):
   const [selectedSteps, setSelectedSteps] = useState<Set<number>>(new Set())

   function toggleSelect(i: number) {
     setSelectedSteps(prev => {
       const next = new Set(prev)
       next.has(i) ? next.delete(i) : next.add(i)
       return next
     })
   }
   function clearSelection() { setSelectedSteps(new Set()) }

 Step 2 — Add clearSelection() at end of removeStep function (line 263):
   function removeStep(i: number) {
     setSteps(steps.filter((_, idx) => idx !== i))
     setExpanded(prev => {
       const next = new Set<number>()
       prev.forEach(idx => { if (idx < i) next.add(idx); else if (idx > i) next.add(idx - 1) })
       return next
     })
     clearSelection()   // ← add this line
   }

 Step 3 — Add checkbox as first child of step-header div (inside the step map at line 1638):
   <input
     type="checkbox"
     checked={selectedSteps.has(i)}
     onChange={e => { e.stopPropagation(); toggleSelect(i) }}
     onClick={e => e.stopPropagation()}
     style={{ cursor: 'pointer', flexShrink: 0, accentColor: 'var(--accent)' }}
   />
   The e.stopPropagation() on both events prevents the header click (expand/collapse) from firing.

 Step 4 — Add action bar just before the Save/Clear buttons div (search for "Save to My Flows"):
   {selectedSteps.size > 0 && (
     <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 10px',
                   background: 'rgba(var(--accent-rgb,87,181,255),0.08)',
                   border: '1px solid var(--accent)', borderRadius: 6, marginTop: 8 }}>
       <span style={{ fontSize: 12, flex: 1, color: 'var(--accent)' }}>
         {selectedSteps.size} step{selectedSteps.size > 1 ? 's' : ''} selected
       </span>
       <button className="btn muted" style={{ fontSize: 11 }} onClick={clearSelection}>
         Clear
       </button>
       <button className="btn" style={{ fontSize: 11 }}
         onClick={() => { /* wired in SUB-2 */ }}>
         Extract as sub-flow…
       </button>
     </div>
   )}

 Verification:
   1. Each step card has a checkbox — clicking it does not expand/collapse the card.
   2. Checking steps shows the action bar with correct count.
   3. "Clear" unchecks all and hides the action bar.
   4. "Extract as sub-flow…" is visible but does nothing.
   5. Removing a step clears the entire selection (no stale indices).
   6. All existing editors, expand/collapse, and reorder work unchanged.

 Do NOT touch: any render*Body function, the palette, FlowMap, or App.tsx.

 ---
 Session [SUB-2]: Extract selected steps as a named sub-flow   [SONNET]

 Goal: Wire the "Extract as sub-flow…" button (SUB-1). A small inline dialog asks for a name.
 On confirm: selected steps are moved into a new saved flow, replaced in the parent flow by
 a single call step at the position of the first selected step.

 Prerequisite: SUB-1 must be complete.

 Known limitation (document in UI): non-contiguous selection (e.g. steps 1 and 3, skipping 2)
 is allowed but the extracted sub-flow will contain those steps executing sequentially, which
 may not match intent. Guide users to prefer contiguous selection.

 Files to read:
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 155–180   state declarations
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 259–295   mutation helpers
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 1–16      Props interface
   internal/studio/ui/src/App.tsx                      lines 120–155   savedFlows mutation functions
   internal/studio/ui/src/App.tsx                      lines 360–390   FlowDesigner JSX props
   internal/studio/ui/src/types.ts                     lines 1–30      SavedFlow, FlowStep types
   internal/control/step_descriptors.go                line 344        call step uses "flow_name" key

 Files to change:
   internal/studio/ui/src/components/FlowDesigner.tsx
   internal/studio/ui/src/App.tsx

 Step 1 — App.tsx: add onCreateSubFlow prop and wire it.
 Pass to FlowDesigner:
   onCreateSubFlow={(name, subSteps) => {
     setSavedFlows(prev => {
       if (prev.some(f => f.name === name)) return prev   // guard: do not overwrite existing
       return [...prev, { name, steps: subSteps }]
     })
   }}

 Step 2 — FlowDesigner.tsx Props: add:
   onCreateSubFlow?: (name: string, steps: FlowStep[]) => void

 Step 3 — FlowDesigner.tsx: add state for the dialog:
   const [extractName, setExtractName] = useState('')
   const [showExtractDialog, setShowExtractDialog] = useState(false)

 Step 4 — doExtract function (add near updateStep helpers):
   function doExtract() {
     const name = extractName.trim()
     if (!name) return
     const indices = Array.from(selectedSteps).sort((a, b) => a - b)
     const subSteps = indices.map(idx => steps[idx])
     const callStep: FlowStep = { action: 'call', flow_name: name }
     const insertAt = indices[0]
     // Remove selected steps from the array, insert call step at first selected position.
     // After filtering, all items originally before insertAt that were NOT selected
     // remain at the same positions in the new array, so splice(insertAt, 0, callStep) is correct.
     const next = steps.filter((_, idx) => !selectedSteps.has(idx))
     next.splice(insertAt, 0, callStep)
     setSteps(next)
     onCreateSubFlow?.(name, subSteps)
     clearSelection()
     setShowExtractDialog(false)
     setExtractName('')
   }

 Step 5 — Inline dialog (add just above the action bar from SUB-1):
   {showExtractDialog && (
     <div style={{ padding: '10px 12px', background: 'var(--surface)',
                   border: '1px solid var(--border)', borderRadius: 6, marginTop: 8 }}>
       <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 6 }}>Name the sub-flow</div>
       <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 8 }}>
         Tip: select a contiguous range of steps for predictable results.
       </div>
       <input className="input" autoFocus placeholder="e.g. fetch_flow"
         value={extractName} onChange={e => setExtractName(e.target.value)}
         onKeyDown={e => { if (e.key === 'Enter') doExtract(); if (e.key === 'Escape') {
           setShowExtractDialog(false); setExtractName('') }}} />
       <div style={{ display: 'flex', gap: 6, marginTop: 8 }}>
         <button className="btn muted" style={{ flex: 1 }}
           onClick={() => { setShowExtractDialog(false); setExtractName('') }}>Cancel</button>
         <button className="btn" style={{ flex: 1 }}
           disabled={!extractName.trim()} onClick={doExtract}>Extract</button>
       </div>
     </div>
   )}

 Step 6 — Wire the Extract button onClick (in action bar from SUB-1):
   onClick={() => setShowExtractDialog(true)}

 Verification:
   1. Select steps 2–4, click Extract, type "fetch_flow", confirm.
   2. Canvas: step 1, call:fetch_flow (at position 2), step 5+.
   3. FlowMap shows fetch_flow as child of current flow (it's referenced via call).
   4. Clicking fetch_flow in FlowMap opens it with the 3 extracted steps.
   5. Name already exists: extraction creates the call step but does NOT overwrite the
      existing saved flow (onCreateSubFlow guard).
   6. Cancel: no changes to steps or savedFlows.
   7. Enter key submits dialog. Escape cancels.

 Do NOT touch: expandSteps, flattenForDeploy, deploy path, Observability.

 ---
 Session [GRAPH-1]: Interactive graph canvas — flow-level view   [SONNET]

 Goal: A "Graph" toggle in the FlowDesigner canvas switches from the linear step list to
 a 2D SVG node graph. Nodes are saved flows; edges are call/then/else/case references.
 Click any node to navigate to that flow (using NAV-1 breadcrumb). Read-only visualization;
 editing still happens in the linear view.

 Prerequisite: NAV-1 must be complete.

 Important — shared helpers: buildCallGraph and collectFlowRefs are currently private
 functions inside FlowMap.tsx. Before writing GRAPH-1, first export them from FlowMap.tsx:
   export function collectFlowRefs(steps: FlowStep[]): string[] { ... }
   export function buildCallGraph(flows: SavedFlow[]): Map<string, string[]> { ... }
 Then import them in FlowDesigner.tsx:
   import { buildCallGraph, collectFlowRefs } from './FlowMap'
 Do NOT duplicate these functions — they must stay in one place.

 Files to read:
   internal/studio/ui/src/components/FlowMap.tsx         full file     data model + helpers to export
   internal/studio/ui/src/components/FlowDesigner.tsx    lines 1580–1665  canvas area
   internal/studio/ui/src/components/FlowDesigner.tsx    lines 155–180    state declarations

 Files to change:
   internal/studio/ui/src/components/FlowMap.tsx        (export two functions)
   internal/studio/ui/src/components/FlowDesigner.tsx   (import + graph view)

 Step 1 — FlowMap.tsx: add export keyword to collectFlowRefs and buildCallGraph.
 Both are pure functions with no component dependencies. This is the only change to FlowMap.tsx.

 Step 2 — FlowDesigner.tsx: add view mode state:
   const [canvasView, setCanvasView] = useState<'steps' | 'graph'>('steps')

 Step 3 — Add toggle button in the toolbar above the canvas (near the JSON preview toggle):
   <button
     className={`btn${canvasView === 'graph' ? '' : ' muted'}`}
     style={{ fontSize: 11 }}
     onClick={() => setCanvasView(v => v === 'steps' ? 'graph' : 'steps')}
   >
     {canvasView === 'graph' ? '≡ Steps' : '⬡ Graph'}
   </button>

 Step 4 — Layout helper (add before component return, outside the component function):
   function computeGraphLayout(
     flows: SavedFlow[],
     callGraph: Map<string, string[]>
   ): Map<string, { x: number; y: number }> {
     const referenced = new Set<string>()
     for (const [, refs] of callGraph) refs.forEach(r => referenced.add(r))
     const roots = flows.filter(f => !referenced.has(f.name)).map(f => f.name)
     // BFS to assign layers
     const layers = new Map<string, number>()
     const queue: Array<{ name: string; layer: number }> =
       (roots.length > 0 ? roots : flows.map(f => f.name)).map(name => ({ name, layer: 0 }))
     while (queue.length > 0) {
       const { name, layer } = queue.shift()!
       if (layers.has(name)) continue
       layers.set(name, layer)
       for (const child of callGraph.get(name) ?? []) {
         queue.push({ name: child, layer: layer + 1 })
       }
     }
     // Any flows not reached by BFS (fully disconnected): place at layer 0
     flows.forEach(f => { if (!layers.has(f.name)) layers.set(f.name, 0) })
     // Collect nodes per layer, assign y positions
     const perLayer = new Map<number, string[]>()
     for (const [name, layer] of layers) {
       if (!perLayer.has(layer)) perLayer.set(layer, [])
       perLayer.get(layer)!.push(name)
     }
     const pos = new Map<string, { x: number; y: number }>()
     for (const [layer, names] of perLayer) {
       names.forEach((name, i) => pos.set(name, { x: layer * 200 + 20, y: i * 64 + 20 }))
     }
     return pos
   }

 Step 5 — Graph render (inside component, replace canvas content when canvasView === 'graph'):
   Compute inside the render:
     const graphCallGraph = useMemo(() => buildCallGraph(savedFlows), [savedFlows])
     const graphLayout    = useMemo(() => computeGraphLayout(savedFlows, graphCallGraph), [savedFlows, graphCallGraph])

   const NODE_W = 130, NODE_H = 36
   const maxX = Math.max(...[...graphLayout.values()].map(p => p.x), 0) + NODE_W + 20
   const maxY = Math.max(...[...graphLayout.values()].map(p => p.y), 0) + NODE_H + 20

   SVG structure:
   <div style={{ overflowX: 'auto', overflowY: 'auto', maxHeight: 420, border: '1px solid var(--border)', borderRadius: 6 }}>
     <svg width={maxX} height={maxY} style={{ display: 'block' }}>
       {/* Edges — draw first so nodes render on top */}
       {[...graphCallGraph.entries()].flatMap(([parent, children]) =>
         children.map(child => {
           const p = graphLayout.get(parent); const c = graphLayout.get(child)
           if (!p || !c) return null
           const x1 = p.x + NODE_W, y1 = p.y + NODE_H / 2
           const x2 = c.x,          y2 = c.y + NODE_H / 2
           const mx = (x1 + x2) / 2
           return (
             <path key={`${parent}-${child}`}
               d={`M ${x1} ${y1} C ${mx} ${y1} ${mx} ${y2} ${x2} ${y2}`}
               fill="none" stroke="rgba(255,255,255,0.15)" strokeWidth={1.5} />
           )
         })
       )}
       {/* Nodes */}
       {savedFlows.map(flow => {
         const p = graphLayout.get(flow.name)
         if (!p) return null
         const isActive  = flow.name === flowName
         const isAuto    = flow.name.startsWith('__auto_')
         return (
           <g key={flow.name} style={{ cursor: 'pointer' }}
             onClick={() => onNavigateToFlow?.(flow.name)}>
             <rect x={p.x} y={p.y} width={NODE_W} height={NODE_H} rx={5}
               fill={isActive ? 'rgba(var(--accent-rgb,87,181,255),0.15)' : 'rgba(255,255,255,0.04)'}
               stroke={isActive ? 'var(--accent)' : 'rgba(255,255,255,0.12)'}
               strokeWidth={isActive ? 1.5 : 1} />
             <text x={p.x + NODE_W / 2} y={p.y + NODE_H / 2 + 4}
               textAnchor="middle" fontSize={11} fill={isAuto ? 'var(--muted)' : 'var(--fg)'}
               fontStyle={isAuto ? 'italic' : 'normal'}
               fontFamily="inherit">
               {flow.name.length > 16 ? flow.name.slice(0, 15) + '…' : flow.name}
             </text>
           </g>
         )
       })}
     </svg>
   </div>

 Step 6 — Conditional rendering in canvas:
   When canvasView === 'graph': show the SVG above, hide the step list div and the selection
   action bar. Keep palette, flow name input, Save/Clear buttons, and JSON preview visible.

 Verification:
   1. "⬡ Graph" button appears. Clicking shows SVG graph with all saved flows as nodes.
   2. Current flow has accent-colored border. Auto-generated flows (starting __auto_) are italic.
   3. Click a node → navigates to that flow, breadcrumb updates (NAV-1).
   4. "≡ Steps" returns to the linear canvas with all steps intact.
   5. Flows with no connections appear as isolated nodes at layer 0.
   6. Cyclic references: edges render but BFS terminates (layers.has check). No infinite loop.
   7. Graph scrolls when wider/taller than the canvas container.
   8. FlowMap.tsx still works as the sidebar tree — no regression.

 Do NOT touch: FlowMap tree rendering, step editors, App.tsx, Observability.

 ---
 ---
 Session [NEST-1]: N-level deep drag-and-drop inside if/else branches   [SONNET]

 Goal: Steps dropped into a THEN/ELSE branch can themselves be if/else steps with their
 own droppable branches — to arbitrary depth. Currently only 1 level works.

 Why it is not trivial: updateBranchStep addresses steps[parentIdx][branch][ni][key] —
 one level deep. For n-levels a path is needed:
   type NestedPath = Array<{ branch: 'then_steps' | 'else_steps'; idx: number }>
 Every mutation (update, remove, add) must traverse this path to reach the right node.

 Data model: already supports n-levels — FlowStep is recursive (then_steps?: FlowStep[]).
 No type changes needed.

 Files to read:
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 272–295   updateBranchStep / removeBranchStep / addToBranch
   internal/studio/ui/src/components/FlowDesigner.tsx  lines 343–437   renderNestedStepCard + renderBranch
   internal/studio/ui/src/types.ts                     lines 78–90     FlowStep interface

 File to change:
   internal/studio/ui/src/components/FlowDesigner.tsx

 Step 1 — Replace the three path-unaware helpers with path-aware versions.

 Add a path type alias near the other state declarations:
   type BranchPath = Array<{ branch: 'then_steps' | 'else_steps'; idx: number }>

 Replace updateBranchStep, removeBranchStep, addToBranch with path-aware versions:

   function getNestedStep(root: FlowStep, path: BranchPath): FlowStep {
     let cur = root
     for (const seg of path) cur = ((cur[seg.branch] as FlowStep[]) ?? [])[seg.idx]
     return cur
   }

   function setNestedStep(root: FlowStep, path: BranchPath, updater: (s: FlowStep) => FlowStep): FlowStep {
     if (path.length === 0) return updater(root)
     const [head, ...tail] = path
     const arr = [...((root[head.branch] as FlowStep[]) ?? [])]
     arr[head.idx] = setNestedStep(arr[head.idx], tail, updater)
     return { ...root, [head.branch]: arr }
   }

   function updateNestedField(topIdx: number, path: BranchPath, key: string, value: unknown) {
     setSteps(steps.map((s, i) => i !== topIdx ? s
       : setNestedStep(s, path, step => ({ ...step, [key]: value }))))
   }

   function removeNestedStep(topIdx: number, path: BranchPath) {
     // path[-1] is the step to remove; path[0..-2] is its parent
     const parentPath = path.slice(0, -1)
     const last = path[path.length - 1]
     setSteps(steps.map((s, i) => i !== topIdx ? s
       : setNestedStep(s, parentPath, parent => {
           const arr = ((parent[last.branch] as FlowStep[]) ?? []).filter((_, j) => j !== last.idx)
           return { ...parent, [last.branch]: arr }
         })))
   }

   function addNestedStep(topIdx: number, parentPath: BranchPath, branch: 'then_steps' | 'else_steps', b: PaletteBlock) {
     setSteps(steps.map((s, i) => i !== topIdx ? s
       : setNestedStep(s, parentPath, parent => {
           const arr = [...((parent[branch] as FlowStep[]) ?? []), { action: b.type, ...b.defaults }]
           return { ...parent, [branch]: arr }
         })))
   }

 Step 2 — Update renderNestedStepCard and renderBranch to accept topIdx and path.

 Change signatures:
   renderNestedStepCard(step, ni, topIdx, path: BranchPath)
   renderBranch(label, isElse, branchSteps, topIdx, path: BranchPath, branch)

 In renderNestedStepCard:
   - key becomes path.map(s => `${s.branch}[${s.idx}]`).join('.') — unique across all depths
   - updateFn calls updateNestedField(topIdx, path, k, v)
   - remove button calls removeNestedStep(topIdx, path)
   - if step.action === 'if': after the fields, render renderBranch for then/else sub-branches:
       const thenSteps = (step.then_steps as FlowStep[]) ?? []
       const elseSteps = (step.else_steps as FlowStep[]) ?? []
       {thenSteps.length > 0 || true  // always show drop zones for if steps
         ? renderBranch('✓ THEN', false, thenSteps, topIdx,
             path,   // parent path (this if step)
             'then_steps')
         : null}
       // same for else_steps

 In renderBranch:
   - branchSteps.map((ns, ni) => renderNestedStepCard(ns, ni, topIdx,
       [...path, { branch, idx: ni }]))
   - Drop zone onDrop: calls addNestedStep(topIdx, path, branch, b)
     where path is the path TO the parent if step (not including the branch segment)

 Step 3 — Update existing call sites in renderIfBody (top-level if):
   renderBranch('✓ THEN', false, thenSteps, i, [], 'then_steps')
   renderBranch('✗ ELSE', true,  elseSteps, i, [], 'else_steps')
   (empty path [] = top level, topIdx = i)

 Also remove the old updateBranchStep / removeBranchStep / addToBranch functions entirely
 to avoid confusion — they are replaced by the path-aware versions above.

 Verification:
   1. Drag cache_get into THEN branch of an if step → appears, fields editable. ✓ (existing)
   2. Drag another if step into THEN branch → if step appears with its own THEN/ELSE drop zones.
   3. Drag cache_get into the nested if's THEN → appears correctly nested.
   4. Edit a field in a 3-levels-deep step → value persists on collapse/expand.
   5. Remove a nested step → removed from correct position, siblings unaffected.
   6. JSON preview shows correct nested then_steps/else_steps structure.
   7. flattenForDeploy handles it correctly (it already recurses — no change needed).

 Do NOT touch: renderCacheBody, renderTokenValidationBody, renderHttpCallBody, or any
 top-level step editor. Only renderNestedStepCard, renderBranch, and the three helpers change.

 ---
 Session dependency order and parallel tracks:

   FIX-1   (no deps)         ← do first, restores broken if/else UI, lowest risk [DONE ✓]
   NEST-1  (needs FIX-1)     ← n-level nesting, replaces 1-level helpers
   NAV-1   (no deps)         ← breadcrumb, additive only
   NAV-2   (needs FIX-1 + NAV-1)
   SUB-1   (no deps)         ← checkbox UI only, no logic change
   SUB-2   (needs SUB-1)
   GRAPH-1 (needs NAV-1)     ← also needs FlowMap.tsx export change (safe, no behavior change)

 Parallel tracks:
   Track A: FIX-1 [DONE] → NEST-1 → NAV-1 → NAV-2 → GRAPH-1
   Track B: SUB-1 → SUB-2
   (Both tracks are independent. NAV-1 has no deps and can run alongside NEST-1.)

 Already complete — do not re-implement:
   FIX-1: renderIfBody restored, nestedStepCard fixed, console.logs removed ✓
   OBS-4 / StepIdx backend: executor.go:55, compiler.go:139-142, telemetry.go:92 ✓
   FlowView (step-grouped trace): Observability.tsx:1771 ✓
