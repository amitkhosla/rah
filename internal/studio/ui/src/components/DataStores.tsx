import { useEffect, useState } from 'react'
import { listSQLDataSources, addSQLDataSource, updateSQLDataSource, deleteSQLDataSource } from '../api'
import type { SQLDataSourceDef } from '../api'

const YAML_SNIPPET = `data_sources:
  - name: my-postgres
    driver: postgres
    dsn_ref: "env:DATABASE_URL"
    max_connections: 10
    query_timeout_sec: 30

  - name: my-mysql
    driver: mysql
    dsn_ref: "env:MYSQL_DSN"
    max_connections: 10`

const DRIVER_COLORS: Record<string, { bg: string; text: string }> = {
  postgres: { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' },
  mysql:    { bg: 'rgba(250,179,135,0.1)', text: '#fab387' },
}

const DSN_SCHEMES = [
  { value: 'env',   label: 'Env variable',           placeholder: 'DATABASE_URL' },
  { value: 'gsm',   label: 'Google Secret Manager',  placeholder: 'projects/my-project/secrets/db-dsn' },
  { value: 'awssm', label: 'AWS Secrets Manager',    placeholder: 'my-secret-name' },
  { value: 'vault', label: 'HashiCorp Vault',         placeholder: 'secret/data/db/dsn' },
]

function parseDsnRef(dsnRef: string | undefined): { scheme: string; value: string } {
  if (!dsnRef) return { scheme: 'env', value: '' }
  const colonIdx = dsnRef.indexOf(':')
  if (colonIdx > 0) {
    return { scheme: dsnRef.slice(0, colonIdx), value: dsnRef.slice(colonIdx + 1) }
  }
  return { scheme: 'env', value: dsnRef }
}

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

const EMPTY_FORM: SQLDataSourceDef = {
  name: '',
  driver: 'postgres',
  dsn_ref: '',
  max_connections: undefined,
  query_timeout_sec: undefined,
}

interface FormProps {
  initial: SQLDataSourceDef
  isEdit: boolean
  onSave: (cfg: SQLDataSourceDef) => Promise<void>
  onCancel: () => void
}

function SQLDataSourceForm({ initial, isEdit, onSave, onCancel }: FormProps) {
  const [form, setForm]         = useState<SQLDataSourceDef>({ ...initial })
  const [saving, setSaving]     = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [showAdvanced, setShowAdvanced] = useState(false)

  // DSN scheme fields
  const parsedDsn = parseDsnRef(form.dsn_ref)
  const [dsnScheme, setDsnScheme] = useState<string>(parsedDsn.scheme)
  const [dsnValue, setDsnValue] = useState<string>(parsedDsn.value)
  const [dsnChanged, setDsnChanged] = useState<boolean>(!isEdit)

  function set<K extends keyof SQLDataSourceDef>(key: K, value: SQLDataSourceDef[K]) {
    setForm(f => ({ ...f, [key]: value }))
  }

  async function handleSave() {
    if (!form.name.trim())   { setFormError('Name is required.');   return }
    if (!form.driver.trim()) { setFormError('Driver is required.'); return }
    setSaving(true)
    setFormError(null)
    try {
      const payload: SQLDataSourceDef = { name: form.name.trim(), driver: form.driver.trim() }

      // Handle DSN ref: if changed, construct from scheme and value; if not changed, omit (preserves stored value)
      if (dsnChanged) {
        if (dsnValue.trim()) {
          payload.dsn_ref = `${dsnScheme}:${dsnValue.trim()}`
        }
      }

      if (form.max_connections && form.max_connections > 0) payload.max_connections = form.max_connections
      if (form.query_timeout_sec && form.query_timeout_sec > 0) payload.query_timeout_sec = form.query_timeout_sec
      if (form.tenant_isolation?.trim()) payload.tenant_isolation = form.tenant_isolation.trim()
      if (form.tenant_key?.trim())       payload.tenant_key       = form.tenant_key.trim()
      if (form.rls_variable?.trim())     payload.rls_variable     = form.rls_variable.trim()
      await onSave(payload)
    } catch (e) {
      setFormError(String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 20, marginTop: 16 }}>
      <h3 style={{ fontSize: 13, fontWeight: 700, color: '#cdd6f4', marginBottom: 16 }}>
        {isEdit ? `Edit — ${initial.name}` : 'Add SQL Data Source'}
      </h3>

      {formError && <div style={{ color: '#f38ba8', fontSize: 12, marginBottom: 12 }}>{formError}</div>}

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 16px' }}>
        {/* Name */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Name *</label>
          <input
            style={{ ...inputStyle, opacity: isEdit ? 0.5 : 1 }}
            value={form.name}
            disabled={isEdit}
            placeholder="my-postgres"
            onChange={e => set('name', e.target.value)}
          />
        </div>

        {/* Driver */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Driver *</label>
          <select
            style={{ ...inputStyle, cursor: 'pointer' }}
            value={form.driver}
            onChange={e => set('driver', e.target.value)}
          >
            <option value="postgres">postgres</option>
            <option value="mysql">mysql</option>
          </select>
        </div>

        {/* DSN Ref */}
        <div style={{ ...fieldWrap, gridColumn: '1 / -1' }}>
          <label style={labelStyle}>Connection String</label>

          {/* If edit mode and not changed, show masked value */}
          {isEdit && !dsnChanged && (
            <div>
              <div style={{ ...inputStyle, display: 'flex', alignItems: 'center', justifyContent: 'space-between', cursor: 'default', opacity: 0.7 }}>
                <span style={{ color: '#6c7086' }}>
                  {DSN_SCHEMES.find(s => s.value === parsedDsn.scheme)?.label || 'Unknown'} — ***
                </span>
              </div>
              <div style={{ fontSize: 11, color: '#6c7086', marginTop: 4, marginBottom: 8 }}>
                Connection string stored securely
              </div>
              <button
                type="button"
                style={{ fontSize: 11, color: '#89b4fa', background: 'none', border: '1px solid #89b4fa', borderRadius: 4, padding: '4px 8px', cursor: 'pointer' }}
                onClick={() => { setDsnChanged(true); setDsnValue(""); }}
              >
                Change connection string
              </button>
            </div>
          )}

          {/* When dsnChanged is true, show scheme dropdown + ref input */}
          {dsnChanged && (
            <div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 2fr', gap: 12, marginBottom: 8 }}>
                <div>
                  <select
                    style={{ ...inputStyle, cursor: 'pointer' }}
                    value={dsnScheme}
                    onChange={e => setDsnScheme(e.target.value)}
                  >
                    {DSN_SCHEMES.map(s => (
                      <option key={s.value} value={s.value}>{s.label}</option>
                    ))}
                  </select>
                </div>
                <div>
                  <input
                    style={inputStyle}
                    type="password"
                    value={dsnValue}
                    placeholder={DSN_SCHEMES.find(s => s.value === dsnScheme)?.placeholder}
                    onChange={e => setDsnValue(e.target.value)}
                  />
                </div>
              </div>
              <div style={{ fontSize: 11, color: '#6c7086', marginTop: 4 }}>
                Use env:VAR_NAME for environment variables, gsm:projects/x/secrets/name for Google Secret Manager,
                awssm:secret-name for AWS Secrets Manager, or vault:secret/path for HashiCorp Vault.
              </div>
            </div>
          )}
        </div>

        {/* Max Connections */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Max Connections</label>
          <input
            style={inputStyle}
            type="number"
            min={1}
            value={form.max_connections ?? ''}
            placeholder="10"
            onChange={e => set('max_connections', e.target.value ? Number(e.target.value) : undefined)}
          />
        </div>

        {/* Query Timeout */}
        <div style={fieldWrap}>
          <label style={labelStyle}>Query Timeout (sec)</label>
          <input
            style={inputStyle}
            type="number"
            min={1}
            value={form.query_timeout_sec ?? ''}
            placeholder="30"
            onChange={e => set('query_timeout_sec', e.target.value ? Number(e.target.value) : undefined)}
          />
        </div>
      </div>

      {/* Advanced */}
      <button
        style={{ fontSize: 11, color: '#6c7086', background: 'none', border: 'none', cursor: 'pointer', padding: '4px 0', marginBottom: showAdvanced ? 12 : 0 }}
        onClick={() => setShowAdvanced(v => !v)}
      >
        {showAdvanced ? '▾ Hide advanced' : '▸ Show advanced (tenant isolation)'}
      </button>

      {showAdvanced && (
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 16px' }}>
          <div style={fieldWrap}>
            <label style={labelStyle}>Tenant Isolation</label>
            <select
              style={{ ...inputStyle, cursor: 'pointer' }}
              value={form.tenant_isolation ?? ''}
              onChange={e => set('tenant_isolation', e.target.value || undefined)}
            >
              <option value="">none</option>
              <option value="schema">schema</option>
              <option value="rls">rls</option>
            </select>
          </div>
          <div style={fieldWrap}>
            <label style={labelStyle}>Tenant Key</label>
            <input
              style={inputStyle}
              value={form.tenant_key ?? ''}
              placeholder="tenant_id"
              onChange={e => set('tenant_key', e.target.value)}
            />
          </div>
          <div style={fieldWrap}>
            <label style={labelStyle}>RLS Variable</label>
            <input
              style={inputStyle}
              value={form.rls_variable ?? ''}
              placeholder="app.current_tenant"
              onChange={e => set('rls_variable', e.target.value)}
            />
          </div>
        </div>
      )}

      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <button
          style={{ fontSize: 12, padding: '6px 16px', border: 'none', borderRadius: 6, background: '#89b4fa', color: '#1e1e2e', cursor: saving ? 'not-allowed' : 'pointer', fontWeight: 600, opacity: saving ? 0.7 : 1 }}
          disabled={saving}
          onClick={handleSave}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button
          style={{ fontSize: 12, padding: '6px 14px', border: '1px solid #313244', borderRadius: 6, background: '#1e1e2e', color: '#cdd6f4', cursor: 'pointer' }}
          disabled={saving}
          onClick={onCancel}
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

type FormMode = { mode: 'add' } | { mode: 'edit'; source: SQLDataSourceDef } | { mode: 'none' }

export default function DataStores() {
  const [sources, setSources]     = useState<SQLDataSourceDef[]>([])
  const [loading, setLoading]     = useState(true)
  const [error, setError]         = useState<string | null>(null)
  const [formMode, setFormMode]   = useState<FormMode>({ mode: 'none' })
  const [actionError, setActionError] = useState<string | null>(null)
  const [showSnippet, setShowSnippet] = useState(false)

  function load() {
    setLoading(true)
    setError(null)
    listSQLDataSources()
      .then(r => setSources(r.sources ?? []))
      .catch(e => setError(String(e)))
      .finally(() => setLoading(false))
  }

  useEffect(load, [])

  async function handleSave(cfg: SQLDataSourceDef) {
    setActionError(null)
    if (formMode.mode === 'add') {
      await addSQLDataSource(cfg)
    } else if (formMode.mode === 'edit') {
      await updateSQLDataSource(formMode.source.name, cfg)
    }
    setFormMode({ mode: 'none' })
    load()
  }

  async function handleDelete(name: string) {
    if (!window.confirm(`Delete SQL data source "${name}"? This cannot be undone.`)) return
    setActionError(null)
    try {
      await deleteSQLDataSource(name)
      setSources(prev => prev.filter(s => s.name !== name))
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
          <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>SQL Data Sources</h2>
          <p style={{ fontSize: 12, color: '#6c7086' }}>
            Named SQL connections used in <code>db_query</code> / <code>db_exec</code> / <code>db_one</code> flow steps.
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
            style={{ fontSize: 12, padding: '5px 12px', border: 'none', borderRadius: 6, background: '#89b4fa', color: '#1e1e2e', cursor: 'pointer', fontWeight: 600, opacity: showForm ? 0.5 : 1 }}
            disabled={showForm}
            onClick={() => { setFormMode({ mode: 'add' }); setActionError(null) }}
          >
            + Add Data Source
          </button>
        </div>
      </div>

      {/* YAML snippet */}
      {showSnippet && (
        <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 14, marginBottom: 16 }}>
          <div style={{ fontSize: 11, color: '#6c7086', marginBottom: 8 }}>Add to gateway.yaml:</div>
          <pre style={{ margin: 0, fontSize: 12, color: '#cdd6f4', whiteSpace: 'pre', overflowX: 'auto' }}>{YAML_SNIPPET}</pre>
          <div style={{ fontSize: 11, color: '#6c7086', marginTop: 8 }}>
            Supported drivers: <code>postgres</code>, <code>mysql</code>.
            Use <code>dsn_ref: "env:VAR"</code> to load the connection string from an environment variable.
          </div>
        </div>
      )}

      {actionError && <div style={{ color: '#f38ba8', fontSize: 12, marginBottom: 12 }}>{actionError}</div>}

      {/* Source list */}
      {sources.length === 0 && !showForm ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>
          No SQL data sources configured.{' '}
          <span style={{ cursor: 'pointer', textDecoration: 'underline', color: '#89b4fa' }} onClick={() => setShowSnippet(true)}>
            See how to add one.
          </span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {sources.map(src => {
            const colors       = DRIVER_COLORS[src.driver] ?? { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' }
            const isBeingEdited = formMode.mode === 'edit' && formMode.source.name === src.name

            return (
              <div
                key={src.name}
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
                  <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>{src.name}</span>
                  <span style={{ fontSize: 11, color: colors.text, background: colors.bg, padding: '2px 8px', borderRadius: 4 }}>
                    {src.driver}
                  </span>
                  {src.dsn_ref && (
                    <span style={{ fontSize: 11, color: '#6c7086', fontFamily: 'monospace' }}>{src.dsn_ref}</span>
                  )}
                  {src.tenant_isolation && (
                    <span style={{ fontSize: 11, color: '#6c7086' }}>isolation: {src.tenant_isolation}</span>
                  )}
                </div>
                <div style={{ display: 'flex', gap: 6 }}>
                  <button
                    title="Edit"
                    style={{ background: 'none', border: '1px solid #313244', borderRadius: 5, color: '#89b4fa', cursor: 'pointer', padding: '4px 7px', lineHeight: 1, display: 'flex', alignItems: 'center' }}
                    onClick={() => { setFormMode({ mode: 'edit', source: src }); setActionError(null) }}
                  >
                    <PencilIcon />
                  </button>
                  <button
                    title="Delete"
                    style={{ background: 'none', border: '1px solid #313244', borderRadius: 5, color: '#f38ba8', cursor: 'pointer', padding: '4px 7px', lineHeight: 1, display: 'flex', alignItems: 'center' }}
                    onClick={() => handleDelete(src.name)}
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
        <SQLDataSourceForm
          initial={EMPTY_FORM}
          isEdit={false}
          onSave={handleSave}
          onCancel={() => setFormMode({ mode: 'none' })}
        />
      )}
      {formMode.mode === 'edit' && (
        <SQLDataSourceForm
          initial={formMode.source}
          isEdit={true}
          onSave={handleSave}
          onCancel={() => setFormMode({ mode: 'none' })}
        />
      )}
    </div>
  )
}
