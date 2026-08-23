import { useEffect, useState } from 'react'
import {
  listDocumentConnectors,
  addDocumentConnector,
  updateDocumentConnector,
  deleteDocumentConnector,
} from '../api'
import type { DocumentConnectorDef } from '../api'

const YAML_SNIPPET = `document_connectors:
  - name: my-mongo
    kind: mongodb
    uri: "mongodb://localhost:27017"
    database: mydb
    pool_size: 10
    timeout_ms: 5000

  - name: my-postgres
    kind: postgresql
    uri: "postgresql://user:pass@localhost:5432/mydb"
    database: mydb

  - name: my-mysql
    kind: mysql
    uri: "user:pass@tcp(localhost:3306)/mydb"
    database: mydb`

const KIND_COLORS: Record<string, { bg: string; text: string }> = {
  mongodb:    { bg: 'rgba(166,227,161,0.1)', text: '#a6e3a1' },
  postgresql: { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' },
  mysql:      { bg: 'rgba(250,179,135,0.1)', text: '#fab387' },
  grpc:       { bg: 'rgba(203,166,247,0.1)', text: '#cba6f7' },
}

const KINDS = ['mongodb', 'postgresql', 'mysql', 'grpc'] as const

const URI_PLACEHOLDERS: Record<string, string> = {
  mongodb:    'mongodb://localhost:27017',
  postgresql: 'postgresql://user:pass@localhost:5432/mydb',
  mysql:      'user:pass@tcp(localhost:3306)/mydb',
  grpc:       '',
}

const EMPTY_FORM: DocumentConnectorDef = {
  name: '',
  kind: 'mongodb',
  uri: '',
  database: '',
  credential_ref: '',
  pool_size: undefined,
  tls_enabled: false,
  timeout_ms: undefined,
  grpc_endpoint: '',
}

// ── Shared input style ────────────────────────────────────────────────────────
const inputStyle: React.CSSProperties = {
  width: '100%',
  boxSizing: 'border-box',
  background: '#181825',
  border: '1px solid #313244',
  borderRadius: 6,
  color: '#cdd6f4',
  fontSize: 12,
  padding: '6px 10px',
  outline: 'none',
}

const labelStyle: React.CSSProperties = {
  fontSize: 11,
  color: '#6c7086',
  marginBottom: 4,
  display: 'block',
}

const fieldWrap: React.CSSProperties = { marginBottom: 12 }

// ── Icon helpers ──────────────────────────────────────────────────────────────
function PencilIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
      <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
    </svg>
  )
}

function TrashIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="3 6 5 6 21 6"/>
      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
      <path d="M10 11v6M14 11v6"/>
      <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
    </svg>
  )
}

// ── Form component ────────────────────────────────────────────────────────────
interface FormProps {
  initial: DocumentConnectorDef
  isEdit: boolean
  onSave: (cfg: DocumentConnectorDef) => Promise<void>
  onCancel: () => void
}

function ConnectorForm({ initial, isEdit, onSave, onCancel }: FormProps) {
  const [form, setForm] = useState<DocumentConnectorDef>({ ...initial })
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  function set<K extends keyof DocumentConnectorDef>(key: K, value: DocumentConnectorDef[K]) {
    setForm(f => ({ ...f, [key]: value }))
  }

  async function handleSave() {
    if (!form.name.trim()) { setFormError('Name is required.'); return }
    if (!form.kind) { setFormError('Kind is required.'); return }
    setSaving(true)
    setFormError(null)
    try {
      // Strip empty optional strings so the backend doesn't see empty strings
      const payload: DocumentConnectorDef = { name: form.name.trim(), kind: form.kind }
      if (form.uri?.trim())            payload.uri            = form.uri.trim()
      if (form.database?.trim())       payload.database       = form.database.trim()
      if (form.credential_ref?.trim()) payload.credential_ref = form.credential_ref.trim()
      if (form.grpc_endpoint?.trim())  payload.grpc_endpoint  = form.grpc_endpoint.trim()
      if (form.pool_size != null && form.pool_size > 0)   payload.pool_size  = form.pool_size
      if (form.timeout_ms != null && form.timeout_ms > 0) payload.timeout_ms = form.timeout_ms
      payload.tls_enabled = !!form.tls_enabled
      await onSave(payload)
    } catch (e) {
      setFormError(String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{
      background: '#181825',
      border: '1px solid #313244',
      borderRadius: 8,
      padding: 20,
      marginTop: 16,
    }}>
      <h3 style={{ fontSize: 13, fontWeight: 700, color: '#cdd6f4', marginBottom: 16 }}>
        {isEdit ? `Edit Connector — ${initial.name}` : 'Add Connector'}
      </h3>

      {formError && (
        <div style={{ color: '#f38ba8', fontSize: 12, marginBottom: 12 }}>{formError}</div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 16px' }}>
        {/* Name */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Name *</label>
          <input
            style={{ ...inputStyle, opacity: isEdit ? 0.5 : 1 }}
            value={form.name}
            disabled={isEdit}
            placeholder="my-connector"
            onChange={e => set('name', e.target.value)}
          />
        </div>

        {/* Kind */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Kind *</label>
          <select
            style={{ ...inputStyle, cursor: 'pointer' }}
            value={form.kind}
            onChange={e => set('kind', e.target.value)}
          >
            {KINDS.map(k => <option key={k} value={k}>{k}</option>)}
          </select>
        </div>

        {/* URI — hidden for grpc */}
        {form.kind !== 'grpc' && (
          <div style={{ ...fieldWrap, gridColumn: '1 / -1' }}>
            <label style={labelStyle}>URI</label>
            <input
              style={inputStyle}
              value={form.uri ?? ''}
              placeholder={URI_PLACEHOLDERS[form.kind] ?? ''}
              onChange={e => set('uri', e.target.value)}
            />
          </div>
        )}

        {/* gRPC endpoint — only for grpc */}
        {form.kind === 'grpc' && (
          <div style={{ ...fieldWrap, gridColumn: '1 / -1' }}>
            <label style={labelStyle}>gRPC Endpoint</label>
            <input
              style={inputStyle}
              value={form.grpc_endpoint ?? ''}
              placeholder="host:port"
              onChange={e => set('grpc_endpoint', e.target.value)}
            />
          </div>
        )}

        {/* Database */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Database</label>
          <input
            style={inputStyle}
            value={form.database ?? ''}
            placeholder="mydb"
            onChange={e => set('database', e.target.value)}
          />
        </div>

        {/* Credential Ref */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Credential Ref</label>
          <input
            style={inputStyle}
            value={form.credential_ref ?? ''}
            placeholder="env:MY_VAR"
            onChange={e => set('credential_ref', e.target.value)}
          />
        </div>

        {/* Pool Size */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Pool Size</label>
          <input
            style={inputStyle}
            type="number"
            min={1}
            value={form.pool_size ?? ''}
            placeholder="10"
            onChange={e => set('pool_size', e.target.value ? Number(e.target.value) : undefined)}
          />
        </div>

        {/* Timeout MS */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Timeout MS</label>
          <input
            style={inputStyle}
            type="number"
            min={1}
            value={form.timeout_ms ?? ''}
            placeholder="5000"
            onChange={e => set('timeout_ms', e.target.value ? Number(e.target.value) : undefined)}
          />
        </div>

        {/* TLS Enabled */}
        <div style={{ ...fieldWrap, display: 'flex', alignItems: 'center', gap: 8 }}>
          <input
            id="tls-enabled"
            type="checkbox"
            checked={!!form.tls_enabled}
            onChange={e => set('tls_enabled', e.target.checked)}
            style={{ accentColor: '#89b4fa', cursor: 'pointer' }}
          />
          <label htmlFor="tls-enabled" style={{ ...labelStyle, marginBottom: 0, cursor: 'pointer', color: '#cdd6f4' }}>
            TLS Enabled
          </label>
        </div>
      </div>

      {/* Buttons */}
      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <button
          style={{
            fontSize: 12,
            padding: '6px 16px',
            border: 'none',
            borderRadius: 6,
            background: '#89b4fa',
            color: '#1e1e2e',
            cursor: saving ? 'not-allowed' : 'pointer',
            fontWeight: 600,
            opacity: saving ? 0.7 : 1,
          }}
          disabled={saving}
          onClick={handleSave}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button
          style={{
            fontSize: 12,
            padding: '6px 14px',
            border: '1px solid #313244',
            borderRadius: 6,
            background: '#1e1e2e',
            color: '#cdd6f4',
            cursor: 'pointer',
          }}
          disabled={saving}
          onClick={onCancel}
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────────────
type FormMode = { mode: 'add' } | { mode: 'edit'; connector: DocumentConnectorDef } | { mode: 'none' }

export default function DocumentConnectors() {
  const [connectors, setConnectors] = useState<DocumentConnectorDef[]>([])
  const [loading, setLoading]       = useState(true)
  const [error, setError]           = useState<string | null>(null)
  const [showSnippet, setShowSnippet] = useState(false)
  const [formMode, setFormMode]     = useState<FormMode>({ mode: 'none' })
  const [actionError, setActionError] = useState<string | null>(null)

  function load() {
    setLoading(true)
    setError(null)
    listDocumentConnectors()
      .then(r => setConnectors(r.connectors ?? []))
      .catch(e => setError(String(e)))
      .finally(() => setLoading(false))
  }

  useEffect(load, [])

  async function handleSave(cfg: DocumentConnectorDef) {
    setActionError(null)
    if (formMode.mode === 'add') {
      await addDocumentConnector(cfg)
    } else if (formMode.mode === 'edit') {
      await updateDocumentConnector(formMode.connector.name, cfg)
    }
    setFormMode({ mode: 'none' })
    load()
  }

  async function handleDelete(name: string) {
    if (!window.confirm(`Delete connector "${name}"? This cannot be undone.`)) return
    setActionError(null)
    try {
      await deleteDocumentConnector(name)
      load()
    } catch (e) {
      setActionError(String(e))
    }
  }

  if (loading) return <div style={{ padding: '20px 24px', color: '#a6adc8', fontSize: 13 }}>Loading…</div>
  if (error)   return <div style={{ padding: '20px 24px', color: '#f38ba8', fontSize: 13 }}>Error: {error}</div>

  const showForm = formMode.mode !== 'none'

  return (
    <div style={{ padding: '20px 24px', height: '100%', overflowY: 'auto' }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>Document Connectors</h2>
          <p style={{ fontSize: 12, color: '#6c7086' }}>
            Defined via the gateway management API. Used in <code>doc_get</code> / <code>doc_put</code> / <code>doc_query</code> flow steps.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button
            style={{ fontSize: 12, padding: '5px 12px', border: '1px solid #313244', borderRadius: 6, background: '#1e1e2e', color: '#cdd6f4', cursor: 'pointer' }}
            onClick={() => setShowSnippet(v => !v)}
          >
            {showSnippet ? 'Hide config' : 'How to configure'}
          </button>
          <button
            style={{
              fontSize: 12,
              padding: '5px 12px',
              border: 'none',
              borderRadius: 6,
              background: '#89b4fa',
              color: '#1e1e2e',
              cursor: 'pointer',
              fontWeight: 600,
              opacity: showForm ? 0.5 : 1,
            }}
            disabled={showForm}
            onClick={() => { setFormMode({ mode: 'add' }); setActionError(null) }}
          >
            + Add Connector
          </button>
        </div>
      </div>

      {/* YAML snippet */}
      {showSnippet && (
        <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 14, marginBottom: 16 }}>
          <div style={{ fontSize: 11, color: '#6c7086', marginBottom: 8 }}>Add to gateway.yaml, then restart the gateway:</div>
          <pre style={{ margin: 0, fontSize: 12, color: '#cdd6f4', whiteSpace: 'pre', overflowX: 'auto' }}>{YAML_SNIPPET}</pre>
          <div style={{ fontSize: 11, color: '#6c7086', marginTop: 8 }}>
            Supported kinds: <code>mongodb</code>, <code>postgresql</code>, <code>mysql</code>, <code>grpc</code>.
            Use <code>credential_ref: "env:VAR"</code> to load credentials from environment variables.
          </div>
        </div>
      )}

      {/* Action-level error (delete failures, etc.) */}
      {actionError && (
        <div style={{ color: '#f38ba8', fontSize: 12, marginBottom: 12 }}>{actionError}</div>
      )}

      {/* Connector list */}
      {connectors.length === 0 && !showForm ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>
          No document connectors configured.{' '}
          <span
            style={{ cursor: 'pointer', textDecoration: 'underline', color: '#89b4fa' }}
            onClick={() => setShowSnippet(true)}
          >
            See how to add one.
          </span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {connectors.map(c => {
            const colors = KIND_COLORS[c.kind] ?? { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' }
            const isBeingEdited = formMode.mode === 'edit' && formMode.connector.name === c.name
            return (
              <div
                key={c.name}
                style={{
                  background: isBeingEdited ? '#1e1e3a' : '#1e1e2e',
                  border: `1px solid ${isBeingEdited ? '#89b4fa' : '#313244'}`,
                  borderRadius: 8,
                  padding: '10px 14px',
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>{c.name}</span>
                  <span style={{ fontSize: 11, color: colors.text, background: colors.bg, padding: '2px 8px', borderRadius: 4 }}>
                    {c.kind}
                  </span>
                  {c.uri && (
                    <span style={{ fontSize: 11, color: '#6c7086', fontFamily: 'monospace' }}>{c.uri}</span>
                  )}
                  {c.database && (
                    <span style={{ fontSize: 11, color: '#6c7086' }}>db: {c.database}</span>
                  )}
                </div>
                <div style={{ display: 'flex', gap: 6 }}>
                  <button
                    title="Edit"
                    style={{
                      background: 'none',
                      border: '1px solid #313244',
                      borderRadius: 5,
                      color: '#89b4fa',
                      cursor: 'pointer',
                      padding: '4px 7px',
                      lineHeight: 1,
                      display: 'flex',
                      alignItems: 'center',
                    }}
                    onClick={() => {
                      setFormMode({ mode: 'edit', connector: c })
                      setActionError(null)
                    }}
                  >
                    <PencilIcon />
                  </button>
                  <button
                    title="Delete"
                    style={{
                      background: 'none',
                      border: '1px solid #313244',
                      borderRadius: 5,
                      color: '#f38ba8',
                      cursor: 'pointer',
                      padding: '4px 7px',
                      lineHeight: 1,
                      display: 'flex',
                      alignItems: 'center',
                    }}
                    onClick={() => handleDelete(c.name)}
                  >
                    <TrashIcon />
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Inline form */}
      {formMode.mode === 'add' && (
        <ConnectorForm
          initial={EMPTY_FORM}
          isEdit={false}
          onSave={handleSave}
          onCancel={() => setFormMode({ mode: 'none' })}
        />
      )}
      {formMode.mode === 'edit' && (
        <ConnectorForm
          initial={formMode.connector}
          isEdit={true}
          onSave={handleSave}
          onCancel={() => setFormMode({ mode: 'none' })}
        />
      )}
    </div>
  )
}
