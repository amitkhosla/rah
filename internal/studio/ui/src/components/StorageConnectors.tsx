import { useEffect, useState } from 'react'
import {
  listGWStorageProviders,
  addGWStorageProvider,
  updateGWStorageProvider,
  deleteGWStorageProvider,
} from '../api'
import type { StorageProviderConfig } from '../api'

const YAML_SNIPPET = `storage_providers:
  - name: my-s3
    type: s3
    bucket_ref: "env:S3_BUCKET"
    region: us-east-1
    access_key_ref: "env:AWS_ACCESS_KEY_ID"
    secret_key_ref: "env:AWS_SECRET_ACCESS_KEY"

  - name: my-gcs
    type: gcs
    bucket_ref: "env:GCS_BUCKET"
    project_id: my-gcp-project
    credential_ref: "env:GOOGLE_APPLICATION_CREDENTIALS"

  - name: my-local
    type: local
    root_dir: ./rah-assets`

const TYPE_COLORS: Record<string, { bg: string; text: string }> = {
  s3:    { bg: 'rgba(250,179,135,0.1)', text: '#fab387' },
  gcs:   { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' },
  local: { bg: 'rgba(166,227,161,0.1)', text: '#a6e3a1' },
}

const EMPTY_FORM: StorageProviderConfig = {
  name: '',
  type: 's3',
  bucket_ref: '',
  region: '',
  access_key_ref: '',
  secret_key_ref: '',
  endpoint_url: '',
  project_id: '',
  credential_ref: '',
  root_dir: '',
}

// ── small helpers ─────────────────────────────────────────────────────────────

const inputStyle: React.CSSProperties = {
  width: '100%',
  background: '#181825',
  border: '1px solid #313244',
  borderRadius: 6,
  color: '#cdd6f4',
  fontSize: 12,
  padding: '5px 8px',
  boxSizing: 'border-box',
  outline: 'none',
}

const labelStyle: React.CSSProperties = {
  fontSize: 11,
  color: '#a6adc8',
  marginBottom: 3,
  display: 'block',
}

function Field({
  label,
  value,
  onChange,
  disabled,
  placeholder,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  disabled?: boolean
  placeholder?: string
}) {
  return (
    <div>
      <label style={labelStyle}>{label}</label>
      <input
        style={{ ...inputStyle, opacity: disabled ? 0.5 : 1, cursor: disabled ? 'not-allowed' : 'text' }}
        value={value}
        disabled={disabled}
        placeholder={placeholder}
        onChange={e => onChange(e.target.value)}
      />
    </div>
  )
}

// ── main component ────────────────────────────────────────────────────────────

export default function StorageConnectors() {
  const [providers, setProviders]     = useState<StorageProviderConfig[]>([])
  const [loading, setLoading]         = useState(true)
  const [listError, setListError]     = useState<string | null>(null)
  const [showSnippet, setShowSnippet] = useState(false)

  // form state
  const [formOpen, setFormOpen]       = useState(false)
  const [editingName, setEditingName] = useState<string | null>(null)  // null = adding new
  const [form, setForm]               = useState<StorageProviderConfig>(EMPTY_FORM)
  const [saving, setSaving]           = useState(false)
  const [formError, setFormError]     = useState<string | null>(null)

  function load() {
    setLoading(true)
    setListError(null)
    listGWStorageProviders()
      .then(r => setProviders(r.providers ?? []))
      .catch(e => setListError(String(e)))
      .finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [])

  function openAdd() {
    setForm(EMPTY_FORM)
    setEditingName(null)
    setFormError(null)
    setFormOpen(true)
  }

  function openEdit(p: StorageProviderConfig) {
    setForm({ ...EMPTY_FORM, ...p })
    setEditingName(p.name)
    setFormError(null)
    setFormOpen(true)
  }

  function closeForm() {
    setFormOpen(false)
    setFormError(null)
  }

  function patchForm(patch: Partial<StorageProviderConfig>) {
    setForm(f => ({ ...f, ...patch }))
  }

  async function handleSave() {
    if (!form.name.trim()) { setFormError('Name is required.'); return }
    if (!form.type)        { setFormError('Type is required.'); return }
    setSaving(true)
    setFormError(null)
    try {
      if (editingName !== null) {
        await updateGWStorageProvider(editingName, form)
      } else {
        await addGWStorageProvider(form)
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
    if (!window.confirm(`Delete storage provider "${name}"?`)) return
    try {
      await deleteGWStorageProvider(name)
      load()
    } catch (e) {
      setListError(String(e))
    }
  }

  // ── render ──────────────────────────────────────────────────────────────────

  return (
    <div style={{ padding: '20px 24px', height: '100%', overflowY: 'auto' }}>

      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Storage Connectors (Gateway)
          </h2>
          <p style={{ fontSize: 12, color: '#6c7086' }}>
            Used in <code>storage_get</code> / <code>storage_put</code> flow steps. Configured via <code>/api/gateway/storage-providers</code>.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <button
            style={{ fontSize: 12, padding: '5px 12px', border: '1px solid #313244', borderRadius: 6, background: '#1e1e2e', color: '#cdd6f4', cursor: 'pointer' }}
            onClick={() => setShowSnippet(v => !v)}
          >
            {showSnippet ? 'Hide config' : 'How to configure'}
          </button>
          <button
            style={{ fontSize: 12, padding: '5px 12px', border: '1px solid #89b4fa', borderRadius: 6, background: 'rgba(137,180,250,0.08)', color: '#89b4fa', cursor: 'pointer', fontWeight: 600 }}
            onClick={openAdd}
          >
            + Add Provider
          </button>
        </div>
      </div>

      {/* YAML snippet */}
      {showSnippet && (
        <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 14, marginBottom: 16 }}>
          <div style={{ fontSize: 11, color: '#6c7086', marginBottom: 8 }}>
            Add to <code>gateway.yaml</code>, then restart the gateway:
          </div>
          <pre style={{ margin: 0, fontSize: 12, color: '#cdd6f4', whiteSpace: 'pre', overflowX: 'auto' }}>{YAML_SNIPPET}</pre>
          <div style={{ fontSize: 11, color: '#6c7086', marginTop: 8 }}>
            Supported types: <code>s3</code>, <code>gcs</code>, <code>local</code>. Use <code>env:VAR</code> refs for secrets.
          </div>
        </div>
      )}

      {/* Inline add/edit form */}
      {formOpen && (
        <div style={{ background: '#1e1e2e', border: '1px solid #45475a', borderRadius: 8, padding: 16, marginBottom: 16 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: '#cdd6f4', marginBottom: 12 }}>
            {editingName !== null ? `Edit — ${editingName}` : 'New Storage Provider'}
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
            <Field
              label="Name *"
              value={form.name}
              onChange={v => patchForm({ name: v })}
              disabled={editingName !== null}
              placeholder="my-s3"
            />
            <div>
              <label style={labelStyle}>Type *</label>
              <select
                style={{ ...inputStyle }}
                value={form.type}
                onChange={e => patchForm({ type: e.target.value })}
              >
                <option value="s3">s3</option>
                <option value="gcs">gcs</option>
                <option value="local">local</option>
              </select>
            </div>
          </div>

          {/* S3 fields */}
          {form.type === 's3' && (
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
              <Field label="Bucket Ref"      value={form.bucket_ref ?? ''}     onChange={v => patchForm({ bucket_ref: v })}     placeholder="env:S3_BUCKET" />
              <Field label="Region"          value={form.region ?? ''}          onChange={v => patchForm({ region: v })}          placeholder="us-east-1" />
              <Field label="Access Key Ref"  value={form.access_key_ref ?? ''}  onChange={v => patchForm({ access_key_ref: v })}  placeholder="env:AWS_ACCESS_KEY_ID" />
              <Field label="Secret Key Ref"  value={form.secret_key_ref ?? ''}  onChange={v => patchForm({ secret_key_ref: v })}  placeholder="env:AWS_SECRET_ACCESS_KEY" />
              <div style={{ gridColumn: '1 / -1' }}>
                <Field label="Endpoint URL (optional)" value={form.endpoint_url ?? ''} onChange={v => patchForm({ endpoint_url: v })} placeholder="https://s3.example.com" />
              </div>
            </div>
          )}

          {/* GCS fields */}
          {form.type === 'gcs' && (
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
              <Field label="Bucket Ref"      value={form.bucket_ref ?? ''}    onChange={v => patchForm({ bucket_ref: v })}    placeholder="env:GCS_BUCKET" />
              <Field label="Project ID"      value={form.project_id ?? ''}    onChange={v => patchForm({ project_id: v })}    placeholder="my-gcp-project" />
              <div style={{ gridColumn: '1 / -1' }}>
                <Field label="Credential Ref" value={form.credential_ref ?? ''} onChange={v => patchForm({ credential_ref: v })} placeholder="env:GOOGLE_APPLICATION_CREDENTIALS" />
              </div>
            </div>
          )}

          {/* Local fields */}
          {form.type === 'local' && (
            <div style={{ marginBottom: 10 }}>
              <Field label="Root Dir" value={form.root_dir ?? ''} onChange={v => patchForm({ root_dir: v })} placeholder="./rah-assets" />
            </div>
          )}

          {formError && (
            <div style={{ fontSize: 12, color: '#f38ba8', marginBottom: 10 }}>{formError}</div>
          )}

          <div style={{ display: 'flex', gap: 8 }}>
            <button
              style={{ fontSize: 12, padding: '5px 14px', border: 'none', borderRadius: 6, background: '#89b4fa', color: '#1e1e2e', cursor: saving ? 'not-allowed' : 'pointer', fontWeight: 600, opacity: saving ? 0.6 : 1 }}
              onClick={handleSave}
              disabled={saving}
            >
              {saving ? 'Saving…' : editingName !== null ? 'Update' : 'Create'}
            </button>
            <button
              style={{ fontSize: 12, padding: '5px 14px', border: '1px solid #313244', borderRadius: 6, background: 'transparent', color: '#a6adc8', cursor: 'pointer' }}
              onClick={closeForm}
              disabled={saving}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* List error (load / delete errors) */}
      {listError && (
        <div style={{ fontSize: 12, color: '#f38ba8', marginBottom: 12 }}>Error: {listError}</div>
      )}

      {/* Provider list */}
      {loading ? (
        <div style={{ color: '#a6adc8', fontSize: 13 }}>Loading…</div>
      ) : providers.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>
          No storage providers configured.{' '}
          <span
            style={{ cursor: 'pointer', textDecoration: 'underline', color: '#89b4fa' }}
            onClick={openAdd}
          >
            Add one now.
          </span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {providers.map(p => {
            const colors = TYPE_COLORS[p.type] ?? { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' }
            return (
              <div
                key={p.name}
                style={{
                  background: '#1e1e2e',
                  border: '1px solid #313244',
                  borderRadius: 8,
                  padding: '10px 14px',
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>{p.name}</span>
                  <span style={{ fontSize: 11, color: colors.text, background: colors.bg, padding: '2px 8px', borderRadius: 4 }}>
                    {p.type}
                  </span>
                </div>
                <div style={{ display: 'flex', gap: 6 }}>
                  <button
                    style={{ fontSize: 11, padding: '3px 10px', border: '1px solid #313244', borderRadius: 5, background: 'transparent', color: '#a6adc8', cursor: 'pointer' }}
                    onClick={() => openEdit(p)}
                  >
                    Edit
                  </button>
                  <button
                    style={{ fontSize: 11, padding: '3px 10px', border: '1px solid rgba(243,139,168,0.3)', borderRadius: 5, background: 'rgba(243,139,168,0.06)', color: '#f38ba8', cursor: 'pointer' }}
                    onClick={() => handleDelete(p.name)}
                  >
                    Delete
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
