import { useEffect, useState } from 'react'
import { listNamedQueries, upsertNamedQuery, deleteNamedQuery, NamedQueryDef } from '../api'
import ConfirmDialog from './ConfirmDialog'

const card: React.CSSProperties = {
  background: '#1e1e2e', border: '1px solid #313244', borderRadius: 8, padding: 16, marginBottom: 12,
}
const btn = (color = '#89b4fa'): React.CSSProperties => ({
  background: 'transparent', border: `1px solid ${color}`, color, borderRadius: 4,
  padding: '4px 10px', cursor: 'pointer', fontSize: 12,
})
const input: React.CSSProperties = {
  background: '#181825', border: '1px solid #313244', color: '#cdd6f4',
  borderRadius: 4, padding: '4px 8px', fontSize: 13, width: '100%', boxSizing: 'border-box',
}
const label: React.CSSProperties = { fontSize: 12, color: '#a6adc8', marginBottom: 4, display: 'block' }
const row: React.CSSProperties = { display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8 }

const EMPTY_QUERY: NamedQueryDef = { sql: '' }

export default function NamedQueries() {
  const [queries, setQueries] = useState<Record<string, NamedQueryDef>>({})
  const [selectedQuery, setSelectedQuery] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const [newQueryName, setNewQueryName] = useState('')
  const [showNewQuery, setShowNewQuery] = useState(false)
  const [showQueryForm, setShowQueryForm] = useState(false)
  const [queryForm, setQueryForm] = useState<NamedQueryDef>(EMPTY_QUERY)
  const [confirmDialog, setConfirmDialog] = useState<{ title: string; message: string; onConfirm: () => void } | null>(null)

  useEffect(() => { loadQueries() }, [])

  function loadQueries() {
    listNamedQueries()
      .then(r => setQueries(r.queries ?? {}))
      .catch(e => setErr(String(e)))
  }

  function handleSelectQuery(name: string) {
    setSelectedQuery(name)
    const q = queries[name]
    if (q) setQueryForm({ ...q })
    setShowQueryForm(false)
  }

  function handleAddQuery() {
    const name = newQueryName.trim()
    if (!name) return
    if (!queries[name]) setQueries(q => ({ ...q, [name]: EMPTY_QUERY }))
    setSelectedQuery(name)
    setQueryForm(EMPTY_QUERY)
    setNewQueryName('')
    setShowNewQuery(false)
  }

  async function handleDeleteQuery(name: string) {
    setConfirmDialog({
      title: 'Delete Named Query',
      message: `Delete named query "${name}"?`,
      onConfirm: async () => {
        try {
          await deleteNamedQuery(name)
          setQueries(q => {
            const updated = { ...q }
            delete updated[name]
            return updated
          })
          if (selectedQuery === name) {
            setSelectedQuery(null)
            setShowQueryForm(false)
          }
        } catch (e) { setErr(String(e)) }
        setConfirmDialog(null)
      }
    })
  }

  function handleNewQuery() {
    setQueryForm(EMPTY_QUERY)
    setShowQueryForm(true)
  }

  async function handleSaveQuery() {
    if (!selectedQuery || !queryForm.sql.trim()) return
    try {
      await upsertNamedQuery(selectedQuery, queryForm)
      await loadQueries()
      setShowQueryForm(false)
      setSelectedQuery(selectedQuery)
    } catch (e) { setErr(String(e)) }
  }

  return (
    <div style={{ display: 'flex', gap: 16, height: '100%' }}>
      {/* Left panel — named queries list */}
      <div style={{ width: 220, flexShrink: 0 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>Named Queries</span>
          <button style={btn()} onClick={() => setShowNewQuery(v => !v)}>+ New</button>
        </div>
        {showNewQuery && (
          <div style={{ ...card, marginBottom: 8 }}>
            <div style={row}>
              <input
                style={{ ...input, flex: 1 }}
                placeholder="query name"
                value={newQueryName}
                onChange={e => setNewQueryName(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleAddQuery()}
              />
              <button style={btn('#a6e3a1')} onClick={handleAddQuery}>Add</button>
            </div>
          </div>
        )}
        {Object.keys(queries).map(name => (
          <div
            key={name}
            onClick={() => handleSelectQuery(name)}
            style={{
              ...card,
              cursor: 'pointer',
              borderColor: selectedQuery === name ? '#89b4fa' : '#313244',
              display: 'flex', justifyContent: 'space-between', alignItems: 'center',
            }}
          >
            <span style={{ fontSize: 13, color: selectedQuery === name ? '#89b4fa' : '#cdd6f4' }}>{name}</span>
            <button
              style={btn('#f38ba8')}
              onClick={e => { e.stopPropagation(); handleDeleteQuery(name) }}
            >✕</button>
          </div>
        ))}
      </div>

      {/* Right panel — query details */}
      <div style={{ flex: 1, minWidth: 0 }}>
        {err && <div style={{ color: '#f38ba8', marginBottom: 8, fontSize: 13 }}>{err}</div>}
        {!selectedQuery ? (
          <div style={{ color: '#6c7086', fontSize: 13 }}>Select or create a named query to view details.</div>
        ) : (
          <>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
              <span style={{ fontSize: 14, color: '#cdd6f4', fontWeight: 600 }}>{selectedQuery}</span>
              <button style={btn()} onClick={handleNewQuery}>Edit</button>
            </div>

            {showQueryForm && (
              <div style={card}>
                <div style={{ marginBottom: 12 }}>
                  <span style={label}>SQL Query *</span>
                  <textarea
                    style={{
                      ...input,
                      fontFamily: 'monospace',
                      fontSize: 12,
                      minHeight: 120,
                      resize: 'vertical',
                    }}
                    value={queryForm.sql}
                    onChange={e => setQueryForm(q => ({ ...q, sql: e.target.value }))}
                    placeholder="SELECT * FROM ..."
                  />
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 12 }}>
                  <div>
                    <span style={label}>Batch By</span>
                    <input
                      style={input}
                      value={queryForm.batch_by ?? ''}
                      onChange={e => setQueryForm(q => ({ ...q, batch_by: e.target.value || undefined }))}
                      placeholder="field_name"
                    />
                  </div>
                  <div>
                    <span style={label}>Batch Window</span>
                    <input
                      style={input}
                      value={queryForm.batch_window ?? ''}
                      onChange={e => setQueryForm(q => ({ ...q, batch_window: e.target.value || undefined }))}
                      placeholder="10s"
                    />
                  </div>
                </div>

                <div style={{ marginBottom: 12 }}>
                  <span style={label}>Batch Max</span>
                  <input
                    style={input}
                    type="number"
                    value={queryForm.batch_max ?? ''}
                    onChange={e => setQueryForm(q => ({ ...q, batch_max: e.target.value ? Number(e.target.value) : undefined }))}
                    placeholder="100"
                  />
                </div>

                <div style={{ display: 'flex', gap: 8 }}>
                  <button style={btn('#a6e3a1')} onClick={handleSaveQuery}>Save</button>
                  <button style={btn('#6c7086')} onClick={() => setShowQueryForm(false)}>Cancel</button>
                </div>
              </div>
            )}

            {!showQueryForm && (
              <div style={card}>
                <div>
                  <span style={{ fontSize: 12, color: '#a6adc8', display: 'block', marginBottom: 8 }}>SQL Query</span>
                  <div
                    style={{
                      background: '#181825',
                      border: '1px solid #313244',
                      borderRadius: 4,
                      padding: '8px',
                      fontFamily: 'monospace',
                      fontSize: 12,
                      color: '#89b4fa',
                      overflowX: 'auto',
                      wordBreak: 'break-all',
                    }}
                  >
                    {queryForm.sql || '(empty)'}
                  </div>
                </div>

                {queryForm.batch_by && (
                  <div style={{ marginTop: 12 }}>
                    <span style={{ fontSize: 11, color: '#6c7086' }}>Batch by: {queryForm.batch_by}</span>
                  </div>
                )}
                {queryForm.batch_window && (
                  <div>
                    <span style={{ fontSize: 11, color: '#6c7086' }}>Batch window: {queryForm.batch_window}</span>
                  </div>
                )}
                {queryForm.batch_max && (
                  <div>
                    <span style={{ fontSize: 11, color: '#6c7086' }}>Batch max: {queryForm.batch_max}</span>
                  </div>
                )}
              </div>
            )}
          </>
        )}
      </div>

      {confirmDialog && (
        <ConfirmDialog
          title={confirmDialog.title}
          message={confirmDialog.message}
          confirmLabel="Delete"
          onConfirm={confirmDialog.onConfirm}
          onCancel={() => setConfirmDialog(null)}
        />
      )}
    </div>
  )
}
