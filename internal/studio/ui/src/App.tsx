import { useEffect, useState } from 'react'
import { fetchSchema } from './api'
import type { ApiDef, ConnStatus, FlowStep, PaletteBlock, SavedFlow, TabId } from './types'
import FlowDesigner from './components/FlowDesigner'
import APIsSection  from './components/APIsSection'
import AISection    from './components/AISection'
import Deploy       from './components/Deploy'
import Gateway      from './components/Gateway'
import Tenants      from './components/Tenants'
import Settings     from './components/Settings'
import Dashboard    from './components/Dashboard'

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
  { kind: 'item',    id: 'deploy',    label: 'Deploy' },
  { kind: 'item',    id: 'gateway',   label: 'Live' },
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

  function saveCurrentFlow() {
    if (!flowName.trim() || steps.length === 0) return
    setSavedFlows(prev => {
      const idx = prev.findIndex(f => f.name === flowName)
      if (idx >= 0) {
        const updated = [...prev]
        updated[idx] = { name: flowName, steps: [...steps] }
        return updated
      }
      return [...prev, { name: flowName, steps: [...steps] }]
    })
  }

  // Called from APIsSection when user creates a flow name without going to designer
  function createNamedFlow(name: string) {
    setSavedFlows(prev => {
      if (prev.some(f => f.name === name)) return prev
      return [...prev, { name, steps: [] }]
    })
  }

  // APIs — each carries its own flow_name
  const [apis, setApis] = useState<ApiDef[]>([])

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
              <button
                key={item.id}
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
                    {apis.length}
                  </span>
                )}
              </button>
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
            onSaveFlow={saveCurrentFlow}
          />
        )}
        {tab === 'apis' && (
          <APIsSection
            flows={savedFlows}
            apis={apis}
            setApis={setApis}
            onCreateFlow={createNamedFlow}
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
            onLoadFlow={(name, loadedSteps) => {
              setFlowName(name)
              setSteps(loadedSteps)
              // also upsert into savedFlows so APIsSection can see it
              setSavedFlows(prev => {
                const idx = prev.findIndex(f => f.name === name)
                if (idx >= 0) {
                  const updated = [...prev]
                  updated[idx] = { name, steps: loadedSteps }
                  return updated
                }
                return [...prev, { name, steps: loadedSteps }]
              })
              setTab('flows')
            }}
          />
        )}
        {tab === 'tenants'  && <Tenants />}
        {tab === 'settings' && <Settings accent={accent} setAccent={setAccent} />}
      </main>
    </div>
  )
}
