import { useEffect, useState } from 'react'
import { listWSUpstreams } from '../api'
import type { WSUpstream } from '../api'

export default function WSUpstreams() {
  const [upstreams, setUpstreams] = useState<WSUpstream[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  async function load() {
    setLoading(true)
    setErr('')
    try {
      setUpstreams(await listWSUpstreams())
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to load WebSocket upstreams')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  // Auto-refresh every 30 seconds
  useEffect(() => {
    const interval = setInterval(() => {
      void load()
    }, 30000)
    return () => clearInterval(interval)
  }, [])

  function getStatusColor(status: string): string {
    if (status === 'connected' || status === 'ok') return '#22c55e'
    if (status === 'reconnecting' || status === 'connecting') return '#f59e0b'
    return '#ef4444'
  }

  function getStatusLabel(status: string): string {
    return status.charAt(0).toUpperCase() + status.slice(1)
  }

  return (
    <div style={{ padding: '20px 24px' }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 20 }}>
        <div>
          <span style={{ fontSize: 15, fontWeight: 700 }}>WebSocket Upstreams</span>
          <span style={{ color: 'var(--muted)', fontSize: 13, marginLeft: 10 }}>{upstreams.length} total</span>
          <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>
            Health status of upstream WebSocket connections (auto-refreshed every 30 seconds)
          </div>
        </div>
        <button className="btn" style={{ width: 'auto', padding: '0 18px', flexShrink: 0 }} onClick={load}>
          Refresh
        </button>
      </div>

      {err && <p className="status-err" style={{ marginBottom: 12 }}>{err}</p>}

      {loading ? (
        <p className="hint">Loading…</p>
      ) : upstreams.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 0', color: 'var(--muted)' }}>
          <div style={{ fontSize: 32, opacity: 0.3, marginBottom: 12 }}>⚡</div>
          <p>
            No WebSocket upstreams configured.
            <br />
            Add <code style={{ background: 'var(--bg)', padding: '1px 4px', borderRadius: 3, fontFamily: 'monospace' }}>ws_upstreams</code> to your gateway config.
          </p>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ display: 'grid', gridTemplateColumns: '160px 1fr 120px 120px', gap: 6, fontSize: 11, color: 'var(--muted)', fontWeight: 600, marginBottom: 6, paddingLeft: 4 }}>
            <span>Name</span>
            <span>URL</span>
            <span>Status</span>
            <span>Last Ping</span>
          </div>
          {upstreams.map(u => (
            <div key={u.name} style={{ display: 'grid', gridTemplateColumns: '160px 1fr 120px 120px', gap: 6, alignItems: 'center', padding: '10px 8px', background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 6 }}>
              <span style={{ fontFamily: 'monospace', fontSize: 13, fontWeight: 600 }}>{u.name}</span>
              <span style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={u.url}>
                {u.url}
              </span>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <span
                  style={{
                    width: 8,
                    height: 8,
                    borderRadius: '50%',
                    background: getStatusColor(u.status),
                    flexShrink: 0,
                  }}
                />
                <span style={{ fontSize: 11, color: getStatusColor(u.status), fontWeight: 600 }}>
                  {getStatusLabel(u.status)}
                </span>
              </div>
              <span style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--muted)' }}>
                {u.last_ping_rtt_ms != null ? `${u.last_ping_rtt_ms}ms` : '—'}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
