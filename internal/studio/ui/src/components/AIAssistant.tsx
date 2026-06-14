import { useState, useEffect, useRef } from 'react'
import { fetchGatewaySnapshot, listLLMModels } from '../api'
import type { LLMModel } from '../types'

// ── Types ─────────────────────────────────────────────────────────────────────

type ChatAction = {
  op: string
  name?: string
  dsl?: string
  description?: string
  api?: string
  path?: string
  method?: string
  flow?: string
  target?: string
}

type APICallLog = {
  label: string
  url: string
  status_code?: number
  duration_ms: number
  error?: string
}

type ChatDebugInfo = {
  model_alias: string
  system_prompt: string
  messages_sent: { role: string; content: string }[]
  raw_llm_response: string
  api_calls: APICallLog[]
  dropped_actions?: string[]   // actions rejected by server-side validation
  duration_ms: number
}

type PlanFlow = {
  name: string
  description: string
  subflows?: PlanFlow[]
}

type PlanEndpoint = {
  method: string
  path: string
  description: string
}

type PlanAPI = {
  name: string
  endpoints: PlanEndpoint[]
}

type Plan = {
  summary: string
  flows: PlanFlow[]
  apis: PlanAPI[]
  policies?: string[]
}

type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
  response?: {
    phase?: 'discovery' | 'plan' | 'execute' | string
    confirm_message: string
    plan?: Plan
    actions: ChatAction[]
    questions: string[]
    dsl_preview?: string
    debug?: ChatDebugInfo
  }
  appliedActions: Set<number>
  failedActions: Record<number, string>
}

type CapabilitiesInfo = {
  apis: { name: string; endpointCount: number }[]
  flows: string[]
}

type AuditRecord = {
  id: string
  created_at: string
  summary: string
  artifacts: string[]
  gateway_name: string
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function opColor(op: string): string {
  const o = op.toLowerCase()
  if (o.includes('upsert') || o.includes('create')) return '#0ea5e9'
  if (o.includes('delete') || o.includes('remove')) return '#ef4444'
  if (o.includes('update')) return '#f59e0b'
  return 'var(--accent)'
}

function fmtMs(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms`
}

// ── Debug Panel ───────────────────────────────────────────────────────────────

function DebugPanel({ debug }: { debug: ChatDebugInfo }) {
  const [showPrompt, setShowPrompt] = useState(false)
  const [showMessages, setShowMessages] = useState(false)
  const [showRaw, setShowRaw] = useState(false)

  const sectionStyle: React.CSSProperties = {
    marginTop: 6,
    border: '1px solid var(--border)',
    borderRadius: 6,
    overflow: 'hidden',
  }
  const headerStyle: React.CSSProperties = {
    padding: '4px 10px',
    fontSize: 11,
    fontWeight: 700,
    cursor: 'pointer',
    background: 'var(--panel)',
    color: 'var(--muted)',
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    userSelect: 'none',
  }
  const bodyStyle: React.CSSProperties = {
    padding: '8px 10px',
    fontFamily: 'monospace',
    fontSize: 11,
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-all',
    overflowX: 'auto',
    maxHeight: 280,
    overflowY: 'auto',
    color: 'var(--text)',
    background: 'var(--block-bg)',
  }

  return (
    <div style={{
      marginTop: 10,
      border: '1px solid #7c3aed44',
      borderRadius: 8,
      padding: '10px 12px',
      background: 'rgba(124,58,237,0.04)',
      fontSize: 12,
    }}>
      {/* Header row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 10, fontWeight: 800, padding: '2px 8px', borderRadius: 4, background: '#7c3aed', color: '#fff', letterSpacing: 0.5 }}>
          DEBUG
        </span>
        <span style={{ color: 'var(--muted)', fontWeight: 600 }}>
          model: <span style={{ color: 'var(--text)' }}>{debug.model_alias}</span>
        </span>
        <span style={{ color: 'var(--muted)', fontWeight: 600 }}>
          total: <span style={{ color: 'var(--text)' }}>{fmtMs(debug.duration_ms)}</span>
        </span>
      </div>

      {/* Dropped actions — show prominently as a security signal */}
      {debug.dropped_actions && debug.dropped_actions.length > 0 && (
        <div style={{
          marginBottom: 8,
          padding: '6px 10px',
          background: 'rgba(239,68,68,0.08)',
          border: '1px solid #ef444444',
          borderRadius: 6,
        }}>
          <div style={{ fontSize: 10, fontWeight: 800, color: '#ef4444', marginBottom: 4, letterSpacing: 0.5 }}>
            BLOCKED BY SERVER ({debug.dropped_actions.length})
          </div>
          {debug.dropped_actions.map((d, i) => (
            <div key={i} style={{ fontSize: 11, color: '#ef4444', fontFamily: 'monospace' }}>{d}</div>
          ))}
        </div>
      )}

      {/* API calls table */}
      {debug.api_calls && debug.api_calls.length > 0 && (
        <div style={{ marginBottom: 8 }}>
          <div style={{ fontSize: 10, fontWeight: 700, color: 'var(--muted)', marginBottom: 4, letterSpacing: 0.5 }}>
            API CALLS
          </div>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 11 }}>
            <thead>
              <tr style={{ borderBottom: '1px solid var(--border)' }}>
                {(['label', 'url', 'status', 'duration', 'error'] as const).map(col => (
                  <th key={col} style={{ textAlign: 'left', padding: '2px 6px', color: 'var(--muted)', fontWeight: 600, whiteSpace: 'nowrap' }}>
                    {col}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {debug.api_calls.map((c, i) => (
                <tr key={i} style={{ borderBottom: '1px solid var(--border)' }}>
                  <td style={{ padding: '3px 6px', fontFamily: 'monospace', color: 'var(--text)', whiteSpace: 'nowrap' }}>{c.label}</td>
                  <td style={{ padding: '3px 6px', fontFamily: 'monospace', color: 'var(--muted)', maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={c.url}>{c.url}</td>
                  <td style={{ padding: '3px 6px', fontFamily: 'monospace', color: c.status_code && c.status_code >= 400 ? '#ef4444' : c.status_code ? '#22c55e' : 'var(--muted)', whiteSpace: 'nowrap' }}>
                    {c.status_code ?? '—'}
                  </td>
                  <td style={{ padding: '3px 6px', fontFamily: 'monospace', color: 'var(--text)', whiteSpace: 'nowrap' }}>{fmtMs(c.duration_ms)}</td>
                  <td style={{ padding: '3px 6px', color: '#ef4444', fontFamily: 'monospace', maxWidth: 180, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={c.error}>{c.error ?? ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* System prompt collapsible */}
      <div style={sectionStyle}>
        <div style={headerStyle} onClick={() => setShowPrompt(o => !o)}>
          <span>{showPrompt ? '▾' : '▸'}</span> System Prompt
          <span style={{ marginLeft: 'auto', fontWeight: 400 }}>{debug.system_prompt.length} chars</span>
        </div>
        {showPrompt && (
          <div style={bodyStyle}>{debug.system_prompt}</div>
        )}
      </div>

      {/* Messages sent collapsible */}
      <div style={sectionStyle}>
        <div style={headerStyle} onClick={() => setShowMessages(o => !o)}>
          <span>{showMessages ? '▾' : '▸'}</span> Messages Sent
          <span style={{ marginLeft: 'auto', fontWeight: 400 }}>{debug.messages_sent?.length ?? 0} messages</span>
        </div>
        {showMessages && (
          <div style={bodyStyle}>
            {(debug.messages_sent ?? []).map((m, i) => (
              <div key={i} style={{ marginBottom: 8 }}>
                <span style={{ fontWeight: 700, color: m.role === 'user' ? '#0ea5e9' : '#a78bfa' }}>[{m.role}] </span>
                {m.content}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Raw LLM response collapsible */}
      {debug.raw_llm_response && (
        <div style={sectionStyle}>
          <div style={headerStyle} onClick={() => setShowRaw(o => !o)}>
            <span>{showRaw ? '▾' : '▸'}</span> Raw LLM Response
            <span style={{ marginLeft: 'auto', fontWeight: 400 }}>{debug.raw_llm_response.length} chars</span>
          </div>
          {showRaw && (
            <div style={bodyStyle}>{debug.raw_llm_response}</div>
          )}
        </div>
      )}
    </div>
  )
}

// ── Plan Preview ─────────────────────────────────────────────────────────────

function FlowNode({ flow, depth = 0 }: { flow: PlanFlow; depth?: number }) {
  const [open, setOpen] = useState(true)
  const hasChildren = (flow.subflows?.length ?? 0) > 0
  return (
    <div style={{ marginLeft: depth * 16 }}>
      <div
        style={{ display: 'flex', alignItems: 'flex-start', gap: 8, padding: '5px 0', cursor: hasChildren ? 'pointer' : 'default' }}
        onClick={() => hasChildren && setOpen(o => !o)}
      >
        <span style={{ color: 'var(--accent)', fontSize: 13, flexShrink: 0, marginTop: 1 }}>
          {hasChildren ? (open ? '▾' : '▸') : '◆'}
        </span>
        <div>
          <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--text)', fontFamily: 'monospace' }}>{flow.name}</span>
          {flow.description && (
            <span style={{ fontSize: 12, color: 'var(--muted)', marginLeft: 8 }}>— {flow.description}</span>
          )}
        </div>
      </div>
      {open && hasChildren && flow.subflows?.map((sf, i) => (
        <FlowNode key={i} flow={sf} depth={depth + 1} />
      ))}
    </div>
  )
}

function PlanPreview({ plan }: { plan: Plan }) {
  return (
    <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 12 }}>
      {/* Summary */}
      {plan.summary && (
        <div style={{ fontSize: 13, color: 'var(--text)', lineHeight: 1.6, padding: '10px 14px', background: 'rgba(14,165,233,0.06)', border: '1px solid rgba(14,165,233,0.2)', borderRadius: 8 }}>
          {plan.summary}
        </div>
      )}

      {/* Flows tree */}
      {plan.flows?.length > 0 && (
        <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
          <div style={{ padding: '8px 14px', background: 'var(--panel)', borderBottom: '1px solid var(--border)', fontSize: 11, fontWeight: 700, color: 'var(--muted)', letterSpacing: 0.5 }}>
            FLOWS & SUB-FLOWS ({plan.flows.length})
          </div>
          <div style={{ padding: '8px 14px' }}>
            {plan.flows.map((f, i) => <FlowNode key={i} flow={f} />)}
          </div>
        </div>
      )}

      {/* APIs */}
      {plan.apis?.length > 0 && (
        <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
          <div style={{ padding: '8px 14px', background: 'var(--panel)', borderBottom: '1px solid var(--border)', fontSize: 11, fontWeight: 700, color: 'var(--muted)', letterSpacing: 0.5 }}>
            APIs & ENDPOINTS
          </div>
          <div style={{ padding: '8px 14px', display: 'flex', flexDirection: 'column', gap: 10 }}>
            {plan.apis.map((api, ai) => (
              <div key={ai}>
                <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--text)', marginBottom: 4, fontFamily: 'monospace' }}>
                  ◆ {api.name}
                </div>
                {api.endpoints?.map((ep, ei) => (
                  <div key={ei} style={{ display: 'flex', gap: 8, alignItems: 'flex-start', marginLeft: 16, padding: '3px 0' }}>
                    <span style={{
                      fontSize: 10, fontWeight: 800, padding: '1px 6px', borderRadius: 4,
                      background: ep.method === 'GET' ? '#0ea5e930' : ep.method === 'POST' ? '#22c55e30' : ep.method === 'DELETE' ? '#ef444430' : '#f59e0b30',
                      color: ep.method === 'GET' ? '#0ea5e9' : ep.method === 'POST' ? '#22c55e' : ep.method === 'DELETE' ? '#ef4444' : '#f59e0b',
                      flexShrink: 0, marginTop: 2, letterSpacing: 0.5
                    }}>{ep.method}</span>
                    <span style={{ fontSize: 12, fontFamily: 'monospace', color: 'var(--text)', marginRight: 6, flexShrink: 0 }}>{ep.path}</span>
                    {ep.description && <span style={{ fontSize: 12, color: 'var(--muted)' }}>{ep.description}</span>}
                  </div>
                ))}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Policies */}
      {plan.policies && plan.policies.length > 0 && (
        <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
          <div style={{ padding: '8px 14px', background: 'var(--panel)', borderBottom: '1px solid var(--border)', fontSize: 11, fontWeight: 700, color: 'var(--muted)', letterSpacing: 0.5 }}>
            SECURITY & POLICIES
          </div>
          <ul style={{ margin: 0, padding: '8px 14px 8px 30px', display: 'flex', flexDirection: 'column', gap: 4 }}>
            {plan.policies.map((p, i) => (
              <li key={i} style={{ fontSize: 13, color: 'var(--text)' }}>{p}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

// ── Action Block ──────────────────────────────────────────────────────────────

function ActionBlock({
  action,
  index,
  msgIndex,
  applied,
  failed,
  onApply,
  onDiscard,
}: {
  action: ChatAction
  index: number
  msgIndex: number
  applied: boolean
  failed: string | undefined
  onApply: (msgIndex: number, actionIndex: number, action: ChatAction) => void
  onDiscard: (msgIndex: number, actionIndex: number) => void
}) {
  const [showDSL, setShowDSL] = useState(false)
  const kvFields: [string, string][] = (
    [
      ['api', action.api],
      ['path', action.path],
      ['method', action.method],
      ['flow', action.flow],
      ['target', action.target],
    ] as [string, string | undefined][]
  ).filter((e): e is [string, string] => !!e[1])

  const label = action.name || action.api || action.path || ''

  return (
    <div style={{
      border: '1px solid var(--border)',
      borderRadius: 8,
      marginTop: 8,
      overflow: 'hidden',
      background: 'var(--step-bg)',
    }}>
      {/* header row */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        padding: '8px 12px',
        borderBottom: action.description || kvFields.length > 0 || (showDSL && action.dsl) ? '1px solid var(--border)' : 'none',
      }}>
        <span style={{
          fontSize: 10,
          fontWeight: 800,
          padding: '2px 8px',
          borderRadius: 4,
          background: opColor(action.op),
          color: '#fff',
          flexShrink: 0,
          letterSpacing: 0.5,
        }}>
          {action.op.toUpperCase()}
        </span>
        {label && (
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--text)' }}>{label}</span>
        )}
      </div>

      {/* Description (plain English — shown by default) */}
      {action.description && (
        <div style={{ padding: '8px 12px', fontSize: 13, color: 'var(--text)', lineHeight: 1.5 }}>
          {action.description}
        </div>
      )}

      {/* Key fields (for add_endpoint etc. when no description) */}
      {!action.description && kvFields.length > 0 && (
        <table style={{ margin: '8px 12px', borderSpacing: '8px 2px', fontSize: 12 }}>
          <tbody>
            {kvFields.map(([k, v]) => (
              <tr key={k}>
                <td style={{ color: 'var(--muted)', fontWeight: 600, paddingRight: 8 }}>{k}</td>
                <td style={{ color: 'var(--text)', fontFamily: 'monospace' }}>{v}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {/* DSL toggle (hidden by default) */}
      {action.dsl && (
        <div style={{ padding: '0 12px 4px' }}>
          <button
            onClick={() => setShowDSL(v => !v)}
            style={{ fontSize: 11, color: 'var(--muted)', background: 'none', border: 'none', cursor: 'pointer', padding: '2px 0' }}
          >
            {showDSL ? '▾ Hide DSL' : '▸ Show DSL'}
          </button>
          {showDSL && (
            <pre style={{
              margin: '4px 0 0',
              padding: '8px 10px',
              fontFamily: 'monospace',
              fontSize: 11,
              overflowX: 'auto',
              maxHeight: 200,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              color: 'var(--text)',
              background: 'var(--block-bg)',
              borderRadius: 6,
              border: '1px solid var(--border)',
            }}>
              {action.dsl}
            </pre>
          )}
        </div>
      )}

      {/* buttons / status */}
      <div style={{ padding: '8px 12px', display: 'flex', alignItems: 'center', gap: 8 }}>
        {!applied && failed === undefined && (
          <>
            <button
              onClick={() => onApply(msgIndex, index, action)}
              style={{
                padding: '4px 14px',
                borderRadius: 6,
                border: 'none',
                cursor: 'pointer',
                fontSize: 12,
                fontWeight: 700,
                background: '#22c55e',
                color: '#fff',
              }}
            >
              Apply
            </button>
            <button
              onClick={() => onDiscard(msgIndex, index)}
              style={{
                padding: '4px 14px',
                borderRadius: 6,
                border: 'none',
                cursor: 'pointer',
                fontSize: 12,
                fontWeight: 600,
                background: 'var(--step-bg)',
                color: 'var(--muted)',
              } as React.CSSProperties}
            >
              Discard
            </button>
          </>
        )}
        {applied && (
          <span style={{ fontSize: 12, color: '#22c55e', fontWeight: 600 }}>✓ Applied</span>
        )}
        {!applied && failed !== undefined && (
          <span style={{ fontSize: 12, color: '#ef4444', fontWeight: 600 }}>✗ {failed}</span>
        )}
      </div>
    </div>
  )
}

// ── Main Component ────────────────────────────────────────────────────────────

export default function AIAssistant() {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [thinking, setThinking] = useState(false)
  const [model, setModel] = useState('')
  const [registeredModels, setRegisteredModels] = useState<LLMModel[]>([])
  const [modelsLoading, setModelsLoading] = useState(false)
  const [includeAPIs, setIncludeAPIs] = useState(true)
  const [includeFlows, setIncludeFlows] = useState(true)
  const [showDebug, setShowDebug] = useState(true)   // verbose by default; will be opt-in later
  const [capabilities, setCapabilities] = useState<CapabilitiesInfo>({ apis: [], flows: [] })
  const [capLoading, setCapLoading] = useState(false)
  const chatEndRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const [discardedActions, setDiscardedActions] = useState<Set<string>>(new Set())
  const [contextOpen, setContextOpen] = useState(false)
  const [contextText, setContextText] = useState('')
  const [contextSaving, setContextSaving] = useState(false)
  const [contextSaved, setContextSaved] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [auditRecords, setAuditRecords] = useState<AuditRecord[]>([])
  const [historyLoading, setHistoryLoading] = useState(false)

  // ── Load registered models ───────────────────────────────────────────────
  async function loadRegisteredModels() {
    setModelsLoading(true)
    try {
      const models = await listLLMModels()
      setRegisteredModels(models)
      if (models.length > 0) {
        setModel(prev => prev || models[0].alias)
      }
    } catch {
      setRegisteredModels([])
    } finally {
      setModelsLoading(false)
    }
  }

  // ── Load capabilities ────────────────────────────────────────────────────
  async function loadCapabilities() {
    setCapLoading(true)
    try {
      const snapshot = await fetchGatewaySnapshot()
      const apiMap = new Map<string, number>()
      for (const a of snapshot.apis) {
        const count = apiMap.get(a.name) ?? 0
        apiMap.set(a.name, count + 1 + (a.endpoint_configs?.length ?? 0))
      }
      const apis = Array.from(apiMap.entries()).map(([name, endpointCount]) => ({ name, endpointCount }))
      const flows = snapshot.flows.map(f => f.name)
      setCapabilities({ apis, flows })
    } catch {
      setCapabilities({ apis: [], flows: [] })
    } finally {
      setCapLoading(false)
    }
  }

  // ── Load audit history ───────────────────────────────────────────────────
  async function loadHistory() {
    setHistoryLoading(true)
    try {
      const res = await fetch('/api/ai/chat-history')
      if (res.ok) {
        const data: AuditRecord[] = await res.json()
        setAuditRecords(data ?? [])
      }
    } catch {
      // ignore
    } finally {
      setHistoryLoading(false)
    }
  }

  useEffect(() => {
    loadRegisteredModels()
    loadCapabilities()
    fetch('/api/ai/project-context')
      .then(r => r.json())
      .then(data => { if (data.content) setContextText(data.content) })
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (historyOpen) loadHistory()
  }, [historyOpen])

  // ── Auto-scroll ───────────────────────────────────────────────────────────
  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // ── Auto-resize textarea ──────────────────────────────────────────────────
  function handleInputChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    setInput(e.target.value)
    const el = textareaRef.current
    if (el) {
      el.style.height = 'auto'
      el.style.height = Math.min(el.scrollHeight, 120) + 'px'
    }
  }

  // ── Send sync payload to gateway ─────────────────────────────────────────
  async function postSync(flows: unknown[], apis: unknown[]) {
    const res = await fetch('/api/sync', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sync_uuid: `ai-${Date.now()}`, flows, apis }),
    })
    if (!res.ok) {
      const text = await res.text().catch(() => `HTTP ${res.status}`)
      throw new Error(text || `HTTP ${res.status}`)
    }
  }

  // ── Apply single action ────────────────────────────────────────────────────
  async function applyAction(msgIndex: number, actionIndex: number, action: ChatAction) {
    try {
      if (action.op === 'upsert_flow') {
        let instructions: unknown[]
        try { instructions = JSON.parse(action.dsl ?? '[]') } catch { throw new Error('Invalid flow DSL') }
        await postSync([{ name: action.name ?? '', instructions, action: 'upsert' }], [])
      } else if (action.op === 'add_endpoint') {
        await postSync([], [{
          name: action.api ?? '',
          path: action.path ?? '/',
          method: action.method ?? 'ANY',
          flow_name: action.flow ?? '',
          action: 'upsert',
        }])
      } else if (action.op === 'upsert_api') {
        // API container is created implicitly when its endpoints are added via sync.
        // Mark applied immediately — nothing to send standalone.
      } else if (action.op === 'upsert_tenant') {
        const res = await fetch('/api/tenants', {
          method: 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ alias: action.name ?? '' }),
        })
        if (!res.ok) throw new Error(await res.text().catch(() => `HTTP ${res.status}`))
      } else if (action.op === 'upsert_rate_limit') {
        const res = await fetch('/api/rate-limit-configs', {
          method: 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: action.name ?? '' }),
        })
        if (!res.ok) throw new Error(await res.text().catch(() => `HTTP ${res.status}`))
      } else if (action.op === 'publish') {
        const res = await fetch('/api/deploy', {
          method: 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ target: action.target ?? '' }),
        })
        if (!res.ok) throw new Error(await res.text().catch(() => `HTTP ${res.status}`))
      } else {
        throw new Error(`Unknown op: ${action.op}`)
      }
      setMessages(prev =>
        prev.map((m, i) => {
          if (i !== msgIndex) return m
          const next = new Set(m.appliedActions)
          next.add(actionIndex)
          return { ...m, appliedActions: next }
        })
      )
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setMessages(prev =>
        prev.map((m, i) => {
          if (i !== msgIndex) return m
          return { ...m, failedActions: { ...m.failedActions, [actionIndex]: msg } }
        })
      )
    }
  }

  // ── Apply all pending actions as one batch sync ───────────────────────────
  async function applyAllActions(msgIndex: number) {
    const msg = messages[msgIndex]
    if (!msg?.response) return
    const pending = msg.response.actions
      .map((a, i) => ({ a, i }))
      .filter(({ i }) => !msg.appliedActions.has(i) && !discardedActions.has(`${msgIndex}:${i}`))
    if (pending.length === 0) return

    const flows: unknown[] = []
    const apis: unknown[] = []
    const noOpIndices: number[] = []

    for (const { a, i } of pending) {
      if (a.op === 'upsert_flow') {
        let instructions: unknown[]
        try { instructions = JSON.parse(a.dsl ?? '[]') } catch { instructions = [] }
        flows.push({ name: a.name ?? '', instructions, action: 'upsert' })
      } else if (a.op === 'add_endpoint') {
        apis.push({ name: a.api ?? '', path: a.path ?? '/', method: a.method ?? 'ANY', flow_name: a.flow ?? '', action: 'upsert' })
      } else if (a.op === 'upsert_api') {
        noOpIndices.push(i)
      }
    }

    try {
      if (flows.length > 0 || apis.length > 0) await postSync(flows, apis)
      setMessages(prev => prev.map((m, idx) => {
        if (idx !== msgIndex) return m
        const next = new Set(m.appliedActions)
        pending.forEach(({ i }) => next.add(i))
        noOpIndices.forEach(i => next.add(i))
        return { ...m, appliedActions: next }
      }))
    } catch (err: unknown) {
      const errMsg = err instanceof Error ? err.message : String(err)
      setMessages(prev => prev.map((m, idx) => {
        if (idx !== msgIndex) return m
        const failed = { ...m.failedActions }
        pending.forEach(({ i }) => { failed[i] = errMsg })
        return { ...m, failedActions: failed }
      }))
    }
  }

  // ── Discard action ────────────────────────────────────────────────────────
  function discardAction(msgIndex: number, actionIndex: number) {
    const key = `${msgIndex}:${actionIndex}`
    setDiscardedActions(prev => {
      const next = new Set(prev)
      next.add(key)
      return next
    })
  }

  // ── Send message ──────────────────────────────────────────────────────────
  async function sendMessage() {
    if (!input.trim() || thinking) return
    const userMsg: ChatMessage = {
      role: 'user',
      content: input.trim(),
      appliedActions: new Set(),
      failedActions: {},
    }
    const history = messages.slice(-8).map(m => ({ role: m.role, content: m.content }))
    setMessages(prev => [...prev, userMsg])
    setInput('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
    setThinking(true)
    try {
      const res = await fetch('/api/ai/chat', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          message: userMsg.content,
          model,
          include_apis: includeAPIs,
          include_flows: includeFlows,
          session_history: history,
        }),
      })

      // Always try JSON first; fall back to text if content-type is wrong
      const contentType = res.headers.get('content-type') ?? ''
      let data: Record<string, unknown> = {}
      if (contentType.includes('application/json')) {
        data = await res.json()
      } else {
        const text = await res.text()
        data = { error: text || `HTTP ${res.status}` }
      }

      if (!res.ok) {
        setMessages(prev => [
          ...prev,
          { role: 'assistant', content: (data.error as string) || 'Error from server', appliedActions: new Set(), failedActions: {} },
        ])
      } else {
        setMessages(prev => [
          ...prev,
          {
            role: 'assistant',
            content: (data.confirm_message as string) ?? '',
            response: data as ChatMessage['response'],
            appliedActions: new Set(),
            failedActions: {},
          },
        ])
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      setMessages(prev => [
        ...prev,
        { role: 'assistant', content: 'Network error: ' + msg, appliedActions: new Set(), failedActions: {} },
      ])
    } finally {
      setThinking(false)
    }
  }

  // ── Key handler ───────────────────────────────────────────────────────────
  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && e.ctrlKey) {
      e.preventDefault()
      sendMessage()
    }
  }

  // ── Save project context ───────────────────────────────────────────────────
  async function saveProjectContext() {
    setContextSaving(true)
    setContextSaved(false)
    try {
      await fetch('/api/ai/project-context', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: 'project_context', content: contextText, updated_at: new Date().toISOString() })
      })
      setContextSaved(true)
      setTimeout(() => setContextSaved(false), 2000)
    } finally {
      setContextSaving(false)
    }
  }

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <div style={{ display: 'flex', height: 'calc(100vh - 120px)', gap: 16 }}>

      {/* ── Left: capabilities panel ── */}
      <div style={{
        width: 280,
        flexShrink: 0,
        display: 'flex',
        flexDirection: 'column',
        gap: 16,
        overflowY: 'auto',
      }}>
        <div style={{
          background: 'var(--step-bg)',
          border: '1px solid var(--border)',
          borderRadius: 10,
          padding: 16,
        }}>
          <div style={{ fontSize: 13, fontWeight: 700, marginBottom: 14, color: 'var(--text)' }}>
            Capabilities
          </div>

          {/* Model selector */}
          <div style={{ marginBottom: 14 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
              <label style={{ fontSize: 11, color: 'var(--muted)', fontWeight: 600 }}>
                MODEL
              </label>
              <button
                onClick={loadRegisteredModels}
                disabled={modelsLoading}
                style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 11, color: 'var(--muted)', padding: 0 }}
                title="Refresh model list"
              >
                {modelsLoading ? '…' : '↻'}
              </button>
            </div>
            {registeredModels.length === 0 ? (
              <div style={{ fontSize: 12, color: '#f59e0b', padding: '6px 8px', borderRadius: 6, border: '1px solid #f59e0b44', background: 'rgba(245,158,11,0.06)' }}>
                {modelsLoading ? 'Loading…' : 'No models registered — go to AI → Models to add one.'}
              </div>
            ) : (
              <select
                value={model}
                onChange={e => setModel(e.target.value)}
                style={{
                  width: '100%',
                  padding: '6px 8px',
                  borderRadius: 6,
                  border: '1px solid var(--border)',
                  background: 'var(--step-bg)',
                  color: 'var(--text)',
                  fontSize: 12,
                  cursor: 'pointer',
                }}
              >
                {registeredModels.map(m => (
                  <option key={m.alias} value={m.alias}>
                    {m.alias}{m.provider ? ` (${m.provider})` : ''}
                  </option>
                ))}
              </select>
            )}
          </div>

          {/* Context toggles */}
          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 11, color: 'var(--muted)', fontWeight: 600, display: 'block', marginBottom: 6 }}>
              CONTEXT
            </label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, cursor: 'pointer', marginBottom: 6 }}>
              <input
                type="checkbox"
                checked={includeAPIs}
                onChange={e => setIncludeAPIs(e.target.checked)}
                style={{ cursor: 'pointer' }}
              />
              Include API list
            </label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={includeFlows}
                onChange={e => setIncludeFlows(e.target.checked)}
                style={{ cursor: 'pointer' }}
              />
              Include flow names
            </label>
          </div>

          {/* Verbose toggle */}
          <div style={{ marginBottom: 14 }}>
            <label style={{ fontSize: 11, color: 'var(--muted)', fontWeight: 600, display: 'block', marginBottom: 6 }}>
              OBSERVABILITY
            </label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={showDebug}
                onChange={e => setShowDebug(e.target.checked)}
                style={{ cursor: 'pointer' }}
              />
              Show debug info
            </label>
            {showDebug && (
              <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4, lineHeight: 1.4 }}>
                Prompts, API calls, model, timing visible per message.
              </div>
            )}
          </div>

          {/* Refresh button */}
          <button
            onClick={loadCapabilities}
            disabled={capLoading}
            style={{
              width: '100%',
              padding: '6px 0',
              borderRadius: 6,
              border: '1px solid var(--border)',
              background: 'transparent',
              color: 'var(--muted)',
              fontSize: 12,
              cursor: capLoading ? 'default' : 'pointer',
              fontWeight: 600,
            }}
          >
            {capLoading ? 'Loading…' : 'Refresh'}
          </button>
        </div>

        {/* APIs list */}
        <div style={{
          background: 'var(--step-bg)',
          border: '1px solid var(--border)',
          borderRadius: 10,
          padding: 16,
        }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--muted)', marginBottom: 10, letterSpacing: 0.5 }}>
            YOUR APIS
          </div>
          {capabilities.apis.length === 0 ? (
            <div style={{ fontSize: 12, color: 'var(--muted)', fontStyle: 'italic' }}>
              {capLoading ? 'Loading…' : 'Could not load'}
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {capabilities.apis.map(a => (
                <div key={a.name} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 6 }}>
                  <span style={{ fontSize: 12, color: 'var(--text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {a.name}
                  </span>
                  <span style={{
                    fontSize: 10,
                    fontWeight: 700,
                    padding: '1px 6px',
                    borderRadius: 4,
                    background: 'var(--accent)',
                    color: '#031427',
                    flexShrink: 0,
                  }}>
                    {a.endpointCount}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Flows list */}
        <div style={{
          background: 'var(--step-bg)',
          border: '1px solid var(--border)',
          borderRadius: 10,
          padding: 16,
        }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--muted)', marginBottom: 10, letterSpacing: 0.5 }}>
            YOUR FLOWS
          </div>
          {capabilities.flows.length === 0 ? (
            <div style={{ fontSize: 12, color: 'var(--muted)', fontStyle: 'italic' }}>
              {capLoading ? 'Loading…' : 'Could not load'}
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              {capabilities.flows.map(name => (
                <div key={name} style={{
                  fontSize: 12,
                  color: 'var(--text)',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  padding: '2px 0',
                }}>
                  {name}
                </div>
              ))}
            </div>
          )}
        </div>

        {/* ── Project Context ── */}
        <div style={{ marginTop: 16, borderTop: '1px solid var(--border)', paddingTop: 12 }}>
          <button
            onClick={() => setContextOpen(o => !o)}
            style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 12, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 4, padding: 0 }}
          >
            <span>{contextOpen ? '▾' : '▸'}</span> Project Context
          </button>
          {contextOpen && (
            <div style={{ marginTop: 8 }}>
              <p style={{ fontSize: 11, color: 'var(--muted)', margin: '0 0 6px' }}>
                Always injected into every AI prompt. Add naming conventions, security rules, constraints.
              </p>
              <textarea
                value={contextText}
                onChange={e => setContextText(e.target.value)}
                rows={5}
                style={{ width: '100%', fontSize: 12, background: 'var(--step-bg)', border: '1px solid var(--border)', borderRadius: 4, padding: 6, color: 'inherit', resize: 'vertical', boxSizing: 'border-box' }}
                placeholder="e.g. Always use JWT auth. Rate limit all /api/* endpoints."
              />
              <div style={{ display: 'flex', gap: 8, marginTop: 6, alignItems: 'center' }}>
                <button
                  onClick={saveProjectContext}
                  disabled={contextSaving}
                  style={{ fontSize: 12, padding: '4px 12px', background: 'var(--accent)', color: '#fff', border: 'none', borderRadius: 4, cursor: 'pointer' }}
                >
                  {contextSaving ? 'Saving…' : 'Save'}
                </button>
                <button
                  onClick={() => { setContextText(''); saveProjectContext() }}
                  disabled={contextSaving}
                  style={{ fontSize: 12, padding: '4px 12px', background: 'var(--step-bg)', color: 'var(--muted)', border: '1px solid var(--border)', borderRadius: 4, cursor: 'pointer' }}
                >
                  Clear
                </button>
                {contextSaved && <span style={{ fontSize: 11, color: '#22c55e' }}>Saved ✓</span>}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* ── Right: chat pane ── */}
      <div style={{
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        border: '1px solid var(--border)',
        borderRadius: 10,
        overflow: 'hidden',
        background: 'var(--step-bg)',
        minWidth: 0,
      }}>
        {/* Scrollable message area */}
        <div style={{
          flex: 1,
          overflowY: 'auto',
          padding: 16,
          display: 'flex',
          flexDirection: 'column',
          gap: 12,
        }}>
          {messages.length === 0 && (
            <div style={{
              flex: 1,
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              color: 'var(--muted)',
              gap: 8,
              paddingTop: 40,
            }}>
              <div style={{ fontSize: 32 }}>✦</div>
              <div style={{ fontSize: 14, fontWeight: 600 }}>AI Assistant</div>
              <div style={{ fontSize: 13, textAlign: 'center', maxWidth: 340 }}>
                Describe what you want to build — flows, APIs, routes — and the assistant will generate them for you.
              </div>
              <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4 }}>
                Press Ctrl+Enter to send
              </div>
            </div>
          )}

          {messages.map((msg, mi) => (
            <div
              key={mi}
              style={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: msg.role === 'user' ? 'flex-end' : 'flex-start',
              }}
            >
              {msg.role === 'user' ? (
                <div style={{
                  maxWidth: '70%',
                  background: '#1e293b',
                  borderRadius: 10,
                  padding: '10px 14px',
                  fontSize: 13,
                  color: 'var(--text)',
                  lineHeight: 1.5,
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-word',
                }}>
                  {msg.content}
                </div>
              ) : (
                <div style={{ maxWidth: '90%', minWidth: 0 }}>
                  {/* Phase badge */}
                  {msg.response?.phase && msg.response.phase !== 'execute' && (
                    <div style={{ marginBottom: 6 }}>
                      <span style={{
                        fontSize: 10, fontWeight: 800, padding: '2px 8px', borderRadius: 4, letterSpacing: 0.5,
                        background: msg.response.phase === 'discovery' ? '#f59e0b' : msg.response.phase === 'plan' ? '#0ea5e9' : 'var(--accent)',
                        color: '#fff',
                      }}>
                        {msg.response.phase === 'discovery' ? '● QUESTIONS' : msg.response.phase === 'plan' ? '● PLAN' : msg.response.phase.toUpperCase()}
                      </span>
                    </div>
                  )}

                  {/* confirm_message or plain content */}
                  <div style={{ fontSize: 13, color: 'var(--text)', lineHeight: 1.6, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                    {msg.response?.confirm_message ?? msg.content}
                  </div>

                  {/* Plan preview (phase = "plan") */}
                  {msg.response?.plan && (
                    <PlanPreview plan={msg.response.plan} />
                  )}

                  {/* Questions box */}
                  {msg.response && msg.response.questions.length > 0 && (
                    <div style={{
                      marginTop: 10,
                      border: `1px solid ${msg.response.phase === 'plan' ? '#0ea5e9' : '#ca8a04'}`,
                      borderRadius: 8,
                      padding: '10px 14px',
                      background: msg.response.phase === 'plan' ? 'rgba(14,165,233,0.06)' : 'rgba(202,138,4,0.08)',
                    }}>
                      {msg.response.phase === 'discovery' && (
                        <div style={{ fontSize: 11, fontWeight: 700, color: '#ca8a04', marginBottom: 6, letterSpacing: 0.5 }}>
                          A FEW QUESTIONS BEFORE I BUILD
                        </div>
                      )}
                      <ul style={{ margin: 0, paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 6 }}>
                        {msg.response.questions.map((q, qi) => (
                          <li key={qi} style={{ fontSize: 13, color: 'var(--text)' }}>{q}</li>
                        ))}
                      </ul>
                      {msg.response.actions.length === 0 && mi === messages.length - 1 && (
                        <div style={{ display: 'flex', gap: 8, marginTop: 10, flexWrap: 'wrap' }}>
                          {msg.response.phase === 'plan' ? (
                            <>
                              <button
                                onClick={() => { setInput('Yes, looks good. Please proceed.'); textareaRef.current?.focus() }}
                                style={{ padding: '5px 18px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 700, background: '#22c55e', color: '#fff' }}
                              >
                                ✓ Looks good, proceed
                              </button>
                              <button
                                onClick={() => { setInput('I want to change: '); textareaRef.current?.focus() }}
                                style={{ padding: '5px 16px', borderRadius: 6, border: '1px solid var(--border)', cursor: 'pointer', fontSize: 12, fontWeight: 600, background: 'var(--step-bg)', color: 'var(--muted)' }}
                              >
                                ✎ Adjust the plan
                              </button>
                            </>
                          ) : (
                            <>
                              <button
                                onClick={() => { setInput('Here are my answers: '); textareaRef.current?.focus() }}
                                style={{ padding: '5px 18px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 700, background: 'var(--accent)', color: '#031427' }}
                              >
                                Answer & continue
                              </button>
                              <button
                                onClick={() => { setInput('Skip the questions and use sensible defaults.'); textareaRef.current?.focus() }}
                                style={{ padding: '5px 16px', borderRadius: 6, border: '1px solid var(--border)', cursor: 'pointer', fontSize: 12, fontWeight: 600, background: 'var(--step-bg)', color: 'var(--muted)' }}
                              >
                                Use defaults
                              </button>
                            </>
                          )}
                        </div>
                      )}
                    </div>
                  )}

                  {/* Action blocks */}
                  {msg.response && msg.response.actions.length > 0 && (() => {
                    const pendingCount = msg.response.actions.filter((_, ai) => {
                      return !msg.appliedActions.has(ai) && !discardedActions.has(`${mi}:${ai}`)
                    }).length
                    return (
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
                        {/* Apply All header */}
                        {pendingCount >= 2 && (
                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 8, padding: '8px 12px', background: 'var(--panel)', borderRadius: 8, border: '1px solid var(--border)' }}>
                            <span style={{ fontSize: 12, color: 'var(--muted)' }}>{pendingCount} item{pendingCount !== 1 ? 's' : ''} ready to apply</span>
                            <button
                              onClick={() => applyAllActions(mi)}
                              style={{ padding: '4px 16px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 700, background: 'var(--accent)', color: '#031427' }}
                            >
                              Apply All
                            </button>
                          </div>
                        )}
                        {msg.response.actions.map((action, ai) => {
                          const discardKey = `${mi}:${ai}`
                          const isDiscarded = discardedActions.has(discardKey)
                          const isApplied = msg.appliedActions.has(ai)
                          const failMsg = msg.failedActions[ai]
                          if (isDiscarded) return null
                          return (
                            <ActionBlock
                              key={ai}
                              action={action}
                              index={ai}
                              msgIndex={mi}
                              applied={isApplied}
                              failed={failMsg}
                              onApply={applyAction}
                              onDiscard={discardAction}
                            />
                          )
                        })}
                      </div>
                    )
                  })()}

                  {/* Debug panel */}
                  {showDebug && msg.response?.debug && (
                    <DebugPanel debug={msg.response.debug} />
                  )}
                </div>
              )}
            </div>
          ))}

          {thinking && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--muted)', fontSize: 13 }}>
              <span style={{ animation: 'pulse 1s ease-in-out infinite' }}>●</span>
              Thinking…
            </div>
          )}

          <div ref={chatEndRef} />
        </div>

        {/* Input bar */}
        <div style={{
          borderTop: '1px solid var(--border)',
          padding: '12px 16px',
          display: 'flex',
          alignItems: 'flex-end',
          gap: 10,
          background: 'var(--step-bg)',
        }}>
          <textarea
            ref={textareaRef}
            value={input}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            placeholder="Describe what you want to build…"
            rows={1}
            style={{
              flex: 1,
              resize: 'none',
              border: '1px solid var(--border)',
              borderRadius: 8,
              padding: '8px 12px',
              fontSize: 13,
              lineHeight: 1.5,
              background: 'transparent',
              color: 'var(--text)',
              outline: 'none',
              fontFamily: 'inherit',
              overflow: 'hidden',
              minHeight: 36,
              maxHeight: 120,
            }}
          />
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0 }}>
            {thinking && (
              <span style={{ fontSize: 12, color: 'var(--muted)' }}>Thinking…</span>
            )}
            <button
              onClick={sendMessage}
              disabled={thinking || !input.trim()}
              style={{
                padding: '8px 18px',
                borderRadius: 8,
                border: 'none',
                cursor: thinking || !input.trim() ? 'not-allowed' : 'pointer',
                fontSize: 13,
                fontWeight: 700,
                background: thinking || !input.trim() ? 'var(--step-bg)' : 'var(--accent)',
                color: thinking || !input.trim() ? 'var(--muted)' : '#031427',
                transition: 'background 0.15s, color 0.15s',
              }}
            >
              Send
            </button>
          </div>
        </div>

        {/* ── Session History ── */}
        <div style={{ borderTop: '1px solid var(--border)', paddingTop: 8, marginTop: 4, paddingBottom: 12, paddingLeft: 16, paddingRight: 16 }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <button
              onClick={() => setHistoryOpen(o => !o)}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 12, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 4, padding: 0 }}
            >
              <span>{historyOpen ? '▾' : '▸'}</span> Session History
            </button>
            {historyOpen && (
              <button onClick={loadHistory} disabled={historyLoading}
                style={{ fontSize: 11, padding: '2px 8px', background: 'var(--step-bg)', border: '1px solid var(--border)', borderRadius: 4, cursor: 'pointer', color: 'var(--muted)' }}>
                {historyLoading ? '…' : 'Refresh'}
              </button>
            )}
          </div>
          {historyOpen && (
            <div style={{ marginTop: 8, maxHeight: 220, overflowY: 'auto' }}>
              {auditRecords.length === 0 && !historyLoading && (
                <p style={{ fontSize: 12, color: 'var(--muted)', margin: 0 }}>No history yet.</p>
              )}
              {auditRecords.map(rec => (
                <div key={rec.id} style={{ padding: '6px 0', borderBottom: '1px solid var(--border)', fontSize: 12 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 3 }}>
                    <span style={{ color: 'var(--muted)', fontSize: 11 }}>
                      {new Date(rec.created_at).toLocaleDateString()} {new Date(rec.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                    </span>
                    {rec.gateway_name && (
                      <span style={{ fontSize: 10, padding: '1px 6px', background: 'var(--step-bg)', borderRadius: 8, color: 'var(--muted)' }}>
                        {rec.gateway_name}
                      </span>
                    )}
                  </div>
                  <div style={{ marginBottom: 4 }}>{rec.summary}</div>
                  {rec.artifacts && rec.artifacts.length > 0 && (
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                      {rec.artifacts.map(a => (
                        <span key={a} style={{ fontSize: 10, padding: '1px 8px', background: 'var(--accent)', color: '#fff', borderRadius: 10 }}>
                          {a}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
