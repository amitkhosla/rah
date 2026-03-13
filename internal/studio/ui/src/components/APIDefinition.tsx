import { useState } from 'react'
import { importOpenAPI } from '../api'
import type { ApiDef } from '../types'

interface Props {
  apis: ApiDef[]
  setApis: (apis: ApiDef[]) => void
  flowName: string
}

export default function APIDefinition({ apis, setApis, flowName }: Props) {
  const [spec, setSpec] = useState('')
  const [importMsg, setImportMsg] = useState('')
  const [importErr, setImportErr] = useState(false)
  const [importing, setImporting] = useState(false)

  const [name, setName] = useState('')
  const [path, setPath] = useState('')
  const [method, setMethod] = useState('GET')

  async function handleImport() {
    if (!spec.trim()) return
    setImporting(true)
    setImportMsg('')
    setImportErr(false)
    try {
      const data = await importOpenAPI(spec)
      const imported = data.apis.map(a => ({ name: a.name, path: a.path, method: a.method }))
      setApis([...apis, ...imported])
      setImportMsg(`Imported ${imported.length} API(s) from ${data.source}`)
    } catch (e) {
      setImportErr(true)
      setImportMsg(e instanceof Error ? e.message : 'Import failed')
    } finally {
      setImporting(false)
    }
  }

  function handleAdd() {
    if (!name.trim() || !path.trim()) return
    setApis([...apis, { name: name.trim(), path: path.trim(), method: method.toUpperCase() }])
    setName('')
    setPath('')
    setMethod('GET')
  }

  function removeApi(i: number) {
    setApis(apis.filter((_, idx) => idx !== i))
  }

  const linked = flowName || 'new_flow'

  return (
    <div className="two-col">
      {/* OpenAPI import */}
      <div className="panel">
        <div className="panel-header">OpenAPI Import (JSON or YAML)</div>
        <div className="panel-body">
          <textarea
            className="input"
            placeholder="Paste OpenAPI spec here…"
            value={spec}
            onChange={e => setSpec(e.target.value)}
            style={{ minHeight: 180 }}
          />
          <button className="btn mt8" onClick={handleImport} disabled={importing}>
            {importing ? 'Importing…' : 'Import APIs'}
          </button>
          {importMsg && (
            <p className={`mt8 ${importErr ? 'status-err' : 'status-ok'}`}>{importMsg}</p>
          )}
        </div>
      </div>

      {/* Manual add + list */}
      <div className="panel">
        <div className="panel-header">API Definitions → flow: {linked}</div>
        <div className="panel-body">
          <input
            className="input"
            placeholder="api name"
            value={name}
            onChange={e => setName(e.target.value)}
          />
          <input
            className="input mt8"
            placeholder="path  e.g. /v1/orders"
            value={path}
            onChange={e => setPath(e.target.value)}
          />
          <select
            className="input mt8"
            value={method}
            onChange={e => setMethod(e.target.value)}
          >
            {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
          <button className="btn mt8" onClick={handleAdd}>Add API</button>

          <div className="mt12">
            {apis.length === 0 && <span className="hint">No APIs added yet.</span>}
            {apis.map((a, i) => (
              <div key={i} className="step" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <span className="hint" style={{ color: 'var(--text)' }}>
                  <strong>{a.method}</strong> {a.path}
                  <br />
                  <span style={{ fontSize: 11, color: 'var(--muted)' }}>{a.name} → {linked}</span>
                </span>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0 }}
                  onClick={() => removeApi(i)}
                >
                  ✕
                </button>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
