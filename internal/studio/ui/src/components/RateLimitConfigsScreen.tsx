import { useEffect, useState, useCallback } from 'react'
import { listRateLimitConfigsV2, upsertRateLimitConfigV2, deleteRateLimitConfigV2 } from '../api'
import type {
  RateLimitConfigV2,
  RateLimitConfigV2Record,
  RateLimitWindow,
  Enforcement,
  RedisUnavailable,
  OnEmptyKey,
} from '../types'

// ── constants ─────────────────────────────────────────────────────────────────

const PERIOD_PRESETS = ['1s', '30s', '1m', '5m', '15m', '30m', '1h', '6h', '1d', 'custom'] as const
type PeriodPreset = typeof PERIOD_PRESETS[number]

const LONG_WINDOW_PERIODS = new Set(['1h', '6h', '1d'])

function isLongWindow(period: string): boolean {
  if (LONG_WINDOW_PERIODS.has(period)) return true
  // parse custom: e.g. "2h", "90m"
  const m = /^(\d+)([smhd])$/.exec(period)
  if (!m) return false
  const val = parseInt(m[1], 10)
  const unit = m[2]
  const secs = unit === 's' ? val : unit === 'm' ? val * 60 : unit === 'h' ? val * 3600 : val * 86400
  return secs > 60
}

// ── blank config factory ──────────────────────────────────────────────────────

function blankConfig(): RateLimitConfigV2 {
  return {
    name: '',
    enforcement: 'approximate',
    windows: [],
    exceeded_status: 429,
    exceeded_body: '',
    exceeded_content_type: '',
    emit_headers: false,
    case_sensitive: false,
    on_empty_key: 'fail',
  }
}

// ── WindowRow: one row in the windows table ────────────────────────────────────

interface WindowRowState {
  periodPreset: PeriodPreset
  periodCustom: string
  limit: string
  burst: string
}

function windowRowToState(w: RateLimitWindow): WindowRowState {
  const preset = (PERIOD_PRESETS as readonly string[]).includes(w.period) && w.period !== 'custom'
    ? (w.period as PeriodPreset)
    : 'custom'
  return {
    periodPreset: preset,
    periodCustom: preset === 'custom' ? w.period : '',
    limit: String(w.limit),
    burst: w.burst_factor !== undefined ? String(w.burst_factor) : '',
  }
}

function windowRowToWindow(row: WindowRowState): RateLimitWindow {
  const period = row.periodPreset === 'custom' ? row.periodCustom.trim() : row.periodPreset
  const limit = parseInt(row.limit, 10)
  const burstVal = row.burst.trim() !== '' ? parseInt(row.burst, 10) : undefined
  return {
    period,
    limit: isNaN(limit) ? 0 : limit,
    ...(burstVal !== undefined && !isNaN(burstVal) ? { burst_factor: burstVal } : {}),
  }
}

// ── section header (collapsible) ──────────────────────────────────────────────

function SectionHeader({
  title, open, onToggle,
}: { title: string; open: boolean; onToggle: () => void }) {
  return (
    <button
      onClick={onToggle}
      style={{
        display: 'flex', alignItems: 'center', gap: 6,
        width: '100%', background: 'none', border: 'none',
        borderTop: '1px solid var(--border)', padding: '10px 0 6px',
        cursor: 'pointer', color: 'inherit', textAlign: 'left',
      }}
    >
      <span style={{ fontSize: 11, color: 'var(--text-muted)', userSelect: 'none' }}>
        {open ? '▾' : '▸'}
      </span>
      <span style={{ fontWeight: 600, fontSize: 13 }}>{title}</span>
    </button>
  )
}

// ── ConfigDetail: right-panel form ────────────────────────────────────────────

interface ConfigDetailProps {
  initial: RateLimitConfigV2 | null  // null = new
  isNew: boolean
  onSaved: () => void
  onDeleted: () => void
}

function ConfigDetail({ initial, isNew, onSaved, onDeleted }: ConfigDetailProps) {
  // derive initial form state
  const initCfg = initial ?? blankConfig()

  const [name, setName]             = useState(initCfg.name)
  const [enforcement, setEnforcement] = useState<Enforcement>(initCfg.enforcement)
  const [redisUnavail, setRedisUnavail] = useState<RedisUnavailable>(
    initCfg.redis_unavailable ?? 'fail_open'
  )
  const [exceededStatus, setExceededStatus] = useState(
    String(initCfg.exceeded_status ?? 429)
  )
  const [exceededBody, setExceededBody]   = useState(initCfg.exceeded_body ?? '')
  const [exceededCT, setExceededCT]       = useState(initCfg.exceeded_content_type ?? '')
  const [emitHeaders, setEmitHeaders]     = useState(initCfg.emit_headers ?? false)
  const [caseSensitive, setCaseSensitive] = useState(initCfg.case_sensitive ?? false)
  const [onEmptyKey, setOnEmptyKey]       = useState<OnEmptyKey>(initCfg.on_empty_key ?? 'fail')

  // windows
  const [rows, setRows] = useState<WindowRowState[]>(
    (initCfg.windows ?? []).map(windowRowToState)
  )

  // new-window form
  const [addPreset, setAddPreset]   = useState<PeriodPreset>('5m')
  const [addCustom, setAddCustom]   = useState('')
  const [addLimit, setAddLimit]     = useState('')
  const [addBurst, setAddBurst]     = useState('')

  // collapsible sections
  const [outputOpen, setOutputOpen]     = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)

  // feedback
  const [saving, setSaving]         = useState(false)
  const [saveMsg, setSaveMsg]       = useState('')
  const [saveErr, setSaveErr]       = useState('')
  const [delConfirm, setDelConfirm] = useState(false)
  const [delErr, setDelErr]         = useState('')
  const [validErr, setValidErr]     = useState('')

  // reset when switching configs
  useEffect(() => {
    const cfg = initial ?? blankConfig()
    setName(cfg.name)
    setEnforcement(cfg.enforcement)
    setRedisUnavail(cfg.redis_unavailable ?? 'fail_open')
    setExceededStatus(String(cfg.exceeded_status ?? 429))
    setExceededBody(cfg.exceeded_body ?? '')
    setExceededCT(cfg.exceeded_content_type ?? '')
    setEmitHeaders(cfg.emit_headers ?? false)
    setCaseSensitive(cfg.case_sensitive ?? false)
    setOnEmptyKey(cfg.on_empty_key ?? 'fail')
    setRows((cfg.windows ?? []).map(windowRowToState))
    setAddPreset('5m'); setAddCustom(''); setAddLimit(''); setAddBurst('')
    setSaveMsg(''); setSaveErr(''); setValidErr(''); setDelConfirm(false); setDelErr('')
    setOutputOpen(false); setAdvancedOpen(false)
  }, [initial, isNew])

  function hasLongWindow(): boolean {
    return rows.some(r => {
      const period = r.periodPreset === 'custom' ? r.periodCustom : r.periodPreset
      return isLongWindow(period)
    }) || (addPreset === 'custom' ? isLongWindow(addCustom) : isLongWindow(addPreset))
  }

  function handleAddWindow() {
    const period = addPreset === 'custom' ? addCustom.trim() : addPreset
    if (!period) return
    const limit = parseInt(addLimit, 10)
    if (isNaN(limit) || limit < 0) { setValidErr('Limit must be a number ≥ 0'); return }
    const newRow: WindowRowState = {
      periodPreset: addPreset,
      periodCustom: addCustom,
      limit: addLimit,
      burst: addBurst,
    }
    setRows(prev => [...prev, newRow])
    setAddLimit(''); setAddBurst(''); setValidErr('')
  }

  function handleRemoveWindow(i: number) {
    setRows(prev => prev.filter((_, idx) => idx !== i))
  }

  function handleRowChange(i: number, patch: Partial<WindowRowState>) {
    setRows(prev => prev.map((r, idx) => idx === i ? { ...r, ...patch } : r))
  }

  async function handleSave() {
    const trimmedName = name.trim()
    if (!trimmedName) { setValidErr('Name is required'); return }
    if (rows.length === 0) { setValidErr('At least one window is required'); return }

    // validate rows
    for (const row of rows) {
      const period = row.periodPreset === 'custom' ? row.periodCustom.trim() : row.periodPreset
      if (!period) { setValidErr('Each window must have a period'); return }
      const limit = parseInt(row.limit, 10)
      if (isNaN(limit) || limit < 0) { setValidErr('Each window limit must be a number ≥ 0'); return }
    }

    const cfg: RateLimitConfigV2 = {
      name: trimmedName,
      enforcement,
      ...(enforcement === 'strict' ? { redis_unavailable: redisUnavail } : {}),
      windows: rows.map(windowRowToWindow),
      ...(exceededStatus.trim() !== '' ? { exceeded_status: parseInt(exceededStatus, 10) || 429 } : {}),
      ...(exceededBody.trim() !== '' ? { exceeded_body: exceededBody.trim() } : {}),
      ...(exceededCT.trim() !== '' ? { exceeded_content_type: exceededCT.trim() } : {}),
      emit_headers: emitHeaders,
      case_sensitive: caseSensitive,
      on_empty_key: onEmptyKey,
    }

    setSaving(true); setSaveMsg(''); setSaveErr(''); setValidErr('')
    try {
      await upsertRateLimitConfigV2(cfg)
      setSaveMsg('Saved!')
      onSaved()
    } catch (e) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  async function handleDelete() {
    if (!delConfirm) { setDelConfirm(true); setDelErr(''); return }
    try {
      await deleteRateLimitConfigV2(name.trim())
      onDeleted()
    } catch (e) { setDelErr(String(e)) }
  }

  const inputStyle: React.CSSProperties = {
    background: 'var(--surface)', border: '1px solid var(--border)',
    borderRadius: 4, color: 'inherit', padding: '5px 8px', fontSize: 13,
  }

  const toggleBtnStyle = (active: boolean): React.CSSProperties => ({
    padding: '4px 14px', fontSize: 12, cursor: 'pointer',
    background: active ? 'var(--accent)' : 'var(--surface)',
    color: active ? '#fff' : 'inherit',
    border: '1px solid var(--border)', borderRadius: 4,
  })

  return (
    <div style={{ padding: 24, overflowY: 'auto', height: '100%', boxSizing: 'border-box' }}>

      {/* Name */}
      <div style={{ marginBottom: 18 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
          Config Name
        </label>
        {isNew ? (
          <input
            className="input"
            style={{ width: '100%', boxSizing: 'border-box' }}
            placeholder="e.g. premium_api"
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

      {/* Enforcement */}
      <div style={{ marginBottom: 14 }}>
        <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 6 }}>
          Enforcement
        </label>
        <div style={{ display: 'flex', gap: 6 }}>
          <button style={toggleBtnStyle(enforcement === 'approximate')}
            onClick={() => setEnforcement('approximate')}>Approximate</button>
          <button style={toggleBtnStyle(enforcement === 'strict')}
            onClick={() => setEnforcement('strict')}>Strict</button>
        </div>
        {enforcement === 'approximate' && (
          <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 4 }}>
            Local counters — fastest, no Redis dependency
          </div>
        )}
      </div>

      {/* Redis unavailable — only when strict */}
      {enforcement === 'strict' && (
        <div style={{ marginBottom: 14 }}>
          <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
            Redis unavailable
          </label>
          <select
            className="input"
            value={redisUnavail}
            onChange={e => setRedisUnavail(e.target.value as RedisUnavailable)}
          >
            <option value="fail_open">Fail open (allow requests)</option>
            <option value="fail_closed">Fail closed (block requests)</option>
          </select>
        </div>
      )}

      {/* ── Windows ── */}
      <div style={{ marginBottom: 6 }}>
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          borderTop: '1px solid var(--border)', padding: '10px 0 8px',
        }}>
          <span style={{ fontWeight: 600, fontSize: 13 }}>Windows</span>
          {rows.length === 0 && (
            <span style={{ fontSize: 11, color: '#f87171' }}>At least one window required</span>
          )}
        </div>

        {/* window rows table */}
        {rows.length > 0 && (
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13, marginBottom: 10 }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)', color: 'var(--text-muted)' }}>
                <th style={{ textAlign: 'left', padding: '4px 8px 4px 0' }}>Period</th>
                <th style={{ textAlign: 'right', padding: '4px 8px' }}>Limit</th>
                <th style={{ textAlign: 'right', padding: '4px 8px' }}>Burst%</th>
                <th style={{ width: 32 }}></th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => {
                const displayPeriod = row.periodPreset === 'custom'
                  ? row.periodCustom || '—'
                  : row.periodPreset
                const warnLong = isLongWindow(displayPeriod)
                return (
                  <tr key={i} style={{ borderBottom: '1px solid var(--border)' }}>
                    <td style={{ padding: '6px 8px 6px 0' }}>
                      <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                        <select
                          className="input"
                          style={{ fontSize: 12, padding: '3px 6px' }}
                          value={row.periodPreset}
                          onChange={e => handleRowChange(i, { periodPreset: e.target.value as PeriodPreset })}
                        >
                          {PERIOD_PRESETS.map(p => <option key={p} value={p}>{p}</option>)}
                        </select>
                        {row.periodPreset === 'custom' && (
                          <input
                            className="input"
                            style={{ fontSize: 12, padding: '3px 6px', width: 70 }}
                            placeholder="e.g. 2h"
                            value={row.periodCustom}
                            onChange={e => handleRowChange(i, { periodCustom: e.target.value })}
                          />
                        )}
                        {warnLong && (
                          <span title="Long windows use Redis for accuracy" style={{ fontSize: 12 }}>
                            ⚠
                          </span>
                        )}
                      </div>
                    </td>
                    <td style={{ padding: '6px 8px', textAlign: 'right' }}>
                      <input
                        className="input"
                        style={{ fontSize: 12, padding: '3px 6px', width: 64, textAlign: 'right' }}
                        value={row.limit}
                        onChange={e => handleRowChange(i, { limit: e.target.value })}
                      />
                    </td>
                    <td style={{ padding: '6px 8px', textAlign: 'right' }}>
                      <input
                        className="input"
                        style={{ fontSize: 12, padding: '3px 6px', width: 64, textAlign: 'right' }}
                        placeholder="—"
                        value={row.burst}
                        onChange={e => handleRowChange(i, { burst: e.target.value })}
                      />
                    </td>
                    <td style={{ padding: '6px 0 6px 8px', textAlign: 'center' }}>
                      <button
                        onClick={() => handleRemoveWindow(i)}
                        style={{
                          background: 'none', border: 'none', cursor: 'pointer',
                          color: 'var(--danger)', fontSize: 16, lineHeight: 1, padding: 0,
                        }}
                        title="Remove window"
                      >×</button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}

        {/* Add window form */}
        <div style={{
          background: 'var(--block-bg)', borderRadius: 6, padding: 12,
          border: '1px solid var(--border)', marginBottom: 8,
        }}>
          <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 8, fontWeight: 600 }}>
            + Add Window
          </div>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <div>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Period</label>
              <select
                className="input"
                style={{ fontSize: 12, padding: '4px 6px' }}
                value={addPreset}
                onChange={e => { setAddPreset(e.target.value as PeriodPreset); setAddCustom('') }}
              >
                {PERIOD_PRESETS.map(p => <option key={p} value={p}>{p}</option>)}
              </select>
            </div>
            {addPreset === 'custom' && (
              <div>
                <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Custom</label>
                <input
                  className="input"
                  style={{ fontSize: 12, padding: '4px 6px', width: 70 }}
                  placeholder="e.g. 2h"
                  value={addCustom}
                  onChange={e => setAddCustom(e.target.value)}
                />
              </div>
            )}
            <div>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Limit</label>
              <input
                className="input"
                style={{ fontSize: 12, padding: '4px 6px', width: 72 }}
                placeholder="100"
                value={addLimit}
                onChange={e => setAddLimit(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleAddWindow()}
              />
            </div>
            <div>
              <label style={{ fontSize: 11, color: 'var(--text-muted)', display: 'block', marginBottom: 3 }}>Burst%</label>
              <input
                className="input"
                style={{ fontSize: 12, padding: '4px 6px', width: 72 }}
                placeholder="optional"
                value={addBurst}
                onChange={e => setAddBurst(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleAddWindow()}
              />
            </div>
            <button
              className="btn"
              style={{ fontSize: 12, alignSelf: 'flex-end' }}
              onClick={handleAddWindow}
            >Add</button>
          </div>

          {/* long window note */}
          {hasLongWindow() && (
            <div style={{ fontSize: 11, color: '#f59e0b', marginTop: 8 }}>
              ⚠ Long windows use Redis for accuracy
            </div>
          )}
        </div>
      </div>

      {/* ── Output section (collapsible) ── */}
      <SectionHeader title="Output" open={outputOpen} onToggle={() => setOutputOpen(o => !o)} />
      {outputOpen && (
        <div style={{ marginBottom: 8, paddingBottom: 8 }}>
          <div style={{ display: 'flex', gap: 12, marginBottom: 10, flexWrap: 'wrap' }}>
            <div style={{ flex: '0 0 100px' }}>
              <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
                Status code
              </label>
              <input
                className="input"
                style={{ width: '100%', boxSizing: 'border-box' }}
                placeholder="429"
                value={exceededStatus}
                onChange={e => setExceededStatus(e.target.value)}
              />
            </div>
            <div style={{ flex: '1 1 160px' }}>
              <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
                Content-Type
              </label>
              <input
                className="input"
                style={{ width: '100%', boxSizing: 'border-box' }}
                placeholder="application/json"
                value={exceededCT}
                onChange={e => setExceededCT(e.target.value)}
              />
            </div>
          </div>
          <div style={{ marginBottom: 10 }}>
            <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
              Body
            </label>
            <textarea
              className="input"
              style={{ width: '100%', boxSizing: 'border-box', height: 64, resize: 'vertical', fontFamily: 'monospace', fontSize: 12 }}
              placeholder='{"error":"rate_limit_exceeded"}'
              value={exceededBody}
              onChange={e => setExceededBody(e.target.value)}
            />
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 13, userSelect: 'none' }}>
            <input
              type="checkbox"
              checked={emitHeaders}
              onChange={e => setEmitHeaders(e.target.checked)}
            />
            Emit rate limit headers
          </label>
        </div>
      )}

      {/* ── Advanced section (collapsible) ── */}
      <SectionHeader title="Advanced" open={advancedOpen} onToggle={() => setAdvancedOpen(o => !o)} />
      {advancedOpen && (
        <div style={{ marginBottom: 8, paddingBottom: 8 }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 13, userSelect: 'none', marginBottom: 12 }}>
            <input
              type="checkbox"
              checked={caseSensitive}
              onChange={e => setCaseSensitive(e.target.checked)}
            />
            Case sensitive keys
          </label>
          <div>
            <label style={{ fontSize: 12, color: 'var(--text-muted)', display: 'block', marginBottom: 4 }}>
              On empty key
            </label>
            <select
              className="input"
              value={onEmptyKey}
              onChange={e => setOnEmptyKey(e.target.value as OnEmptyKey)}
            >
              <option value="fail">Fail (deny request)</option>
              <option value="skip">Skip (pass through)</option>
              <option value="fallback_tenant">Fallback to tenant</option>
            </select>
          </div>
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
        <button className="btn" onClick={handleSave} disabled={saving}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  )
}

// ── Main screen ───────────────────────────────────────────────────────────────

export default function RateLimitConfigsScreen() {
  const [configs, setConfigs]   = useState<RateLimitConfigV2Record[]>([])
  const [loading, setLoading]   = useState(true)
  const [err, setErr]           = useState('')
  const [search, setSearch]     = useState('')
  const [selected, setSelected] = useState<string | null>(null)  // config name
  const [isNew, setIsNew]       = useState(false)

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listRateLimitConfigsV2()
      .then(r => setConfigs(r.items ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  const filtered = search.trim()
    ? configs.filter(c => c.name.toLowerCase().includes(search.trim().toLowerCase()))
    : configs

  const selectedRecord = selected
    ? configs.find(c => c.name === selected) ?? null
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

  function handleNewConfig() {
    setSelected(null)
    setIsNew(true)
  }

  function handleSelectConfig(name: string) {
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
            placeholder="Search configs…"
            value={search}
            onChange={e => setSearch(e.target.value)}
          />
        </div>

        {/* new button */}
        <button
          className="btn"
          style={{ margin: 10, fontSize: 12 }}
          onClick={handleNewConfig}
        >+ New Config</button>

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
              {search ? 'No matches.' : 'No configs yet.'}
            </div>
          )}
          {filtered.map(c => {
            const isSelected = selected === c.name && !isNew
            const windowCount = c.config.windows?.length ?? 0
            return (
              <div
                key={c.name}
                onClick={() => handleSelectConfig(c.name)}
                style={{
                  padding: '8px 12px', cursor: 'pointer', fontSize: 13,
                  background: isSelected ? 'var(--accent-bg)' : undefined,
                  borderLeft: isSelected ? '3px solid var(--accent)' : '3px solid transparent',
                }}
              >
                <div style={{ fontWeight: 600, marginBottom: 2, wordBreak: 'break-all' }}>{c.name}</div>
                <div style={{ color: 'var(--text-muted)', fontSize: 11 }}>
                  {c.config.enforcement} · {windowCount} window{windowCount !== 1 ? 's' : ''}
                </div>
              </div>
            )
          })}
        </div>
      </div>

      {/* ── Right panel ── */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {isNew && (
          <ConfigDetail
            key="__new__"
            initial={null}
            isNew={true}
            onSaved={handleSaved}
            onDeleted={handleDeleted}
          />
        )}
        {!isNew && selectedRecord && (
          <ConfigDetail
            key={selectedRecord.name}
            initial={selectedRecord.config}
            isNew={false}
            onSaved={handleSaved}
            onDeleted={handleDeleted}
          />
        )}
        {!isNew && !selectedRecord && !loading && (
          <div className="panel-empty">Select a config to edit, or create a new one</div>
        )}
        {!isNew && !selectedRecord && loading && (
          <div className="panel-empty">Loading…</div>
        )}
      </div>
    </div>
  )
}
