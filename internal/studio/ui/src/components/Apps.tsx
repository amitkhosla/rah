import { useEffect, useState, useCallback, useRef } from 'react'
import {
  listApps, createApp, updateApp, deleteApp,
  listKeys, generateKey, updateKey, revokeKey, rotateKey,
  listAppUsages, listAppReleases, createAppRelease, promoteAppRelease, rollbackAppRelease, generateAppBlueprint,
  fetchGatewaySnapshot, syncFlows, deploy,
  fetchStudioConfig, getAppDraft, putAppDraft, deleteFromAppDraft,
  listAssets, uploadAsset, deleteAsset,
  type AppRelease, type AppBlueprintRequest, type AppBlueprintResponse, type FlowBlueprint, type AssetMeta,
  type AppDraft,
} from '../api'
import type { App, APIKeyView, APIKeyCreateResponse, PaletteBlock, SavedFlow, FlowStep, GatewayApi } from '../types'
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

// ── App APIs section ─────────────────────────────────────────────────────────

const METHOD_COLORS: Record<string, string> = {
  GET: '#4caf50', POST: '#2196f3', PUT: '#ff9800',
  PATCH: '#9c27b0', DELETE: '#f44336',
}
const HTTP_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']

function AppAPIsSection({ appName, gatewayBase = 'http://localhost:8081', flowNames = [], onDesignNew, directSyncEnabled = true }: {
  appName: string
  gatewayBase?: string
  flowNames?: string[]
  onDesignNew?: (suggestedName: string) => void
  directSyncEnabled?: boolean
}) {
  const [apis, setApis]             = useState<GatewayApi[]>([])
  const [allApis, setAllApis]       = useState<GatewayApi[]>([])
  const [loading, setLoading]       = useState(true)
  const [err, setErr]               = useState('')
  const [showForm, setShowForm]     = useState(false)
  const [showLink, setShowLink]     = useState(false)
  const [linking, setLinking]       = useState(false)
  const [linkErr, setLinkErr]       = useState('')
  const [saving, setSaving]         = useState(false)
  const [saveErr, setSaveErr]       = useState('')

  // inline test state per API
  const [testStates, setTestStates] = useState<Record<string, { body: string; running: boolean; result: { status: number; body: string } | null; err: string; expanded: boolean }>>({})

  function updateTestState(name: string, patch: Partial<{ body: string; running: boolean; result: { status: number; body: string } | null; err: string; expanded: boolean }>) {
    setTestStates(prev => {
      const cur = prev[name] ?? { body: '', running: false, result: null, err: '', expanded: false }
      return { ...prev, [name]: { ...cur, ...patch } }
    })
  }

  async function runTest(api: GatewayApi) {
    const method = (api.method ?? 'GET').toUpperCase()
    const needsBody = ['POST', 'PUT', 'PATCH'].includes(method)
    const st = testStates[api.name]
    updateTestState(api.name, { running: true, result: null, err: '', expanded: true })
    try {
      const r = await invokeApi(method, `${gatewayBase}${api.path}`, { 'content-type': 'application/json' }, needsBody && st?.body?.trim() ? st.body.trim() : undefined)
      updateTestState(api.name, { result: r, running: false })
    } catch (e) { updateTestState(api.name, { err: String(e), running: false }) }
  }

  // new-api form state
  const [newName, setNewName]   = useState('')
  const [newPath, setNewPath]   = useState('')
  const [newMethod, setNewMethod] = useState('GET')
  const [newFlow, setNewFlow]   = useState('')
  const [endpoints, setEndpoints] = useState<Array<{path: string; method: string; flow: string}>>([])

  function addEndpoint() {
    setEndpoints(prev => [...prev, { path: '/', method: 'GET', flow: '' }])
  }
  function removeEndpoint(i: number) {
    setEndpoints(prev => prev.filter((_, idx) => idx !== i))
  }
  function updateEndpoint(i: number, field: 'path' | 'method' | 'flow', value: string) {
    setEndpoints(prev => prev.map((ep, idx) => idx === i ? { ...ep, [field]: value } : ep))
  }

  const load = useCallback(() => {
    setLoading(true); setErr('')
    if (!directSyncEnabled) {
      getAppDraft(appName)
        .then(draft => setApis((draft.apis ?? []) as unknown as GatewayApi[]))
        .catch(e => setErr(String(e)))
        .finally(() => setLoading(false))
      return
    }
    fetchGatewaySnapshot()
      .then(snap => {
        const all = snap.apis ?? []
        setAllApis(all as GatewayApi[])
        setApis((all as GatewayApi[]).filter(a => a.app_name === appName))
      })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [appName, directSyncEnabled])

  useEffect(() => { load() }, [load])

  function resetForm() {
    setNewName(''); setNewPath(''); setNewMethod('GET'); setNewFlow('')
    setEndpoints([])
    setSaveErr(''); setShowForm(false)
  }

  // "Link existing" — take a gateway API that has no app_name and assign it here
  async function linkExisting(api: GatewayApi) {
    setLinking(true); setLinkErr('')
    try {
      await syncFlows({
        sync_uuid: crypto.randomUUID(),
        flows: [],
        apis: [{
          name:      api.name,
          path:      api.path,
          method:    api.method,
          flow_name: api.flow_name,
          app_name:  appName,
          action:    'upsert',
          ...(api.endpoint_configs ? { endpoint_configs: api.endpoint_configs } : {}),
        }],
      })
      setShowLink(false)
      load()
    } catch (e) { setLinkErr(String(e)) }
    finally { setLinking(false) }
  }

  // APIs that exist in the gateway but belong to a different app (or none)
  const unlinkedApis = allApis.filter(a => !a.app_name || a.app_name !== appName)

  async function handleAddApi() {
    if (!newName.trim()) { setSaveErr('Name is required'); return }
    if (!newPath.trim() || !newPath.startsWith('/')) { setSaveErr('Path must start with /'); return }
    if (!newFlow.trim()) { setSaveErr('Flow name is required'); return }
    setSaving(true); setSaveErr('')

    // Auto-prefix path with app name
    const relativePath = newPath.trim()
    const prefix = `/${appName}`
    const fullPath = relativePath.startsWith(prefix) ? relativePath : `${prefix}${relativePath}`

    // Build endpoint_configs only when user has explicitly added them
    const endpointConfigs = endpoints.length > 0
      ? endpoints.map(ep => ({
          path: ep.path || '/',
          method: ep.method,
          ...(ep.flow.trim() ? { flow_name: ep.flow.trim() } : {}),
        }))
      : undefined

    const apiPayload = {
      sync_uuid: crypto.randomUUID(),
      flows: [],
      apis: [{
        name:      newName.trim(),
        path:      fullPath,
        method:    newMethod,
        flow_name: newFlow.trim(),
        app_name:  appName,
        action:    'upsert' as const,
        ...(endpointConfigs ? { endpoint_configs: endpointConfigs } : {}),
      }],
    }
    try {
      if (!directSyncEnabled) {
        await putAppDraft(appName, apiPayload)
      } else {
        await syncFlows(apiPayload)
      }
      resetForm()
      load()
    } catch (e) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 }}>
        <div style={{ fontWeight: 600, color: 'var(--accent)', fontSize: 13 }}>APIs</div>
        {!showForm && !showLink && (
          <div style={{ display: 'flex', gap: 6 }}>
            <button className="btn" style={{ fontSize: 11 }} onClick={() => setShowLink(true)}>Link Existing</button>
            <button className="btn" style={{ fontSize: 11 }} onClick={() => setShowForm(true)}>+ Add API</button>
          </div>
        )}
      </div>

      {/* Link Existing API panel */}
      {showLink && (
        <div style={{
          background: 'var(--block-bg)', border: '1px solid var(--border)',
          borderRadius: 6, padding: 14, marginBottom: 12,
        }}>
          <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 8 }}>Link Existing API to {appName}</div>
          <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 8 }}>
            Select a gateway API to assign to this app. Its <code>app_name</code> will be updated.
          </div>
          {unlinkedApis.length === 0 ? (
            <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>
              All gateway APIs are already linked to apps.
            </div>
          ) : (
            <div style={{ maxHeight: 240, overflowY: 'auto', border: '1px solid var(--border)', borderRadius: 4 }}>
              {unlinkedApis.map(api => (
                <div key={api.name} style={{
                  display: 'flex', alignItems: 'center', gap: 8, padding: '6px 10px',
                  borderBottom: '1px solid var(--border)', fontSize: 12,
                }}>
                  <span style={{
                    fontSize: 10, fontWeight: 700, color: '#fff', minWidth: 40, textAlign: 'center',
                    background: METHOD_COLORS[(api.method ?? '').toUpperCase()] ?? '#607d8b',
                    borderRadius: 3, padding: '1px 5px',
                  }}>{api.method || 'ANY'}</span>
                  <span style={{ fontFamily: 'monospace', flex: 1, fontSize: 11 }}>{api.path}</span>
                  <span style={{ color: 'var(--text-muted)', fontSize: 11 }}>{api.name}</span>
                  {api.app_name && (
                    <span style={{ fontSize: 10, color: '#f59e0b' }}>({api.app_name})</span>
                  )}
                  <button className="btn btn-primary" style={{ fontSize: 10, padding: '2px 8px' }}
                    onClick={() => linkExisting(api)} disabled={linking}>
                    {linking ? '…' : 'Link'}
                  </button>
                </div>
              ))}
            </div>
          )}
          {linkErr && <div style={{ fontSize: 11, color: '#f44336', marginTop: 6 }}>{linkErr}</div>}
          <div style={{ marginTop: 8 }}>
            <button className="btn" style={{ fontSize: 11 }} onClick={() => { setShowLink(false); setLinkErr('') }}>Cancel</button>
          </div>
        </div>
      )}

      {showForm && (
        <div style={{
          background: 'var(--block-bg)', border: '1px solid var(--border)',
          borderRadius: 6, padding: 14, marginBottom: 12,
        }}>
          <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 10 }}>New API</div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 8 }}>
            <div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Name</div>
              <input className="input" style={{ width: '100%', fontSize: 12 }}
                placeholder="e.g. school-students-list"
                value={newName} onChange={e => setNewName(e.target.value)} />
            </div>
            <div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>
                Path <span style={{ fontWeight: 400 }}>(relative to app — prefixed with /{appName})</span>
              </div>
              <input className="input" style={{ width: '100%', fontSize: 12 }}
                placeholder="e.g. /students"
                value={newPath} onChange={e => setNewPath(e.target.value)} />
              {newPath && newPath.startsWith('/') && (
                <div style={{ fontSize: 10, color: 'var(--text-muted)', marginTop: 2, fontFamily: 'monospace' }}>
                  → /{appName}{newPath}
                </div>
              )}
            </div>
            <div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Method</div>
              <select className="input" style={{ width: '100%', fontSize: 12 }}
                value={newMethod} onChange={e => setNewMethod(e.target.value)}>
                {HTTP_METHODS.map(m => <option key={m} value={m}>{m}</option>)}
              </select>
            </div>
            <div>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Flow</div>
              <FlowNameInput
                value={newFlow}
                onChange={setNewFlow}
                flowNames={flowNames}
                placeholder="Select or type flow name…"
                onDesignNew={onDesignNew}
              />
            </div>
          </div>

          {/* Endpoint Configs */}
          <div style={{ marginBottom: 8 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', fontWeight: 600 }}>Endpoint Configs</div>
              <button className="btn" style={{ fontSize: 10, padding: '1px 7px' }} onClick={addEndpoint}>+ Add</button>
              <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>
                Optional — define sub-paths/methods with per-endpoint flows
              </span>
            </div>
            {endpoints.length > 0 && (
              <div style={{ border: '1px solid var(--border)', borderRadius: 4, overflow: 'hidden' }}>
                <div style={{
                  display: 'grid', gridTemplateColumns: '1.5fr 90px 1fr auto',
                  gap: 0, background: 'var(--block-bg)',
                  borderBottom: '1px solid var(--border)',
                  fontSize: 10, color: 'var(--text-muted)', fontWeight: 600,
                }}>
                  <div style={{ padding: '3px 8px' }}>Sub-path</div>
                  <div style={{ padding: '3px 8px' }}>Method</div>
                  <div style={{ padding: '3px 8px' }}>Flow override</div>
                  <div style={{ padding: '3px 8px' }}></div>
                </div>
                {endpoints.map((ep, i) => (
                  <div key={i} style={{
                    display: 'grid', gridTemplateColumns: '1.5fr 90px 1fr auto',
                    gap: 0, borderBottom: i < endpoints.length - 1 ? '1px solid var(--border)' : undefined,
                    alignItems: 'center',
                  }}>
                    <div style={{ padding: '3px 6px' }}>
                      <input className="input" style={{ width: '100%', fontSize: 11 }}
                        placeholder="/" value={ep.path}
                        onChange={e => updateEndpoint(i, 'path', e.target.value)} />
                    </div>
                    <div style={{ padding: '3px 6px' }}>
                      <select className="input" style={{ width: '100%', fontSize: 11 }}
                        value={ep.method} onChange={e => updateEndpoint(i, 'method', e.target.value)}>
                        {HTTP_METHODS.map(m => <option key={m} value={m}>{m}</option>)}
                      </select>
                    </div>
                    <div style={{ padding: '3px 6px' }}>
                      <input className="input" style={{ width: '100%', fontSize: 11 }}
                        placeholder="(uses default flow)"
                        value={ep.flow} onChange={e => updateEndpoint(i, 'flow', e.target.value)} />
                    </div>
                    <div style={{ padding: '3px 6px' }}>
                      <button className="btn" style={{ fontSize: 10, color: '#f87171' }}
                        onClick={() => removeEndpoint(i)}>×</button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
          {saveErr && <div style={{ fontSize: 11, color: '#f44336', marginBottom: 6 }}>{saveErr}</div>}
          <div style={{ display: 'flex', gap: 6 }}>
            <button className="btn btn-primary" style={{ fontSize: 11 }}
              onClick={handleAddApi} disabled={saving}>
              {saving ? 'Saving…' : 'Save API'}
            </button>
            <button className="btn" style={{ fontSize: 11 }} onClick={resetForm}>Cancel</button>
          </div>
        </div>
      )}

      {loading && <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>Loading…</div>}
      {err && <div style={{ fontSize: 12, color: '#f44336' }}>{err}</div>}
      {!loading && !err && apis.length === 0 && !showForm && (
        <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>
          No APIs yet. Click "+ Add API" to register one, or add <code>app_name: {appName}</code> to your bundle YAML and re-sync.
        </div>
      )}
      {!loading && apis.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {apis.map(api => {
            const epMethods = api.endpoint_configs?.map(ec => ec.method?.toUpperCase() ?? 'ANY')
            const effectiveMethod = api.method?.toUpperCase()
              || (epMethods && epMethods.length === 1 ? epMethods[0] : undefined)
              || (epMethods && epMethods.length > 1 ? 'MULTI' : 'ANY')
            const needsBody = ['POST', 'PUT', 'PATCH'].includes(effectiveMethod ?? '')
            const st = testStates[api.name] ?? { body: '', running: false, result: null, err: '', expanded: false }
            const statusColor = st.result ? (st.result.status < 400 ? '#4caf50' : '#f44336') : 'var(--text-muted)'

            return (
              <div key={api.name} style={{
                border: '1px solid var(--border)', borderRadius: 6,
                background: 'var(--block-bg)', overflow: 'hidden',
              }}>
                {/* Header row */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px' }}>
                  {/* Method badge(s) */}
                  {effectiveMethod === 'MULTI' ? (
                    <div style={{ display: 'flex', gap: 2 }}>
                      {epMethods!.map((m, i) => (
                        <span key={i} style={{
                          background: METHOD_COLORS[m] ?? '#607d8b', color: '#fff',
                          borderRadius: 3, padding: '1px 4px', fontSize: 9, fontWeight: 600,
                        }}>{m}</span>
                      ))}
                    </div>
                  ) : (
                    <span style={{
                      background: METHOD_COLORS[effectiveMethod ?? 'ANY'] ?? '#607d8b',
                      color: '#fff', borderRadius: 3, padding: '2px 6px', fontSize: 10, fontWeight: 600, minWidth: 40, textAlign: 'center',
                    }}>{effectiveMethod}</span>
                  )}
                  {!directSyncEnabled && (
                    <span style={{ fontSize: 9, fontWeight: 700, color: '#fff', background: '#f97316', borderRadius: 3, padding: '1px 5px' }}>DRAFT</span>
                  )}
                  <span style={{ fontFamily: 'monospace', fontSize: 12, flex: 1, color: 'var(--text)' }}>{api.path}</span>
                  <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{api.flow_name}</span>
                  <span
                    style={{ fontFamily: 'monospace', fontSize: 10, color: '#89b4fa', cursor: 'pointer' }}
                    title="Copy URL"
                    onClick={() => navigator.clipboard.writeText(`${gatewayBase}${api.path}`)}
                  >📋</span>
                  {st.result && (
                    <span style={{ fontSize: 11, fontWeight: 700, color: statusColor }}>{st.result.status}</span>
                  )}
                  <button
                    className="btn btn-primary"
                    style={{ fontSize: 10, padding: '2px 8px' }}
                    onClick={() => runTest(api)}
                    disabled={st.running}
                  >{st.running ? '…' : '▶ Test'}</button>
                  <button
                    className="btn"
                    style={{ fontSize: 10, padding: '2px 6px', color: 'var(--text-muted)' }}
                    onClick={() => updateTestState(api.name, { expanded: !st.expanded })}
                  >{st.expanded ? '▲' : '▼'}</button>
                </div>

                {/* Expandable: body input + response */}
                {st.expanded && (
                  <div style={{ borderTop: '1px solid var(--border)', padding: '8px 10px', background: 'var(--bg)' }}>
                    <div style={{ fontSize: 10, color: 'var(--text-muted)', fontFamily: 'monospace', marginBottom: 8 }}>
                      {effectiveMethod} {gatewayBase}{api.path}
                      {api.name !== api.path && <span style={{ color: 'var(--text-muted)', marginLeft: 10 }}>{api.name}</span>}
                    </div>
                    {needsBody && (
                      <div style={{ marginBottom: 8 }}>
                        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Request body (JSON)</div>
                        <textarea className="input"
                          style={{ width: '100%', height: 72, resize: 'vertical', fontSize: 11, fontFamily: 'monospace', boxSizing: 'border-box' }}
                          placeholder={'{\n  "key": "value"\n}'}
                          value={st.body}
                          onChange={e => updateTestState(api.name, { body: e.target.value })}
                        />
                      </div>
                    )}
                    {st.err && <div style={{ fontSize: 11, color: '#f44336', marginBottom: 6 }}>{st.err}</div>}
                    {st.result && (
                      <div>
                        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 4 }}>
                          Response — <span style={{ color: statusColor, fontWeight: 700 }}>{st.result.status}</span>
                        </div>
                        <pre style={{
                          background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 4,
                          padding: 8, fontSize: 10, fontFamily: 'monospace',
                          maxHeight: 180, overflowY: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0,
                        }}>{st.result.body}</pre>
                      </div>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ── Shared test invoke helper ─────────────────────────────────────────────────

async function invokeApi(
  method: string,
  url: string,
  headers: Record<string, string>,
  body?: string
): Promise<{ status: number; body: string }> {
  const res = await fetch('/api/gateway-invoke', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify({ method, url, headers, body: body || '' }),
  })
  if (!res.ok) {
    const t = await res.text()
    throw new Error(t || `proxy error ${res.status}`)
  }
  const data = await res.json()
  if (data.error) throw new Error(data.error)
  let pretty = data.body ?? ''
  try { pretty = JSON.stringify(JSON.parse(pretty), null, 2) } catch {}
  return { status: data.status, body: pretty }
}

// ── Full app test panel ───────────────────────────────────────────────────────

interface ApiTestState {
  expanded: boolean
  body: string
  running: boolean
  result: { status: number; body: string } | null
  err: string
}

function TestAppPanel({ app, gatewayBase }: { app: App; gatewayBase: string }) {
  const [apis, setApis]           = useState<GatewayApi[]>([])
  const [loading, setLoading]     = useState(true)
  const [sharedHeaders, setSharedHeaders] = useState('')
  const [runningAll, setRunningAll] = useState(false)
  const [apiStates, setApiStates] = useState<Record<string, ApiTestState>>({})

  useEffect(() => {
    fetchGatewaySnapshot()
      .then(snap => {
        const appApis = (snap.apis ?? []).filter((a: any) => a.app_name === app.name) as GatewayApi[]
        setApis(appApis)
        const initial: Record<string, ApiTestState> = {}
        for (const a of appApis) {
          initial[a.name] = { expanded: false, body: '', running: false, result: null, err: '' }
        }
        setApiStates(initial)
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [app.name])

  function parseHeaders(raw: string): Record<string, string> {
    const h: Record<string, string> = {}
    for (const line of raw.split('\n')) {
      const idx = line.indexOf(':')
      if (idx > 0) h[line.slice(0, idx).trim()] = line.slice(idx + 1).trim()
    }
    return h
  }

  function updateApiState(name: string, patch: Partial<ApiTestState>) {
    setApiStates(prev => ({ ...prev, [name]: { ...prev[name], ...patch } }))
  }

  async function runOne(api: GatewayApi): Promise<void> {
    const method = (api.method ?? 'GET').toUpperCase()
    const needsBody = ['POST', 'PUT', 'PATCH'].includes(method)
    const st = apiStates[api.name]
    updateApiState(api.name, { running: true, result: null, err: '', expanded: true })
    try {
      const hdrs = { 'content-type': 'application/json', ...parseHeaders(sharedHeaders) }
      const r = await invokeApi(
        method,
        `${gatewayBase}${api.path}`,
        hdrs,
        needsBody && st?.body?.trim() ? st.body.trim() : undefined,
      )
      updateApiState(api.name, { result: r, running: false })
    } catch (e) {
      updateApiState(api.name, { err: String(e), running: false })
    }
  }

  async function runAll() {
    setRunningAll(true)
    for (const api of apis) await runOne(api)
    setRunningAll(false)
  }

  const METHOD_COLORS_LOCAL: Record<string, string> = {
    GET: '#4caf50', POST: '#2196f3', PUT: '#ff9800', PATCH: '#9c27b0', DELETE: '#f44336',
  }

  if (loading) return <div style={{ fontSize: 12, color: 'var(--text-muted)', padding: '8px 0' }}>Loading APIs…</div>
  if (apis.length === 0) return (
    <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>
      No APIs registered for this app. Add APIs first.
    </div>
  )

  const passCount = apis.filter(a => apiStates[a.name]?.result && apiStates[a.name].result!.status < 400).length
  const failCount = apis.filter(a => apiStates[a.name]?.result && apiStates[a.name].result!.status >= 400).length
  const anyRan = apis.some(a => apiStates[a.name]?.result)

  return (
    <div>
      {/* Shared headers + run all */}
      <div style={{ marginBottom: 12 }}>
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>
          Shared headers <span style={{ fontWeight: 400 }}>(applied to all requests — one per line, key: value)</span>
        </div>
        <textarea className="input"
          style={{ width: '100%', height: 52, resize: 'vertical', fontSize: 11, fontFamily: 'monospace', boxSizing: 'border-box', marginBottom: 8 }}
          placeholder={'Authorization: Bearer token\nX-Tenant: acme'}
          value={sharedHeaders} onChange={e => setSharedHeaders(e.target.value)}
        />
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <button className="btn btn-primary" style={{ fontSize: 12 }} onClick={runAll} disabled={runningAll}>
            {runningAll ? 'Running…' : `▶ Run All (${apis.length})`}
          </button>
          {anyRan && (
            <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              <span style={{ color: '#4caf50', fontWeight: 700 }}>✓ {passCount}</span>
              {' / '}
              <span style={{ color: failCount > 0 ? '#f44336' : 'var(--text-muted)', fontWeight: 700 }}>{failCount > 0 ? `✗ ${failCount}` : `✗ 0`}</span>
            </span>
          )}
        </div>
      </div>

      {/* Per-API rows */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {apis.map(api => {
          const st = apiStates[api.name] ?? { expanded: false, body: '', running: false, result: null, err: '' }
          const method = (api.method ?? 'GET').toUpperCase()
          const needsBody = ['POST', 'PUT', 'PATCH'].includes(method)
          const statusColor = st.result ? (st.result.status < 400 ? '#4caf50' : '#f44336') : 'var(--text-muted)'

          return (
            <div key={api.name} style={{
              border: '1px solid var(--border)', borderRadius: 6,
              background: 'var(--block-bg)', overflow: 'hidden',
            }}>
              {/* Row header */}
              <div
                style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', cursor: 'pointer' }}
                onClick={() => updateApiState(api.name, { expanded: !st.expanded })}
              >
                <span style={{
                  fontSize: 10, fontWeight: 700, color: '#fff', minWidth: 44, textAlign: 'center',
                  background: METHOD_COLORS_LOCAL[method] ?? '#607d8b', borderRadius: 3, padding: '2px 5px',
                }}>{method}</span>
                <span style={{ fontFamily: 'monospace', fontSize: 12, flex: 1, color: 'var(--text)' }}>{api.path}</span>
                <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{api.name}</span>
                {st.result && (
                  <span style={{ fontSize: 11, fontWeight: 700, color: statusColor }}>{st.result.status}</span>
                )}
                {st.running && <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>…</span>}
                <button
                  className="btn" style={{ fontSize: 11, padding: '2px 8px' }}
                  onClick={e => { e.stopPropagation(); runOne(api) }}
                  disabled={st.running}
                >▶</button>
                <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{st.expanded ? '▲' : '▼'}</span>
              </div>

              {/* Expanded body/response */}
              {st.expanded && (
                <div style={{ borderTop: '1px solid var(--border)', padding: '8px 10px' }}>
                  {needsBody && (
                    <div style={{ marginBottom: 8 }}>
                      <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Body (JSON)</div>
                      <textarea className="input"
                        style={{ width: '100%', height: 72, resize: 'vertical', fontSize: 11, fontFamily: 'monospace', boxSizing: 'border-box' }}
                        placeholder={'{\n  "key": "value"\n}'}
                        value={st.body}
                        onChange={e => updateApiState(api.name, { body: e.target.value })}
                      />
                    </div>
                  )}
                  {st.err && <div style={{ fontSize: 11, color: '#f44336', marginBottom: 6 }}>{st.err}</div>}
                  {st.result && (
                    <div>
                      <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 4 }}>
                        Response — <span style={{ color: statusColor, fontWeight: 700 }}>{st.result.status}</span>
                      </div>
                      <pre style={{
                        background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 4,
                        padding: 8, fontSize: 10, fontFamily: 'monospace',
                        maxHeight: 160, overflowY: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                        margin: 0,
                      }}>{st.result.body}</pre>
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

// ── Try API panel ─────────────────────────────────────────────────────────────

function TryAPIsSection({ appName, gatewayBase = 'http://localhost:8081' }: { appName: string; gatewayBase?: string }) {
  const [apis, setApis]           = useState<GatewayApi[]>([])
  const [loading, setLoading]     = useState(true)
  const [selectedApi, setSelectedApi] = useState<GatewayApi | null>(null)
  const [headers, setHeaders]     = useState('')
  const [body, setBody]           = useState('')
  const [running, setRunning]     = useState(false)
  const [result, setResult]       = useState<{ status: number; body: string } | null>(null)
  const [runErr, setRunErr]       = useState('')

  useEffect(() => {
    fetchGatewaySnapshot()
      .then(snap => {
        const appApis = (snap.apis ?? []).filter((a: any) => a.app_name === appName)
        setApis(appApis as GatewayApi[])
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [appName])

  const method = selectedApi?.method?.toUpperCase() ?? 'GET'
  const needsBody = ['POST', 'PUT', 'PATCH'].includes(method)

  async function handleRun() {
    if (!selectedApi) return
    setRunning(true); setResult(null); setRunErr('')
    try {
      const parsedHeaders: Record<string, string> = { 'content-type': 'application/json' }
      for (const line of headers.split('\n')) {
        const idx = line.indexOf(':')
        if (idx > 0) parsedHeaders[line.slice(0, idx).trim()] = line.slice(idx + 1).trim()
      }
      const r = await invokeApi(
        method,
        `${gatewayBase}${selectedApi.path}`,
        parsedHeaders,
        needsBody && body.trim() ? body.trim() : undefined,
      )
      setResult(r)
    } catch (e) { setRunErr(String(e)) }
    finally { setRunning(false) }
  }

  return (
    <div>
      <div style={{ fontWeight: 600, color: 'var(--accent)', fontSize: 13, marginBottom: 10 }}>Try API</div>
      {loading && <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>Loading…</div>}
      {!loading && apis.length === 0 && (
        <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>
          No APIs registered for this app yet.
        </div>
      )}
      {!loading && apis.length > 0 && (
        <div>
          <div style={{ marginBottom: 8 }}>
            <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Select API</div>
            <select className="input" style={{ width: '100%', fontSize: 12 }}
              value={selectedApi?.name ?? ''}
              onChange={e => {
                const a = apis.find(x => x.name === e.target.value) ?? null
                setSelectedApi(a); setResult(null); setRunErr('')
              }}>
              <option value="">— pick an API —</option>
              {apis.map(a => (
                <option key={a.name} value={a.name}>
                  {a.method?.toUpperCase() || 'GET'} {a.path} ({a.name})
                </option>
              ))}
            </select>
          </div>

          {selectedApi && (
            <div>
              <div style={{ marginBottom: 8 }}>
                <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>
                  Headers <span style={{ fontWeight: 400 }}>(one per line, key: value)</span>
                </div>
                <textarea className="input"
                  style={{ width: '100%', height: 56, resize: 'vertical', fontSize: 12, fontFamily: 'monospace', boxSizing: 'border-box' }}
                  placeholder={'Authorization: Bearer token\nX-Tenant: acme'}
                  value={headers} onChange={e => setHeaders(e.target.value)} />
              </div>
              {needsBody && (
                <div style={{ marginBottom: 8 }}>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Body (JSON)</div>
                  <textarea className="input"
                    style={{ width: '100%', height: 80, resize: 'vertical', fontSize: 12, fontFamily: 'monospace', boxSizing: 'border-box' }}
                    placeholder={'{\n  "key": "value"\n}'}
                    value={body} onChange={e => setBody(e.target.value)} />
                </div>
              )}
              <button className="btn btn-primary" style={{ fontSize: 12 }}
                onClick={handleRun} disabled={running}>
                {running ? 'Sending…' : `Send ${method}`}
              </button>

              {runErr && <div style={{ marginTop: 8, fontSize: 12, color: '#f44336' }}>{runErr}</div>}
              {result && (
                <div style={{ marginTop: 10 }}>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 4 }}>
                    Response —{' '}
                    <span style={{ color: result.status < 400 ? '#4caf50' : '#f44336', fontWeight: 700 }}>
                      {result.status}
                    </span>
                  </div>
                  <pre style={{
                    background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 4,
                    padding: 10, fontSize: 11, fontFamily: 'monospace',
                    maxHeight: 200, overflowY: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                  }}>{result.body}</pre>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ── Test flows panel ──────────────────────────────────────────────────────────

interface TestEndpointState {
  body: string
  running: boolean
  result: { status: number; body: string } | null
  err: string
}

function TestFlowsSection({ app, gatewayBase = 'http://localhost:8081' }: { app: App; gatewayBase?: string }) {
  const [testApis, setTestApis]     = useState<GatewayApi[]>([])
  const [loading, setLoading]       = useState(true)
  const [manualFlow, setManualFlow] = useState('')
  const [publishing, setPublishing] = useState<string | null>(null)
  const [removing, setRemoving]     = useState<string | null>(null)
  const [err, setErr]               = useState('')
  const [testStates, setTestStates] = useState<Record<string, TestEndpointState>>({})

  function updateTestState(name: string, patch: Partial<TestEndpointState>) {
    setTestStates(prev => {
      const cur = prev[name] ?? { body: '', running: false, result: null, err: '' }
      return { ...prev, [name]: { ...cur, ...patch } }
    })
  }

  async function runTest(api: GatewayApi) {
    updateTestState(api.name, { running: true, result: null, err: '' })
    const st = testStates[api.name]
    try {
      const r = await invokeApi('POST', `${gatewayBase}${api.path}`, { 'content-type': 'application/json' }, st?.body?.trim() || undefined)
      updateTestState(api.name, { result: r, running: false })
    } catch (e) {
      updateTestState(api.name, { err: String(e), running: false })
    }
  }

  const testPrefix = `/test/${app.name}/`

  const load = useCallback(() => {
    setLoading(true)
    fetchGatewaySnapshot()
      .then(snap => {
        const tests = (snap.apis ?? []).filter(
          (a: any) => a.app_name === app.name && (a.path ?? '').startsWith(testPrefix)
        )
        setTestApis(tests as GatewayApi[])
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [app.name, testPrefix])

  useEffect(() => { load() }, [load])

  async function publish(flowName: string) {
    if (!flowName.trim()) return
    setPublishing(flowName); setErr('')
    try {
      await syncFlows({
        sync_uuid: crypto.randomUUID(),
        flows: [],
        apis: [{
          name: `test-${app.name}-${flowName.trim()}`,
          path: `${testPrefix}${flowName.trim()}`,
          flow_name: flowName.trim(),
          app_name: app.name,
          action: 'upsert',
          endpoint_configs: [{ path: '/', method: 'POST' }],
        }],
      })
      setManualFlow('')
      load()
    } catch (e) { setErr(String(e)) }
    finally { setPublishing(null) }
  }

  async function remove(api: GatewayApi) {
    setRemoving(api.name); setErr('')
    try {
      await syncFlows({
        sync_uuid: crypto.randomUUID(),
        flows: [],
        apis: [{
          name: api.name,
          path: api.path,
          flow_name: api.flow_name,
          app_name: app.name,
          action: 'delete',
          endpoint_configs: [{ path: '/', method: 'POST' }],
        }],
      })
      load()
    } catch (e) { setErr(String(e)) }
    finally { setRemoving(null) }
  }

  const eventBindings = app.event_bindings ?? []

  return (
    <div>
      <div style={{ fontWeight: 600, color: 'var(--accent)', fontSize: 13, marginBottom: 4 }}>Test Endpoints</div>
      <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 12 }}>
        Publish temporary <code>POST</code> endpoints to test event and schedule flows with synthetic payloads. No live events consumed.
      </div>

      {/* Event bindings */}
      {eventBindings.length > 0 && (
        <div style={{ marginBottom: 12 }}>
          <div style={{ fontSize: 11, fontWeight: 700, color: '#89b4fa', marginBottom: 6 }}>EVENT FLOWS</div>
          {eventBindings.map((eb: any) => {
            const flowName = eb.flow_name
            const alreadyPublished = testApis.some(a => a.flow_name === flowName)
            return (
              <div key={flowName} style={{
                display: 'flex', alignItems: 'center', gap: 8,
                padding: '6px 0', borderBottom: '1px solid var(--border)', fontSize: 12,
              }}>
                <span style={{ color: 'var(--text-muted)', fontFamily: 'monospace', flex: 1 }}>
                  {eb.publisher}/{eb.topic} → <strong>{flowName}</strong>
                </span>
                {alreadyPublished ? (
                  <span style={{ fontSize: 10, color: '#f59e0b', fontWeight: 700 }}>PUBLISHED</span>
                ) : (
                  <button className="btn" style={{ fontSize: 11 }}
                    onClick={() => publish(flowName)}
                    disabled={publishing === flowName}>
                    {publishing === flowName ? 'Publishing…' : 'Publish Test Endpoint'}
                  </button>
                )}
              </div>
            )
          })}
        </div>
      )}

      {/* Manual flow (for schedules and others) */}
      <div style={{ marginBottom: 12 }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: '#89b4fa', marginBottom: 6 }}>SCHEDULE / OTHER FLOWS</div>
        <div style={{ display: 'flex', gap: 6 }}>
          <input className="input" style={{ flex: 1, fontSize: 12 }}
            placeholder="flow_name to publish as test endpoint"
            value={manualFlow} onChange={e => setManualFlow(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && publish(manualFlow)}
          />
          <button className="btn btn-primary" style={{ fontSize: 12 }}
            onClick={() => publish(manualFlow)}
            disabled={!manualFlow.trim() || !!publishing}>
            {publishing ? 'Publishing…' : 'Publish'}
          </button>
        </div>
      </div>

      {err && <div style={{ fontSize: 11, color: '#f44336', marginBottom: 8 }}>{err}</div>}

      {/* Published test endpoints */}
      {!loading && testApis.length > 0 && (
        <div>
          <div style={{ fontSize: 11, fontWeight: 700, color: '#89b4fa', marginBottom: 6 }}>ACTIVE TEST ENDPOINTS</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {testApis.map(api => (
              <div key={api.name} style={{
                background: 'var(--block-bg)', border: '1px solid #f59e0b44',
                borderRadius: 6, padding: '8px 12px',
              }}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span style={{
                      fontSize: 10, fontWeight: 700, color: '#fff',
                      background: '#f59e0b', borderRadius: 3, padding: '1px 5px',
                    }}>TEST</span>
                    <span style={{ fontSize: 12, fontFamily: 'monospace', color: 'var(--text)' }}>{api.flow_name}</span>
                  </div>
                  <button className="btn" style={{ fontSize: 10, color: '#f87171' }}
                    onClick={() => remove(api)}
                    disabled={removing === api.name}>
                    {removing === api.name ? 'Removing…' : 'Remove'}
                  </button>
                </div>
                <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 4 }}>
                  Send a POST with your synthetic payload to test this flow:
                </div>
                <div style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  background: 'var(--bg)', borderRadius: 4, padding: '4px 8px',
                }}>
                  <span style={{ fontFamily: 'monospace', fontSize: 11, color: '#89b4fa', flex: 1, wordBreak: 'break-all' }}>
                    POST {gatewayBase}{api.path}
                  </span>
                  <button
                    className="btn" style={{ fontSize: 10, padding: '2px 6px' }}
                    onClick={() => navigator.clipboard.writeText(`${gatewayBase}${api.path}`)}
                    title="Copy URL">📋</button>
                </div>
                {/* Inline test invocation */}
                {(() => {
                  const ts = testStates[api.name] ?? { body: '', running: false, result: null, err: '' }
                  const statusColor = ts.result ? (ts.result.status < 400 ? '#4caf50' : '#f44336') : 'var(--text-muted)'
                  return (
                    <div style={{ marginTop: 8 }}>
                      <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>
                        Request body (JSON)
                      </div>
                      <textarea className="input"
                        style={{ width: '100%', height: 64, resize: 'vertical', fontSize: 11, fontFamily: 'monospace', boxSizing: 'border-box', marginBottom: 6 }}
                        placeholder={'{\n  "key": "value"\n}'}
                        value={ts.body}
                        onChange={e => updateTestState(api.name, { body: e.target.value })}
                      />
                      <button className="btn btn-primary" style={{ fontSize: 11 }}
                        onClick={() => runTest(api)} disabled={ts.running}>
                        {ts.running ? 'Sending…' : 'Send POST'}
                      </button>
                      {ts.err && <div style={{ marginTop: 6, fontSize: 11, color: '#f44336' }}>{ts.err}</div>}
                      {ts.result && (
                        <div style={{ marginTop: 6 }}>
                          <div style={{ fontSize: 10, color: 'var(--text-muted)', marginBottom: 3 }}>
                            Response — <span style={{ color: statusColor, fontWeight: 700 }}>{ts.result.status}</span>
                          </div>
                          <pre style={{
                            background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 4,
                            padding: 6, fontSize: 10, fontFamily: 'monospace',
                            maxHeight: 120, overflowY: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all', margin: 0,
                          }}>{ts.result.body}</pre>
                        </div>
                      )}
                    </div>
                  )
                })()}
                <div style={{ marginTop: 6, fontSize: 10, color: 'var(--text-muted)', fontFamily: 'monospace' }}>
                  curl -X POST {gatewayBase}{api.path} \<br/>
                  &nbsp;&nbsp;-H "Content-Type: application/json" \<br/>
                  &nbsp;&nbsp;-d '{`{"key":"value"}`}'
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
      {loading && <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>Loading…</div>}
    </div>
  )
}

// ── App detail panel ──────────────────────────────────────────────────────────

function ReleasesSection({ appName }: { appName: string }) {
  const [releases, setReleases]         = useState<AppRelease[]>([])
  const [loading, setLoading]           = useState(true)
  const [err, setErr]                   = useState('')
  const [showNewForm, setShowNewForm]   = useState(false)
  const [newVersion, setNewVersion]     = useState('')
  const [newChannel, setNewChannel]     = useState('production')
  const [newNotes, setNewNotes]         = useState('')
  const [creating, setCreating]         = useState(false)
  const [createErr, setCreateErr]       = useState('')
  const [promoting, setPromoting]       = useState<string | null>(null)
  const [promoteErr, setPromoteErr]     = useState('')
  // available APIs for this app (from gateway snapshot)
  const [appApis, setAppApis]           = useState<Array<{ name: string; path: string; method?: string; flow_name: string; endpoint_configs?: Array<{ flow_name?: string }> }>>([])
  // user-selected APIs for the draft release (all selected by default)
  const [selectedApis, setSelectedApis] = useState<string[]>([])

  useEffect(() => {
    fetchGatewaySnapshot().then(snap => {
      const apis = (snap.apis ?? []).filter(a => a.app_name === appName)
      setAppApis(apis)
      setSelectedApis(apis.map(a => a.name))
    }).catch(() => {})
  }, [appName])

  // Flows derived from the currently selected APIs
  const derivedFlows = Array.from(new Set(
    appApis
      .filter(a => selectedApis.includes(a.name))
      .flatMap(a => [
        a.flow_name,
        ...(a.endpoint_configs ?? []).map(ec => ec.flow_name).filter(Boolean) as string[],
      ])
      .filter(Boolean)
  )).sort()

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listAppReleases(appName)
      .then(r => setReleases((r.releases ?? []).sort((a, b) => b.created_at.localeCompare(a.created_at))))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [appName])

  useEffect(() => { load() }, [load])

  function toggleApi(name: string) {
    setSelectedApis(prev => prev.includes(name) ? prev.filter(n => n !== name) : [...prev, name])
  }

  async function handleCreate() {
    if (!newVersion.trim()) { setCreateErr('Version is required'); return }
    if (selectedApis.length === 0) { setCreateErr('Select at least one API for this release'); return }
    setCreating(true); setCreateErr('')
    try {
      await createAppRelease(appName, {
        version: newVersion.trim(),
        channel: newChannel.trim() || 'production',
        api_names: selectedApis,
        flow_names: derivedFlows,
        notes: newNotes.trim() || undefined,
      })
      setShowNewForm(false)
      setNewVersion(''); setNewNotes('')
      load()
    } catch (e) { setCreateErr(String(e)) }
    finally { setCreating(false) }
  }

  // Publish: deploy the release bundle to gateway, then mark as active
  async function handlePublish(rel: AppRelease) {
    setPromoting(rel.version); setPromoteErr('')
    try {
      const snap = await fetchGatewaySnapshot()
      const relApiNames = new Set(rel.api_names ?? [])
      const relFlowNames = new Set(rel.flow_names ?? [])
      const apis = snap.apis.filter(a => relApiNames.has(a.name))
      const flows = snap.flows.filter(f => relFlowNames.has(f.name))
      if (apis.length === 0 && flows.length === 0) {
        setPromoteErr('No APIs or flows found in this release — ensure they are still on the gateway')
        return
      }

      // All flow_names declared in the release must exist on the gateway
      const foundFlowNames = new Set(flows.map(f => f.name))
      const missingOnGateway = (rel.flow_names ?? []).filter(fn => !foundFlowNames.has(fn))
      if (missingOnGateway.length > 0) {
        setPromoteErr(`Cannot publish: flow(s) not found on gateway: ${missingOnGateway.join(', ')}. Sync the flows first.`)
        return
      }

      // Every API endpoint must reference only flows included in this release
      const missingFromRelease: string[] = []
      for (const api of apis) {
        for (const ep of (api as any).endpoint_configs ?? []) {
          if (ep.flow_name && !relFlowNames.has(ep.flow_name)) {
            missingFromRelease.push(`${api.name} → ${ep.flow_name}`)
          }
        }
      }
      if (missingFromRelease.length > 0) {
        setPromoteErr(`Cannot publish: API endpoint(s) reference flow(s) not in this release — add the missing flows or deselect the API:\n${missingFromRelease.join(', ')}`)
        return
      }

      await deploy({
        payload: { sync_uuid: crypto.randomUUID(), flows: flows as any, apis: apis.map(a => ({ ...a, action: 'upsert' })) as any },
        levels: [],
        target_names: [],
      })
      await promoteAppRelease(appName, rel.version, rel.channel)
      load()
    } catch (e) { setPromoteErr(String(e)) }
    finally { setPromoting(null) }
  }

  async function handleRollback(rel: AppRelease) {
    setPromoting(rel.version); setPromoteErr('')
    try {
      await rollbackAppRelease(appName, rel.version, rel.channel)
      load()
    } catch (e) { setPromoteErr(String(e)) }
    finally { setPromoting(null) }
  }

  const active = releases.find(r => r.active)

  return (
    <div style={{ marginBottom: 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <div style={{ fontWeight: 600, color: 'var(--accent)', fontSize: 13 }}>Publish & Releases</div>
          {active && (
            <span style={{
              fontSize: 10, padding: '2px 8px', borderRadius: 10,
              background: '#4ade8022', border: '1px solid #4ade80',
              color: '#4ade80', fontWeight: 700,
            }}>● LIVE v{active.version}</span>
          )}
        </div>
        <button className="btn" style={{ fontSize: 11 }} onClick={() => { setShowNewForm(v => !v); setCreateErr('') }}>
          {showNewForm ? 'Cancel' : '+ New Release'}
        </button>
      </div>

      {loading && <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>Loading…</div>}
      {err && <div style={{ fontSize: 12, color: '#f87171' }}>{err}</div>}
      {promoteErr && <div style={{ fontSize: 12, color: '#f87171', marginBottom: 6 }}>{promoteErr}</div>}

      {!loading && !err && releases.length === 0 && (
        <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>
          No releases yet. Draft a release by selecting APIs, then publish to deploy.
        </div>
      )}

      {/* Release cards */}
      {releases.map(rel => (
        <div key={rel.version} style={{
          background: 'var(--block-bg)', border: `1px solid ${rel.active ? '#4ade8066' : 'var(--border)'}`,
          borderRadius: 6, padding: '8px 12px', marginBottom: 6,
        }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <span style={{ fontWeight: 600, fontSize: 13 }}>v{rel.version}</span>
              <span style={{
                fontSize: 10, padding: '1px 6px', borderRadius: 3,
                background: rel.active ? '#4ade8033' : 'var(--border)',
                color: rel.active ? '#4ade80' : 'var(--text-muted)',
                fontWeight: rel.active ? 700 : 400,
              }}>{rel.channel}</span>
              {rel.active && <span style={{ fontSize: 10, color: '#4ade80', fontWeight: 700 }}>● PUBLISHED</span>}
            </div>
            <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{rel.created_at ? new Date(rel.created_at).toLocaleDateString() : ''}</span>
              {!rel.active && (
                <button className="btn btn-primary" style={{ fontSize: 10, padding: '2px 8px' }}
                  onClick={() => handlePublish(rel)}
                  disabled={promoting === rel.version}>
                  {promoting === rel.version ? 'Publishing…' : 'Publish'}
                </button>
              )}
              {rel.active && (
                <button className="btn" style={{ fontSize: 10, padding: '2px 8px', color: '#f59e0b' }}
                  onClick={() => handleRollback(rel)}
                  disabled={promoting === rel.version}
                  title="Roll back to previous active version">
                  Rollback
                </button>
              )}
            </div>
          </div>
          {/* APIs in this release */}
          {(rel.api_names ?? []).length > 0 && (
            <div style={{ marginBottom: 4 }}>
              <div style={{ fontSize: 10, color: 'var(--text-muted)', marginBottom: 3, fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.5 }}>APIs</div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                {(rel.api_names ?? []).map(n => (
                  <span key={n} style={{
                    background: '#89b4fa22', border: '1px solid #89b4fa44',
                    borderRadius: 3, padding: '1px 6px', fontFamily: 'monospace', fontSize: 11, color: '#89b4fa',
                  }}>{n}</span>
                ))}
              </div>
            </div>
          )}
          {/* Flows in this release */}
          {(rel.flow_names ?? []).length > 0 && (
            <div>
              <div style={{ fontSize: 10, color: 'var(--text-muted)', marginBottom: 3, fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.5 }}>Flows</div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                {(rel.flow_names ?? []).map(f => (
                  <span key={f} style={{
                    background: '#4ade8011', border: '1px solid #4ade8033',
                    borderRadius: 3, padding: '1px 6px', fontFamily: 'monospace', fontSize: 11, color: '#4ade80',
                  }}>{f}</span>
                ))}
              </div>
            </div>
          )}
          {rel.notes && <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 6, fontStyle: 'italic' }}>{rel.notes}</div>}
        </div>
      ))}

      {/* New Release form */}
      {showNewForm && (
        <div style={{
          background: 'var(--block-bg)', border: '1px solid var(--border)',
          borderRadius: 6, padding: 12, marginTop: 8,
        }}>
          <div style={{ fontWeight: 600, fontSize: 12, marginBottom: 10 }}>Draft New Release</div>

          {/* API selection */}
          <div style={{ marginBottom: 10 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', fontWeight: 600 }}>APIs to include</label>
              <div style={{ display: 'flex', gap: 6 }}>
                <button className="btn" style={{ fontSize: 10, padding: '1px 6px' }} onClick={() => setSelectedApis(appApis.map(a => a.name))}>All</button>
                <button className="btn" style={{ fontSize: 10, padding: '1px 6px' }} onClick={() => setSelectedApis([])}>None</button>
              </div>
            </div>
            {appApis.length === 0 ? (
              <div style={{ fontSize: 11, color: '#f59e0b', fontStyle: 'italic' }}>
                No APIs found for this app — add APIs first.
              </div>
            ) : (
              <div style={{
                border: '1px solid var(--border)', borderRadius: 4, overflow: 'hidden',
              }}>
                {appApis.map((api, i) => (
                  <label key={api.name} style={{
                    display: 'flex', alignItems: 'center', gap: 8, padding: '5px 10px',
                    cursor: 'pointer', fontSize: 12,
                    borderBottom: i < appApis.length - 1 ? '1px solid var(--border)' : undefined,
                    background: selectedApis.includes(api.name) ? '#89b4fa0a' : undefined,
                  }}>
                    <input type="checkbox"
                      checked={selectedApis.includes(api.name)}
                      onChange={() => { toggleApi(api.name); setCreateErr('') }} />
                    <span style={{
                      fontSize: 9, fontWeight: 700, color: '#fff', minWidth: 36, textAlign: 'center',
                      background: METHOD_COLORS[(api.method ?? 'ANY').toUpperCase()] ?? '#607d8b',
                      borderRadius: 3, padding: '1px 4px',
                    }}>{(api.method ?? 'ANY').toUpperCase()}</span>
                    <span style={{ fontFamily: 'monospace', fontSize: 11, flex: 1 }}>{api.path}</span>
                    <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>{api.name}</span>
                  </label>
                ))}
              </div>
            )}
          </div>

          {/* Flows derived from selected APIs (read-only) */}
          {derivedFlows.length > 0 && (
            <div style={{ marginBottom: 10 }}>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', fontWeight: 600, display: 'block', marginBottom: 4 }}>
                Flows included <span style={{ fontWeight: 400, fontStyle: 'italic' }}>(auto-derived from selected APIs)</span>
              </label>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                {derivedFlows.map(f => (
                  <span key={f} style={{
                    background: '#4ade8011', border: '1px solid #4ade8033',
                    borderRadius: 3, padding: '1px 7px', fontFamily: 'monospace', fontSize: 11, color: '#4ade80',
                  }}>{f}</span>
                ))}
              </div>
            </div>
          )}

          <div style={{ display: 'flex', gap: 8, marginBottom: 10 }}>
            <div style={{ flex: 1 }}>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Version *</label>
              <input className="input" style={{ width: '100%', boxSizing: 'border-box', fontSize: 12 }}
                placeholder="e.g. 1.0.0" value={newVersion}
                onChange={e => { setNewVersion(e.target.value); setCreateErr('') }} />
            </div>
            <div style={{ flex: 1 }}>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Channel</label>
              <select className="input" style={{ width: '100%', boxSizing: 'border-box', fontSize: 12 }}
                value={newChannel} onChange={e => setNewChannel(e.target.value)}>
                <option value="production">production</option>
                <option value="staging">staging</option>
              </select>
            </div>
          </div>

          <div style={{ marginBottom: 10 }}>
            <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Release notes</label>
            <input className="input" style={{ width: '100%', boxSizing: 'border-box', fontSize: 12 }}
              placeholder="What's in this release?" value={newNotes}
              onChange={e => setNewNotes(e.target.value)} />
          </div>

          {createErr && <div style={{ color: '#f87171', fontSize: 11, marginBottom: 8 }}>{createErr}</div>}

          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <button className="btn btn-primary" style={{ fontSize: 12 }} onClick={handleCreate} disabled={creating}>
              {creating ? 'Creating…' : 'Create Release Draft'}
            </button>
            <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>
              Then click <strong>Publish</strong> to deploy to gateway.
            </span>
          </div>
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
  directSyncEnabled?: boolean
}

function AppPanel({ app, onDeleted, flowNames = [], savedFlows = [], blocks = [], onSaveFlow, directSyncEnabled = true }: AppPanelProps) {
  const [designingFlow, setDesigningFlow] = useState<string | null>(null)
  const [testingApp, setTestingApp] = useState(false)
  const [editMode, setEditMode]     = useState(false)
  const [editName, setEditName]     = useState(app.name)
  const [editDesc, setEditDesc]     = useState(app.description)
  const [editType, setEditType]     = useState<string>(app.type ?? '')
  const [editTenantMode, setEditTenantMode] = useState<string>(app.tenant_mode ?? '')
  const [showAuth, setShowAuth]     = useState(!!app.flow_bindings)
  const [editLoginFlow, setEditLoginFlow]   = useState(app.flow_bindings?.login_flow ?? '')
  const [editLogoutFlow, setEditLogoutFlow] = useState(app.flow_bindings?.logout_flow ?? '')
  const [editCallbackFlow, setEditCallbackFlow] = useState(app.flow_bindings?.callback_flow ?? '')
  const [saveErr, setSaveErr]       = useState('')
  const [saving, setSaving]         = useState(false)
  const [delConfirm, setDelConfirm] = useState(false)
  const [delErr, setDelErr]         = useState('')
  const [showBlueprint, setShowBlueprint] = useState(false)
  const [gatewayBase, setGatewayBase] = useState('http://localhost:8080')

  // Reset local edit state when the selected app changes
  useEffect(() => {
    setEditName(app.name)
    setEditDesc(app.description)
    setEditType(app.type ?? '')
    setEditTenantMode(app.tenant_mode ?? '')
    setShowAuth(!!app.flow_bindings)
    setEditLoginFlow(app.flow_bindings?.login_flow ?? '')
    setEditLogoutFlow(app.flow_bindings?.logout_flow ?? '')
    setEditCallbackFlow(app.flow_bindings?.callback_flow ?? '')
    setEditMode(false)
    setSaveErr('')
    setDelConfirm(false)
    setDelErr('')
    setShowBlueprint(false)
  }, [app.app_id])

  async function handleSave() {
    setSaving(true); setSaveErr('')
    try {
      await updateApp(app.app_id, {
        name: editName.trim(),
        description: editDesc.trim(),
        ...(editType ? { type: editType as any } : {}),
        ...(editTenantMode ? { tenant_mode: editTenantMode as any } : {}),
        flow_bindings: showAuth ? {
          login_flow: editLoginFlow.trim() || undefined,
          logout_flow: editLogoutFlow.trim() || undefined,
          callback_flow: editCallbackFlow.trim() || undefined,
        } : null,
      })
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
          {/* App type */}
          <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>App type</label>
          <select className="input" style={{ width: '100%', marginBottom: 10, fontSize: 12 }}
            value={editType} onChange={e => setEditType(e.target.value)}>
            <option value="">— select type —</option>
            <option value="web">Web</option>
            <option value="api-service">API Service</option>
            <option value="event-processor">Event Processor</option>
            <option value="webhook">Webhook</option>
          </select>

          {/* Auth — off by default */}
          <div style={{ marginBottom: 10 }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 12, color: 'var(--text-muted)' }}>
              <input type="checkbox" checked={showAuth} onChange={e => setShowAuth(e.target.checked)} />
              Requires login (auth flows)
            </label>
          </div>
          {showAuth && (
            <div style={{ background: 'var(--bg)', borderRadius: 4, padding: 10, marginBottom: 10, border: '1px solid var(--border)' }}>
              <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 8 }}>Auth flows — leave blank to skip</div>
              {[
                { label: 'Login flow', val: editLoginFlow, set: setEditLoginFlow },
                { label: 'Logout flow', val: editLogoutFlow, set: setEditLogoutFlow },
                { label: 'Callback flow', val: editCallbackFlow, set: setEditCallbackFlow },
              ].map(({ label, val, set }) => (
                <div key={label} style={{ marginBottom: 6 }}>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 2 }}>{label}</div>
                  <input className="input" style={{ width: '100%', fontSize: 12, boxSizing: 'border-box' }}
                    placeholder="flow_name"
                    value={val} onChange={e => set(e.target.value)} />
                </div>
              ))}
            </div>
          )}

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

      {/* Test App panel — always visible as a collapsible section */}
      <div style={{ marginBottom: 16, background: testingApp ? 'var(--block-bg)' : undefined, border: `1px solid ${testingApp ? 'var(--accent)' : 'var(--border)'}`, borderRadius: 8, overflow: 'hidden' }}>
        <div
          style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '10px 16px', cursor: 'pointer' }}
          onClick={() => setTestingApp(t => !t)}
        >
          <div style={{ fontWeight: 700, fontSize: 13, color: testingApp ? 'var(--accent)' : 'var(--text)' }}>▶ Test App</div>
          <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{testingApp ? '▲ collapse' : '▼ expand'}</span>
        </div>
        {testingApp && (
          <div style={{ padding: '0 16px 16px' }}>
            <TestAppPanel app={app} gatewayBase={gatewayBase} />
          </div>
        )}
      </div>

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginBottom: 16 }}>
        <ReleasesSection appName={app.name} />
      </div>

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginBottom: 16 }}>
        <AppAPIsSection
          appName={app.name}
          gatewayBase={gatewayBase}
          flowNames={flowNames}
          onDesignNew={name => setDesigningFlow(name || `${app.name.replace(/-/g,'_')}_flow`)}
          directSyncEnabled={directSyncEnabled}
        />
      </div>

      {/* Gateway base URL — shared across all test/try sections */}
      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 12, marginBottom: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <span style={{ fontSize: 11, color: 'var(--text-muted)', whiteSpace: 'nowrap' }}>Gateway URL</span>
          <input className="input" style={{ flex: 1, fontSize: 11, fontFamily: 'monospace' }}
            value={gatewayBase} onChange={e => setGatewayBase(e.target.value)} />
        </div>
      </div>

      <div style={{ paddingTop: 16, marginBottom: 16 }}>
        <TryAPIsSection appName={app.name} gatewayBase={gatewayBase} />
      </div>

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginBottom: 16 }}>
        <TestFlowsSection app={app} gatewayBase={gatewayBase} />
      </div>

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginBottom: 16 }}>
        <AssetsSection appName={app.name} />
      </div>

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16 }}>
        <APIKeysSection app={app} />
      </div>
    </div>
  )
}

// ── Static assets section ────────────────────────────────────────────────────

function AssetsSection({ appName }: { appName: string }) {
  const [assets, setAssets]     = useState<AssetMeta[]>([])
  const [loading, setLoading]   = useState(true)
  const [err, setErr]           = useState('')
  const [uploading, setUploading] = useState(false)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listAssets(appName)
      .then(list => { setAssets(list ?? []); setUnavailable(false) })
      .catch(e => {
        const msg = String(e)
        if (msg.includes('503') || msg.includes('not configured')) setUnavailable(true)
        else setErr(msg)
      })
      .finally(() => setLoading(false))
  }, [appName])

  useEffect(() => { load() }, [load])

  async function handleUpload(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setUploading(true); setErr('')
    try {
      await uploadAsset(appName, file)
      load()
    } catch (ex) { setErr(String(ex)) }
    finally { setUploading(false); if (fileRef.current) fileRef.current.value = '' }
  }

  async function handleDelete(filename: string) {
    setDeleting(filename)
    try { await deleteAsset(appName, filename); load() }
    catch (ex) { setErr(String(ex)) }
    finally { setDeleting(null) }
  }

  if (unavailable) {
    return (
      <div>
        <div style={{ fontWeight: 600, color: 'var(--accent)', fontSize: 13, marginBottom: 8 }}>Static Assets</div>
        <div style={{ fontSize: 12, color: 'var(--text-muted)', background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 6, padding: '10px 14px' }}>
          Asset storage not configured. Start Studio with <code>--assets-dir ./rah-assets</code> to enable file hosting.
        </div>
      </div>
    )
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 }}>
        <div style={{ fontWeight: 600, color: 'var(--accent)', fontSize: 13 }}>Static Assets</div>
        <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          {uploading && <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>Uploading…</span>}
          <button className="btn" style={{ fontSize: 11 }} onClick={() => fileRef.current?.click()} disabled={uploading}>
            + Upload File
          </button>
          <input ref={fileRef} type="file" style={{ display: 'none' }} onChange={handleUpload} />
        </div>
      </div>
      {err && <div style={{ fontSize: 12, color: '#f87171', marginBottom: 8 }}>{err}</div>}
      {loading && <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>Loading…</div>}
      {!loading && assets.length === 0 && (
        <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>
          No files uploaded yet. Click "+ Upload File" to add HTML, JS, CSS or images.
        </div>
      )}
      {assets.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
          <thead>
            <tr style={{ color: 'var(--text-muted)', textAlign: 'left' }}>
              <th style={{ padding: '4px 8px', fontWeight: 500 }}>File</th>
              <th style={{ padding: '4px 8px', fontWeight: 500 }}>Type</th>
              <th style={{ padding: '4px 8px', fontWeight: 500 }}>Size</th>
              <th style={{ padding: '4px 8px', fontWeight: 500 }}>URL</th>
              <th style={{ padding: '4px 8px', fontWeight: 500 }}></th>
            </tr>
          </thead>
          <tbody>
            {assets.map(a => (
              <tr key={a.filename} style={{ borderTop: '1px solid var(--border)' }}>
                <td style={{ padding: '5px 8px', fontFamily: 'monospace' }}>{a.filename}</td>
                <td style={{ padding: '5px 8px', color: 'var(--text-muted)' }}>{a.content_type}</td>
                <td style={{ padding: '5px 8px', color: 'var(--text-muted)' }}>{(a.size / 1024).toFixed(1)} KB</td>
                <td style={{ padding: '5px 8px' }}>
                  <span
                    style={{ fontFamily: 'monospace', fontSize: 10, color: '#89b4fa', cursor: 'pointer' }}
                    title="Click to copy"
                    onClick={() => navigator.clipboard.writeText(window.location.origin + (a.url ?? ''))}
                  >{a.url} 📋</span>
                </td>
                <td style={{ padding: '5px 8px' }}>
                  <button
                    className="btn"
                    style={{ fontSize: 10, color: '#f87171' }}
                    disabled={deleting === a.filename}
                    onClick={() => handleDelete(a.filename)}
                  >{deleting === a.filename ? '…' : 'Delete'}</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

// ── Flow name autocomplete input (single selection, used in API forms) ───────

function FlowNameInput({
  value,
  onChange,
  flowNames,
  placeholder,
  onDesignNew,
}: {
  value: string
  onChange: (v: string) => void
  flowNames: string[]
  placeholder?: string
  onDesignNew?: (suggestedName: string) => void
}) {
  const [open, setOpen] = useState(false)
  const filtered = flowNames.filter(fn => !value || fn.toLowerCase().includes(value.toLowerCase()))

  return (
    <div style={{ position: 'relative' }}>
      <input
        className="input"
        style={{ width: '100%', fontSize: 12, boxSizing: 'border-box' }}
        placeholder={placeholder ?? 'Select or type flow name'}
        value={value}
        onChange={e => { onChange(e.target.value); setOpen(true) }}
        onFocus={() => setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 160)}
      />
      {open && (filtered.length > 0 || onDesignNew) && (
        <div style={{
          position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 300,
          background: 'var(--sidebar-bg, #0f1117)', border: '1px solid var(--border)',
          borderRadius: 4, marginTop: 2, maxHeight: 200, overflowY: 'auto',
          boxShadow: '0 4px 12px rgba(0,0,0,0.5)',
        }}>
          {filtered.slice(0, 12).map(fn => (
            <div key={fn} onMouseDown={() => { onChange(fn); setOpen(false) }}
              style={{ padding: '7px 10px', fontSize: 12, fontFamily: 'monospace', cursor: 'pointer' }}
              onMouseEnter={e => { (e.currentTarget as HTMLElement).style.background = 'var(--accent-bg)' }}
              onMouseLeave={e => { (e.currentTarget as HTMLElement).style.background = '' }}>
              {fn}
            </div>
          ))}
          {onDesignNew && (
            <div onMouseDown={() => { setOpen(false); onDesignNew(value) }}
              style={{
                padding: '7px 10px', fontSize: 12, cursor: 'pointer', color: 'var(--accent)',
                borderTop: filtered.length > 0 ? '1px solid var(--border)' : 'none',
              }}
              onMouseEnter={e => { (e.currentTarget as HTMLElement).style.background = 'var(--accent-bg)' }}
              onMouseLeave={e => { (e.currentTarget as HTMLElement).style.background = '' }}>
              + Create new flow{value ? ` "${value}"` : ''}…
            </div>
          )}
          {filtered.length === 0 && !onDesignNew && (
            <div style={{ padding: '7px 10px', fontSize: 12, color: 'var(--text-muted)' }}>No matching flows</div>
          )}
        </div>
      )}
    </div>
  )
}

// ── Flow picker autocomplete ──────────────────────────────────────────────────

interface FlowPickerProps {
  flowNames: string[]
  selected: string[]
  onAdd: (name: string) => void
  onRemove: (name: string) => void
  onNavigate?: (name: string) => void
  onDesignNew?: (suggestedName?: string) => void
}

function FlowPicker({ flowNames, selected, onAdd, onRemove, onNavigate, onDesignNew }: FlowPickerProps) {
  const [query, setQuery]         = useState('')
  const [showInput, setShowInput] = useState(selected.length === 0)
  const [open, setOpen]           = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const suggestions = flowNames
    .filter(fn => !selected.includes(fn))
    .filter(fn => !query || fn.toLowerCase().includes(query.toLowerCase()))

  function pick(name: string) {
    onAdd(name)
    setQuery('')
    setOpen(false)
    setShowInput(false)
  }

  function handleKey(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter' && suggestions.length >= 1) pick(suggestions[0])
    if (e.key === 'Escape') { setOpen(false); setShowInput(false) }
  }

  function showAdd() {
    setShowInput(true)
    setOpen(true)
    setTimeout(() => inputRef.current?.focus(), 30)
  }

  return (
    <div>
      {/* Selected flow pills */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: showInput ? 8 : 0 }}>
        {selected.map(fn => (
          <span key={fn} style={{
            display: 'inline-flex', alignItems: 'center', gap: 2,
            fontFamily: 'monospace', fontSize: 12,
            background: 'rgba(74,222,128,0.12)', border: '1px solid rgba(74,222,128,0.3)',
            borderRadius: 4, padding: '3px 6px',
          }}>
            {fn}
            {onNavigate && (
              <button onClick={() => onNavigate(fn)} title="Open flow in designer"
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#89b4fa', padding: '0 3px', fontSize: 12 }}>
                ↗
              </button>
            )}
            <button onClick={() => { onRemove(fn); if (selected.length === 1) setShowInput(true) }}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#f87171', padding: '0 2px', fontSize: 12 }}>
              ×
            </button>
          </span>
        ))}

        {/* + pill to trigger input */}
        {!showInput && (
          <button className="btn" onClick={showAdd}
            style={{ fontSize: 11, padding: '3px 8px', borderRadius: 4, minWidth: 'unset' }}>
            +
          </button>
        )}
      </div>

      {/* Autocomplete input */}
      {showInput && (
        <div style={{ position: 'relative', maxWidth: 340 }}>
          <input
            ref={inputRef}
            className="input"
            style={{ width: '100%', fontSize: 12, boxSizing: 'border-box' }}
            placeholder="Type to search flows…"
            value={query}
            autoFocus
            onChange={e => { setQuery(e.target.value); setOpen(true) }}
            onFocus={() => setOpen(true)}
            onBlur={() => setTimeout(() => { setOpen(false); if (!query) setShowInput(selected.length > 0) }, 160)}
            onKeyDown={handleKey}
          />
          {open && (suggestions.length > 0 || onDesignNew) && (
            <div style={{
              position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 200,
              background: 'var(--sidebar-bg, #0f1117)', border: '1px solid var(--border)',
              borderRadius: 4, marginTop: 2, maxHeight: 200, overflowY: 'auto', boxShadow: '0 4px 12px rgba(0,0,0,0.4)',
            }}>
              {suggestions.slice(0, 12).map(fn => (
                <div key={fn} onMouseDown={() => pick(fn)}
                  style={{ padding: '7px 10px', fontSize: 12, fontFamily: 'monospace', cursor: 'pointer' }}
                  onMouseEnter={e => { (e.currentTarget as HTMLElement).style.background = 'var(--accent-bg)' }}
                  onMouseLeave={e => { (e.currentTarget as HTMLElement).style.background = '' }}>
                  {fn}
                </div>
              ))}
              {onDesignNew && (
                <div onMouseDown={() => { setOpen(false); setShowInput(false); onDesignNew(query || undefined) }}
                  style={{
                    padding: '7px 10px', fontSize: 12, cursor: 'pointer', color: 'var(--accent)',
                    borderTop: suggestions.length > 0 ? '1px solid var(--border)' : 'none',
                  }}
                  onMouseEnter={e => { (e.currentTarget as HTMLElement).style.background = 'var(--accent-bg)' }}
                  onMouseLeave={e => { (e.currentTarget as HTMLElement).style.background = '' }}>
                  + Design new flow{query ? ` "${query}"` : ''}…
                </div>
              )}
              {suggestions.length === 0 && !onDesignNew && (
                <div style={{ padding: '7px 10px', fontSize: 12, color: 'var(--text-muted)' }}>No matching flows</div>
              )}
            </div>
          )}
        </div>
      )}
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
  onNavigateToFlow?: (flowName: string) => void
}

function NewAppForm({ onCreated, flowNames = [], savedFlows = [], blocks = [], onSaveFlow, onNavigateToFlow }: NewAppFormProps) {
  const [name, setName]         = useState('')
  const [desc, setDesc]         = useState('')
  const [appType, setAppType]   = useState<'web' | 'api-service' | 'event-processor' | 'webhook'>('api-service')
  const [authFlow, setAuthFlow] = useState<'oauth_code' | 'form_login' | 'none'>('none')
  const [basePath, setBasePath] = useState('')
  const [err, setErr]           = useState('')
  const [saving, setSaving]     = useState(false)

  // Phase 2 state
  const [createdName, setCreatedName]         = useState('')
  const [createdBasePath, setCreatedBasePath] = useState('')
  const [linkedFlows, setLinkedFlows]         = useState<string[]>([])
  const [relCreating, setRelCreating]         = useState(false)
  const [relErr, setRelErr]                   = useState('')
  const [relDone, setRelDone]                 = useState(false)
  const [designingFlow, setDesigningFlow]     = useState<string | null>(null)

  // Phase 2 — inline API creation
  const [showApiForm, setShowApiForm]   = useState(false)
  const [apiName, setApiName]           = useState('')
  const [apiPath, setApiPath]           = useState('')
  const [apiMethod, setApiMethod]       = useState('GET')
  const [apiFlow, setApiFlow]     = useState('')
  const [apiSaving, setApiSaving] = useState(false)
  const [apiErr, setApiErr]             = useState('')
  const [addedApis, setAddedApis]       = useState<{ name: string; method: string; path: string; flow: string }[]>([])

  // Phase 2 — event bindings (for event-processor / webhook)
  const [showEventForm, setShowEventForm] = useState(false)
  const [evPublisher, setEvPublisher]     = useState('')
  const [evTopic, setEvTopic]             = useState('')
  const [evFlow, setEvFlow]               = useState('')
  const [evErr, setEvErr]                 = useState('')
  const [addedEvents, setAddedEvents]     = useState<{ publisher: string; topic: string; flow_name: string }[]>([])

  async function handleCreate() {
    if (!name.trim()) { setErr('Name required'); return }
    setSaving(true); setErr('')
    const bp = basePath.trim() || `/${name.trim()}`
    try {
      await createApp({ name: name.trim(), description: desc.trim(), labels: { base_path: bp } })
      setCreatedName(name.trim())
      setCreatedBasePath(bp)
      // pre-fill API path with base path prefix
      setApiPath(bp + '/')
    } catch (e) { setErr(String(e)) }
    finally { setSaving(false) }
  }

  function suggestedFlowName(suffix: string) {
    return `${createdName.replace(/-/g, '_')}_${suffix}`
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

  async function handleAddApi() {
    if (!apiName.trim()) { setApiErr('Name required'); return }
    if (!apiPath.trim() || !apiPath.startsWith('/')) { setApiErr('Path must start with /'); return }
    if (!apiFlow.trim()) { setApiErr('Flow name required'); return }
    setApiSaving(true); setApiErr('')
    try {
      await syncFlows({
        sync_uuid: crypto.randomUUID(),
        flows: [],
        apis: [{
          name: apiName.trim(),
          path: apiPath.trim(),
          flow_name: apiFlow.trim(),
          app_name: createdName,
          action: 'upsert',
          endpoint_configs: [{ path: '/', method: apiMethod }],
        }],
      })
      setAddedApis(prev => [...prev, { name: apiName.trim(), method: apiMethod, path: apiPath.trim(), flow: apiFlow.trim() }])
      setApiName(''); setApiPath(createdBasePath + '/'); setApiMethod('GET'); setApiFlow('')
      setShowApiForm(false)
    } catch (ex) { setApiErr(String(ex)) }
    finally { setApiSaving(false) }
  }

  function handleAddEvent() {
    if (!evPublisher.trim() || !evTopic.trim() || !evFlow.trim()) { setEvErr('All fields required'); return }
    setAddedEvents(prev => [...prev, { publisher: evPublisher.trim(), topic: evTopic.trim(), flow_name: evFlow.trim() }])
    setEvPublisher(''); setEvTopic(''); setEvFlow(''); setShowEventForm(false); setEvErr('')
  }

  async function handleCreateRelease() {
    const flows = [...new Set([...linkedFlows, ...addedApis.map(a => a.flow)])]
    if (flows.length === 0) { setRelErr('Add at least one flow or API first'); return }
    setRelCreating(true); setRelErr('')
    try {
      await createAppRelease(createdName, { version: '1.0.0', channel: 'stable', flow_names: flows })
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

  // ── Phase 2: configure app ──
  if (createdName) {
    const showEvents = appType === 'event-processor' || appType === 'webhook'

    return (
      <div style={{ padding: 20, maxWidth: 700 }}>
        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 24 }}>
          <span style={{ fontSize: 20, color: '#4ade80' }}>✓</span>
          <div>
            <div style={{ fontWeight: 700, fontSize: 16 }}>"{createdName}" created</div>
            <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
              {appType} · base path: <code style={{ fontFamily: 'monospace' }}>{createdBasePath}</code>
            </div>
          </div>
          <button className="btn" style={{ marginLeft: 'auto', fontSize: 12 }} onClick={() => onCreated(createdName)}>
            Open App →
          </button>
        </div>

        {/* ── APIs section ── */}
        <div style={{ marginBottom: 20, background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 8, padding: 16 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
            <div style={{ fontWeight: 600, fontSize: 13, color: 'var(--accent)' }}>APIs</div>
            {!showApiForm && (
              <button className="btn" style={{ fontSize: 11 }} onClick={() => setShowApiForm(true)}>+ Add API</button>
            )}
          </div>

          {showApiForm && (
            <div style={{ background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, padding: 14, marginBottom: 12 }}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginBottom: 8 }}>
                <div>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Name</div>
                  <input className="input" style={{ width: '100%', fontSize: 12, boxSizing: 'border-box' }}
                    placeholder={`${createdName}-list`}
                    value={apiName} onChange={e => setApiName(e.target.value)} />
                </div>
                <div>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Method</div>
                  <select className="input" style={{ width: '100%', fontSize: 12 }}
                    value={apiMethod} onChange={e => setApiMethod(e.target.value)}>
                    {HTTP_METHODS.map(m => <option key={m} value={m}>{m}</option>)}
                  </select>
                </div>
                <div>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Path</div>
                  <input className="input" style={{ width: '100%', fontSize: 12, boxSizing: 'border-box' }}
                    placeholder={`${createdBasePath}/items`}
                    value={apiPath} onChange={e => setApiPath(e.target.value)} />
                </div>
                <div>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Flow</div>
                  <FlowNameInput
                    value={apiFlow}
                    onChange={setApiFlow}
                    flowNames={flowNames}
                    placeholder="Select or type flow name…"
                    onDesignNew={name => {
                      setShowApiForm(false)
                      setDesigningFlow(name || suggestedFlowName('handler'))
                    }}
                  />
                </div>
              </div>
              {apiErr && <div style={{ fontSize: 11, color: '#f87171', marginBottom: 6 }}>{apiErr}</div>}
              <div style={{ display: 'flex', gap: 6 }}>
                <button className="btn btn-primary" style={{ fontSize: 11 }} onClick={handleAddApi} disabled={apiSaving}>
                  {apiSaving ? 'Saving…' : 'Save API'}
                </button>
                <button className="btn" style={{ fontSize: 11 }} onClick={() => { setShowApiForm(false); setApiErr('') }}>Cancel</button>
              </div>
            </div>
          )}

          {addedApis.length === 0 && !showApiForm ? (
            <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>
              No APIs yet. Click "+ Add API" to register an HTTP endpoint for this app.
            </div>
          ) : addedApis.length > 0 && (
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={{ color: 'var(--text-muted)', textAlign: 'left' }}>
                  <th style={{ padding: '3px 6px', fontWeight: 500 }}>Method</th>
                  <th style={{ padding: '3px 6px', fontWeight: 500 }}>Path</th>
                  <th style={{ padding: '3px 6px', fontWeight: 500 }}>Flow</th>
                </tr>
              </thead>
              <tbody>
                {addedApis.map((a, i) => (
                  <tr key={i} style={{ borderTop: '1px solid var(--border)' }}>
                    <td style={{ padding: '4px 6px' }}>
                      <span style={{ background: METHOD_COLORS[a.method] ?? '#607d8b', color: '#fff', borderRadius: 3, padding: '1px 5px', fontSize: 10, fontWeight: 600 }}>{a.method}</span>
                    </td>
                    <td style={{ padding: '4px 6px', fontFamily: 'monospace' }}>{a.path}</td>
                    <td style={{ padding: '4px 6px', color: 'var(--text-muted)', fontFamily: 'monospace' }}>{a.flow}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        {/* ── Events section (event-processor / webhook) ── */}
        {showEvents && (
          <div style={{ marginBottom: 20, background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 8, padding: 16 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
              <div style={{ fontWeight: 600, fontSize: 13, color: 'var(--accent)' }}>Events</div>
              {!showEventForm && (
                <button className="btn" style={{ fontSize: 11 }} onClick={() => setShowEventForm(true)}>+ Add Event</button>
              )}
            </div>
            {showEventForm && (
              <div style={{ background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, padding: 14, marginBottom: 12 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 8, marginBottom: 8 }}>
                  <div>
                    <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Publisher</div>
                    <input className="input" style={{ width: '100%', fontSize: 12, boxSizing: 'border-box' }}
                      placeholder="kafka" value={evPublisher} onChange={e => setEvPublisher(e.target.value)} />
                  </div>
                  <div>
                    <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Topic</div>
                    <input className="input" style={{ width: '100%', fontSize: 12, boxSizing: 'border-box' }}
                      placeholder="orders.created" value={evTopic} onChange={e => setEvTopic(e.target.value)} />
                  </div>
                  <div>
                    <div style={{ fontSize: 11, color: 'var(--text-muted)', marginBottom: 3 }}>Flow name</div>
                    <input className="input" style={{ width: '100%', fontSize: 12, boxSizing: 'border-box' }}
                      placeholder={suggestedFlowName('on_event')} value={evFlow} onChange={e => setEvFlow(e.target.value)} />
                  </div>
                </div>
                {evErr && <div style={{ fontSize: 11, color: '#f87171', marginBottom: 6 }}>{evErr}</div>}
                <div style={{ display: 'flex', gap: 6 }}>
                  <button className="btn btn-primary" style={{ fontSize: 11 }} onClick={handleAddEvent}>Add Event</button>
                  <button className="btn" style={{ fontSize: 11 }} onClick={() => { setShowEventForm(false); setEvErr('') }}>Cancel</button>
                </div>
              </div>
            )}
            {addedEvents.length === 0 && !showEventForm ? (
              <div style={{ fontSize: 12, color: 'var(--text-muted)', fontStyle: 'italic' }}>No event bindings yet.</div>
            ) : addedEvents.length > 0 && (
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                <thead>
                  <tr style={{ color: 'var(--text-muted)', textAlign: 'left' }}>
                    <th style={{ padding: '3px 6px', fontWeight: 500 }}>Publisher</th>
                    <th style={{ padding: '3px 6px', fontWeight: 500 }}>Topic</th>
                    <th style={{ padding: '3px 6px', fontWeight: 500 }}>Flow</th>
                  </tr>
                </thead>
                <tbody>
                  {addedEvents.map((ev, i) => (
                    <tr key={i} style={{ borderTop: '1px solid var(--border)' }}>
                      <td style={{ padding: '4px 6px', fontFamily: 'monospace' }}>{ev.publisher}</td>
                      <td style={{ padding: '4px 6px', fontFamily: 'monospace' }}>{ev.topic}</td>
                      <td style={{ padding: '4px 6px', color: 'var(--text-muted)', fontFamily: 'monospace' }}>{ev.flow_name}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}

        {/* ── Flows section ── */}
        <div style={{ marginBottom: 20, background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 8, padding: 16 }}>
          <div style={{ fontWeight: 600, fontSize: 13, color: 'var(--accent)', marginBottom: 10 }}>Flows</div>
          <FlowPicker
            flowNames={flowNames}
            selected={linkedFlows}
            onAdd={fn => setLinkedFlows(prev => prev.includes(fn) ? prev : [...prev, fn])}
            onRemove={fn => setLinkedFlows(prev => prev.filter(f => f !== fn))}
            onNavigate={onNavigateToFlow}
            onDesignNew={suggested => openDesigner(suggested?.replace(/\s+/g, '_') ?? 'new_flow')}
          />
        </div>

        {/* ── Static Assets section ── */}
        <div style={{ marginBottom: 20, background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 8, padding: 16 }}>
          <AssetsSection appName={createdName} />
        </div>

        {/* ── Publish release ── */}
        <div style={{ background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 8, padding: 16 }}>
          <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 10 }}>Publish Release</div>
          {relErr && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 8 }}>{relErr}</div>}
          {!relDone ? (
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <button
                className="btn"
                style={{ background: (addedApis.length + linkedFlows.length) > 0 ? 'var(--accent)' : undefined }}
                onClick={handleCreateRelease}
                disabled={relCreating}
              >
                {relCreating ? 'Creating release…' : `Publish v1.0.0 (${addedApis.length + linkedFlows.length} flows)`}
              </button>
              <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>or</span>
              <button className="btn" onClick={() => onCreated(createdName)}>Skip → Open App</button>
            </div>
          ) : (
            <div>
              <div style={{ color: '#4ade80', fontSize: 13, marginBottom: 10 }}>✓ Release v1.0.0 published</div>
              <button className="btn" onClick={() => onCreated(createdName)}>Open App →</button>
            </div>
          )}
        </div>
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
        onChange={e => { setName(e.target.value); setBasePath('/' + e.target.value.trim()); setErr('') }}
      />

      <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Description</label>
      <textarea
        className="input"
        style={{ width: '100%', height: 60, marginBottom: 12, resize: 'vertical', boxSizing: 'border-box' }}
        placeholder="What is this app for?"
        value={desc}
        onChange={e => setDesc(e.target.value)}
      />

      <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>Base Path</label>
      <input
        className="input"
        style={{ width: '100%', marginBottom: 16, boxSizing: 'border-box', fontFamily: 'monospace' }}
        placeholder="/school-mgmt"
        value={basePath}
        onChange={e => setBasePath(e.target.value)}
      />
      <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: -12, marginBottom: 16 }}>
        All APIs will use this prefix by default (e.g. /school-mgmt/students)
      </div>

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
              { value: 'none',       label: 'No login',            desc: 'Public app — no authentication required' },
              { value: 'oauth_code', label: 'OAuth 2.0 Auth Code', desc: 'Redirect to provider, exchange code for tokens' },
              { value: 'form_login', label: 'Form Login',          desc: 'Username/password, session cookie, no external provider' },
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
  initialAppName?: string | null
  onAppNavConsumed?: () => void
  onNavigateToFlow?: (flowName: string) => void
}

export default function Apps({ flowNames = [], savedFlows = [], blocks = [], onSaveFlow, initialAppName, onAppNavConsumed, onNavigateToFlow }: AppsProps) {
  const [apps, setApps]                   = useState<App[]>([])
  const [loading, setLoading]             = useState(true)
  const [err, setErr]                     = useState('')
  const [selected, setSelected]           = useState<App | null>(null)
  const [view, setView]                   = useState<'list' | 'new'>('list')
  const [directSyncEnabled, setDirectSyncEnabled] = useState(true)

  useEffect(() => {
    fetchStudioConfig()
      .then(cfg => setDirectSyncEnabled(cfg.direct_sync_enabled))
      .catch(() => {}) // default to true (direct sync) if endpoint not available
  }, [])
  const [liveAppNames, setLiveAppNames] = useState<Set<string>>(new Set())

  const loadApps = useCallback(() => {
    setLoading(true); setErr('')
    Promise.all([
      listApps(),
      listAppUsages().catch(() => ({ flow_usages: {} })),
      fetchGatewaySnapshot().catch(() => ({ flows: [], apis: [] })),
    ])
      .then(([registered, usages, snap]) => {
        const all = [...(registered ?? [])]
        const registeredNames = new Set(all.map(a => a.name))

        // Discover apps from rah-sync flow_usages
        const syncedNames = new Set<string>()
        for (const entries of Object.values(usages.flow_usages ?? {})) {
          for (const e of entries) if (e.app_name) syncedNames.add(e.app_name)
        }
        for (const name of syncedNames) {
          if (!registeredNames.has(name)) {
            all.push({ app_id: 0, name, description: 'Deployed via rah-sync', labels: { source: 'sync' }, created_at: 0, updated_at: 0 })
            registeredNames.add(name)
          }
        }

        // Discover apps from gateway APIs that have app_name set
        const gatewayNames = new Set((snap.apis ?? []).map((a: any) => a.app_name).filter(Boolean) as string[])
        setLiveAppNames(gatewayNames)
        for (const name of gatewayNames) {
          if (!registeredNames.has(name)) {
            all.push({ app_id: 0, name, description: 'Discovered from gateway APIs', labels: { source: 'gateway' }, created_at: 0, updated_at: 0 })
            registeredNames.add(name)
          }
        }

        setApps(all)
      })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { loadApps() }, [loadApps])

  useEffect(() => {
    if (initialAppName && apps.length > 0) {
      const target = apps.find(a => a.name === initialAppName)
      if (target) {
        setSelected(target)
        setView('list')
        onAppNavConsumed?.()
      }
    }
  }, [initialAppName, apps])

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
                  {liveAppNames.has(app.name) && <span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: '50%', background: '#4ade80', marginRight: 2 }}></span>}
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
            onNavigateToFlow={onNavigateToFlow}
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
            directSyncEnabled={directSyncEnabled}
          />
        )}
        {view === 'list' && !selected && !loading && (
          <div className="panel-empty">Select an app to view details</div>
        )}
      </div>
    </div>
  )
}
