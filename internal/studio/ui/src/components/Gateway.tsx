import { useCallback, useEffect, useState } from 'react'
import { fetchGatewayApis } from '../api'
import type { FlowStep, GatewayState } from '../types'

interface GatewayProps {
  onLoadFlow?: (name: string, steps: FlowStep[]) => void
}

export default function Gateway({ onLoadFlow }: GatewayProps) {
  const [state, setState] = useState<GatewayState | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [expandedFlow, setExpandedFlow] = useState<string | null>(null)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchGatewayApis()
      .then(d => { setState(d); setLoading(false) })
      .catch(e => { setError(String(e)); setLoading(false) })
  }, [])

  useEffect(() => { load() }, [load])

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
          {state && state.flows.map(f => (
            <div key={f.name} className="block">
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <strong>{f.name}</strong>
                <div style={{ display: 'flex', gap: '6px' }}>
                  {onLoadFlow && (
                    <button
                      className="btn"
                      title="Load this flow into the Flow Designer for editing"
                      onClick={() => onLoadFlow(f.name, f.instructions)}
                    >
                      Load
                    </button>
                  )}
                  <button
                    className="btn muted"
                    onClick={() => setExpandedFlow(expandedFlow === f.name ? null : f.name)}
                  >
                    {expandedFlow === f.name ? 'Hide' : 'Steps'}
                  </button>
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
          ))}
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
          {state && state.apis.map(a => (
            <div key={a.name} className="block">
              <strong>{a.name}</strong>
              <div className="sub">{a.path}</div>
              <div className="sub">flow: {a.flow_name}</div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
