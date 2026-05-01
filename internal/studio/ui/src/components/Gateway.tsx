import { useCallback, useEffect, useState } from 'react'
import { fetchGatewayApis, deleteFlow } from '../api'
import type { FlowStep, GatewayFlow, GatewayState } from '../types'

interface GatewayProps {
  onLoadFlow?: (name: string, steps: FlowStep[], allGatewayFlows: GatewayFlow[]) => void
  onLoadApi?: (api: { name: string; path: string; method: string; flow_name: string }) => void
}

const METHOD_COLOR: Record<string, string> = {
  GET:    '#22c55e',
  POST:   '#3b82f6',
  PUT:    '#f97316',
  PATCH:  '#eab308',
  DELETE: '#ef4444',
}

function MethodBadge({ method }: { method: string }) {
  const m = method || 'ANY'
  return (
    <span style={{
      fontSize: 10, fontWeight: 700,
      padding: '1px 6px', borderRadius: 4,
      color: '#fff',
      background: METHOD_COLOR[m] ?? '#64748b',
      minWidth: 40, textAlign: 'center',
      display: 'inline-block',
      marginRight: 8,
      letterSpacing: 0.4,
    }}>
      {m}
    </span>
  )
}

export default function Gateway({ onLoadFlow, onLoadApi }: GatewayProps) {
  const [state, setState] = useState<GatewayState | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [expandedFlow, setExpandedFlow] = useState<string | null>(null)
  const [expandedApi, setExpandedApi] = useState<string | null>(null)
  const [expandedSubFlows, setExpandedSubFlows] = useState<string | null>(null)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchGatewayApis()
      .then(d => { setState(d); setLoading(false) })
      .catch(e => { setError(String(e)); setLoading(false) })
  }, [])

  useEffect(() => { load() }, [load])

  const findFlow = (name: string) => state?.flows.find(f => f.name === name)

  const scrollToFlow = (name: string) => {
    setExpandedFlow(name)
    // Small delay to allow expansion before scrolling
    setTimeout(() => {
      const el = document.getElementById(`flow-${name}`)
      if (el) el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }, 50)
  }

  return (
    <div className="two-col">
      {/* Flows */}
      <div className="panel">
        <div className="panel-header">
          Live Flows
          <button className="btn muted" style={{ float: 'right', marginTop: '-2px' }} onClick={load}>
            Refresh
          </button>
        </div>
        <div className="panel-body">
          {loading && <span className="hint">Loading…</span>}
          {error && <span className="status-err">{error}</span>}
          {!loading && !error && state && state.flows.length === 0 && (
            <span className="hint">No flows deployed yet.</span>
          )}
          {state && (() => {
            // Separate parent flows (referenced by at least one API) from internal sub-flows.
            const parentNames = new Set(state.apis.map(a => a.flow_name).filter(Boolean))
            const parentFlows = state.flows.filter(f => parentNames.has(f.name))
            const subFlows = state.flows.filter(f => !parentNames.has(f.name))
            // Group sub-flows by their parent (heuristic: sub-flow name starts with parent name + '-').
            const subFlowsByParent: Record<string, typeof state.flows> = {}
            for (const sf of subFlows) {
              const parent = parentFlows.find(p => sf.name.startsWith(p.name + '-'))
              const key = parent ? parent.name : '__orphan__'
              subFlowsByParent[key] = [...(subFlowsByParent[key] ?? []), sf]
            }
            const renderFlow = (f: typeof state.flows[0], isSubFlow = false, onDelete?: () => void) => (
              <div key={f.name} id={`flow-${f.name}`} className="block" style={{
                borderLeft: expandedFlow === f.name ? '3px solid var(--accent)' : '3px solid transparent',
                marginLeft: isSubFlow ? 16 : 0,
                opacity: isSubFlow ? 0.85 : 1,
              }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <strong style={{ fontSize: isSubFlow ? 12 : undefined }}>{f.name}</strong>
                  <div style={{ display: 'flex', gap: '6px' }}>
                    {onLoadFlow && !isSubFlow && (
                      <button className="btn" title="Load this flow into the Flow Designer for editing"
                        onClick={() => onLoadFlow(f.name, f.instructions, state?.flows ?? [])}>Load</button>
                    )}
                    <button className="btn muted"
                      onClick={() => setExpandedFlow(expandedFlow === f.name ? null : f.name)}>
                      {expandedFlow === f.name ? 'Hide' : 'Steps'}
                    </button>
                    {onDelete && (
                      <button className="btn" style={{ color: '#ef4444', borderColor: '#ef4444' }}
                        title="Delete this orphaned flow"
                        onClick={onDelete}>Delete</button>
                    )}
                  </div>
                </div>
                <div className="sub">{f.instructions.length} step{f.instructions.length !== 1 ? 's' : ''}</div>
                {expandedFlow === f.name && (
                  <div style={{ marginTop: '8px' }}>
                    {f.instructions.map((step, i) => {
                      const params = Object.entries(step).filter(([k]) => k !== 'action')
                      return (
                        <div key={i} className="step" style={{ marginBottom: '4px' }}>
                          <strong>{i + 1}. {step.action}</strong>
                          {params.map(([k, v]) => (
                            <div key={k} className="sub" style={{ paddingLeft: '12px' }}>
                              {k}: <span style={{ opacity: 0.8 }}>{String(v)}</span>
                            </div>
                          ))}
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            )

            return (
              <>
                {parentFlows.map(f => (
                  <div key={f.name}>
                    {renderFlow(f)}
                    {subFlowsByParent[f.name] && (
                      <div>
                        <button type="button" className="btn muted" style={{ fontSize: 11, marginLeft: 16, marginBottom: 4 }}
                          onClick={() => setExpandedSubFlows(expandedSubFlows === f.name ? null : f.name)}>
                          {expandedSubFlows === f.name ? '▲' : '▼'} {subFlowsByParent[f.name].length} internal sub-flow{subFlowsByParent[f.name].length !== 1 ? 's' : ''}
                        </button>
                        {expandedSubFlows === f.name && subFlowsByParent[f.name].map(sf => renderFlow(sf, true))}
                      </div>
                    )}
                  </div>
                ))}
                {subFlowsByParent['__orphan__'] && (
                  <div style={{ marginTop: 8 }}>
                    <div className="sub" style={{ marginBottom: 4, color: 'var(--muted)' }}>Orphaned flows (not linked to any API):</div>
                    {subFlowsByParent['__orphan__'].map(sf => renderFlow(sf, true, () => {
                      if (confirm(`Delete orphaned flow "${sf.name}"?`)) {
                        deleteFlow(sf.name).then(load).catch(e => alert(String(e)))
                      }
                    }))}
                  </div>
                )}
              </>
            )
          })()}
          {state && (
            <div className="hint" style={{ marginTop: '8px' }}>
              Sync UUID: {state.sync_uuid || '(none)'}
            </div>
          )}
        </div>
      </div>

      {/* APIs */}
      <div className="panel">
        <div className="panel-header">Live APIs</div>
        <div className="panel-body">
          {loading && <span className="hint">Loading…</span>}
          {!loading && !error && state && state.apis.length === 0 && (
            <span className="hint">No APIs registered yet.</span>
          )}
          {state && state.apis.map(a => {
            const flow = findFlow(a.flow_name)
            return (
              <div key={a.name} className="block">
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <div>
                    <div style={{ display: 'flex', alignItems: 'center' }}>
                      <MethodBadge method={a.method} />
                      <strong>{a.name}</strong>
                    </div>
                    <div className="sub" style={{ marginTop: 4 }}>{a.path}</div>
                    <div className="sub" style={{ color: 'var(--accent)', fontSize: 11, fontWeight: 600 }}>
                      flow: {a.flow_name}
                    </div>
                  </div>
                  <div style={{ display: 'flex', gap: '6px' }}>
                    {onLoadApi && onLoadFlow && flow && (
                      <button
                        className="btn"
                        title="Load this API and its flow into the editor"
                        onClick={() => {
                          onLoadFlow(flow.name, flow.instructions, state?.flows ?? [])
                          onLoadApi({
                            name: a.name,
                            path: a.path,
                            method: a.method || 'ANY',
                            flow_name: a.flow_name
                          })
                        }}
                      >
                        Edit
                      </button>
                    )}
                    <button
                      className="btn muted"
                      disabled={!flow}
                      title={!flow ? "Associated flow not found" : "View flow logic"}
                      onClick={() => scrollToFlow(a.flow_name)}
                    >
                      View Flow
                    </button>
                    <button
                      className="btn muted"
                      disabled={!flow}
                      onClick={() => setExpandedApi(expandedApi === a.name ? null : a.name)}
                    >
                      {expandedApi === a.name ? 'Hide Steps' : 'Expand Steps'}
                    </button>
                  </div>
                </div>
                {expandedApi === a.name && flow && (
                  <div style={{
                    marginTop: '12px',
                    padding: '12px',
                    background: 'rgba(0,0,0,0.2)',
                    borderRadius: '8px',
                    border: '1px solid var(--border)'
                  }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '8px' }}>
                      <span className="hint" style={{ fontWeight: 600, color: 'var(--accent)' }}>
                        Instructions for flow: {flow.name}
                      </span>
                    </div>
                    {flow.instructions.map((step, i) => {
                      const params = Object.entries(step).filter(([k]) => k !== 'action')
                      return (
                        <div key={i} className="step" style={{
                          marginBottom: '6px',
                          background: 'var(--panel)',
                          padding: '6px 10px',
                          borderRadius: '4px'
                        }}>
                          <strong style={{ color: 'var(--accent)' }}>{i + 1}. {step.action}</strong>
                          {params.map(([k, v]) => (
                            <div key={k} className="sub" style={{ paddingLeft: '12px', fontSize: '11px' }}>
                              <span style={{ color: 'var(--muted)' }}>{k}:</span> <span>{String(v)}</span>
                            </div>
                          ))}
                        </div>
                      )
                    })}
                    <div className="hint" style={{ marginTop: '8px', fontSize: '10px', fontStyle: 'italic' }}>
                      To modify this logic: Click "Edit" above → Modify in Designer → Go to "Deploy" tab to push changes.
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
