import { useEffect, useState, useCallback } from 'react'
import {
  listTenants, getTenant, upsertTenant, deleteTenant, addAlias,
  listRateLimitConfigs, upsertRateLimitConfig,
  listCredentials, setCredential, deleteCredential,
} from '../api'
import type {
  TenantSummary, TenantDetail, RateLimitRecord, UpsertTenantRequest,
} from '../types'

// ── helpers ──────────────────────────────────────────────────────────────────

function groupProperties(props: Record<string, string>) {
  const urls: Record<string, string> = {}
  const ids: Record<string, string> = {}
  const meta: Record<string, string> = {}
  for (const [k, v] of Object.entries(props)) {
    if (k.startsWith('url:'))  urls[k.slice(4)]  = v
    else if (k.startsWith('id:'))  ids[k.slice(3)]   = v
    else if (k.startsWith('meta:')) meta[k.slice(5)] = v
  }
  return { urls, ids, meta }
}

// ── sub-components ────────────────────────────────────────────────────────────

function KVTable({ title, data }: { title: string; data: Record<string, string> }) {
  const rows = Object.entries(data)
  if (rows.length === 0) return null
  return (
    <div style={{ marginBottom: 16 }}>
      <div style={{ fontWeight: 600, marginBottom: 4, color: 'var(--accent)' }}>{title}</div>
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
        <tbody>
          {rows.map(([k, v]) => (
            <tr key={k} style={{ borderBottom: '1px solid var(--border)' }}>
              <td style={{ padding: '4px 8px', color: 'var(--text-muted)', width: '35%', wordBreak: 'break-all' }}>{k}</td>
              <td style={{ padding: '4px 8px', wordBreak: 'break-all' }}>{v}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ── Credentials panel ─────────────────────────────────────────────────────────

const LLM_PRESETS = ['llm:openai', 'llm:anthropic', 'llm:gemini', 'llm:ollama']
const MCP_PRESETS = ['mcp:brave-search', 'mcp:e2b']
const ALL_PRESETS = [...LLM_PRESETS, ...MCP_PRESETS]

function groupCredentials(names: string[]) {
  const llm: string[] = []
  const mcp: string[] = []
  const other: string[] = []
  for (const n of names) {
    if (n.startsWith('llm:'))      llm.push(n)
    else if (n.startsWith('mcp:')) mcp.push(n)
    else                            other.push(n)
  }
  return { llm, mcp, other }
}

function CredentialsPanel({ alias }: { alias: string }) {
  const [names, setNames]       = useState<string[]>([])
  const [loading, setLoading]   = useState(true)
  const [err, setErr]           = useState('')

  // add-credential form state
  const [credName, setCredName]   = useState('')
  const [credValue, setCredValue] = useState('')
  const [saving, setSaving]       = useState(false)
  const [saveMsg, setSaveMsg]     = useState('')
  const [saveErr, setSaveErr]     = useState('')

  // delete confirmation: name of cred awaiting confirm, or ''
  const [delConfirm, setDelConfirm] = useState('')
  const [delErr, setDelErr]         = useState('')

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listCredentials(alias)
      .then(r => setNames(r.credentials ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [alias])

  useEffect(() => { load() }, [load])

  async function handleSet() {
    const n = credName.trim()
    const v = credValue.trim()
    if (!n) { setSaveErr('Credential name required'); return }
    if (!v) { setSaveErr('Value required'); return }
    setSaving(true); setSaveErr(''); setSaveMsg('')
    try {
      await setCredential(alias, n, v)
      setCredName(''); setCredValue('')
      setSaveMsg('Key saved.')
      load()
    } catch (e) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  async function handleDelete(name: string) {
    if (delConfirm !== name) { setDelConfirm(name); setDelErr(''); return }
    try {
      await deleteCredential(alias, name)
      setDelConfirm('')
      load()
    } catch (e) { setDelErr(String(e)) }
  }

  const { llm, mcp, other } = groupCredentials(names)

  function CredGroup({ title, items }: { title: string; items: string[] }) {
    if (items.length === 0) return null
    return (
      <div style={{ marginBottom: 16 }}>
        <div style={{ fontWeight: 600, marginBottom: 4, color: 'var(--accent)', fontSize: 13 }}>{title}</div>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <tbody>
            {items.map(n => (
              <tr key={n} style={{ borderBottom: '1px solid var(--border)' }}>
                <td style={{ padding: '5px 8px', wordBreak: 'break-all' }}>
                  <span style={{
                    background: 'var(--block-bg)', border: '1px solid var(--border)',
                    borderRadius: 4, padding: '2px 8px', fontSize: 12,
                  }}>{n}</span>
                </td>
                <td style={{ padding: '5px 8px', color: 'var(--text-muted)', fontFamily: 'monospace', letterSpacing: 2 }}>
                  ••••••••
                </td>
                <td style={{ padding: '5px 8px', textAlign: 'right', whiteSpace: 'nowrap' }}>
                  {delErr && delConfirm === n && (
                    <span style={{ color: '#f87171', fontSize: 11, marginRight: 6 }}>{delErr}</span>
                  )}
                  <button
                    className="btn"
                    style={{ fontSize: 11, background: delConfirm === n ? '#ef4444' : undefined }}
                    onClick={() => handleDelete(n)}
                  >
                    {delConfirm === n ? 'Confirm delete' : 'Delete'}
                  </button>
                  {delConfirm === n && (
                    <button
                      className="btn"
                      style={{ fontSize: 11, marginLeft: 4 }}
                      onClick={() => setDelConfirm('')}
                    >Cancel</button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <div>
      {loading && <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 12 }}>Loading…</div>}
      {err && <div style={{ color: '#f87171', fontSize: 13, marginBottom: 12 }}>{err}</div>}

      {!loading && names.length === 0 && !err && (
        <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 16 }}>No credentials stored.</div>
      )}

      <CredGroup title="LLM Keys" items={llm} />
      <CredGroup title="MCP Keys" items={mcp} />
      <CredGroup title="Other" items={other} />

      {/* Add credential form */}
      <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14, marginTop: 4 }}>
        <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 10 }}>Set Credential</div>
        <div style={{ display: 'flex', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
          <div style={{ position: 'relative', flex: '1 1 180px', minWidth: 160 }}>
            <input
              className="input"
              style={{ width: '100%', boxSizing: 'border-box' }}
              list="cred-name-presets"
              placeholder="llm:openai or custom…"
              value={credName}
              onChange={e => { setCredName(e.target.value); setSaveMsg(''); setSaveErr('') }}
            />
            <datalist id="cred-name-presets">
              {ALL_PRESETS.map(p => <option key={p} value={p} />)}
            </datalist>
          </div>
          <input
            className="input"
            style={{ flex: '2 1 220px', minWidth: 180 }}
            type="password"
            placeholder="Value (write-only)"
            value={credValue}
            onChange={e => { setCredValue(e.target.value); setSaveMsg(''); setSaveErr('') }}
            onKeyDown={e => e.key === 'Enter' && handleSet()}
          />
          <button className="btn" onClick={handleSet} disabled={saving} style={{ whiteSpace: 'nowrap' }}>
            {saving ? 'Saving…' : 'Set Key'}
          </button>
        </div>
        {saveErr && <div style={{ color: '#f87171', fontSize: 12 }}>{saveErr}</div>}
        {saveMsg && <div style={{ color: '#4ade80', fontSize: 12 }}>{saveMsg}</div>}
      </div>
    </div>
  )
}

// ── Tenant detail panel ───────────────────────────────────────────────────────

function TenantPanel({ alias, onDeleted }: { alias: string; onDeleted: () => void }) {
  const [detail, setDetail]   = useState<TenantDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr]         = useState('')
  const [newAlias, setNewAlias] = useState('')
  const [aliasErr, setAliasErr] = useState('')
  const [delConfirm, setDelConfirm] = useState(false)
  const [tab, setTab] = useState<'properties' | 'credentials'>('properties')

  const load = useCallback(() => {
    setLoading(true); setErr('')
    getTenant(alias)
      .then(setDetail)
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [alias])

  useEffect(() => { load() }, [load])

  async function handleAddAlias() {
    if (!newAlias.trim()) return
    setAliasErr('')
    try {
      await addAlias(alias, newAlias.trim())
      setNewAlias('')
      load()
    } catch (e) { setAliasErr(String(e)) }
  }

  async function handleDelete() {
    if (!delConfirm) { setDelConfirm(true); return }
    try {
      await deleteTenant(alias)
      onDeleted()
    } catch (e) { setErr(String(e)) }
  }

  if (loading) return <div className="panel-empty">Loading…</div>
  if (err)     return <div className="panel-empty" style={{ color: '#f87171' }}>{err}</div>
  if (!detail) return null

  const { urls, ids, meta } = groupProperties(detail.properties)

  return (
    <div style={{ padding: 20, overflowY: 'auto', height: '100%', boxSizing: 'border-box' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <div style={{ fontSize: 18, fontWeight: 700 }}>{alias}</div>
          <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>TenantID: {detail.tenant_id}</div>
        </div>
        <button
          className="btn"
          style={{ background: delConfirm ? '#ef4444' : undefined, fontSize: 12 }}
          onClick={handleDelete}
        >
          {delConfirm ? 'Confirm delete' : 'Delete tenant'}
        </button>
      </div>

      {/* Aliases */}
      <div style={{ marginBottom: 16 }}>
        <div style={{ fontWeight: 600, marginBottom: 6, color: 'var(--accent)' }}>Aliases</div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8 }}>
          {detail.aliases?.map(a => (
            <span key={a} style={{
              background: 'var(--block-bg)', border: '1px solid var(--border)',
              borderRadius: 4, padding: '2px 8px', fontSize: 13,
            }}>{a}</span>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <input
            className="input"
            style={{ flex: 1 }}
            placeholder="Add alias…"
            value={newAlias}
            onChange={e => setNewAlias(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && handleAddAlias()}
          />
          <button className="btn" onClick={handleAddAlias}>Add</button>
        </div>
        {aliasErr && <div style={{ color: '#f87171', fontSize: 12, marginTop: 4 }}>{aliasErr}</div>}
      </div>

      {/* Tab switcher */}
      <div style={{ display: 'flex', gap: 4, marginBottom: 16, borderBottom: '1px solid var(--border)', paddingBottom: 0 }}>
        <button
          className={`tab-btn${tab === 'properties' ? ' active' : ''}`}
          style={{ fontSize: 12 }}
          onClick={() => setTab('properties')}
        >Properties</button>
        <button
          className={`tab-btn${tab === 'credentials' ? ' active' : ''}`}
          style={{ fontSize: 12 }}
          onClick={() => setTab('credentials')}
        >Credentials</button>
      </div>

      {/* Properties tab */}
      {tab === 'properties' && (
        <>
          <KVTable title="Service URLs" data={urls} />
          <KVTable title="Identifiers"  data={ids} />
          <KVTable title="Metadata"     data={meta} />

          {Object.keys(detail.properties).length === 0 && (
            <div style={{ color: 'var(--text-muted)', fontSize: 13 }}>No properties stored.</div>
          )}
        </>
      )}

      {/* Credentials tab */}
      {tab === 'credentials' && (
        <CredentialsPanel alias={alias} />
      )}
    </div>
  )
}

// ── New tenant form ───────────────────────────────────────────────────────────

function NewTenantForm({ onCreated }: { onCreated: () => void }) {
  const [aliases, setAliases]   = useState('')
  const [urlsRaw, setUrlsRaw]   = useState('')
  const [idsRaw, setIdsRaw]     = useState('')
  const [metaRaw, setMetaRaw]   = useState('')
  const [err, setErr]           = useState('')
  const [saving, setSaving]     = useState(false)

  function parseKV(raw: string): Record<string, string> {
    const out: Record<string, string> = {}
    for (const line of raw.split('\n')) {
      const eq = line.indexOf('=')
      if (eq < 1) continue
      out[line.slice(0, eq).trim()] = line.slice(eq + 1).trim()
    }
    return out
  }

  async function handleSave() {
    const aliasList = aliases.split(',').map(a => a.trim()).filter(Boolean)
    if (aliasList.length === 0) { setErr('At least one alias required'); return }
    setSaving(true); setErr('')
    const body: UpsertTenantRequest = {
      aliases: aliasList,
      service_urls: parseKV(urlsRaw) || undefined,
      identifiers:  parseKV(idsRaw)  || undefined,
      metadata:     parseKV(metaRaw) || undefined,
    }
    try {
      await upsertTenant(body)
      setAliases(''); setUrlsRaw(''); setIdsRaw(''); setMetaRaw('')
      onCreated()
    } catch (e) { setErr(String(e)) }
    finally { setSaving(false) }
  }

  return (
    <div style={{ padding: 20 }}>
      <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 16 }}>New Tenant</div>

      <label style={{ fontSize: 12, color: 'var(--text-muted)' }}>Aliases (comma-separated)</label>
      <input className="input" style={{ width: '100%', marginBottom: 12 }}
        placeholder="acme.com, acme-prod, acme-staging"
        value={aliases} onChange={e => setAliases(e.target.value)} />

      <label style={{ fontSize: 12, color: 'var(--text-muted)' }}>Service URLs (name=url, one per line)</label>
      <textarea className="input" style={{ width: '100%', height: 72, marginBottom: 12, resize: 'vertical' }}
        placeholder={'primary=https://api.acme.com\nfallback=https://backup.acme.com'}
        value={urlsRaw} onChange={e => setUrlsRaw(e.target.value)} />

      <label style={{ fontSize: 12, color: 'var(--text-muted)' }}>Identifiers (name=value, one per line)</label>
      <textarea className="input" style={{ width: '100%', height: 60, marginBottom: 12, resize: 'vertical' }}
        placeholder="api_key=sk-abc123"
        value={idsRaw} onChange={e => setIdsRaw(e.target.value)} />

      <label style={{ fontSize: 12, color: 'var(--text-muted)' }}>Metadata (key=value, one per line)</label>
      <textarea className="input" style={{ width: '100%', height: 60, marginBottom: 16, resize: 'vertical' }}
        placeholder={'tier=premium\nregion=us-east'}
        value={metaRaw} onChange={e => setMetaRaw(e.target.value)} />

      {err && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 8 }}>{err}</div>}
      <button className="btn" onClick={handleSave} disabled={saving}>
        {saving ? 'Saving…' : 'Create Tenant'}
      </button>
    </div>
  )
}

// ── Rate limit configs panel ──────────────────────────────────────────────────

function RateLimitPanel() {
  const [configs, setConfigs] = useState<RateLimitRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr]         = useState('')
  const [name, setName]       = useState('')
  const [perSec, setPerSec]   = useState('')
  const [perMin, setPerMin]   = useState('')
  const [burst, setBurst]     = useState('100')
  const [saving, setSaving]   = useState(false)
  const [saveErr, setSaveErr] = useState('')

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listRateLimitConfigs()
      .then(r => setConfigs(r.items ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  async function handleSave() {
    if (!name.trim()) { setSaveErr('Name required'); return }
    setSaving(true); setSaveErr('')
    try {
      await upsertRateLimitConfig({
        name: name.trim(),
        per_sec: parseInt(perSec) || 0,
        per_min: parseInt(perMin) || 0,
        burst_factor: parseInt(burst) || 100,
      })
      setName(''); setPerSec(''); setPerMin(''); setBurst('100')
      load()
    } catch (e) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  return (
    <div style={{ padding: 20 }}>
      <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 16 }}>Rate Limit Configs</div>

      {loading && <div style={{ color: 'var(--text-muted)' }}>Loading…</div>}
      {err && <div style={{ color: '#f87171', fontSize: 13, marginBottom: 8 }}>{err}</div>}

      {configs.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13, marginBottom: 24 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)', color: 'var(--text-muted)' }}>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Name</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Per Sec</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Per Min</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Burst</th>
            </tr>
          </thead>
          <tbody>
            {configs.map(c => (
              <tr key={c.name} style={{ borderBottom: '1px solid var(--border)' }}>
                <td style={{ padding: '4px 8px', fontWeight: 600 }}>{c.name}</td>
                <td style={{ padding: '4px 8px', textAlign: 'right' }}>{c.config.per_sec || '—'}</td>
                <td style={{ padding: '4px 8px', textAlign: 'right' }}>{c.config.per_min || '—'}</td>
                <td style={{ padding: '4px 8px', textAlign: 'right' }}>{c.config.burst_factor || 100}%</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <div style={{ fontWeight: 600, marginBottom: 10, fontSize: 14 }}>Add / Update Config</div>
      <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', gap: 8, marginBottom: 12 }}>
        <input className="input" placeholder="Name (e.g. premium_api)"
          value={name} onChange={e => setName(e.target.value)} />
        <input className="input" placeholder="Per sec"
          value={perSec} onChange={e => setPerSec(e.target.value)} />
        <input className="input" placeholder="Per min"
          value={perMin} onChange={e => setPerMin(e.target.value)} />
        <input className="input" placeholder="Burst % (100)"
          value={burst} onChange={e => setBurst(e.target.value)} />
      </div>
      {saveErr && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 6 }}>{saveErr}</div>}
      <button className="btn" onClick={handleSave} disabled={saving}>
        {saving ? 'Saving…' : 'Save Config'}
      </button>
    </div>
  )
}

// ── Main Tenants tab ──────────────────────────────────────────────────────────

export default function Tenants() {
  const [tenants, setTenants]         = useState<TenantSummary[]>([])
  const [cursor, setCursor]           = useState(0)
  const [nextCursor, setNextCursor]   = useState(0)
  const [loading, setLoading]         = useState(true)
  const [err, setErr]                 = useState('')
  const [selected, setSelected]       = useState<string | null>(null)
  const [view, setView]               = useState<'list' | 'new' | 'rl'>('list')

  const loadTenants = useCallback((cur: number) => {
    setLoading(true); setErr('')
    listTenants(cur, 100)
      .then(r => {
        setTenants(r.items ?? [])
        setNextCursor(r.next_cursor)
        setCursor(cur)
      })
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { loadTenants(0) }, [loadTenants])

  function handleDeleted() {
    setSelected(null)
    loadTenants(0)
  }

  return (
    <div style={{ display: 'flex', height: '100%', overflow: 'hidden' }}>

      {/* ── Left sidebar ── */}
      <div style={{
        width: 260, borderRight: '1px solid var(--border)',
        display: 'flex', flexDirection: 'column', flexShrink: 0,
      }}>
        {/* toolbar */}
        <div style={{
          padding: '10px 12px', borderBottom: '1px solid var(--border)',
          display: 'flex', gap: 6,
        }}>
          <button
            className={`tab-btn${view === 'list' ? ' active' : ''}`}
            style={{ flex: 1, fontSize: 12 }}
            onClick={() => setView('list')}
          >Tenants</button>
          <button
            className={`tab-btn${view === 'rl' ? ' active' : ''}`}
            style={{ flex: 1, fontSize: 12 }}
            onClick={() => setView('rl')}
          >Rate Limits</button>
        </div>

        {view === 'list' && (
          <>
            <button
              className="btn"
              style={{ margin: 10, fontSize: 12 }}
              onClick={() => { setSelected(null); setView('new') }}
            >+ New Tenant</button>

            {/* tenant list */}
            <div style={{ flex: 1, overflowY: 'auto' }}>
              {loading && <div style={{ padding: 12, color: 'var(--text-muted)', fontSize: 13 }}>Loading…</div>}
              {err && <div style={{ padding: 12, color: '#f87171', fontSize: 13 }}>{err}</div>}
              {tenants.map(t => {
                const primary = t.aliases?.[0] ?? `#${t.tenant_id}`
                const isSelected = selected === primary
                return (
                  <div
                    key={t.tenant_id}
                    onClick={() => { setSelected(primary); setView('list') }}
                    style={{
                      padding: '8px 12px', cursor: 'pointer', fontSize: 13,
                      background: isSelected ? 'var(--accent-bg)' : undefined,
                      borderLeft: isSelected ? '3px solid var(--accent)' : '3px solid transparent',
                    }}
                  >
                    <div style={{ fontWeight: 600, marginBottom: 2 }}>{primary}</div>
                    <div style={{ color: 'var(--text-muted)', fontSize: 11 }}>
                      ID:{t.tenant_id} · {t.aliases?.length ?? 0} alias · {t.service_url_count} URL
                    </div>
                  </div>
                )
              })}

              {/* pagination */}
              {(cursor > 0 || nextCursor > 0) && (
                <div style={{ padding: '8px 12px', display: 'flex', gap: 6 }}>
                  {cursor > 0 && (
                    <button className="btn" style={{ flex: 1, fontSize: 11 }}
                      onClick={() => loadTenants(0)}>← First</button>
                  )}
                  {nextCursor > 0 && (
                    <button className="btn" style={{ flex: 1, fontSize: 11 }}
                      onClick={() => loadTenants(nextCursor)}>Next →</button>
                  )}
                </div>
              )}
            </div>
          </>
        )}

        {view === 'rl' && (
          <div style={{ padding: '8px 0' }}>
            <button
              className="btn"
              style={{ margin: '0 10px 8px', fontSize: 12 }}
              onClick={() => setView('list')}
            >← Tenants</button>
          </div>
        )}
      </div>

      {/* ── Right content ── */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {view === 'new' && (
          <NewTenantForm onCreated={() => { loadTenants(0); setView('list') }} />
        )}
        {view === 'rl' && <RateLimitPanel />}
        {view === 'list' && selected && (
          <TenantPanel key={selected} alias={selected} onDeleted={handleDeleted} />
        )}
        {view === 'list' && !selected && !loading && (
          <div className="panel-empty">Select a tenant to view details</div>
        )}
      </div>
    </div>
  )
}
