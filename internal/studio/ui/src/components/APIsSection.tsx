import { useState } from 'react'
import { importOpenAPI } from '../api'
import type { ApiDef, SavedFlow } from '../types'

interface Props {
  flows: SavedFlow[]
  apis: ApiDef[]
  setApis: (apis: ApiDef[]) => void
  onCreateFlow: (name: string) => void
}

const METHOD_COLOR: Record<string, string> = {
  GET:    '#22c55e',
  POST:   '#3b82f6',
  PUT:    '#f97316',
  PATCH:  '#eab308',
  DELETE: '#ef4444',
}

export default function APIsSection({ flows, apis, setApis, onCreateFlow }: Props) {
  const [selectedFlow, setSelectedFlow] = useState<string>('')
  const [newFlowName, setNewFlowName]   = useState('')

  // Add endpoint form
  const [addName,   setAddName]   = useState('')
  const [addPath,   setAddPath]   = useState('')
  const [addMethod, setAddMethod] = useState('GET')

  // OpenAPI import
  const [spec,       setSpec]       = useState('')
  const [importMsg,  setImportMsg]  = useState('')
  const [importErr,  setImportErr]  = useState(false)
  const [importing,  setImporting]  = useState(false)

  const flowNames  = flows.map(f => f.name)
  const apisFor    = (name: string) => apis.filter(a => a.flow_name === name)
  const unassigned = apis.filter(a => !a.flow_name || !flowNames.includes(a.flow_name))
  const currentApis = apisFor(selectedFlow)

  // ── helpers ─────────────────────────────────────────────────────

  function handleCreateFlow() {
    const name = newFlowName.trim()
    if (!name || flowNames.includes(name)) return
    onCreateFlow(name)
    setSelectedFlow(name)
    setNewFlowName('')
  }

  function handleAddApi() {
    if (!addName.trim() || !addPath.trim() || !selectedFlow) return
    const entry: ApiDef = {
      name:      addName.trim(),
      path:      addPath.trim().startsWith('/') ? addPath.trim() : '/' + addPath.trim(),
      method:    addMethod,
      flow_name: selectedFlow,
    }
    setApis([...apis, entry])
    setAddName('')
    setAddPath('')
    setAddMethod('GET')
  }

  function handleRemove(globalIdx: number) {
    setApis(apis.filter((_, i) => i !== globalIdx))
  }

  function handleAssign(api: ApiDef) {
    if (!selectedFlow) return
    const i = apis.indexOf(api)
    const updated = [...apis]
    updated[i] = { ...api, flow_name: selectedFlow }
    setApis(updated)
  }

  async function handleImport() {
    if (!spec.trim() || !selectedFlow) return
    setImporting(true)
    setImportMsg('')
    setImportErr(false)
    try {
      const data    = await importOpenAPI(spec)
      const entries = data.apis.map(a => ({
        name: a.name, path: a.path, method: a.method, flow_name: selectedFlow,
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

  // ── render ───────────────────────────────────────────────────────

  return (
    <div style={{ display: 'flex', height: 'calc(100vh - 58px)', gap: 0, margin: -14 }}>

      {/* ── Left sidebar: flows ───────────────────────────────── */}
      <div style={{
        width: 260, flexShrink: 0,
        borderRight: '1px solid var(--border)',
        display: 'flex', flexDirection: 'column',
        background: 'var(--panel)',
      }}>
        <div className="panel-header" style={{ padding: '12px 16px' }}>Flows</div>

        {/* flow list */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '10px 10px 0' }}>
          {flows.length === 0 && (
            <p className="hint" style={{ padding: '4px 4px 8px' }}>
              No flows yet. Create one below or design one in Flow Designer.
            </p>
          )}

          {flows.map(f => {
            const count  = apisFor(f.name).length
            const active = f.name === selectedFlow
            return (
              <div
                key={f.name}
                onClick={() => setSelectedFlow(f.name)}
                style={{
                  padding: '8px 10px',
                  borderRadius: 8,
                  cursor: 'pointer',
                  marginBottom: 4,
                  background: active ? 'rgba(87,181,255,0.1)' : 'transparent',
                  border:     active ? '1px solid var(--accent)' : '1px solid transparent',
                  display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                }}
              >
                <span style={{ fontSize: 13, color: active ? 'var(--accent)' : 'var(--text)', wordBreak: 'break-all' }}>
                  {f.name}
                  {f.steps.length === 0 && (
                    <span style={{ fontSize: 10, color: 'var(--muted)', marginLeft: 6 }}>no steps</span>
                  )}
                </span>
                <span style={{
                  fontSize: 11, color: 'var(--muted)',
                  background: 'var(--step-bg)',
                  borderRadius: 10, padding: '1px 8px', marginLeft: 8, flexShrink: 0,
                }}>
                  {count}
                </span>
              </div>
            )
          })}

          {/* unassigned bucket */}
          {unassigned.length > 0 && (
            <div style={{ marginTop: 16 }}>
              <div style={{
                fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase',
                letterSpacing: 1, marginBottom: 6, padding: '0 2px',
              }}>
                Unassigned ({unassigned.length})
              </div>
              {unassigned.map((a, i) => (
                <div key={i} style={{
                  fontSize: 12, padding: '5px 8px', borderRadius: 6,
                  background: 'var(--step-bg)', marginBottom: 4,
                  display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 6,
                }}>
                  <span style={{ color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    <MethodBadge method={a.method} /> {a.path}
                  </span>
                  {selectedFlow && (
                    <button
                      className="btn muted"
                      style={{ width: 'auto', padding: '2px 8px', marginTop: 0, fontSize: 11, flexShrink: 0 }}
                      title={`Move to ${selectedFlow}`}
                      onClick={() => handleAssign(a)}
                    >
                      → here
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>

        {/* new flow input */}
        <div style={{ padding: 12, borderTop: '1px solid var(--border)' }}>
          <input
            className="input"
            placeholder="new flow name…"
            value={newFlowName}
            onChange={e => setNewFlowName(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && handleCreateFlow()}
            style={{ fontSize: 13 }}
          />
          <button className="btn mt8" style={{ fontSize: 13 }} onClick={handleCreateFlow}>
            + Add Flow
          </button>
        </div>
      </div>

      {/* ── Right main: APIs for selected flow ───────────────── */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflowY: 'auto', background: 'var(--bg)' }}>

        {/* Top: Global Add Endpoint (Always visible) */}
        <div style={{
          padding: '20px',
          borderBottom: '1px solid var(--border)',
          background: 'var(--panel)',
          flexShrink: 0
        }}>
          <Section label="Add New Endpoint">
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <div style={{ flex: '0 0 90px' }}>
                <div className="hint" style={{ fontSize: 10, marginBottom: 4 }}>Method</div>
                <select
                  className="input"
                  value={addMethod}
                  onChange={e => setAddMethod(e.target.value)}
                  style={{ width: '100%', marginTop: 0 }}
                >
                  {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => (
                    <option key={m}>{m}</option>
                  ))}
                </select>
              </div>
              <div style={{ flex: 2, minWidth: 180 }}>
                <div className="hint" style={{ fontSize: 10, marginBottom: 4 }}>Path</div>
                <input
                  className="input"
                  placeholder="/v1/orders"
                  value={addPath}
                  onChange={e => setAddPath(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && handleAddApi()}
                  style={{ width: '100%', marginTop: 0 }}
                />
              </div>
              <div style={{ flex: 1, minWidth: 120 }}>
                <div className="hint" style={{ fontSize: 10, marginBottom: 4 }}>Name</div>
                <input
                  className="input"
                  placeholder="list_orders"
                  value={addName}
                  onChange={e => setAddName(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && handleAddApi()}
                  style={{ width: '100%', marginTop: 0 }}
                />
              </div>
              <div style={{ flex: 1, minWidth: 140 }}>
                <div className="hint" style={{ fontSize: 10, marginBottom: 4 }}>Target Flow</div>
                <select
                  className="input"
                  value={selectedFlow}
                  onChange={e => setSelectedFlow(e.target.value)}
                  style={{ width: '100%', marginTop: 0 }}
                >
                  <option value="">-- select flow --</option>
                  {flowNames.map(name => (
                    <option key={name} value={name}>{name}</option>
                  ))}
                </select>
              </div>
              <button
                className="btn"
                style={{ width: 'auto', padding: '0 20px', marginTop: 0, height: 38 }}
                disabled={!addName.trim() || !addPath.trim() || !selectedFlow}
                onClick={handleAddApi}
              >
                Add
              </button>
            </div>
          </Section>
        </div>

        {!selectedFlow ? (
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 8, padding: 40 }}>
            <span style={{ fontSize: 28, opacity: 0.25 }}>⇠</span>
            <p className="hint" style={{ textAlign: 'center' }}>
              Select a flow from the sidebar to view its existing endpoints,<br />
              or use the form above to register a new one.
            </p>
          </div>
        ) : (
          <>
            {/* list header */}
            <div style={{
              padding: '12px 20px',
              borderBottom: '1px solid var(--border)',
              background: 'var(--panel)',
              display: 'flex', alignItems: 'center', gap: 10, flexShrink: 0,
            }}>
              <span style={{ fontSize: 13, fontWeight: 600 }}>Mapped Endpoints</span>
              <span style={{ color: 'var(--muted)', fontSize: 13 }}>→</span>
              <span style={{ color: 'var(--accent)', fontFamily: 'monospace', fontSize: 13 }}>{selectedFlow}</span>
              <span style={{ color: 'var(--muted)', fontSize: 12 }}>
                ({currentApis.length})
              </span>
            </div>

            <div style={{ padding: 20, flex: 1 }}>
              {/* API list */}
              {currentApis.length === 0 ? (
                <p className="hint">No endpoints mapped to this flow yet.</p>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                  {currentApis.map(a => {
                    const gi = apis.indexOf(a)
                    return (
                      <div key={gi} className="step" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 12px' }}>
                        <MethodBadge method={a.method} />
                        <span style={{ flex: 1, fontFamily: 'monospace', fontSize: 13 }}>{a.path}</span>
                        <span style={{ color: 'var(--muted)', fontSize: 12 }}>{a.name}</span>
                        <button
                          className="btn muted"
                          style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 12 }}
                          onClick={() => handleRemove(gi)}
                        >
                          ✕
                        </button>
                      </div>
                    )
                  })}
                </div>
              )}

              {/* OpenAPI import (scoped to selected flow) */}
              <Section label="Import from OpenAPI" style={{ marginTop: 40 }}>
                <p className="hint" style={{ marginBottom: 12 }}>Import multiple endpoints directly into <strong>{selectedFlow}</strong>.</p>
                <textarea
                  className="input"
                  placeholder="Paste OpenAPI spec (JSON or YAML)…"
                  value={spec}
                  onChange={e => setSpec(e.target.value)}
                  style={{ minHeight: 120 }}
                />
                <button
                  className="btn mt8"
                  style={{ width: 'auto', padding: '0 20px' }}
                  onClick={handleImport}
                  disabled={importing}
                >
                  {importing ? 'Importing…' : `Import → ${selectedFlow}`}
                </button>
                {importMsg && (
                  <p className={`mt8 ${importErr ? 'status-err' : 'status-ok'}`}>{importMsg}</p>
                )}
              </Section>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// ── small helpers ────────────────────────────────────────────────

function MethodBadge({ method }: { method: string }) {
  return (
    <span style={{
      fontSize: 10, fontWeight: 700,
      padding: '2px 7px', borderRadius: 4,
      color: '#fff',
      background: METHOD_COLOR[method] ?? '#64748b',
      minWidth: 50, textAlign: 'center',
      letterSpacing: 0.4, flexShrink: 0,
    }}>
      {method}
    </span>
  )
}

function Section({ label, children, style }: { label: string; children: React.ReactNode; style?: React.CSSProperties }) {
  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, ...style }}>
      <div style={{
        fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase',
        letterSpacing: 1, marginBottom: 12,
      }}>
        {label}
      </div>
      {children}
    </div>
  )
}
