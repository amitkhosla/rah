import { useEffect, useState, useCallback } from 'react'
import { fetchAuditLog } from '../api'
import type { AuditRecord } from '../api'

// ── Badge helper for action colors ────────────────────────────────────

function ActionBadge({ action }: { action: string }) {
  let bgColor = 'rgba(107, 114, 128, 0.15)'
  let textColor = '#9ca3af'

  if (action.startsWith('login') || action.startsWith('logout')) {
    bgColor = 'rgba(87, 181, 255, 0.15)'
    textColor = '#57b5ff'
  } else if (action.startsWith('deploy')) {
    bgColor = 'rgba(167, 139, 250, 0.15)'
    textColor = '#a78bfa'
  } else if (action.startsWith('sync')) {
    bgColor = 'rgba(52, 211, 153, 0.15)'
    textColor = '#34d399'
  } else if (action.startsWith('tenant') || action.startsWith('ratelimit') || action.startsWith('tier')) {
    bgColor = 'rgba(245, 158, 11, 0.15)'
    textColor = '#f59e0b'
  } else if (action.startsWith('password')) {
    bgColor = 'rgba(107, 114, 128, 0.15)'
    textColor = '#9ca3af'
  } else if (action.startsWith('user')) {
    bgColor = 'rgba(236, 72, 153, 0.15)'
    textColor = '#ec4899'
  }

  return (
    <span style={{
      fontSize: 11,
      fontWeight: 700,
      background: bgColor,
      color: textColor,
      borderRadius: 6,
      padding: '2px 8px',
      display: 'inline-block',
      whiteSpace: 'nowrap',
    }}>
      {action}
    </span>
  )
}

// ── Status chip helper ────────────────────────────────────────────────

function StatusChip({ status }: { status: string }) {
  const isSuccess = status === 'success'
  const bgColor = isSuccess ? 'rgba(52, 211, 153, 0.15)' : 'rgba(239, 68, 68, 0.15)'
  const textColor = isSuccess ? '#34d399' : '#f87171'

  return (
    <span style={{
      fontSize: 11,
      fontWeight: 700,
      background: bgColor,
      color: textColor,
      borderRadius: 6,
      padding: '2px 8px',
      display: 'inline-block',
      whiteSpace: 'nowrap',
    }}>
      {status}
    </span>
  )
}

// ── Format timestamp ──────────────────────────────────────────────────

function formatTimestamp(ts: string): string {
  try {
    const d = new Date(ts)
    return d.toLocaleString()
  } catch {
    return ts
  }
}

// ── Main AuditLog component ───────────────────────────────────────────

export default function AuditLog() {
  const [records, setRecords] = useState<AuditRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [filterActor, setFilterActor] = useState('')
  const [filterAction, setFilterAction] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await fetchAuditLog()
      setRecords(Array.isArray(data) ? data : [])
    } catch (e: any) {
      setError(e.message ?? 'Failed to fetch audit log')
    } finally {
      setLoading(false)
    }
  }, [])

  // Fetch on mount and set 30s refresh
  useEffect(() => {
    load()
    const timer = setInterval(load, 30000)
    return () => clearInterval(timer)
  }, [load])

  // Filter records
  const filtered = records.filter(r => {
    if (filterActor && !r.actor.toLowerCase().includes(filterActor.toLowerCase())) return false
    if (filterAction && !r.action.toLowerCase().startsWith(filterAction.toLowerCase())) return false
    return true
  })

  // Extract unique action prefixes for dropdown
  const actionPrefixes = Array.from(new Set(
    records.map(r => {
      const parts = r.action.split('/')
      return parts[0]
    })
  )).sort()

  // Render loading
  if (loading && records.length === 0) {
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

  // Render error
  if (error && records.length === 0) {
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
      {/* ── Header with filters ── */}
      <div style={{
        padding: '16px 24px',
        borderBottom: '1px solid var(--border)',
        flexShrink: 0,
        display: 'flex',
        gap: 12,
        alignItems: 'center',
      }}>
        <div style={{ display: 'flex', gap: 12, alignItems: 'center', flex: 1 }}>
          {/* Actor filter */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <label style={{ fontSize: 11, color: 'var(--muted)', fontWeight: 600 }}>Actor</label>
            <input
              type="text"
              value={filterActor}
              onChange={e => setFilterActor(e.target.value)}
              placeholder="Filter…"
              style={{
                fontSize: 12,
                padding: '5px 10px',
                borderRadius: 6,
                border: '1px solid var(--border)',
                background: 'var(--panel)',
                color: 'var(--text)',
                width: 150,
              }}
            />
          </div>

          {/* Action prefix filter */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <label style={{ fontSize: 11, color: 'var(--muted)', fontWeight: 600 }}>Action</label>
            <select
              value={filterAction}
              onChange={e => setFilterAction(e.target.value)}
              style={{
                fontSize: 12,
                padding: '5px 10px',
                borderRadius: 6,
                border: '1px solid var(--border)',
                background: 'var(--panel)',
                color: 'var(--text)',
                cursor: 'pointer',
              }}
            >
              <option value="">All</option>
              {actionPrefixes.map(ap => (
                <option key={ap} value={ap}>{ap}</option>
              ))}
            </select>
          </div>
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
        {filtered.length === 0 && !loading ? (
          <div style={{
            padding: '40px 24px',
            textAlign: 'center',
            fontSize: 13,
            color: 'var(--muted)',
          }}>
            No audit records yet.
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
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Timestamp</th>
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Actor</th>
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Action</th>
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Resource</th>
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Status</th>
                <th style={{ padding: '6px 10px', textAlign: 'left', fontWeight: 700, borderBottom: '1px solid var(--border)' }}>Summary</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((record, idx) => (
                <tr
                  key={record.id}
                  style={{
                    background: idx % 2 === 0 ? 'transparent' : 'rgba(255,255,255,0.01)',
                    borderBottom: '1px solid var(--border)',
                    fontSize: 12,
                  }}
                >
                  <td style={{ padding: '6px 10px', color: 'var(--muted)', fontFamily: 'monospace', fontSize: 11 }}>
                    {formatTimestamp(record.timestamp)}
                  </td>
                  <td style={{ padding: '6px 10px', color: 'var(--text)' }}>
                    {record.actor}
                  </td>
                  <td style={{ padding: '6px 10px', color: 'var(--text)' }}>
                    <ActionBadge action={record.action} />
                  </td>
                  <td style={{ padding: '6px 10px', color: 'var(--text)', fontSize: 11 }}>
                    {record.resource_type}:{record.resource_id}
                  </td>
                  <td style={{ padding: '6px 10px', color: 'var(--text)' }}>
                    <StatusChip status={record.status} />
                  </td>
                  <td style={{ padding: '6px 10px', color: 'var(--text)' }}>
                    {record.summary}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {error && records.length > 0 && (
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
