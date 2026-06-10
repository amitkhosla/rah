import { useEffect, useMemo, useState } from 'react'
import { fetchSchema } from './api'
import type { ApiDef, EndpointDef, ConnStatus, FlowImpact, FlowStep, GatewayFlow, PaletteBlock, SavedFlow, StepGroup } from './types'
export type TabId = 'dashboard' | 'flows' | 'apis' | 'flowmap' | 'ai' | 'deploy' | 'releases' | 'gateway' | 'observability' | 'tenants' | 'apps' | 'rate-limits' | 'tiers' | 'upstream-services' | 'egress' | 'schemas' | 'grpc' | 'cache' | 'concurrency' | 'settings'
import Login          from './components/Login'
import ChangePassword from './components/ChangePassword'
import FlowDesigner   from './components/FlowDesigner'
import FlowMap       from './components/FlowMap'
import FlowGraph     from './components/FlowGraph'
import APIsSection   from './components/APIsSection'
import AISection     from './components/AISection'
import Deploy        from './components/Deploy'
import Gateway       from './components/Gateway'
import Tenants                from './components/Tenants'
import Settings               from './components/Settings'
import Dashboard              from './components/Dashboard'
import Observability          from './components/Observability'
import RateLimitConfigsScreen from './components/RateLimitConfigsScreen'
import TenantTiersScreen      from './components/TenantTiersScreen'
import UpstreamServicesScreen from './components/UpstreamServicesScreen'
import CachePanel             from './components/CachePanel'
import Concurrency            from './components/Concurrency'
import Apps                  from './components/Apps'
import Egress                from './components/Egress'
import Releases              from './components/Releases'
import SchemaLibrary         from './components/SchemaLibrary'
import GrpcDescriptors       from './components/GrpcDescriptors'

// ── Action name normalization (internal engine → display names) ──────

const INTERNAL_TO_DISPLAY: Record<string, string> = {
  early_return:        'return',
  CONCAT:              'concat',
  TO_LOWER:            'to_lower',
  TO_UPPER:            'to_upper',
  SUBSTRING:           'substring',
  TO_INT:              'to_int',
  ADD:                 'add',
  SUB:                 'subtract',
  MUL:                 'multiply',
  DIV:                 'divide',
  SET_RESPONSE_HEADER: 'set_response_header',
}

function normalizeActionNames(steps: FlowStep[]): FlowStep[] {
  return steps.map(s => {
    const display = INTERNAL_TO_DISPLAY[s.action]
    return display ? { ...s, action: display } : s
  })
}

// ── Sub-flow reference collector ─────────────────────────────────────

function collectFlowRefs(steps: FlowStep[]): string[] {
  const refs: string[] = []
  for (const step of steps) {
    if (step['then']) refs.push(step['then'] as string)
    if (step['else']) refs.push(step['else'] as string)
    if (step['flow_name']) refs.push(step['flow_name'] as string)
    if (step['cases']) {
      // cases format: "val1=flowA,val2=flowB"
      (step['cases'] as string).split(',').forEach(c => {
        const eq = c.indexOf('=')
        if (eq >= 0) refs.push(c.slice(eq + 1).trim())
      })
    }
    // Recurse into inline branches
    if (step.then_steps) refs.push(...collectFlowRefs(step.then_steps))
    if (step.else_steps) refs.push(...collectFlowRefs(step.else_steps))
  }
  return refs.filter(Boolean)
}

// ── Reverse dependency (impact) map ─────────────────────────────
// For each flow name, which flows and APIs reference it?
function buildImpactMap(savedFlows: SavedFlow[], apis: ApiDef[]): Map<string, FlowImpact> {
  const map = new Map<string, FlowImpact>()
  function entry(name: string): FlowImpact {
    if (!map.has(name)) map.set(name, { flows: [], apis: [] })
    return map.get(name)!
  }
  // Inter-flow references
  for (const flow of savedFlows) {
    for (const ref of collectFlowRefs(flow.steps)) {
      if (ref !== flow.name) {
        const e = entry(ref)
        if (!e.flows.includes(flow.name)) e.flows.push(flow.name)
      }
    }
  }
  // API → flow references
  for (const api of apis) {
    if (api.defaultFlow) {
      const e = entry(api.defaultFlow)
      if (!e.apis.includes(api.name)) e.apis.push(api.name)
    }
    for (const ep of api.endpoints) {
      if (ep.flowName && ep.flowName !== api.defaultFlow) {
        const e = entry(ep.flowName)
        if (!e.apis.includes(api.name)) e.apis.push(api.name)
      }
    }
  }
  return map
}

// ── Sidebar structure ────────────────────────────────────────────────
type NavItem =
  | { kind: 'item'; id: TabId; label: string }
  | { kind: 'section'; label: string }

const NAV_ICONS: Record<string, string> = {
  dashboard:          '⊞',
  flows:              '⛶',
  apis:               '⬡',
  flowmap:            '🗺',
  ai:                 '⬡',
  deploy:             '↑',
  releases:           '⊕',
  gateway:            '◉',
  observability:      '⊛',
  tenants:            '☰',
  apps:               '⬡',
  'rate-limits':      '⧖',
  tiers:              '≡',
  'upstream-services':'⇥',
  egress:             '⇄',
  schemas:            '⊞',
  grpc:               '⬡',
  concurrency:        '⟿',
  settings:           '⚙',
}

const NAV: NavItem[] = [
  { kind: 'item',    id: 'dashboard', label: 'Dashboard' },
  { kind: 'section', label: 'FLOWS' },
  { kind: 'item',    id: 'flows',     label: 'Designer' },
  { kind: 'item',    id: 'apis',      label: 'APIs' },
  { kind: 'item',    id: 'flowmap',   label: 'Flow Map' },
  { kind: 'section', label: 'AI' },
  { kind: 'item',    id: 'ai',        label: 'Models / MCP' },
  { kind: 'section', label: 'GATEWAY' },
  { kind: 'item',    id: 'deploy',        label: 'Deploy' },
  { kind: 'item',    id: 'releases',      label: 'Releases' },
  { kind: 'item',    id: 'gateway',       label: 'Live' },
  { kind: 'item',    id: 'observability', label: 'Observability' },
  { kind: 'section', label: 'SECURITY' },
  { kind: 'item',    id: 'tenants',            label: 'Tenants' },
  { kind: 'item',    id: 'apps',               label: 'Apps' },
  { kind: 'item',    id: 'rate-limits',         label: 'Rate Limits' },
  { kind: 'item',    id: 'tiers',               label: 'Tiers' },
  { kind: 'item',    id: 'upstream-services',   label: 'Upstreams' },
  { kind: 'item',    id: 'egress',              label: 'Egress' },
  { kind: 'item',    id: 'schemas',             label: 'Schemas' },
  { kind: 'item',    id: 'grpc',                label: 'gRPC' },
  { kind: 'item',    id: 'cache',               label: 'Cache' },
  { kind: 'item',    id: 'concurrency',        label: 'Concurrency' },
  { kind: 'section', label: '' },
  { kind: 'item',    id: 'settings',  label: 'Settings' },
]

// AuthUser holds the current session user. authEnabled=false means Studio runs without auth.
interface AuthUser { username: string; role: string; authEnabled: boolean; mustChangePassword: boolean }

// App is the auth gate. It renders Login until a valid session is confirmed,
// then renders AppContent. Keeping auth separate from AppContent avoids
// violating Rules of Hooks (no hooks-after-conditional-returns).
export default function App() {
  // undefined = loading, null = not authenticated, AuthUser = authenticated
  const [authUser, setAuthUser] = useState<AuthUser | null | undefined>(undefined)

  useEffect(() => {
    fetch('/api/auth/me', { credentials: 'include' })
      .then(r => (r.ok ? r.json() : null))
      .then((d: { username?: string; role?: string; auth_enabled?: boolean; must_change_password?: boolean } | null) => {
        if (d?.username) {
          setAuthUser({ username: d.username, role: d.role ?? 'admin', authEnabled: d.auth_enabled ?? true, mustChangePassword: d.must_change_password ?? false })
        } else {
          setAuthUser(null)
        }
      })
      .catch(() => setAuthUser(null))
  }, [])

  if (authUser === undefined) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100vh', background: 'var(--bg)', color: 'var(--muted)', fontSize: 14 }}>
        Loading…
      </div>
    )
  }

  if (authUser === null) {
    return (
      <Login onLogin={u => setAuthUser({
        username: u.username,
        role: (u as any).role ?? 'admin',
        authEnabled: true,
        mustChangePassword: (u as any).mustChangePassword ?? false,
      })} />
    )
  }

  // One-time forced password change: shown until the user sets a new password.
  if (authUser.mustChangePassword) {
    return (
      <ChangePassword
        username={authUser.username}
        onChanged={() => setAuthUser(prev => prev ? { ...prev, mustChangePassword: false } : prev)}
      />
    )
  }

  return (
    <AppContent
      authUser={authUser}
      onLogout={async () => {
        await fetch('/api/auth/logout', { method: 'POST', credentials: 'include' }).catch(() => {})
        setAuthUser(null)
      }}
    />
  )
}

// AppContent holds all the app state and renders the full Studio UI.
function AppContent({ authUser, onLogout }: { authUser: AuthUser; onLogout: () => void }) {
  const [tab, setTab] = useState<TabId>('dashboard')
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [flowMapView, setFlowMapView] = useState<'tree' | 'graph'>('tree')

  // Accent colour — applied immediately as a CSS custom property
  const [accent, setAccent] = useState('#57b5ff')
  useEffect(() => {
    document.documentElement.style.setProperty('--accent', accent)
  }, [accent])

  // Schema / connection state
  const [blocks, setBlocks] = useState<PaletteBlock[]>([])
  const [conn, setConn] = useState<ConnStatus>('connecting')

  useEffect(() => {
    fetchSchema()
      .then(d => { setBlocks(d.blocks ?? []); setConn('ok') })
      .catch(() => setConn('error'))
  }, [])

  // Active flow in Flow Designer
  const [flowName, setFlowName] = useState('')
  const [steps,    setSteps]    = useState<FlowStep[]>([])

  // Navigation stack: each entry is the flow name we navigated FROM (breadcrumb trail)
  // e.g. ['root_flow', 'auth_flow'] means we drilled: root_flow → auth_flow → current
  const [navStack, setNavStack] = useState<string[]>([])

  // All flows saved during this session (shared between designer + APIs section)
  const [savedFlows, setSavedFlows] = useState<SavedFlow[]>([])

  function saveCurrentFlow(groups: StepGroup[], stepLabels: Record<string, string>) {
    if (!flowName.trim() || steps.length === 0) return
    setSavedFlows(prev => {
      const idx = prev.findIndex(f => f.name === flowName)
      const entry: SavedFlow = { name: flowName, steps: [...steps], groups, stepLabels }
      if (idx >= 0) {
        const updated = [...prev]
        updated[idx] = entry
        return updated
      }
      return [...prev, entry]
    })
  }

  // Auto-save active flow to savedFlows whenever steps change so sync always sees latest.
  // NOTE: depends only on `steps` — NOT `flowName`. Reacting to every flowName keystroke
  // would create a new savedFlow entry for every character typed ("R", "Re", "Ret"...).
  // The flow is registered under its final name when the user explicitly saves/publishes.
  useEffect(() => {
    if (!flowName.trim() || steps.length === 0) return
    setSavedFlows(prev => {
      const idx = prev.findIndex(f => f.name === flowName)
      if (idx >= 0) {
        const updated = [...prev]
        updated[idx] = { ...updated[idx], steps: [...steps] }
        return updated
      }
      // Only add a new entry if the name is already registered (e.g. loaded from gateway).
      // New flows are added explicitly on save/publish, not on every keystroke.
      return prev
    })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [steps])

  // Called from APIsSection when user creates a flow name without going to designer
  function createNamedFlow(name: string) {
    setSavedFlows(prev => {
      if (prev.some(f => f.name === name)) return prev
      return [...prev, { name, steps: [] }]
    })
  }

  // Called from APIsSection when loading flows from gateway — preserves steps
  function loadNamedFlow(name: string, steps: FlowStep[]) {
    setSavedFlows(prev => {
      if (prev.some(f => f.name === name)) return prev  // don't overwrite local edits
      return [...prev, { name, steps }]
    })
  }

  // Clears the active flow and navigates to the designer
  function startNewFlow() {
    setFlowName('')
    setSteps([])
    setTab('flows')
  }

  // Navigate to designer, optionally loading a named flow.
  // If `pushCurrent` is true, the current flowName is pushed onto the navStack first
  // (used when drilling into a sub-flow from FlowMap or a call step).
  function navigateToDesigner(name?: string, pushCurrent?: boolean) {
    if (name) {
      const saved = savedFlows.find(f => f.name === name)
      if (pushCurrent && flowName) {
        setNavStack(prev => [...prev, flowName])
      } else if (!pushCurrent) {
        setNavStack([])
      }
      setFlowName(name)
      setSteps(saved?.steps ?? [])
    } else {
      setFlowName('')
      setSteps([])
      setNavStack([])
    }
    setTab('flows')
  }

  // Navigate back to a specific stack index (0 = root).
  // Pops everything above that index.
  function navigateToStackIndex(idx: number) {
    const target = navStack[idx]
    const saved = savedFlows.find(f => f.name === target)
    setFlowName(target)
    setSteps(saved?.steps ?? [])
    setNavStack(prev => prev.slice(0, idx))
    setTab('flows')
  }

  // APIs — each carries its own flow_name
  const [apis, setApis] = useState<ApiDef[]>([])

  useEffect(() => {
    try {
      const raw = localStorage.getItem('rah_studio_v1')
      if (raw) {
        const snap = JSON.parse(raw) as {
          savedFlows?: SavedFlow[]
          apis?: unknown[]
          accent?: string
        }
        if (snap.savedFlows?.length) setSavedFlows(snap.savedFlows)
        if (snap.apis?.length) {
          // Migrate old flat format { name, path, method, flow_name } to new ApiDef
          const migrated: ApiDef[] = snap.apis.map((a: any) => {
            if (a.endpoints) return a as ApiDef  // already new format
            // Old flat format — wrap as single-endpoint ApiDef
            return {
              id: crypto.randomUUID(),
              name: a.name ?? 'unnamed',
              basePath: a.path ?? '/',
              defaultFlow: a.flow_name ?? '',
              endpoints: [{
                id: crypto.randomUUID(),
                subPath: '/',
                method: a.method ?? 'GET',
              }],
            } satisfies ApiDef
          })
          setApis(migrated)
        }
        if (snap.accent) setAccent(snap.accent)
      }
    } catch { /* corrupt storage — ignore */ }
  }, [])

  useEffect(() => {
    const id = setTimeout(() => {
      try {
        localStorage.setItem('rah_studio_v1', JSON.stringify({ savedFlows, apis, accent }))
      } catch { /* quota exceeded — ignore */ }
    }, 800)
    return () => clearTimeout(id)
  }, [savedFlows, apis, accent])

  // Reverse dependency map: flow name → { flows, apis } that reference it
  const impactMap = useMemo(
    () => buildImpactMap(savedFlows, apis),
    [savedFlows, apis],
  )

  const connLabel: Record<ConnStatus, string> = {
    connecting: 'connecting…',
    ok: '✓ connected',
    error: '⚠ backend unreachable',
  }
  const connClass = conn === 'ok' ? ' ok' : conn === 'error' ? ' err' : ''
  const connColor = conn === 'ok' ? '#22c55e' : conn === 'error' ? '#ef4444' : '#f59e0b'

  return (
    <div style={{ display: 'flex', height: '100vh', overflow: 'hidden' }}>
      {/* ── Sidebar ── */}
      <aside className="sidebar" style={{ width: sidebarCollapsed ? 48 : 180 }}>
        {/* Title */}
        {sidebarCollapsed
          ? <div style={{ height: 53, borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 16, flexShrink: 0 }}>⊛</div>
          : <div className="sidebar-title">RAH Studio</div>
        }

        <nav style={{ flex: 1, overflowY: 'auto' }}>
          {NAV.map((item, i) => {
            if (item.kind === 'section') {
              if (sidebarCollapsed) return null
              return (
                <div key={i} className="sidebar-section-label">
                  {item.label}
                </div>
              )
            }
            const icon = NAV_ICONS[item.id] ?? '•'
            return (
              <div key={item.id}>
                <button
                  className={`sidebar-item${tab === item.id ? ' active' : ''}${sidebarCollapsed ? ' icon-only' : ''}`}
                  title={sidebarCollapsed ? item.label : undefined}
                  onClick={() => setTab(item.id)}
                >
                  <span style={{ fontSize: 14, flexShrink: 0 }}>{icon}</span>
                  {!sidebarCollapsed && (
                    <>
                      <span>{item.label}</span>
                      {item.id === 'apis' && apis.length > 0 && (
                        <span style={{
                          marginLeft: 'auto',
                          fontSize: 10,
                          fontWeight: 700,
                          background: 'rgba(255,255,255,0.15)',
                          borderRadius: 8,
                          padding: '1px 6px',
                        }}>
                          {apis.reduce((sum, a) => sum + a.endpoints.length, 0)}
                        </span>
                      )}
                    </>
                  )}
                </button>
                {item.id === 'flows' && !sidebarCollapsed && (
                  <button
                    style={{
                      display: 'block',
                      width: 'calc(100% - 24px)',
                      margin: '2px 12px 4px',
                      padding: '4px 10px',
                      borderRadius: 8,
                      border: '1px solid var(--accent)',
                      background: 'transparent',
                      color: 'var(--accent)',
                      fontSize: 11,
                      fontWeight: 700,
                      cursor: 'pointer',
                      textAlign: 'left',
                    }}
                    onClick={startNewFlow}
                  >
                    + New Flow
                  </button>
                )}
              </div>
            )
          })}
        </nav>

        {/* ── Sidebar bottom: accent picker + user + connection status + collapse toggle ── */}
        <div className="sidebar-bottom" style={{ padding: sidebarCollapsed ? '8px 4px' : undefined }}>
          {sidebarCollapsed ? (
            <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 6 }} title={connLabel[conn]}>
              <span style={{ fontSize: 12, color: connColor }}>●</span>
            </div>
          ) : (
            <>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <input
                  type="color"
                  className="color-input"
                  value={accent}
                  onChange={e => setAccent(e.target.value)}
                  title="Accent colour"
                  style={{ width: 24, height: 24, padding: 1 }}
                />
                <span style={{ fontSize: 10, color: 'var(--muted)' }}>Accent</span>
              </div>
              <span className={`conn-badge${connClass}`} style={{ fontSize: 11, marginTop: 6, display: 'block' }}>
                {connLabel[conn]}
              </span>
              {/* User / logout */}
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 8, gap: 4 }}>
                <span style={{ fontSize: 11, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  title={authUser.username}>
                  {authUser.username}
                </span>
                {authUser.authEnabled && (
                  <button
                    onClick={onLogout}
                    title="Sign out"
                    style={{
                      flexShrink: 0,
                      fontSize: 10,
                      padding: '2px 6px',
                      borderRadius: 4,
                      border: '1px solid var(--border)',
                      background: 'transparent',
                      color: 'var(--muted)',
                      cursor: 'pointer',
                    }}
                  >
                    Sign out
                  </button>
                )}
              </div>
            </>
          )}
          <button
            onClick={() => setSidebarCollapsed(c => !c)}
            title={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            style={{
              display: 'block',
              width: '100%',
              background: 'none',
              border: 'none',
              color: 'var(--muted)',
              cursor: 'pointer',
              fontSize: 14,
              textAlign: 'center',
              padding: '4px 0',
              marginTop: 4,
            }}
          >
            {sidebarCollapsed ? '»' : '«'}
          </button>
        </div>
      </aside>

      {/* ── Main content ── */}
      <main className="main-content">
        {tab === 'dashboard' && <Dashboard conn={conn} />}
        <div style={{ display: tab === 'flows' ? 'contents' : 'none' }}>
          <FlowDesigner
            blocks={blocks}
            steps={steps}
            setSteps={setSteps}
            flowName={flowName}
            setFlowName={setFlowName}
            savedFlows={savedFlows}
            onSaveFlow={() => saveCurrentFlow([], {})}
            onNavigateToFlow={(name) => navigateToDesigner(name, true)}
            navStack={navStack}
            onNavigateBack={navigateToStackIndex}
            impactMap={impactMap}
            onNavigateToApis={() => setTab('apis')}
            onOpenFlow={(name) => navigateToDesigner(name, false)}
            onDeleteFlow={(name) => setSavedFlows(prev => prev.filter(f => f.name !== name))}
          />
        </div>
        <div style={{ display: tab === 'apis' ? 'contents' : 'none' }}>
          <APIsSection
            flows={savedFlows}
            apis={apis}
            setApis={setApis}
            onCreateFlow={createNamedFlow}
            onLoadFlow={loadNamedFlow}
            onNavigateToDesigner={navigateToDesigner}
            onNavigateToDeploy={() => setTab('deploy')}
          />
        </div>
        {tab === 'flowmap' && (
          <div style={{ padding: '20px 24px', display: 'flex', flexDirection: 'column', gap: 12, height: '100%', minHeight: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span style={{ fontWeight: 700, fontSize: 15 }}>Flow Map</span>
              <div style={{ display: 'flex', borderRadius: 6, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.12)', marginLeft: 8 }}>
                <button
                  style={{ fontSize: 12, padding: '3px 12px', border: 'none', cursor: 'pointer', background: flowMapView === 'tree' ? 'var(--accent)' : 'transparent', color: flowMapView === 'tree' ? '#fff' : 'var(--muted)' }}
                  onClick={() => setFlowMapView('tree')}
                >⬡ Tree</button>
                <button
                  style={{ fontSize: 12, padding: '3px 12px', border: 'none', cursor: 'pointer', background: flowMapView === 'graph' ? 'var(--accent)' : 'transparent', color: flowMapView === 'graph' ? '#fff' : 'var(--muted)' }}
                  onClick={() => setFlowMapView('graph')}
                >⬡ Graph</button>
              </div>
            </div>
            {flowMapView === 'tree' ? (
              <FlowMap
                savedFlows={savedFlows}
                currentFlow={flowName}
                onNavigate={name => navigateToDesigner(name, false)}
                onDeleteFlows={names => setSavedFlows(prev => prev.filter(f => !names.includes(f.name)))}
              />
            ) : (
              <FlowGraph
                savedFlows={savedFlows}
                currentFlow={flowName}
                onNavigate={name => navigateToDesigner(name, false)}
              />
            )}
          </div>
        )}
        {tab === 'ai' && <AISection />}
        {tab === 'deploy' && (
          <Deploy
            savedFlows={savedFlows}
            apis={apis}
          />
        )}
        {tab === 'gateway' && (
          <Gateway
            onLoadFlow={(name, loadedSteps, allGatewayFlows) => {
              const normalizedSteps = normalizeActionNames(loadedSteps)
              setFlowName(name)
              setSteps(normalizedSteps)
              // Also upsert into savedFlows so APIsSection can see it
              setSavedFlows(prev => {
                let updated = [...prev]
                const mainIdx = updated.findIndex(f => f.name === name)
                if (mainIdx >= 0) {
                  updated[mainIdx] = { name, steps: normalizedSteps }
                } else {
                  updated.push({ name, steps: normalizedSteps })
                }
                // Recursively load all referenced sub-flows (up to 3 levels deep)
                const queue = [...collectFlowRefs(normalizedSteps)]
                const visited = new Set([name])
                let depth = 0
                while (queue.length > 0 && depth < 3) {
                  const batch = queue.splice(0, queue.length)
                  depth++
                  for (const refName of batch) {
                    if (visited.has(refName)) continue
                    visited.add(refName)
                    const refFlow = allGatewayFlows.find((f: GatewayFlow) => f.name === refName)
                    if (!refFlow) continue
                    const refSteps = normalizeActionNames(refFlow.instructions)
                    const existingIdx = updated.findIndex(f => f.name === refName)
                    if (existingIdx >= 0) {
                      updated[existingIdx] = { name: refName, steps: refSteps }
                    } else {
                      updated.push({ name: refName, steps: refSteps })
                    }
                    queue.push(...collectFlowRefs(refSteps))
                  }
                }
                return updated
              })
              setTab('flows')
            }}
            onLoadApi={(api) => {
              setApis(prev => {
                const idx = prev.findIndex(a => a.name === api.name)
                const newDef: ApiDef = {
                  id: prev[idx]?.id ?? crypto.randomUUID(),
                  name: api.name,
                  basePath: api.path,
                  defaultFlow: api.flow_name,
                  endpoints: prev[idx]?.endpoints ?? [{
                    id: crypto.randomUUID(),
                    subPath: '/',
                    method: api.method,
                  } satisfies EndpointDef],
                }
                if (idx >= 0) {
                  const updated = [...prev]
                  updated[idx] = newDef
                  return updated
                }
                return [...prev, newDef]
              })
            }}
          />
        )}
        {tab === 'releases'           && <Releases />}
        {tab === 'observability'      && <Observability />}
        {tab === 'tenants'           && <Tenants />}
        {tab === 'apps'              && <Apps />}
        {tab === 'rate-limits'       && <RateLimitConfigsScreen />}
        {tab === 'tiers'             && <TenantTiersScreen />}
        {tab === 'upstream-services' && <UpstreamServicesScreen />}
        {tab === 'egress'            && <Egress />}
        {tab === 'schemas'           && <SchemaLibrary />}
        {tab === 'grpc'              && <GrpcDescriptors />}
        {tab === 'cache'             && <CachePanel />}
        {tab === 'concurrency'       && <Concurrency />}
        {tab === 'settings' && <Settings accent={accent} setAccent={setAccent} />}
      </main>
    </div>
  )
}
