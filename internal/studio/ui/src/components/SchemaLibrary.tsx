import { useEffect, useState } from 'react'
import type { FieldSchema } from '../types'
import { listSchemaSets, listSchemaFields, upsertSchemaField, deleteSchemaField, deleteSchemaSet } from '../api'

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

const EMPTY_FIELD: FieldSchema = { name: '', path: '', required: false, type: 'string' }

export default function SchemaLibrary() {
  const [sets, setSets] = useState<string[]>([])
  const [selectedSet, setSelectedSet] = useState<string | null>(null)
  const [fields, setFields] = useState<FieldSchema[]>([])
  const [err, setErr] = useState('')
  const [newSetName, setNewSetName] = useState('')
  const [showNewSet, setShowNewSet] = useState(false)
  const [editField, setEditField] = useState<FieldSchema | null>(null)
  const [showFieldForm, setShowFieldForm] = useState(false)
  const [fieldForm, setFieldForm] = useState<FieldSchema>(EMPTY_FIELD)
  const [showAdvanced, setShowAdvanced] = useState(false)

  useEffect(() => { loadSets() }, [])
  useEffect(() => { if (selectedSet) loadFields(selectedSet) }, [selectedSet])

  function loadSets() {
    listSchemaSets().then(setSets).catch(e => setErr(String(e)))
  }
  function loadFields(name: string) {
    listSchemaFields(name).then(setFields).catch(e => setErr(String(e)))
  }

  function handleSelectSet(name: string) {
    setSelectedSet(name)
    setShowFieldForm(false)
    setEditField(null)
  }

  function handleAddSet() {
    const name = newSetName.trim()
    if (!name) return
    if (!sets.includes(name)) setSets(s => [...s, name])
    setSelectedSet(name)
    setFields([])
    setNewSetName('')
    setShowNewSet(false)
  }

  async function handleDeleteSet(name: string) {
    if (!window.confirm(`Delete entire schema set "${name}" and all its fields?`)) return
    try {
      await deleteSchemaSet(name)
      setSets(s => s.filter(x => x !== name))
      if (selectedSet === name) { setSelectedSet(null); setFields([]) }
    } catch (e) { setErr(String(e)) }
  }

  function handleEditField(f: FieldSchema) {
    setFieldForm({ ...f })
    setEditField(f)
    setShowFieldForm(true)
  }

  function handleNewField() {
    setFieldForm(EMPTY_FIELD)
    setEditField(null)
    setShowFieldForm(true)
  }

  async function handleSaveField() {
    if (!selectedSet || !fieldForm.name.trim()) return
    try {
      await upsertSchemaField(selectedSet, fieldForm)
      await loadFields(selectedSet)
      setShowFieldForm(false)
      setEditField(null)
    } catch (e) { setErr(String(e)) }
  }

  async function handleDeleteField(fieldName: string) {
    if (!selectedSet) return
    if (!window.confirm(`Delete field "${fieldName}"?`)) return
    try {
      await deleteSchemaField(selectedSet, fieldName)
      setFields(f => f.filter(x => x.name !== fieldName))
    } catch (e) { setErr(String(e)) }
  }

  return (
    <div style={{ display: 'flex', gap: 16, height: '100%' }}>
      {/* Left panel — schema set list */}
      <div style={{ width: 220, flexShrink: 0 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>Schema Sets</span>
          <button style={btn()} onClick={() => setShowNewSet(v => !v)}>+ New</button>
        </div>
        {showNewSet && (
          <div style={{ ...card, marginBottom: 8 }}>
            <div style={row}>
              <input
                style={{ ...input, flex: 1 }}
                placeholder="set name"
                value={newSetName}
                onChange={e => setNewSetName(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleAddSet()}
              />
              <button style={btn('#a6e3a1')} onClick={handleAddSet}>Add</button>
            </div>
          </div>
        )}
        {sets.map(name => (
          <div
            key={name}
            onClick={() => handleSelectSet(name)}
            style={{
              ...card,
              cursor: 'pointer',
              borderColor: selectedSet === name ? '#89b4fa' : '#313244',
              display: 'flex', justifyContent: 'space-between', alignItems: 'center',
            }}
          >
            <span style={{ fontSize: 13, color: selectedSet === name ? '#89b4fa' : '#cdd6f4' }}>{name}</span>
            <button
              style={btn('#f38ba8')}
              onClick={e => { e.stopPropagation(); handleDeleteSet(name) }}
            >✕</button>
          </div>
        ))}
      </div>

      {/* Right panel — fields */}
      <div style={{ flex: 1, minWidth: 0 }}>
        {err && <div style={{ color: '#f38ba8', marginBottom: 8, fontSize: 13 }}>{err}</div>}
        {!selectedSet ? (
          <div style={{ color: '#6c7086', fontSize: 13 }}>Select a schema set to view its fields.</div>
        ) : (
          <>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
              <span style={{ fontSize: 14, color: '#cdd6f4', fontWeight: 600 }}>{selectedSet}</span>
              <button style={btn()} onClick={handleNewField}>+ Add Field</button>
            </div>

            {showFieldForm && (
              <div style={card}>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 8 }}>
                  <div>
                    <span style={label}>Name *</span>
                    <input style={input} value={fieldForm.name} onChange={e => setFieldForm(f => ({ ...f, name: e.target.value }))} placeholder="email" />
                  </div>
                  <div>
                    <span style={label}>Path (gjson)</span>
                    <input style={input} value={fieldForm.path} onChange={e => setFieldForm(f => ({ ...f, path: e.target.value }))} placeholder="user.email" />
                  </div>
                  <div>
                    <span style={label}>Type</span>
                    <select style={{ ...input }} value={fieldForm.type ?? 'string'} onChange={e => setFieldForm(f => ({ ...f, type: e.target.value }))}>
                      {['string','number','bool','array','object'].map(t => <option key={t}>{t}</option>)}
                    </select>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <input type="checkbox" id="req" checked={!!fieldForm.required} onChange={e => setFieldForm(f => ({ ...f, required: e.target.checked }))} />
                    <label htmlFor="req" style={{ ...label, marginBottom: 0 }}>Required</label>
                  </div>
                  <div>
                    <span style={label}>Pattern (regex)</span>
                    <input style={input} value={fieldForm.pattern ?? ''} onChange={e => setFieldForm(f => ({ ...f, pattern: e.target.value }))} placeholder="^[a-z]+$" />
                  </div>
                  <div>
                    <span style={label}>Enum Values (comma-separated)</span>
                    <input style={input} value={(fieldForm.enumValues ?? []).join(',')} onChange={e => setFieldForm(f => ({ ...f, enumValues: e.target.value ? e.target.value.split(',') : [] }))} placeholder="a,b,c" />
                  </div>
                </div>
                <div>
                  <button style={{ ...btn('#a6adc8'), marginBottom: 8 }} onClick={() => setShowAdvanced(v => !v)}>
                    {showAdvanced ? '▲ Hide' : '▼ Show'} Proto/Avro fields
                  </button>
                </div>
                {showAdvanced && (
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 8 }}>
                    <div>
                      <span style={label}>Proto Field Num</span>
                      <input style={input} type="number" value={fieldForm.protoFieldNum ?? ''} onChange={e => setFieldForm(f => ({ ...f, protoFieldNum: e.target.value ? Number(e.target.value) : undefined }))} />
                    </div>
                    <div>
                      <span style={label}>Proto Wire Type</span>
                      <input style={input} type="number" value={fieldForm.protoWireType ?? ''} onChange={e => setFieldForm(f => ({ ...f, protoWireType: e.target.value ? Number(e.target.value) : undefined }))} />
                    </div>
                    <div>
                      <span style={label}>Avro Type</span>
                      <input style={input} value={fieldForm.avroType ?? ''} onChange={e => setFieldForm(f => ({ ...f, avroType: e.target.value }))} />
                    </div>
                    <div>
                      <span style={label}>Avro Schema Ref</span>
                      <input style={input} value={fieldForm.avroSchemaRef ?? ''} onChange={e => setFieldForm(f => ({ ...f, avroSchemaRef: e.target.value }))} />
                    </div>
                  </div>
                )}
                <div style={{ display: 'flex', gap: 8 }}>
                  <button style={btn('#a6e3a1')} onClick={handleSaveField}>Save</button>
                  <button style={btn('#6c7086')} onClick={() => { setShowFieldForm(false); setEditField(null) }}>Cancel</button>
                </div>
              </div>
            )}

            {fields.length === 0 && !showFieldForm && (
              <div style={{ color: '#6c7086', fontSize: 13 }}>No fields yet. Click "+ Add Field" to create one.</div>
            )}

            {fields.map(f => (
              <div key={f.name} style={card}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                  <div>
                    <span style={{ color: '#89b4fa', fontWeight: 600, fontSize: 14 }}>{f.name}</span>
                    {f.required && <span style={{ color: '#f38ba8', fontSize: 11, marginLeft: 6 }}>required</span>}
                    {f.type && <span style={{ color: '#a6adc8', fontSize: 12, marginLeft: 8 }}>{f.type}</span>}
                    {f.path && <div style={{ color: '#6c7086', fontSize: 12, marginTop: 2 }}>path: {f.path}</div>}
                    {f.pattern && <div style={{ color: '#6c7086', fontSize: 12 }}>pattern: {f.pattern}</div>}
                    {f.enumValues && f.enumValues.length > 0 && <div style={{ color: '#6c7086', fontSize: 12 }}>enum: {f.enumValues.join(', ')}</div>}
                  </div>
                  <div style={{ display: 'flex', gap: 6 }}>
                    <button style={btn()} onClick={() => handleEditField(f)}>Edit</button>
                    <button style={btn('#f38ba8')} onClick={() => handleDeleteField(f.name)}>Delete</button>
                  </div>
                </div>
              </div>
            ))}
          </>
        )}
      </div>
    </div>
  )
}
