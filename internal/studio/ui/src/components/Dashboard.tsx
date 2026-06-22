import { useEffect, useRef, useState } from 'react'
import { fetchGatewayApis, fetchTargets, listLLMModels } from '../api'
import type { ConnStatus, DeployRecord, GatewayState } from '../types'
import type { LLMModel } from '../types'

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

// ── Quick AI widget ───────────────────────────────────────────────────────────

type QuickAction = { op: string; name?: string; dsl?: string; description?: string; api?: string; path?: string; method?: string; flow?: string }
type QuickMsg = {
  role: 'user' | 'assistant'
  content: string
  isError?: boolean
  actions?: QuickAction[]
  questions?: string[]
  appliedActions: Set<number>
  failedActions: Record<number, string>
}

function opColor(op: string): string {
  const o = op.toLowerCase()
  if (o.includes('delete') || o.includes('remove')) return '#ef4444'
  if (o.includes('upsert') || o.includes('create')) return '#0ea5e9'
  return 'var(--accent)'
}

function QuickAI() {
  const [models, setModels] = useState<LLMModel[]>([])
  const [model, setModel] = useState('')
  const [input, setInput] = useState('')
  const [messages, setMessages] = useState<QuickMsg[]>([])
  const [thinking, setThinking] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    listLLMModels()
      .then(ms => { setModels(ms); if (ms.length > 0) setModel(ms[0].alias) })
      .catch(() => {})
  }, [])

  useEffect(() => {
    if (expanded) bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, expanded])

  async function ask() {
    if (!input.trim() || thinking || !model) return
    const userText = input.trim()
    const history = messages.slice(-8).map(m => ({ role: m.role, content: m.content }))
    setMessages(prev => [...prev, { role: 'user', content: userText, appliedActions: new Set(), failedActions: {} }])
    setInput('')
    if (textareaRef.current) textareaRef.current.style.height = 'auto'
    setThinking(true)
    try {
      const res = await fetch('/api/ai/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: userText, model, include_apis: true, include_flows: true, session_history: history }),
      })
      const ct = res.headers.get('content-type') ?? ''
      const data = ct.includes('application/json') ? await res.json() : { error: await res.text() }
      if (!res.ok) {
        setMessages(prev => [...prev, { role: 'assistant', content: (data.error as string) || 'Error', isError: true, appliedActions: new Set(), failedActions: {} }])
      } else {
        setMessages(prev => [...prev, {
          role: 'assistant',
          content: (data.confirm_message as string) || 'Done.',
          actions: (data.actions ?? []) as QuickAction[],
          questions: (data.questions ?? []) as string[],
          appliedActions: new Set(),
          failedActions: {},
        }])
      }
    } catch (e: unknown) {
      setMessages(prev => [...prev, { role: 'assistant', content: 'Network error: ' + (e instanceof Error ? e.message : String(e)), isError: true, appliedActions: new Set(), failedActions: {} }])
    } finally {
      setThinking(false)
    }
  }

  async function postSync(flows: unknown[], apis: unknown[]) {
    const res = await fetch('/api/sync', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sync_uuid: `ai-${Date.now()}`, flows, apis }),
    })
    if (!res.ok) throw new Error(await res.text().catch(() => `HTTP ${res.status}`))
  }

  async function applyAction(msgIdx: number, actIdx: number, action: QuickAction) {
    try {
      if (action.op === 'upsert_flow') {
        let instructions: unknown[]
        try { instructions = JSON.parse(action.dsl ?? '[]') } catch { throw new Error('Invalid flow DSL') }
        await postSync([{ name: action.name ?? '', instructions, action: 'upsert' }], [])
      } else if (action.op === 'add_endpoint') {
        await postSync([], [{ name: action.api ?? '', path: action.path ?? '/', method: action.method ?? 'ANY', flow_name: action.flow ?? '', action: 'upsert' }])
      } else if (action.op === 'upsert_api') {
        // no-op: API created implicitly when endpoints are added
      } else {
        throw new Error(`Not yet implemented: ${action.op}`)
      }
      setMessages(prev => prev.map((m, i) => {
        if (i !== msgIdx) return m
        const next = new Set(m.appliedActions); next.add(actIdx)
        return { ...m, appliedActions: next }
      }))
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setMessages(prev => prev.map((m, i) => i !== msgIdx ? m : { ...m, failedActions: { ...m.failedActions, [actIdx]: msg } }))
    }
  }

  function handleKey(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && e.ctrlKey) { e.preventDefault(); ask() }
  }

  function handleInputChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    setInput(e.target.value)
    const el = textareaRef.current
    if (el) { el.style.height = 'auto'; el.style.height = Math.min(el.scrollHeight, 120) + 'px' }
  }

  if (models.length === 0) return null

  // Last assistant message shown in compact mode
  const lastAssistant = [...messages].reverse().find(m => m.role === 'assistant')

  return (
    <div style={{
      background: 'var(--panel)',
      border: '1px solid var(--border)',
      borderRadius: 12,
      overflow: 'hidden',
      marginTop: 20,
      // Animate height change
      transition: 'box-shadow 0.2s',
      boxShadow: expanded ? '0 8px 32px rgba(0,0,0,0.18)' : 'none',
    }}>
      {/* ── Header ── */}
      <div style={{
        padding: '10px 14px',
        borderBottom: '1px solid var(--border)',
        fontWeight: 600,
        fontSize: 13,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        cursor: 'pointer',
        userSelect: 'none',
      }}
        onClick={() => setExpanded(e => !e)}
      >
        <span style={{ color: 'var(--accent)' }}>✦</span>
        <span>AI Assistant</span>
        {messages.length > 0 && (
          <span style={{ fontSize: 11, color: 'var(--muted)', fontWeight: 400 }}>
            {messages.length} message{messages.length !== 1 ? 's' : ''}
          </span>
        )}
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 10 }}>
          {models.length > 1 ? (
            <select
              value={model}
              onChange={e => { e.stopPropagation(); setModel(e.target.value) }}
              onClick={e => e.stopPropagation()}
              style={{ fontSize: 11, padding: '2px 6px', borderRadius: 4, border: '1px solid var(--border)', background: 'var(--step-bg)', color: 'var(--text)', cursor: 'pointer' }}
            >
              {models.map(m => <option key={m.alias} value={m.alias}>{m.alias}</option>)}
            </select>
          ) : (
            <span style={{ fontSize: 11, color: 'var(--muted)' }}>{models[0].alias}</span>
          )}
          <span style={{ fontSize: 16, color: 'var(--muted)', lineHeight: 1, transition: 'transform 0.2s', transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)' }}>
            ⌄
          </span>
        </div>
      </div>

      {/* ── Compact preview (collapsed) ── */}
      {!expanded && (
        <div style={{ padding: 14 }}>
          {lastAssistant && (
            <div style={{
              marginBottom: 10,
              padding: '8px 12px',
              borderRadius: 8,
              background: lastAssistant.isError ? 'rgba(239,68,68,0.08)' : 'rgba(34,197,94,0.06)',
              border: `1px solid ${lastAssistant.isError ? '#ef444444' : '#22c55e44'}`,
              fontSize: 13,
              color: lastAssistant.isError ? '#ef4444' : 'var(--text)',
              lineHeight: 1.5,
            }}>
              {lastAssistant.content}
              {lastAssistant.actions && lastAssistant.actions.length > 0 && (
                <span style={{ color: 'var(--muted)', fontSize: 12 }}>
                  {' '}— {lastAssistant.actions.length} action{lastAssistant.actions.length !== 1 ? 's' : ''}
                </span>
              )}
            </div>
          )}
          <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
            <textarea
              ref={textareaRef}
              value={input}
              onChange={handleInputChange}
              onKeyDown={handleKey}
              onClick={e => e.stopPropagation()}
              placeholder="Ask AI to build something… (Ctrl+Enter to send)"
              rows={2}
              style={{ flex: 1, resize: 'none', border: '1px solid var(--border)', borderRadius: 8, padding: '8px 12px', fontSize: 13, background: 'var(--step-bg)', color: 'var(--text)', outline: 'none', fontFamily: 'inherit' }}
            />
            <button
              onClick={e => { e.stopPropagation(); ask() }}
              disabled={thinking || !input.trim()}
              style={{ padding: '8px 18px', borderRadius: 8, border: 'none', cursor: thinking || !input.trim() ? 'not-allowed' : 'pointer', fontSize: 13, fontWeight: 700, background: thinking || !input.trim() ? 'var(--step-bg)' : 'var(--accent)', color: thinking || !input.trim() ? 'var(--muted)' : '#031427', whiteSpace: 'nowrap' }}
            >
              {thinking ? '…' : 'Ask'}
            </button>
          </div>
          <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 6 }}>
            Click header to expand · Full observability in AI → Assistant
          </div>
        </div>
      )}

      {/* ── Expanded: full conversation ── */}
      {expanded && (
        <div style={{ display: 'flex', flexDirection: 'column', height: 520 }}>
          {/* Message list */}
          <div style={{ flex: 1, overflowY: 'auto', padding: '14px 16px', display: 'flex', flexDirection: 'column', gap: 10 }}>
            {messages.length === 0 && (
              <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', color: 'var(--muted)', gap: 8, paddingTop: 40 }}>
                <div style={{ fontSize: 28 }}>✦</div>
                <div style={{ fontSize: 13, fontWeight: 600 }}>AI Assistant</div>
                <div style={{ fontSize: 12, textAlign: 'center', maxWidth: 300 }}>
                  Describe what you want — flows, APIs, tenants — and the assistant will generate them.
                </div>
                <div style={{ fontSize: 11, color: 'var(--muted)' }}>Ctrl+Enter to send</div>
              </div>
            )}
            {messages.map((msg, mi) => (
              <div key={mi} style={{ display: 'flex', flexDirection: 'column', alignItems: msg.role === 'user' ? 'flex-end' : 'flex-start' }}>
                {msg.role === 'user' ? (
                  <div style={{ maxWidth: '72%', background: '#1e293b', borderRadius: 10, padding: '9px 13px', fontSize: 13, color: 'var(--text)', lineHeight: 1.5, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                    {msg.content}
                  </div>
                ) : (
                  <div style={{ maxWidth: '88%', minWidth: 0 }}>
                    <div style={{ fontSize: 13, color: msg.isError ? '#ef4444' : 'var(--text)', lineHeight: 1.6, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                      {msg.content}
                    </div>
                    {/* Questions / planning response */}
                    {msg.questions && msg.questions.length > 0 && (
                      <div style={{ marginTop: 8, padding: '8px 12px', borderRadius: 8, border: '1px solid #ca8a04', background: 'rgba(202,138,4,0.08)' }}>
                        <ul style={{ margin: 0, paddingLeft: 16, display: 'flex', flexDirection: 'column', gap: 3 }}>
                          {msg.questions.map((q, qi) => (
                            <li key={qi} style={{ fontSize: 12, color: 'var(--text)' }}>{q}</li>
                          ))}
                        </ul>
                      </div>
                    )}
                    {/* Action blocks */}
                    {msg.actions && msg.actions.map((action, ai) => {
                      const isApplied = msg.appliedActions.has(ai)
                      const failMsg = msg.failedActions[ai]
                      const label = action.name || action.api || action.path || ''
                      return (
                        <div key={ai} style={{ border: '1px solid var(--border)', borderRadius: 8, marginTop: 8, overflow: 'hidden', background: 'var(--step-bg)' }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 12px', borderBottom: '1px solid var(--border)' }}>
                            <span style={{ fontSize: 10, fontWeight: 800, padding: '2px 7px', borderRadius: 4, background: opColor(action.op), color: '#fff', letterSpacing: 0.5 }}>
                              {action.op.toUpperCase()}
                            </span>
                            {label && <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text)' }}>{label}</span>}
                          </div>
                          {/* Description (plain English) */}
                          {action.description && (
                            <div style={{ padding: '7px 12px', fontSize: 12, color: 'var(--text)', lineHeight: 1.5 }}>{action.description}</div>
                          )}
                          <div style={{ padding: '7px 12px', display: 'flex', alignItems: 'center', gap: 8 }}>
                            {!isApplied && failMsg === undefined && (
                              <button onClick={() => applyAction(mi, ai, action)} style={{ padding: '3px 12px', borderRadius: 5, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 700, background: '#22c55e', color: '#fff' }}>
                                Apply
                              </button>
                            )}
                            {isApplied && <span style={{ fontSize: 12, color: '#22c55e', fontWeight: 600 }}>✓ Applied</span>}
                            {!isApplied && failMsg !== undefined && <span style={{ fontSize: 12, color: '#ef4444', fontWeight: 600 }}>✗ {failMsg}</span>}
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            ))}
            {thinking && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--muted)', fontSize: 13 }}>
                <span>●</span> Thinking…
              </div>
            )}
            <div ref={bottomRef} />
          </div>

          {/* Input bar */}
          <div style={{ borderTop: '1px solid var(--border)', padding: '10px 14px', display: 'flex', alignItems: 'flex-end', gap: 8, background: 'var(--step-bg)' }}>
            <textarea
              ref={textareaRef}
              value={input}
              onChange={handleInputChange}
              onKeyDown={handleKey}
              placeholder="Describe what you want to build… (Ctrl+Enter to send)"
              rows={1}
              style={{ flex: 1, resize: 'none', border: '1px solid var(--border)', borderRadius: 8, padding: '8px 12px', fontSize: 13, lineHeight: 1.5, background: 'transparent', color: 'var(--text)', outline: 'none', fontFamily: 'inherit', minHeight: 36, maxHeight: 120, overflow: 'hidden' }}
            />
            <button
              onClick={ask}
              disabled={thinking || !input.trim()}
              style={{ padding: '8px 18px', borderRadius: 8, border: 'none', cursor: thinking || !input.trim() ? 'not-allowed' : 'pointer', fontSize: 13, fontWeight: 700, background: thinking || !input.trim() ? 'var(--step-bg)' : 'var(--accent)', color: thinking || !input.trim() ? 'var(--muted)' : '#031427', whiteSpace: 'nowrap' }}
            >
              {thinking ? '…' : 'Send'}
            </button>
          </div>
          <div style={{ padding: '4px 14px 10px', fontSize: 11, color: 'var(--muted)' }}>
            Full observability & session history → AI → Assistant
          </div>
        </div>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────

interface DashboardProps {
  conn: ConnStatus
  localFlowCount?: number
  localApiCount?: number
}

export default function Dashboard({ conn, localFlowCount = 0, localApiCount = 0 }: DashboardProps) {
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
        <StatCard label="Flows" value={localFlowCount} sub={flowCount > 0 ? `${flowCount} deployed to gateway` : 'not yet deployed'} />
        <StatCard label="APIs" value={localApiCount} sub={apiCount > 0 ? `${apiCount} deployed to gateway` : 'not yet deployed'} />
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

      <QuickAI />
    </div>
  )
}
