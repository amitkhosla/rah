import { useState, useEffect } from 'react'
import { fetchGatewaySnapshot } from '../api'
import type { ApiDef, EndpointDef, FlowStep, SavedFlow } from '../types'
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

// ── Full path computation ─────────────────────────────────────────────────────

function fullPath(basePath: string, subPath: string): string {
  const base = basePath.replace(/\/+$/, '')
  const sub  = subPath.startsWith('/') ? subPath : '/' + subPath
  return sub === '/' ? base || '/' : base + sub
}

// ── Resolve flow for endpoint ─────────────────────────────────────────────────

function resolveFlow(ep: EndpointDef, api: ApiDef): string {
  return ep.flowName ?? api.defaultFlow
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
  // Selection state
  const [selectedApiId,      setSelectedApiId]      = useState<string | null>(null)
  const [selectedEndpointId, setSelectedEndpointId] = useState<string | null>(null)
  const [showWizard,         setShowWizard]          = useState(false)

  // Wizard state
  const [wizardStep,   setWizardStep]   = useState<1 | 2 | 3>(1)
  const [wizardApiId,  setWizardApiId]  = useState<string | null>(null) // null = new API
  // Step 1
  const [wBasePaths,   setWBasePaths]   = useState<string[]>(['']) // [primary, ...aliases]
  const [wApiName,     setWApiName]     = useState('')
  const [wDefaultFlow, setWDefaultFlow] = useState('')
  const [wFlowMode,    setWFlowMode]    = useState<'create' | 'existing'>('create')
  // Step 2
  const [wEndpoints,   setWEndpoints]   = useState<Array<{ id: string; subPath: string; method: string; flowName?: string; overrideFlow: boolean }>>([])
  const [wizardErr,    setWizardErr]    = useState('')

  // Sync state
  const [syncing, setSyncing] = useState(true)

  // On mount: fetch gateway snapshot and seed local API list
  useEffect(() => {
    fetchGatewaySnapshot()
      .then(state => {
        if (state?.apis?.length) {
          const grouped = new Map<string, ApiDef>()
          for (const ga of state.apis) {
            const method = ga.method ?? 'POST'
            if (grouped.has(ga.path)) {
              grouped.get(ga.path)!.endpoints.push({
                id: crypto.randomUUID(),
                subPath: '/',
                method,
              })
            } else {
              grouped.set(ga.path, {
                id: crypto.randomUUID(),
                name: ga.name,
                basePath: ga.path,
                defaultFlow: ga.flow_name ?? '',
                endpoints: [{ id: crypto.randomUUID(), subPath: '/', method }],
              })
            }
          }
          const incoming = Array.from(grouped.values()).filter(
            g => !apis.find(a => a.basePath === g.basePath)
          )
          if (incoming.length > 0) setApis([...apis, ...incoming])
        }
        for (const gf of (state?.flows ?? [])) {
          onCreateFlow(gf.name)
        }
        setSyncing(false)
      })
      .catch(() => setSyncing(false))
  }, [])

  // ── Derived ────────────────────────────────────────────────────────────────

  const flowNames = flows.map(f => f.name)

  function endpointStatus(ep: EndpointDef, api: ApiDef): 'ready' | 'empty' | 'unlinked' {
    const fn = resolveFlow(ep, api)
    if (!fn || !flowNames.includes(fn)) return 'unlinked'
    const flow = flows.find(f => f.name === fn)
    if (!flow || flow.steps.length === 0) return 'empty'
    return 'ready'
  }

  const totalEndpoints = apis.reduce((sum, a) => sum + a.endpoints.length, 0)

  // ── Wizard helpers ─────────────────────────────────────────────────────────

  function openNewWizard() {
    setShowWizard(true)
    setSelectedApiId(null)
    setSelectedEndpointId(null)
    setWizardApiId(null)
    setWizardStep(1)
    setWBasePaths([''])
    setWApiName('')
    setWDefaultFlow('')
    setWFlowMode('create')
    setWEndpoints([{ id: crypto.randomUUID(), subPath: '/', method: 'GET', overrideFlow: false }])
    setWizardErr('')
  }

  function openAddEndpointWizard(api: ApiDef) {
    setShowWizard(true)
    setSelectedApiId(null)
    setSelectedEndpointId(null)
    setWizardApiId(api.id)
    setWizardStep(2)
    setWBasePaths([api.basePath, ...(api.aliasPaths ?? [])])
    setWApiName(api.name)
    setWDefaultFlow(api.defaultFlow)
    setWFlowMode('existing')
    setWEndpoints([{ id: crypto.randomUUID(), subPath: '/', method: 'GET', overrideFlow: false }])
    setWizardErr('')
  }

  function wizardStep1Next() {
    setWizardErr('')
    if (wBasePaths.some(p => !p.trim())) { setWizardErr('All basepaths must be non-empty'); return }
    if (wBasePaths.some(p => !p.trim().startsWith('/'))) { setWizardErr('All basepaths must start with /'); return }
    if (wBasePaths.length !== new Set(wBasePaths.map(p => p.trim())).size) {
      setWizardErr('Basepaths must be unique'); return
    }
    if (!wApiName.trim()) { setWizardErr('Name must not be empty'); return }
    if (wizardApiId === null && apis.some(a => a.name === wApiName.trim())) {
      setWizardErr('Name already exists'); return
    }
    if (wFlowMode === 'existing' && !wDefaultFlow) {
      setWizardErr('Please select an existing flow'); return
    }
    if (wFlowMode === 'create' && !wDefaultFlow.trim()) {
      setWizardErr('Please enter a name for the new flow'); return
    }
    setWizardStep(2)
  }

  function wizardStep2Next() {
    setWizardErr('')
    if (wEndpoints.length === 0) { setWizardErr('Add at least one endpoint'); return }
    for (const ep of wEndpoints) {
      if (!ep.subPath.startsWith('/')) { setWizardErr('Sub-paths must start with /'); return }
      if (!ep.method) { setWizardErr('Each endpoint needs a method'); return }
      if (ep.overrideFlow && !ep.flowName?.trim()) {
        setWizardErr('Each overridden endpoint needs a flow selected'); return
      }
    }
    setWizardStep(3)
  }

  function wizardConfirm() {
    const newEndpoints: EndpointDef[] = wEndpoints.map(e => ({
      id: e.id,
      subPath: e.subPath,
      method: e.method,
      flowName: e.overrideFlow && e.flowName ? e.flowName : undefined,
    }))

    if (wizardApiId === null) {
      // Create new API
      const newApi: ApiDef = {
        id: crypto.randomUUID(),
        name: wApiName.trim(),
        basePath: wBasePaths[0].trim(),
        aliasPaths: wBasePaths.slice(1).map(p => p.trim()).filter(Boolean),
        defaultFlow: wFlowMode === 'existing' ? wDefaultFlow : wDefaultFlow.trim(),
        endpoints: newEndpoints,
      }
      setApis([...apis, newApi])
      if (wFlowMode === 'create') onCreateFlow(newApi.defaultFlow)
      setShowWizard(false)
      setSelectedApiId(newApi.id)
    } else {
      // Add endpoints to existing API
      const updated = apis.map(a =>
        a.id === wizardApiId
          ? { ...a, endpoints: [...a.endpoints, ...newEndpoints] }
          : a
      )
      setApis(updated)
      setShowWizard(false)
      setSelectedApiId(wizardApiId)
    }
  }

  function cancelWizard() {
    setShowWizard(false)
  }

  // ── Remove handlers ────────────────────────────────────────────────────────

  function handleRemoveApi(apiId: string) {
    setApis(apis.filter(a => a.id !== apiId))
    if (selectedApiId === apiId) { setSelectedApiId(null); setSelectedEndpointId(null) }
  }

  function handleRemoveEndpoint(apiId: string, endpointId: string) {
    const api = apis.find(a => a.id === apiId)
    if (!api) return
    if (api.endpoints.length <= 1) {
      // Remove entire API
      handleRemoveApi(apiId)
    } else {
      const updated = apis.map(a =>
        a.id === apiId
          ? { ...a, endpoints: a.endpoints.filter(e => e.id !== endpointId) }
          : a
      )
      setApis(updated)
      if (selectedEndpointId === endpointId) setSelectedEndpointId(null)
    }
  }

  function handleUpdateApiDefaultFlow(apiId: string, flowName: string) {
    setApis(apis.map(a => a.id === apiId ? { ...a, defaultFlow: flowName } : a))
  }

  function handleRemoveAlias(apiId: string, index: number) {
    setApis(apis.map(a => a.id === apiId
      ? { ...a, aliasPaths: (a.aliasPaths ?? []).filter((_, j) => j !== index) }
      : a))
  }

  function handleSetEndpointFlow(apiId: string, endpointId: string, flowName: string | undefined) {
    setApis(apis.map(a =>
      a.id === apiId
        ? { ...a, endpoints: a.endpoints.map(e => e.id === endpointId ? { ...e, flowName } : e) }
        : a
    ))
  }

  // ── Selection ──────────────────────────────────────────────────────────────

  const selectedApi = selectedApiId ? apis.find(a => a.id === selectedApiId) ?? null : null
  const selectedEndpoint = selectedApi && selectedEndpointId
    ? selectedApi.endpoints.find(e => e.id === selectedEndpointId) ?? null
    : null

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <div style={{ display: 'flex', height: 'calc(100vh - 58px)', gap: 0, margin: -14 }}>

      {/* ── Left sidebar ──────────────────────────────────────────────── */}
      <div style={{
        width: 290, flexShrink: 0,
        borderRight: '1px solid var(--border)',
        display: 'flex', flexDirection: 'column',
        background: 'var(--panel)',
      }}>
        {/* Header */}
        <div style={{
          padding: '12px 14px',
          borderBottom: '1px solid var(--border)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <span style={{ fontSize: 13, fontWeight: 600 }}>API Catalog</span>
          <button
            className="btn"
            style={{ width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12 }}
            onClick={openNewWizard}
          >
            + New API
          </button>
        </div>

        {/* API accordion list */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '8px 8px 0' }}>
          {apis.length === 0 && !showWizard && (
            <div style={{ padding: '24px 16px', textAlign: 'center', color: 'var(--muted)' }}>
              {syncing
                ? <span style={{ fontSize: 12 }}>Checking gateway…</span>
                : (
                  <>
                    <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>No APIs yet</div>
                    <div style={{ fontSize: 11, marginBottom: 12 }}>
                      Create your first API to start routing traffic through flows.
                    </div>
                    <button className="btn" onClick={openNewWizard}>＋ Create API</button>
                  </>
                )
              }
            </div>
          )}

          {apis.map(api => {
            const isApiSelected = selectedApiId === api.id && !selectedEndpointId && !showWizard
            return (
              <div key={api.id} style={{ marginBottom: 6 }}>
                {/* Accordion header: basePath + API name */}
                <div
                  onClick={() => {
                    setSelectedApiId(api.id)
                    setSelectedEndpointId(null)
                    setShowWizard(false)
                  }}
                  style={{
                    padding: '7px 9px',
                    borderRadius: 7,
                    cursor: 'pointer',
                    background: isApiSelected ? 'rgba(87,181,255,0.1)' : 'rgba(255,255,255,0.03)',
                    border: isApiSelected ? '1px solid var(--accent)' : '1px solid var(--border)',
                    display: 'flex', alignItems: 'center', gap: 6,
                  }}
                >
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{
                      fontFamily: 'monospace', fontSize: 12, fontWeight: 600,
                      color: isApiSelected ? 'var(--accent)' : 'var(--text)',
                      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    }}>
                      {api.basePath}
                    </div>
                    <div style={{ fontSize: 10, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {api.name}
                    </div>
                  </div>
                  {/* + button to add endpoint */}
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '1px 7px', marginTop: 0, fontSize: 12, flexShrink: 0 }}
                    title="Add endpoint"
                    onClick={e => { e.stopPropagation(); openAddEndpointWizard(api) }}
                  >
                    +
                  </button>
                </div>

                {/* Endpoint child rows */}
                {api.endpoints.map(ep => {
                  const isEpSelected = selectedEndpointId === ep.id && selectedApiId === api.id && !showWizard
                  const status = endpointStatus(ep, api)
                  const resolvedFn = resolveFlow(ep, api)
                  return (
                    <div
                      key={ep.id}
                      onClick={() => {
                        setSelectedApiId(api.id)
                        setSelectedEndpointId(ep.id)
                        setShowWizard(false)
                      }}
                      style={{
                        padding: '5px 9px 5px 20px',
                        borderRadius: 5,
                        cursor: 'pointer',
                        marginTop: 2,
                        background: isEpSelected ? 'rgba(87,181,255,0.08)' : 'transparent',
                        border: isEpSelected ? '1px solid rgba(87,181,255,0.4)' : '1px solid transparent',
                        display: 'flex', alignItems: 'center', gap: 6,
                      }}
                    >
                      {/* Status dot */}
                      <span style={{
                        width: 7, height: 7, borderRadius: '50%', flexShrink: 0,
                        background: status === 'ready' ? '#22c55e' : status === 'empty' ? '#f59e0b' : '#ef4444',
                        boxShadow: status === 'ready' ? '0 0 4px #22c55e66' : undefined,
                      }} title={status === 'ready' ? 'Ready' : status === 'empty' ? 'Flow has no steps' : 'Flow not found'} />

                      <MethodBadge method={ep.method} />

                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{
                          fontFamily: 'monospace', fontSize: 11,
                          color: isEpSelected ? 'var(--accent)' : 'var(--text)',
                          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                        }}>
                          {ep.subPath}
                        </div>
                        <div style={{
                          fontSize: 10, color: 'var(--muted)',
                          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                        }}>
                          {ep.flowName ? `★ ${ep.flowName}` : resolvedFn || '(no flow)'}
                        </div>
                      </div>
                    </div>
                  )
                })}
              </div>
            )
          })}
        </div>

        {/* Deploy shortcut at bottom */}
        {totalEndpoints > 0 && (
          <div style={{ padding: '10px 12px', borderTop: '1px solid var(--border)' }}>
            <button
              className="btn"
              style={{ width: '100%', fontSize: 12 }}
              onClick={onNavigateToDeploy}
            >
              → Publish Now ({totalEndpoints} endpoint{totalEndpoints !== 1 ? 's' : ''})
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
            wizardApiId={wizardApiId}
            wBasePaths={wBasePaths}
            wApiName={wApiName}
            wDefaultFlow={wDefaultFlow}
            wFlowMode={wFlowMode}
            wEndpoints={wEndpoints}
            wizardErr={wizardErr}
            setWBasePaths={setWBasePaths}
            onPrimaryBasePathChange={p => { if (!wizardApiId) setWApiName(suggestName(p)) }}
            setWApiName={setWApiName}
            setWDefaultFlow={setWDefaultFlow}
            setWFlowMode={setWFlowMode}
            setWEndpoints={setWEndpoints}
            onStep1Next={wizardStep1Next}
            onStep2Next={wizardStep2Next}
            onBack={() => setWizardStep(s => (s > 1 ? (s - 1) as 1 | 2 | 3 : s))}
            onConfirm={wizardConfirm}
            onCancel={cancelWizard}
          />
        ) : selectedEndpoint !== null && selectedApi !== null ? (
          <EndpointDetailPanel
            api={selectedApi}
            endpoint={selectedEndpoint}
            flows={flows}
            onRemove={() => handleRemoveEndpoint(selectedApi.id, selectedEndpoint.id)}
            onSetFlow={(flowName) => handleSetEndpointFlow(selectedApi.id, selectedEndpoint.id, flowName)}
            onClearOverride={() => handleSetEndpointFlow(selectedApi.id, selectedEndpoint.id, undefined)}
            onNavigateToDesigner={onNavigateToDesigner}
            onNavigateToDeploy={onNavigateToDeploy}
          />
        ) : selectedApi !== null ? (
          <ApiDetailPanel
            api={selectedApi}
            flows={flows}
            onRemove={() => handleRemoveApi(selectedApi.id)}
            onUpdateDefaultFlow={fn => handleUpdateApiDefaultFlow(selectedApi.id, fn)}
            onSelectEndpoint={epId => setSelectedEndpointId(epId)}
            onAddEndpoint={() => openAddEndpointWizard(selectedApi)}
            onNavigateToDesigner={onNavigateToDesigner}
            onRemoveAlias={i => handleRemoveAlias(selectedApi.id, i)}
          />
        ) : (
          <EmptyRight onNewApi={openNewWizard} onNavigateToDesigner={onNavigateToDesigner} />
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
        <p style={{ fontSize: 14, color: 'var(--text)', marginBottom: 6 }}>Register your first API</p>
        <p style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 20, lineHeight: 1.6 }}>
          Group endpoints under a base path and connect them to flows.<br />
          Each endpoint routes incoming requests to a flow that handles<br />
          authentication, upstream calls, and responses.
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

// ── API Detail Panel ─────────────────────────────────────────────────────────

interface ApiDetailProps {
  api: ApiDef
  flows: SavedFlow[]
  onRemove: () => void
  onUpdateDefaultFlow: (name: string) => void
  onSelectEndpoint: (epId: string) => void
  onAddEndpoint: () => void
  onNavigateToDesigner: () => void
  onRemoveAlias: (index: number) => void
}

function ApiDetailPanel({
  api, flows, onRemove, onUpdateDefaultFlow, onSelectEndpoint, onAddEndpoint, onNavigateToDesigner, onRemoveAlias,
}: ApiDetailProps) {
  const [confirmRemove, setConfirmRemove] = useState(false)
  const flowNames = flows.map(f => f.name)

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
            <div style={{ fontFamily: 'monospace', fontSize: 18, fontWeight: 700, color: 'var(--accent)', marginBottom: 3 }}>
              {api.basePath}
            </div>
            {(api.aliasPaths ?? []).length > 0 && (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 4 }}>
                {(api.aliasPaths ?? []).map((alias, i) => (
                  <span key={i} style={{
                    display: 'inline-flex', alignItems: 'center', gap: 4,
                    padding: '2px 8px', borderRadius: 10, fontSize: 11,
                    background: 'rgba(87,181,255,0.08)', border: '1px solid rgba(87,181,255,0.2)',
                    fontFamily: 'monospace', color: 'var(--muted)',
                  }}>
                    {alias}
                    <button
                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', padding: 0, fontSize: 12, lineHeight: 1 }}
                      onClick={() => onRemoveAlias(i)}
                    >×</button>
                  </span>
                ))}
              </div>
            )}
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{api.name}</div>
          </div>
          <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
            {confirmRemove ? (
              <>
                <span style={{ fontSize: 12, color: '#ef4444', alignSelf: 'center' }}>Remove API?</span>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 12 }}
                  onClick={() => setConfirmRemove(false)}
                >Cancel</button>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 12, background: '#ef4444' }}
                  onClick={onRemove}
                >Confirm</button>
              </>
            ) : (
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12 }}
                onClick={() => setConfirmRemove(true)}
              >Remove API</button>
            )}
          </div>
        </div>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>
        {/* Default flow */}
        <Section label="Default Flow">
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>Default:</span>
            <div style={{ flex: 1, maxWidth: 360 }}>
              <FlowSearchSelect
                flows={flows}
                value={api.defaultFlow}
                onChange={onUpdateDefaultFlow}
                placeholder="search or select a flow…"
              />
            </div>
          </div>
          {api.defaultFlow && !flowNames.includes(api.defaultFlow) && (
            <div style={{ fontSize: 11, color: '#f59e0b', marginTop: 6 }}>
              Flow "{api.defaultFlow}" not found in designer — build it first.
            </div>
          )}
        </Section>

        {/* Endpoints table */}
        <Section label="Endpoints" style={{ marginTop: 20 }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
            <thead>
              <tr style={{ color: 'var(--muted)', fontSize: 10, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Method</th>
                <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Sub-path</th>
                <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Flow</th>
                <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Override?</th>
              </tr>
            </thead>
            <tbody>
              {api.endpoints.map(ep => {
                const resolvedFn = resolveFlow(ep, api)
                return (
                  <tr
                    key={ep.id}
                    onClick={() => onSelectEndpoint(ep.id)}
                    style={{ cursor: 'pointer', borderTop: '1px solid var(--border)' }}
                    onMouseEnter={e => (e.currentTarget.style.background = 'rgba(87,181,255,0.05)')}
                    onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
                  >
                    <td style={{ padding: '7px 8px 7px 0' }}><MethodBadge method={ep.method} /></td>
                    <td style={{ padding: '7px 8px 7px 0', fontFamily: 'monospace', color: 'var(--text)' }}>
                      {ep.subPath}
                    </td>
                    <td style={{ padding: '7px 8px 7px 0', color: 'var(--muted)', maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {resolvedFn || <span style={{ color: '#ef4444' }}>(none)</span>}
                    </td>
                    <td style={{ padding: '7px 0 7px 0', color: ep.flowName ? '#fbbf24' : 'var(--muted)' }}>
                      {ep.flowName ? '★ yes' : '—'}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
          <button
            className="btn muted"
            style={{ width: 'auto', padding: '4px 14px', marginTop: 10, fontSize: 12 }}
            onClick={onAddEndpoint}
          >
            + Add Endpoint
          </button>
        </Section>

        {/* Navigate to designer */}
        <div style={{ marginTop: 20 }}>
          <button
            className="btn muted"
            style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
            onClick={onNavigateToDesigner}
          >
            → Open Flow Designer
          </button>
        </div>
      </div>
    </div>
  )
}

// ── Endpoint Detail Panel ─────────────────────────────────────────────────────

interface EndpointDetailProps {
  api: ApiDef
  endpoint: EndpointDef
  flows: SavedFlow[]
  onRemove: () => void
  onSetFlow: (flowName: string) => void
  onClearOverride: () => void
  onNavigateToDesigner: () => void
  onNavigateToDeploy: () => void
}

function EndpointDetailPanel({
  api, endpoint, flows, onRemove, onSetFlow, onClearOverride,
  onNavigateToDesigner, onNavigateToDeploy,
}: EndpointDetailProps) {
  const [showOverridePicker, setShowOverridePicker] = useState(false)
  const [confirmRemove,      setConfirmRemove]      = useState(false)

  const resolvedFn   = resolveFlow(endpoint, api)
  const resolvedFlow = flows.find(f => f.name === resolvedFn) ?? null
  const hasSteps     = !!(resolvedFlow && resolvedFlow.steps.length > 0)
  const fp           = fullPath(api.basePath, endpoint.subPath)

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
              <MethodBadge method={endpoint.method} large />
              <span style={{ fontFamily: 'monospace', fontSize: 16, fontWeight: 600 }}>{fp}</span>
            </div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>
              Part of: <strong style={{ color: 'var(--text)' }}>{api.name}</strong>
              {'  '}
              Basepath: <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>{api.basePath}</span>
            </div>
          </div>
          <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
            {confirmRemove ? (
              <>
                <span style={{ fontSize: 12, color: '#ef4444', alignSelf: 'center' }}>Remove?</span>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 12 }}
                  onClick={() => setConfirmRemove(false)}
                >Cancel</button>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 12, background: '#ef4444' }}
                  onClick={onRemove}
                >Confirm</button>
              </>
            ) : (
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12 }}
                onClick={() => setConfirmRemove(true)}
              >Remove Endpoint</button>
            )}
          </div>
        </div>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>

        {/* Flow row */}
        <Section label="Flow">
          {endpoint.flowName ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              <div>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>Override: </span>
                <span style={{ fontFamily: 'monospace', color: '#fbbf24', fontSize: 12 }}>{endpoint.flowName}</span>
              </div>
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '3px 12px', marginTop: 0, fontSize: 11 }}
                onClick={onClearOverride}
              >
                Clear override (use {api.defaultFlow || 'default'})
              </button>
            </div>
          ) : (
            <div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', marginBottom: 8 }}>
                <div>
                  <span style={{ fontSize: 11, color: 'var(--muted)' }}>Inherited: </span>
                  <span style={{ fontFamily: 'monospace', color: 'var(--accent)', fontSize: 12 }}>
                    {api.defaultFlow || <span style={{ color: '#ef4444' }}>(no default flow)</span>}
                  </span>
                </div>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '3px 12px', marginTop: 0, fontSize: 11 }}
                  onClick={() => setShowOverridePicker(p => !p)}
                >
                  {showOverridePicker ? 'Cancel' : 'Set override'}
                </button>
              </div>
              {showOverridePicker && (
                <div style={{ maxWidth: 360 }}>
                  <FlowSearchSelect
                    flows={flows}
                    value={''}
                    onChange={fn => { onSetFlow(fn); setShowOverridePicker(false) }}
                    placeholder="search or select override flow…"
                  />
                </div>
              )}
            </div>
          )}
        </Section>

        {/* Flow states */}
        {!resolvedFlow && (
          <div style={{
            marginTop: 16, padding: '14px 16px',
            borderRadius: 8,
            background: 'rgba(239,68,68,0.06)',
            border: '1px solid rgba(239,68,68,0.2)',
          }}>
            <p style={{ fontSize: 13, color: 'var(--muted)', marginBottom: 10 }}>
              Flow "{resolvedFn}" not found — build it in the designer.
            </p>
            <button
              className="btn"
              style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
              onClick={onNavigateToDesigner}
            >
              + Create flow in Designer
            </button>
          </div>
        )}

        {resolvedFlow && !hasSteps && (
          <div style={{
            marginTop: 16, padding: '12px 16px',
            borderRadius: 8,
            background: 'rgba(251,191,36,0.06)',
            border: '1px solid rgba(251,191,36,0.25)',
          }}>
            <p style={{ fontSize: 13, color: '#fbbf24', marginBottom: 10 }}>
              Flow "{resolvedFlow.name}" has no steps yet.
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

        {/* Pipeline preview */}
        {resolvedFlow && hasSteps && (
          <Section label="Pipeline Preview" style={{ marginTop: 20 }}>
            <MiniPipeline method={endpoint.method} path={fp} steps={resolvedFlow.steps} />
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

        {/* API Interface */}
        {resolvedFlow && hasSteps && (() => {
          const { inputs, outputs } = deriveApiInterface(resolvedFlow.steps)
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
        {resolvedFlow && hasSteps && (() => {
          const { inputs } = deriveApiInterface(resolvedFlow.steps)
          const headerInputs = inputs.filter(b => b.source === 'header')
          const bodyInputs   = inputs.filter(b => b.source === 'body')
          const queryInputs  = inputs.filter(b => b.source === 'query')

          const method   = endpoint.method ?? 'POST'
          const curlPath = fp.replace(/\{(\w+)\}/g, '<$1>')
          const bodyObj  = bodyInputs.length > 0
            ? JSON.stringify(Object.fromEntries(bodyInputs.map(b => [b.field, `<${b.field}>`])), null, 2)
            : (method !== 'GET' ? '{}' : null)
          const queryStr = queryInputs.length > 0
            ? '?' + queryInputs.map(b => `${b.field}=<${b.field}>`).join('&')
            : ''

          const curl = [
            `curl -X ${method} http://localhost:8080${curlPath}${queryStr}`,
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

      </div>
    </div>
  )
}

// ── Mini pipeline preview ────────────────────────────────────────────────────

function MiniPipeline({ method, path, steps }: { method: string; path: string; steps: FlowStep[] }) {
  type RenderedItem =
    | { kind: 'zone-header'; zone: Zone }
    | { kind: 'step'; step: FlowStep; zone: Zone }
    | { kind: 'boundary' }

  const items: RenderedItem[] = []
  let lastZone: Zone | null   = null
  let hasResponseZone         = false

  steps.forEach(step => {
    const zone = stepZone(step.action)
    if (zone !== lastZone) {
      items.push({ kind: 'zone-header', zone })
      lastZone = zone
    }
    items.push({ kind: 'step', step, zone })
    if (zone === 'response' && !hasResponseZone) hasResponseZone = true
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
        <MethodBadge method={method} />
        <span style={{ color: 'var(--text)' }}>{path}</span>
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

        const { step, zone } = item
        const meta = ZONE_META[zone]
        const inputRef  = (step.key_identifier || step.source) as string | undefined
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
            <div style={{
              width: 3, alignSelf: 'stretch', flexShrink: 0,
              background: meta.color,
              marginRight: 10,
              opacity: 0.6,
            }} />
            <span style={{ color: meta.color, marginRight: 6, fontSize: 10, flexShrink: 0 }}>•</span>
            <span style={{ color: 'var(--text)', fontWeight: 500, flexShrink: 0 }}>
              {step.action}
            </span>
            {inputRef && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11 }}>
                ← <span style={{ color: '#57b5ff' }}>{inputRef}</span>
              </span>
            )}
            {outputRef && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11 }}>
                → <span style={{ color: '#34d399' }}>{outputRef}</span>
              </span>
            )}
            {conditionText && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11, fontStyle: 'italic' }}>
                {conditionText}
              </span>
            )}
          </div>
        )
      })}

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
  wizardStep: 1 | 2 | 3
  wizardApiId: string | null
  wBasePaths: string[]
  wApiName: string
  wDefaultFlow: string
  wFlowMode: 'create' | 'existing'
  wEndpoints: Array<{ id: string; subPath: string; method: string; flowName?: string; overrideFlow: boolean }>
  wizardErr: string
  setWBasePaths: (v: string[]) => void
  onPrimaryBasePathChange: (v: string) => void
  setWApiName: (v: string) => void
  setWDefaultFlow: (v: string) => void
  setWFlowMode: (v: 'create' | 'existing') => void
  setWEndpoints: (v: Array<{ id: string; subPath: string; method: string; flowName?: string; overrideFlow: boolean }>) => void
  onStep1Next: () => void
  onStep2Next: () => void
  onBack: () => void
  onConfirm: () => void
  onCancel: () => void
}

function WizardPanel({
  apis, flows,
  wizardStep, wizardApiId,
  wBasePaths, wApiName, wDefaultFlow, wFlowMode, wEndpoints, wizardErr,
  setWBasePaths, onPrimaryBasePathChange, setWApiName, setWDefaultFlow, setWFlowMode, setWEndpoints,
  onStep1Next, onStep2Next, onBack, onConfirm, onCancel,
}: WizardProps) {
  const isAddEndpointMode = wizardApiId !== null

  function addEndpointRow() {
    setWEndpoints([
      ...wEndpoints,
      { id: crypto.randomUUID(), subPath: '/', method: 'GET', overrideFlow: false },
    ])
  }

  function updateEndpoint(id: string, patch: Partial<typeof wEndpoints[0]>) {
    setWEndpoints(wEndpoints.map(e => e.id === id ? { ...e, ...patch } : e))
  }

  function removeEndpointRow(id: string) {
    if (wEndpoints.length <= 1) return
    setWEndpoints(wEndpoints.filter(e => e.id !== id))
  }

  const stepCount = isAddEndpointMode ? 2 : 3
  const stepLabel = isAddEndpointMode
    ? (wizardStep === 2 ? 'Step 1 of 2 — Endpoints' : 'Step 2 of 2 — Review')
    : (wizardStep === 1 ? 'Step 1 of 3 — API Identity'
      : wizardStep === 2 ? 'Step 2 of 3 — Endpoints'
      : 'Step 3 of 3 — Review')

  const displayStep = isAddEndpointMode ? wizardStep - 1 : wizardStep

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '40px 20px' }}>
      <div style={{
        width: '100%', maxWidth: 560,
        background: 'var(--panel)',
        borderRadius: 10,
        border: '1px solid var(--border)',
        overflow: 'hidden',
      }}>
        {/* Header */}
        <div style={{
          padding: '16px 20px',
          borderBottom: '1px solid var(--border)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <div>
            <div style={{ fontSize: 14, fontWeight: 600, marginBottom: 4 }}>
              {stepLabel}
            </div>
            <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              {Array.from({ length: stepCount }, (_, n) => (
                <div key={n} style={{
                  width: 20, height: 4, borderRadius: 2,
                  background: n < displayStep ? 'var(--accent)' : 'rgba(255,255,255,0.1)',
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
          {/* Step 1: API Identity */}
          {wizardStep === 1 && (
            <>
              <div style={{ marginBottom: 14 }}>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 5 }}>
                  Basepaths
                  <span style={{ fontSize: 10, marginLeft: 8, opacity: 0.6 }}>first = primary · extras = aliases (same API, same endpoints)</span>
                </div>
                {wBasePaths.map((bp, i) => (
                  <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
                    <input
                      className="input"
                      placeholder={i === 0 ? '/api/v1/users' : '/v1/users  (alias)'}
                      value={bp}
                      onChange={e => {
                        const updated = [...wBasePaths]
                        updated[i] = e.target.value
                        setWBasePaths(updated)
                        if (i === 0) onPrimaryBasePathChange(e.target.value)
                      }}
                      onKeyDown={e => e.key === 'Enter' && onStep1Next()}
                      style={{ flex: 1, marginTop: 0, fontFamily: 'monospace' }}
                      autoFocus={i === 0}
                    />
                    {wBasePaths.length > 1 && (
                      <button
                        className="btn muted"
                        style={{ width: 'auto', padding: '0 10px', marginTop: 0, fontSize: 16, flexShrink: 0 }}
                        onClick={() => setWBasePaths(wBasePaths.filter((_, j) => j !== i))}
                      >×</button>
                    )}
                  </div>
                ))}
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '3px 12px', marginTop: 2, fontSize: 11 }}
                  onClick={() => setWBasePaths([...wBasePaths, ''])}
                >+ Add basepath</button>
              </div>

              <div style={{ marginBottom: 14 }}>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 5 }}>
                  API Name <span style={{ fontSize: 10, opacity: 0.6 }}>(auto-suggested)</span>
                </div>
                <input
                  className="input"
                  placeholder="users_api"
                  value={wApiName}
                  onChange={e => setWApiName(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && onStep1Next()}
                  style={{ width: '100%', marginTop: 0 }}
                />
                {wApiName && apis.some(a => a.name === wApiName) && (
                  <div style={{ fontSize: 11, color: '#f97316', marginTop: 4 }}>
                    Name "{wApiName}" already exists
                  </div>
                )}
              </div>

              <div style={{ marginBottom: 16 }}>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 8 }}>Default Flow</div>

                <label style={{
                  display: 'flex', gap: 10, alignItems: 'flex-start',
                  padding: '11px 14px', borderRadius: 8, cursor: 'pointer',
                  marginBottom: 8,
                  background: wFlowMode === 'create' ? 'rgba(87,181,255,0.08)' : 'var(--step-bg)',
                  border: wFlowMode === 'create' ? '1px solid var(--accent)' : '1px solid transparent',
                }}>
                  <input type="radio" checked={wFlowMode === 'create'} onChange={() => setWFlowMode('create')} style={{ marginTop: 2 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, marginBottom: 4 }}>Create new flow</div>
                    {wFlowMode === 'create' && (
                      <input
                        className="input"
                        placeholder="my_api_flow"
                        value={wDefaultFlow}
                        onChange={e => setWDefaultFlow(e.target.value)}
                        style={{ width: '100%', marginTop: 0 }}
                        onClick={e => e.stopPropagation()}
                      />
                    )}
                    {wFlowMode !== 'create' && (
                      <div style={{ fontSize: 11, color: 'var(--muted)' }}>Opens Designer to build from scratch.</div>
                    )}
                  </div>
                </label>

                <label style={{
                  display: 'flex', gap: 10, alignItems: 'flex-start',
                  padding: '11px 14px', borderRadius: 8, cursor: 'pointer',
                  background: wFlowMode === 'existing' ? 'rgba(87,181,255,0.08)' : 'var(--step-bg)',
                  border: wFlowMode === 'existing' ? '1px solid var(--accent)' : '1px solid transparent',
                }}>
                  <input type="radio" checked={wFlowMode === 'existing'} onChange={() => setWFlowMode('existing')} style={{ marginTop: 2 }} />
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, marginBottom: 4 }}>Use existing flow</div>
                    {wFlowMode === 'existing' && (
                      <div onClick={e => e.stopPropagation()}>
                        <FlowSearchSelect
                          flows={flows}
                          value={wDefaultFlow}
                          onChange={setWDefaultFlow}
                          placeholder="search or select a flow…"
                        />
                      </div>
                    )}
                    {wFlowMode !== 'existing' && (
                      <div style={{ fontSize: 11, color: 'var(--muted)' }}>Route to an already-built flow.</div>
                    )}
                  </div>
                </label>
              </div>

              {wizardErr && (
                <div style={{ fontSize: 12, color: '#ef4444', marginBottom: 12 }}>{wizardErr}</div>
              )}

              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '6px 20px', marginTop: 0 }}
                  onClick={onStep1Next}
                >
                  Next →
                </button>
              </div>
            </>
          )}

          {/* Step 2: Endpoints */}
          {wizardStep === 2 && (
            <>
              {/* Base path read-only prefix */}
              <div style={{
                padding: '8px 12px', marginBottom: 16, borderRadius: 6,
                background: 'rgba(87,181,255,0.06)', border: '1px solid rgba(87,181,255,0.2)',
                fontSize: 12, color: 'var(--muted)',
              }}>
                Base path: <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>{wBasePaths[0]}</span>{wBasePaths.length > 1 && <span style={{ color: 'var(--muted)', fontSize: 10, marginLeft: 4 }}>+{wBasePaths.length - 1} alias{wBasePaths.length > 2 ? 'es' : ''}</span>}
                {!isAddEndpointMode && wDefaultFlow && (
                  <span> · Default flow: <span style={{ color: 'var(--text)' }}>{wDefaultFlow}</span></span>
                )}
              </div>

              {wEndpoints.map((ep, idx) => (
                <div key={ep.id} style={{
                  padding: '12px 14px', marginBottom: 10,
                  borderRadius: 8, border: '1px solid var(--border)',
                  background: 'var(--step-bg)',
                }}>
                  <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end', marginBottom: 8 }}>
                    {/* Method */}
                    <div style={{ flexShrink: 0 }}>
                      <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 4 }}>Method</div>
                      <select
                        className="input"
                        value={ep.method}
                        onChange={e => updateEndpoint(ep.id, { method: e.target.value })}
                        style={{ width: 90, marginTop: 0 }}
                      >
                        {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => <option key={m}>{m}</option>)}
                      </select>
                    </div>
                    {/* Sub-path */}
                    <div style={{ flex: 1 }}>
                      <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 4 }}>
                        Sub-path
                        <span style={{ color: 'var(--muted)', fontSize: 10, marginLeft: 8 }}>
                          preview: <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>
                            {fullPath(wBasePaths[0], ep.subPath)}
                          </span>
                        </span>
                      </div>
                      <input
                        className="input"
                        placeholder="/"
                        value={ep.subPath}
                        onChange={e => updateEndpoint(ep.id, { subPath: e.target.value })}
                        style={{ width: '100%', marginTop: 0 }}
                      />
                    </div>
                    {wEndpoints.length > 1 && (
                      <button
                        className="btn muted"
                        style={{ width: 'auto', padding: '4px 8px', marginTop: 0, fontSize: 12, flexShrink: 0 }}
                        onClick={() => removeEndpointRow(ep.id)}
                        title="Remove row"
                      >✕</button>
                    )}
                  </div>

                  {/* Optional flow override */}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <label style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 12, color: 'var(--muted)' }}>
                      <input
                        type="checkbox"
                        checked={ep.overrideFlow}
                        onChange={e => updateEndpoint(ep.id, { overrideFlow: e.target.checked, flowName: e.target.checked ? ep.flowName : undefined })}
                      />
                      Override flow for this endpoint
                    </label>
                  </div>
                  {ep.overrideFlow && (
                    <div style={{ marginTop: 8 }}>
                      <FlowSearchSelect
                        flows={flows}
                        value={ep.flowName ?? ''}
                        onChange={fn => updateEndpoint(ep.id, { flowName: fn })}
                        placeholder="search or select override flow…"
                      />
                    </div>
                  )}

                  {/* Resolved path hint */}
                  {idx === wEndpoints.length - 1 && (
                    <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 6 }}>
                      Full path: <span style={{ fontFamily: 'monospace' }}>{fullPath(wBasePaths[0], ep.subPath)}</span>
                    </div>
                  )}
                </div>
              ))}

              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12, marginBottom: 16 }}
                onClick={addEndpointRow}
              >
                + Add endpoint
              </button>

              {wizardErr && (
                <div style={{ fontSize: 12, color: '#ef4444', marginBottom: 12 }}>{wizardErr}</div>
              )}

              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                {!isAddEndpointMode && (
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '6px 16px', marginTop: 0 }}
                    onClick={onBack}
                  >
                    ← Back
                  </button>
                )}
                {isAddEndpointMode && <div />}
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '6px 20px', marginTop: 0 }}
                  onClick={onStep2Next}
                >
                  Next →
                </button>
              </div>
            </>
          )}

          {/* Step 3: Review */}
          {wizardStep === 3 && (
            <>
              <div style={{ marginBottom: 16 }}>
                <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
                  API: <span style={{ color: 'var(--text)', fontWeight: 600 }}>{wApiName}</span>
                  {'  '}
                  Base path: <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>{wBasePaths[0]}</span>{wBasePaths.length > 1 && <span style={{ color: 'var(--muted)', fontSize: 10, marginLeft: 4 }}>+{wBasePaths.length - 1} alias{wBasePaths.length > 2 ? 'es' : ''}</span>}
                  {'  '}
                  Default flow: <span style={{ color: 'var(--text)' }}>{wDefaultFlow}</span>
                  {wFlowMode === 'create' && <span style={{ color: '#fbbf24', marginLeft: 4 }}>(will be created)</span>}
                </div>

                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                  <thead>
                    <tr style={{ color: 'var(--muted)', fontSize: 10, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                      <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Method</th>
                      <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Full Path</th>
                      <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Flow</th>
                    </tr>
                  </thead>
                  <tbody>
                    {wEndpoints.map(ep => (
                      <tr key={ep.id} style={{ borderTop: '1px solid var(--border)' }}>
                        <td style={{ padding: '7px 8px 7px 0' }}><MethodBadge method={ep.method} /></td>
                        <td style={{ padding: '7px 8px 7px 0', fontFamily: 'monospace', color: 'var(--text)' }}>
                          {fullPath(wBasePaths[0], ep.subPath)}
                        </td>
                        <td style={{ padding: '7px 0 7px 0', color: ep.overrideFlow && ep.flowName ? '#fbbf24' : 'var(--muted)' }}>
                          {ep.overrideFlow && ep.flowName
                            ? `★ ${ep.flowName}`
                            : wDefaultFlow || '(none)'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

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
                  onClick={onConfirm}
                >
                  Confirm & Save
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
