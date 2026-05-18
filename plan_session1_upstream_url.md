# Session 1: Upstream URL at API/Endpoint Level

## Goal
Add an "Upstream URL" section to the API and Endpoint definition panels so users can
declare where the upstream backend lives — without having to manually wire it in a flow step.

## Model
`claude-sonnet-4-6`

## Files to Edit (full paths)
```
D:\GoLand\rah\internal\studio\ui\src\types.ts
D:\GoLand\rah\internal\studio\ui\src\api.ts
D:\GoLand\rah\internal\studio\ui\src\components\APIsSection.tsx
```

---

## Step 1 — types.ts: Add new interface + extend ApiDef and EndpointDef

Add this new interface (insert before `DeployPayload`):
```ts
export interface UpstreamUrlConfig {
  source: 'static' | 'registry' | 'cache' | 'header' | 'queryparam'
  value: string  // URL when source='static'; key name for all others
}
```

Add `upstreamUrl?: UpstreamUrlConfig` to `ApiDef`:
```ts
export interface ApiDef {
  id: string
  name: string
  basePath: string
  aliasPaths?: string[]
  defaultFlow: string
  endpoints: EndpointDef[]
  rateLimitName?: string
  constants?: Record<string, string>
  upstreamUrl?: UpstreamUrlConfig   // NEW
}
```

Add `upstreamUrl?: UpstreamUrlConfig` to `EndpointDef`:
```ts
export interface EndpointDef {
  id: string
  subPath: string
  method: string
  flowName?: string
  rateLimitName?: string
  constants?: Record<string, string>
  upstreamUrl?: UpstreamUrlConfig   // NEW
}
```

---

## Step 2 — api.ts: Update SyncPayload and GatewaySnapshot

In `SyncPayload` (around line 274), add `upstream_url` and fix the missing `constants` type bug:
```ts
export interface SyncPayload {
  sync_uuid: string
  flows: Array<{ name: string; instructions: SyncStep[]; action: 'upsert' }>
  apis: Array<{
    name: string
    path: string
    flow_name: string
    action: 'upsert'
    rate_limit?: string
    upstream_url?: { source: string; value: string }   // NEW
    alias_paths?: string[]
    constants?: Record<string, string>                 // BUG FIX (was missing, sent at runtime via spread)
    endpoint_configs?: Array<{
      path: string
      method?: string
      flow_name?: string
      rate_limit?: string
      upstream_url?: { source: string; value: string } // NEW
      constants?: Record<string, string>               // BUG FIX
    }>
  }>
}
```

In `GatewaySnapshot` (around line 288), add the same fields:
```ts
export interface GatewaySnapshot {
  flows: Array<{ name: string; instructions: SyncStep[] }>
  apis: Array<{
    name: string
    path: string
    method?: string
    flow_name: string
    rate_limit?: string
    upstream_url?: { source: string; value: string }   // NEW
    constants?: Record<string, string>                 // BUG FIX
    endpoint_configs?: Array<{
      path: string
      method?: string
      flow_name?: string
      rate_limit?: string
      upstream_url?: { source: string; value: string } // NEW
      constants?: Record<string, string>               // BUG FIX
    }>
    alias_paths?: string[]
  }>
}
```

---

## Step 3 — APIsSection.tsx: Update import line (top of file, line 1-4)

Change:
```ts
import type { ApiDef, EndpointDef, FlowStep, SavedFlow } from '../types'
```
To:
```ts
import type { ApiDef, EndpointDef, FlowStep, SavedFlow, UpstreamUrlConfig } from '../types'
```

---

## Step 4 — APIsSection.tsx: Add two handler functions in main component

Insert after `handleUpdateEndpointConstants` (around line 369):
```ts
function handleUpdateApiUpstreamUrl(apiId: string, u: UpstreamUrlConfig | undefined) {
  setApis(apis.map(a => a.id === apiId ? { ...a, upstreamUrl: u } : a))
}

function handleUpdateEndpointUpstreamUrl(apiId: string, epId: string, u: UpstreamUrlConfig | undefined) {
  setApis(apis.map(a => a.id !== apiId ? a : {
    ...a,
    endpoints: a.endpoints.map(e => e.id === epId ? { ...e, upstreamUrl: u } : e),
  }))
}
```

---

## Step 5 — APIsSection.tsx: Update syncThisApi()

In the `apisPayload` construction inside `syncThisApi()` (around line 381), add upstream_url:
```ts
const apisPayload = [{
  name: api.name,
  path: api.basePath,
  flow_name: api.defaultFlow,
  ...(api.rateLimitName ? { rate_limit: api.rateLimitName } : {}),
  ...(api.upstreamUrl ? { upstream_url: api.upstreamUrl } : {}),       // NEW
  ...(api.aliasPaths?.length ? { alias_paths: api.aliasPaths } : {}),
  ...(api.constants && Object.keys(api.constants).length ? { constants: api.constants } : {}),
  endpoint_configs: api.endpoints.map(ep => ({
    path: ep.subPath,
    method: ep.method,
    ...(ep.flowName ? { flow_name: ep.flowName } : {}),
    ...(ep.rateLimitName ? { rate_limit: ep.rateLimitName } : {}),
    ...(ep.upstreamUrl ? { upstream_url: ep.upstreamUrl } : {}),       // NEW
    ...(ep.constants && Object.keys(ep.constants).length ? { constants: ep.constants } : {}),
  })),
  action: 'upsert' as const,
}]
```

---

## Step 6 — APIsSection.tsx: Update fetchGatewaySnapshot seeding (around line 143-188)

In the snapshot-to-ApiDef mapping, read back `upstream_url`.

In the API-level mapping (where `grouped.set(ga.path, {...})` is called):
```ts
grouped.set(ga.path, {
  id: crypto.randomUUID(),
  name: ga.name,
  basePath: ga.path,
  defaultFlow: ga.flow_name ?? '',
  endpoints: eps,
  ...(ga.rate_limit ? { rateLimitName: ga.rate_limit } : {}),
  ...(ga.alias_paths?.length ? { aliasPaths: ga.alias_paths } : {}),
  ...(gaAny.constants ? { constants: gaAny.constants as Record<string,string> } : {}),
  ...(gaAny.upstream_url ? { upstreamUrl: gaAny.upstream_url as UpstreamUrlConfig } : {}),  // NEW
})
```

In the endpoint_configs mapping (where `EndpointDef` objects are created):
```ts
{
  id: crypto.randomUUID(),
  subPath: ec.path || '/',
  method: ec.method || method,
  ...(ec.flow_name ? { flowName: ec.flow_name } : {}),
  ...(ec.rate_limit ? { rateLimitName: ec.rate_limit } : {}),
  ...(ecAny.constants ? { constants: ecAny.constants as Record<string,string> } : {}),
  ...(ecAny.upstream_url ? { upstreamUrl: ecAny.upstream_url as UpstreamUrlConfig } : {}),  // NEW
}
```

---

## Step 7 — APIsSection.tsx: Update ApiDetailProps interface (around line 731)

Add two props to the `ApiDetailProps` interface:
```ts
interface ApiDetailProps {
  api: ApiDef
  flows: SavedFlow[]
  onRemove: () => void
  onUpdateDefaultFlow: (name: string) => void
  onSelectEndpoint: (epId: string) => void
  onAddEndpoint: () => void
  onNavigateToDesigner: (flowName?: string) => void
  onRemoveAlias: (index: number) => void
  rateLimitConfigs: string[]
  rateLimitError: boolean
  onUpdateRateLimit: (name: string) => void
  onSync: () => void
  syncStatus: 'idle'|'syncing'|'done'|'error'
  constants: Record<string, string>
  onUpdateConstants: (c: Record<string, string>) => void
  upstreamUrl: UpstreamUrlConfig | undefined           // NEW
  onUpdateUpstreamUrl: (u: UpstreamUrlConfig | undefined) => void  // NEW
}
```

Update the call site (around line 612-629) to pass the new props:
```tsx
<ApiDetailPanel
  ...
  upstreamUrl={selectedApi.upstreamUrl}
  onUpdateUpstreamUrl={u => handleUpdateApiUpstreamUrl(selectedApi.id, u)}
/>
```

---

## Step 8 — APIsSection.tsx: Update EndpointDetailProps interface (around line 942)

Add two props to `EndpointDetailProps`:
```ts
interface EndpointDetailProps {
  api: ApiDef
  endpoint: EndpointDef
  flows: SavedFlow[]
  onRemove: () => void
  onSetFlow: (flowName: string) => void
  onClearOverride: () => void
  onNavigateToDesigner: (flowName?: string) => void
  onNavigateToDeploy: () => void
  rateLimitConfigs: string[]
  rateLimitError: boolean
  onUpdateRateLimit: (name: string) => void
  constants: Record<string, string>
  onUpdateConstants: (c: Record<string, string>) => void
  upstreamUrl: UpstreamUrlConfig | undefined                       // NEW
  onUpdateUpstreamUrl: (u: UpstreamUrlConfig | undefined) => void  // NEW
}
```

Update the call site (around line 596-611) to pass the new props:
```tsx
<EndpointDetailPanel
  ...
  upstreamUrl={selectedEndpoint.upstreamUrl}
  onUpdateUpstreamUrl={u => handleUpdateEndpointUpstreamUrl(selectedApi.id, selectedEndpoint.id, u)}
/>
```

Also update the `EndpointDetailPanel` function signature to destructure the new props:
```ts
function EndpointDetailPanel({
  api, endpoint, flows, onRemove, onSetFlow, onClearOverride,
  onNavigateToDesigner, onNavigateToDeploy,
  rateLimitConfigs, rateLimitError, onUpdateRateLimit,
  constants, onUpdateConstants,
  upstreamUrl, onUpdateUpstreamUrl,   // NEW
}: EndpointDetailProps) {
```

Also update `ApiDetailPanel` function signature:
```ts
function ApiDetailPanel({
  api, flows, onRemove, onUpdateDefaultFlow, onSelectEndpoint, onAddEndpoint,
  onNavigateToDesigner, onRemoveAlias,
  rateLimitConfigs, rateLimitError, onUpdateRateLimit, onSync, syncStatus,
  constants, onUpdateConstants,
  upstreamUrl, onUpdateUpstreamUrl,   // NEW
}: ApiDetailProps) {
```

---

## Step 9 — APIsSection.tsx: Add UpstreamUrlEditor component

Insert this new component just before the `ConstantsEditor` component (around line 668):

```tsx
// ── Upstream URL Editor ──────────────────────────────────────────────────────

const SOURCE_LABELS: Record<string, string> = {
  static:     'Static URL',
  registry:   'Registry key',
  cache:      'Cache key',
  header:     'Request header',
  queryparam: 'Query param',
}

const SOURCE_PLACEHOLDERS: Record<string, string> = {
  static:     'https://api.example.com/v1',
  registry:   'primary_service_url',
  cache:      'backend_url_key',
  header:     'X-Backend-Url',
  queryparam: 'backend',
}

function UpstreamUrlEditor({
  value,
  onChange,
  inheritLabel,
}: {
  value: UpstreamUrlConfig | undefined
  onChange: (u: UpstreamUrlConfig | undefined) => void
  inheritLabel?: string   // shown as the "none" option; undefined = "— none —"
}) {
  return (
    <div>
      <select
        className="input"
        style={{ maxWidth: 220, marginTop: 0 }}
        value={value?.source ?? ''}
        onChange={e => {
          const src = e.target.value as UpstreamUrlConfig['source'] | ''
          if (!src) { onChange(undefined); return }
          onChange({ source: src, value: '' })
        }}
      >
        <option value="">{inheritLabel ?? '— none —'}</option>
        {Object.entries(SOURCE_LABELS).map(([k, v]) => (
          <option key={k} value={k}>{v}</option>
        ))}
      </select>

      {value && (
        <input
          className="input"
          style={{ marginTop: 6, maxWidth: 360 }}
          placeholder={SOURCE_PLACEHOLDERS[value.source]}
          value={value.value}
          onChange={e => onChange({ ...value, value: e.target.value })}
        />
      )}

      {value?.source && value.source !== 'static' && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4, lineHeight: 1.5 }}>
          At runtime, the gateway reads this key from {SOURCE_LABELS[value.source].toLowerCase()} and uses it as the upstream URL.
        </p>
      )}
    </div>
  )
}
```

---

## Step 10 — APIsSection.tsx: Add "Upstream URL" section to ApiDetailPanel

In `ApiDetailPanel`, insert a new `<Section>` after "Default Flow" and before "Rate Limit" (around line 850):

```tsx
{/* Upstream URL */}
<Section label="Upstream URL" style={{ marginTop: 20 }}>
  <UpstreamUrlEditor value={upstreamUrl} onChange={onUpdateUpstreamUrl} />
</Section>
```

---

## Step 11 — APIsSection.tsx: Add "Upstream URL" section to EndpointDetailPanel

In `EndpointDetailPanel`, insert a new `<Section>` before "Rate Limit" (around line 1068):

```tsx
{/* Upstream URL */}
<Section label="Upstream URL" style={{ marginTop: 20 }}>
  <UpstreamUrlEditor
    value={endpoint.upstreamUrl}
    onChange={onUpdateUpstreamUrl}
    inheritLabel={
      api.upstreamUrl
        ? `Inherit from API (${SOURCE_LABELS[api.upstreamUrl.source]}: ${api.upstreamUrl.value || '…'})`
        : '— inherit from API (none set) —'
    }
  />
</Section>
```

Note: `SOURCE_LABELS` must be defined at module scope (Step 9 above) so `EndpointDetailPanel` can reference it.

---

## What NOT to change
- `Tenants.tsx` — untouched
- `FlowDesigner.tsx` — untouched
- `WizardPanel` — untouched (upstream URL not needed at wizard time)
- Rate limit handling — untouched (Session 2)
- Constants/ConstantsEditor — untouched (Session 3)
- No new files created
- No backend Go code

## Verification checklist after implementation
- [ ] TypeScript compiles with no errors: `cd internal/studio/ui && npx tsc --noEmit`
- [ ] API detail panel shows "Upstream URL" section with source dropdown
- [ ] Endpoint detail panel shows "Upstream URL" section with inherit option showing API's value
- [ ] Selecting "Static URL" shows URL input field
- [ ] Selecting any variable source shows key input + help text
- [ ] Selecting blank/none clears the upstreamUrl (sets to undefined)
- [ ] Sync payload includes `upstream_url` when set
- [ ] Gateway snapshot reload correctly restores `upstreamUrl` on ApiDef and EndpointDef
