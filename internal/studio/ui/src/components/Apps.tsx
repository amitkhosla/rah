import { useEffect, useState, useCallback } from 'react'
import {
  listApps, createApp, updateApp, deleteApp,
  listKeys, generateKey, updateKey, revokeKey, rotateKey,
  generateAppBlueprint, type AppBlueprintRequest, type AppBlueprintResponse,
} from '../api'
import type { App, APIKeyView, APIKeyCreateResponse } from '../types'

// ── helpers ──────────────────────────────────────────────────────────────────

function fmtDate(ts: number): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString()
}

// ── Blueprint wizard modal ────────────────────────────────────────────────────

function BlueprintWizard({ appName, onClose }: { appName: string; onClose: () => void }) {
  const [appType, setAppType] = useState<'web' | 'api-service' | 'event-processor' | 'webhook'>('web')
  const [tenantMode, setTenantMode] = useState<'tenant_aware' | 'tenant_agnostic'>('tenant_aware')
  const [oauthProvider, setOauthProvider] = useState('')
  const [loginPath, setLoginPath] = useState('/login')
  const [callbackPath, setCallbackPath] = useState('/oauth/callback')
  const [logoutPath, setLogoutPath] = useState('/logout')
  const [generating, setGenerating] = useState(false)
  const [err, setErr] = useState('')
  const [result, setResult] = useState<AppBlueprintResponse | null>(null)
  const [copiedFlowIndex, setCopiedFlowIndex] = useState(-1)

  async function handleGenerate() {
    setGenerating(true); setErr('')
    try {
      const req: AppBlueprintRequest = {
        type: appType,
        tenant_mode: tenantMode,
      }
      if (appType === 'web') {
        req.oauth_provider = oauthProvider.trim() || undefined
        if (loginPath.trim()) req.login_path = loginPath.trim()
        if (callbackPath.trim()) req.callback_path = callbackPath.trim()
        if (logoutPath.trim()) req.logout_path = logoutPath.trim()
      }
      const resp = await generateAppBlueprint(appName, req)
      setResult(resp)
    } catch (e) { setErr(String(e)) }
    finally { setGenerating(false) }
  }

  function copyFlowYaml(index: number) {
    const flow = result?.flows[index]
    if (!flow) return
    navigator.clipboard.writeText(flow.yaml).then(() => {
      setCopiedFlowIndex(index)
      setTimeout(() => setCopiedFlowIndex(-1), 2000)
    })
  }

  return (
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
      display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
    }}>
      <div style={{
        background: 'var(--block-bg)', border: '1px solid var(--border)',
        borderRadius: 8, padding: 24, width: 560, maxWidth: '90vw', maxHeight: '85vh', overflowY: 'auto',
      }}>
        <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 16 }}>App Blueprint: {appName}</div>

        {!result ? (
          <>
            <div style={{ marginBottom: 14 }}>
              <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 6, fontWeight: 600 }}>App Type</label>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {(['web', 'api-service', 'event-processor', 'webhook'] as const).map(t => (
                  <label key={t} style={{ display: 'flex', alignItems: 'center', fontSize: 13, cursor: 'pointer', gap: 4 }}>
                    <input type="radio" name="appType" value={t} checked={appType === t} onChange={e => setAppType(e.target.value as typeof t)} />
                    {t}
                  </label>
                ))}
              </div>
            </div>

            <div style={{ marginBottom: 14 }}>
              <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 6, fontWeight: 600 }}>Tenant Mode</label>
              <div style={{ display: 'flex', gap: 8 }}>
                {(['tenant_aware', 'tenant_agnostic'] as const).map(t => (
                  <label key={t} style={{ display: 'flex', alignItems: 'center', fontSize: 13, cursor: 'pointer', gap: 4 }}>
                    <input type="radio" name="tenantMode" value={t} checked={tenantMode === t} onChange={e => setTenantMode(e.target.value as typeof t)} />
                    {t}
                  </label>
                ))}
              </div>
            </div>

            {appType === 'web' && (
              <>
                <div style={{ marginBottom: 10 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>OAuth Provider (optional)</label>
                  <input
                    className="input"
                    style={{ width: '100%', boxSizing: 'border-box' }}
                    placeholder="e.g. google, github, okta"
                    value={oauthProvider}
                    onChange={e => { setOauthProvider(e.target.value); setErr('') }}
                  />
                </div>

                <div style={{ marginBottom: 10 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Login Path</label>
                  <input
                    className="input"
                    style={{ width: '100%', boxSizing: 'border-box' }}
                    value={loginPath}
                    onChange={e => { setLoginPath(e.target.value); setErr('') }}
                  />
                </div>

                <div style={{ marginBottom: 10 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Callback Path</label>
                  <input
                    className="input"
                    style={{ width: '100%', boxSizing: 'border-box' }}
                    value={callbackPath}
                    onChange={e => { setCallbackPath(e.target.value); setErr('') }}
                  />
                </div>

                <div style={{ marginBottom: 14 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Logout Path</label>
                  <input
                    className="input"
                    style={{ width: '100%', boxSizing: 'border-box' }}
                    value={logoutPath}
                    onChange={e => { setLogoutPath(e.target.value); setErr('') }}
                  />
                </div>
              </>
            )}

            {err && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 12 }}>{err}</div>}

            <div style={{ display: 'flex', gap: 8 }}>
              <button className="btn" onClick={handleGenerate} disabled={generating} style={{ flex: 1 }}>
                {generating ? 'Generating...' : 'Generate'}
              </button>
              <button className="btn" onClick={onClose} style={{ flex: 1 }}>Cancel</button>
            </div>
          </>
        ) : (
          <>
            <div style={{ fontSize: 13, color: 'var(--text-muted)', marginBottom: 12 }}>Generated {result.flows.length} flow(s):</div>
            {result.flows.map((flow, idx) => (
              <div key={idx} style={{
                background: 'var(--sidebar-bg, #0f1117)', border: '1px solid var(--border)',
                borderRadius: 6, padding: 12, marginBottom: 12,
              }}>
                <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 8, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <span>{flow.name}</span>
                  <button
                    className="btn"
                    style={{ fontSize: 11 }}
                    onClick={() => copyFlowYaml(idx)}
                  >
                    {copiedFlowIndex === idx ? 'Copied!' : 'Copy YAML'}
                  </button>
                </div>
                <pre style={{
                  fontFamily: 'monospace', fontSize: 11,
                  background: 'var(--block-bg)', border: '1px solid var(--border)',
                  borderRadius: 4, padding: '8px 10px', overflow: 'auto', maxHeight: 200,
                  margin: 0, color: 'inherit', userSelect: 'all',
                }}>
                  {flow.yaml}
                </pre>
              </div>
            ))}

            <div style={{ display: 'flex', gap: 8 }}>
              <button className="btn" onClick={() => setResult(null)} style={{ flex: 1 }}>Back</button>
              <button className="btn" onClick={onClose} style={{ flex: 1 }}>Close</button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// ── Raw-key modal ─────────────────────────────────────────────────────────────

function RawKeyModal({ keyData, onClose }: { keyData: APIKeyCreateResponse; onClose: () => void }) {
  const [copied, setCopied] = useState(false)

  function handleCopy() {
    navigator.clipboard.writeText(keyData.key).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  return (
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
      display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
    }}>
      <div style={{
        background: 'var(--block-bg)', border: '1px solid var(--border)',
        borderRadius: 8, padding: 24, width: 480, maxWidth: '90vw',
      }}>
        <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 8 }}>API Key Generated</div>
        <div style={{
          background: '#ef4444', color: '#fff', borderRadius: 4,
          padding: '6px 10px', fontSize: 12, marginBottom: 16,
        }}>
          This key will not be shown again. Copy it now.
        </div>

        <div style={{ marginBottom: 8, fontSize: 12, color: 'var(--text-muted)' }}>
          Alias: <strong style={{ color: 'inherit' }}>{keyData.alias}</strong>
          &nbsp;&nbsp;Prefix: <strong style={{ color: 'inherit', fontFamily: 'monospace' }}>{keyData.prefix}</strong>
        </div>

        <div style={{
          fontFamily: 'monospace', fontSize: 13,
          background: 'var(--sidebar-bg, #0f1117)', border: '1px solid var(--border)',
          borderRadius: 4, padding: '10px 12px', wordBreak: 'break-all',
          marginBottom: 12, userSelect: 'all',
        }}>
          {keyData.key}
        </div>

        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn" onClick={handleCopy} style={{ flex: 1 }}>
            {copied ? 'Copied!' : 'Copy Key'}
          </button>
          <button className="btn" onClick={onClose} style={{ flex: 1 }}>
            Close
          </button>
        </div>
      </div>
    </div>
  )
}

// ── API Keys section ──────────────────────────────────────────────────────────

function APIKeysSection({ app }: { app: App }) {
  const [keys, setKeys]           = useState<APIKeyView[]>([])
  const [loading, setLoading]     = useState(true)
  const [err, setErr]             = useState('')
  const [rawKeyData, setRawKeyData] = useState<APIKeyCreateResponse | null>(null)

  // Generate-key form
  const [showGenForm, setShowGenForm] = useState(false)
  const [genAlias, setGenAlias]       = useState('')
  const [genTenants, setGenTenants]   = useState('')
  const [genErr, setGenErr]           = useState('')
  const [genSaving, setGenSaving]     = useState(false)

  // Revoke confirmation: keyId awaiting confirm, or 0
  const [revokeConfirm, setRevokeConfirm] = useState(0)
  const [revokeErr, setRevokeErr]         = useState('')

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listKeys(app.app_id)
      .then(r => setKeys(r ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [app.app_id])

  useEffect(() => { load() }, [load])

  function parseTenants(raw: string): number[] | undefined {
    const ids = raw.split(',').map(s => parseInt(s.trim(), 10)).filter(n => !isNaN(n))
    return ids.length > 0 ? ids : undefined
  }

  async function handleGenerate() {
    if (!genAlias.trim()) { setGenErr('Alias required'); return }
    setGenSaving(true); setGenErr('')
    try {
      const resp = await generateKey(app.app_id, {
        alias: genAlias.trim(),
        allowed_tenants: parseTenants(genTenants),
      })
      setGenAlias(''); setGenTenants(''); setShowGenForm(false)
      setRawKeyData(resp)
      load()
    } catch (e) { setGenErr(String(e)) }
    finally { setGenSaving(false) }
  }

  async function handleToggle(key: APIKeyView) {
    try {
      await updateKey(app.app_id, key.key_id, { enabled: !key.enabled })
      load()
    } catch (e) { setErr(String(e)) }
  }

  async function handleRotate(key: APIKeyView) {
    try {
      const resp = await rotateKey(app.app_id, key.key_id)
      setRawKeyData(resp)
      load()
    } catch (e) { setErr(String(e)) }
  }

  async function handleRevoke(keyId: number) {
    if (revokeConfirm !== keyId) { setRevokeConfirm(keyId); setRevokeErr(''); return }
    try {
      await revokeKey(app.app_id, keyId)
      setRevokeConfirm(0)
      load()
    } catch (e) { setRevokeErr(String(e)) }
  }

  return (
    <div style={{ marginTop: 24 }}>
      {rawKeyData && (
        <RawKeyModal keyData={rawKeyData} onClose={() => setRawKeyData(null)} />
      )}

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 }}>
        <div style={{ fontWeight: 600, color: 'var(--accent)' }}>API Keys</div>
        <button
          className="btn"
          style={{ fontSize: 12 }}
          onClick={() => { setShowGenForm(f => !f); setGenErr('') }}
        >
          {showGenForm ? 'Cancel' : '+ Generate Key'}
        </button>
      </div>

      {showGenForm && (
        <div style={{
          background: 'var(--block-bg)', border: '1px solid var(--border)',
          borderRadius: 6, padding: 14, marginBottom: 14,
        }}>
          <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 10 }}>New API Key</div>
          <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Alias</label>
          <input
            className="input"
            style={{ width: '100%', marginBottom: 10, boxSizing: 'border-box' }}
            placeholder="e.g. my-app-prod"
            value={genAlias}
            onChange={e => { setGenAlias(e.target.value); setGenErr('') }}
            onKeyDown={e => e.key === 'Enter' && handleGenerate()}
          />
          <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
            Allowed Tenant IDs (optional, comma-separated)
          </label>
          <input
            className="input"
            style={{ width: '100%', marginBottom: 10, boxSizing: 'border-box' }}
            placeholder="1, 2, 3 (leave blank for all tenants)"
            value={genTenants}
            onChange={e => setGenTenants(e.target.value)}
          />
          {genErr && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 8 }}>{genErr}</div>}
          <button className="btn" onClick={handleGenerate} disabled={genSaving}>
            {genSaving ? 'Generating…' : 'Generate'}
          </button>
        </div>
      )}

      {loading && <div style={{ color: 'var(--text-muted)', fontSize: 13 }}>Loading keys…</div>}
      {err && <div style={{ color: '#f87171', fontSize: 13, marginBottom: 8 }}>{err}</div>}

      {!loading && keys.length === 0 && !err && (
        <div style={{ color: 'var(--text-muted)', fontSize: 13 }}>No keys yet.</div>
      )}

      {keys.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)', color: 'var(--text-muted)' }}>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Alias</th>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Prefix</th>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Tenants</th>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Status</th>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Created</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {keys.map(k => (
              <tr key={k.key_id} style={{ borderBottom: '1px solid var(--border)' }}>
                <td style={{ padding: '6px 8px', fontWeight: 600 }}>{k.alias}</td>
                <td style={{ padding: '6px 8px', fontFamily: 'monospace', fontSize: 12 }}>{k.prefix}</td>
                <td style={{ padding: '6px 8px', color: 'var(--text-muted)', fontSize: 12 }}>
                  {k.allowed_tenants && k.allowed_tenants.length > 0
                    ? k.allowed_tenants.join(', ')
                    : 'all'}
                </td>
                <td style={{ padding: '6px 8px' }}>
                  <span style={{
                    fontSize: 11, fontWeight: 700,
                    color: k.enabled ? '#4ade80' : '#f87171',
                    background: k.enabled ? 'rgba(74,222,128,0.12)' : 'rgba(248,113,113,0.12)',
                    borderRadius: 4, padding: '2px 6px',
                  }}>
                    {k.enabled ? 'enabled' : 'disabled'}
                  </span>
                </td>
                <td style={{ padding: '6px 8px', color: 'var(--text-muted)', fontSize: 12 }}>
                  {fmtDate(k.created_at)}
                </td>
                <td style={{ padding: '6px 8px', textAlign: 'right', whiteSpace: 'nowrap' }}>
                  <button
                    className="btn"
                    style={{ fontSize: 11, marginRight: 4 }}
                    onClick={() => handleToggle(k)}
                  >
                    {k.enabled ? 'Disable' : 'Enable'}
                  </button>
                  <button
                    className="btn"
                    style={{ fontSize: 11, marginRight: 4 }}
                    onClick={() => handleRotate(k)}
                  >
                    Rotate
                  </button>
                  {revokeErr && revokeConfirm === k.key_id && (
                    <span style={{ color: '#f87171', fontSize: 11, marginRight: 4 }}>{revokeErr}</span>
                  )}
                  <button
                    className="btn"
                    style={{ fontSize: 11, background: revokeConfirm === k.key_id ? '#ef4444' : undefined }}
                    onClick={() => handleRevoke(k.key_id)}
                  >
                    {revokeConfirm === k.key_id ? 'Confirm revoke' : 'Revoke'}
                  </button>
                  {revokeConfirm === k.key_id && (
                    <button
                      className="btn"
                      style={{ fontSize: 11, marginLeft: 4 }}
                      onClick={() => setRevokeConfirm(0)}
                    >Cancel</button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

// ── App detail panel ──────────────────────────────────────────────────────────

function AppPanel({ app, onDeleted }: { app: App; onDeleted: () => void }) {
  const [editMode, setEditMode]     = useState(false)
  const [editName, setEditName]     = useState(app.name)
  const [editDesc, setEditDesc]     = useState(app.description)
  const [saveErr, setSaveErr]       = useState('')
  const [saving, setSaving]         = useState(false)
  const [delConfirm, setDelConfirm] = useState(false)
  const [delErr, setDelErr]         = useState('')
  const [showBlueprint, setShowBlueprint] = useState(false)

  // Reset local edit state when the selected app changes
  useEffect(() => {
    setEditName(app.name)
    setEditDesc(app.description)
    setEditMode(false)
    setSaveErr('')
    setDelConfirm(false)
    setDelErr('')
    setShowBlueprint(false)
  }, [app.app_id, app.name, app.description])

  async function handleSave() {
    setSaving(true); setSaveErr('')
    try {
      await updateApp(app.app_id, { name: editName.trim(), description: editDesc.trim() })
      setEditMode(false)
      // Parent will re-fetch the list; the panel itself stays open with the same app_id.
      // The updated name/desc will appear after parent refresh triggers a new app prop.
    } catch (e) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  async function handleDelete() {
    if (!delConfirm) { setDelConfirm(true); setDelErr(''); return }
    try {
      await deleteApp(app.app_id)
      onDeleted()
    } catch (e) { setDelErr(String(e)) }
  }

  return (
    <div style={{ padding: 20, overflowY: 'auto', height: '100%', boxSizing: 'border-box' }}>
      {showBlueprint && (
        <BlueprintWizard appName={app.name} onClose={() => setShowBlueprint(false)} />
      )}

      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <div style={{ fontSize: 18, fontWeight: 700 }}>{app.name}</div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>App ID: {app.app_id}</div>
        </div>
        <div style={{ display: 'flex', gap: 6 }}>
          {!editMode && (
            <>
              <button className="btn" style={{ fontSize: 12 }} onClick={() => setEditMode(true)}>Edit</button>
              <button className="btn" style={{ fontSize: 12 }} onClick={() => setShowBlueprint(true)}>Blueprint</button>
            </>
          )}
          <button
            className="btn"
            style={{ fontSize: 12, background: delConfirm ? '#ef4444' : undefined }}
            onClick={handleDelete}
          >
            {delConfirm ? 'Confirm delete' : 'Delete App'}
          </button>
          {delConfirm && (
            <button className="btn" style={{ fontSize: 12 }} onClick={() => setDelConfirm(false)}>Cancel</button>
          )}
        </div>
      </div>

      {delErr && <div style={{ color: '#f87171', fontSize: 13, marginBottom: 12 }}>{delErr}</div>}

      {/* Edit form */}
      {editMode ? (
        <div style={{
          background: 'var(--block-bg)', border: '1px solid var(--border)',
          borderRadius: 6, padding: 14, marginBottom: 16,
        }}>
          <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Name</label>
          <input
            className="input"
            style={{ width: '100%', marginBottom: 10, boxSizing: 'border-box' }}
            value={editName}
            onChange={e => { setEditName(e.target.value); setSaveErr('') }}
          />
          <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Description</label>
          <textarea
            className="input"
            style={{ width: '100%', height: 72, marginBottom: 10, resize: 'vertical', boxSizing: 'border-box' }}
            value={editDesc}
            onChange={e => { setEditDesc(e.target.value); setSaveErr('') }}
          />
          {saveErr && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 8 }}>{saveErr}</div>}
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="btn" style={{ fontSize: 12 }} onClick={handleSave} disabled={saving}>
              {saving ? 'Saving…' : 'Save'}
            </button>
            <button
              className="btn"
              style={{ fontSize: 12 }}
              onClick={() => { setEditMode(false); setEditName(app.name); setEditDesc(app.description); setSaveErr('') }}
            >Cancel</button>
          </div>
        </div>
      ) : (
        <div style={{ marginBottom: 16 }}>
          <div style={{ fontSize: 13, color: 'var(--text-muted)', marginBottom: 4 }}>Description</div>
          <div style={{ fontSize: 14 }}>{app.description || <span style={{ color: 'var(--text-muted)' }}>—</span>}</div>
        </div>
      )}

      {/* Metadata */}
      <div style={{ display: 'flex', gap: 24, marginBottom: 16, fontSize: 12, color: 'var(--text-muted)' }}>
        <div>Created: <span style={{ color: 'inherit' }}>{fmtDate(app.created_at)}</span></div>
        <div>Updated: <span style={{ color: 'inherit' }}>{fmtDate(app.updated_at)}</span></div>
      </div>

      {/* Labels */}
      {app.labels && Object.keys(app.labels).length > 0 && (
        <div style={{ marginBottom: 16 }}>
          <div style={{ fontWeight: 600, color: 'var(--accent)', marginBottom: 6, fontSize: 13 }}>Labels</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {Object.entries(app.labels).map(([k, v]) => (
              <span key={k} style={{
                background: 'var(--block-bg)', border: '1px solid var(--border)',
                borderRadius: 4, padding: '2px 8px', fontSize: 12,
              }}>{k}: {v}</span>
            ))}
          </div>
        </div>
      )}

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16 }}>
        <APIKeysSection app={app} />
      </div>
    </div>
  )
}

// ── New app form ──────────────────────────────────────────────────────────────

function NewAppForm({ onCreated }: { onCreated: () => void }) {
  const [name, setName]       = useState('')
  const [desc, setDesc]       = useState('')
  const [err, setErr]         = useState('')
  const [saving, setSaving]   = useState(false)

  async function handleSave() {
    if (!name.trim()) { setErr('Name required'); return }
    setSaving(true); setErr('')
    try {
      await createApp({ name: name.trim(), description: desc.trim() })
      setName(''); setDesc('')
      onCreated()
    } catch (e) { setErr(String(e)) }
    finally { setSaving(false) }
  }

  return (
    <div style={{ padding: 20 }}>
      <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 16 }}>New App</div>

      <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Name</label>
      <input
        className="input"
        style={{ width: '100%', marginBottom: 12, boxSizing: 'border-box' }}
        placeholder="e.g. My Application"
        value={name}
        onChange={e => { setName(e.target.value); setErr('') }}
        onKeyDown={e => e.key === 'Enter' && handleSave()}
      />

      <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Description</label>
      <textarea
        className="input"
        style={{ width: '100%', height: 72, marginBottom: 16, resize: 'vertical', boxSizing: 'border-box' }}
        placeholder="What is this app for?"
        value={desc}
        onChange={e => setDesc(e.target.value)}
      />

      {err && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 8 }}>{err}</div>}
      <button className="btn" onClick={handleSave} disabled={saving}>
        {saving ? 'Creating…' : 'Create App'}
      </button>
    </div>
  )
}

// ── Main Apps tab ─────────────────────────────────────────────────────────────

export default function Apps() {
  const [apps, setApps]           = useState<App[]>([])
  const [loading, setLoading]     = useState(true)
  const [err, setErr]             = useState('')
  const [selected, setSelected]   = useState<App | null>(null)
  const [view, setView]           = useState<'list' | 'new'>('list')

  const loadApps = useCallback(() => {
    setLoading(true); setErr('')
    listApps()
      .then(r => setApps(r ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { loadApps() }, [loadApps])

  function handleDeleted() {
    setSelected(null)
    loadApps()
  }

  return (
    <div style={{ display: 'flex', height: '100%', overflow: 'hidden' }}>

      {/* ── Left sidebar ── */}
      <div style={{
        width: 260, borderRight: '1px solid var(--border)',
        display: 'flex', flexDirection: 'column', flexShrink: 0,
      }}>
        <div style={{
          padding: '10px 12px', borderBottom: '1px solid var(--border)',
          fontWeight: 700, fontSize: 13,
        }}>
          Apps
        </div>

        <button
          className="btn"
          style={{ margin: 10, fontSize: 12 }}
          onClick={() => { setSelected(null); setView('new') }}
        >
          + New App
        </button>

        <div style={{ flex: 1, overflowY: 'auto' }}>
          {loading && <div style={{ padding: 12, color: 'var(--text-muted)', fontSize: 13 }}>Loading…</div>}
          {err && <div style={{ padding: 12, color: '#f87171', fontSize: 13 }}>{err}</div>}
          {apps.map(app => {
            const isSelected = selected?.app_id === app.app_id
            return (
              <div
                key={app.app_id}
                onClick={() => { setSelected(app); setView('list') }}
                style={{
                  padding: '8px 12px', cursor: 'pointer', fontSize: 13,
                  background: isSelected ? 'var(--accent-bg)' : undefined,
                  borderLeft: isSelected ? '3px solid var(--accent)' : '3px solid transparent',
                }}
              >
                <div style={{ fontWeight: 600, marginBottom: 2 }}>{app.name}</div>
                <div style={{ color: 'var(--text-muted)', fontSize: 11 }}>ID: {app.app_id}</div>
              </div>
            )
          })}
          {!loading && apps.length === 0 && !err && (
            <div style={{ padding: 12, color: 'var(--text-muted)', fontSize: 13 }}>No apps yet.</div>
          )}
        </div>
      </div>

      {/* ── Right content ── */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {view === 'new' && (
          <NewAppForm onCreated={() => { loadApps(); setView('list') }} />
        )}
        {view === 'list' && selected && (
          <AppPanel
            key={selected.app_id}
            app={selected}
            onDeleted={handleDeleted}
          />
        )}
        {view === 'list' && !selected && !loading && (
          <div className="panel-empty">Select an app to view details</div>
        )}
      </div>
    </div>
  )
}
