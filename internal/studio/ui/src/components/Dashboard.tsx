import { useEffect, useState } from 'react'
import { fetchGatewayApis, fetchTargets } from '../api'
import type { ConnStatus, DeployRecord, GatewayState } from '../types'

interface StatCardProps {
  label: string
  value: string | number
  sub?: string
  accent?: string
}

function StatCard({ label, value, sub, accent }: StatCardProps) {
  return (
    <div style={{
      background: 'var(--panel)',
      border: '1px solid var(--border)',
      borderLeft: `3px solid ${accent ?? 'var(--accent)'}`,
      borderRadius: 12,
      padding: '18px 22px',
      minWidth: 160,
      flex: 1,
    }}>
      <div style={{ fontSize: 32, fontWeight: 700, color: accent ?? 'var(--accent)', lineHeight: 1 }}>
        {value}
      </div>
      <div style={{ fontSize: 13, color: 'var(--text)', marginTop: 6, fontWeight: 600 }}>
        {label}
      </div>
      {sub && (
        <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4 }}>
          {sub}
        </div>
      )}
    </div>
  )
}

interface DashboardProps {
  conn: ConnStatus
}

export default function Dashboard({ conn }: DashboardProps) {
  const [gatewayState, setGatewayState] = useState<GatewayState | null>(null)
  const [recentDeploys, setRecentDeploys] = useState<DeployRecord[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false

    async function load() {
      try {
        const [gw, targets] = await Promise.allSettled([
          fetchGatewayApis(),
          fetchTargets(),
        ])
        if (cancelled) return
        if (gw.status === 'fulfilled') setGatewayState(gw.value)
        if (targets.status === 'fulfilled') {
          const hist = targets.value.history ?? []
          setRecentDeploys(hist.slice(-3).reverse())
        }
      } finally {
        if (!cancelled) setLoading(false)
      }
    }

    load()
    const interval = setInterval(load, 10000)
    return () => { cancelled = true; clearInterval(interval) }
  }, [])

  // Count only parent flows (flows that have at least one API endpoint pointing to them).
  // Sub-flows (e.g. "my-route-hist-on", "my-route-hist-off") are internal compiler
  // artefacts and should not be shown to customers as independent flows.
  const parentFlowNames = new Set((gatewayState?.apis ?? []).map(a => a.flow_name).filter(Boolean))
  const flowCount = (gatewayState?.flows ?? []).filter(f => parentFlowNames.has(f.name)).length
  const apiCount = gatewayState?.apis?.length ?? 0
  const syncUUID = gatewayState?.sync_uuid ?? '—'

  const connColor = conn === 'ok' ? '#4caf50' : conn === 'error' ? '#ff7043' : 'var(--muted)'
  const connText = conn === 'ok' ? 'Connected' : conn === 'error' ? 'Unreachable' : 'Connecting…'

  return (
    <div>
      <h2 style={{ fontSize: 20, fontWeight: 700, marginBottom: 20, color: 'var(--text)' }}>
        Dashboard
      </h2>

      {loading && (
        <p style={{ color: 'var(--muted)', fontSize: 13, marginBottom: 16 }}>Loading…</p>
      )}

      {/* Stat cards */}
      <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', marginBottom: 28 }}>
        <StatCard label="Flows" value={flowCount} sub="active gateway flows" />
        <StatCard label="APIs" value={apiCount} sub="registered endpoints" />
        <StatCard
          label="Connection"
          value={connText}
          sub="backend gateway"
          accent={connColor}
        />
        <StatCard
          label="Sync UUID"
          value={syncUUID.length > 16 ? syncUUID.slice(0, 16) + '…' : syncUUID}
          sub="current gateway state"
          accent="#a78bfa"
        />
      </div>

      {/* Recent deploys */}
      <div style={{
        background: 'var(--panel)',
        border: '1px solid var(--border)',
        borderRadius: 12,
        overflow: 'hidden',
      }}>
        <div style={{
          padding: '11px 16px',
          borderBottom: '1px solid var(--border)',
          fontWeight: 600,
          fontSize: 13,
        }}>
          Recent Deploys
        </div>
        <div style={{ padding: 14 }}>
          {recentDeploys.length === 0 ? (
            <p style={{ color: 'var(--muted)', fontSize: 13 }}>No deploy history found.</p>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {recentDeploys.map((d, i) => {
                const succeeded = d.results?.filter(r => r.status >= 200 && r.status < 300).length ?? 0
                const total = d.results?.length ?? 0
                const allOk = succeeded === total && total > 0
                return (
                  <div key={i} style={{
                    background: 'var(--block-bg)',
                    border: '1px solid var(--border-hi)',
                    borderLeft: `3px solid ${allOk ? '#4caf50' : '#ff7043'}`,
                    borderRadius: 8,
                    padding: '10px 14px',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 14,
                  }}>
                    <div style={{ flex: 1 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text)' }}>
                        {d.release_id ?? 'unknown release'}
                      </div>
                      <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 2 }}>
                        {d.at?.T ? new Date(d.at.T).toLocaleString() : 'unknown time'}
                        {d.targets?.length ? ` · ${d.targets.join(', ')}` : ''}
                      </div>
                    </div>
                    <div style={{
                      fontSize: 11,
                      fontWeight: 700,
                      color: allOk ? '#4caf50' : '#ff7043',
                      flexShrink: 0,
                    }}>
                      {allOk ? `${succeeded}/${total} ok` : `${succeeded}/${total} ok`}
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
