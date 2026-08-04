import { useEffect, useState, useCallback } from 'react'
import { listMigrations } from '../api'
import type { MigrationStatus } from '../api'

function formatTimestamp(ts: string): string {
  try {
    const d = new Date(ts)
    return d.toLocaleString()
  } catch {
    return ts
  }
}

export default function Migrations() {
  const [migrations, setMigrations] = useState<MigrationStatus[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await listMigrations()
      setMigrations(Array.isArray(data.migrations) ? data.migrations : [])
    } catch (e: any) {
      setError(e.message ?? 'Failed to fetch migrations')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  if (loading && migrations.length === 0) {
    return (
      <div style={{
        padding: '20px 24px',
        height: '100%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: 13,
        color: 'var(--muted)',
      }}>
        Loading…
      </div>
    )
  }

  if (error && migrations.length === 0) {
    return (
      <div style={{
        padding: '20px 24px',
        height: '100%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: 13,
        color: '#f87171',
      }}>
        {error}
      </div>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      {/* ── Header ── */}
      <div style={{
        padding: '16px 24px',
        borderBottom: '1px solid var(--border)',
        flexShrink: 0,
        display: 'flex',
        gap: 12,
        alignItems: 'center',
        justifyContent: 'space-between',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontSize: 15, fontWeight: 700 }}>Migrations</span>
          <span style={{
            fontSize: 11,
            color: 'var(--muted)',
            background: 'rgba(255,255,255,0.08)',
            padding: '2px 8px',
            borderRadius: 4,
          }}>
            {migrations.length} applied
          </span>
        </div>

        {/* Refresh button */}
        <button
          onClick={load}
          title="Refresh"
          style={{
            flexShrink: 0,
            fontSize: 12,
            padding: '5px 12px',
            borderRadius: 6,
            border: '1px solid var(--border)',
            background: 'transparent',
            color: 'var(--muted)',
            cursor: 'pointer',
          }}
        >
          ↺ Refresh
        </button>
      </div>

      {/* ── Table container ── */}
      <div style={{ flex: 1, overflowY: 'auto', background: 'var(--bg)' }}>
        {migrations.length === 0 ? (
          <div style={{
            padding: '40px 24px',
            textAlign: 'center',
            fontSize: 13,
            color: 'var(--muted)',
          }}>
            No migrations applied yet.
          </div>
        ) : (
          <table style={{
            width: '100%',
            borderCollapse: 'collapse',
            fontSize: 12,
          }}>
            <thead>
              <tr style={{
                background: 'rgba(255,255,255,0.04)',
                fontSize: 11,
                fontWeight: 700,
                textTransform: 'uppercase',
                letterSpacing: '0.06em',
                color: 'var(--muted)',
              }}>
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Version</th>
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Name</th>
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Applied At</th>
              </tr>
            </thead>
            <tbody>
              {migrations.map((m, idx) => (
                <tr
                  key={m.version}
                  style={{
                    background: idx % 2 === 0 ? 'transparent' : 'rgba(255,255,255,0.01)',
                    borderBottom: '1px solid var(--border)',
                    fontSize: 12,
                  }}
                >
                  <td style={{ padding: '6px 10px', color: 'var(--text)', fontWeight: 600 }}>
                    {m.version}
                  </td>
                  <td style={{ padding: '6px 10px', color: 'var(--text)' }}>
                    {m.name}
                  </td>
                  <td style={{ padding: '6px 10px', color: 'var(--muted)', fontFamily: 'monospace', fontSize: 11 }}>
                    {formatTimestamp(m.applied_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {error && migrations.length > 0 && (
        <div style={{
          padding: '8px 24px',
          borderTop: '1px solid var(--border)',
          fontSize: 11,
          color: '#f87171',
          flexShrink: 0,
        }}>
          {error}
        </div>
      )}
    </div>
  )
}
