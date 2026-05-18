import { useState, useEffect, useCallback } from 'react'
import { listTiers, upsertTier, deleteTier } from '../api'
import type { TierDef } from '../types'

// ── helpers ───────────────────────────────────────────────────────────────────

function splitList(s: string): string[] {
  return s.split(',').map(v => v.trim()).filter(v => v.length > 0)
}

function joinList(arr: string[] | undefined): string {
  return arr ? arr.join(', ') : ''
}

// ── TierDetail: right-panel form ──────────────────────────────────────────────

interface TierDetailProps {
  initial: TierDef | null  // null = new
  isNew: boolean
  onSaved: () => void
  onDeleted: () => void
  onCancel: () => void
}

function TierDetail({ initial, isNew, onSaved, onDeleted, onCancel }: TierDetailProps) {
  const initTier: TierDef = initial ?? { name: '' }

  const [name, setName]               = useState(initTier.name)
  const [configName, setConfigName]   = useState(initTier.config_name ?? '')
  const [overallName, setOverallName] = useState(initTier.overall_name ?? '')
  const [allowedApis, setAllowedApis] = useState(joinList(initTier.allowed_apis))
  const [blockedApis, setBlockedApis] = useState(joinList(initTier.blocked_apis))

  const [saving, setSaving]         = useState(false)
  const [saveMsg, setSaveMsg]       = useState('')
  const [saveErr, setSaveErr]       = useState('')
  const [validErr, setValidErr]     = useState('')
  const [delConfirm, setDelConfirm] = useState(false)
  const [delErr, setDelErr]         = useState('')

  // reset when switching tiers
  useEffect(() => {
    const t: TierDef = initial ?? { name: '' }
    setName(t.name)
    setConfigName(t.config_name ?? '')
    setOverallName(t.overall_name ?? '')
    setAllowedApis(joinList(t.allowed_apis))
    setBlockedApis(joinList(t.blocked_apis))
    setSaveMsg(''); setSaveErr(''); setValidErr('')
    setDelConfirm(false); setDelErr('')
  }, [initial, isNew])

  async function handleSave() {
    const trimmedName = name.trim()
    if (!trimmedName) { setValidErr('Name is required'); return }

    const body: TierDef = {
      name: trimmedName,
      ...(configName.trim() ? { config_name: configName.trim() } : {}),
      ...(overallName.trim() ? { overall_name: overallName.trim() } : {}),
      ...(allowedApis.trim() ? { allowed_apis: splitList(allowedApis) } : {}),
      ...(blockedApis.trim() ? { blocked_apis: splitList(blockedApis) } : {}),
    }

    setSaving(true); setSaveMsg(''); setSaveErr(''); setValidErr('')
    try {
      await upsertTier(body)
      setSaveMsg('Saved!')
      onSaved()
    } catch (e: unknown) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  async function handleDelete() {
    if (!delConfirm) { setDelConfirm(true); setDelErr(''); return }
    try {
      await deleteTier(name.trim())
      onDeleted()
    } catch (e: unknown) { setDelErr(String(e)) }
  }

  const inputStyle: React.CSSProperties = {
    background: 'var(--surface)', border: '1px solid var(--border)',
    borderRadius: 4, color: 'inherit', padding: '5px 8px', fontSize: 13,
  }

  return (
    <div style={{ padding: 24, overflowY: 'auto', height: '100%', boxSizing: 'border-box' }}>

      {/* Name */}
      <div style={{ marginBottom: 18 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
          Name
        </label>
        {isNew ? (
          <input
            className="input"
            style={{ width: '100%', boxSizing: 'border-box' }}
            placeholder="e.g. premium"
            value={name}
            onChange={e => { setName(e.target.value); setSaveMsg('') }}
          />
        ) : (
          <div style={{
            ...inputStyle,
            background: 'var(--block-bg)', color: 'var(--text-muted)',
            fontWeight: 600, userSelect: 'all',
          }}>{name}</div>
        )}
      </div>

      {/* Rate Limit Config */}
      <div style={{ marginBottom: 18 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
          Rate Limit Config
        </label>
        <input
          className="input"
          style={{ width: '100%', boxSizing: 'border-box' }}
          placeholder="e.g. premium-rl"
          value={configName}
          onChange={e => { setConfigName(e.target.value); setSaveMsg('') }}
        />
      </div>

      {/* Overall Quota Config */}
      <div style={{ marginBottom: 18 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
          Overall Quota Config
        </label>
        <input
          className="input"
          style={{ width: '100%', boxSizing: 'border-box' }}
          placeholder="e.g. daily-overall"
          value={overallName}
          onChange={e => { setOverallName(e.target.value); setSaveMsg('') }}
        />
      </div>

      {/* Allowed APIs */}
      <div style={{ marginBottom: 18 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
          Allowed APIs
        </label>
        <input
          className="input"
          style={{ width: '100%', boxSizing: 'border-box' }}
          placeholder="api-one, api-two, …"
          value={allowedApis}
          onChange={e => { setAllowedApis(e.target.value); setSaveMsg('') }}
        />
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 4 }}>
          Leave blank to allow all APIs
        </div>
      </div>

      {/* Blocked APIs */}
      <div style={{ marginBottom: 18 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
          Blocked APIs
        </label>
        <input
          className="input"
          style={{ width: '100%', boxSizing: 'border-box' }}
          placeholder="api-one, api-two, …"
          value={blockedApis}
          onChange={e => { setBlockedApis(e.target.value); setSaveMsg('') }}
        />
        <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 4 }}>
          Leave blank to block none
        </div>
      </div>

      {/* ── Actions ── */}
      <div style={{
        borderTop: '1px solid var(--border)', marginTop: 16, paddingTop: 14,
        display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
      }}>
        {validErr && (
          <span style={{ color: '#f87171', fontSize: 12 }}>{validErr}</span>
        )}
        {saveErr && (
          <span style={{ color: '#f87171', fontSize: 12 }}>{saveErr}</span>
        )}
        {saveMsg && (
          <span style={{ color: '#4ade80', fontSize: 12 }}>{saveMsg}</span>
        )}
        <div style={{ flex: 1 }} />
        {!isNew && (
          <>
            {delErr && <span style={{ color: '#f87171', fontSize: 12 }}>{delErr}</span>}
            <button
              className="btn"
              style={{ fontSize: 12, background: delConfirm ? '#ef4444' : undefined }}
              onClick={handleDelete}
            >
              {delConfirm ? 'Confirm delete' : 'Delete'}
            </button>
            {delConfirm && (
              <button className="btn" style={{ fontSize: 12 }} onClick={() => setDelConfirm(false)}>
                Cancel
              </button>
            )}
          </>
        )}
        <button className="btn" style={{ fontSize: 12 }} onClick={onCancel}>
          Cancel
        </button>
        <button
          className="btn"
          style={{ fontSize: 12, background: 'var(--accent)', color: '#fff' }}
          onClick={handleSave}
          disabled={saving}
        >
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  )
}

// ── Main screen ───────────────────────────────────────────────────────────────

export default function TenantTiersScreen() {
  const [tiers, setTiers]     = useState<TierDef[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr]         = useState('')
  const [search, setSearch]   = useState('')
  const [selected, setSelected] = useState<string | null>(null)  // tier name
  const [isNew, setIsNew]     = useState(false)

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listTiers()
      .then(r => setTiers(r.items ?? []))
      .catch((e: unknown) => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  const filtered = search.trim()
    ? tiers.filter(t => t.name.toLowerCase().includes(search.trim().toLowerCase()))
    : tiers

  const selectedTier = selected
    ? tiers.find(t => t.name === selected) ?? null
    : null

  function handleSaved() {
    load()
    setIsNew(false)
  }

  function handleDeleted() {
    setSelected(null)
    setIsNew(false)
    load()
  }

  function handleCancel() {
    setSelected(null)
    setIsNew(false)
  }

  function handleNewTier() {
    setSelected(null)
    setIsNew(true)
  }

  function handleSelectTier(name: string) {
    setSelected(name)
    setIsNew(false)
  }

  return (
    <div style={{ display: 'flex', height: '100%', overflow: 'hidden' }}>

      {/* ── Left sidebar ── */}
      <div style={{
        width: 260, borderRight: '1px solid var(--border)',
        display: 'flex', flexDirection: 'column', flexShrink: 0,
      }}>
        {/* search */}
        <div style={{ padding: '10px 12px', borderBottom: '1px solid var(--border)' }}>
          <input
            className="input"
            style={{ width: '100%', boxSizing: 'border-box', fontSize: 12 }}
            placeholder="Search tiers…"
            value={search}
            onChange={e => setSearch(e.target.value)}
          />
        </div>

        {/* new button */}
        <button
          className="btn"
          style={{ margin: 10, fontSize: 12 }}
          onClick={handleNewTier}
        >+ New Tier</button>

        {/* list */}
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {loading && (
            <div style={{ padding: 12, color: 'var(--text-muted)', fontSize: 13 }}>Loading…</div>
          )}
          {err && (
            <div style={{ padding: 12, color: '#f87171', fontSize: 13 }}>{err}</div>
          )}
          {!loading && !err && filtered.length === 0 && (
            <div style={{ padding: 12, color: 'var(--text-muted)', fontSize: 13 }}>
              {search ? 'No matches.' : 'No tiers yet.'}
            </div>
          )}
          {filtered.map(t => {
            const isSelected = selected === t.name && !isNew
            return (
              <div
                key={t.name}
                onClick={() => handleSelectTier(t.name)}
                style={{
                  padding: '8px 12px', cursor: 'pointer', fontSize: 13,
                  background: isSelected ? 'var(--accent-bg)' : undefined,
                  borderLeft: isSelected ? '3px solid var(--accent)' : '3px solid transparent',
                }}
              >
                <div style={{ fontWeight: 600, marginBottom: 2, wordBreak: 'break-all' }}>{t.name}</div>
                {t.config_name && (
                  <div style={{ color: 'var(--text-muted)', fontSize: 11 }}>
                    Config: {t.config_name}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>

      {/* ── Right panel ── */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {isNew && (
          <TierDetail
            key="__new__"
            initial={null}
            isNew={true}
            onSaved={handleSaved}
            onDeleted={handleDeleted}
            onCancel={handleCancel}
          />
        )}
        {!isNew && selectedTier && (
          <TierDetail
            key={selectedTier.name}
            initial={selectedTier}
            isNew={false}
            onSaved={handleSaved}
            onDeleted={handleDeleted}
            onCancel={handleCancel}
          />
        )}
        {!isNew && !selectedTier && !loading && (
          <div className="panel-empty">Select a tier to edit, or create a new one</div>
        )}
        {!isNew && !selectedTier && loading && (
          <div className="panel-empty">Loading…</div>
        )}
      </div>
    </div>
  )
}
