import { useEffect, useState } from 'react'
import { fetchSchema } from './api'
import type { ApiDef, EndpointDef, ConnStatus, FlowStep, GatewayFlow, PaletteBlock, SavedFlow, StepGroup, TabId } from './types'
import FlowDesigner  from './components/FlowDesigner'
import APIsSection   from './components/APIsSection'
import AISection     from './components/AISection'
import Deploy        from './components/Deploy'
import Gateway       from './components/Gateway'
import Tenants       from './components/Tenants'
import Settings      from './components/Settings'
import Dashboard     from './components/Dashboard'
import Observability from './components/Observability'

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
    if (step['then']) refs.push(step['then'])
    if (step['else']) refs.push(step['else'])
    if (step['flow_name']) refs.push(step['flow_name'])
    if (step['cases']) {
      // cases format: "val1=flowA,val2=flowB"
      step['cases'].split(',').forEach(c => {
        const eq = c.indexOf('=')
        if (eq >= 0) refs.push(c.slice(eq + 1).trim())
      })
    }
  }
  return refs.filter(Boolean)
}

// ── Sidebar structure ────────────────────────────────────────────────
type NavItem =
  | { kind: 'item'; id: TabId; label: string }
  | { kind: 'section'; label: string }

const NAV: NavItem[] = [
  { kind: 'item',    id: 'dashboard', label: 'Dashboard' },
  { kind: 'section', label: 'FLOWS' },
  { kind: 'item',    id: 'flows',     label: 'Designer' },
  { kind: 'item',    id: 'apis',      label: 'APIs' },
  { kind: 'section', label: 'AI' },
  { kind: 'item',    id: 'ai',        label: 'Models / MCP' },
  { kind: 'section', label: 'GATEWAY' },
  { kind: 'item',    id: 'deploy',        label: 'Deploy' },
  { kind: 'item',    id: 'gateway',       label: 'Live' },
  { kind: 'item',    id: 'observability', label: 'Observability' },
  { kind: 'section', label: 'SECURITY' },
  { kind: 'item',    id: 'tenants',   label: 'Tenants' },
  { kind: 'section', label: '' },
  { kind: 'item',    id: 'settings',  label: 'Settings' },
]

export default function App() {
  const [tab, setTab] = useState<TabId>('dashboard')

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

  // Called from APIsSection when user creates a flow name without going to designer
  function createNamedFlow(name: string) {
    setSavedFlows(prev => {
      if (prev.some(f => f.name === name)) return prev
      return [...prev, { name, steps: [] }]
    })
  }

  // Clears the active flow and navigates to the designer
  function startNewFlow() {
    setFlowName('')
    setSteps([])
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

  const connLabel: Record<ConnStatus, string> = {
    connecting: 'connecting…',
    ok: '✓ connected',
    error: '⚠ backend unreachable',
  }
  const connClass = conn === 'ok' ? ' ok' : conn === 'error' ? ' err' : ''

  return (
    <div style={{ display: 'flex', height: '100vh', overflow: 'hidden' }}>
      {/* ── Sidebar ── */}
      <aside className="sidebar">
        <div className="sidebar-title">RAH Studio</div>

        <nav style={{ flex: 1, overflowY: 'auto' }}>
          {NAV.map((item, i) => {
            if (item.kind === 'section') {
              return (
                <div key={i} className="sidebar-section-label">
                  {item.label}
                </div>
              )
            }
            return (
              <div key={item.id}>
                <button
                  className={`sidebar-item${tab === item.id ? ' active' : ''}`}
                  onClick={() => setTab(item.id)}
                >
                  {item.label}
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
                </button>
                {item.id === 'flows' && (
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

        {/* ── Sidebar bottom: accent picker + connection status ── */}
        <div className="sidebar-bottom">
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
        </div>
      </aside>

      {/* ── Main content ── */}
      <main className="main-content">
        {tab === 'dashboard' && <Dashboard conn={conn} />}
        {tab === 'flows' && (
          <FlowDesigner
            blocks={blocks}
            steps={steps}
            setSteps={setSteps}
            flowName={flowName}
            setFlowName={setFlowName}
            savedFlows={savedFlows}
            onSaveFlow={() => saveCurrentFlow([], {})}
          />
        )}
        {tab === 'apis' && (
          <APIsSection
            flows={savedFlows}
            apis={apis}
            setApis={setApis}
            onCreateFlow={createNamedFlow}
            onNavigateToDesigner={startNewFlow}
            onNavigateToDeploy={() => setTab('deploy')}
          />
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
        {tab === 'observability' && <Observability />}
        {tab === 'tenants'  && <Tenants />}
        {tab === 'settings' && <Settings accent={accent} setAccent={setAccent} />}
      </main>
    </div>
  )
}
