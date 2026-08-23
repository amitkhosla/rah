import { useEffect, useState } from 'react'
import {
  listSFTPConnectors,
  addSFTPConnector,
  updateSFTPConnector,
  deleteSFTPConnector,
} from '../api'
import type { SFTPConnectorDef } from '../api'

const YAML_SNIPPET = `sftp_connectors:
  - name: my-sftp
    host: sftp.example.com
    port: 22
    username: deploy
    password_ref: "env:SFTP_PASSWORD"
    # Or use a private key instead of a password:
    # private_key_ref: "env:SFTP_PRIVATE_KEY"
    timeout_ms: 5000`

const EMPTY: SFTPConnectorDef = {
  name: '', host: '', port: undefined, username: '',
  password_ref: '', private_key_ref: '', known_hosts_ref: '', timeout_ms: undefined,
}

const inputStyle: React.CSSProperties = {
  width: '100%', boxSizing: 'border-box', background: '#181825',
  border: '1px solid #313244', borderRadius: 6, color: '#cdd6f4',
  fontSize: 12, padding: '6px 8px', outline: 'none',
}
const labelStyle: React.CSSProperties = { fontSize: 11, color: '#a6adc8', marginBottom: 3, display: 'block' }
const fieldStyle: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 2 }
const btnPrimary: React.CSSProperties = {
  fontSize: 12, padding: '5px 14px', borderRadius: 6, border: 'none',
  background: '#89b4fa', color: '#1e1e2e', cursor: 'pointer', fontWeight: 600,
}
const btnSecondary: React.CSSProperties = {
  fontSize: 12, padding: '5px 14px', borderRadius: 6,
  border: '1px solid #313244', background: '#1e1e2e', color: '#cdd6f4', cursor: 'pointer',
}
const btnDanger: React.CSSProperties = {
  fontSize: 11, padding: '4px 10px', borderRadius: 5,
  border: '1px solid #f38ba8', background: 'transparent', color: '#f38ba8', cursor: 'pointer',
}
const btnEdit: React.CSSProperties = {
  fontSize: 11, padding: '4px 10px', borderRadius: 5,
  border: '1px solid #313244', background: 'transparent', color: '#cdd6f4', cursor: 'pointer',
}

export default function SFTPConnectors() {
  const [connectors, setConnectors] = useState<SFTPConnectorDef[]>([])
  const [loading, setLoading]       = useState(true)
  const [pageError, setPageError]   = useState<string | null>(null)
  const [showSnippet, setShowSnippet] = useState(false)

  // null = hidden, 'add' = new, string = editing by name
  const [formMode, setFormMode]     = useState<null | 'add' | string>(null)
  const [form, setForm]             = useState<SFTPConnectorDef>({ ...EMPTY })
  const [formError, setFormError]   = useState<string | null>(null)
  const [saving, setSaving]         = useState(false)

  function load() {
    setLoading(true)
    listSFTPConnectors()
      .then(r => setConnectors(r.connectors ?? []))
      .catch(e => setPageError(String(e)))
      .finally(() => setLoading(false))
  }

  useEffect(load, [])

  function openAdd() {
    setForm({ ...EMPTY })
    setFormError(null)
    setFormMode('add')
  }

  function openEdit(c: SFTPConnectorDef) {
    setForm({ ...c })
    setFormError(null)
    setFormMode(c.name)
  }

  function closeForm() {
    setFormMode(null)
    setFormError(null)
  }

  function set<K extends keyof SFTPConnectorDef>(key: K, val: SFTPConnectorDef[K]) {
    setForm(f => ({ ...f, [key]: val }))
  }

  function buildPayload(): SFTPConnectorDef {
    const out: SFTPConnectorDef = { name: form.name, host: form.host, username: form.username }
    if (form.port)             out.port             = Number(form.port)
    if (form.password_ref)     out.password_ref     = form.password_ref
    if (form.private_key_ref)  out.private_key_ref  = form.private_key_ref
    if (form.known_hosts_ref)  out.known_hosts_ref  = form.known_hosts_ref
    if (form.timeout_ms)       out.timeout_ms       = Number(form.timeout_ms)
    return out
  }

  async function handleSave() {
    setFormError(null)
    if (!form.name.trim())     { setFormError('Name is required');     return }
    if (!form.host.trim())     { setFormError('Host is required');     return }
    if (!form.username.trim()) { setFormError('Username is required'); return }
    setSaving(true)
    try {
      const cfg = buildPayload()
      if (formMode === 'add') {
        await addSFTPConnector(cfg)
      } else {
        await updateSFTPConnector(formMode as string, cfg)
      }
      closeForm()
      load()
    } catch (e) {
      setFormError(String(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(name: string) {
    if (!window.confirm(`Delete SFTP connector "${name}"?`)) return
    try {
      await deleteSFTPConnector(name)
      load()
    } catch (e) {
      setPageError(String(e))
    }
  }

  if (loading) return <div style={{ padding: '20px 24px', color: '#a6adc8', fontSize: 13 }}>Loading…</div>
  if (pageError) return <div style={{ padding: '20px 24px', color: '#f38ba8', fontSize: 13 }}>Error: {pageError}</div>

  return (
    <div style={{ padding: '20px 24px', height: '100%', overflowY: 'auto' }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>SFTP Connectors</h2>
          <p style={{ fontSize: 12, color: '#6c7086' }}>
            Defined in <code>gateway.yaml</code> → <code>sftp_connectors</code>. Restart gateway to pick up changes.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button style={btnPrimary} onClick={openAdd}>+ Add SFTP Connector</button>
          <button
            style={btnSecondary}
            onClick={() => setShowSnippet(v => !v)}
          >{showSnippet ? 'Hide config' : 'How to configure'}</button>
        </div>
      </div>

      {/* YAML snippet */}
      {showSnippet && (
        <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 14, marginBottom: 16 }}>
          <div style={{ fontSize: 11, color: '#6c7086', marginBottom: 8 }}>Add to gateway.yaml, then restart the gateway:</div>
          <pre style={{ margin: 0, fontSize: 12, color: '#cdd6f4', whiteSpace: 'pre', overflowX: 'auto' }}>{YAML_SNIPPET}</pre>
          <div style={{ fontSize: 11, color: '#6c7086', marginTop: 8 }}>
            Use <code>env:VAR_NAME</code> refs for secrets. Steps: <code>sftp_get</code>, <code>sftp_put</code>, <code>sftp_delete</code>, <code>sftp_list</code>.
          </div>
        </div>
      )}

      {/* Inline form */}
      {formMode !== null && (
        <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 16, marginBottom: 16 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: '#cdd6f4', marginBottom: 12 }}>
            {formMode === 'add' ? 'New SFTP Connector' : `Edit: ${formMode}`}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
            {/* Name */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Name *</label>
              <input
                style={{ ...inputStyle, opacity: formMode !== 'add' ? 0.5 : 1 }}
                value={form.name}
                disabled={formMode !== 'add'}
                onChange={e => set('name', e.target.value)}
                placeholder="my-sftp"
              />
            </div>
            {/* Host */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Host *</label>
              <input
                style={inputStyle}
                value={form.host}
                onChange={e => set('host', e.target.value)}
                placeholder="sftp.example.com"
              />
            </div>
            {/* Port */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Port</label>
              <input
                style={inputStyle} type="number"
                value={form.port ?? ''}
                onChange={e => set('port', e.target.value === '' ? undefined : Number(e.target.value))}
                placeholder="22"
              />
            </div>
            {/* Username */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Username *</label>
              <input
                style={inputStyle}
                value={form.username}
                onChange={e => set('username', e.target.value)}
                placeholder="deploy"
              />
            </div>
            {/* Password Ref */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Password Ref (optional)</label>
              <input
                style={inputStyle}
                value={form.password_ref ?? ''}
                onChange={e => set('password_ref', e.target.value)}
                placeholder="env:MY_SFTP_PASS"
              />
            </div>
            {/* Private Key Ref */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Private Key Ref (optional)</label>
              <input
                style={inputStyle}
                value={form.private_key_ref ?? ''}
                onChange={e => set('private_key_ref', e.target.value)}
                placeholder="env:MY_SFTP_KEY"
              />
            </div>
            {/* Known Hosts Ref */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Known Hosts Ref (optional)</label>
              <input
                style={inputStyle}
                value={form.known_hosts_ref ?? ''}
                onChange={e => set('known_hosts_ref', e.target.value)}
                placeholder="env:SFTP_KNOWN_HOSTS"
              />
            </div>
            {/* Timeout MS */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Timeout MS (optional)</label>
              <input
                style={inputStyle} type="number"
                value={form.timeout_ms ?? ''}
                onChange={e => set('timeout_ms', e.target.value === '' ? undefined : Number(e.target.value))}
                placeholder="5000"
              />
            </div>
          </div>

          {formError && (
            <div style={{ fontSize: 12, color: '#f38ba8', marginBottom: 10 }}>{formError}</div>
          )}

          <div style={{ display: 'flex', gap: 8 }}>
            <button style={btnPrimary} onClick={handleSave} disabled={saving}>
              {saving ? 'Saving…' : 'Save'}
            </button>
            <button style={btnSecondary} onClick={closeForm} disabled={saving}>Cancel</button>
          </div>
        </div>
      )}

      {/* List */}
      {connectors.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>
          No SFTP connectors configured.{' '}
          <span style={{ cursor: 'pointer', textDecoration: 'underline', color: '#89b4fa' }} onClick={openAdd}>
            Add one now.
          </span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {connectors.map(c => {
            const hostPort = c.port ? `${c.host}:${c.port}` : c.host
            return (
              <div key={c.name} style={{ background: '#1e1e2e', border: '1px solid #313244', borderRadius: 8, padding: '10px 14px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>{c.name}</span>
                  <span style={{ fontSize: 11, color: '#89b4fa', background: 'rgba(137,180,250,0.1)', padding: '2px 8px', borderRadius: 4 }}>{hostPort}</span>
                  <span style={{ fontSize: 11, color: '#a6adc8' }}>{c.username}</span>
                </div>
                <div style={{ display: 'flex', gap: 8 }}>
                  <button style={btnEdit} onClick={() => openEdit(c)}>Edit</button>
                  <button style={btnDanger} onClick={() => handleDelete(c.name)}>Delete</button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
