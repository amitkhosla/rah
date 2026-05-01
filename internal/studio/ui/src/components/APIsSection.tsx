import { useState, useEffect } from 'react'
import { importOpenAPI, fetchGatewaySnapshot } from '../api'
import type { ApiDef, FlowStep, SavedFlow } from '../types'
import FlowSearchSelect from './FlowSearchSelect'

interface Props {
  flows: SavedFlow[]
  apis: ApiDef[]
  setApis: (apis: ApiDef[]) => void
  onCreateFlow: (name: string) => void
  onNavigateToDesigner: () => void
  onNavigateToDeploy: () => void
}

// ── Zone system ──────────────────────────────────────────────────────────────

type Zone = 'security' | 'process' | 'upstream' | 'response' | 'post'

const ACTION_ZONE: Record<string, Zone> = {
  token_validation: 'security', check_rate_limit: 'security',
  load_credential: 'security', registry_lookup: 'security',
  load_identifier: 'security', load_service_url: 'security',
  http_call: 'upstream', llm_call: 'upstream', mcp_call_tool: 'upstream',
  execute_plan: 'upstream', vector_search: 'upstream', embed_text: 'upstream',
  semantic_cache_get: 'upstream', route_llm: 'upstream',
  set_response_body: 'response', return: 'response', format_response: 'response',
  respond: 'response', set_response_header: 'response', early_return: 'response',
  emit_event: 'post', save_history: 'post', append_message: 'post',
}

const ZONE_META: Record<Zone, { label: string; color: string; icon: string }> = {
  security: { label: 'SECURITY',      color: '#f97316', icon: '🔒' },
  process:  { label: 'PROCESS',       color: '#57b5ff', icon: '⚙'  },
  upstream: { label: 'UPSTREAM',      color: '#fbbf24', icon: '🔗' },
  response: { label: 'RESPONSE',      color: '#34d399', icon: '📤' },
  post:     { label: 'POST-RESPONSE', color: '#a78bfa', icon: '📊' },
}

function stepZone(action: string): Zone {
  return ACTION_ZONE[action] ?? 'process'
}

// ── Method badge colors ──────────────────────────────────────────────────────

const METHOD_COLOR: Record<string, string> = {
  GET:    '#22c55e',
  POST:   '#3b82f6',
  PUT:    '#f97316',
  PATCH:  '#eab308',
  DELETE: '#ef4444',
}

// ── Name auto-suggest ────────────────────────────────────────────────────────

function suggestName(path: string): string {
  return path
    .replace(/^\/+/, '')
    .replace(/[/:.-]/g, '_')
    .replace(/_+/g, '_')
    .replace(/^_|_$/g, '')
}

// ── API interface derivation ─────────────────────────────────────────────────

interface InputBinding  { source: string; field: string; variable: string }
interface OutputBinding { action: string; value: string }

function deriveApiInterface(steps: FlowStep[]): { inputs: InputBinding[]; outputs: OutputBinding[] } {
  const inputs:  InputBinding[]  = []
  const outputs: OutputBinding[] = []

  for (const step of steps) {
    if (step.action === 'bind_header') {
      inputs.push({ source: 'header', field: step['key'] ?? '', variable: step['as'] ?? '' })
    } else if (step.action === 'bind_query_param') {
      inputs.push({ source: 'query',  field: step['key'] ?? '', variable: step['as'] ?? '' })
    } else if (step.action === 'bind_body') {
      inputs.push({ source: 'body',   field: step['key'] ?? '', variable: step['as'] ?? '' })
    } else if (step.action === 'set_response_body') {
      outputs.push({ action: 'response body', value: step['source'] ?? '' })
    } else if (step.action === 'return') {
      const v = step['body'] || step['as'] || ''
      if (v) outputs.push({ action: 'return', value: v })
    } else if (step.action === 'set_response_header') {
      outputs.push({ action: `header ${step['key'] ?? ''}`, value: step['source'] ?? '' })
    }
  }

  return { inputs, outputs }
}

// ── Main component ───────────────────────────────────────────────────────────

export default function APIsSection({ flows, apis, setApis, onCreateFlow, onNavigateToDesigner, onNavigateToDeploy }: Props) {
  const [selectedApiIdx, setSelectedApiIdx] = useState<number | null>(null)
  const [showWizard,     setShowWizard]     = useState(false)

  // Wizard state
  const [wizardStep,         setWizardStep]         = useState<1 | 2>(1)
  const [wizardMethod,       setWizardMethod]       = useState('GET')
  const [wizardPath,         setWizardPath]         = useState('')
  const [wizardName,         setWizardName]         = useState('')
  const [wizardFlowMode,     setWizardFlowMode]     = useState<'create' | 'existing'>('create')
  const [wizardExistingFlow, setWizardExistingFlow] = useState('')
  const [wizardErr,          setWizardErr]          = useState('')

  // Loading state while we check the gateway on mount
  const [syncing, setSyncing] = useState(true)

  // OpenAPI import state (lives in detail panel)
  const [spec,      setSpec]      = useState('')
  const [importMsg, setImportMsg] = useState('')
  const [importErr, setImportErr] = useState(false)
  const [importing, setImporting] = useState(false)

  // On mount: fetch live gateway state and seed the local API list so the
  // panel is not empty after a page refresh.
  useEffect(() => {
    fetchGatewaySnapshot()
      .then(state => {
        if (state?.apis?.length) {
          const incoming = state.apis
            .filter(ga => !apis.find(a => a.name === ga.name))
            .map(ga => ({ name: ga.name, path: ga.path, method: 'POST' as const, flow_name: ga.flow_name ?? '' }))
          if (incoming.length > 0) setApis([...apis, ...incoming])
        }
        // Register any gateway flow names that aren't yet in local savedFlows
        for (const gf of (state.flows ?? [])) {
          onCreateFlow(gf.name)
        }
        setSyncing(false)
      })
      .catch(() => {
        setSyncing(false) /* gateway unreachable — silent */
      })
  }, [])

  // ── Derived ────────────────────────────────────────────────────────────────

  const flowNames  = flows.map(f => f.name)
  const unassigned = apis.filter(a => !a.flow_name || !flowNames.includes(a.flow_name))

  function apiStatus(api: ApiDef): 'ready' | 'empty' | 'unlinked' {
    if (!api.flow_name || !flowNames.includes(api.flow_name)) return 'unlinked'
    const flow = flows.find(f => f.name === api.flow_name)
    if (!flow || flow.steps.length === 0) return 'empty'
    return 'ready'
  }

  const selectedApi  = selectedApiIdx !== null ? apis[selectedApiIdx] ?? null : null
  const selectedFlow = selectedApi
    ? flows.find(f => f.name === selectedApi.flow_name) ?? null
    : null

  // ── Handlers ───────────────────────────────────────────────────────────────

  function handleRemoveApi(idx: number) {
    setApis(apis.filter((_, i) => i !== idx))
    if (selectedApiIdx === idx) setSelectedApiIdx(null)
    else if (selectedApiIdx !== null && selectedApiIdx > idx) setSelectedApiIdx(selectedApiIdx - 1)
  }

  function handleAssignFlow(flowName: string) {
    if (selectedApiIdx === null) return
    const updated = [...apis]
    updated[selectedApiIdx] = { ...updated[selectedApiIdx], flow_name: flowName }
    setApis(updated)
  }

  function handleAssignUnassigned(api: ApiDef, flowName: string) {
    const i = apis.indexOf(api)
    if (i === -1) return
    const updated = [...apis]
    updated[i] = { ...api, flow_name: flowName }
    setApis(updated)
  }

  // Wizard
  function openWizard() {
    setShowWizard(true)
    setSelectedApiIdx(null)
    setWizardStep(1)
    setWizardMethod('GET')
    setWizardPath('')
    setWizardName('')
    setWizardFlowMode('create')
    setWizardExistingFlow('')
    setWizardErr('')
  }

  function wizardNext() {
    setWizardErr('')
    if (!wizardPath.startsWith('/')) { setWizardErr('Path must start with /'); return }
    if (!wizardName.trim()) { setWizardErr('Name must not be empty'); return }
    if (apis.some(a => a.name === wizardName.trim())) { setWizardErr('Name already exists'); return }
    setWizardStep(2)
  }

  function wizardRegister() {
    setWizardErr('')
    const entry: ApiDef = {
      name:      wizardName.trim(),
      path:      wizardPath.trim(),
      method:    wizardMethod,
      flow_name: wizardFlowMode === 'existing' ? wizardExistingFlow : `${wizardName.trim()}_flow`,
    }
    const newApis = [...apis, entry]
    setApis(newApis)

    if (wizardFlowMode === 'create') {
      onCreateFlow(entry.flow_name)
      onNavigateToDesigner()
    } else {
      // Select the newly created API
      setShowWizard(false)
      setSelectedApiIdx(newApis.length - 1)
    }
  }

  // OpenAPI import
  async function handleImport(targetFlow: string) {
    if (!spec.trim() || !targetFlow) return
    setImporting(true)
    setImportMsg('')
    setImportErr(false)
    try {
      const data    = await importOpenAPI(spec)
      const entries = data.apis.map(a => ({
        name: a.name, path: a.path, method: a.method, flow_name: targetFlow,
      }))
      setApis([...apis, ...entries])
      setImportMsg(`Imported ${entries.length} API(s) from ${data.source}`)
    } catch (e) {
      setImportErr(true)
      setImportMsg(e instanceof Error ? e.message : 'Import failed')
    } finally {
      setImporting(false)
    }
  }

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div style={{ display: 'flex', height: 'calc(100vh - 58px)', gap: 0, margin: -14 }}>

      {/* ── Left sidebar: API catalog ──────────────────────────────────── */}
      <div style={{
        width: 280, flexShrink: 0,
        borderRight: '1px solid var(--border)',
        display: 'flex', flexDirection: 'column',
        background: 'var(--panel)',
      }}>
        {/* Header + New API button */}
        <div style={{
          padding: '12px 14px',
          borderBottom: '1px solid var(--border)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <span style={{ fontSize: 13, fontWeight: 600 }}>API Catalog</span>
          <button
            className="btn"
            style={{ width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12 }}
            onClick={openWizard}
          >
            + New API
          </button>
        </div>

        {/* API list */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '8px 8px 0' }}>
          {apis.length === 0 && !showWizard && (
            <div style={{ padding: '24px 16px', textAlign: 'center', color: 'var(--muted)' }}>
              {syncing
                ? <span style={{ fontSize: 12 }}>Checking gateway…</span>
                : (
                  <>
                    <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>No APIs yet</div>
                    <div style={{ fontSize: 11, marginBottom: 12 }}>
                      Create your first API endpoint to start routing traffic through a flow.
                    </div>
                    <button className="btn" onClick={() => setShowWizard(true)}>＋ Create API</button>
                  </>
                )
              }
            </div>
          )}

          {/* Assigned APIs grouped by status */}
          {apis
            .map((a, i) => ({ api: a, idx: i }))
            .filter(({ api }) => api.flow_name && flowNames.includes(api.flow_name))
            .map(({ api, idx }) => {
              const status = apiStatus(api)
              const active = selectedApiIdx === idx && !showWizard
              return (
                <div
                  key={idx}
                  onClick={() => { setSelectedApiIdx(idx); setShowWizard(false) }}
                  style={{
                    padding: '7px 9px',
                    borderRadius: 7,
                    cursor: 'pointer',
                    marginBottom: 3,
                    background: active ? 'rgba(87,181,255,0.1)' : 'transparent',
                    border:     active ? '1px solid var(--accent)' : '1px solid transparent',
                    display: 'flex', alignItems: 'center', gap: 7,
                  }}
                >
                  {/* Status dot */}
                  <span style={{
                    width: 8, height: 8, borderRadius: '50%', flexShrink: 0,
                    background: status === 'ready' ? '#22c55e' : status === 'empty' ? '#f59e0b' : '#ef4444',
                    boxShadow: status === 'ready' ? '0 0 4px #22c55e66' : undefined,
                  }} title={status === 'ready' ? 'Ready' : status === 'empty' ? 'Flow has no steps' : 'No flow linked'} />

                  <MethodBadge method={api.method} />

                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{
                      fontFamily: 'monospace', fontSize: 12,
                      color: active ? 'var(--accent)' : 'var(--text)',
                      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    }}>
                      {api.path}
                    </div>
                    <div style={{ fontSize: 10, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {api.flow_name}
                    </div>
                  </div>

                  {/* Action button */}
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '2px 7px', marginTop: 0, fontSize: 10, flexShrink: 0 }}
                    onClick={e => { e.stopPropagation(); setSelectedApiIdx(idx); setShowWizard(false) }}
                  >
                    {status === 'ready' ? 'Edit' : status === 'empty' ? 'Build' : 'Assign'}
                  </button>
                </div>
              )
            })
          }

          {/* Unassigned bucket */}
          {unassigned.length > 0 && (
            <div style={{ marginTop: 12, marginBottom: 8 }}>
              <div style={{
                fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase',
                letterSpacing: 1, marginBottom: 6, padding: '0 3px',
              }}>
                Unassigned ({unassigned.length})
              </div>
              {unassigned.map((a, i) => {
                const idx = apis.indexOf(a)
                const active = selectedApiIdx === idx && !showWizard
                return (
                  <div
                    key={i}
                    onClick={() => { setSelectedApiIdx(idx); setShowWizard(false) }}
                    style={{
                      padding: '6px 9px',
                      borderRadius: 7,
                      cursor: 'pointer',
                      marginBottom: 3,
                      background: active ? 'rgba(239,68,68,0.08)' : 'rgba(239,68,68,0.03)',
                      border: active ? '1px solid rgba(239,68,68,0.5)' : '1px solid rgba(239,68,68,0.2)',
                      display: 'flex', alignItems: 'center', gap: 7,
                    }}
                  >
                    <span style={{ width: 8, height: 8, borderRadius: '50%', flexShrink: 0, background: '#ef4444' }} />
                    <MethodBadge method={a.method} />
                    <span style={{
                      flex: 1, fontFamily: 'monospace', fontSize: 12,
                      color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    }}>
                      {a.path}
                    </span>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* Deploy shortcut at bottom */}
        {apis.length > 0 && (
          <div style={{ padding: '10px 12px', borderTop: '1px solid var(--border)' }}>
            <button
              className="btn"
              style={{ width: '100%', fontSize: 12 }}
              onClick={onNavigateToDeploy}
            >
              → Publish Now ({apis.length})
            </button>
          </div>
        )}
      </div>

      {/* ── Right panel ────────────────────────────────────────────────── */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflowY: 'auto', background: 'var(--bg)' }}>

        {showWizard ? (
          <WizardPanel
            apis={apis}
            flows={flows}
            wizardStep={wizardStep}
            wizardMethod={wizardMethod}
            wizardPath={wizardPath}
            wizardName={wizardName}
            wizardFlowMode={wizardFlowMode}
            wizardExistingFlow={wizardExistingFlow}
            wizardErr={wizardErr}
            setWizardMethod={setWizardMethod}
            setWizardPath={p => { setWizardPath(p); setWizardName(suggestName(p)) }}
            setWizardName={setWizardName}
            setWizardFlowMode={setWizardFlowMode}
            setWizardExistingFlow={setWizardExistingFlow}
            onNext={wizardNext}
            onBack={() => setWizardStep(1)}
            onRegister={wizardRegister}
            onCancel={() => setShowWizard(false)}
          />
        ) : selectedApi !== null && selectedApiIdx !== null ? (
          <DetailPanel
            api={selectedApi}
            apiIdx={selectedApiIdx}
            flow={selectedFlow}
            flows={flows}
            spec={spec}
            importMsg={importMsg}
            importErr={importErr}
            importing={importing}
            setSpec={setSpec}
            onRemove={() => handleRemoveApi(selectedApiIdx)}
            onAssignFlow={handleAssignFlow}
            onCreateFlow={(name) => { onCreateFlow(name); handleAssignFlow(name) }}
            onNavigateToDesigner={onNavigateToDesigner}
            onNavigateToDeploy={onNavigateToDeploy}
            onImport={handleImport}
          />
        ) : (
          <EmptyRight onNewApi={openWizard} onNavigateToDesigner={onNavigateToDesigner} />
        )}
      </div>
    </div>
  )
}

// ── Empty right panel ────────────────────────────────────────────────────────

function EmptyRight({ onNewApi, onNavigateToDesigner }: { onNewApi: () => void; onNavigateToDesigner: () => void }) {
  return (
    <div style={{
      flex: 1, display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center',
      gap: 14, padding: 40, color: 'var(--muted)',
    }}>
      <span style={{ fontSize: 36, opacity: 0.2 }}>⚡</span>
      <div style={{ textAlign: 'center' }}>
        <p style={{ fontSize: 14, color: 'var(--text)', marginBottom: 6 }}>Register your first API endpoint</p>
        <p style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 20, lineHeight: 1.6 }}>
          Connect HTTP routes to flows. Each endpoint routes incoming requests<br />
          to a flow that handles authentication, upstream calls, and responses.
        </p>
      </div>
      <div style={{ display: 'flex', gap: 10 }}>
        <button className="btn" style={{ width: 'auto', padding: '6px 18px', marginTop: 0 }} onClick={onNewApi}>
          + New API
        </button>
        <button className="btn muted" style={{ width: 'auto', padding: '6px 18px', marginTop: 0 }} onClick={onNavigateToDesigner}>
          → Flow Designer
        </button>
      </div>
    </div>
  )
}

// ── Detail panel ─────────────────────────────────────────────────────────────

interface DetailProps {
  api: ApiDef
  apiIdx: number
  flow: SavedFlow | null
  flows: SavedFlow[]
  spec: string
  importMsg: string
  importErr: boolean
  importing: boolean
  setSpec: (s: string) => void
  onRemove: () => void
  onAssignFlow: (name: string) => void
  onCreateFlow: (name: string) => void
  onNavigateToDesigner: () => void
  onNavigateToDeploy: () => void
  onImport: (targetFlow: string) => void
}

function DetailPanel({
  api, flow, flows,
  spec, importMsg, importErr, importing,
  setSpec,
  onRemove, onAssignFlow, onCreateFlow,
  onNavigateToDesigner, onNavigateToDeploy, onImport,
}: DetailProps) {
  const [importFlow, setImportFlow] = useState(api.flow_name ?? '')

  // Sync importFlow when api changes
  const resolvedImportFlow = api.flow_name || importFlow

  const hasFlow  = !!flow
  const hasSteps = hasFlow && flow!.steps.length > 0

  function handleCreateAndAssign() {
    const name = `${api.name}_flow`
    onCreateFlow(name)
    onNavigateToDesigner()
  }

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      {/* Header */}
      <div style={{
        padding: '16px 20px',
        borderBottom: '1px solid var(--border)',
        background: 'var(--panel)',
        flexShrink: 0,
      }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 4 }}>
              <MethodBadge method={api.method} large />
              <span style={{ fontFamily: 'monospace', fontSize: 16, fontWeight: 600 }}>{api.path}</span>
            </div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{api.name}</div>
          </div>
          <button
            className="btn muted"
            style={{ width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12, flexShrink: 0 }}
            onClick={onRemove}
          >
            Remove API
          </button>
        </div>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>

        {/* Flow assignment */}
        <Section label="Flow Assignment">
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
            <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>Flow:</span>
            <div style={{ flex: 1, maxWidth: 360 }}>
              <FlowSearchSelect
                flows={flows}
                value={api.flow_name ?? ''}
                onChange={onAssignFlow}
                placeholder="search or select a flow…"
              />
            </div>
          </div>

          {/* Flow states */}
          {!hasFlow && (
            <div style={{
              padding: '14px 16px',
              borderRadius: 8,
              background: 'rgba(239,68,68,0.06)',
              border: '1px solid rgba(239,68,68,0.2)',
            }}>
              <p style={{ fontSize: 13, color: 'var(--muted)', marginBottom: 12 }}>
                No flow assigned yet.
              </p>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
                  onClick={() => { /* FlowSearchSelect handles pick */ }}
                >
                  Pick existing flow ▼
                </button>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
                  onClick={handleCreateAndAssign}
                >
                  + Create new flow
                </button>
              </div>
            </div>
          )}

          {hasFlow && !hasSteps && (
            <div style={{
              padding: '12px 16px',
              borderRadius: 8,
              background: 'rgba(251,191,36,0.06)',
              border: '1px solid rgba(251,191,36,0.25)',
            }}>
              <p style={{ fontSize: 13, color: '#fbbf24', marginBottom: 10 }}>
                ⚠ Flow "{flow!.name}" has no steps yet.
              </p>
              <button
                className="btn"
                style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
                onClick={onNavigateToDesigner}
              >
                Build this flow in Designer →
              </button>
            </div>
          )}
        </Section>

        {/* Mini pipeline preview */}
        {hasFlow && hasSteps && (
          <Section label="Pipeline Preview" style={{ marginTop: 20 }}>
            <MiniPipeline api={api} steps={flow!.steps} />
            <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
                onClick={onNavigateToDesigner}
              >
                Edit Flow in Designer →
              </button>
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
                onClick={onNavigateToDeploy}
              >
                Go to Deploy →
              </button>
            </div>
          </Section>
        )}

        {/* API Interface — inputs/outputs derived from flow steps */}
        {hasFlow && hasSteps && (() => {
          const { inputs, outputs } = deriveApiInterface(flow!.steps)
          if (inputs.length === 0 && outputs.length === 0) return null
          return (
            <Section label="API Interface" style={{ marginTop: 20 }}>
              {inputs.length > 0 && (
                <div style={{ marginBottom: outputs.length > 0 ? 14 : 0 }}>
                  <div style={{ fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>
                    Expects (inputs)
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '56px 1fr 1fr', gap: '3px 8px', fontSize: 11 }}>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>SOURCE</span>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>FIELD</span>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>→ VARIABLE</span>
                    {inputs.map((b, i) => (
                      <>
                        <span key={`s${i}`} style={{
                          padding: '2px 5px', borderRadius: 4, fontSize: 10, fontWeight: 700,
                          background: b.source === 'header' ? 'rgba(87,181,255,0.15)' : b.source === 'body' ? 'rgba(52,211,153,0.15)' : 'rgba(251,191,36,0.15)',
                          color:      b.source === 'header' ? '#57b5ff'              : b.source === 'body' ? '#34d399'              : '#fbbf24',
                          alignSelf: 'center',
                        }}>{b.source}</span>
                        <code key={`f${i}`} style={{ fontFamily: 'monospace', color: 'var(--text)', alignSelf: 'center' }}>{b.field || '—'}</code>
                        <code key={`v${i}`} style={{ fontFamily: 'monospace', color: '#a78bfa', alignSelf: 'center' }}>{b.variable || '—'}</code>
                      </>
                    ))}
                  </div>
                </div>
              )}
              {outputs.length > 0 && (
                <div>
                  <div style={{ fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>
                    Returns (outputs)
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '120px 1fr', gap: '3px 8px', fontSize: 11 }}>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>ACTION</span>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>VALUE</span>
                    {outputs.map((o, i) => (
                      <>
                        <span key={`oa${i}`} style={{ color: 'var(--muted)', alignSelf: 'center' }}>{o.action}</span>
                        <code key={`ov${i}`} style={{ fontFamily: 'monospace', color: '#34d399', alignSelf: 'center' }}>{o.value || '—'}</code>
                      </>
                    ))}
                  </div>
                </div>
              )}
            </Section>
          )
        })()}

        {/* Try it — curl snippet */}
        {hasFlow && hasSteps && (() => {
          const { inputs } = deriveApiInterface(flow!.steps)
          const headerInputs = inputs.filter(b => b.source === 'header')
          const bodyInputs   = inputs.filter(b => b.source === 'body')
          const queryInputs  = inputs.filter(b => b.source === 'query')

          const method  = api.method ?? 'POST'
          const path    = api.path.replace(/\{(\w+)\}/g, '<$1>')
          const bodyObj = bodyInputs.length > 0
            ? JSON.stringify(Object.fromEntries(bodyInputs.map(b => [b.field, `<${b.field}>`])), null, 2)
            : (method !== 'GET' ? '{}' : null)
          const queryStr = queryInputs.length > 0
            ? '?' + queryInputs.map(b => `${b.field}=<${b.field}>`).join('&')
            : ''

          const curl = [
            `curl -X ${method} http://localhost:8080${path}${queryStr}`,
            `  -H 'Content-Type: application/json'`,
            ...headerInputs.map(b => `  -H '${b.field}: <${b.field.toLowerCase().replace(/[^a-z0-9]/g, '_')}>'`),
            ...(bodyObj ? [`  -d '${bodyObj}'`] : []),
          ].join(' \\\n')

          return (
            <div style={{ marginTop: 16, marginBottom: 8 }}>
              <details style={{ borderRadius: 8, border: '1px solid var(--border)', overflow: 'hidden' }}>
                <summary style={{
                  padding: '10px 14px', cursor: 'pointer', fontSize: 12,
                  color: 'var(--muted)', background: 'var(--panel)',
                  userSelect: 'none', listStyle: 'none',
                  display: 'flex', alignItems: 'center', gap: 6,
                }}>
                  <span style={{ fontSize: 10, opacity: 0.6 }}>▶</span>
                  Try it — curl snippet
                </summary>
                <div style={{ padding: '14px 14px 16px', background: 'var(--bg)' }}>
                  <pre style={{
                    fontFamily: 'monospace', fontSize: 11, color: 'var(--text)',
                    background: 'rgba(0,0,0,0.2)', borderRadius: 6, padding: '10px 12px',
                    overflowX: 'auto', margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                  }}>{curl}</pre>
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '3px 12px', marginTop: 10, fontSize: 11 }}
                    onClick={() => navigator.clipboard.writeText(curl).catch(() => {})}
                  >
                    Copy
                  </button>
                  <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 8, opacity: 0.7 }}>
                    Replace &lt;placeholders&gt; with real values. Adjust host if not running locally.
                  </div>
                </div>
              </details>
            </div>
          )
        })()}

        {/* OpenAPI import collapsible */}
        <div style={{ marginTop: 24 }}>
          <details style={{ borderRadius: 8, border: '1px solid var(--border)', overflow: 'hidden' }}>
            <summary style={{
              padding: '10px 14px',
              cursor: 'pointer',
              fontSize: 12,
              color: 'var(--muted)',
              background: 'var(--panel)',
              userSelect: 'none',
              listStyle: 'none',
              display: 'flex',
              alignItems: 'center',
              gap: 6,
            }}>
              <span style={{ fontSize: 10, opacity: 0.6 }}>▶</span>
              Import endpoints from OpenAPI spec
            </summary>
            <div style={{ padding: '14px 14px 16px', background: 'var(--bg)' }}>
              {!api.flow_name && (
                <div style={{ marginBottom: 12 }}>
                  <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 6 }}>Target flow for import:</div>
                  <FlowSearchSelect
                    flows={flows}
                    value={importFlow}
                    onChange={setImportFlow}
                    placeholder="select target flow…"
                  />
                </div>
              )}
              {api.flow_name && (
                <p style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
                  Endpoints will be imported into <strong style={{ color: 'var(--accent)' }}>{api.flow_name}</strong>.
                </p>
              )}
              <textarea
                className="input"
                placeholder="Paste OpenAPI spec (JSON or YAML)…"
                value={spec}
                onChange={e => setSpec(e.target.value)}
                style={{ minHeight: 110 }}
              />
              <button
                className="btn mt8"
                style={{ width: 'auto', padding: '0 18px' }}
                onClick={() => onImport(resolvedImportFlow)}
                disabled={importing || !resolvedImportFlow}
              >
                {importing ? 'Importing…' : `Import → ${resolvedImportFlow || '(select flow)'}`}
              </button>
              {importMsg && (
                <p className={`mt8 ${importErr ? 'status-err' : 'status-ok'}`}>{importMsg}</p>
              )}
            </div>
          </details>
        </div>

      </div>
    </div>
  )
}

// ── Mini pipeline preview ────────────────────────────────────────────────────

function MiniPipeline({ api, steps }: { api: ApiDef; steps: FlowStep[] }) {
  // Build zone-segmented step list
  type RenderedItem =
    | { kind: 'zone-header'; zone: Zone }
    | { kind: 'step'; step: FlowStep; zone: Zone }
    | { kind: 'boundary' }

  const items: RenderedItem[] = []
  let lastZone: Zone | null   = null
  let hasResponseZone         = false

  steps.forEach((step, i) => {
    const zone = stepZone(step.action)
    if (zone !== lastZone) {
      items.push({ kind: 'zone-header', zone })
      lastZone = zone
    }
    items.push({ kind: 'step', step, zone })
    if (zone === 'response' && !hasResponseZone) {
      // We'll add boundary after all response-zone steps in a second pass
      hasResponseZone = true
    }
  })

  // Insert boundary after last response-zone step
  let boundaryInserted = false
  const finalItems: RenderedItem[] = []
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i]
    if (!boundaryInserted && item.kind === 'step' && item.zone === 'response') {
      finalItems.unshift({ kind: 'boundary' })
      boundaryInserted = true
    }
    finalItems.unshift(item)
  }

  return (
    <div style={{
      background: 'rgba(0,0,0,0.2)',
      borderRadius: 8,
      border: '1px solid var(--border)',
      overflow: 'hidden',
      fontFamily: 'monospace',
      fontSize: 12,
    }}>
      {/* Request line */}
      <div style={{
        padding: '7px 12px',
        background: 'rgba(87,181,255,0.08)',
        borderBottom: '1px solid var(--border)',
        fontSize: 11,
        color: 'var(--muted)',
        display: 'flex', alignItems: 'center', gap: 8,
      }}>
        <span style={{ color: '#57b5ff' }}>↓ REQUEST IN</span>
        <MethodBadge method={api.method} />
        <span style={{ color: 'var(--text)' }}>{api.path}</span>
      </div>

      {/* Steps */}
      {finalItems.map((item, i) => {
        if (item.kind === 'zone-header') {
          const meta = ZONE_META[item.zone]
          return (
            <div key={`zh-${i}`} style={{
              padding: '4px 12px',
              background: `${meta.color}14`,
              borderTop: i > 0 ? `1px solid ${meta.color}30` : undefined,
              borderBottom: `1px solid ${meta.color}30`,
              display: 'flex', alignItems: 'center', gap: 6,
            }}>
              <span style={{ fontSize: 10 }}>{meta.icon}</span>
              <span style={{
                fontSize: 9, fontWeight: 700, letterSpacing: 1,
                color: meta.color, textTransform: 'uppercase', fontFamily: 'sans-serif',
              }}>
                {meta.label}
              </span>
            </div>
          )
        }

        if (item.kind === 'boundary') {
          return (
            <div key={`boundary-${i}`} style={{
              padding: '5px 12px',
              borderTop: '1px dashed #34d39966',
              borderBottom: '1px dashed #34d39966',
              background: 'rgba(52,211,153,0.04)',
              fontSize: 10,
              color: '#34d399',
              display: 'flex', alignItems: 'center', gap: 6,
              fontFamily: 'sans-serif',
            }}>
              <span>↩</span>
              <span style={{ letterSpacing: 0.5 }}>CLIENT RECEIVES RESPONSE</span>
            </div>
          )
        }

        // step
        const { step, zone } = item
        const meta = ZONE_META[zone]
        const inputRef = (step.key_identifier || step.source) as string | undefined
        const outputRef = step.as as string | undefined

        let conditionText: string | null = null
        if (step.action === 'if' && step.condition) {
          const thenStr = step.then_flow ? `✓ ${step.then_flow}` : ''
          const elseStr = step.else_flow ? `✗ ${step.else_flow}` : ''
          conditionText = `${step.condition}  ${thenStr} ${elseStr}`.trim()
        }

        return (
          <div key={`step-${i}`} style={{
            padding: '4px 12px 4px 0',
            display: 'flex', alignItems: 'baseline', gap: 0,
            borderBottom: '1px solid rgba(255,255,255,0.03)',
          }}>
            {/* Zone color bar */}
            <div style={{
              width: 3, alignSelf: 'stretch', flexShrink: 0,
              background: meta.color,
              marginRight: 10,
              opacity: 0.6,
            }} />
            {/* Bullet */}
            <span style={{ color: meta.color, marginRight: 6, fontSize: 10, flexShrink: 0 }}>•</span>
            {/* Action */}
            <span style={{ color: 'var(--text)', fontWeight: 500, flexShrink: 0 }}>
              {step.action}
            </span>
            {/* Input ref */}
            {inputRef && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11 }}>
                ← <span style={{ color: '#57b5ff' }}>{inputRef}</span>
              </span>
            )}
            {/* Output ref */}
            {outputRef && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11 }}>
                → <span style={{ color: '#34d399' }}>{outputRef}</span>
              </span>
            )}
            {/* Condition */}
            {conditionText && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11, fontStyle: 'italic' }}>
                {conditionText}
              </span>
            )}
          </div>
        )
      })}

      {/* If no response zone, show boundary at end */}
      {!hasResponseZone && (
        <div style={{
          padding: '5px 12px',
          borderTop: '1px dashed #34d39966',
          background: 'rgba(52,211,153,0.04)',
          fontSize: 10,
          color: '#34d399',
          display: 'flex', alignItems: 'center', gap: 6,
          fontFamily: 'sans-serif',
        }}>
          <span>↩</span>
          <span style={{ letterSpacing: 0.5 }}>CLIENT RECEIVES RESPONSE</span>
        </div>
      )}
    </div>
  )
}

// ── Wizard panel ─────────────────────────────────────────────────────────────

interface WizardProps {
  apis: ApiDef[]
  flows: SavedFlow[]
  wizardStep: 1 | 2
  wizardMethod: string
  wizardPath: string
  wizardName: string
  wizardFlowMode: 'create' | 'existing'
  wizardExistingFlow: string
  wizardErr: string
  setWizardMethod: (v: string) => void
  setWizardPath: (v: string) => void
  setWizardName: (v: string) => void
  setWizardFlowMode: (v: 'create' | 'existing') => void
  setWizardExistingFlow: (v: string) => void
  onNext: () => void
  onBack: () => void
  onRegister: () => void
  onCancel: () => void
}

function WizardPanel({
  apis, flows,
  wizardStep, wizardMethod, wizardPath, wizardName,
  wizardFlowMode, wizardExistingFlow, wizardErr,
  setWizardMethod, setWizardPath, setWizardName,
  setWizardFlowMode, setWizardExistingFlow,
  onNext, onBack, onRegister, onCancel,
}: WizardProps) {
  const newFlowName = wizardName ? `${wizardName}_flow` : '_flow'

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '40px 20px' }}>
      <div style={{
        width: '100%', maxWidth: 520,
        background: 'var(--panel)',
        borderRadius: 10,
        border: '1px solid var(--border)',
        overflow: 'hidden',
      }}>
        {/* Wizard header */}
        <div style={{
          padding: '16px 20px',
          borderBottom: '1px solid var(--border)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <div>
            <div style={{ fontSize: 14, fontWeight: 600, marginBottom: 2 }}>
              {wizardStep === 1 ? 'Step 1 of 2 — Define the endpoint' : 'Step 2 of 2 — Assign a flow'}
            </div>
            {/* Step indicators */}
            <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              {[1, 2].map(n => (
                <div key={n} style={{
                  width: 20, height: 4, borderRadius: 2,
                  background: n <= wizardStep ? 'var(--accent)' : 'rgba(255,255,255,0.1)',
                  transition: 'background 0.2s',
                }} />
              ))}
            </div>
          </div>
          <button
            style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', fontSize: 18, lineHeight: 1 }}
            onClick={onCancel}
            title="Cancel"
          >×</button>
        </div>

        <div style={{ padding: '20px 20px 24px' }}>
          {wizardStep === 1 ? (
            <>
              <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', marginBottom: 14 }}>
                {/* Method */}
                <div style={{ flexShrink: 0 }}>
                  <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 5 }}>Method</div>
                  <select
                    className="input"
                    value={wizardMethod}
                    onChange={e => setWizardMethod(e.target.value)}
                    style={{ width: 90, marginTop: 0 }}
                  >
                    {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => <option key={m}>{m}</option>)}
                  </select>
                </div>
                {/* Path */}
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 5 }}>Path</div>
                  <input
                    className="input"
                    placeholder="/v1/orders"
                    value={wizardPath}
                    onChange={e => setWizardPath(e.target.value)}
                    onKeyDown={e => e.key === 'Enter' && onNext()}
                    style={{ width: '100%', marginTop: 0 }}
                    autoFocus
                  />
                </div>
              </div>
              {/* Path param hint */}
              {(() => {
                const params = (wizardPath.match(/\{(\w+)\}/g) ?? []).map(p => p.slice(1, -1))
                if (params.length === 0) return null
                return (
                  <div style={{ fontSize: 11, color: 'var(--accent)', marginBottom: 10, lineHeight: 1.6 }}>
                    Path params detected: {params.map(p => (
                      <code key={p} style={{
                        fontFamily: 'monospace', background: 'rgba(87,181,255,0.12)',
                        padding: '1px 5px', borderRadius: 3, marginRight: 5,
                      }}>{'{' + p + '}'}</code>
                    ))}
                    <span style={{ color: 'var(--muted)' }}>
                      — add "Read Body Field" steps in your flow to bind these to variables
                    </span>
                  </div>
                )
              })()}
              {/* Name */}
              <div style={{ marginBottom: 16 }}>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 5 }}>
                  Name <span style={{ fontSize: 10, opacity: 0.6 }}>(auto-suggested)</span>
                </div>
                <input
                  className="input"
                  placeholder="v1_orders"
                  value={wizardName}
                  onChange={e => setWizardName(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && onNext()}
                  style={{ width: '100%', marginTop: 0 }}
                />
                {wizardName && apis.some(a => a.name === wizardName) && (
                  <div style={{ fontSize: 11, color: '#f97316', marginTop: 4 }}>
                    Name "{wizardName}" already exists
                  </div>
                )}
              </div>

              {wizardErr && (
                <div style={{ fontSize: 12, color: '#ef4444', marginBottom: 12 }}>{wizardErr}</div>
              )}

              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '6px 20px', marginTop: 0 }}
                  onClick={onNext}
                >
                  Next →
                </button>
              </div>
            </>
          ) : (
            <>
              <p style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 16 }}>
                How should{' '}
                <MethodBadge method={wizardMethod} />{' '}
                <span style={{ fontFamily: 'monospace', color: 'var(--text)' }}>{wizardPath}</span>{' '}
                be handled?
              </p>

              {/* Option: Create new */}
              <label style={{
                display: 'flex', gap: 10, alignItems: 'flex-start',
                padding: '12px 14px', borderRadius: 8, cursor: 'pointer',
                marginBottom: 10,
                background: wizardFlowMode === 'create' ? 'rgba(87,181,255,0.08)' : 'var(--step-bg)',
                border: wizardFlowMode === 'create' ? '1px solid var(--accent)' : '1px solid transparent',
                transition: 'all 0.15s',
              }}>
                <input
                  type="radio"
                  value="create"
                  checked={wizardFlowMode === 'create'}
                  onChange={() => setWizardFlowMode('create')}
                  style={{ marginTop: 2 }}
                />
                <div>
                  <div style={{ fontSize: 13, fontWeight: 500, marginBottom: 2 }}>
                    Create new flow:{' '}
                    <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>{newFlowName}</span>
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--muted)' }}>
                    Opens Designer to build from scratch.
                  </div>
                </div>
              </label>

              {/* Option: Use existing */}
              <label style={{
                display: 'flex', gap: 10, alignItems: 'flex-start',
                padding: '12px 14px', borderRadius: 8, cursor: 'pointer',
                marginBottom: 16,
                background: wizardFlowMode === 'existing' ? 'rgba(87,181,255,0.08)' : 'var(--step-bg)',
                border: wizardFlowMode === 'existing' ? '1px solid var(--accent)' : '1px solid transparent',
                transition: 'all 0.15s',
              }}>
                <input
                  type="radio"
                  value="existing"
                  checked={wizardFlowMode === 'existing'}
                  onChange={() => setWizardFlowMode('existing')}
                  style={{ marginTop: 2 }}
                />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 13, fontWeight: 500, marginBottom: 6 }}>
                    Use existing flow:
                  </div>
                  {wizardFlowMode === 'existing' && (
                    <FlowSearchSelect
                      flows={flows}
                      value={wizardExistingFlow}
                      onChange={setWizardExistingFlow}
                      placeholder="search or select a flow…"
                    />
                  )}
                  {wizardFlowMode !== 'existing' && (
                    <div style={{ fontSize: 11, color: 'var(--muted)' }}>
                      Route to an already-built flow.
                    </div>
                  )}
                </div>
              </label>

              {wizardErr && (
                <div style={{ fontSize: 12, color: '#ef4444', marginBottom: 12 }}>{wizardErr}</div>
              )}

              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '6px 16px', marginTop: 0 }}
                  onClick={onBack}
                >
                  ← Back
                </button>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '6px 20px', marginTop: 0 }}
                  disabled={wizardFlowMode === 'existing' && !wizardExistingFlow}
                  onClick={onRegister}
                >
                  Register API
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

// ── Small helpers ────────────────────────────────────────────────────────────

function MethodBadge({ method, large }: { method: string; large?: boolean }) {
  return (
    <span style={{
      fontSize: large ? 12 : 10, fontWeight: 700,
      padding: large ? '3px 9px' : '2px 7px',
      borderRadius: 4,
      color: '#fff',
      background: METHOD_COLOR[method] ?? '#64748b',
      minWidth: large ? 55 : 50, textAlign: 'center',
      letterSpacing: 0.4, flexShrink: 0,
      display: 'inline-block',
    }}>
      {method}
    </span>
  )
}

function Section({ label, children, style }: { label: string; children: React.ReactNode; style?: React.CSSProperties }) {
  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14, ...style }}>
      <div style={{
        fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase',
        letterSpacing: 1, marginBottom: 10,
      }}>
        {label}
      </div>
      {children}
    </div>
  )
}
