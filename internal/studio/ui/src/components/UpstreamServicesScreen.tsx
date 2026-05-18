import { useState, useEffect, useCallback } from 'react'
import { listUpstreamServices, upsertUpstreamService, deleteUpstreamService } from '../api'
import type { UpstreamServiceDef, UpstreamPattern } from '../types'

// ── ServiceDetail: right-panel form ──────────────────────────────────────────

interface ServiceDetailProps {
  initial: UpstreamServiceDef | null  // null = new
  isNew: boolean
  onSaved: () => void
  onDeleted: () => void
  onCancel: () => void
}

function ServiceDetail({ initial, isNew, onSaved, onDeleted, onCancel }: ServiceDetailProps) {
  const initSvc: UpstreamServiceDef = initial ?? { name: '', patterns: [] }

  const [name, setName]             = useState(initSvc.name)
  const [patterns, setPatterns]     = useState<UpstreamPattern[]>(
    initSvc.patterns.length > 0 ? initSvc.patterns : [],
  )
  const [unmatched, setUnmatched]   = useState<'fail_open' | 'fail_closed' | 'default_config'>(
    initSvc.unmatched ?? 'fail_open',
  )
  const [defaultConfig, setDefaultConfig] = useState(initSvc.default_config ?? '')

  const [saving, setSaving]         = useState(false)
  const [saveMsg, setSaveMsg]       = useState('')
  const [saveErr, setSaveErr]       = useState('')
  const [validErr, setValidErr]     = useState('')
  const [delConfirm, setDelConfirm] = useState(false)
  const [delErr, setDelErr]         = useState('')

  // reset when switching services
  useEffect(() => {
    const s: UpstreamServiceDef = initial ?? { name: '', patterns: [] }
    setName(s.name)
    setPatterns(s.patterns.length > 0 ? s.patterns : [])
    setUnmatched(s.unmatched ?? 'fail_open')
    setDefaultConfig(s.default_config ?? '')
    setSaveMsg(''); setSaveErr(''); setValidErr('')
    setDelConfirm(false); setDelErr('')
  }, [initial, isNew])

  function addPattern() {
    setPatterns(prev => [...prev, { pattern: '', config_name: '' }])
  }

  function removePattern(idx: number) {
    setPatterns(prev => prev.filter((_, i) => i !== idx))
  }

  function updatePattern(idx: number, field: keyof UpstreamPattern, value: string) {
    setPatterns(prev => prev.map((p, i) => i === idx ? { ...p, [field]: value } : p))
  }

  async function handleSave() {
    const trimmedName = name.trim()
    if (!trimmedName) { setValidErr('Name is required'); return }

    const body: UpstreamServiceDef = {
      name: trimmedName,
      patterns: patterns.filter(p => p.pattern.trim() !== ''),
      unmatched,
      ...(unmatched === 'default_config' && defaultConfig.trim()
        ? { default_config: defaultConfig.trim() }
        : {}),
    }

    setSaving(true); setSaveMsg(''); setSaveErr(''); setValidErr('')
    try {
      await upsertUpstreamService(body)
      setSaveMsg('Saved!')
      onSaved()
    } catch (e: unknown) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  async function handleDelete() {
    if (!delConfirm) { setDelConfirm(true); setDelErr(''); return }
    try {
      await deleteUpstreamService(name.trim())
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
            placeholder="e.g. openai-service"
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

      {/* URL Patterns */}
      <div style={{ marginBottom: 18 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 8 }}>
          URL Patterns
        </label>

        {/* Column headers */}
        {patterns.length > 0 && (
          <div style={{
            display: 'grid',
            gridTemplateColumns: '1fr 1fr 28px',
            gap: 6,
            marginBottom: 4,
          }}>
            <div style={{ fontSize: 11, color: 'var(--text-muted)', paddingLeft: 2 }}>URL Pattern</div>
            <div style={{ fontSize: 11, color: 'var(--text-muted)', paddingLeft: 2 }}>Rate Limit Config</div>
            <div />
          </div>
        )}

        {/* Pattern rows */}
        {patterns.map((p, idx) => (
          <div
            key={idx}
            style={{
              display: 'grid',
              gridTemplateColumns: '1fr 1fr 28px',
              gap: 6,
              marginBottom: 6,
              alignItems: 'center',
            }}
          >
            <input
              className="input"
              style={{ boxSizing: 'border-box', fontSize: 12 }}
              placeholder="https://api.example.com/*"
              value={p.pattern}
              onChange={e => { updatePattern(idx, 'pattern', e.target.value); setSaveMsg('') }}
            />
            <input
              className="input"
              style={{ boxSizing: 'border-box', fontSize: 12 }}
              placeholder="my-rl-config"
              value={p.config_name}
              onChange={e => { updatePattern(idx, 'config_name', e.target.value); setSaveMsg('') }}
            />
            <button
              className="btn"
              style={{ fontSize: 13, padding: '2px 6px', lineHeight: 1 }}
              onClick={() => removePattern(idx)}
              title="Remove pattern"
            >×</button>
          </div>
        ))}

        <button
          className="btn"
          style={{ fontSize: 12, marginTop: 4 }}
          onClick={addPattern}
        >+ Add Pattern</button>
      </div>

      {/* Unmatched Policy */}
      <div style={{ marginBottom: 18 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
          Unmatched Policy
        </label>
        <select
          className="input"
          style={{ width: '100%', boxSizing: 'border-box' }}
          value={unmatched}
          onChange={e => {
            setUnmatched(e.target.value as 'fail_open' | 'fail_closed' | 'default_config')
            setSaveMsg('')
          }}
        >
          <option value="fail_open">Fail Open (allow all)</option>
          <option value="fail_closed">Fail Closed (deny all)</option>
          <option value="default_config">Use Default Config</option>
        </select>
      </div>

      {/* Default Config — only shown when unmatched = 'default_config' */}
      {unmatched === 'default_config' && (
        <div style={{ marginBottom: 18 }}>
          <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
            Default Config
          </label>
          <input
            className="input"
            style={{ width: '100%', boxSizing: 'border-box' }}
            placeholder="e.g. default-rl"
            value={defaultConfig}
            onChange={e => { setDefaultConfig(e.target.value); setSaveMsg('') }}
          />
        </div>
      )}

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

export default function UpstreamServicesScreen() {
  const [services, setServices]   = useState<UpstreamServiceDef[]>([])
  const [loading, setLoading]     = useState(true)
  const [err, setErr]             = useState('')
  const [search, setSearch]       = useState('')
  const [selected, setSelected]   = useState<string | null>(null)  // service name
  const [isNew, setIsNew]         = useState(false)

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listUpstreamServices()
      .then(r => setServices(r.items ?? []))
      .catch((e: unknown) => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  const filtered = search.trim()
    ? services.filter(s => s.name.toLowerCase().includes(search.trim().toLowerCase()))
    : services

  const selectedService = selected
    ? services.find(s => s.name === selected) ?? null
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

  function handleNewService() {
    setSelected(null)
    setIsNew(true)
  }

  function handleSelectService(name: string) {
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
            placeholder="Search services…"
            value={search}
            onChange={e => setSearch(e.target.value)}
          />
        </div>

        {/* new button */}
        <button
          className="btn"
          style={{ margin: 10, fontSize: 12 }}
          onClick={handleNewService}
        >+ New Service</button>

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
              {search ? 'No matches.' : 'No upstream services yet.'}
            </div>
          )}
          {filtered.map(s => {
            const isSelected = selected === s.name && !isNew
            return (
              <div
                key={s.name}
                onClick={() => handleSelectService(s.name)}
                style={{
                  padding: '8px 12px', cursor: 'pointer', fontSize: 13,
                  background: isSelected ? 'var(--accent-bg)' : undefined,
                  borderLeft: isSelected ? '3px solid var(--accent)' : '3px solid transparent',
                }}
              >
                <div style={{ fontWeight: 600, marginBottom: 2, wordBreak: 'break-all' }}>{s.name}</div>
                <div style={{ color: 'var(--text-muted)', fontSize: 11 }}>
                  {s.patterns.length} {s.patterns.length === 1 ? 'pattern' : 'patterns'}
                </div>
              </div>
            )
          })}
        </div>
      </div>

      {/* ── Right panel ── */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {isNew && (
          <ServiceDetail
            key="__new__"
            initial={null}
            isNew={true}
            onSaved={handleSaved}
            onDeleted={handleDeleted}
            onCancel={handleCancel}
          />
        )}
        {!isNew && selectedService && (
          <ServiceDetail
            key={selectedService.name}
            initial={selectedService}
            isNew={false}
            onSaved={handleSaved}
            onDeleted={handleDeleted}
            onCancel={handleCancel}
          />
        )}
        {!isNew && !selectedService && !loading && (
          <div className="panel-empty">Select a service to edit, or create a new one</div>
        )}
        {!isNew && !selectedService && loading && (
          <div className="panel-empty">Loading…</div>
        )}
      </div>
    </div>
  )
}
