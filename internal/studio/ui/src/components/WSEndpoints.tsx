import { useEffect, useState } from 'react'
import { fetchGatewayApis } from '../api'
import type { GatewayState } from '../types'

interface WSEnabledEndpoint {
  api_name: string
  path: string
  method: string
  inbound_flow?: string
  connect_flow?: string
  ping_interval_ms?: number
  max_message_size?: number
}

export default function WSEndpoints() {
  const [endpoints, setEndpoints] = useState<WSEnabledEndpoint[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    setErr('')
    fetchGatewayApis()
      .then((state: GatewayState) => {
        const wsEndpoints: WSEnabledEndpoint[] = []
        for (const api of state.apis ?? []) {
          // Check if main API has websocket enabled
          if ((api as any).websocket?.enabled) {
            wsEndpoints.push({
              api_name: api.name,
              path: api.path,
              method: api.method || 'ANY',
              inbound_flow: (api as any).websocket?.inbound_flow,
              connect_flow: (api as any).websocket?.connect_flow,
              ping_interval_ms: (api as any).websocket?.ping_interval_ms,
              max_message_size: (api as any).websocket?.max_message_size,
            })
          }
          // Check endpoint_configs for WebSocket enabled endpoints
          if ((api as any).endpoint_configs) {
            for (const ep of (api as any).endpoint_configs) {
              if (ep.websocket?.enabled) {
                wsEndpoints.push({
                  api_name: api.name,
                  path: ep.path,
                  method: ep.method || 'ANY',
                  inbound_flow: ep.websocket?.inbound_flow,
                  connect_flow: ep.websocket?.connect_flow,
                  ping_interval_ms: ep.websocket?.ping_interval_ms,
                  max_message_size: ep.websocket?.max_message_size,
                })
              }
            }
          }
        }
        setEndpoints(wsEndpoints)
        setLoading(false)
      })
      .catch(e => {
        setErr(e instanceof Error ? e.message : 'Failed to load APIs')
        setLoading(false)
      })
  }, [])

  return (
    <div style={{ padding: '20px 24px' }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 20 }}>
        <div>
          <span style={{ fontSize: 15, fontWeight: 700 }}>WebSocket-Enabled Endpoints</span>
          <span style={{ color: 'var(--muted)', fontSize: 13, marginLeft: 10 }}>{endpoints.length} total</span>
          <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>
            API endpoints with WebSocket support
          </div>
        </div>
      </div>

      {err && <p className="status-err" style={{ marginBottom: 12 }}>{err}</p>}

      {loading ? (
        <p className="hint">Loading…</p>
      ) : endpoints.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 0', color: 'var(--muted)' }}>
          <div style={{ fontSize: 32, opacity: 0.3, marginBottom: 12 }}>⚡</div>
          <p>
            No WebSocket-enabled endpoints configured.
            <br />
            Add <code style={{ background: 'var(--bg)', padding: '1px 4px', borderRadius: 3, fontFamily: 'monospace' }}>websocket: {'{enabled: true}'}</code> to an API definition.
          </p>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ display: 'grid', gridTemplateColumns: '120px 80px 140px 140px 100px 120px', gap: 6, fontSize: 11, color: 'var(--muted)', fontWeight: 600, marginBottom: 6, paddingLeft: 4 }}>
            <span>API Name</span>
            <span>Method</span>
            <span>Path</span>
            <span>Inbound Flow</span>
            <span>Ping (ms)</span>
            <span>Max Message</span>
          </div>
          {endpoints.map((ep, idx) => (
            <div key={idx} style={{ display: 'grid', gridTemplateColumns: '120px 80px 140px 140px 100px 120px', gap: 6, alignItems: 'center', padding: '10px 8px', background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 6 }}>
              <span style={{ fontFamily: 'monospace', fontSize: 13, fontWeight: 600 }}>{ep.api_name}</span>
              <span
                style={{
                  fontSize: 10,
                  fontWeight: 700,
                  padding: '2px 6px',
                  borderRadius: 4,
                  color: '#fff',
                  background: METHOD_COLOR[ep.method] ?? '#64748b',
                  textAlign: 'center',
                  display: 'inline-block',
                }}
              >
                {ep.method}
              </span>
              <span style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={ep.path}>
                {ep.path}
              </span>
              <span style={{ fontFamily: 'monospace', fontSize: 11, color: ep.inbound_flow ? 'var(--accent)' : 'var(--muted)' }}>
                {ep.inbound_flow || '—'}
              </span>
              <span style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--muted)' }}>
                {ep.ping_interval_ms != null ? ep.ping_interval_ms : '—'}
              </span>
              <span style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--muted)' }}>
                {ep.max_message_size != null ? formatBytes(ep.max_message_size) : '—'}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

const METHOD_COLOR: Record<string, string> = {
  GET: '#22c55e',
  POST: '#3b82f6',
  PUT: '#f97316',
  PATCH: '#eab308',
  DELETE: '#ef4444',
  ANY: '#64748b',
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return Math.round((bytes / Math.pow(k, i)) * 100) / 100 + ' ' + sizes[i]
}
