import { useState, useEffect } from 'react'
import { listLLMModels, syncFlows } from '../api'
import type { SyncStep } from '../api'
import type { LLMModel } from '../types'

// ── Types ────────────────────────────────────────────────────────────────────

interface RoutingRule {
  id: string
  operator: '>' | '<' | '>=' | '<='
  threshold: number
  model: string
}

interface ProxyConfig {
  endpoint: string
  inputField: string
  model: string
  fallbackChain: string[]
  systemPrompt: string
  maxTokens: string
  temperature: string
}

interface RouterConfig {
  endpoint: string
  inputField: string
  systemPrompt: string
  routingRules: RoutingRule[]
  defaultModel: string
  fallbackChain: string[]
  historyEnabled: boolean
  sessionHeader: string
  maxTurns: string
}

type Mode = 'proxy' | 'router'

// ── Step builders ────────────────────────────────────────────────────────────

function buildProxySteps(cfg: ProxyConfig): SyncStep[] {
  const steps: SyncStep[] = []

  // 1. Bind request body field → prompt slot
  steps.push({ action: 'bind_body', key: cfg.inputField || 'message', as: 'var.prompt' })

  // 2. Set system prompt as constant (if provided)
  if (cfg.systemPrompt.trim()) {
    steps.push({ action: 'set_const', value: cfg.systemPrompt.trim(), as: 'var.system' })
  }

  // 3. LLM call
  const llmInput: Record<string, string> = {
    model: cfg.model,
    max_tokens: cfg.maxTokens || '2000',
    temperature: cfg.temperature || '0.7',
  }
  if (cfg.systemPrompt.trim()) llmInput.system_slot = 'var.system'
  if (cfg.fallbackChain.length > 0) llmInput.fallback_chain = cfg.fallbackChain.join(',')

  steps.push({
    action: 'llm_call',
    key_identifier: 'var.prompt',
    as: 'var.reply',
    input: llmInput,
  })

  // 4. Respond
  steps.push({ action: 'respond', key_identifier: 'var.reply' })

  return steps
}

function buildRouterSteps(cfg: RouterConfig): SyncStep[] {
  const steps: SyncStep[] = []

  // 1. Bind request body field → prompt slot
  steps.push({ action: 'bind_body', key: cfg.inputField || 'message', as: 'var.prompt' })

  // 2. System prompt constant (if provided)
  if (cfg.systemPrompt.trim()) {
    steps.push({ action: 'set_const', value: cfg.systemPrompt.trim(), as: 'var.system' })
  }

  // 3. Bind session header for history
  if (cfg.historyEnabled) {
    steps.push({ action: 'bind_header', key: cfg.sessionHeader || 'X-Session-Id', as: 'var.session' })
    steps.push({ action: 'load_history', key_identifier: 'var.session', as: 'var.history' })
  }

  // 4. Check context fit (estimates total token count including history)
  const ctxInput: Record<string, string> = { model: cfg.defaultModel, total_slot: 'var.total_tok' }
  if (cfg.historyEnabled) ctxInput.history_slot = 'var.history'
  steps.push({
    action: 'check_context_fit',
    key_identifier: 'var.prompt',
    as: 'var.ctx_fits',
    input: ctxInput,
  })

  // 5. Route LLM — build rules JSON from routing rule table
  // Rules are evaluated in order; first match wins.
  // We generate: [{condition:"token_count > N","model":"alias"}, ..., {condition:"true","model":"default"}]
  const rulesArr = cfg.routingRules.map(r => ({
    condition: `token_count ${r.operator} ${r.threshold}`,
    model: r.model,
  }))
  // Always append a catch-all "true" rule for the default model
  rulesArr.push({ condition: 'true', model: cfg.defaultModel })

  steps.push({
    action: 'route_llm',
    as: 'var.model',
    input: {
      rules: JSON.stringify(rulesArr),
      default: cfg.defaultModel,
      token_slot: 'var.total_tok',
    },
  })

  // 6. LLM call — uses the model selected by route_llm (model_slot), with fallback
  const llmInput: Record<string, string> = {
    model: cfg.defaultModel,        // baked fallback (route_llm overrides at runtime)
    model_slot: 'var.model',        // runtime model from route_llm
    max_tokens: '2000',
  }
  if (cfg.systemPrompt.trim()) llmInput.system_slot = 'var.system'
  if (cfg.historyEnabled) llmInput.history_slot = 'var.history'
  if (cfg.fallbackChain.length > 0) llmInput.fallback_chain = cfg.fallbackChain.join(',')

  steps.push({
    action: 'llm_call',
    key_identifier: 'var.prompt',
    as: 'var.reply',
    input: llmInput,
  })

  // 7. Persist history
  if (cfg.historyEnabled) {
    steps.push({
      action: 'append_message',
      key_identifier: 'var.prompt',
      as: 'var.history',
      input: { role: 'user', max_turns: cfg.maxTurns || '20' },
    })
    steps.push({
      action: 'append_message',
      key_identifier: 'var.reply',
      as: 'var.history',
      input: { role: 'assistant' },
    })
    steps.push({ action: 'save_history', key_identifier: 'var.session', source: 'var.history' })
  }

  // 8. Respond
  steps.push({ action: 'respond', key_identifier: 'var.reply' })

  return steps
}

// ── Sub-components ────────────────────────────────────────────────────────────

function ModelSelect({
  value,
  onChange,
  models,
  placeholder = '— select model —',
}: {
  value: string
  onChange: (v: string) => void
  models: LLMModel[]
  placeholder?: string
}) {
  return (
    <select className="input" value={value} onChange={e => onChange(e.target.value)}>
      <option value="">{placeholder}</option>
      {models.map(m => (
        <option key={m.alias} value={m.alias}>
          {m.alias} ({m.provider})
        </option>
      ))}
    </select>
  )
}

function FallbackChainEditor({
  chain,
  onChange,
  models,
}: {
  chain: string[]
  onChange: (c: string[]) => void
  models: LLMModel[]
}) {
  const [adding, setAdding] = useState('')
  const available = models.filter(m => !chain.includes(m.alias))

  function add() {
    if (adding && !chain.includes(adding)) {
      onChange([...chain, adding])
      setAdding('')
    }
  }

  return (
    <div>
      {/* Current chain */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8 }}>
        {chain.length === 0 && (
          <span style={{ fontSize: 12, color: 'var(--muted)' }}>No fallback — add models below</span>
        )}
        {chain.map((alias, i) => (
          <div key={alias} style={{
            display: 'flex', alignItems: 'center', gap: 5,
            background: 'var(--step-bg)', border: '1px solid var(--border-hi)',
            borderRadius: 6, padding: '3px 10px', fontSize: 12,
          }}>
            <span style={{ color: 'var(--muted)', marginRight: 2 }}>{i + 1}.</span>
            <span style={{ fontWeight: 600 }}>{alias}</span>
            <button
              type="button"
              onClick={() => onChange(chain.filter((_, j) => j !== i))}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 14, lineHeight: 1, padding: '0 2px' }}
            >×</button>
          </div>
        ))}
      </div>
      {/* Add picker */}
      <div style={{ display: 'flex', gap: 6 }}>
        <select className="input" style={{ flex: 1 }} value={adding} onChange={e => setAdding(e.target.value)}>
          <option value="">+ add fallback model…</option>
          {available.map(m => <option key={m.alias} value={m.alias}>{m.alias} ({m.provider})</option>)}
        </select>
        <button className="btn" type="button" onClick={add} disabled={!adding}
          style={{ width: 'auto', padding: '0 14px', flexShrink: 0, opacity: adding ? 1 : 0.4 }}>
          Add
        </button>
      </div>
    </div>
  )
}

function RoutingRuleRow({
  rule,
  onChange,
  onRemove,
  models,
}: {
  rule: RoutingRule
  onChange: (r: RoutingRule) => void
  onRemove: () => void
  models: LLMModel[]
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
      <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>token_count</span>
      <select className="input" value={rule.operator}
        onChange={e => onChange({ ...rule, operator: e.target.value as RoutingRule['operator'] })}
        style={{ width: 56, flexShrink: 0, padding: '5px 4px' }}>
        <option value=">">&gt;</option>
        <option value=">=">&gt;=</option>
        <option value="<">&lt;</option>
        <option value="<=">&lt;=</option>
      </select>
      <input type="number" className="input" value={rule.threshold} min={0}
        onChange={e => onChange({ ...rule, threshold: parseInt(e.target.value) || 0 })}
        style={{ width: 80, flexShrink: 0 }} />
      <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>→</span>
      <div style={{ flex: 1 }}>
        <ModelSelect value={rule.model} onChange={v => onChange({ ...rule, model: v })} models={models} />
      </div>
      <button type="button" onClick={onRemove}
        style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 18, lineHeight: 1, flexShrink: 0 }}>
        ×
      </button>
    </div>
  )
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ fontSize: 11, fontWeight: 700, letterSpacing: 0.5, color: 'var(--muted)',
      textTransform: 'uppercase', marginTop: 18, marginBottom: 8, paddingBottom: 4,
      borderBottom: '1px solid var(--border)' }}>
      {children}
    </div>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 4 }}>{label}</div>
      {children}
      {hint && <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 3 }}>{hint}</div>}
    </div>
  )
}

function StepPreview({ steps }: { steps: SyncStep[] }) {
  return (
    <div style={{ background: 'var(--input-bg)', border: '1px solid var(--border-hi)',
      borderRadius: 6, padding: '10px 12px', maxHeight: 200, overflowY: 'auto' }}>
      {steps.map((s, i) => (
        <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 4, fontSize: 12, fontFamily: 'monospace' }}>
          <span style={{ color: 'var(--muted)', minWidth: 18, textAlign: 'right', flexShrink: 0 }}>{i + 1}</span>
          <span style={{ color: 'var(--accent)', fontWeight: 700, flexShrink: 0 }}>{s.action}</span>
          <span style={{ color: 'var(--muted)', wordBreak: 'break-all' }}>
            {[
              s.key_identifier && `from:${s.key_identifier}`,
              s.as && `→ ${s.as}`,
              s.key && `key:${s.key}`,
              s.input?.model && `model:${s.input.model}`,
              s.input?.model_slot && `model_slot:${s.input.model_slot}`,
              s.input?.fallback_chain && `fallback:${s.input.fallback_chain}`,
              s.input?.history_slot && `history:${s.input.history_slot}`,
              s.input?.role && `role:${s.input.role}`,
            ].filter(Boolean).join('  ')}
          </span>
        </div>
      ))}
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────────────

const DEFAULT_PROXY: ProxyConfig = {
  endpoint: '/chat',
  inputField: 'message',
  model: '',
  fallbackChain: [],
  systemPrompt: 'You are a helpful assistant.',
  maxTokens: '2000',
  temperature: '0.7',
}

const DEFAULT_ROUTER: RouterConfig = {
  endpoint: '/ai/chat',
  inputField: 'message',
  systemPrompt: 'You are a helpful assistant.',
  routingRules: [
    { id: '1', operator: '>', threshold: 8000, model: '' },
    { id: '2', operator: '>', threshold: 2000, model: '' },
  ],
  defaultModel: '',
  fallbackChain: [],
  historyEnabled: false,
  sessionHeader: 'X-Session-Id',
  maxTurns: '20',
}

export default function AIRoutes() {
  const [mode, setMode] = useState<Mode>('proxy')
  const [proxy, setProxy] = useState<ProxyConfig>(DEFAULT_PROXY)
  const [router, setRouter] = useState<RouterConfig>(DEFAULT_ROUTER)
  const [flowName, setFlowName] = useState('my-llm-proxy')
  const [models, setModels] = useState<LLMModel[]>([])
  const [loadingModels, setLoadingModels] = useState(true)

  const [deploying, setDeploying] = useState(false)
  const [deployResult, setDeployResult] = useState<{ ok: boolean; msg: string } | null>(null)

  useEffect(() => {
    listLLMModels()
      .then(setModels)
      .catch(() => {})
      .finally(() => setLoadingModels(false))
  }, [])

  useEffect(() => {
    setFlowName(mode === 'proxy' ? 'my-llm-proxy' : 'my-smart-router')
    setDeployResult(null)
  }, [mode])

  function addRoutingRule() {
    setRouter(r => ({
      ...r,
      routingRules: [...r.routingRules, { id: Date.now().toString(), operator: '>', threshold: 1000, model: '' }],
    }))
  }

  function updateRoutingRule(id: string, updated: RoutingRule) {
    setRouter(r => ({ ...r, routingRules: r.routingRules.map(rr => rr.id === id ? updated : rr) }))
  }

  function removeRoutingRule(id: string) {
    setRouter(r => ({ ...r, routingRules: r.routingRules.filter(rr => rr.id !== id) }))
  }

  async function handleDeploy() {
    const name = flowName.trim()
    if (!name) return
    const endpoint = (mode === 'proxy' ? proxy.endpoint : router.endpoint).trim()
    if (!endpoint) return

    const steps = mode === 'proxy' ? buildProxySteps(proxy) : buildRouterSteps(router)

    const payload = {
      sync_uuid: Math.random().toString(36).slice(2),
      flows: [{ name, instructions: steps, action: 'upsert' as const }],
      apis: [{ name: name + '-api', path: endpoint, flow_name: name, action: 'upsert' as const }],
    }

    setDeploying(true)
    setDeployResult(null)
    try {
      await syncFlows(payload)
      setDeployResult({ ok: true, msg: `Deployed! Call: POST ${endpoint}  (body: {"${mode === 'proxy' ? proxy.inputField : router.inputField}": "..."})` })
    } catch (e) {
      setDeployResult({ ok: false, msg: String(e) })
    } finally {
      setDeploying(false)
    }
  }

  const steps = mode === 'proxy' ? buildProxySteps(proxy) : buildRouterSteps(router)
  const canDeploy = flowName.trim().length > 0 &&
    (mode === 'proxy' ? proxy.endpoint.trim() && proxy.model : router.endpoint.trim() && router.defaultModel)

  // ── Render ─────────────────────────────────────────────────────────

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>

      {/* ── Mode picker ── */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
        {([
          {
            id: 'proxy' as Mode,
            icon: '⚡',
            title: 'LLM Proxy',
            subtitle: 'Simple AI endpoint',
            desc: 'Route requests to one model with optional fallback chain. Minimal setup — pick a model and go.',
          },
          {
            id: 'router' as Mode,
            icon: '🔀',
            title: 'Smart Router',
            subtitle: 'Context-aware routing + history',
            desc: 'Route to different models based on token count. Optionally load/save conversation history per session.',
          },
        ] as const).map(card => (
          <div key={card.id} onClick={() => setMode(card.id)} style={{
            background: mode === card.id ? 'rgba(87,181,255,0.08)' : 'var(--block-bg)',
            border: mode === card.id ? '2px solid var(--accent)' : '1px solid var(--border-hi)',
            borderRadius: 10, padding: '14px 16px', cursor: 'pointer',
            transition: 'background 0.15s, border-color 0.15s', userSelect: 'none',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 6 }}>
              <span style={{ fontSize: 20 }}>{card.icon}</span>
              <div>
                <div style={{ fontWeight: 700, fontSize: 14, color: mode === card.id ? 'var(--accent)' : 'var(--text)' }}>{card.title}</div>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 1 }}>{card.subtitle}</div>
              </div>
            </div>
            <div style={{ fontSize: 12, color: 'var(--muted)', lineHeight: 1.55 }}>{card.desc}</div>
          </div>
        ))}
      </div>

      {loadingModels && (
        <div style={{ fontSize: 12, color: 'var(--muted)' }}>Loading models…</div>
      )}
      {!loadingModels && models.length === 0 && (
        <div className="status-err" style={{ fontSize: 12 }}>
          No models registered yet. Go to the Models tab to add LLM models first.
        </div>
      )}

      {/* ── Config form ── */}
      <div className="panel">
        <div className="panel-header">
          {mode === 'proxy' ? 'LLM Proxy Configuration' : 'Smart Router Configuration'}
        </div>
        <div className="panel-body">

          {/* Endpoint + input field — shared */}
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10 }}>
            <Field label="Endpoint path" hint="URL path clients will POST to">
              <input className="input" value={mode === 'proxy' ? proxy.endpoint : router.endpoint}
                onChange={e => mode === 'proxy' ? setProxy(p => ({ ...p, endpoint: e.target.value })) : setRouter(r => ({ ...r, endpoint: e.target.value }))}
                placeholder="/chat" />
            </Field>
            <Field label="Body field" hint="JSON key for the user message">
              <input className="input" value={mode === 'proxy' ? proxy.inputField : router.inputField}
                onChange={e => mode === 'proxy' ? setProxy(p => ({ ...p, inputField: e.target.value })) : setRouter(r => ({ ...r, inputField: e.target.value }))}
                placeholder="message" />
            </Field>
          </div>

          <Field label="System prompt">
            <textarea className="input" rows={2}
              value={mode === 'proxy' ? proxy.systemPrompt : router.systemPrompt}
              onChange={e => mode === 'proxy' ? setProxy(p => ({ ...p, systemPrompt: e.target.value })) : setRouter(r => ({ ...r, systemPrompt: e.target.value }))}
              placeholder="You are a helpful assistant." />
          </Field>

          {/* ── PROXY specific ── */}
          {mode === 'proxy' && (<>
            <SectionLabel>Model</SectionLabel>

            <Field label="Primary model" hint="Called on every request.">
              <ModelSelect value={proxy.model} onChange={v => setProxy(p => ({ ...p, model: v }))} models={models} />
            </Field>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <Field label="Max tokens">
                <input className="input" type="number" value={proxy.maxTokens} min={1}
                  onChange={e => setProxy(p => ({ ...p, maxTokens: e.target.value }))} />
              </Field>
              <Field label="Temperature">
                <input className="input" type="number" value={proxy.temperature} min={0} max={2} step={0.1}
                  onChange={e => setProxy(p => ({ ...p, temperature: e.target.value }))} />
              </Field>
            </div>

            <SectionLabel>Fallback chain <span style={{ fontWeight: 400 }}>(tried in order on failure)</span></SectionLabel>
            <FallbackChainEditor chain={proxy.fallbackChain} onChange={c => setProxy(p => ({ ...p, fallbackChain: c }))} models={models} />
          </>)}

          {/* ── ROUTER specific ── */}
          {mode === 'router' && (<>
            <SectionLabel>Routing rules <span style={{ fontWeight: 400 }}>— evaluated in order, first match wins</span></SectionLabel>

            <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
              Token count is estimated from prompt + history (if enabled). Each rule routes to a specific model when its condition matches.
            </div>

            {router.routingRules.map(rule => (
              <RoutingRuleRow key={rule.id} rule={rule} models={models}
                onChange={updated => updateRoutingRule(rule.id, updated)}
                onRemove={() => removeRoutingRule(rule.id)} />
            ))}

            {/* Default (catch-all) row */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 10 }}>
              <span style={{ fontSize: 12, color: 'var(--muted)', minWidth: 120 }}>default (no match)</span>
              <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>→</span>
              <div style={{ flex: 1 }}>
                <ModelSelect value={router.defaultModel} onChange={v => setRouter(r => ({ ...r, defaultModel: v }))} models={models}
                  placeholder="— select default model —" />
              </div>
              <div style={{ width: 32 }} />
            </div>

            <button className="btn" type="button" onClick={addRoutingRule}
              style={{ width: 'auto', padding: '0 16px', marginBottom: 8, fontSize: 12 }}>
              + Add Rule
            </button>

            <SectionLabel>Fallback chain <span style={{ fontWeight: 400 }}>(on LLM error, try these in order)</span></SectionLabel>
            <FallbackChainEditor chain={router.fallbackChain} onChange={c => setRouter(r => ({ ...r, fallbackChain: c }))} models={models} />

            <SectionLabel>Conversation history</SectionLabel>

            <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10, cursor: 'pointer' }}>
              <input type="checkbox" checked={router.historyEnabled}
                onChange={e => setRouter(r => ({ ...r, historyEnabled: e.target.checked }))} />
              <span style={{ fontSize: 13 }}>Enable per-session conversation history</span>
            </label>

            {router.historyEnabled && (
              <div style={{ marginLeft: 24 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10, marginBottom: 8 }}>
                  <Field label="Session ID header" hint="Request header used as the history key">
                    <input className="input" value={router.sessionHeader}
                      onChange={e => setRouter(r => ({ ...r, sessionHeader: e.target.value }))}
                      placeholder="X-Session-Id" />
                  </Field>
                  <Field label="Max turns" hint="History turns to keep">
                    <input className="input" type="number" value={router.maxTurns} min={1}
                      onChange={e => setRouter(r => ({ ...r, maxTurns: e.target.value }))} />
                  </Field>
                </div>
                <div style={{ fontSize: 11, color: 'var(--muted)', padding: '6px 10px', background: 'var(--step-bg)', borderRadius: 5 }}>
                  Requires a datastore configured for history (Redis or PostgreSQL recommended).
                  Session key is read from <code>{router.sessionHeader || 'X-Session-Id'}</code> header.
                </div>
              </div>
            )}
          </>)}
        </div>
      </div>

      {/* ── Flow name + deploy ── */}
      <div className="panel">
        <div className="panel-header">Deploy</div>
        <div className="panel-body">

          <Field label="Flow name" hint="Internal identifier — lowercase, letters, digits, hyphens">
            <input className="input" value={flowName} onChange={e => { setFlowName(e.target.value); setDeployResult(null) }}
              placeholder="my-llm-proxy" />
          </Field>

          <div style={{ marginBottom: 14 }}>
            <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 6 }}>
              Generated flow — {steps.length} steps
            </div>
            <StepPreview steps={steps} />
          </div>

          <button className="btn" onClick={handleDeploy} disabled={deploying || !canDeploy}
            style={{ opacity: canDeploy && !deploying ? 1 : 0.45, cursor: canDeploy && !deploying ? 'pointer' : 'not-allowed' }}>
            {deploying ? 'Deploying…' : 'Deploy →'}
          </button>

          {!canDeploy && !deployResult && (
            <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 6 }}>
              {mode === 'proxy' ? 'Select a model and set an endpoint to deploy.' : 'Select a default model and set an endpoint to deploy.'}
            </div>
          )}

          {deployResult && (
            <div style={{
              marginTop: 12, padding: '10px 14px', borderRadius: 6, fontSize: 12,
              background: deployResult.ok ? 'rgba(34,197,94,0.07)' : 'rgba(239,68,68,0.07)',
              border: `1px solid ${deployResult.ok ? '#22c55e44' : '#ef444444'}`,
              color: deployResult.ok ? '#22c55e' : '#ef4444',
              fontFamily: deployResult.ok ? 'monospace' : 'inherit',
            }}>
              {deployResult.msg}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
