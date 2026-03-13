import { useEffect, useState } from 'react'
import { fetchSchema } from './api'
import type { ApiDef, ConnStatus, FlowStep, PaletteBlock, TabId } from './types'
import FlowDesigner from './components/FlowDesigner'
import APIDefinition from './components/APIDefinition'
import Deploy from './components/Deploy'
import Settings from './components/Settings'

const TABS: { id: TabId; label: string }[] = [
  { id: 'flows',    label: 'Flow Designer' },
  { id: 'apis',     label: 'API Definition' },
  { id: 'deploy',   label: 'Deploy / Releases' },
  { id: 'settings', label: 'UI Settings' },
]

export default function App() {
  const [tab, setTab] = useState<TabId>('flows')

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

  // Shared state lifted from FlowDesigner + APIDefinition (both consumed by Deploy)
  const [flowName, setFlowName] = useState('')
  const [steps, setSteps] = useState<FlowStep[]>([])
  const [apis, setApis] = useState<ApiDef[]>([])

  const connLabel: Record<ConnStatus, string> = {
    connecting: 'connecting…',
    ok: '✓ connected',
    error: '⚠ backend unreachable',
  }

  return (
    <>
      <nav className="topbar">
        <span className="topbar-title">RAH Studio</span>

        {TABS.map(t => (
          <button
            key={t.id}
            className={`tab-btn${tab === t.id ? ' active' : ''}`}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}

        <div className="topbar-right">
          <input
            type="color"
            className="color-input"
            value={accent}
            onChange={e => setAccent(e.target.value)}
            title="Accent colour"
          />
          <span className={`conn-badge${conn === 'ok' ? ' ok' : conn === 'error' ? ' err' : ''}`}>
            {connLabel[conn]}
          </span>
        </div>
      </nav>

      <main className="wrap">
        {tab === 'flows' && (
          <FlowDesigner
            blocks={blocks}
            steps={steps}
            setSteps={setSteps}
            flowName={flowName}
            setFlowName={setFlowName}
          />
        )}
        {tab === 'apis' && (
          <APIDefinition
            apis={apis}
            setApis={setApis}
            flowName={flowName}
          />
        )}
        {tab === 'deploy' && (
          <Deploy
            steps={steps}
            apis={apis}
            flowName={flowName}
          />
        )}
        {tab === 'settings' && (
          <Settings accent={accent} setAccent={setAccent} />
        )}
      </main>
    </>
  )
}
