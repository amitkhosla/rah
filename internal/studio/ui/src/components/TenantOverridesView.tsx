import React, { useEffect, useState } from 'react'
import { listAllV2Overrides, TenantOverrideRow } from '../api'

export default function TenantOverridesView() {
  const [rows, setRows] = useState<TenantOverrideRow[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [filter, setFilter] = useState('')

  async function load() {
    setLoading(true); setErr('')
    try { setRows(await listAllV2Overrides()) }
    catch (e) { setErr(e instanceof Error ? e.message : 'Failed to load') }
    finally { setLoading(false) }
  }

  useEffect(() => { void load() }, [])

  const filtered = rows.filter(r =>
    !filter || r.alias.toLowerCase().includes(filter.toLowerCase())
  )

  return (
    <div style={{ padding: '24px 28px', maxWidth: 1100 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
        <div>
          <h2 style={{ fontSize: 17, fontWeight: 700, color: 'var(--text)', margin: 0 }}>Rate Limit Overrides</h2>
          <p style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4 }}>
            Tenants with custom rate limit behaviour — blocked, disabled, scaled, or per-config overrides.
          </p>
        </div>
        <button
          onClick={load}
          style={{ padding: '6px 14px', borderRadius: 6, border: '1px solid var(--border-hi)',
            background: 'transparent', color: 'var(--muted)', cursor: 'pointer', fontSize: 12 }}
        >
          Refresh
        </button>
      </div>

      <input
        className="input"
        placeholder="Filter by tenant alias…"
        value={filter}
        onChange={e => setFilter(e.target.value)}
        style={{ marginBottom: 16, maxWidth: 320 }}
      />

      {loading && <p style={{ color: 'var(--muted)', fontSize: 13 }}>Loading…</p>}
      {err && <p style={{ color: '#f87171', fontSize: 13 }}>{err}</p>}
      {!loading && !err && filtered.length === 0 && (
        <p style={{ color: 'var(--muted)', fontSize: 13 }}>
          {filter ? 'No tenants match the filter.' : 'No overrides configured.'}
        </p>
      )}

      {filtered.map(row => (
        <div key={row.tenant_id} style={{
          marginBottom: 12, border: '1px solid var(--border)', borderRadius: 10,
          background: 'var(--panel)', overflow: 'hidden',
        }}>
          <div style={{
            display: 'flex', alignItems: 'center', gap: 10, padding: '10px 16px',
            borderBottom: row.overrides.length > 0 ? '1px solid var(--border)' : 'none',
          }}>
            <span style={{ fontWeight: 600, fontSize: 14, color: 'var(--text)' }}>{row.alias}</span>
            <span style={{ fontSize: 11, color: 'var(--muted)', fontFamily: 'monospace' }}>#{row.tenant_id}</span>
            {row.global_blocked && (
              <span style={badge('#ef4444', 'rgba(239,68,68,0.15)')}>BLOCKED</span>
            )}
            {row.global_rl_disabled && (
              <span style={badge('#f59e0b', 'rgba(245,158,11,0.15)')}>RL DISABLED</span>
            )}
            {!row.global_blocked && !row.global_rl_disabled && row.global_scale_pct !== 0 && (
              <span style={badge('#57b5ff', 'rgba(87,181,255,0.15)')}>
                {row.global_scale_pct > 0 ? `+${row.global_scale_pct}%` : `${row.global_scale_pct}%`} global
              </span>
            )}
            <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--muted)' }}>
              {row.overrides.length} config override{row.overrides.length !== 1 ? 's' : ''}
            </span>
          </div>

          {row.overrides.length > 0 && (
            <div style={{ padding: '8px 16px 12px' }}>
              <div style={{
                display: 'grid', gridTemplateColumns: '1fr 80px 90px 90px auto',
                gap: 8, fontSize: 11, color: 'var(--muted)', fontWeight: 600,
                marginBottom: 6, paddingLeft: 2,
              }}>
                <span>Config</span><span>Blocked</span><span>RL Disabled</span><span>Scale</span><span>Window Limits</span>
              </div>
              {row.overrides.map(o => (
                <div key={o.config_name} style={{
                  display: 'grid', gridTemplateColumns: '1fr 80px 90px 90px auto',
                  gap: 8, alignItems: 'center', fontSize: 13,
                  padding: '5px 8px', background: 'var(--block-bg)',
                  border: '1px solid var(--border)', borderRadius: 6, marginBottom: 4,
                }}>
                  <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>{o.config_name}</span>
                  <span>{o.blocked
                    ? <span style={badge('#ef4444', 'rgba(239,68,68,0.15)')}>Yes</span>
                    : <span style={{ color: 'var(--muted)' }}>—</span>}</span>
                  <span>{o.rl_disabled
                    ? <span style={badge('#f59e0b', 'rgba(245,158,11,0.15)')}>Yes</span>
                    : <span style={{ color: 'var(--muted)' }}>—</span>}</span>
                  <span style={{ fontFamily: 'monospace' }}>
                    {o.scale_override_pct !== 0
                      ? <span style={{ color: o.scale_override_pct > 0 ? '#34d399' : '#f87171' }}>
                          {o.scale_override_pct > 0 ? `+${o.scale_override_pct}%` : `${o.scale_override_pct}%`}
                        </span>
                      : <span style={{ color: 'var(--muted)' }}>—</span>}
                  </span>
                  <span style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--muted)' }}>
                    {o.window_limits && o.window_limits.length > 0 ? o.window_limits.join(', ') : '—'}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  )
}

function badge(color: string, bg: string): React.CSSProperties {
  return { fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 4, color, background: bg, letterSpacing: '0.05em' }
}
