import { useEffect, useRef, useState } from 'react'
import {
  fetchObsMetrics, fetchObsAccessLog, fetchObsApis, fetchObsTraces,
  fetchObsDetailLogConfig, updateObsDetailLogConfig,
  fetchObsConfig, updateObsConfig,
  fetchInstrSchema,
  listObservabilityApps,
  type ObsRuntimeConfig,
  type InstrSchemaRow,
} from '../api'

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
  app_name?: string
  tenant_id: number
  tenant_key: string
  method: string
  path: string
  status: number
  total_ms: number
  gateway_ms: number
  upstream_ms: number
  ttfb_ms: number
  conn_setup_ms?: number
  transfer_ms?: number
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
  method?: string          // HTTP method (GET, POST, etc.)
  payload?: string         // V1 — JSON blob, used by existing rendering
  // V2 fields:
  instr_pcs?: number[]
  instr_durs_ns?: number[]
  endpoint_id?: number
  api_version_id?: number
}

interface TracePayloadEvent {
  seq?: number
  name: string
  duration_ns?: number
  total_ns?: number
  status?: number
  step_idx?: number  // flow step index (-1 = system/infra, 0+ = user step)
  output?: Array<{ k: string; v: string }>
  // upstream-only fields (populated for entries in the upstreams[] array)
  host?: string
  url?: string
  bytes_sent?: number
  bytes_received?: number
  dns_duration_ns?: number
  connect_duration_ns?: number
  tls_duration_ns?: number
  ttfb_ns?: number
  conn_reused?: boolean
  attempt?: number
  err?: string
  req_headers?: Record<string, string>
  res_headers?: Record<string, string>
  res_body?: string
}

interface TracePayload {
  summary?: {
    duration_ns?: number
    method?: string
    path?: string
    status?: number
    tenant_id?: number
    api_id?: number
  }
  instructions?: TracePayloadEvent[]
  upstreams?: TracePayloadEvent[]
  request_headers?: Record<string, string>
  query_string?: string
}

interface RouterTraceDetails {
  classifierPrompt?: string
  classifierSystem?: string
  classifierResponse?: string
  finalPrompt?: string
  finalSystem?: string
  finalResponse?: string
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

function fmtBytes(n?: number): string {
  if (!n || n <= 0) return '0 B'
  if (n >= 1_048_576) return (n / 1_048_576).toFixed(1) + ' MB'
  if (n >= 1_024) return (n / 1_024).toFixed(1) + ' KB'
  return `${n} B`
}

function snip(value?: string, n = 220): string {
  if (!value) return ''
  return value.length <= n ? value : value.slice(0, n) + '…'
}

function kvToMap(kvs?: Array<{ k: string; v: string }>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const kv of kvs ?? []) {
    if (kv?.k && kv?.v) out[kv.k] = kv.v
  }
  return out
}

function deriveRouterTraceDetails(payload: TracePayload): RouterTraceDetails {
  const instructions = Array.isArray(payload.instructions) ? payload.instructions : []
  const classify = instructions.find(e => e.name === 'classify_llm')
  const llmCalls = instructions.filter(e => e.name?.startsWith('llm_call'))
  const finalLLM = llmCalls[llmCalls.length - 1]

  const clsKV = kvToMap(classify?.output)
  const finalKV = kvToMap(finalLLM?.output)

  return {
    classifierPrompt: clsKV.prompt,
    classifierSystem: clsKV.system,
    classifierResponse: clsKV.response || clsKV.classifier_output,
    finalPrompt: finalKV.prompt,
    finalSystem: finalKV.system,
    finalResponse: finalKV.response,
  }
}

function statusColor(status: number): string {
  if (status >= 500) return '#f87171'
  if (status >= 400) return '#fbbf24'
  if (status >= 200 && status < 300) return '#34d399'
  return 'var(--muted)'
}

async function fetchAndCacheSchema(
  apiName: string,
  endpointID: number,
  cache: Map<string, InstrSchemaRow[]>
): Promise<InstrSchemaRow[]> {
  const key = `${apiName}:${endpointID}`
  if (cache.has(key)) return cache.get(key)!
  try {
    const rows: InstrSchemaRow[] = await fetchInstrSchema(apiName)
    const filtered = rows.filter(r => r.endpoint_id === endpointID)
    cache.set(key, filtered)
    return filtered
  } catch {
    return []
  }
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
  const [accessLogError, setAccessLogError] = useState<string | null>(null)
  const [logLimit, setLogLimit] = useState(50)
  const [traceLimit, setTraceLimit] = useState(20)
  const [expandedLogRows, setExpandedLogRows] = useState<Set<number>>(new Set())
  const [currentTps, setCurrentTps] = useState<number | null>(null)
  const prevMetricsRef = useRef<{ total: number; ts: number } | null>(null)

  // Filters
  const [apiFilter, setApiFilter] = useState<string>('')
  const [appFilter, setAppFilter] = useState<string>('')
  const [availableApps, setAvailableApps] = useState<string[]>([])
  const [tenantFilter, setTenantFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [logsOpen, setLogsOpen] = useState(true)
  const [tracesOpen, setTracesOpen] = useState(false)
  const [expandedTrace, setExpandedTrace] = useState<number | null>(null)
  const [detailLogEnabled, setDetailLogEnabled] = useState(false)
  const [detailLogPath, setDetailLogPath] = useState('/app/logs/detailLogging.log')
  const [detailLogMsg, setDetailLogMsg] = useState<string | null>(null)
  const [detailLogSaving, setDetailLogSaving] = useState(false)
  const [showConnTiming, setShowConnTiming] = useState<boolean>(() =>
    localStorage.getItem('rah_show_conn_timing') === 'true'
  )

  // ── Trace & log runtime config ─────────────────────────────────────
  const [obsCfg, setObsCfg]         = useState<ObsRuntimeConfig | null>(null)
  const [obsCfgOpen, setObsCfgOpen] = useState(false)
  const [obsCfgSaving, setObsCfgSaving] = useState(false)
  const [obsCfgMsg, setObsCfgMsg]   = useState<string | null>(null)
  // Draft values (only applied on Save)
  const [draftTrace,     setDraftTrace]     = useState(false)
  const [draftSampleRate,setDraftSampleRate]= useState(0)
  const [draftInstrTiming,setDraftInstrTiming]= useState(true)
  const [draftInfoLog,   setDraftInfoLog]   = useState(true)
  const [draftLogFields, setDraftLogFields] = useState('')  // comma-separated

  const metricsTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const logTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const instrSchemaCache = useRef<Map<string, InstrSchemaRow[]>>(new Map())
  const [v2Payloads, setV2Payloads] = useState<Map<number, TracePayload>>(new Map())

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

  async function loadAccessLog(limit?: number) {
    try {
      const data = await fetchObsAccessLog({
        api: apiFilter || undefined,
        app: appFilter || undefined,
        tenant: tenantFilter || undefined,
        status: statusCodeForFilter(statusFilter),
        limit: limit ?? logLimit,
      })
      setAccessLog(data?.data ?? [])
      setAccessLogError(null)
    } catch (e: any) {
      setAccessLogError(e?.message ?? 'Failed to load access log')
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

  async function loadTraces(limit?: number) {
    try {
      const data = await fetchObsTraces({ api: apiFilter || undefined, limit: limit ?? traceLimit })
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

  async function loadDetailLogConfig() {
    try {
      const cfg = await fetchObsDetailLogConfig()
      setDetailLogEnabled(!!cfg.enabled)
      setDetailLogPath(cfg.path || '/app/logs/detailLogging.log')
    } catch {
      // silently ignore if endpoint unavailable
    }
  }

  async function saveDetailLogConfig() {
    setDetailLogSaving(true)
    setDetailLogMsg(null)
    try {
      const cfg = await updateObsDetailLogConfig({ enabled: detailLogEnabled, path: detailLogPath })
      setDetailLogEnabled(!!cfg.enabled)
      setDetailLogPath(cfg.path || '')
      setDetailLogMsg('Detail log config saved.')
    } catch (e: any) {
      setDetailLogMsg(`Failed to save detail log config: ${e?.message ?? String(e)}`)
    } finally {
      setDetailLogSaving(false)
    }
  }

  async function loadObsConfig() {
    try {
      const cfg = await fetchObsConfig()
      setObsCfg(cfg)
      setDraftTrace(cfg.trace_mode)
      setDraftSampleRate(cfg.trace_sample_rate)
      setDraftInstrTiming(cfg.instruction_timing_enabled)
      setDraftInfoLog(cfg.info_log_enabled)
      setDraftLogFields(cfg.info_log_fields.join(', '))
    } catch {
      // config endpoint may not be reachable in all setups
    }
  }

  async function saveObsConfig() {
    setObsCfgSaving(true)
    setObsCfgMsg(null)
    try {
      await updateObsConfig({
        trace_mode:                  draftTrace,
        trace_sample_rate:           draftSampleRate,
        instruction_timing_enabled:  draftInstrTiming,
        info_log_enabled:            draftInfoLog,
        info_log_fields:             draftLogFields.split(',').map(s => s.trim()).filter(Boolean),
      })
      setObsCfgMsg('Saved.')
      await loadObsConfig()
    } catch (e: any) {
      setObsCfgMsg('Error: ' + (e?.message ?? 'unknown'))
    } finally {
      setObsCfgSaving(false)
    }
  }

  async function loadObservabilityApps() {
    try {
      const data = await listObservabilityApps()
      setAvailableApps(data?.apps ?? [])
    } catch {
      // tolerate
    }
  }

  useEffect(() => {
    loadAll()
    loadDetailLogConfig()
    loadObsConfig()
    loadObservabilityApps()

    metricsTimerRef.current = setInterval(loadMetrics, 10_000)
    logTimerRef.current = setInterval(() => { loadAccessLog(); loadApis() }, 5_000)

    return () => {
      if (metricsTimerRef.current) clearInterval(metricsTimerRef.current)
      if (logTimerRef.current) clearInterval(logTimerRef.current)
    }
  }, [])

  // Reload access log when filters change
  useEffect(() => {
    loadAccessLog()
  }, [apiFilter, appFilter, tenantFilter, statusFilter])

  // Reload metrics when api filter changes
  useEffect(() => {
    loadMetrics()
  }, [apiFilter])

  // Load V2 instruction schema when a V2 trace is expanded
  useEffect(() => {
    if (expandedTrace === null) return
    const trace = traces[expandedTrace]
    if (!trace || !trace.instr_pcs?.length) return
    if (v2Payloads.has(expandedTrace)) return  // already loaded

    const endpointID = trace.endpoint_id ?? 0
    fetchAndCacheSchema(trace.api_name, endpointID, instrSchemaCache.current).then(schema => {
      const instructions: TracePayloadEvent[] = trace.instr_pcs!.map((pc, i) => {
        const row = schema.find(s => s.pc === pc)
        return {
          seq: i,
          name: row?.step_name ?? row?.step_type ?? String(pc),
          duration_ns: trace.instr_durs_ns?.[i] ?? 0,
          output: [],
        }
      })
      const syntheticPayload: TracePayload = {
        summary: {
          status: trace.status,
          duration_ns: Math.round(trace.total_ms * 1_000_000),
        },
        instructions,
      }
      setV2Payloads(prev => new Map(prev).set(expandedTrace, syntheticPayload))
    })
  }, [expandedTrace, traces])

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

  // accessLog is already fetched with the user-selected logLimit — apply client-side app filter
  const displayLog = accessLog.filter(r => !appFilter || r.app_name === appFilter)

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

      {/* ── Trace & Log Settings panel ─────────────────────────────── */}
      <div style={{
        marginBottom: 20,
        borderRadius: 8,
        border: '1px solid var(--border)',
        background: 'var(--panel)',
        overflow: 'hidden',
      }}>
        {/* Collapsible header */}
        <button
          onClick={() => setObsCfgOpen(o => !o)}
          style={{
            width: '100%', display: 'flex', alignItems: 'center', justifyContent: 'space-between',
            padding: '10px 14px', background: 'none', border: 'none', cursor: 'pointer',
            color: 'var(--text)', fontSize: 13, fontWeight: 600, textAlign: 'left',
          }}
        >
          <span>Trace &amp; Log Settings</span>
          <span style={{ fontSize: 11, color: 'var(--muted)', marginLeft: 8 }}>
            {obsCfg
              ? `trace: ${obsCfg.trace_mode ? `on (${(obsCfg.trace_sample_rate * 100).toFixed(0)}% sample)` : 'off'}  ·  instr timing: ${obsCfg.instruction_timing_enabled ? 'on' : 'off'}`
              : 'loading…'}
            {' '}{obsCfgOpen ? '▲' : '▼'}
          </span>
        </button>

        {obsCfgOpen && (
          <div style={{ padding: '0 14px 14px', borderTop: '1px solid var(--border)' }}>
            <p style={{ margin: '10px 0 14px', fontSize: 12, color: 'var(--muted)', lineHeight: 1.5 }}>
              These settings take effect immediately — no deploy needed. They control what the
              gateway records at runtime. Changes are lost on gateway restart (use env vars for persistence).
            </p>

            {/* Trace mode */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px 24px' }}>
              <div>
                <div style={{ fontSize: 11, fontWeight: 700, color: 'var(--muted)', marginBottom: 6, textTransform: 'uppercase', letterSpacing: '0.05em' }}>Distributed Tracing</div>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', marginBottom: 8 }}>
                  <input type="checkbox" checked={draftTrace} onChange={e => setDraftTrace(e.target.checked)} />
                  <span style={{ fontSize: 13 }}>Enable trace collection</span>
                </label>
                <p style={{ margin: '0 0 8px 0', fontSize: 11, color: 'var(--muted)' }}>
                  When on, sampled requests get a full timeline of every instruction with its duration. View traces in the Traces section below.
                </p>
                {draftTrace && (
                  <div>
                    <label style={{ fontSize: 12, color: 'var(--muted)', display: 'block', marginBottom: 4 }}>
                      Sample rate: <strong style={{ color: 'var(--text)' }}>{(draftSampleRate * 100).toFixed(0)}%</strong>
                    </label>
                    <input type="range" min={0} max={1} step={0.01}
                      value={draftSampleRate}
                      onChange={e => setDraftSampleRate(Number(e.target.value))}
                      style={{ width: '100%' }} />
                    <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 10, color: 'var(--muted)' }}>
                      <span>0% (off)</span><span>50%</span><span>100% (all requests)</span>
                    </div>
                    <p style={{ margin: '6px 0 0', fontSize: 11, color: 'var(--muted)' }}>
                      High sample rates add ~1–5µs per request. Start at 5–10% in production.
                    </p>
                  </div>
                )}
              </div>

              <div>
                <div style={{ fontSize: 11, fontWeight: 700, color: 'var(--muted)', marginBottom: 6, textTransform: 'uppercase', letterSpacing: '0.05em' }}>Instruction Timing</div>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', marginBottom: 8 }}>
                  <input type="checkbox" checked={draftInstrTiming} onChange={e => setDraftInstrTiming(e.target.checked)} />
                  <span style={{ fontSize: 13 }}>Per-instruction timing</span>
                </label>
                <p style={{ margin: '0 0 16px', fontSize: 11, color: 'var(--muted)' }}>
                  Records how long each step type takes on average. Visible in the "Slow Instructions" table above. Near-zero overhead (~10ns/instruction).
                </p>

                <div style={{ fontSize: 11, fontWeight: 700, color: 'var(--muted)', marginBottom: 6, textTransform: 'uppercase', letterSpacing: '0.05em' }}>Access Log</div>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', marginBottom: 8 }}>
                  <input type="checkbox" checked={draftInfoLog} onChange={e => setDraftInfoLog(e.target.checked)} />
                  <span style={{ fontSize: 13 }}>Enable access log</span>
                </label>
                <div>
                  <label style={{ fontSize: 12, color: 'var(--muted)', display: 'block', marginBottom: 4 }}>
                    Log fields <span style={{ fontWeight: 400 }}>(comma-separated)</span>
                  </label>
                  <input
                    className="input"
                    placeholder="api_id, tenant_id, status, duration_ns"
                    value={draftLogFields}
                    onChange={e => setDraftLogFields(e.target.value)}
                  />
                  <p style={{ margin: '4px 0 0', fontSize: 11, color: 'var(--muted)' }}>
                    Available: api_id, tenant_id, status, duration_ns, gateway_duration_ns, upstream_duration_ns, upstream_calls, client_bytes_sent
                  </p>
                </div>
              </div>
            </div>

            {/* Per-step trace/log note */}
            <div style={{
              marginTop: 14, padding: '8px 12px', borderRadius: 6,
              background: 'rgba(87,181,255,0.06)', border: '1px solid rgba(87,181,255,0.2)',
              fontSize: 12, color: 'var(--muted)',
            }}>
              <strong style={{ color: 'var(--accent)' }}>Per-step controls</strong> — In the Flow Designer,
              expand any step and use <em>Log result as</em> and <em>Capture in trace</em> to selectively
              record individual step outputs without changing global settings.
            </div>

            <div style={{ marginTop: 12, display: 'flex', alignItems: 'center', gap: 10 }}>
              <button
                className="btn primary"
                onClick={saveObsConfig}
                disabled={obsCfgSaving}
              >
                {obsCfgSaving ? 'Saving…' : 'Apply'}
              </button>
              <button className="btn muted" onClick={loadObsConfig}>Reset</button>
              {obsCfgMsg && (
                <span style={{ fontSize: 12, color: obsCfgMsg.startsWith('Error') ? '#f87171' : '#34d399' }}>
                  {obsCfgMsg}
                </span>
              )}
            </div>
          </div>
        )}
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

      {/* ── Detail Log Controls ── */}
      <div style={{
        background: 'var(--panel)',
        border: '1px solid var(--border)',
        borderRadius: 10,
        padding: '12px 16px',
        marginBottom: 16,
      }}>
        <div style={{ fontSize: 14, fontWeight: 700, color: 'var(--text)', marginBottom: 6 }}>
          Full Detail Logs (JSONL)
        </div>
        <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
          Enable to write full classifier/final LLM payloads to a gateway file.
          Extract from Docker with:{' '}
          <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.06)', padding: '1px 5px', borderRadius: 4 }}>
            docker cp &lt;container&gt;:/app/logs/detailLogging.log ./detailLogging.log
          </code>
          {' '}or mount <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.06)', padding: '1px 5px', borderRadius: 4 }}>./logs:/app/logs</code> in your compose file.
        </div>
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, cursor: 'pointer' }}>
          <input type="checkbox" checked={detailLogEnabled} onChange={e => setDetailLogEnabled(e.target.checked)} />
          <span style={{ fontSize: 12, color: 'var(--text)' }}>Enable full detail log file</span>
        </label>
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, cursor: 'pointer' }}>
          <input type="checkbox" checked={showConnTiming} onChange={e => {
            setShowConnTiming(e.target.checked)
            localStorage.setItem('rah_show_conn_timing', e.target.checked ? 'true' : 'false')
          }} />
          <span style={{ fontSize: 12, color: 'var(--text)' }}>
            Show connection timing (TCP/TLS setup + response transfer) in access log and traces
            <span style={{ color: 'var(--muted)', marginLeft: 4 }}>— collected free on each connection, zero latency impact</span>
          </span>
        </label>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <input
            value={detailLogPath}
            onChange={e => setDetailLogPath(e.target.value)}
            placeholder="/app/logs/detailLogging.log"
            style={{
              flex: 1, minWidth: 260, padding: '6px 10px',
              background: 'var(--bg)', border: '1px solid var(--border)',
              borderRadius: 6, color: 'var(--text)', fontSize: 12,
            }}
          />
          <button
            onClick={saveDetailLogConfig}
            disabled={detailLogSaving}
            style={{
              padding: '6px 12px', borderRadius: 6,
              border: '1px solid var(--accent)', background: 'rgba(87,181,255,0.08)',
              color: 'var(--accent)', cursor: detailLogSaving ? 'wait' : 'pointer', fontSize: 12,
            }}
          >
            {detailLogSaving ? 'Saving…' : 'Save'}
          </button>
        </div>
        {detailLogMsg && (
          <div style={{ marginTop: 8, fontSize: 11, color: detailLogMsg.startsWith('Failed') ? '#ef4444' : '#22c55e' }}>
            {detailLogMsg}
          </div>
        )}
      </div>

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
      }}>
        {/* Collapsible header — same pattern as Traces */}
        <button
          onClick={() => setLogsOpen(o => !o)}
          style={{
            width: '100%',
            padding: '14px 18px',
            background: 'transparent',
            border: 'none',
            borderBottom: logsOpen ? '1px solid var(--border)' : 'none',
            cursor: 'pointer',
            textAlign: 'left',
            display: 'flex',
            alignItems: 'center',
            gap: 8,
          }}
        >
          <span style={{ fontSize: 13, color: 'var(--muted)', userSelect: 'none' }}>
            {logsOpen ? '▾' : '▸'}
          </span>
          <div style={{ flex: 1 }}>
            <div style={{ fontSize: 15, fontWeight: 700, color: 'var(--text)' }}>Access Log</div>
            <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 1 }}>
              {accessLog.length} most recent requests. Live tail every 5s.
            </div>
          </div>
          {logsOpen && (
            <>
              <select
                value={logLimit}
                onClick={e => e.stopPropagation()}
                onChange={e => { const n = Number(e.target.value); setLogLimit(n); loadAccessLog(n) }}
                style={{ padding: '3px 8px', borderRadius: 6, fontSize: 11, background: 'var(--bg)', border: '1px solid var(--border)', color: 'var(--muted)', cursor: 'pointer' }}
              >
                {[25, 50, 100, 250, 500].map(n => <option key={n} value={n}>{n} rows</option>)}
              </select>
              <button
                onClick={e => { e.stopPropagation(); loadAccessLog() }}
                style={{ padding: '4px 10px', borderRadius: 6, fontSize: 11, cursor: 'pointer', border: '1px solid var(--border)', background: 'transparent', color: 'var(--muted)' }}
              >↺ Refresh</button>
            </>
          )}
        </button>

        {logsOpen && (
          <>
            {/* Error + filter bar */}
            <div style={{ padding: '10px 18px', borderBottom: '1px solid var(--border)' }}>
              {accessLogError && (
                <div style={{ fontSize: 11, color: '#f87171', marginBottom: 8 }}>
                  ⚠ Access log unavailable: {accessLogError}
                </div>
              )}
              <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
                <select
                  value={apiFilter}
                  onChange={e => setApiFilter(e.target.value)}
                  style={{ padding: '4px 10px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text)', fontSize: 12, cursor: 'pointer' }}
                >
                  <option value="">All APIs</option>
                  {apiNames.map(n => <option key={n} value={n}>{n}</option>)}
                </select>
                <select
                  value={appFilter}
                  onChange={e => setAppFilter(e.target.value)}
                  style={{ padding: '4px 10px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text)', fontSize: 12, cursor: 'pointer' }}
                >
                  <option value="">All Apps</option>
                  {availableApps.map(n => <option key={n} value={n}>{n}</option>)}
                </select>
                <input
                  type="text"
                  placeholder="Filter tenant…"
                  value={tenantFilter}
                  onChange={e => setTenantFilter(e.target.value)}
                  style={{ padding: '4px 10px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text)', fontSize: 12, width: 140 }}
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
                      {['Time', 'Method', 'Path', 'Status', 'Total ms', 'Gateway ms', 'Upstream ms', 'Tenant', 'App', 'Bytes'].map(h => (
                        <th key={h} style={{ padding: '7px 12px', textAlign: 'left', color: 'var(--muted)', fontWeight: 600, fontSize: 11, borderBottom: '1px solid var(--border)', whiteSpace: 'nowrap' }}>{h}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {displayLog.map((rec, i) => {
                      const isExpanded = expandedLogRows.has(i)
                      const toggleRow = () => setExpandedLogRows(prev => {
                        const next = new Set(prev)
                        isExpanded ? next.delete(i) : next.add(i)
                        return next
                      })
                      return (
                        <>
                          <tr
                            key={`row-${i}`}
                            onClick={toggleRow}
                            style={{ borderBottom: isExpanded ? 'none' : '1px solid var(--border)', cursor: 'pointer' }}
                          >
                            <td style={{ padding: '6px 12px', color: 'var(--muted)', whiteSpace: 'nowrap' }}>{fmtTime(rec.timestamp_ns)}</td>
                            <td style={{ padding: '6px 12px', color: 'var(--accent)', fontWeight: 600, whiteSpace: 'nowrap' }}>{rec.method}</td>
                            <td style={{ padding: '6px 12px', color: 'var(--text)', maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{rec.path}</td>
                            <td style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>
                              <span style={{ color: statusColor(rec.status), fontWeight: 700 }}>{rec.status}</span>
                            </td>
                            <td style={{ padding: '6px 12px', color: 'var(--text)', whiteSpace: 'nowrap' }}>{rec.total_ms?.toFixed(2) ?? '—'}</td>
                            <td style={{ padding: '6px 12px', color: 'var(--muted)', whiteSpace: 'nowrap' }}>{rec.gateway_ms?.toFixed(2) ?? '—'}</td>
                            <td style={{ padding: '6px 12px', color: 'var(--muted)', whiteSpace: 'nowrap' }}>{rec.upstream_ms?.toFixed(2) ?? '—'}</td>
                            <td style={{ padding: '6px 12px', color: 'var(--muted)', maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{rec.tenant_key || rec.tenant_id || '—'}</td>
                            <td style={{ padding: '6px 12px', color: 'var(--muted)', maxWidth: 100, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{rec.app_name || '—'}</td>
                            <td style={{ padding: '6px 12px', color: 'var(--muted)', whiteSpace: 'nowrap' }}>
                              {rec.res_bytes > 0 ? fmtNum(rec.res_bytes) : '—'}
                            </td>
                          </tr>
                          {isExpanded && (
                            <tr key={`exp-${i}`} style={{ borderBottom: '1px solid var(--border)' }}>
                              <td colSpan={10} style={{ padding: '8px 16px 12px 24px', background: 'rgba(0,0,0,0.18)' }}>
                                <div style={{ display: 'flex', flexWrap: 'wrap', gap: '10px 28px', fontSize: 11 }}>
                                  <span><span style={{ color: 'var(--muted)' }}>TTFB:</span> <span style={{ color: 'var(--text)' }}>{rec.ttfb_ms?.toFixed(2) ?? '—'} ms</span></span>
                                  <span><span style={{ color: 'var(--muted)' }}>Response bytes:</span> <span style={{ color: 'var(--text)' }}>{rec.res_bytes ?? '—'}</span></span>
                                  <span><span style={{ color: 'var(--muted)' }}>Request bytes:</span> <span style={{ color: 'var(--text)' }}>{rec.req_bytes ?? '—'}</span></span>
                                  <span><span style={{ color: 'var(--muted)' }}>API:</span> <span style={{ color: 'var(--text)' }}>{rec.api_name || '—'}</span></span>
                                  {rec.app_name && <span><span style={{ color: 'var(--muted)' }}>App:</span> <span style={{ color: 'var(--text)' }}>{rec.app_name}</span></span>}
                                  <span><span style={{ color: 'var(--muted)' }}>Tenant:</span> <span style={{ color: 'var(--text)' }}>{rec.tenant_key || rec.tenant_id || '—'}</span></span>
                                  <span><span style={{ color: 'var(--muted)' }}>Total (client):</span> <span style={{ color: 'var(--text)', fontWeight: 600 }}>{rec.total_ms?.toFixed(3) ?? '—'} ms</span></span>
                                  <span><span style={{ color: 'var(--muted)' }}>Gateway only:</span> <span style={{ color: 'var(--text)' }}>{rec.gateway_ms?.toFixed(3) ?? '—'} ms</span></span>
                                  <span><span style={{ color: 'var(--muted)' }}>Upstream only:</span> <span style={{ color: 'var(--text)' }}>{rec.upstream_ms?.toFixed(3) ?? '—'} ms</span></span>
                                  {showConnTiming && rec.conn_setup_ms != null && rec.conn_setup_ms > 0 && (
                                    <span><span style={{ color: '#a78bfa' }}>Conn setup:</span> <span style={{ color: 'var(--text)' }}>{rec.conn_setup_ms.toFixed(2)} ms</span></span>
                                  )}
                                  {showConnTiming && rec.transfer_ms != null && rec.transfer_ms > 0 && (
                                    <span><span style={{ color: '#a78bfa' }}>Transfer time:</span> <span style={{ color: 'var(--text)' }}>{rec.transfer_ms.toFixed(2)} ms</span></span>
                                  )}
                                </div>
                              </td>
                            </tr>
                          )}
                        </>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
      </div>

      {/* ── Traces Panel ── */}
      <div style={{
        background: 'var(--panel)',
        border: '1px solid var(--border)',
        borderRadius: 10,
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
          <div style={{ flex: 1 }}>
            <div style={{ fontSize: 15, fontWeight: 700, color: 'var(--text)' }}>Traces</div>
            <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 1 }}>
              Last {traces.length} sampled or error traces
            </div>
          </div>
          {tracesOpen && (
            <select
              value={traceLimit}
              onClick={e => e.stopPropagation()}
              onChange={e => { const n = Number(e.target.value); setTraceLimit(n); loadTraces(n) }}
              style={{ padding: '3px 8px', borderRadius: 6, fontSize: 11, background: 'var(--bg)', border: '1px solid var(--border)', color: 'var(--muted)', cursor: 'pointer' }}
            >
              {[10, 20, 50, 100].map(n => <option key={n} value={n}>{n} traces</option>)}
            </select>
          )}
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
                let payload: TracePayload | string | null = null
                if (isExpanded) {
                  if (trace.instr_pcs?.length) {
                    // V2 trace: use loaded schema-joined payload, or show loading
                    payload = v2Payloads.get(i) ?? null
                  } else if (trace.payload) {
                    // V1 trace: parse JSON blob as before
                    try { payload = JSON.parse(trace.payload) } catch { payload = trace.payload }
                  }
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
                      {trace.method && (
                        <span style={{ fontSize: 11, color: '#60a5fa', fontWeight: 600, minWidth: 50 }}>
                          {trace.method}
                        </span>
                      )}
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
                        maxHeight: 600,
                        overflowY: 'auto',
                      }}>
                        <div style={{ marginBottom: 8, color: 'var(--muted)' }}>
                          Trace ID: <span style={{ color: 'var(--text)' }}>{trace.trace_id}</span>
                          {' · '}
                          Tenant: <span style={{ color: 'var(--text)' }}>{trace.tenant_id}</span>
                        </div>
                        {payload && typeof payload !== 'string' ? (
                          <>
                            <IncomingRequestPanel payload={payload as TracePayload} />
                            <RouterTraceSummary payload={payload as TracePayload} />
                            <TraceViewTabs payload={payload as TracePayload} />
                          </>
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
                        ) : isExpanded && trace.instr_pcs?.length ? (
                          <div style={{ color: 'var(--muted)' }}>Loading instruction schema…</div>
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

const ROUTER_TRUNCATE_AT = 300

function RouterField({ label, value, color }: { label: string; value: string; color: string }) {
  const [expanded, setExpanded] = useState(false)
  const isLong = value.length > ROUTER_TRUNCATE_AT
  return (
    <div>
      <span style={{ color }}>{label}:</span>{' '}
      <span style={{ color: 'var(--text)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
        {isLong && !expanded ? value.slice(0, ROUTER_TRUNCATE_AT) + '…' : value}
      </span>
      {isLong && (
        <button
          onClick={() => setExpanded(e => !e)}
          style={{ marginLeft: 6, fontSize: 10, color: 'var(--accent)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
        >
          {expanded ? 'show less' : `show all (${value.length} chars)`}
        </button>
      )}
    </div>
  )
}

function IncomingRequestPanel({ payload }: { payload: TracePayload }) {
  const [collapsed, setCollapsed] = useState(false)
  const method = payload.summary?.method
  const path = payload.summary?.path
  const query = payload.query_string
  const headers = payload.request_headers
  const hasContent = method || path || query || (headers && Object.keys(headers).length > 0)
  if (!hasContent) return null

  return (
    <div style={{
      marginBottom: 10,
      background: 'var(--bg)',
      borderRadius: 6,
      border: '1px solid var(--border)',
      fontSize: 11,
      overflow: 'hidden',
    }}>
      <button
        onClick={() => setCollapsed(c => !c)}
        style={{
          display: 'flex', alignItems: 'center', gap: 6,
          width: '100%', textAlign: 'left',
          padding: '7px 12px',
          background: 'rgba(255,255,255,0.03)',
          border: 'none', borderBottom: collapsed ? 'none' : '1px solid var(--border)',
          color: 'var(--muted)', fontSize: 12, cursor: 'pointer',
        }}
      >
        <span style={{ fontSize: 10 }}>{collapsed ? '▶' : '▾'}</span>
        <span>Incoming Request</span>
        {method && path && (
          <span style={{ marginLeft: 8, color: 'var(--text)', fontWeight: 600 }}>
            {method} {path}{query ? '?' + query : ''}
          </span>
        )}
      </button>
      {!collapsed && (
        <div style={{ padding: '8px 12px' }}>
          {/* Method + path + query as a code line if not already shown */}
          {(method || path) && (
            <div style={{ marginBottom: 8, fontFamily: 'monospace', color: 'var(--text)', fontSize: 12 }}>
              <span style={{ color: '#60a5fa', fontWeight: 700, marginRight: 8 }}>{method}</span>
              <span>{path}</span>
              {query && <span style={{ color: 'var(--muted)' }}>?{query}</span>}
            </div>
          )}

          {/* Query params as individual rows */}
          {query && (() => {
            const params: Array<[string, string]> = []
            try {
              new URLSearchParams(query).forEach((v, k) => params.push([k, v]))
            } catch { /* ignore */ }
            if (params.length === 0) return null
            return (
              <div style={{ marginBottom: 8 }}>
                <div style={{ color: 'var(--muted)', marginBottom: 4, fontWeight: 600, letterSpacing: '0.05em', textTransform: 'uppercase', fontSize: 10 }}>Query Params</div>
                <table style={{ borderCollapse: 'collapse', width: '100%' }}>
                  <tbody>
                    {params.map(([k, v]) => (
                      <tr key={k}>
                        <td style={{ padding: '2px 8px 2px 0', color: '#a78bfa', fontFamily: 'monospace', whiteSpace: 'nowrap', width: 1 }}>{k}</td>
                        <td style={{ padding: '2px 0', color: 'var(--text)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{v}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          })()}

          {/* Request headers */}
          {headers && Object.keys(headers).length > 0 && (
            <div>
              <div style={{ color: 'var(--muted)', marginBottom: 4, fontWeight: 600, letterSpacing: '0.05em', textTransform: 'uppercase', fontSize: 10 }}>Headers</div>
              <table style={{ borderCollapse: 'collapse', width: '100%' }}>
                <tbody>
                  {Object.entries(headers).map(([k, v]) => (
                    <tr key={k}>
                      <td style={{ padding: '2px 8px 2px 0', color: '#34d399', fontFamily: 'monospace', whiteSpace: 'nowrap', width: 1 }}>{k}</td>
                      <td style={{ padding: '2px 0', color: 'var(--text)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{v}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function RouterTraceSummary({ payload }: { payload: TracePayload }) {
  const d = deriveRouterTraceDetails(payload)
  const hasAny = !!(d.classifierPrompt || d.classifierResponse || d.finalPrompt || d.finalResponse)
  if (!hasAny) return null

  return (
    <div style={{
      marginBottom: 10,
      background: 'var(--bg)',
      borderRadius: 6,
      border: '1px solid var(--border)',
      padding: '10px 12px',
      fontSize: 11,
    }}>
      <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 8 }}>
        AI Router Details (classifier + final LLM)
      </div>
      <div style={{ display: 'grid', gap: 6 }}>
        {d.classifierPrompt && <RouterField label="Classifier prompt" value={d.classifierPrompt} color="#a78bfa" />}
        {d.classifierSystem && <RouterField label="Classifier system" value={d.classifierSystem} color="#a78bfa" />}
        {d.classifierResponse && <RouterField label="Classifier response" value={d.classifierResponse} color="#fbbf24" />}
        {d.finalPrompt && <RouterField label="Final LLM prompt" value={d.finalPrompt} color="var(--accent)" />}
        {d.finalSystem && <RouterField label="Final LLM system" value={d.finalSystem} color="var(--accent)" />}
        {d.finalResponse && <RouterField label="Final LLM response" value={d.finalResponse} color="#34d399" />}
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

// Maps internal engine instruction names to the user-facing action names shown in flow JSON.
// Internal names differ when the compiler renames for clarity (e.g. "return" → "early_return"
// to distinguish intentional stop from error stop, or all-caps for arithmetic steps).
const INSTRUCTION_DISPLAY_NAME: Record<string, string> = {
  early_return:  'return',
  CONCAT:        'concat',
  TO_LOWER:      'to_lower',
  TO_UPPER:      'to_upper',
  SUBSTRING:     'substring',
  TO_INT:        'to_int',
  ADD:           'add',
  SUB:           'subtract',
  MUL:           'multiply',
  DIV:           'divide',
  SET_RESPONSE_HEADER: 'set_response_header',
}

function displayName(raw: string | undefined): string {
  if (!raw) return '—'
  return INSTRUCTION_DISPLAY_NAME[raw] ?? raw
}

function InstructionEventRow({ ev }: { ev: TracePayloadEvent }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const kvs = (ev.output ?? []).filter(o => o.v)
  return (
    <div style={{ padding: '4px 8px', fontSize: 11, borderLeft: '2px solid var(--border)', marginBottom: 2 }}>
      <span style={{ color: 'var(--text)', fontWeight: 500 }}>{displayName(ev.name)}</span>
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

// Human-readable labels for gateway lifecycle phases appended after flow execution.
// These are NOT flow steps — they are gateway overhead recorded post-execution.
const GATEWAY_PHASE_LABELS: Record<string, string> = {
  GATEWAY_PHASE_CONN_SETUP:            'Connection setup (TCP/TLS)',
  GATEWAY_PHASE_ROUTING:               'Request routing & setup',
  GATEWAY_PHASE_PROCESS_REQUEST:       'Flow execution (total)',
  GATEWAY_PHASE_FINALIZE_RESPONSE:     'Flush response to client',
  GATEWAY_PHASE_RESPONSE_TRANSFER:     'Response transfer (first→last byte)',
  GATEWAY_PHASE_AFTER_RESPONSE_HOOKS:  'Post-response hooks',
  GATEWAY_PHASE_ACCESS_LOG_SNAPSHOT:   'Access log (in-memory)',
  GATEWAY_PHASE_TELEMETRY_FINISH:      'Metrics update',
  GATEWAY_PHASE_ACCESS_LOG_ENQUEUE:    'Access log (persist queue)',
  GATEWAY_PHASE_RESIDUAL:              'Flow executor overhead',
}

// Phases that happen BEFORE flow steps run (shown before the flow steps, not after).
const PRE_FLOW_PHASES = new Set(['GATEWAY_PHASE_CONN_SETUP', 'GATEWAY_PHASE_ROUTING'])

// Instructions that represent sending the response to the client.
const RESPONSE_SENT_NAMES = new Set(['early_return', 'respond', 'return'])

function TraceTimeline({ payload }: { payload: TracePayload }) {
  const instructionEvents = Array.isArray(payload.instructions) ? payload.instructions : []
  const upstreamEvents = Array.isArray(payload.upstreams) ? payload.upstreams : []

  type TimelineEvent = {
    kind: 'instruction' | 'upstream'
    rawName: string
    label: string
    durationNs: number
    seq: number
    status?: number
    isGatewayPhase: boolean
    sourceEvent?: TracePayloadEvent
  }

  const events: TimelineEvent[] = [
    ...instructionEvents.map((e) => {
      const raw = e.name || ''
      const isPhase = raw.startsWith('GATEWAY_PHASE_')
      return {
        kind: 'instruction' as const,
        rawName: raw,
        label: GATEWAY_PHASE_LABELS[raw] ?? displayName(raw),
        durationNs: Number(e.duration_ns ?? 0),
        seq: Number(e.seq ?? 0),
        status: undefined,
        isGatewayPhase: isPhase,
      }
    }),
    ...upstreamEvents.map((e) => ({
      kind: 'upstream' as const,
      rawName: e.name || '',
      label: e.host || e.name || 'upstream',
      durationNs: Number(e.total_ns ?? e.duration_ns ?? 0),
      seq: Number(e.seq ?? 0),
      status: e.status,
      isGatewayPhase: false,
      sourceEvent: e,
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

  // RESIDUAL belongs inside the flow execution window (it's processDuration minus
  // individual instruction times — executor loop, dispatch overhead, etc.).
  // Treat it as a flow-section event, not a post-response phase.
  const FLOW_INTERNAL_PHASES = new Set(['GATEWAY_PHASE_RESIDUAL'])

  // Determine where the response was sent: last instruction whose raw name is a return variant,
  // or last non-gateway-phase instruction if no explicit return found.
  const flowEvents = events.filter(e => !e.isGatewayPhase || FLOW_INTERNAL_PHASES.has(e.rawName))
  const phaseEvents = events.filter(e => e.isGatewayPhase && !FLOW_INTERNAL_PHASES.has(e.rawName))
  const responseSentIdx = (() => {
    for (let i = flowEvents.length - 1; i >= 0; i--) {
      if (RESPONSE_SENT_NAMES.has(flowEvents[i].rawName)) return i
    }
    return flowEvents.length - 1 // fallback: after last flow step
  })()

  // Pre-flow phases (e.g. routing/setup) — shown before flow steps.
  const preFlowPhases = phaseEvents.filter(e => PRE_FLOW_PHASES.has(e.rawName))
  // PROCESS_REQUEST is a summary of the whole flow execution — shown as header stat, not a bar.
  const processTotalEvent = phaseEvents.find(e => e.rawName === 'GATEWAY_PHASE_PROCESS_REQUEST')
  // Post-response phases — shown after the "response sent" marker.
  const postResponsePhases = phaseEvents.filter(
    e => e.rawName !== 'GATEWAY_PHASE_PROCESS_REQUEST' && !PRE_FLOW_PHASES.has(e.rawName)
  )

  function EventBar({ e, i, dimmed }: { e: TimelineEvent; i: number; dimmed?: boolean }) {
    const [phaseExpanded, setPhaseExpanded] = useState(false)
    const [urlExpanded, setUrlExpanded] = useState(false)
    const [reqHdrsExpanded, setReqHdrsExpanded] = useState(false)
    const [resExpanded, setResExpanded] = useState(false)
    const [resBodyExpanded, setResBodyExpanded] = useState(false)
    const widthPct = Math.max(2, (e.durationNs / maxNs) * 100)
    const color = e.kind === 'upstream' ? '#fbbf24' : dimmed ? 'rgba(87,181,255,0.35)' : '#57b5ff'
    const seqLabel = (!e.isGatewayPhase && e.seq > 0) ? `${e.seq}. ` : ''
    const src = e.sourceEvent

    const upstreamMeta = e.kind === 'upstream' && src ? (
      <div style={{ marginTop: 3, fontSize: 11 }}>
        {src.url && (
          <div style={{ color: 'var(--muted)', wordBreak: 'break-all', marginBottom: 2 }}>
            <span style={{ color: '#fbbf24', marginRight: 4 }}>URL</span>
            {src.url.length > 60 && !urlExpanded ? src.url.slice(0, 60) + '…' : src.url}
            {src.url.length > 60 && (
              <button
                onClick={ev => { ev.stopPropagation(); setUrlExpanded(x => !x) }}
                style={{ marginLeft: 4, fontSize: 10, color: 'var(--accent)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
              >
                {urlExpanded ? 'less' : 'more'}
              </button>
            )}
          </div>
        )}
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
          {(src.bytes_sent !== undefined || src.bytes_received !== undefined) && (
            <span style={{ color: 'var(--muted)' }}>
              <span style={{ color: '#4ade80' }}>↑</span>{' '}{fmtBytes(src.bytes_sent)}
              {'  '}
              <span style={{ color: '#60a5fa' }}>↓</span>{' '}{fmtBytes(src.bytes_received)}
            </span>
          )}
          {!!src.attempt && src.attempt > 0 && (
            <span style={{ color: '#f97316' }}>retry #{src.attempt}</span>
          )}
          {src.conn_reused && (
            <span style={{ color: 'var(--muted)' }}>conn reused</span>
          )}
          {src.err && (
            <span style={{ color: '#f87171' }}>⚠ {src.err}</span>
          )}
        </div>
        {(src.dns_duration_ns || src.connect_duration_ns || src.tls_duration_ns || src.ttfb_ns) && (
          <div style={{ marginTop: 3 }}>
            <button
              onClick={ev => { ev.stopPropagation(); setPhaseExpanded(x => !x) }}
              style={{ fontSize: 10, color: 'var(--accent)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
            >
              {phaseExpanded ? '▾ hide timing' : '▸ show timing breakdown'}
            </button>
            {phaseExpanded && (
              <div style={{ marginTop: 4, display: 'grid', gridTemplateColumns: 'auto auto', columnGap: 16, rowGap: 2, fontSize: 11, color: 'var(--muted)' }}>
                {!!src.dns_duration_ns && <><span>DNS</span><span>{fmtNsAsMs(src.dns_duration_ns)}</span></>}
                {!!src.connect_duration_ns && <><span>TCP connect</span><span>{fmtNsAsMs(src.connect_duration_ns)}</span></>}
                {!!src.tls_duration_ns && <><span>TLS handshake</span><span>{fmtNsAsMs(src.tls_duration_ns)}</span></>}
                {!!src.ttfb_ns && <><span>TTFB</span><span>{fmtNsAsMs(src.ttfb_ns)}</span></>}
              </div>
            )}
          </div>
        )}
        {src.req_headers && Object.keys(src.req_headers).length > 0 && (
          <div style={{ marginTop: 3 }}>
            <button
              onClick={ev => { ev.stopPropagation(); setReqHdrsExpanded(x => !x) }}
              style={{ fontSize: 10, color: 'var(--accent)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
            >
              {reqHdrsExpanded ? '▾ hide request headers' : `▸ request headers (${Object.keys(src.req_headers).length})`}
            </button>
            {reqHdrsExpanded && (
              <table style={{ marginTop: 4, borderCollapse: 'collapse', width: '100%', fontSize: 11 }}>
                <tbody>
                  {Object.entries(src.req_headers).map(([k, v]) => (
                    <tr key={k}>
                      <td style={{ padding: '1px 8px 1px 0', color: '#a78bfa', fontFamily: 'monospace', whiteSpace: 'nowrap', width: 1 }}>{k}</td>
                      <td style={{ padding: '1px 0', color: v === '***' ? '#f87171' : 'var(--text)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{v}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
        {(src.res_headers || src.res_body) && (
          <div style={{ marginTop: 3 }}>
            <button
              onClick={ev => { ev.stopPropagation(); setResExpanded(x => !x) }}
              style={{ fontSize: 10, color: 'var(--accent)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
            >
              {resExpanded ? '▾ hide response' : '▸ show response'}
            </button>
            {resExpanded && (
              <div style={{ marginTop: 4 }}>
                {src.res_headers && Object.keys(src.res_headers).length > 0 && (
                  <div style={{ marginBottom: 4 }}>
                    <div style={{ fontSize: 10, color: 'var(--muted)', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 2 }}>Response Headers</div>
                    <table style={{ borderCollapse: 'collapse', width: '100%', fontSize: 11 }}>
                      <tbody>
                        {Object.entries(src.res_headers).map(([k, v]) => (
                          <tr key={k}>
                            <td style={{ padding: '1px 8px 1px 0', color: '#34d399', fontFamily: 'monospace', whiteSpace: 'nowrap', width: 1 }}>{k}</td>
                            <td style={{ padding: '1px 0', color: 'var(--text)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{v}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                {src.res_body && (
                  <div>
                    <div style={{ fontSize: 10, color: 'var(--muted)', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 2 }}>
                      Response Body {src.res_body.length >= 1024 ? '(first 1 KB)' : ''}
                    </div>
                    <pre style={{
                      margin: 0, padding: '6px 8px',
                      background: 'rgba(0,0,0,0.3)',
                      borderRadius: 4,
                      fontSize: 11,
                      fontFamily: 'monospace',
                      color: 'var(--text)',
                      whiteSpace: 'pre-wrap',
                      wordBreak: 'break-all',
                      maxHeight: resBodyExpanded ? 'none' : 80,
                      overflow: resBodyExpanded ? 'visible' : 'hidden',
                    }}>
                      {src.res_body}
                    </pre>
                    {src.res_body.length > 200 && (
                      <button
                        onClick={ev => { ev.stopPropagation(); setResBodyExpanded(x => !x) }}
                        style={{ marginTop: 2, fontSize: 10, color: 'var(--accent)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
                      >
                        {resBodyExpanded ? 'collapse' : 'expand'}
                      </button>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    ) : null

    return (
      <div key={`${e.kind}-${e.seq}-${i}`} style={{ marginBottom: 8, opacity: dimmed ? 0.7 : 1 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4, gap: 8 }}>
          <div style={{ color: dimmed ? 'var(--muted)' : 'var(--text)', fontSize: 12, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {seqLabel}{e.label}
            {e.kind === 'upstream' && e.status ? ` (${e.status})` : ''}
          </div>
          <div style={{ color: 'var(--muted)', fontSize: 11, whiteSpace: 'nowrap' }}>
            {fmtNsAsMs(e.durationNs)}
          </div>
        </div>
        <div style={{ height: 6, background: 'rgba(255,255,255,0.06)', borderRadius: 999, overflow: 'hidden' }}>
          <div style={{ width: `${widthPct}%`, height: '100%', background: color }} />
        </div>
        {upstreamMeta}
      </div>
    )
  }

  return (
    <div style={{ background: 'var(--bg)', borderRadius: 6, border: '1px solid var(--border)', padding: '10px 12px' }}>
      <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
        Timeline • total {fmtNsAsMs(totalNs)}
        {processTotalEvent && (
          <span style={{ marginLeft: 12, color: 'var(--muted)' }}>
            flow {fmtNsAsMs(processTotalEvent.durationNs)}
          </span>
        )}
      </div>

      {/* Pre-flow gateway phases (routing + setup before first flow step) */}
      {preFlowPhases.length > 0 && (
        <div style={{ marginBottom: 6 }}>
          {preFlowPhases.map((e, i) => (
            <EventBar key={`pre-${i}`} e={e} i={i} dimmed />
          ))}
          <div style={{ height: 1, background: 'var(--border)', margin: '6px 0' }} />
        </div>
      )}

      {/* Flow steps */}
      {flowEvents.map((e, i) => (
        <EventBar key={`flow-${i}`} e={e} i={i} />
      ))}

      {/* Response-sent marker — inserted after the return/respond step */}
      {flowEvents.length > 0 && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8, margin: '6px 0 10px',
          fontSize: 11, color: '#4ade80',
        }}>
          <div style={{ flex: 1, height: 1, background: 'rgba(74,222,128,0.3)' }} />
          <span>↩ response sent to client</span>
          <div style={{ flex: 1, height: 1, background: 'rgba(74,222,128,0.3)' }} />
        </div>
      )}

      {/* Post-response gateway phases */}
      {postResponsePhases.length > 0 && (
        <div>
          <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 6, fontStyle: 'italic' }}>
            Gateway bookkeeping (after response)
          </div>
          {postResponsePhases.map((e, i) => (
            <EventBar key={`phase-${i}`} e={e} i={i} dimmed />
          ))}
        </div>
      )}
    </div>
  )
}

// ── Flow View ────────────────────────────────────────────────────
// Groups instruction events by their source flow step index (step_idx),
// giving users a flow-aligned trace view that matches the steps they built.

function FlowView({ payload }: { payload: TracePayload }) {
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  const instructions = Array.isArray(payload.instructions) ? payload.instructions : []
  const upstreams = Array.isArray(payload.upstreams) ? payload.upstreams : []

  // Require at least one non-system step_idx to show this view.
  const hasStepIdx = instructions.some(e => typeof e.step_idx === 'number' && e.step_idx >= 0)
  if (!hasStepIdx) return <TraceTimeline payload={payload} />

  // Partition: step_idx >= 0 are user steps; step_idx < 0 (or missing) are system.
  const stepGroupMap = new Map<number, TracePayloadEvent[]>()
  const systemEvents: TracePayloadEvent[] = []
  for (const ev of instructions) {
    const idx = typeof ev.step_idx === 'number' ? ev.step_idx : -1
    if (idx < 0) {
      systemEvents.push(ev)
    } else {
      if (!stepGroupMap.has(idx)) stepGroupMap.set(idx, [])
      stepGroupMap.get(idx)!.push(ev)
    }
  }

  const sortedIdxs = [...stepGroupMap.keys()].sort((a, b) => a - b)
  const totalNs = Number(payload.summary?.duration_ns ?? 0)
  const maxNs = Math.max(totalNs, 1)

  function toggle(idx: number) {
    setExpanded(prev => {
      const next = new Set(prev)
      next.has(idx) ? next.delete(idx) : next.add(idx)
      return next
    })
  }

  // Pick the primary label for a step group: first non-control name.
  function stepLabel(events: TracePayloadEvent[]): string {
    const SKIP = new Set(['GOTO', 'RET', 'STOP', 'CALL', 'LOOP_GATE', 'LOOP_GATE_SLOT', 'LOOP_REPEAT', 'WHILE_GATE', 'WHILE_REPEAT'])
    for (const ev of events) {
      const n = ev.name ?? ''
      if (!n.startsWith('GATEWAY_PHASE_') && !SKIP.has(n)) return displayName(n)
    }
    return displayName(events[0]?.name)
  }

  function stepTotalNs(events: TracePayloadEvent[]): number {
    return events.reduce((sum, e) => sum + Number(e.duration_ns ?? 0), 0)
  }

  // Detect if branch was taken for an if step (looks for COMPLEX_LOGIC_GATE variants).
  function branchInfo(events: TracePayloadEvent[]): string | null {
    const names = events.map(e => e.name ?? '')
    const hasGate = names.some(n => n.includes('LOGIC_GATE') || n.includes('complex_logic'))
    if (!hasGate) return null
    // If there are instructions after the gate, heuristically determine which branch ran.
    const gateIdx = names.findIndex(n => n.includes('LOGIC_GATE') || n.includes('complex_logic'))
    const afterGate = names.slice(gateIdx + 1)
    if (afterGate.length === 0) return 'no branch'
    // We can't easily tell then vs else without more context; just note that a branch ran.
    return `${afterGate.length} instruction${afterGate.length !== 1 ? 's' : ''} in branch`
  }

  return (
    <div style={{ background: 'var(--bg)', borderRadius: 6, border: '1px solid var(--border)', padding: '10px 12px' }}>
      <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
        Flow Steps • total {fmtNsAsMs(totalNs)}
      </div>

      {sortedIdxs.map(idx => {
        const events = stepGroupMap.get(idx)!
        const ns = stepTotalNs(events)
        const isExp = expanded.has(idx)
        const widthPct = Math.max(2, (ns / maxNs) * 100)
        const label = stepLabel(events)
        const branch = branchInfo(events)

        return (
          <div key={idx} style={{ marginBottom: 6 }}>
            <div
              onClick={() => toggle(idx)}
              style={{ cursor: 'pointer', padding: '4px 0', display: 'flex', alignItems: 'center', gap: 8 }}
            >
              <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 12, userSelect: 'none' }}>
                {isExp ? '▾' : '▸'}
              </span>
              <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--text)', flex: 1 }}>
                {idx + 1}. {label}
                {branch && <span style={{ fontSize: 10, color: 'var(--muted)', fontWeight: 400, marginLeft: 8 }}>({branch})</span>}
              </span>
              <span style={{ fontSize: 11, color: events.length > 1 ? 'var(--muted)' : 'var(--muted)', whiteSpace: 'nowrap' }}>
                {events.length > 1 && <span style={{ marginRight: 6, color: 'rgba(255,255,255,0.3)' }}>{events.length} instr</span>}
                {fmtNsAsMs(ns)}
              </span>
            </div>
            <div style={{ height: 5, background: 'rgba(255,255,255,0.06)', borderRadius: 999, overflow: 'hidden', marginLeft: 20, marginBottom: 2 }}>
              <div style={{ width: `${widthPct}%`, height: '100%', background: '#57b5ff' }} />
            </div>
            {isExp && (
              <div style={{ marginLeft: 20, marginTop: 2 }}>
                {events.map((ev, j) => <InstructionEventRow key={ev.seq ?? j} ev={ev} />)}
              </div>
            )}
          </div>
        )
      })}

      {/* System instructions (auto-bind, infra) — collapsed by default */}
      {systemEvents.length > 0 && (
        <div style={{ marginTop: 8, paddingTop: 8, borderTop: '1px solid rgba(255,255,255,0.05)' }}>
          <div style={{ fontSize: 10, color: 'rgba(255,255,255,0.2)', marginBottom: 4 }}>
            Infrastructure ({systemEvents.length} instructions — auto-bind, routing overhead)
          </div>
        </div>
      )}

      {/* Upstream calls */}
      {upstreams.length > 0 && (
        <div style={{ marginTop: 8, paddingTop: 8, borderTop: '1px solid var(--border)' }}>
          <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 6 }}>Upstream Calls</div>
          {upstreams.map((e, i) => {
            const ns = Number(e.total_ns ?? e.duration_ns ?? 0)
            const widthPct = Math.max(2, (ns / maxNs) * 100)
            return (
              <div key={i} style={{ marginBottom: 6 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 3, gap: 8 }}>
                  <div style={{ color: 'var(--text)', fontSize: 12 }}>
                    {e.host || 'upstream'}{e.status ? ` (${e.status})` : ''}
                    {e.err && <span style={{ color: '#f87171', marginLeft: 6 }}>⚠ {e.err}</span>}
                  </div>
                  <div style={{ color: 'var(--muted)', fontSize: 11 }}>{fmtNsAsMs(ns)}</div>
                </div>
                <div style={{ height: 5, background: 'rgba(255,255,255,0.06)', borderRadius: 999, overflow: 'hidden' }}>
                  <div style={{ width: `${widthPct}%`, height: '100%', background: '#fbbf24' }} />
                </div>
                {e.url && (
                  <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {e.url}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ── Trace View Tabs ───────────────────────────────────────────────
// Tab selector for the three trace views: Flow (step-aligned), Blocks, Timeline.

function TraceViewTabs({ payload }: { payload: TracePayload }) {
  const instructions = Array.isArray(payload.instructions) ? payload.instructions : []
  const hasStepIdx = instructions.some(e => typeof e.step_idx === 'number' && e.step_idx >= 0)
  const hasBlocks = buildBlockGroups(instructions).length > 0

  // Default to Flow view if step_idx data is available, else Blocks, else Timeline.
  const defaultView = hasStepIdx ? 'flow' : hasBlocks ? 'blocks' : 'timeline'
  const [view, setView] = useState<'flow' | 'blocks' | 'timeline'>(defaultView)

  const tabStyle = (active: boolean) => ({
    padding: '4px 10px',
    fontSize: 11,
    fontWeight: active ? 700 : 400,
    color: active ? 'var(--text)' : 'var(--muted)',
    background: active ? 'rgba(87,181,255,0.12)' : 'transparent',
    border: `1px solid ${active ? 'rgba(87,181,255,0.3)' : 'var(--border)'}`,
    borderRadius: 4,
    cursor: 'pointer',
  })

  return (
    <div>
      <div style={{ display: 'flex', gap: 4, marginBottom: 8 }}>
        {hasStepIdx && (
          <button onClick={() => setView('flow')} style={tabStyle(view === 'flow')}>
            Flow
          </button>
        )}
        <button onClick={() => setView('blocks')} style={tabStyle(view === 'blocks')}>
          Blocks
        </button>
        <button onClick={() => setView('timeline')} style={tabStyle(view === 'timeline')}>
          Timeline
        </button>
      </div>
      {view === 'flow' && <FlowView payload={payload} />}
      {view === 'blocks' && <BlockView payload={payload} />}
      {view === 'timeline' && <TraceTimeline payload={payload} />}
    </div>
  )
}
