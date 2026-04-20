import { useEffect, useRef, useState } from 'react'
import { fetchObsMetrics, fetchObsAccessLog, fetchObsApis, fetchObsTraces } from '../api'

// ── Types ─────────────────────────────────────────────────────────

interface GatewayMetrics {
  requests_total: number
  requests_5xx: number
  gateway_latency_total_ns: number
  upstream_latency_total_ns: number
  instruction_top_slow: NameLatency[]
  upstream_top_slow: NameLatency[]
}

interface NameLatency {
  name: string
  count: number
  total_latency_ns: number
  bytes_tx?: number
  bytes_rx?: number
}

interface AccessLogRecord {
  timestamp_ns: number
  api_name: string
  tenant_id: number
  tenant_key: string
  method: string
  path: string
  status: number
  total_ms: number
  gateway_ms: number
  upstream_ms: number
  ttfb_ms: number
  req_bytes: number
  res_bytes: number
}

interface TraceRecord {
  trace_id: number
  timestamp: number
  api_name: string
  tenant_id: number
  status: number
  total_ms: number
  payload?: string
}

interface TracePayloadEvent {
  seq?: number
  name: string
  duration_ns?: number
  total_ns?: number
  status?: number
  output?: Array<{ k: string; v: string }>
}

interface TracePayload {
  summary?: {
    duration_ns?: number
  }
  instructions?: TracePayloadEvent[]
  upstreams?: TracePayloadEvent[]
}

// ── Helpers ───────────────────────────────────────────────────────

function fmtNum(n: number): string {
  if (n === undefined || n === null) return '0'
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}

function fmtTime(ns: number): string {
  if (!ns) return '—'
  const d = new Date(ns / 1_000_000)
  return d.toLocaleTimeString()
}

function fmtNsAsMs(ns: number): string {
  if (!ns || ns <= 0) return '0 ms'
  return `${(ns / 1_000_000).toFixed(2)} ms`
}

function statusColor(status: number): string {
  if (status >= 500) return '#f87171'
  if (status >= 400) return '#fbbf24'
  if (status >= 200 && status < 300) return '#34d399'
  return 'var(--muted)'
}

// ── Stat Card ────────────────────────────────────────────────────

interface StatCardProps {
  label: string
  value: string
  sub?: string
  color?: string
}

function StatCard({ label, value, sub, color }: StatCardProps) {
  return (
    <div style={{
      background: 'var(--panel)',
      border: '1px solid var(--border)',
      borderLeft: `3px solid ${color ?? 'var(--accent)'}`,
      borderRadius: 10,
      padding: '16px 20px',
      flex: 1,
      minWidth: 140,
    }}>
      <div style={{ fontSize: 28, fontWeight: 700, color: color ?? 'var(--accent)', lineHeight: 1 }}>
        {value}
      </div>
      <div style={{ fontSize: 12, color: 'var(--text)', marginTop: 6, fontWeight: 600 }}>
        {label}
      </div>
      {sub && <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 3 }}>{sub}</div>}
    </div>
  )
}

// ── Status Chip ───────────────────────────────────────────────────

interface ChipProps {
  label: string
  active: boolean
  onClick: () => void
  color?: string
}

function Chip({ label, active, onClick, color }: ChipProps) {
  return (
    <button
      onClick={onClick}
      style={{
        padding: '3px 12px',
        borderRadius: 20,
        border: `1px solid ${active ? (color ?? 'var(--accent)') : 'var(--border)'}`,
        background: active ? (color ?? 'var(--accent)') : 'transparent',
        color: active ? '#fff' : 'var(--muted)',
        fontSize: 12,
        fontWeight: 600,
        cursor: 'pointer',
      }}
    >
      {label}
    </button>
  )
}

// ── Section Header ────────────────────────────────────────────────

function SectionHeader({ title, sub }: { title: string; sub?: string }) {
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{ fontSize: 15, fontWeight: 700, color: 'var(--text)' }}>{title}</div>
      {sub && <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>{sub}</div>}
    </div>
  )
}

// ── Main Component ────────────────────────────────────────────────

type StatusFilter = 'all' | '2xx' | '4xx' | '5xx'

export default function Observability() {
  const [metrics, setMetrics] = useState<GatewayMetrics | null>(null)
  const [accessLog, setAccessLog] = useState<AccessLogRecord[]>([])
  const [apiPerf, setApiPerf] = useState<NameLatency[]>([])
  const [traces, setTraces] = useState<TraceRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [currentTps, setCurrentTps] = useState<number | null>(null)
  const prevMetricsRef = useRef<{ total: number; ts: number } | null>(null)

  // Filters
  const [apiFilter, setApiFilter] = useState<string>('')
  const [tenantFilter, setTenantFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [tracesOpen, setTracesOpen] = useState(false)
  const [expandedTrace, setExpandedTrace] = useState<number | null>(null)

  const metricsTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const logTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // Derive status code for access log query
  function statusCodeForFilter(f: StatusFilter): number | undefined {
    if (f === '2xx') return 200
    if (f === '4xx') return 400
    if (f === '5xx') return 500
    return undefined
  }

  async function loadMetrics() {
    try {
      const data = await fetchObsMetrics(apiFilter || undefined)
      const m = data?.metrics as GatewayMetrics | undefined
      if (m) {
        setMetrics(m)
        const now = Date.now()
        const prev = prevMetricsRef.current
        if (prev && m.requests_total > prev.total) {
          const elapsed = (now - prev.ts) / 1000
          if (elapsed > 0) setCurrentTps((m.requests_total - prev.total) / elapsed)
        }
        prevMetricsRef.current = { total: m.requests_total, ts: now }
      }
    } catch {
      // metrics may be absent; don't error
    }
  }

  async function loadAccessLog() {
    try {
      const data = await fetchObsAccessLog({
        api: apiFilter || undefined,
        tenant: tenantFilter || undefined,
        status: statusCodeForFilter(statusFilter),
        limit: 50,
      })
      setAccessLog(data?.data ?? [])
    } catch (e: any) {
      // tolerate
    }
  }

  async function loadApis() {
    try {
      const data = await fetchObsApis()
      setApiPerf(data?.apis ?? [])
    } catch {
      // tolerate
    }
  }

  async function loadTraces() {
    try {
      const data = await fetchObsTraces({ api: apiFilter || undefined, limit: 20 })
      setTraces(data?.data ?? [])
    } catch {
      // tolerate
    }
  }

  async function loadAll() {
    setError(null)
    try {
      await Promise.all([loadMetrics(), loadAccessLog(), loadApis(), loadTraces()])
    } catch (e: any) {
      setError(e?.message ?? 'Failed to load observability data')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadAll()

    metricsTimerRef.current = setInterval(loadMetrics, 10_000)
    logTimerRef.current = setInterval(loadAccessLog, 5_000)

    return () => {
      if (metricsTimerRef.current) clearInterval(metricsTimerRef.current)
      if (logTimerRef.current) clearInterval(logTimerRef.current)
    }
  }, [])

  // Reload access log when filters change
  useEffect(() => {
    loadAccessLog()
  }, [apiFilter, tenantFilter, statusFilter])

  // Reload metrics when api filter changes
  useEffect(() => {
    loadMetrics()
  }, [apiFilter])

  // ── Derived stats ──────────────────────────────────────────────
  const totalReqs = metrics?.requests_total ?? 0
  const total5xx = metrics?.requests_5xx ?? 0
  const errorRate = totalReqs > 0 ? ((total5xx / totalReqs) * 100).toFixed(1) + '%' : '0%'

  // P99 and P50 from top-slow list are not directly available from in-memory metrics snapshot.
  // We show average gateway latency instead, derived from totals.
  const avgGatewayMs = totalReqs > 0
    ? ((metrics?.gateway_latency_total_ns ?? 0) / totalReqs / 1e6).toFixed(2)
    : '—'
  const avgUpstreamMs = totalReqs > 0
    ? ((metrics?.upstream_latency_total_ns ?? 0) / totalReqs / 1e6).toFixed(2)
    : '—'

  // ── Sort API perf by request count (from upstream_top_slow name counts) ──
  const sortedApis = [...apiPerf].sort((a, b) => b.count - a.count)

  // ── Filter access log for display ─────────────────────────────
  const displayLog = accessLog.slice(0, 50)

  // ── Distinct APIs for dropdown ─────────────────────────────────
  const apiNames = Array.from(new Set([
    ...apiPerf.map(a => a.name),
    ...accessLog.map(r => r.api_name).filter(Boolean),
  ])).sort()

  if (loading) {
    return (
      <div style={{ padding: 40, color: 'var(--muted)', fontSize: 14 }}>
        Loading observability data…
      </div>
    )
  }

  return (
    <div style={{ padding: 28, overflowY: 'auto', height: '100%', boxSizing: 'border-box' }}>
      <div style={{ marginBottom: 24 }}>
        <h2 style={{ margin: 0, fontSize: 20, fontWeight: 700, color: 'var(--text)' }}>
          Observability
        </h2>
        <p style={{ margin: '4px 0 0', fontSize: 12, color: 'var(--muted)' }}>
          Live gateway metrics, access log, and traces. Metrics refresh every 10s, access log every 5s.
        </p>
      </div>

      {error && (
        <div style={{
          background: 'rgba(248,113,113,0.1)',
          border: '1px solid #f87171',
          borderRadius: 8,
          padding: '10px 14px',
          marginBottom: 20,
          fontSize: 13,
          color: '#f87171',
        }}>
          {error}
        </div>
      )}

      {/* ── Stat Cards ── */}
      <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', marginBottom: 28 }}>
        <StatCard
          label="Total Requests"
          value={fmtNum(totalReqs)}
          sub="since gateway start"
        />
        <StatCard
          label="Error Rate"
          value={errorRate}
          sub={`${fmtNum(total5xx)} 5xx errors`}
          color={total5xx > 0 ? '#f87171' : '#34d399'}
        />
        <StatCard
          label="Avg Gateway Latency"
          value={avgGatewayMs === '—' ? '—' : avgGatewayMs + ' ms'}
          sub="avg across all requests"
          color="#a78bfa"
        />
        <StatCard
          label="Avg Upstream Latency"
          value={avgUpstreamMs === '—' ? '—' : avgUpstreamMs + ' ms'}
          sub="avg across all requests"
          color="#fbbf24"
        />
        <StatCard
          label="Current TPS"
          value={currentTps !== null ? currentTps.toFixed(1) : '—'}
          sub="requests/sec (10s window)"
          color="#34d399"
        />
      </div>

      {/* ── API Performance Table ── */}
      <div style={{
        background: 'var(--panel)',
        border: '1px solid var(--border)',
        borderRadius: 10,
        marginBottom: 24,
        overflow: 'hidden',
      }}>
        <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border)' }}>
          <SectionHeader
            title="API Performance"
            sub="Top APIs by request count. Click a row to filter access log."
          />
        </div>
        {sortedApis.length === 0 ? (
          <div style={{ padding: '20px 18px', color: 'var(--muted)', fontSize: 13 }}>
            No API data yet — metrics are collected as requests arrive.
          </div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr style={{ background: 'rgba(255,255,255,0.03)' }}>
                  {['API Name', 'Requests', 'Avg Latency ms', 'Bytes TX', 'Bytes RX'].map(h => (
                    <th key={h} style={{
                      padding: '8px 14px',
                      textAlign: 'left',
                      color: 'var(--muted)',
                      fontWeight: 600,
                      fontSize: 11,
                      borderBottom: '1px solid var(--border)',
                      whiteSpace: 'nowrap',
                    }}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {sortedApis.map((api, i) => {
                  const avgMs = api.count > 0
                    ? (api.total_latency_ns / api.count / 1e6).toFixed(2)
                    : '—'
                  const isSelected = apiFilter === api.name
                  return (
                    <tr
                      key={api.name + i}
                      onClick={() => setApiFilter(isSelected ? '' : api.name)}
                      style={{
                        cursor: 'pointer',
                        background: isSelected ? 'rgba(87,181,255,0.07)' : 'transparent',
                        borderBottom: '1px solid var(--border)',
                      }}
                    >
                      <td style={{ padding: '8px 14px', color: isSelected ? 'var(--accent)' : 'var(--text)', fontWeight: isSelected ? 600 : 400 }}>
                        {api.name}
                      </td>
                      <td style={{ padding: '8px 14px', color: 'var(--text)' }}>{fmtNum(api.count)}</td>
                      <td style={{ padding: '8px 14px', color: 'var(--muted)' }}>{avgMs}</td>
                      <td style={{ padding: '8px 14px', color: 'var(--muted)' }}>{api.bytes_tx ? fmtNum(api.bytes_tx) : '—'}</td>
                      <td style={{ padding: '8px 14px', color: 'var(--muted)' }}>{api.bytes_rx ? fmtNum(api.bytes_rx) : '—'}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* ── Access Log ── */}
      <div style={{
        background: 'var(--panel)',
        border: '1px solid var(--border)',
        borderRadius: 10,
        marginBottom: 24,
        overflow: 'hidden',
      }}>
        <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border)' }}>
          <SectionHeader title="Access Log" sub="50 most recent requests. Live tail every 5s." />

          {/* Filter bar */}
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center', marginTop: 10 }}>
            <select
              value={apiFilter}
              onChange={e => setApiFilter(e.target.value)}
              style={{
                padding: '4px 10px',
                background: 'var(--bg)',
                border: '1px solid var(--border)',
                borderRadius: 6,
                color: 'var(--text)',
                fontSize: 12,
                cursor: 'pointer',
              }}
            >
              <option value="">All APIs</option>
              {apiNames.map(n => (
                <option key={n} value={n}>{n}</option>
              ))}
            </select>

            <input
              type="text"
              placeholder="Filter tenant…"
              value={tenantFilter}
              onChange={e => setTenantFilter(e.target.value)}
              style={{
                padding: '4px 10px',
                background: 'var(--bg)',
                border: '1px solid var(--border)',
                borderRadius: 6,
                color: 'var(--text)',
                fontSize: 12,
                width: 140,
              }}
            />

            <div style={{ display: 'flex', gap: 6 }}>
              <Chip label="All" active={statusFilter === 'all'} onClick={() => setStatusFilter('all')} />
              <Chip label="2xx" active={statusFilter === '2xx'} onClick={() => setStatusFilter('2xx')} color="#34d399" />
              <Chip label="4xx" active={statusFilter === '4xx'} onClick={() => setStatusFilter('4xx')} color="#fbbf24" />
              <Chip label="5xx" active={statusFilter === '5xx'} onClick={() => setStatusFilter('5xx')} color="#f87171" />
            </div>
          </div>
        </div>

        {displayLog.length === 0 ? (
          <div style={{ padding: '20px 18px', color: 'var(--muted)', fontSize: 13 }}>
            No access log entries yet. Requests will appear here as they arrive.
          </div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={{ background: 'rgba(255,255,255,0.03)' }}>
                  {['Time', 'Method', 'Path', 'Status', 'Total ms', 'Gateway ms', 'Upstream ms', 'Tenant', 'Bytes'].map(h => (
                    <th key={h} style={{
                      padding: '7px 12px',
                      textAlign: 'left',
                      color: 'var(--muted)',
                      fontWeight: 600,
                      fontSize: 11,
                      borderBottom: '1px solid var(--border)',
                      whiteSpace: 'nowrap',
                    }}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {displayLog.map((rec, i) => (
                  <tr key={i} style={{ borderBottom: '1px solid var(--border)' }}>
                    <td style={{ padding: '6px 12px', color: 'var(--muted)', whiteSpace: 'nowrap' }}>
                      {fmtTime(rec.timestamp_ns)}
                    </td>
                    <td style={{ padding: '6px 12px', color: 'var(--accent)', fontWeight: 600, whiteSpace: 'nowrap' }}>
                      {rec.method}
                    </td>
                    <td style={{ padding: '6px 12px', color: 'var(--text)', maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {rec.path}
                    </td>
                    <td style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>
                      <span style={{
                        color: statusColor(rec.status),
                        fontWeight: 700,
                      }}>
                        {rec.status}
                      </span>
                    </td>
                    <td style={{ padding: '6px 12px', color: 'var(--text)', whiteSpace: 'nowrap' }}>
                      {rec.total_ms?.toFixed(2) ?? '—'}
                    </td>
                    <td style={{ padding: '6px 12px', color: 'var(--muted)', whiteSpace: 'nowrap' }}>
                      {rec.gateway_ms?.toFixed(2) ?? '—'}
                    </td>
                    <td style={{ padding: '6px 12px', color: 'var(--muted)', whiteSpace: 'nowrap' }}>
                      {rec.upstream_ms?.toFixed(2) ?? '—'}
                    </td>
                    <td style={{ padding: '6px 12px', color: 'var(--muted)', maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {rec.tenant_key || rec.tenant_id || '—'}
                    </td>
                    <td style={{ padding: '6px 12px', color: 'var(--muted)', whiteSpace: 'nowrap' }}>
                      {rec.res_bytes ?? '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* ── Traces Panel ── */}
      <div style={{
        background: 'var(--panel)',
        border: '1px solid var(--border)',
        borderRadius: 10,
        overflow: 'hidden',
      }}>
        <button
          onClick={() => setTracesOpen(o => !o)}
          style={{
            width: '100%',
            padding: '14px 18px',
            background: 'transparent',
            border: 'none',
            borderBottom: tracesOpen ? '1px solid var(--border)' : 'none',
            cursor: 'pointer',
            textAlign: 'left',
            display: 'flex',
            alignItems: 'center',
            gap: 8,
          }}
        >
          <span style={{ fontSize: 13, color: 'var(--muted)', userSelect: 'none' }}>
            {tracesOpen ? '▾' : '▸'}
          </span>
          <div>
            <div style={{ fontSize: 15, fontWeight: 700, color: 'var(--text)' }}>Traces</div>
            <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 1 }}>
              Last {traces.length} sampled or error traces
            </div>
          </div>
        </button>

        {tracesOpen && (
          traces.length === 0 ? (
            <div style={{ padding: '20px 18px', color: 'var(--muted)', fontSize: 13 }}>
              No traces recorded yet. Traces are sampled from live requests.
            </div>
          ) : (
            <div>
              {traces.map((trace, i) => {
                const isExpanded = expandedTrace === i
                let payload: any = null
                if (isExpanded && trace.payload) {
                  try { payload = JSON.parse(trace.payload) } catch { payload = trace.payload }
                }
                return (
                  <div key={i} style={{ borderBottom: '1px solid var(--border)' }}>
                    <div
                      onClick={() => setExpandedTrace(isExpanded ? null : i)}
                      style={{
                        padding: '10px 18px',
                        cursor: 'pointer',
                        display: 'flex',
                        alignItems: 'center',
                        gap: 14,
                        background: isExpanded ? 'rgba(87,181,255,0.05)' : 'transparent',
                      }}
                    >
                      <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 70 }}>
                        {trace.timestamp ? new Date(trace.timestamp * 1000).toLocaleTimeString() : '—'}
                      </span>
                      <span style={{ fontSize: 13, color: 'var(--text)', fontWeight: 600, flex: 1 }}>
                        {trace.api_name || '—'}
                      </span>
                      <span style={{ fontSize: 12, color: statusColor(trace.status), fontWeight: 700, minWidth: 36 }}>
                        {trace.status}
                      </span>
                      <span style={{ fontSize: 12, color: 'var(--muted)', minWidth: 70, textAlign: 'right' }}>
                        {trace.total_ms?.toFixed(2) ?? '—'} ms
                      </span>
                      <span style={{ fontSize: 11, color: 'var(--accent)' }}>
                        {isExpanded ? '▴' : '▾'}
                      </span>
                    </div>

                    {isExpanded && (
                      <div style={{
                        padding: '12px 18px 16px 18px',
                        background: 'rgba(0,0,0,0.2)',
                        fontSize: 12,
                      }}>
                        <div style={{ marginBottom: 8, color: 'var(--muted)' }}>
                          Trace ID: <span style={{ color: 'var(--text)' }}>{trace.trace_id}</span>
                          {' · '}
                          Tenant: <span style={{ color: 'var(--text)' }}>{trace.tenant_id}</span>
                        </div>
                        {payload && typeof payload !== 'string' ? (
                          <BlockView payload={payload as TracePayload} />
                        ) : payload ? (
                          <pre style={{
                            margin: 0,
                            padding: '10px 12px',
                            background: 'var(--bg)',
                            borderRadius: 6,
                            border: '1px solid var(--border)',
                            color: 'var(--text)',
                            fontSize: 11,
                            overflowX: 'auto',
                            maxHeight: 300,
                            overflowY: 'auto',
                          }}>
                            {typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2)}
                          </pre>
                        ) : (
                          <div style={{ color: 'var(--muted)' }}>No payload data</div>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )
        )}
      </div>
    </div>
  )
}

// ── Block Abstraction ─────────────────────────────────────────────
//
// Studio knows what each instruction means because it compiled it.
// We map raw instruction names → human-readable blocks so customers
// see "Classify Request" and "Generate Response" instead of
// "classify_llm" and "llm_call[haiku]".

interface BlockGroup {
  label: string
  totalNs: number
  events: TracePayloadEvent[]
  headline: Record<string, string>   // merged KVs for headline display
}

function buildBlockGroups(instructions: TracePayloadEvent[]): BlockGroup[] {
  if (instructions.length === 0) return []

  const classifySeq = instructions.find(e => e.name === 'classify_llm')?.seq ?? Infinity
  const llmCallSeq = instructions.find(e => e.name?.startsWith('llm_call'))?.seq ?? Infinity

  const labeled: Array<{ event: TracePayloadEvent; blockLabel: string | null }> = []
  for (let i = 0; i < instructions.length; i++) {
    const ev = instructions[i]
    const name = ev.name ?? ''
    const seq = ev.seq ?? i
    let blockLabel: string | null = null

    if (name === 'classify_llm') {
      blockLabel = 'Classify Request'
    } else if (name.startsWith('llm_call')) {
      blockLabel = 'Generate Response'
    } else if (name === 'route_llm') {
      blockLabel = seq < classifySeq ? 'Select Classifier' : 'Select Model'
    } else if (name === 'set_const') {
      // Fold set_const after route_llm into the same block
      const prev = labeled.length > 0 ? labeled[labeled.length - 1] : null
      if (prev?.event.name === 'route_llm') blockLabel = prev.blockLabel
    } else if (name === 'CHECK_CONTEXT_FIT' || name === 'check_context_fit') {
      blockLabel = 'Measure Request'
    } else if (name === 'bind_body' || name === 'bind_header') {
      blockLabel = 'Extract Request'
    } else if ((name === 'load_history' || name === 'append_message') && seq < llmCallSeq) {
      blockLabel = 'Load History'
    } else if ((name === 'save_history' || name === 'append_message') && seq > llmCallSeq) {
      blockLabel = 'Save History'
    } else if (name === 'respond') {
      blockLabel = 'Return Response'
    }

    labeled.push({ event: ev, blockLabel })
  }

  const groups: BlockGroup[] = []
  for (const { event, blockLabel } of labeled) {
    if (!blockLabel) continue
    const last = groups.length > 0 ? groups[groups.length - 1] : null
    if (last && last.label === blockLabel) {
      last.events.push(event)
      last.totalNs += Number(event.duration_ns ?? 0)
      for (const kv of (event.output ?? [])) { if (kv.v) last.headline[kv.k] = kv.v }
    } else {
      const headline: Record<string, string> = {}
      for (const kv of (event.output ?? [])) { if (kv.v) headline[kv.k] = kv.v }
      groups.push({ label: blockLabel, totalNs: Number(event.duration_ns ?? 0), events: [event], headline })
    }
  }

  return groups
}

const LONG_VALUE_KEYS = new Set(['classifier_output', 'prompt', 'system', 'response', 'provider_error'])
const TRUNCATE_AT = 300

function InstructionEventRow({ ev }: { ev: TracePayloadEvent }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const kvs = (ev.output ?? []).filter(o => o.v)
  return (
    <div style={{ padding: '4px 8px', fontSize: 11, borderLeft: '2px solid var(--border)', marginBottom: 2 }}>
      <span style={{ color: 'var(--text)', fontWeight: 500 }}>{ev.name}</span>
      {' '}
      <span style={{ color: 'var(--muted)' }}>{fmtNsAsMs(Number(ev.duration_ns ?? 0))}</span>
      {kvs.map(({ k, v }) => {
        const str = String(v)
        const isLong = LONG_VALUE_KEYS.has(k) && str.length > TRUNCATE_AT
        const isExp = expanded.has(k)
        return (
          <div key={k} style={{ marginTop: 2 }}>
            <span style={{ color: 'var(--muted)' }}>{k}:</span>{' '}
            <span style={{ color: 'var(--text)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
              {isLong && !isExp ? str.slice(0, TRUNCATE_AT) + '…' : str}
            </span>
            {isLong && (
              <button
                onClick={() => setExpanded(prev => {
                  const next = new Set(prev)
                  isExp ? next.delete(k) : next.add(k)
                  return next
                })}
                style={{ marginLeft: 6, fontSize: 10, color: 'var(--accent)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
              >
                {isExp ? 'show less' : `show all (${str.length} chars)`}
              </button>
            )}
          </div>
        )
      })}
    </div>
  )
}

function BlockView({ payload }: { payload: TracePayload }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const instructions = Array.isArray(payload.instructions) ? payload.instructions : []
  const groups = buildBlockGroups(instructions)
  const totalNs = Number(payload.summary?.duration_ns ?? 0)

  // Fallback to raw timeline when no blocks can be identified
  if (groups.length === 0) return <TraceTimeline payload={payload} />

  function toggle(key: string) {
    setExpanded(prev => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  return (
    <div style={{ background: 'var(--bg)', borderRadius: 6, border: '1px solid var(--border)', padding: '10px 12px' }}>
      <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
        Execution Blocks • total {fmtNsAsMs(totalNs)}
      </div>
      {groups.map((group, i) => {
        const key = `${group.label}:${i}`
        const isExp = expanded.has(key)
        const kv = group.headline
        return (
          <div key={key} style={{ marginBottom: 4 }}>
            {/* Block header row */}
            <div
              onClick={() => toggle(key)}
              style={{
                display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer',
                padding: '5px 8px', borderRadius: 4,
                background: isExp ? 'rgba(87,181,255,0.07)' : 'transparent',
              }}
            >
              <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 12, userSelect: 'none' }}>
                {isExp ? '▾' : '▸'}
              </span>
              <span style={{ flex: 1, fontSize: 13, fontWeight: 600, color: 'var(--text)' }}>
                {group.label}
              </span>
              <span style={{ fontSize: 11, color: 'var(--muted)', whiteSpace: 'nowrap' }}>
                {fmtNsAsMs(group.totalNs)}
              </span>
              {kv.token_count && (
                <span style={{ fontSize: 11, color: '#a78bfa', whiteSpace: 'nowrap' }}>{kv.token_count} tok</span>
              )}
              {kv.selected_model && (
                <span style={{ fontSize: 11, color: 'var(--accent)', whiteSpace: 'nowrap' }}>→ {kv.selected_model}</span>
              )}
              {kv.model && !kv.selected_model && (
                <span style={{ fontSize: 11, color: 'var(--accent)', whiteSpace: 'nowrap' }}>{kv.model}</span>
              )}
              {(kv.input_tokens || kv.output_tokens) && (
                <span style={{ fontSize: 11, color: 'var(--muted)', whiteSpace: 'nowrap' }}>
                  {kv.input_tokens ?? '?'}→{kv.output_tokens ?? '?'} tok
                </span>
              )}
              {kv.cost_usd && (
                <span style={{ fontSize: 11, color: '#34d399', whiteSpace: 'nowrap' }}>${kv.cost_usd}</span>
              )}
              {kv.classifier_output && (
                <span style={{ fontSize: 11, color: '#fbbf24', maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {kv.classifier_output.slice(0, 40)}
                </span>
              )}
            </div>

            {/* Expanded: individual instruction events */}
            {isExp && (
              <div style={{ marginLeft: 20, marginTop: 2, marginBottom: 4 }}>
                {group.events.map((ev, j) => (
                  <InstructionEventRow key={ev.seq ?? j} ev={ev} />
                ))}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function TraceTimeline({ payload }: { payload: TracePayload }) {
  const instructionEvents = Array.isArray(payload.instructions) ? payload.instructions : []
  const upstreamEvents = Array.isArray(payload.upstreams) ? payload.upstreams : []
  const events = [
    ...instructionEvents.map((e) => ({
      kind: 'instruction' as const,
      label: e.name || 'instruction',
      durationNs: Number(e.duration_ns ?? 0),
      seq: Number(e.seq ?? 0),
      status: undefined as number | undefined,
    })),
    ...upstreamEvents.map((e) => ({
      kind: 'upstream' as const,
      label: e.name || 'upstream',
      durationNs: Number(e.total_ns ?? e.duration_ns ?? 0),
      seq: Number(e.seq ?? 0),
      status: e.status,
    })),
  ].filter(e => e.durationNs >= 0)

  events.sort((a, b) => (a.seq - b.seq) || (b.durationNs - a.durationNs))

  const totalNsFromSummary = Number(payload.summary?.duration_ns ?? 0)
  const totalNsFromEvents = events.reduce((acc, e) => acc + e.durationNs, 0)
  const totalNs = totalNsFromSummary > 0 ? totalNsFromSummary : totalNsFromEvents
  const maxNs = Math.max(...events.map(e => e.durationNs), totalNs, 1)

  if (events.length === 0) {
    return <div style={{ color: 'var(--muted)' }}>No timeline events available</div>
  }

  return (
    <div style={{
      background: 'var(--bg)',
      borderRadius: 6,
      border: '1px solid var(--border)',
      padding: '10px 12px',
    }}>
      <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
        Timeline • total {fmtNsAsMs(totalNs)}
      </div>

      {events.map((e, i) => {
        const widthPct = Math.max(2, (e.durationNs / maxNs) * 100)
        const color = e.kind === 'upstream' ? '#fbbf24' : '#57b5ff'
        return (
          <div key={`${e.kind}-${e.seq}-${i}`} style={{ marginBottom: 8 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4, gap: 8 }}>
              <div style={{ color: 'var(--text)', fontSize: 12, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {e.seq > 0 ? `${e.seq}. ` : ''}{e.label}
                {e.kind === 'upstream' && e.status ? ` (${e.status})` : ''}
              </div>
              <div style={{ color: 'var(--muted)', fontSize: 11, whiteSpace: 'nowrap' }}>
                {fmtNsAsMs(e.durationNs)}
              </div>
            </div>
            <div style={{ height: 8, background: 'rgba(255,255,255,0.06)', borderRadius: 999, overflow: 'hidden' }}>
              <div style={{ width: `${widthPct}%`, height: '100%', background: color }} />
            </div>
          </div>
        )
      })}
    </div>
  )
}
