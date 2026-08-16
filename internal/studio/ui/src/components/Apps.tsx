import { useEffect, useState, useCallback } from 'react'
import {
  listApps, createApp, updateApp, deleteApp,
  listKeys, generateKey, updateKey, revokeKey, rotateKey,
  listAppUsages, listAppReleases, createAppRelease, generateAppBlueprint,
  type AppRelease, type AppBlueprintRequest, type AppBlueprintResponse, type FlowBlueprint,
} from '../api'
import type { App, APIKeyView, APIKeyCreateResponse, PaletteBlock, SavedFlow, FlowStep } from '../types'
import FlowDesigner from './FlowDesigner'

// ── helpers ──────────────────────────────────────────────────────────────────

function fmtDate(ts: number): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString()
}

// ── Flow Designer modal ───────────────────────────────────────────────────────

interface FlowDesignerModalProps {
  initialName: string
  blocks: PaletteBlock[]
  savedFlows: SavedFlow[]
  onSave: (name: string, steps: FlowStep[], constants: Record<string, string>) => void
  onClose: () => void
}

function FlowDesignerModal({ initialName, blocks, savedFlows, onSave, onClose }: FlowDesignerModalProps) {
  const [flowName, setFlowName] = useState(initialName)
  const [steps, setSteps]       = useState<FlowStep[]>([])
  const [constants, setConstants] = useState<Record<string, string>>({})

  function handleSave() {
    if (!flowName.trim()) return
    onSave(flowName.trim(), steps, constants)
  }

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 2000,
      background: 'var(--bg, #0a0c10)',
      display: 'flex', flexDirection: 'column',
    }}>
      {/* Modal top bar */}
      <div style={{
        height: 44, background: 'var(--sidebar-bg, #0f1117)',
        borderBottom: '1px solid var(--border)',
        display: 'flex', alignItems: 'center', padding: '0 16px', gap: 12, flexShrink: 0,
      }}>
        <button className="btn" style={{ fontSize: 12 }} onClick={onClose}>← Back to App</button>
        <div style={{ flex: 1, fontSize: 13, fontWeight: 600, color: 'var(--text-muted)' }}>
          Designing flow: <span style={{ color: 'var(--accent)', fontFamily: 'monospace' }}>{flowName || '(untitled)'}</span>
        </div>
        <button
          className="btn"
          style={{ fontSize: 12, background: steps.length > 0 && flowName.trim() ? 'var(--accent)' : undefined }}
          onClick={handleSave}
          disabled={steps.length === 0 || !flowName.trim()}
        >
          Save & Link to App
        </button>
      </div>

      {/* Full FlowDesigner fills the rest */}
      <div style={{ flex: 1, overflow: 'hidden' }}>
        <FlowDesigner
          blocks={blocks}
          steps={steps}
          setSteps={setSteps}
          flowName={flowName}
          setFlowName={setFlowName}
          flowConstants={constants}
          setFlowConstants={setConstants}
          savedFlows={savedFlows}
          onSaveFlow={handleSave}
          onNavigateToFlow={() => {}}
        />
      </div>
    </div>
  )
}

// ── Blueprint wizard modal ────────────────────────────────────────────────────

function BlueprintWizard({ appName, onClose }: { appName: string; onClose: () => void }) {
  const [appType, setAppType] = useState<'web' | 'api-service' | 'event-processor' | 'webhook'>('web')
  const [tenantMode, setTenantMode] = useState<'tenant_aware' | 'tenant_agnostic'>('tenant_aware')
  const [authFlow, setAuthFlow] = useState<'oauth_code' | 'form_login'>('oauth_code')
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
        req.auth_flow = authFlow
        if (authFlow === 'oauth_code') {
          req.oauth_provider = oauthProvider.trim() || undefined
          if (callbackPath.trim()) req.callback_path = callbackPath.trim()
        }
        if (loginPath.trim()) req.login_path = loginPath.trim()
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
                <div style={{ marginBottom: 14 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 6, fontWeight: 600 }}>Auth Flow</label>
                  <div style={{ display: 'flex', gap: 16 }}>
                    <label style={{ display: 'flex', alignItems: 'flex-start', fontSize: 13, cursor: 'pointer', gap: 6 }}>
                      <input
                        type="radio" name="authFlow" value="oauth_code"
                        checked={authFlow === 'oauth_code'}
                        onChange={() => setAuthFlow('oauth_code')}
                        style={{ marginTop: 2 }}
                      />
                      <div>
                        <div style={{ fontWeight: 600 }}>OAuth 2.0 Auth Code Flow</div>
                        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>
                          Redirect to provider (Google, GitHub, Okta…), exchange code for tokens
                        </div>
                      </div>
                    </label>
                    <label style={{ display: 'flex', alignItems: 'flex-start', fontSize: 13, cursor: 'pointer', gap: 6 }}>
                      <input
                        type="radio" name="authFlow" value="form_login"
                        checked={authFlow === 'form_login'}
                        onChange={() => setAuthFlow('form_login')}
                        style={{ marginTop: 2 }}
                      />
                      <div>
                        <div style={{ fontWeight: 600 }}>Form Login</div>
                        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>
                          Username/password form, session cookie — no external provider
                        </div>
                      </div>
                    </label>
                  </div>
                </div>

                {authFlow === 'oauth_code' && (
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
                )}

                <div style={{ marginBottom: 10 }}>
                  <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Login Path</label>
                  <input
                    className="input"
                    style={{ width: '100%', boxSizing: 'border-box' }}
                    value={loginPath}
                    onChange={e => { setLoginPath(e.target.value); setErr('') }}
                  />
                </div>

                {authFlow === 'oauth_code' && (
                  <div style={{ marginBottom: 10 }}>
                    <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Callback Path</label>
                    <input
                      className="input"
                      style={{ width: '100%', boxSizing: 'border-box' }}
                      value={callbackPath}
                      onChange={e => { setCallbackPath(e.target.value); setErr('') }}
                    />
                  </div>
                )}

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
            {result.flows.map((flow: FlowBlueprint, idx: number) => (
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

function ReleasesSection({ appName, flowNames = [] }: { appName: string; flowNames?: string[] }) {
  const [releases, setReleases]           = useState<AppRelease[]>([])
  const [loading, setLoading]             = useState(true)
  const [err, setErr]                     = useState('')
  const [showNewForm, setShowNewForm]     = useState(false)
  const [newVersion, setNewVersion]       = useState('')
  const [newChannel, setNewChannel]       = useState('stable')
  const [selectedFlows, setSelectedFlows] = useState<string[]>([])
  const [extraFlows, setExtraFlows]       = useState('')  // comma-sep for flows not in studio
  const [newNotes, setNewNotes]           = useState('')
  const [creating, setCreating]           = useState(false)
  const [createErr, setCreateErr]         = useState('')

  function toggleFlow(name: string) {
    setSelectedFlows(prev => prev.includes(name) ? prev.filter(f => f !== name) : [...prev, name])
  }

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listAppReleases(appName)
      .then(r => setReleases(r.releases ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [appName])

  useEffect(() => { load() }, [load])

  async function handleCreate() {
    if (!newVersion.trim()) { setCreateErr('Version is required'); return }
    const extra = extraFlows.split(',').map(f => f.trim()).filter(Boolean)
    const allFlows = [...selectedFlows, ...extra.filter(f => !selectedFlows.includes(f))]
    if (allFlows.length === 0) { setCreateErr('Select or enter at least one flow'); return }
    setCreating(true); setCreateErr('')
    try {
      await createAppRelease(appName, {
        version: newVersion.trim(),
        channel: newChannel.trim() || 'stable',
        flow_names: allFlows,
        notes: newNotes.trim() || undefined,
      })
      setShowNewForm(false)
      setNewVersion(''); setSelectedFlows([]); setExtraFlows(''); setNewNotes('')
      load()
    } catch (e) { setCreateErr(String(e)) }
    finally { setCreating(false) }
  }

  const active = releases.find(r => r.active)

  return (
    <div style={{ marginBottom: 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
        <div style={{ fontWeight: 600, color: 'var(--accent)', fontSize: 13 }}>Releases</div>
        <button className="btn" style={{ fontSize: 11 }} onClick={() => { setShowNewForm(v => !v); setCreateErr('') }}>
          {showNewForm ? 'Cancel' : '+ New Release'}
        </button>
      </div>

      {loading && <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>Loading…</div>}
      {err && <div style={{ fontSize: 12, color: '#f87171' }}>{err}</div>}

      {!loading && !err && releases.length === 0 && (
        <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>
          No releases yet. Create one to associate flows with this app.
        </div>
      )}

      {releases.map(rel => (
        <div key={rel.version} style={{
          background: 'var(--block-bg)', border: `1px solid ${rel.active ? 'var(--accent)' : 'var(--border)'}`,
          borderRadius: 6, padding: '8px 12px', marginBottom: 6,
        }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <span style={{ fontWeight: 600, fontSize: 13 }}>v{rel.version}</span>
              <span style={{
                fontSize: 10, padding: '1px 6px', borderRadius: 3,
                background: rel.active ? 'var(--accent)' : 'var(--border)',
                color: rel.active ? '#fff' : 'var(--text-muted)',
              }}>{rel.channel}</span>
              {rel.active && <span style={{ fontSize: 10, color: '#4ade80' }}>● ACTIVE</span>}
            </div>
            <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{rel.created_at ? new Date(rel.created_at).toLocaleDateString() : ''}</span>
          </div>
          <div style={{ fontSize: 11, color: 'var(--text-muted)', display: 'flex', flexWrap: 'wrap', gap: 4 }}>
            {(rel.flow_names ?? []).map(f => (
              <span key={f} style={{
                background: 'var(--sidebar-bg, #0f1117)', border: '1px solid var(--border)',
                borderRadius: 3, padding: '1px 6px', fontFamily: 'monospace',
              }}>{f}</span>
            ))}
          </div>
          {rel.notes && <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 4 }}>{rel.notes}</div>}
        </div>
      ))}

      {showNewForm && (
        <div style={{
          background: 'var(--block-bg)', border: '1px solid var(--border)',
          borderRadius: 6, padding: 12, marginTop: 8,
        }}>
          <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 8 }}>New Release</div>
          <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
            <div style={{ flex: 1 }}>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Version *</label>
              <input className="input" style={{ width: '100%', boxSizing: 'border-box', fontSize: 12 }}
                placeholder="e.g. 1.0.0" value={newVersion}
                onChange={e => { setNewVersion(e.target.value); setCreateErr('') }} />
            </div>
            <div style={{ flex: 1 }}>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Channel</label>
              <input className="input" style={{ width: '100%', boxSizing: 'border-box', fontSize: 12 }}
                placeholder="stable" value={newChannel}
                onChange={e => setNewChannel(e.target.value)} />
            </div>
          </div>
          <div style={{ marginBottom: 8 }}>
            <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Flows *</label>
            {flowNames.length > 0 ? (
              <div style={{
                background: 'var(--sidebar-bg, #0f1117)', border: '1px solid var(--border)',
                borderRadius: 4, padding: '6px 10px', marginBottom: 6,
                display: 'flex', flexWrap: 'wrap', gap: 6, maxHeight: 120, overflowY: 'auto',
              }}>
                {flowNames.map(fn => (
                  <label key={fn} style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 11, cursor: 'pointer', whiteSpace: 'nowrap' }}>
                    <input type="checkbox" checked={selectedFlows.includes(fn)} onChange={() => { toggleFlow(fn); setCreateErr('') }} />
                    <span style={{ fontFamily: 'monospace' }}>{fn}</span>
                  </label>
                ))}
              </div>
            ) : null}
            <input className="input" style={{ width: '100%', boxSizing: 'border-box', fontSize: 12 }}
              placeholder={flowNames.length > 0 ? 'Additional flows not listed above (comma-separated)' : 'Flow names, comma-separated'}
              value={extraFlows}
              onChange={e => { setExtraFlows(e.target.value); setCreateErr('') }} />
          </div>
          <div style={{ marginBottom: 10 }}>
            <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Notes</label>
            <input className="input" style={{ width: '100%', boxSizing: 'border-box', fontSize: 12 }}
              placeholder="Optional release notes" value={newNotes}
              onChange={e => setNewNotes(e.target.value)} />
          </div>
          {createErr && <div style={{ color: '#f87171', fontSize: 11, marginBottom: 8 }}>{createErr}</div>}
          <button className="btn" style={{ fontSize: 12 }} onClick={handleCreate} disabled={creating}>
            {creating ? 'Creating…' : 'Create Release'}
          </button>
        </div>
      )}

      {active && active.flow_names && active.flow_names.length > 0 && (
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 6 }}>
          Active release <strong>v{active.version}</strong> includes {active.flow_names.length} flow(s).
        </div>
      )}
    </div>
  )
}

interface AppPanelProps {
  app: App
  onDeleted: () => void
  flowNames?: string[]
  savedFlows?: SavedFlow[]
  blocks?: PaletteBlock[]
  onSaveFlow?: (name: string, steps: FlowStep[], constants: Record<string, string>) => void
}

function AppPanel({ app, onDeleted, flowNames = [], savedFlows = [], blocks = [], onSaveFlow }: AppPanelProps) {
  const [designingFlow, setDesigningFlow] = useState<string | null>(null)
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

  function handleFlowSavedFromPanel(flowName: string, steps: FlowStep[], constants: Record<string, string>) {
    onSaveFlow?.(flowName, steps, constants)
    setDesigningFlow(null)
  }

  if (designingFlow !== null) {
    return (
      <FlowDesignerModal
        initialName={designingFlow}
        blocks={blocks}
        savedFlows={savedFlows}
        onSave={handleFlowSavedFromPanel}
        onClose={() => setDesigningFlow(null)}
      />
    )
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
              <button className="btn" style={{ fontSize: 12 }} onClick={() => setDesigningFlow(`${app.name}-flow`)}>+ Design Flow</button>
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

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginBottom: 16 }}>
        <ReleasesSection appName={app.name} flowNames={flowNames} />
      </div>

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16 }}>
        <APIKeysSection app={app} />
      </div>
    </div>
  )
}

// ── New app form ──────────────────────────────────────────────────────────────

const APP_TYPE_DESCRIPTIONS: Record<string, string> = {
  'web':             'Browser app with login, sessions, OAuth',
  'api-service':     'Backend API consumed by other services',
  'event-processor': 'Reacts to events from queues or topics',
  'webhook':         'Receives and processes inbound webhooks',
}

interface NewAppFormProps {
  onCreated: (appName: string) => void
  flowNames?: string[]
  savedFlows?: SavedFlow[]
  blocks?: PaletteBlock[]
  onSaveFlow?: (name: string, steps: FlowStep[], constants: Record<string, string>) => void
}

function NewAppForm({ onCreated, flowNames = [], savedFlows = [], blocks = [], onSaveFlow }: NewAppFormProps) {
  const [name, setName]         = useState('')
  const [desc, setDesc]         = useState('')
  const [appType, setAppType]   = useState<'web' | 'api-service' | 'event-processor' | 'webhook'>('web')
  const [authFlow, setAuthFlow] = useState<'oauth_code' | 'form_login'>('oauth_code')
  const [err, setErr]           = useState('')
  const [saving, setSaving]     = useState(false)

  // Phase 2 state
  const [createdName, setCreatedName]   = useState('')
  const [linkedFlows, setLinkedFlows]   = useState<string[]>([])
  const [relCreating, setRelCreating]   = useState(false)
  const [relErr, setRelErr]             = useState('')
  const [relDone, setRelDone]           = useState(false)
  const [designingFlow, setDesigningFlow] = useState<string | null>(null)

  async function handleCreate() {
    if (!name.trim()) { setErr('Name required'); return }
    setSaving(true); setErr('')
    try {
      await createApp({ name: name.trim(), description: desc.trim() })
      setCreatedName(name.trim())
    } catch (e) { setErr(String(e)) }
    finally { setSaving(false) }
  }

  function suggestedFlowName(suffix: string) {
    return `${createdName}-${suffix}`
  }

  function openDesigner(suffix: string) {
    setDesigningFlow(suggestedFlowName(suffix))
  }

  function handleFlowSaved(flowName: string, steps: FlowStep[], constants: Record<string, string>) {
    onSaveFlow?.(flowName, steps, constants)
    setLinkedFlows(prev => prev.includes(flowName) ? prev : [...prev, flowName])
    setDesigningFlow(null)
  }

  function toggleLinked(fn: string) {
    setLinkedFlows(prev => prev.includes(fn) ? prev.filter(f => f !== fn) : [...prev, fn])
  }

  async function handleCreateRelease() {
    if (linkedFlows.length === 0) { setRelErr('Add at least one flow first'); return }
    setRelCreating(true); setRelErr('')
    try {
      await createAppRelease(createdName, { version: '1.0.0', channel: 'stable', flow_names: linkedFlows })
      setRelDone(true)
    } catch (e) { setRelErr(String(e)) }
    finally { setRelCreating(false) }
  }

  // ── Flow Designer full-screen modal ──
  if (designingFlow !== null) {
    return (
      <FlowDesignerModal
        initialName={designingFlow}
        blocks={blocks}
        savedFlows={savedFlows}
        onSave={handleFlowSaved}
        onClose={() => setDesigningFlow(null)}
      />
    )
  }

  // ── Phase 2: add flows ──
  if (createdName) {
    const flowSuggestions: { suffix: string; label: string; desc: string }[] = appType === 'web'
      ? authFlow === 'oauth_code'
        ? [
            { suffix: 'login',    label: 'Login',    desc: 'Redirect user to OAuth provider' },
            { suffix: 'callback', label: 'Callback', desc: 'Exchange auth code for tokens, set session' },
            { suffix: 'logout',   label: 'Logout',   desc: 'Clear session cookie and redirect' },
          ]
        : [
            { suffix: 'login',      label: 'Login',      desc: 'Accept username/password form, create session' },
            { suffix: 'logout',     label: 'Logout',     desc: 'Clear session and redirect to login' },
            { suffix: 'auth-check', label: 'Auth Check', desc: 'Validate session cookie on protected routes' },
          ]
      : appType === 'api-service'
        ? [
            { suffix: 'auth',    label: 'Auth',    desc: 'Validate API key or bearer token' },
            { suffix: 'handler', label: 'Handler', desc: 'Main request handler logic' },
          ]
        : appType === 'event-processor'
          ? [
              { suffix: 'handler', label: 'Handler', desc: 'Process incoming event' },
              { suffix: 'dlq',     label: 'DLQ',     desc: 'Handle failed / dead-letter events' },
            ]
          : [
              { suffix: 'verify',  label: 'Verify',  desc: 'Verify HMAC signature of incoming webhook' },
              { suffix: 'process', label: 'Process', desc: 'Process the webhook payload' },
            ]

    return (
      <div style={{ padding: 20, maxWidth: 640 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
          <span style={{ fontSize: 20, color: '#4ade80' }}>✓</span>
          <div>
            <div style={{ fontWeight: 700, fontSize: 16 }}>"{createdName}" is ready</div>
            <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              {appType} · {appType === 'web' ? (authFlow === 'oauth_code' ? 'OAuth 2.0 Auth Code' : 'Form Login') : APP_TYPE_DESCRIPTIONS[appType]}
            </div>
          </div>
        </div>

        <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 10 }}>Design flows for this app</div>
        <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 14 }}>
          Each button opens the visual Flow Designer. Add steps one by one — no YAML needed.
        </div>

        {/* Suggested flows for this app type */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 20 }}>
          {flowSuggestions.map(s => {
            const flowName = suggestedFlowName(s.suffix)
            const done = linkedFlows.includes(flowName)
            return (
              <button
                key={s.suffix}
                className="btn"
                style={{
                  display: 'flex', flexDirection: 'column', alignItems: 'flex-start',
                  padding: '10px 12px', textAlign: 'left', gap: 4,
                  border: `1px solid ${done ? '#4ade80' : 'var(--border)'}`,
                  background: done ? 'rgba(74,222,128,0.08)' : 'var(--block-bg)',
                }}
                onClick={() => openDesigner(s.suffix)}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, width: '100%' }}>
                  <span style={{ fontSize: 13, fontWeight: 600 }}>{done ? '✓ ' : ''}{s.label}</span>
                </div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)', fontWeight: 400 }}>{s.desc}</div>
                <div style={{ fontSize: 10, fontFamily: 'monospace', color: 'var(--accent)', marginTop: 2 }}>{flowName}</div>
              </button>
            )
          })}
        </div>

        {/* Custom flow */}
        <div style={{ marginBottom: 20 }}>
          <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 8 }}>Add a custom flow</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {[
              { suffix: 'api-call',      label: '+ API Call' },
              { suffix: 'schedule-job',  label: '+ Schedule Job' },
              { suffix: 'event-handler', label: '+ Event Handler' },
              { suffix: 'transform',     label: '+ Transform Data' },
              { suffix: 'notify',        label: '+ Notification' },
            ].map(s => (
              <button key={s.suffix} className="btn" style={{ fontSize: 12 }} onClick={() => openDesigner(s.suffix)}>
                {s.label}
              </button>
            ))}
          </div>
        </div>

        {/* Link existing flows from Studio */}
        {flowNames.filter(fn => !linkedFlows.includes(fn)).length > 0 && (
          <div style={{ marginBottom: 20 }}>
            <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 6 }}>Or link existing flows from Studio</div>
            <div style={{
              background: 'var(--block-bg)', border: '1px solid var(--border)',
              borderRadius: 6, padding: '8px 12px', display: 'flex', flexWrap: 'wrap', gap: 6,
            }}>
              {flowNames.filter(fn => !linkedFlows.includes(fn)).map(fn => (
                <label key={fn} style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 12, cursor: 'pointer' }}>
                  <input type="checkbox" checked={false} onChange={() => toggleLinked(fn)} />
                  <span style={{ fontFamily: 'monospace' }}>{fn}</span>
                </label>
              ))}
            </div>
          </div>
        )}

        {/* Linked flows summary */}
        {linkedFlows.length > 0 && (
          <div style={{
            background: 'rgba(74,222,128,0.06)', border: '1px solid rgba(74,222,128,0.3)',
            borderRadius: 6, padding: '10px 14px', marginBottom: 16,
          }}>
            <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 6 }}>
              {linkedFlows.length} flow{linkedFlows.length !== 1 ? 's' : ''} ready to release
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
              {linkedFlows.map(fn => (
                <span key={fn} style={{
                  fontFamily: 'monospace', fontSize: 11,
                  background: 'rgba(74,222,128,0.12)', borderRadius: 3, padding: '1px 6px',
                }}>
                  {fn}
                  <button
                    onClick={() => setLinkedFlows(prev => prev.filter(f => f !== fn))}
                    style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#f87171', marginLeft: 4, padding: 0, fontSize: 11 }}
                  >×</button>
                </span>
              ))}
            </div>
          </div>
        )}

        {relErr && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 8 }}>{relErr}</div>}

        {!relDone ? (
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              className="btn"
              style={{ background: linkedFlows.length > 0 ? 'var(--accent)' : undefined }}
              onClick={handleCreateRelease}
              disabled={relCreating || linkedFlows.length === 0}
            >
              {relCreating ? 'Creating release…' : `Publish Release v1.0.0 (${linkedFlows.length} flow${linkedFlows.length !== 1 ? 's' : ''})`}
            </button>
            <button className="btn" onClick={() => onCreated(createdName)}>Open App</button>
          </div>
        ) : (
          <div>
            <div style={{ color: '#4ade80', fontSize: 13, marginBottom: 10 }}>
              ✓ Release v1.0.0 published with {linkedFlows.length} flow{linkedFlows.length !== 1 ? 's' : ''}
            </div>
            <button className="btn" onClick={() => onCreated(createdName)}>Open App →</button>
          </div>
        )}
      </div>
    )
  }

  // ── Phase 1: creation form ──
  return (
    <div style={{ padding: 20, maxWidth: 560 }}>
      <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 16 }}>New App</div>

      <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Name</label>
      <input
        className="input"
        style={{ width: '100%', marginBottom: 12, boxSizing: 'border-box' }}
        placeholder="e.g. school-mgmt"
        value={name}
        onChange={e => { setName(e.target.value); setErr('') }}
      />

      <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Description</label>
      <textarea
        className="input"
        style={{ width: '100%', height: 60, marginBottom: 16, resize: 'vertical', boxSizing: 'border-box' }}
        placeholder="What is this app for?"
        value={desc}
        onChange={e => setDesc(e.target.value)}
      />

      <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 6, fontWeight: 600 }}>App Type</label>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 16 }}>
        {(['web', 'api-service', 'event-processor', 'webhook'] as const).map(t => (
          <label key={t} style={{
            display: 'flex', alignItems: 'flex-start', gap: 8, cursor: 'pointer',
            padding: '10px 12px', borderRadius: 6,
            border: `1px solid ${appType === t ? 'var(--accent)' : 'var(--border)'}`,
            background: appType === t ? 'var(--accent-bg)' : 'var(--block-bg)',
          }}>
            <input type="radio" name="appType" value={t} checked={appType === t} onChange={() => setAppType(t)} style={{ marginTop: 2 }} />
            <div>
              <div style={{ fontSize: 13, fontWeight: 600 }}>{t}</div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>{APP_TYPE_DESCRIPTIONS[t]}</div>
            </div>
          </label>
        ))}
      </div>

      {appType === 'web' && (
        <>
          <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 6, fontWeight: 600 }}>Auth Flow</label>
          <div style={{ display: 'flex', gap: 10, marginBottom: 16 }}>
            {([
              { value: 'oauth_code', label: 'OAuth 2.0 Auth Code', desc: 'Redirect to provider, exchange code for tokens' },
              { value: 'form_login', label: 'Form Login', desc: 'Username/password, session cookie, no external provider' },
            ] as const).map(opt => (
              <label key={opt.value} style={{
                flex: 1, display: 'flex', alignItems: 'flex-start', gap: 8, cursor: 'pointer',
                padding: '10px 12px', borderRadius: 6,
                border: `1px solid ${authFlow === opt.value ? 'var(--accent)' : 'var(--border)'}`,
                background: authFlow === opt.value ? 'var(--accent-bg)' : 'var(--block-bg)',
              }}>
                <input type="radio" name="authFlow" value={opt.value} checked={authFlow === opt.value}
                  onChange={() => setAuthFlow(opt.value)} style={{ marginTop: 3 }} />
                <div>
                  <div style={{ fontSize: 12, fontWeight: 600 }}>{opt.label}</div>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>{opt.desc}</div>
                </div>
              </label>
            ))}
          </div>
        </>
      )}

      {err && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 8 }}>{err}</div>}
      <button className="btn" onClick={handleCreate} disabled={saving}>
        {saving ? 'Creating…' : 'Create App →'}
      </button>
    </div>
  )
}

// ── Main Apps tab ─────────────────────────────────────────────────────────────

interface AppsProps {
  flowNames?: string[]
  savedFlows?: SavedFlow[]
  blocks?: PaletteBlock[]
  onSaveFlow?: (name: string, steps: FlowStep[], constants: Record<string, string>) => void
}

export default function Apps({ flowNames = [], savedFlows = [], blocks = [], onSaveFlow }: AppsProps) {
  const [apps, setApps]           = useState<App[]>([])
  const [loading, setLoading]     = useState(true)
  const [err, setErr]             = useState('')
  const [selected, setSelected]   = useState<App | null>(null)
  const [view, setView]           = useState<'list' | 'new'>('list')

  const loadApps = useCallback(() => {
    setLoading(true); setErr('')
    Promise.all([listApps(), listAppUsages().catch(() => ({ flow_usages: {} }))])
      .then(([registered, usages]) => {
        const all = [...(registered ?? [])]
        const registeredNames = new Set(all.map(a => a.name))
        // Discover apps deployed via rah-sync from release data in app-usages
        const syncedNames = new Set<string>()
        for (const entries of Object.values(usages.flow_usages ?? {})) {
          for (const e of entries) if (e.app_name) syncedNames.add(e.app_name)
        }
        for (const name of syncedNames) {
          if (!registeredNames.has(name)) {
            all.push({ app_id: 0, name, description: 'Deployed via rah-sync', labels: { source: 'sync' }, created_at: 0, updated_at: 0 })
          }
        }
        setApps(all)
      })
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
                <div style={{ fontWeight: 600, marginBottom: 2, display: 'flex', alignItems: 'center', gap: 6 }}>
                  {app.name}
                  {app.labels?.source === 'sync' && (
                    <span style={{ fontSize: 9, fontWeight: 700, padding: '1px 5px', borderRadius: 4, background: 'rgba(87,181,255,0.15)', color: 'var(--accent)', letterSpacing: '0.04em' }}>SYNCED</span>
                  )}
                </div>
                <div style={{ color: 'var(--text-muted)', fontSize: 11 }}>{app.app_id ? `ID: ${app.app_id}` : 'via rah-sync'}</div>
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
          <NewAppForm
            flowNames={flowNames}
            savedFlows={savedFlows}
            blocks={blocks}
            onSaveFlow={onSaveFlow}
            onCreated={(appName) => {
              loadApps()
              if (appName) {
                setSelected({ app_id: 0, name: appName, description: '', labels: {}, created_at: 0, updated_at: 0 })
              }
              setView('list')
            }}
          />
        )}
        {view === 'list' && selected && (
          <AppPanel
            key={selected.app_id}
            app={selected}
            onDeleted={handleDeleted}
            flowNames={flowNames}
            savedFlows={savedFlows}
            blocks={blocks}
            onSaveFlow={onSaveFlow}
          />
        )}
        {view === 'list' && !selected && !loading && (
          <div className="panel-empty">Select an app to view details</div>
        )}
      </div>
    </div>
  )
}
