import { useState, useEffect, useRef } from 'react'
import { listLLMModels } from '../api'
import type { LLMModel } from '../types'

// ── Types ─────────────────────────────────────────────────────────────────────

type ChatAction = {
  op: string; name?: string; dsl?: string; description?: string
  api?: string; path?: string; method?: string; flow?: string; target?: string
}

type PlanFlow = { name: string; description: string; subflows?: PlanFlow[] }
type PlanEndpoint = { method: string; path: string; description: string }
type PlanAPI = { name: string; endpoints: PlanEndpoint[] }
type Plan = { summary: string; flows: PlanFlow[]; apis: PlanAPI[]; policies?: string[] }

type ChatMsg = {
  role: 'user' | 'assistant'
  content: string
  response?: {
    phase?: string
    confirm_message: string
    plan?: Plan
    actions: ChatAction[]
    questions: string[]
    suggested_answers?: string[]
    debug?: DebugInfo
  }
  appliedActions: Set<number>
  failedActions: Record<number, string>
}

type DebugInfo = {
  model_alias: string
  system_prompt: string
  messages_sent: { role: string; content: string }[]
  raw_llm_response: string
  duration_ms: number
  api_calls: { label: string; url: string; status_code?: number; duration_ms: number; error?: string }[]
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function methodColor(m: string) {
  if (m === 'GET') return { bg: '#0ea5e930', fg: '#0ea5e9' }
  if (m === 'POST') return { bg: '#22c55e30', fg: '#22c55e' }
  if (m === 'DELETE') return { bg: '#ef444430', fg: '#ef4444' }
  return { bg: '#f59e0b30', fg: '#f59e0b' }
}

function opColor(op: string) {
  const o = op.toLowerCase()
  if (o.includes('upsert') || o.includes('create')) return '#0ea5e9'
  if (o.includes('delete') || o.includes('remove')) return '#ef4444'
  return 'var(--accent)'
}

// ── Plan Preview ──────────────────────────────────────────────────────────────

function FlowNode({ flow, depth = 0 }: { flow: PlanFlow; depth?: number }) {
  const [open, setOpen] = useState(true)
  const hasKids = (flow.subflows?.length ?? 0) > 0
  return (
    <div style={{ marginLeft: depth * 14 }}>
      <div style={{ display: 'flex', gap: 6, padding: '4px 0', cursor: hasKids ? 'pointer' : 'default', alignItems: 'flex-start' }}
        onClick={() => hasKids && setOpen(o => !o)}>
        <span style={{ color: 'var(--accent)', fontSize: 12, flexShrink: 0, marginTop: 1 }}>
          {hasKids ? (open ? '▾' : '▸') : '◆'}
        </span>
        <div>
          <span style={{ fontSize: 12, fontWeight: 700, color: 'var(--text)', fontFamily: 'monospace' }}>{flow.name}</span>
          {flow.description && <span style={{ fontSize: 11, color: 'var(--muted)', marginLeft: 6 }}>— {flow.description}</span>}
        </div>
      </div>
      {open && hasKids && flow.subflows?.map((sf, i) => <FlowNode key={i} flow={sf} depth={depth + 1} />)}
    </div>
  )
}

function PlanPreview({ plan }: { plan: Plan }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 10 }}>
      {plan.summary && (
        <div style={{ fontSize: 12, color: 'var(--text)', lineHeight: 1.6, padding: '8px 12px', background: 'rgba(14,165,233,0.06)', border: '1px solid rgba(14,165,233,0.2)', borderRadius: 8 }}>
          {plan.summary}
        </div>
      )}
      {plan.flows?.length > 0 && (
        <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
          <div style={{ padding: '6px 12px', background: 'var(--panel)', borderBottom: '1px solid var(--border)', fontSize: 10, fontWeight: 700, color: 'var(--muted)', letterSpacing: 0.5 }}>
            FLOWS & SUB-FLOWS ({plan.flows.length})
          </div>
          <div style={{ padding: '6px 12px' }}>
            {plan.flows.map((f, i) => <FlowNode key={i} flow={f} />)}
          </div>
        </div>
      )}
      {plan.apis?.length > 0 && (
        <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
          <div style={{ padding: '6px 12px', background: 'var(--panel)', borderBottom: '1px solid var(--border)', fontSize: 10, fontWeight: 700, color: 'var(--muted)', letterSpacing: 0.5 }}>
            APIs & ENDPOINTS
          </div>
          <div style={{ padding: '6px 12px', display: 'flex', flexDirection: 'column', gap: 8 }}>
            {plan.apis.map((api, ai) => (
              <div key={ai}>
                <div style={{ fontSize: 12, fontWeight: 700, color: 'var(--text)', marginBottom: 3, fontFamily: 'monospace' }}>◆ {api.name}</div>
                {api.endpoints?.map((ep, ei) => {
                  const { bg, fg } = methodColor(ep.method)
                  return (
                    <div key={ei} style={{ display: 'flex', gap: 6, alignItems: 'flex-start', marginLeft: 12, padding: '2px 0' }}>
                      <span style={{ fontSize: 9, fontWeight: 800, padding: '1px 5px', borderRadius: 3, background: bg, color: fg, flexShrink: 0, marginTop: 2, letterSpacing: 0.5 }}>{ep.method}</span>
                      <span style={{ fontSize: 11, fontFamily: 'monospace', color: 'var(--text)', flexShrink: 0, marginRight: 4 }}>{ep.path}</span>
                      {ep.description && <span style={{ fontSize: 11, color: 'var(--muted)' }}>{ep.description}</span>}
                    </div>
                  )
                })}
              </div>
            ))}
          </div>
        </div>
      )}
      {plan.policies && plan.policies.length > 0 && (
        <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
          <div style={{ padding: '6px 12px', background: 'var(--panel)', borderBottom: '1px solid var(--border)', fontSize: 10, fontWeight: 700, color: 'var(--muted)', letterSpacing: 0.5 }}>
            SECURITY & POLICIES
          </div>
          <ul style={{ margin: 0, padding: '6px 12px 6px 26px', display: 'flex', flexDirection: 'column', gap: 3 }}>
            {plan.policies.map((p, i) => <li key={i} style={{ fontSize: 11, color: 'var(--text)' }}>{p}</li>)}
          </ul>
        </div>
      )}
    </div>
  )
}

// ── Discovery Form ────────────────────────────────────────────────────────────

function DiscoveryForm({ questions, suggested, onSubmit, disabled }: {
  questions: string[]
  suggested: string[]
  onSubmit: (answers: string[]) => void
  disabled: boolean
}) {
  const [answers, setAnswers] = useState<string[]>(() => questions.map((_, i) => suggested[i] ?? ''))

  function setAnswer(i: number, val: string) {
    setAnswers(prev => { const next = [...prev]; next[i] = val; return next })
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 10 }}>
      {questions.map((q, i) => (
        <div key={i} style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
          <div style={{ padding: '6px 10px', background: 'var(--panel)', fontSize: 12, color: 'var(--text)', lineHeight: 1.5 }}>
            {q}
          </div>
          <textarea
            value={answers[i] ?? ''}
            onChange={e => setAnswer(i, e.target.value)}
            disabled={disabled}
            rows={2}
            style={{
              width: '100%', boxSizing: 'border-box',
              resize: 'none', border: 'none',
              borderTop: '1px solid var(--border)',
              padding: '6px 10px', fontSize: 12,
              background: 'var(--step-bg)', color: 'var(--text)',
              fontFamily: 'inherit', outline: 'none',
            }}
          />
        </div>
      ))}
      <button
        onClick={() => onSubmit(answers)}
        disabled={disabled}
        style={{ alignSelf: 'flex-start', padding: '6px 18px', borderRadius: 6, border: 'none', cursor: disabled ? 'not-allowed' : 'pointer', fontSize: 12, fontWeight: 700, background: 'var(--accent)', color: '#031427' }}
      >
        Submit Answers
      </button>
    </div>
  )
}

// ── Debug Panel ───────────────────────────────────────────────────────────────

function DebugPanel({ debug }: { debug: DebugInfo }) {
  const [showPrompt, setShowPrompt] = useState(false)
  const [showMsgs, setShowMsgs] = useState(false)
  const [showRaw, setShowRaw] = useState(false)
  const sec: React.CSSProperties = { marginTop: 4, border: '1px solid var(--border)', borderRadius: 6, overflow: 'hidden' }
  const hdr: React.CSSProperties = { padding: '3px 8px', background: 'var(--panel)', fontSize: 10, fontWeight: 700, color: 'var(--muted)', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4, userSelect: 'none' }
  const body: React.CSSProperties = { padding: '6px 8px', fontFamily: 'monospace', fontSize: 10, whiteSpace: 'pre-wrap', wordBreak: 'break-all', color: 'var(--text)', background: 'var(--block-bg)', maxHeight: 200, overflowY: 'auto' }
  return (
    <div style={{ marginTop: 8, padding: '8px', border: '1px solid #7c3aed44', borderRadius: 8, background: 'rgba(124,58,237,0.04)', fontSize: 11 }}>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 4 }}>
        <span style={{ fontSize: 9, fontWeight: 800, padding: '1px 6px', borderRadius: 3, background: '#7c3aed', color: '#fff', letterSpacing: 0.5 }}>DEBUG</span>
        <span style={{ color: 'var(--muted)' }}>model: <b>{debug.model_alias}</b></span>
        <span style={{ color: 'var(--muted)' }}>{debug.duration_ms}ms</span>
      </div>
      <div style={sec}><div style={hdr} onClick={() => setShowPrompt(o => !o)}><span>{showPrompt ? '▾' : '▸'}</span> System Prompt <span style={{ marginLeft: 'auto', fontWeight: 400 }}>{debug.system_prompt?.length} chars</span></div>{showPrompt && <div style={body}>{debug.system_prompt}</div>}</div>
      <div style={sec}><div style={hdr} onClick={() => setShowMsgs(o => !o)}><span>{showMsgs ? '▾' : '▸'}</span> Messages Sent <span style={{ marginLeft: 'auto', fontWeight: 400 }}>{debug.messages_sent?.length ?? 0}</span></div>{showMsgs && <div style={body}>{(debug.messages_sent ?? []).map((m, i) => <div key={i} style={{ marginBottom: 6 }}><b style={{ color: m.role === 'user' ? '#0ea5e9' : '#a78bfa' }}>[{m.role}]</b> {m.content}</div>)}</div>}</div>
      {debug.raw_llm_response && <div style={sec}><div style={hdr} onClick={() => setShowRaw(o => !o)}><span>{showRaw ? '▾' : '▸'}</span> Raw LLM Response <span style={{ marginLeft: 'auto', fontWeight: 400 }}>{debug.raw_llm_response.length} chars</span></div>{showRaw && <div style={body}>{debug.raw_llm_response}</div>}</div>}
    </div>
  )
}

// ── Main Component ────────────────────────────────────────────────────────────

export default function GlobalAIAssistant({ currentTab }: { currentTab: string }) {
  const [open, setOpen] = useState(false)
  const [messages, setMessages] = useState<ChatMsg[]>([])
  const [input, setInput] = useState('')
  const [thinking, setThinking] = useState(false)
  const [model, setModel] = useState('')
  const [models, setModels] = useState<LLMModel[]>([])
  const [showDebug, setShowDebug] = useState(false)
  const [discarded, setDiscarded] = useState<Set<string>>(new Set())
  const chatEndRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    listLLMModels()
      .then(ms => { setModels(ms); if (ms.length > 0) setModel(prev => prev || ms[0].alias) })
      .catch(() => {})
  }, [])

  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // ── Sync helper ────────────────────────────────────────────────────────────
  async function postSync(flows: unknown[], apis: unknown[]) {
    const res = await fetch('/api/sync', {
      method: 'POST', credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sync_uuid: `ai-${Date.now()}`, flows, apis }),
    })
    if (!res.ok) throw new Error(await res.text().catch(() => `HTTP ${res.status}`))
  }

  // ── Apply single action ────────────────────────────────────────────────────
  async function applyAction(mi: number, ai: number, action: ChatAction) {
    try {
      if (action.op === 'upsert_flow') {
        let instructions: unknown[]
        try { instructions = JSON.parse(action.dsl ?? '[]') } catch { throw new Error('Invalid flow DSL') }
        await postSync([{ name: action.name ?? '', instructions, action: 'upsert' }], [])
      } else if (action.op === 'add_endpoint') {
        await postSync([], [{ name: action.api ?? '', path: action.path ?? '/', method: action.method ?? 'ANY', flow_name: action.flow ?? '', action: 'upsert' }])
      } else if (action.op === 'upsert_api') {
        // no-op — API created implicitly when endpoints are synced
      } else if (action.op === 'upsert_tenant') {
        const r = await fetch('/api/tenants', { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ alias: action.name ?? '' }) })
        if (!r.ok) throw new Error(await r.text().catch(() => `HTTP ${r.status}`))
      } else if (action.op === 'publish') {
        const r = await fetch('/api/deploy', { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ target: action.target ?? '' }) })
        if (!r.ok) throw new Error(await r.text().catch(() => `HTTP ${r.status}`))
      } else {
        throw new Error(`Unknown op: ${action.op}`)
      }
      setMessages(prev => prev.map((m, i) => {
        if (i !== mi) return m
        const next = new Set(m.appliedActions); next.add(ai)
        return { ...m, appliedActions: next }
      }))
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setMessages(prev => prev.map((m, i) => i !== mi ? m : { ...m, failedActions: { ...m.failedActions, [ai]: msg } }))
    }
  }

  // ── Apply all pending ──────────────────────────────────────────────────────
  async function applyAll(mi: number) {
    const msg = messages[mi]
    if (!msg?.response) return
    const pending = msg.response.actions.map((a, i) => ({ a, i }))
      .filter(({ i }) => !msg.appliedActions.has(i) && !discarded.has(`${mi}:${i}`))
    if (pending.length === 0) return
    const flows: unknown[] = []
    const apis: unknown[] = []
    const noOps: number[] = []
    for (const { a, i } of pending) {
      if (a.op === 'upsert_flow') {
        let instructions: unknown[]; try { instructions = JSON.parse(a.dsl ?? '[]') } catch { instructions = [] }
        flows.push({ name: a.name ?? '', instructions, action: 'upsert' })
      } else if (a.op === 'add_endpoint') {
        apis.push({ name: a.api ?? '', path: a.path ?? '/', method: a.method ?? 'ANY', flow_name: a.flow ?? '', action: 'upsert' })
      } else { noOps.push(i) }
    }
    try {
      if (flows.length > 0 || apis.length > 0) await postSync(flows, apis)
      setMessages(prev => prev.map((m, idx) => {
        if (idx !== mi) return m
        const next = new Set(m.appliedActions)
        pending.forEach(({ i }) => next.add(i))
        noOps.forEach(i => next.add(i))
        return { ...m, appliedActions: next }
      }))
    } catch (err: unknown) {
      const errMsg = err instanceof Error ? err.message : String(err)
      setMessages(prev => prev.map((m, idx) => {
        if (idx !== mi) return m
        const failed = { ...m.failedActions }
        pending.forEach(({ i }) => { failed[i] = errMsg })
        return { ...m, failedActions: failed }
      }))
    }
  }

  // ── Send ───────────────────────────────────────────────────────────────────
  async function send(text?: string) {
    const content = (text ?? input).trim()
    if (!content || thinking) return
    const history = messages.slice(-8).map(m => ({ role: m.role, content: m.content }))
    setMessages(prev => [...prev, { role: 'user', content, appliedActions: new Set(), failedActions: {} }])
    setInput('')
    if (textareaRef.current) textareaRef.current.style.height = 'auto'
    setThinking(true)
    try {
      const res = await fetch('/api/ai/chat', {
        method: 'POST', credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          message: content, model,
          include_apis: true, include_flows: true,
          session_history: history,
          context_hint: currentTab,
        }),
      })
      const ct = res.headers.get('content-type') ?? ''
      const data = ct.includes('application/json') ? await res.json() : { error: await res.text() }
      if (!res.ok) {
        setMessages(prev => [...prev, { role: 'assistant', content: (data.error as string) || 'Error', appliedActions: new Set(), failedActions: {} }])
      } else {
        setMessages(prev => [...prev, {
          role: 'assistant',
          content: (data.confirm_message as string) ?? '',
          response: data as ChatMsg['response'],
          appliedActions: new Set(),
          failedActions: {},
        }])
      }
    } catch (e: unknown) {
      setMessages(prev => [...prev, { role: 'assistant', content: 'Network error: ' + (e instanceof Error ? e.message : String(e)), appliedActions: new Set(), failedActions: {} }])
    } finally {
      setThinking(false)
    }
  }

  function handleKey(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && e.ctrlKey) { e.preventDefault(); send() }
  }

  function handleInput(e: React.ChangeEvent<HTMLTextAreaElement>) {
    setInput(e.target.value)
    const el = textareaRef.current
    if (el) { el.style.height = 'auto'; el.style.height = Math.min(el.scrollHeight, 120) + 'px' }
  }

  function submitDiscoveryAnswers(mi: number, questions: string[], answers: string[]) {
    const formatted = questions.map((q, i) => `${q}\n→ ${answers[i] || '(no preference)'}`).join('\n\n')
    send(formatted)
  }

  // ── Render ─────────────────────────────────────────────────────────────────
  return (
    <>
      {/* ── Floating toggle button ── */}
      <button
        onClick={() => setOpen(o => !o)}
        title={open ? 'Close AI Assistant' : 'Open AI Assistant'}
        style={{
          position: 'fixed', bottom: 24, right: 24, zIndex: 1000,
          width: 48, height: 48, borderRadius: '50%',
          border: 'none', cursor: 'pointer',
          background: open ? 'var(--panel)' : 'var(--accent)',
          color: open ? 'var(--muted)' : '#031427',
          fontSize: 18, fontWeight: 800,
          boxShadow: '0 4px 16px rgba(0,0,0,0.3)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          transition: 'background 0.2s, color 0.2s',
        }}
      >
        {open ? '✕' : '✦'}
      </button>

      {/* ── Drawer ── */}
      {open && (
        <div style={{
          position: 'fixed', right: 0, top: 0, bottom: 0,
          width: 420, zIndex: 999,
          background: 'var(--panel)',
          borderLeft: '1px solid var(--border)',
          boxShadow: '-4px 0 24px rgba(0,0,0,0.2)',
          display: 'flex', flexDirection: 'column',
        }}>
          {/* Header */}
          <div style={{ padding: '10px 14px', borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', gap: 10, flexShrink: 0 }}>
            <span style={{ fontSize: 14, fontWeight: 700 }}>✦ AI Assistant</span>
            <span style={{ fontSize: 10, color: 'var(--muted)', background: 'var(--step-bg)', padding: '2px 7px', borderRadius: 8 }}>
              {currentTab}
            </span>
            <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 8 }}>
              {/* Model selector */}
              {models.length > 0 && (
                <select
                  value={model}
                  onChange={e => setModel(e.target.value)}
                  style={{ fontSize: 11, padding: '2px 6px', borderRadius: 4, border: '1px solid var(--border)', background: 'var(--step-bg)', color: 'var(--text)', cursor: 'pointer' }}
                >
                  {models.map(m => <option key={m.alias} value={m.alias}>{m.alias}</option>)}
                </select>
              )}
              {/* Debug toggle */}
              <button
                onClick={() => setShowDebug(d => !d)}
                title="Toggle debug panel"
                style={{ fontSize: 10, padding: '2px 8px', borderRadius: 4, border: '1px solid var(--border)', background: showDebug ? '#7c3aed' : 'var(--step-bg)', color: showDebug ? '#fff' : 'var(--muted)', cursor: 'pointer' }}
              >
                {showDebug ? 'Debug ON' : 'Debug'}
              </button>
            </div>
          </div>

          {/* Messages */}
          <div style={{ flex: 1, overflowY: 'auto', padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 10 }}>
            {messages.length === 0 && (
              <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', color: 'var(--muted)', gap: 6, paddingTop: 60 }}>
                <div style={{ fontSize: 28 }}>✦</div>
                <div style={{ fontSize: 13, fontWeight: 600 }}>AI Assistant</div>
                <div style={{ fontSize: 12, textAlign: 'center', maxWidth: 300, lineHeight: 1.5 }}>
                  Describe what you want to build — flows, APIs, routes — and I'll guide you through it.
                </div>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4 }}>Ctrl+Enter to send</div>
              </div>
            )}

            {messages.map((msg, mi) => (
              <div key={mi} style={{ display: 'flex', flexDirection: 'column', alignItems: msg.role === 'user' ? 'flex-end' : 'flex-start' }}>
                {msg.role === 'user' ? (
                  <div style={{ maxWidth: '80%', background: '#1e293b', borderRadius: 10, padding: '8px 12px', fontSize: 13, color: 'var(--text)', lineHeight: 1.5, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                    {msg.content}
                  </div>
                ) : (
                  <div style={{ width: '100%' }}>
                    {/* Phase badge */}
                    {msg.response?.phase && msg.response.phase !== 'execute' && (
                      <div style={{ marginBottom: 5 }}>
                        <span style={{
                          fontSize: 9, fontWeight: 800, padding: '1px 7px', borderRadius: 3, letterSpacing: 0.5,
                          background: msg.response.phase === 'discovery' ? '#f59e0b' : '#0ea5e9',
                          color: '#fff',
                        }}>
                          {msg.response.phase === 'discovery' ? '● QUESTIONS' : '● PLAN'}
                        </span>
                      </div>
                    )}

                    {/* Confirm message */}
                    <div style={{ fontSize: 13, color: 'var(--text)', lineHeight: 1.6, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                      {msg.response?.confirm_message ?? msg.content}
                    </div>

                    {/* Plan preview */}
                    {msg.response?.plan && <PlanPreview plan={msg.response.plan} />}

                    {/* Discovery form with inline answer boxes */}
                    {msg.response?.phase === 'discovery' && msg.response.questions.length > 0 && mi === messages.length - 1 && !thinking && (
                      <DiscoveryForm
                        questions={msg.response.questions}
                        suggested={msg.response.suggested_answers ?? []}
                        onSubmit={(answers) => submitDiscoveryAnswers(mi, msg.response!.questions, answers)}
                        disabled={thinking}
                      />
                    )}

                    {/* After-discovery questions already answered (non-last or plan phase) */}
                    {msg.response?.questions && msg.response.questions.length > 0 &&
                     (msg.response.phase !== 'discovery' || mi !== messages.length - 1) && (
                      <div style={{ marginTop: 8, padding: '8px 12px', borderRadius: 8, border: `1px solid ${msg.response.phase === 'plan' ? '#0ea5e9' : '#ca8a04'}`, background: msg.response.phase === 'plan' ? 'rgba(14,165,233,0.06)' : 'rgba(202,138,4,0.08)' }}>
                        <ul style={{ margin: 0, paddingLeft: 16, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          {msg.response.questions.map((q, qi) => <li key={qi} style={{ fontSize: 12, color: 'var(--text)' }}>{q}</li>)}
                        </ul>
                        {msg.response.phase === 'plan' && mi === messages.length - 1 && !thinking && (
                          <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                            <button onClick={() => send('Yes, looks good. Please proceed.')}
                              style={{ padding: '4px 14px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 11, fontWeight: 700, background: '#22c55e', color: '#fff' }}>
                              ✓ Proceed
                            </button>
                            <button onClick={() => { setInput('I want to change: '); textareaRef.current?.focus() }}
                              style={{ padding: '4px 12px', borderRadius: 6, border: '1px solid var(--border)', cursor: 'pointer', fontSize: 11, fontWeight: 600, background: 'var(--step-bg)', color: 'var(--muted)' }}>
                              ✎ Adjust
                            </button>
                          </div>
                        )}
                      </div>
                    )}

                    {/* Actions */}
                    {msg.response && msg.response.actions.length > 0 && (() => {
                      const pending = msg.response.actions.filter((_, ai) =>
                        !msg.appliedActions.has(ai) && !discarded.has(`${mi}:${ai}`)
                      ).length
                      return (
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
                          {pending >= 2 && (
                            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 8, padding: '6px 10px', background: 'var(--panel)', borderRadius: 8, border: '1px solid var(--border)' }}>
                              <span style={{ fontSize: 11, color: 'var(--muted)' }}>{pending} items ready</span>
                              <button onClick={() => applyAll(mi)}
                                style={{ padding: '3px 14px', borderRadius: 5, border: 'none', cursor: 'pointer', fontSize: 11, fontWeight: 700, background: 'var(--accent)', color: '#031427' }}>
                                Apply All
                              </button>
                            </div>
                          )}
                          {msg.response.actions.map((action, ai) => {
                            if (discarded.has(`${mi}:${ai}`)) return null
                            const isApplied = msg.appliedActions.has(ai)
                            const failMsg = msg.failedActions[ai]
                            const [showDSL, setShowDSL] = useState(false)
                            const label = action.name || action.api || action.path || ''
                            return (
                              <div key={ai} style={{ border: '1px solid var(--border)', borderRadius: 8, marginTop: 6, overflow: 'hidden', background: 'var(--step-bg)' }}>
                                <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '6px 10px', borderBottom: '1px solid var(--border)' }}>
                                  <span style={{ fontSize: 9, fontWeight: 800, padding: '1px 6px', borderRadius: 3, background: opColor(action.op), color: '#fff', letterSpacing: 0.5 }}>
                                    {action.op.toUpperCase()}
                                  </span>
                                  {label && <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text)', fontFamily: 'monospace' }}>{label}</span>}
                                </div>
                                {action.description && (
                                  <div style={{ padding: '6px 10px', fontSize: 12, color: 'var(--text)', lineHeight: 1.5 }}>{action.description}</div>
                                )}
                                {action.dsl && (
                                  <div style={{ padding: '0 10px 4px' }}>
                                    <button onClick={() => setShowDSL(v => !v)}
                                      style={{ fontSize: 10, color: 'var(--muted)', background: 'none', border: 'none', cursor: 'pointer', padding: '2px 0' }}>
                                      {showDSL ? '▾ Hide DSL' : '▸ Show DSL'}
                                    </button>
                                    {showDSL && (
                                      <pre style={{ margin: '3px 0 0', padding: '6px 8px', fontFamily: 'monospace', fontSize: 10, overflowX: 'auto', maxHeight: 160, whiteSpace: 'pre-wrap', wordBreak: 'break-all', color: 'var(--text)', background: 'var(--block-bg)', borderRadius: 5, border: '1px solid var(--border)' }}>
                                        {action.dsl}
                                      </pre>
                                    )}
                                  </div>
                                )}
                                <div style={{ padding: '6px 10px', display: 'flex', alignItems: 'center', gap: 6 }}>
                                  {!isApplied && failMsg === undefined && (
                                    <>
                                      <button onClick={() => applyAction(mi, ai, action)}
                                        style={{ padding: '3px 12px', borderRadius: 5, border: 'none', cursor: 'pointer', fontSize: 11, fontWeight: 700, background: '#22c55e', color: '#fff' }}>
                                        Apply
                                      </button>
                                      <button onClick={() => setDiscarded(prev => { const n = new Set(prev); n.add(`${mi}:${ai}`); return n })}
                                        style={{ padding: '3px 10px', borderRadius: 5, border: 'none', cursor: 'pointer', fontSize: 11, fontWeight: 600, background: 'var(--step-bg)', color: 'var(--muted)' }}>
                                        Discard
                                      </button>
                                    </>
                                  )}
                                  {isApplied && <span style={{ fontSize: 11, color: '#22c55e', fontWeight: 600 }}>✓ Applied</span>}
                                  {!isApplied && failMsg && <span style={{ fontSize: 11, color: '#ef4444', fontWeight: 600 }}>✗ {failMsg}</span>}
                                </div>
                              </div>
                            )
                          })}
                        </div>
                      )
                    })()}

                    {/* Debug panel */}
                    {showDebug && msg.response?.debug && <DebugPanel debug={msg.response.debug} />}
                  </div>
                )}
              </div>
            ))}

            {thinking && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, color: 'var(--muted)', fontSize: 13 }}>
                <span style={{ animation: 'pulse 1s ease-in-out infinite' }}>●</span> Thinking…
              </div>
            )}
            <div ref={chatEndRef} />
          </div>

          {/* Input bar */}
          <div style={{ borderTop: '1px solid var(--border)', padding: '10px 14px', display: 'flex', alignItems: 'flex-end', gap: 8, flexShrink: 0, background: 'var(--step-bg)' }}>
            <textarea
              ref={textareaRef}
              value={input}
              onChange={handleInput}
              onKeyDown={handleKey}
              placeholder="Describe what you want to build… (Ctrl+Enter)"
              rows={1}
              style={{ flex: 1, resize: 'none', border: '1px solid var(--border)', borderRadius: 8, padding: '7px 10px', fontSize: 13, lineHeight: 1.5, background: 'transparent', color: 'var(--text)', outline: 'none', fontFamily: 'inherit', minHeight: 34, maxHeight: 120, overflow: 'hidden' }}
            />
            <button
              onClick={() => send()}
              disabled={thinking || !input.trim()}
              style={{ padding: '7px 16px', borderRadius: 8, border: 'none', cursor: thinking || !input.trim() ? 'not-allowed' : 'pointer', fontSize: 13, fontWeight: 700, background: thinking || !input.trim() ? 'var(--step-bg)' : 'var(--accent)', color: thinking || !input.trim() ? 'var(--muted)' : '#031427', transition: 'background 0.15s', whiteSpace: 'nowrap' }}
            >
              {thinking ? '…' : 'Send'}
            </button>
          </div>
        </div>
      )}
    </>
  )
}
