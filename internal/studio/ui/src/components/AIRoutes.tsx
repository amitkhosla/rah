import { useState, useEffect, useRef } from 'react'
import { listLLMModels, syncFlows, loadRouteConfigs, saveRouteConfigs, fetchGatewaySnapshot } from '../api'
import { reconstructRoute } from '../reconstructRoute'
import type { SyncStep } from '../api'
import type { LLMModel } from '../types'

// ── Types ─────────────────────────────────────────────────────────────────────

/** Tier-1: which classifier model to call, based on prompt token count */
interface ClassifierTierRule {
  id: string
  operator: '>' | '<' | '>=' | '<='
  threshold: number
  classifierModel: string   // empty = this slot is the catch-all default row
  fallbackChain: string[]   // tried if classifierModel fails (rate limit / 5xx)
}

/** Tier-2: which actual model to call, based on classifier output field value */
interface ModelTierRule {
  id: string
  conditionValue: string    // e.g. "low", "medium", "high"
  model: string
  fallbackChain: string[]
}

/** Token Router rule: route actual model by prompt token count directly */
interface RoutingRule {
  id: string
  operator: '>' | '<' | '>=' | '<='
  threshold: number
  model: string
  fallbackChain: string[]
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
  defaultFallback: string[]
  historyEnabled: boolean
  sessionHeader: string
  maxTurns: string
}

interface ClassifierRouterConfig {
  endpoint: string
  inputField: string

  // Tier 1 — classifier selection (which cheap model classifies this request)
  classifierSystemPrompt: string    // instructs classifier to return structured JSON
  classifierField: string           // JSON key to extract from classifier, e.g. "complexity"
  classifierRules: ClassifierTierRule[]
  defaultClassifierModel: string
  defaultClassifierFallback: string[]

  // Tier 2 — model selection (actual LLM based on classifier output)
  actualSystemPrompt: string        // optional system prompt for the actual model (persona, instructions)
  modelRules: ModelTierRule[]
  defaultModel: string
  defaultFallback: string[]

  // Classifier-driven conditional features
  classifierDecideHistory: boolean  // ask classifier if history is needed per-request
  classifierDecideTools: boolean    // ask classifier if tools are needed per-request

  // History (server-side, keyed by session header)
  historyEnabled: boolean
  sessionHeader: string
  maxTurns: string
}

type Mode = 'proxy' | 'router' | 'classifier'

// ── Helpers ───────────────────────────────────────────────────────────────────

function buildFallbackByModel(
  rules: Array<{ model: string; fallbackChain: string[] }>,
  defaultModel: string,
  defaultFallback: string[],
): Record<string, string[]> {
  const map: Record<string, string[]> = {}
  for (const r of rules) {
    if (r.model && r.fallbackChain.length > 0) map[r.model] = r.fallbackChain
  }
  if (defaultModel && defaultFallback.length > 0) map[defaultModel] = defaultFallback
  return map
}

/**
 * Builds the effective classifier system prompt by combining the user-entered
 * base prompt with auto-appended instructions for each enabled classifier feature.
 */
function buildEffectiveClassifierPrompt(cfg: ClassifierRouterConfig): string {
  const base = cfg.classifierSystemPrompt.trim()
  const additions: string[] = []

  if (cfg.classifierDecideHistory) {
    additions.push(
      `Only include "needs_history": "yes" if the request references prior turns, asks about "before", ` +
      `or clearly requires conversation context. Omit the field entirely if history is not needed.`
    )
  }
  if (cfg.classifierDecideTools) {
    additions.push(
      `Only include "needs_tools": "yes" if the request requires external tools (web search, ` +
      `calculations, live data, APIs, etc.). Omit the field entirely if tools are not needed.`
    )
  }

  if (additions.length === 0) return base
  return base ? base + '\n' + additions.join('\n') : additions.join('\n')
}

// ── Step builders ─────────────────────────────────────────────────────────────

// aiMessagePath builds a ||‑separated gjson fallback path that extracts the
// last user message text for single-value uses (context fit, classify, history).
function aiMessagePath(inputField: string): string {
  const simple = inputField || 'message'
  return `messages.#(role=="user").content||${simple}`
}

// buildParseSteps prepends the raw-body bind + parse_message_format steps that
// extract the full messages array (multi-turn + content blocks), detected format,
// and system prompt. Also binds the 'stream' flag for SSE detection.
// These slots are used by llm_call (messages_slot) and format_response (format_slot, stream_slot).
function buildParseSteps(out: SyncStep[]) {
  out.push({ action: 'bind_body', key: '@this', as: 'var.body' })
  out.push({
    action: 'parse_message_format',
    key_identifier: 'var.body',
    as: 'var.messages',
    input: { system_slot: 'var.parsed_system', detected_fmt_slot: 'var.fmt' },
  })
  out.push({ action: 'bind_body', key: 'stream', as: 'var.stream' })
}

// buildFormatAndRespondSteps appends format_response + respond, which converts
// the raw LLM text into the caller's expected wire format (Anthropic/OpenAI/Gemini)
// and handles SSE streaming for Claude Code / streaming clients.
function buildFormatAndRespondSteps(
  out: SyncStep[],
  primaryModel: string,
  modelSlot?: string,
  inputTokensSlot = '0',
  outputTokensSlot = '1',
) {
  const fmtInput: Record<string, string> = {
    format_slot:        'var.fmt',
    stop_reason_slot:   'var.stop_reason',
    stream_slot:        'var.stream',
    model:              primaryModel,
    input_tokens_slot:  inputTokensSlot,
    output_tokens_slot: outputTokensSlot,
  }
  if (modelSlot) fmtInput.model_slot = modelSlot
  out.push({ action: 'format_response', key_identifier: 'var.reply', as: 'var.formatted', input: fmtInput })
  out.push({ action: 'respond', key_identifier: 'var.formatted' })
}

function buildProxySteps(cfg: ProxyConfig): SyncStep[] {
  const out: SyncStep[] = []
  buildParseSteps(out)
  // Also extract last user message text (needed as fallback if messages_slot is empty)
  out.push({ action: 'bind_body', key: aiMessagePath(cfg.inputField), as: 'var.prompt' })
  if (cfg.systemPrompt.trim()) {
    out.push({ action: 'set_const', value: cfg.systemPrompt.trim(), as: 'var.system' })
  }
  const llmInput: Record<string, string> = {
    model:              cfg.model,
    max_tokens:         cfg.maxTokens || '2000',
    temperature:        cfg.temperature || '0.7',
    messages_slot:      'var.messages',
    input_tokens_slot:  '0',
    output_tokens_slot: '1',
    stop_reason_slot:   'var.stop_reason',
    system_slot:        cfg.systemPrompt.trim() ? 'var.system' : 'var.parsed_system',
  }
  if (cfg.fallbackChain.length > 0) llmInput.fallback_chain = cfg.fallbackChain.join(',')
  out.push({ action: 'llm_call', key_identifier: 'var.prompt', as: 'var.reply', input: llmInput })
  buildFormatAndRespondSteps(out, cfg.model)
  return out
}

function buildRouterSteps(cfg: RouterConfig): SyncStep[] {
  const out: SyncStep[] = []
  buildParseSteps(out)
  out.push({ action: 'bind_body', key: aiMessagePath(cfg.inputField), as: 'var.prompt' })
  if (cfg.systemPrompt.trim()) {
    out.push({ action: 'set_const', value: cfg.systemPrompt.trim(), as: 'var.system' })
  }
  if (cfg.historyEnabled) {
    out.push({ action: 'bind_header', key: cfg.sessionHeader || 'X-Session-Id', as: 'var.session' })
    out.push({ action: 'load_history', key_identifier: 'var.history', as: 'var.session', input: { domain: 'customer_data' } })
  }
  const ctxInput: Record<string, string> = { model: cfg.defaultModel, total_slot: 'var.total_tok' }
  if (cfg.historyEnabled) ctxInput.history_slot = 'var.history'
  out.push({ action: 'check_context_fit', key_identifier: 'var.prompt', as: 'var.ctx_fits', input: ctxInput })

  const rulesArr = [
    ...cfg.routingRules.map(r => ({ condition: `token_count ${r.operator} ${r.threshold}`, model: r.model, fallback: r.fallbackChain })),
    { condition: 'true', model: cfg.defaultModel, fallback: cfg.defaultFallback },
  ]
  out.push({ action: 'route_llm', as: 'var.model', input: { rules: JSON.stringify(rulesArr), default: cfg.defaultModel, token_slot: 'var.total_tok' } })

  const fbByModel = buildFallbackByModel(cfg.routingRules, cfg.defaultModel, cfg.defaultFallback)
  const llmInput: Record<string, string> = {
    model:              cfg.defaultModel,
    model_slot:         'var.model',
    max_tokens:         '2000',
    messages_slot:      'var.messages',
    input_tokens_slot:  '0',
    output_tokens_slot: '1',
    stop_reason_slot:   'var.stop_reason',
    system_slot:        cfg.systemPrompt.trim() ? 'var.system' : 'var.parsed_system',
  }
  if (cfg.historyEnabled) llmInput.history_slot = 'var.history'
  if (Object.keys(fbByModel).length > 0) llmInput.fallback_by_model = JSON.stringify(fbByModel)
  out.push({ action: 'llm_call', key_identifier: 'var.prompt', as: 'var.reply', input: llmInput })

  if (cfg.historyEnabled) {
    out.push({ action: 'append_message', key_identifier: 'var.prompt', as: 'var.history', input: { role: 'user', max_turns: cfg.maxTurns || '20' } })
    out.push({ action: 'append_message', key_identifier: 'var.reply', as: 'var.history', input: { role: 'assistant' } })
    out.push({ action: 'save_history', key_identifier: 'var.history', as: 'var.session', input: { domain: 'customer_data' } })
  }
  buildFormatAndRespondSteps(out, cfg.defaultModel, 'var.model')
  return out
}

interface ClassifierFlowResult {
  main: SyncStep[]
  // Additional named sub-flows required by if/else branches (not API-exposed)
  subFlows: Array<{ name: string; instructions: SyncStep[] }>
}

function buildSmartClassifierSteps(cfg: ClassifierRouterConfig, flowName: string): ClassifierFlowResult {
  const main: SyncStep[] = []
  const subFlows: Array<{ name: string; instructions: SyncStep[] }> = []
  const field = cfg.classifierField || 'complexity'
  const slotName = `var.${field}`

  buildParseSteps(main)
  main.push({ action: 'bind_body', key: aiMessagePath(cfg.inputField), as: 'var.prompt' })

  // Actual LLM system prompt (for the final model, not the classifier) — optional
  if (cfg.actualSystemPrompt.trim()) {
    main.push({ action: 'set_const', value: cfg.actualSystemPrompt.trim(), as: 'var.system' })
  }

  // Session header is always bound when history is possible (classifier may decide per-request)
  const needsSession = cfg.historyEnabled || cfg.classifierDecideHistory
  if (needsSession) {
    main.push({ action: 'bind_header', key: cfg.sessionHeader || 'X-Session-Id', as: 'var.session' })
  }

  // When history is always-on (not classifier-driven), load it before token estimation
  if (cfg.historyEnabled && !cfg.classifierDecideHistory) {
    main.push({ action: 'load_history', key_identifier: 'var.history', as: 'var.session', input: { domain: 'customer_data' } })
  }

  // Token estimation (drives tier-1 classifier selection)
  const ctxInput: Record<string, string> = { model: cfg.defaultModel || 'default', total_slot: 'var.total_tok' }
  if (cfg.historyEnabled && !cfg.classifierDecideHistory) ctxInput.history_slot = 'var.history'
  main.push({ action: 'check_context_fit', key_identifier: 'var.prompt', as: 'var.ctx_fits', input: ctxInput })

  // ── TIER 1: select which classifier to call based on token count ──
  const clsRulesArr = [
    ...cfg.classifierRules
      .filter(r => r.classifierModel)
      .map(r => ({ condition: `token_count ${r.operator} ${r.threshold}`, model: r.classifierModel, fallback: r.fallbackChain })),
    { condition: 'true', model: cfg.defaultClassifierModel, fallback: cfg.defaultClassifierFallback },
  ]
  main.push({ action: 'route_llm', as: 'var.cls_model', input: { rules: JSON.stringify(clsRulesArr), default: cfg.defaultClassifierModel, token_slot: 'var.total_tok' } })

  // Classifier call — uses dynamic model from tier-1, with per-model fallbacks
  const clsFBM = buildFallbackByModel(
    cfg.classifierRules.map(r => ({ model: r.classifierModel, fallbackChain: r.fallbackChain })),
    cfg.defaultClassifierModel,
    cfg.defaultClassifierFallback,
  )
  const effectiveClsPrompt = buildEffectiveClassifierPrompt(cfg)
  const classifyInput: Record<string, string> = {
    model: cfg.defaultClassifierModel,
    model_slot: 'var.cls_model',
  }
  if (Object.keys(clsFBM).length > 0) classifyInput.fallback_by_model = JSON.stringify(clsFBM)

  // Classifier system prompt — separate from the actual LLM system prompt
  if (effectiveClsPrompt) {
    main.push({ action: 'set_const', value: effectiveClsPrompt, as: 'var.cls_system' })
    classifyInput.system_slot = 'var.cls_system'
  }
  // Output field mappings from classifier JSON
  classifyInput[field] = slotName   // e.g. "complexity" → "var.complexity"
  if (cfg.classifierDecideHistory) classifyInput['needs_history'] = 'var.needs_history'
  if (cfg.classifierDecideTools)   classifyInput['needs_tools']   = 'var.needs_tools'

  main.push({ action: 'classify_llm', key_identifier: 'var.prompt', as: 'var.cls_raw', input: classifyInput })

  // ── TIER 2: select actual model based on classifier output ──
  const modelRulesArr = [
    ...cfg.modelRules
      .filter(r => r.conditionValue && r.model)
      .map(r => ({ condition: `${slotName} == ${r.conditionValue}`, model: r.model, fallback: r.fallbackChain })),
    { condition: 'true', model: cfg.defaultModel, fallback: cfg.defaultFallback },
  ]
  main.push({ action: 'route_llm', as: 'var.model', input: { rules: JSON.stringify(modelRulesArr), default: cfg.defaultModel } })

  // Actual LLM call — per-model fallback chains
  const fbByModel = buildFallbackByModel(cfg.modelRules, cfg.defaultModel, cfg.defaultFallback)
  const llmInput: Record<string, string> = {
    model:              cfg.defaultModel,
    model_slot:         'var.model',
    max_tokens:         '2000',
    messages_slot:      'var.messages',
    input_tokens_slot:  '0',
    output_tokens_slot: '1',
    stop_reason_slot:   'var.stop_reason',
    system_slot:        cfg.actualSystemPrompt.trim() ? 'var.system' : 'var.parsed_system',
  }
  if (Object.keys(fbByModel).length > 0) llmInput.fallback_by_model = JSON.stringify(fbByModel)

  if (cfg.classifierDecideHistory) {
    // Classifier decides per-request — generate if/else sub-flows
    const histOnName  = flowName + '-hist-on'
    const histOffName = flowName + '-hist-off'

    const histOnLlmInput = { ...llmInput, history_slot: 'var.history' }
    const histOnSteps: SyncStep[] = [
      { action: 'load_history', key_identifier: 'var.history', as: 'var.session', input: { domain: 'customer_data' } },
      { action: 'llm_call', key_identifier: 'var.prompt', as: 'var.reply', input: histOnLlmInput },
      { action: 'append_message', key_identifier: 'var.prompt', as: 'var.history', input: { role: 'user', max_turns: cfg.maxTurns || '20' } },
      { action: 'append_message', key_identifier: 'var.reply', as: 'var.history', input: { role: 'assistant' } },
      { action: 'save_history', key_identifier: 'var.history', as: 'var.session', input: { domain: 'customer_data' } },
      { action: 'format_response', key_identifier: 'var.reply', as: 'var.formatted', input: { format_slot: 'var.fmt', stop_reason_slot: 'var.stop_reason', stream_slot: 'var.stream', model: cfg.defaultModel, model_slot: 'var.model', input_tokens_slot: '0', output_tokens_slot: '1' } },
    ]
    const histOffSteps: SyncStep[] = [
      { action: 'llm_call', key_identifier: 'var.prompt', as: 'var.reply', input: llmInput },
      { action: 'format_response', key_identifier: 'var.reply', as: 'var.formatted', input: { format_slot: 'var.fmt', stop_reason_slot: 'var.stop_reason', stream_slot: 'var.stream', model: cfg.defaultModel, model_slot: 'var.model', input_tokens_slot: '0', output_tokens_slot: '1' } },
    ]

    subFlows.push({ name: histOnName,  instructions: histOnSteps  })
    subFlows.push({ name: histOffName, instructions: histOffSteps })

    // if var.needs_history is non-empty ("yes"), take the history branch
    main.push({ action: 'if', condition: 'var.needs_history', then: histOnName, else: histOffName })
    main.push({ action: 'respond', key_identifier: 'var.formatted' })

  } else if (cfg.historyEnabled) {
    // History always-on (not classifier-driven)
    llmInput.history_slot = 'var.history'
    main.push({ action: 'llm_call', key_identifier: 'var.prompt', as: 'var.reply', input: llmInput })
    main.push({ action: 'append_message', key_identifier: 'var.prompt', as: 'var.history', input: { role: 'user', max_turns: cfg.maxTurns || '20' } })
    main.push({ action: 'append_message', key_identifier: 'var.reply', as: 'var.history', input: { role: 'assistant' } })
    main.push({ action: 'save_history', key_identifier: 'var.history', as: 'var.session', input: { domain: 'customer_data' } })
    buildFormatAndRespondSteps(main, cfg.defaultModel, 'var.model')
  } else {
    main.push({ action: 'llm_call', key_identifier: 'var.prompt', as: 'var.reply', input: llmInput })
    buildFormatAndRespondSteps(main, cfg.defaultModel, 'var.model')
  }
  return { main, subFlows }
}

// ── Sub-components ────────────────────────────────────────────────────────────

function ModelSelect({ value, onChange, models, placeholder = '— select model —' }: {
  value: string; onChange: (v: string) => void; models: LLMModel[]; placeholder?: string
}) {
  const selected = models.find(m => m.alias === value)
  const costHint = selected && (selected.cost_per_input_token || selected.cost_per_output_token)
    ? `$${selected.cost_per_input_token ?? 0}/$${selected.cost_per_output_token ?? 0} per 1M tok`
    : null
  return (
    <div>
      <select className="input" value={value} onChange={e => onChange(e.target.value)}>
        <option value="">{placeholder}</option>
        {models.map(m => <option key={m.alias} value={m.alias}>{m.alias} ({m.provider})</option>)}
      </select>
      {costHint && (
        <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 2, paddingLeft: 2 }}>
          {costHint}
        </div>
      )}
    </div>
  )
}

function FallbackChainEditor({ chain, onChange, models }: {
  chain: string[]; onChange: (c: string[]) => void; models: LLMModel[]
}) {
  const [adding, setAdding] = useState('')
  const available = models.filter(m => !chain.includes(m.alias))
  function add() { if (adding && !chain.includes(adding)) { onChange([...chain, adding]); setAdding('') } }
  return (
    <div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8 }}>
        {chain.length === 0 && <span style={{ fontSize: 12, color: 'var(--muted)' }}>No fallback — add models below</span>}
        {chain.map((alias, i) => (
          <div key={alias} style={{ display: 'flex', alignItems: 'center', gap: 5, background: 'var(--step-bg)', border: '1px solid var(--border-hi)', borderRadius: 6, padding: '3px 10px', fontSize: 12 }}>
            <span style={{ color: 'var(--muted)', marginRight: 2 }}>{i + 1}.</span>
            <span style={{ fontWeight: 600 }}>{alias}</span>
            <button type="button" onClick={() => onChange(chain.filter((_, j) => j !== i))} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 14, lineHeight: 1, padding: '0 2px' }}>×</button>
          </div>
        ))}
      </div>
      <div style={{ display: 'flex', gap: 6 }}>
        <select className="input" style={{ flex: 1 }} value={adding} onChange={e => setAdding(e.target.value)}>
          <option value="">+ add fallback model…</option>
          {available.map(m => <option key={m.alias} value={m.alias}>{m.alias} ({m.provider})</option>)}
        </select>
        <button className="btn" type="button" onClick={add} disabled={!adding} style={{ width: 'auto', padding: '0 14px', flexShrink: 0, opacity: adding ? 1 : 0.4 }}>Add</button>
      </div>
    </div>
  )
}

/** Compact inline fallback pill row inside rule cards */
function InlineFallback({ chain, onChange, models }: {
  chain: string[]; onChange: (c: string[]) => void; models: LLMModel[]
}) {
  const [adding, setAdding] = useState('')
  const available = models.filter(m => !chain.includes(m.alias))
  function add() { if (adding && !chain.includes(adding)) { onChange([...chain, adding]); setAdding('') } }
  return (
    <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 4, marginTop: 7, paddingTop: 6, borderTop: '1px dashed var(--border)' }}>
      <span style={{ fontSize: 11, color: 'var(--muted)', flexShrink: 0, marginRight: 2 }}>Fallback if fails:</span>
      {chain.length === 0 && <span style={{ fontSize: 11, color: 'var(--muted)', fontStyle: 'italic' }}>none</span>}
      {chain.map((alias, i) => (
        <span key={alias} style={{ display: 'inline-flex', alignItems: 'center', gap: 3, background: 'var(--step-bg)', border: '1px solid var(--border-hi)', borderRadius: 4, padding: '1px 7px', fontSize: 11 }}>
          <span style={{ color: 'var(--muted)', fontSize: 10 }}>{i + 1}.</span>
          <span>{alias}</span>
          <button type="button" onClick={() => onChange(chain.filter((_, j) => j !== i))} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 12, lineHeight: 1, padding: 0 }}>×</button>
        </span>
      ))}
      <div style={{ display: 'inline-flex', gap: 3, alignItems: 'center' }}>
        <select value={adding} onChange={e => setAdding(e.target.value)} style={{ fontSize: 11, padding: '1px 4px', border: '1px dashed var(--border-hi)', borderRadius: 4, background: 'var(--input-bg)', color: 'var(--text)', cursor: 'pointer' }}>
          <option value="">+ add</option>
          {available.map(m => <option key={m.alias} value={m.alias}>{m.alias}</option>)}
        </select>
        {adding && <button type="button" onClick={add} style={{ fontSize: 11, padding: '1px 8px', border: '1px solid var(--accent)', borderRadius: 4, background: 'transparent', color: 'var(--accent)', cursor: 'pointer' }}>+</button>}
      </div>
    </div>
  )
}

/** Token-count routing rule card — used in both Token Router and Tier-1 classifier selection */
function TokenRuleRow({ label, rule, onChangeOp, onChangeThreshold, onChangeModel, onRemove, onChangeFallback, models }: {
  label: string
  rule: { operator: '>' | '<' | '>=' | '<='; threshold: number; model: string; fallbackChain: string[] }
  onChangeOp: (op: '>' | '<' | '>=' | '<=') => void
  onChangeThreshold: (n: number) => void
  onChangeModel: (m: string) => void
  onRemove: () => void
  onChangeFallback: (fc: string[]) => void
  models: LLMModel[]
}) {
  return (
    <div style={{ marginBottom: 8, background: 'var(--step-bg)', border: '1px solid var(--border)', borderRadius: 6, padding: '8px 10px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>token_count</span>
        <select className="input" value={rule.operator} onChange={e => onChangeOp(e.target.value as '>' | '<' | '>=' | '<=')} style={{ width: 56, flexShrink: 0, padding: '5px 4px' }}>
          <option value=">">&gt;</option><option value=">=">&gt;=</option><option value="<">&lt;</option><option value="<=">&lt;=</option>
        </select>
        <input type="number" className="input" value={rule.threshold} min={0} onChange={e => onChangeThreshold(parseInt(e.target.value) || 0)} style={{ width: 80, flexShrink: 0 }} />
        <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>→</span>
        <div style={{ flex: 1 }}>
          <ModelSelect value={rule.model} onChange={onChangeModel} models={models} placeholder={label} />
        </div>
        <button type="button" onClick={onRemove} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 18, lineHeight: 1, flexShrink: 0 }}>×</button>
      </div>
      <InlineFallback chain={rule.fallbackChain} onChange={onChangeFallback} models={models} />
    </div>
  )
}

/** Default / catch-all model row (no condition) */
function DefaultRow({ label, model, fallbackChain, onChangeModel, onChangeFallback, models }: {
  label: string; model: string; fallbackChain: string[]
  onChangeModel: (m: string) => void; onChangeFallback: (fc: string[]) => void; models: LLMModel[]
}) {
  return (
    <div style={{ marginBottom: 8, background: 'var(--step-bg)', border: '1px solid var(--border)', borderRadius: 6, padding: '8px 10px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <span style={{ fontSize: 12, color: 'var(--muted)', minWidth: 110, flexShrink: 0 }}>default (no match)</span>
        <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>→</span>
        <div style={{ flex: 1 }}>
          <ModelSelect value={model} onChange={onChangeModel} models={models} placeholder={label} />
        </div>
        <div style={{ width: 26 }} />
      </div>
      <InlineFallback chain={fallbackChain} onChange={onChangeFallback} models={models} />
    </div>
  )
}

/** Classifier output routing rule card — Tier 2 */
function ClassifierOutputRuleRow({ rule, field, onChange, onRemove, models }: {
  rule: ModelTierRule; field: string; onChange: (r: ModelTierRule) => void; onRemove: () => void; models: LLMModel[]
}) {
  return (
    <div style={{ marginBottom: 8, background: 'var(--step-bg)', border: '1px solid var(--border)', borderRadius: 6, padding: '8px 10px' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <code style={{ fontSize: 11, color: 'var(--muted)', flexShrink: 0, background: 'var(--block-bg)', padding: '2px 6px', borderRadius: 3 }}>{field || 'complexity'}</code>
        <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>==</span>
        <input className="input" value={rule.conditionValue} placeholder="e.g. low" onChange={e => onChange({ ...rule, conditionValue: e.target.value })} style={{ width: 90, flexShrink: 0 }} />
        <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>→</span>
        <div style={{ flex: 1 }}>
          <ModelSelect value={rule.model} onChange={v => onChange({ ...rule, model: v })} models={models} />
        </div>
        <button type="button" onClick={onRemove} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 18, lineHeight: 1, flexShrink: 0 }}>×</button>
      </div>
      <InlineFallback chain={rule.fallbackChain} onChange={fc => onChange({ ...rule, fallbackChain: fc })} models={models} />
    </div>
  )
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ fontSize: 11, fontWeight: 700, letterSpacing: 0.5, color: 'var(--muted)', textTransform: 'uppercase', marginTop: 18, marginBottom: 8, paddingBottom: 4, borderBottom: '1px solid var(--border)' }}>
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
    <div style={{ background: 'var(--input-bg)', border: '1px solid var(--border-hi)', borderRadius: 6, padding: '10px 12px', maxHeight: 220, overflowY: 'auto' }}>
      {steps.map((s, i) => (
        <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 4, fontSize: 12, fontFamily: 'monospace' }}>
          <span style={{ color: 'var(--muted)', minWidth: 18, textAlign: 'right', flexShrink: 0 }}>{i + 1}</span>
          <span style={{ color: 'var(--accent)', fontWeight: 700, flexShrink: 0 }}>{s.action}</span>
          <span style={{ color: 'var(--muted)', wordBreak: 'break-all' }}>
            {[
              s.key_identifier && `from:${s.key_identifier}`,
              s.as && `→ ${s.as}`,
              s.key && `key:${s.key}`,
              s.condition && `if:${s.condition}`,
              (s.then as string | undefined) && `then:${s.then}`,
              (s.else as string | undefined) && `else:${s.else}`,
              s.input?.model && `model:${s.input.model}`,
              s.input?.model_slot && `slot:${s.input.model_slot}`,
              s.input?.fallback_chain && `fb:${s.input.fallback_chain}`,
              s.input?.fallback_by_model && `fb_rules:${Object.keys(JSON.parse(s.input.fallback_by_model)).length}`,
              s.input?.history_slot && `hist:${s.input.history_slot}`,
              s.input?.system_slot && `sys:${s.input.system_slot}`,
              s.input?.role && `role:${s.input.role}`,
            ].filter(Boolean).join('  ')}
          </span>
        </div>
      ))}
    </div>
  )
}

/** Shows the classifier system prompt auto-additions when checkboxes are enabled */
function EffectivePromptPreview({ cfg }: { cfg: ClassifierRouterConfig }) {
  const hasAdditions = cfg.classifierDecideHistory || cfg.classifierDecideTools
  if (!hasAdditions) return null

  const additions: string[] = []
  if (cfg.classifierDecideHistory) additions.push('"needs_history": "yes"  — include only when prior context is required')
  if (cfg.classifierDecideTools)   additions.push('"needs_tools": "yes"    — include only when external tools are needed')

  return (
    <div style={{ marginTop: 8, padding: '8px 10px', borderRadius: 6, background: 'rgba(87,181,255,0.05)', border: '1px dashed var(--border-hi)', fontSize: 11 }}>
      <div style={{ fontWeight: 700, color: 'var(--muted)', marginBottom: 5 }}>Auto-appended to classifier prompt:</div>
      {additions.map((a, i) => (
        <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 2 }}>
          <span style={{ color: 'var(--accent)', flexShrink: 0 }}>+</span>
          <code style={{ color: 'var(--text)' }}>{a}</code>
        </div>
      ))}
      <div style={{ color: 'var(--muted)', marginTop: 6 }}>
        The classifier should omit these fields entirely when not applicable — omission = false, presence = true.
      </div>
    </div>
  )
}

// ── Defaults ──────────────────────────────────────────────────────────────────

const DEFAULT_CLASSIFIER_SYSTEM_PROMPT =
  `Analyze the request and return ONLY valid JSON with this field:
{"complexity": "low|medium|high"}
low = simple fact, greeting, quick lookup
medium = analysis, coding, multi-step reasoning
high = complex research, long doc, expert technical work`

const DEFAULT_PROXY: ProxyConfig = {
  endpoint: '/chat', inputField: 'message', model: '', fallbackChain: [],
  systemPrompt: '', maxTokens: '2000', temperature: '0.7',
}

const DEFAULT_ROUTER: RouterConfig = {
  endpoint: '/ai/chat',
  inputField: 'message',
  systemPrompt: '',
  routingRules: [
    { id: '1', operator: '>', threshold: 8000, model: '', fallbackChain: [] },
    { id: '2', operator: '>', threshold: 2000, model: '', fallbackChain: [] },
  ],
  defaultModel: '',
  defaultFallback: [],
  historyEnabled: false,
  sessionHeader: 'X-Session-Id',
  maxTurns: '20',
}

const DEFAULT_CLASSIFIER_ROUTER: ClassifierRouterConfig = {
  endpoint: '/ai/smart-chat',
  inputField: 'message',

  classifierSystemPrompt: DEFAULT_CLASSIFIER_SYSTEM_PROMPT,
  classifierField: 'complexity',
  classifierRules: [
    { id: '1', operator: '>', threshold: 4000, classifierModel: '', fallbackChain: [] },
    { id: '2', operator: '>', threshold: 500,  classifierModel: '', fallbackChain: [] },
  ],
  defaultClassifierModel: '',
  defaultClassifierFallback: [],

  actualSystemPrompt: '',
  modelRules: [
    { id: '1', conditionValue: 'low',    model: '', fallbackChain: [] },
    { id: '2', conditionValue: 'medium', model: '', fallbackChain: [] },
    { id: '3', conditionValue: 'high',   model: '', fallbackChain: [] },
  ],
  defaultModel: '',
  defaultFallback: [],

  classifierDecideHistory: false,
  classifierDecideTools: false,

  historyEnabled: false,
  sessionHeader: 'X-Session-Id',
  maxTurns: '20',
}

// ── Saved-route model ─────────────────────────────────────────────────────────

interface SavedRoute {
  id: string
  label: string
  mode: Mode
  flowName: string
  proxy: ProxyConfig
  router: RouterConfig
  classifier: ClassifierRouterConfig
  deployedAt?: string   // ISO string of last successful deploy
}

const STORAGE_KEY = 'rah-ai-routes-v2'
const STORAGE_KEY_LEGACY = 'rah-ai-routes-state'

function makeRoute(mode: Mode = 'proxy', overrides: Partial<SavedRoute> = {}): SavedRoute {
  const defaultNames: Record<Mode, string> = { proxy: 'my-llm-proxy', router: 'my-smart-router', classifier: 'my-classifier-router' }
  const defaultLabels: Record<Mode, string> = { proxy: 'LLM Proxy', router: 'Token Router', classifier: 'Smart Router' }
  return {
    id: Date.now().toString() + Math.random().toString(36).slice(2, 6),
    label: defaultLabels[mode],
    mode,
    flowName: defaultNames[mode],
    proxy: { ...DEFAULT_PROXY },
    router: { ...DEFAULT_ROUTER },
    classifier: { ...DEFAULT_CLASSIFIER_ROUTER },
    ...overrides,
  }
}

function loadRoutes(): { routes: SavedRoute[]; activeId: string } {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const data = JSON.parse(raw)
      if (data.routes && data.routes.length > 0) return data
    }
    // Migrate from legacy single-route key
    const legacy = localStorage.getItem(STORAGE_KEY_LEGACY)
    if (legacy) {
      const s = JSON.parse(legacy)
      const r = makeRoute(s.mode || 'proxy', {
        label: s.mode === 'classifier' ? 'Smart Router' : s.mode === 'router' ? 'Token Router' : 'LLM Proxy',
        mode: s.mode || 'proxy',
        flowName: s.flowName || 'my-llm-proxy',
        proxy: s.proxy || DEFAULT_PROXY,
        router: s.router || DEFAULT_ROUTER,
        classifier: s.classifier || DEFAULT_CLASSIFIER_ROUTER,
      })
      return { routes: [r], activeId: r.id }
    }
  } catch { /* ignore */ }
  const r = makeRoute('proxy')
  return { routes: [r], activeId: r.id }
}

// ── Main component ─────────────────────────────────────────────────────────────

export default function AIRoutes() {
  const init = useRef(loadRoutes()).current
  const [routes, setRoutes] = useState<SavedRoute[]>(init.routes)
  const [activeId, setActiveId] = useState<string>(init.activeId)
  const [models, setModels] = useState<LLMModel[]>([])
  const [loadingModels, setLoadingModels] = useState(true)
  const [deploying, setDeploying] = useState(false)
  const [deployResult, setDeployResult] = useState<{ ok: boolean; msg: string } | null>(null)
  const [editingLabelId, setEditingLabelId] = useState<string | null>(null)
  const [labelDraft, setLabelDraft] = useState('')
  const [serverLoaded, setServerLoaded] = useState(false)
  const [importing, setImporting] = useState(false)
  const [importMsg, setImportMsg] = useState<string | null>(null)
  const [existingFlowNames, setExistingFlowNames] = useState<string[]>([])
  const [showExistingFlowPicker, setShowExistingFlowPicker] = useState(false)
  const [existingFlowSearch, setExistingFlowSearch] = useState('')

  // On mount: try to load routes from the gateway (server-side persistence).
  // Falls back to whatever loadRoutes() already returned from localStorage.
  useEffect(() => {
    loadRouteConfigs().then(data => {
      if (Array.isArray(data) && data.length > 0) {
        const rs = data as SavedRoute[]
        setRoutes(rs)
        setActiveId(rs[0].id)
      }
      setServerLoaded(true)
    })
  }, [])

  // Persist to localStorage on every routes change (immediate, offline-safe).
  useEffect(() => {
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify({ routes, activeId })) } catch {}
  }, [routes, activeId])

  // Debounced save to gateway — fires 1.5s after the last routes change,
  // but only after the server load has completed (avoids overwriting with stale data).
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => {
    if (!serverLoaded) return
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => { saveRouteConfigs(routes) }, 1500)
    return () => { if (saveTimerRef.current) clearTimeout(saveTimerRef.current) }
  }, [routes, serverLoaded])

  useEffect(() => {
    listLLMModels().then(setModels).catch(() => {}).finally(() => setLoadingModels(false))
  }, [])

  // Active route derived
  const active = routes.find(r => r.id === activeId) ?? routes[0]
  const { mode, flowName, proxy, router, classifier } = active

  // Generic updater — patches any top-level field of the active route
  function patchActive(patch: Partial<SavedRoute>) {
    setRoutes(rs => rs.map(r => r.id === activeId ? { ...r, ...patch } : r))
  }

  // Typed setters that mirror old API
  function setMode(m: Mode) {
    const defaultNames: Record<Mode, string> = { proxy: 'my-llm-proxy', router: 'my-smart-router', classifier: 'my-classifier-router' }
    patchActive({ mode: m, flowName: defaultNames[m] })
    setDeployResult(null)
  }
  function setFlowName(v: string) { patchActive({ flowName: v }); setDeployResult(null) }
  function setProxy(fn: (p: ProxyConfig) => ProxyConfig) { patchActive({ proxy: fn(proxy) }) }
  function setRouter(fn: (r: RouterConfig) => RouterConfig) { patchActive({ router: fn(router) }) }
  function setClassifier(fn: (c: ClassifierRouterConfig) => ClassifierRouterConfig) { patchActive({ classifier: fn(classifier) }) }

  // Load parent flow names from the gateway for the "use existing flow" picker
  async function loadExistingFlowNames() {
    try {
      const snap = await fetchGatewaySnapshot()
      const parentNames = new Set(snap.apis.map(a => a.flow_name).filter(Boolean))
      setExistingFlowNames(snap.flows.filter(f => parentNames.has(f.name)).map(f => f.name))
    } catch { /* ignore — picker just won't show options */ }
  }

  // Import all AI-looking flows from the gateway into the sidebar
  async function handleImportFromGateway() {
    setImporting(true); setImportMsg(null)
    try {
      const snap = await fetchGatewaySnapshot()
      // Build endpoint map: flow_name → path
      const endpointMap: Record<string, string> = {}
      for (const api of snap.apis) endpointMap[api.flow_name] = api.path

      // Only import flows that look like AI routes (have llm_call or classify_llm)
      const aiFlows = snap.flows.filter(f =>
        f.instructions.some(s => s.action === 'llm_call' || s.action === 'classify_llm')
      )
      if (aiFlows.length === 0) {
        setImportMsg('No AI flows found in the gateway.')
        return
      }

      // Reconstruct and deduplicate against existing routes by flowName
      const existingNames = new Set(routes.map(r => r.flowName))
      const imported: SavedRoute[] = []
      for (const f of aiFlows) {
        if (existingNames.has(f.name)) continue  // already in sidebar
        const rec = reconstructRoute(f.name, f.instructions, endpointMap[f.name] || '')
        if (!rec) continue
        imported.push({
          ...rec,
          id: Date.now().toString() + Math.random().toString(36).slice(2, 6),
          deployedAt: new Date().toISOString(),
          proxy: rec.proxy,
          router: rec.router,
          classifier: rec.classifier,
        } as SavedRoute)
      }

      if (imported.length === 0) {
        setImportMsg('All gateway AI flows are already in the sidebar.')
        return
      }

      setRoutes(rs => [...rs, ...imported])
      setActiveId(imported[0].id)
      setImportMsg(`Imported ${imported.length} flow${imported.length > 1 ? 's' : ''}: ${imported.map(r => r.flowName).join(', ')}`)
    } catch (e) {
      setImportMsg('Import failed: ' + String(e))
    } finally {
      setImporting(false)
    }
  }

  // Route list management
  function addRoute(m: Mode) {
    const r = makeRoute(m)
    setRoutes(rs => [...rs, r])
    setActiveId(r.id)
    setDeployResult(null)
  }
  function deleteRoute(id: string) {
    setRoutes(rs => {
      const remaining = rs.filter(r => r.id !== id)
      if (remaining.length === 0) {
        const fresh = makeRoute('proxy')
        setActiveId(fresh.id)
        return [fresh]
      }
      if (activeId === id) setActiveId(remaining[remaining.length - 1].id)
      return remaining
    })
    setDeployResult(null)
  }
  function duplicateRoute(r: SavedRoute) {
    const copy = { ...r, id: Date.now().toString() + Math.random().toString(36).slice(2, 6), label: r.label + ' (copy)', deployedAt: undefined }
    setRoutes(rs => {
      const idx = rs.findIndex(x => x.id === r.id)
      const next = [...rs]
      next.splice(idx + 1, 0, copy)
      return next
    })
    setActiveId(copy.id)
    setDeployResult(null)
  }

  // Token Router helpers
  function addRouterRule() {
    setRouter(r => ({ ...r, routingRules: [...r.routingRules, { id: Date.now().toString(), operator: '>' as const, threshold: 1000, model: '', fallbackChain: [] }] }))
  }
  function updateRouterRule(id: string, u: RoutingRule) { setRouter(r => ({ ...r, routingRules: r.routingRules.map(x => x.id === id ? u : x) })) }
  function removeRouterRule(id: string) { setRouter(r => ({ ...r, routingRules: r.routingRules.filter(x => x.id !== id) })) }

  // Classifier tier-1 helpers
  function addClsRule() {
    setClassifier(c => ({ ...c, classifierRules: [...c.classifierRules, { id: Date.now().toString(), operator: '>' as const, threshold: 1000, classifierModel: '', fallbackChain: [] }] }))
  }
  function updateClsRule(id: string, u: ClassifierTierRule) { setClassifier(c => ({ ...c, classifierRules: c.classifierRules.map(x => x.id === id ? u : x) })) }
  function removeClsRule(id: string) { setClassifier(c => ({ ...c, classifierRules: c.classifierRules.filter(x => x.id !== id) })) }

  // Classifier tier-2 helpers
  function addModelRule() {
    setClassifier(c => ({ ...c, modelRules: [...c.modelRules, { id: Date.now().toString(), conditionValue: '', model: '', fallbackChain: [] }] }))
  }
  function updateModelRule(id: string, u: ModelTierRule) { setClassifier(c => ({ ...c, modelRules: c.modelRules.map(x => x.id === id ? u : x) })) }
  function removeModelRule(id: string) { setClassifier(c => ({ ...c, modelRules: c.modelRules.filter(x => x.id !== id) })) }

  async function handleDeploy() {
    const name = flowName.trim()
    if (!name) return
    const endpoint = (mode === 'proxy' ? proxy.endpoint : mode === 'router' ? router.endpoint : classifier.endpoint).trim()
    if (!endpoint) return

    let mainSteps: SyncStep[]
    let extraFlows: Array<{ name: string; instructions: SyncStep[] }> = []

    if (mode === 'proxy') {
      mainSteps = buildProxySteps(proxy)
    } else if (mode === 'router') {
      mainSteps = buildRouterSteps(router)
    } else {
      const result = buildSmartClassifierSteps(classifier, name)
      mainSteps = result.main
      extraFlows = result.subFlows
    }

    const payload = {
      sync_uuid: Math.random().toString(36).slice(2),
      flows: [
        { name, instructions: mainSteps, action: 'upsert' as const },
        ...extraFlows.map(sf => ({ name: sf.name, instructions: sf.instructions, action: 'upsert' as const })),
      ],
      apis: [{ name: name + '-api', path: endpoint, flow_name: name, action: 'upsert' as const }],
    }

    setDeploying(true); setDeployResult(null)
    try {
      await syncFlows(payload)
      await saveRouteConfigs(routes)  // persist route config on every deploy, not just on edit
      const f = mode === 'proxy' ? proxy.inputField : mode === 'router' ? router.inputField : classifier.inputField
      setDeployResult({ ok: true, msg: `Deployed! Call: POST ${endpoint}  (body: {"${f || 'message'}": "..."})` })
      patchActive({ deployedAt: new Date().toISOString() })
    } catch (e) {
      setDeployResult({ ok: false, msg: String(e) })
    } finally {
      setDeploying(false)
    }
  }

  // For step preview, use main flow only (sub-flows shown as "if" step refs in the main flow)
  const previewSteps = (() => {
    if (mode === 'proxy')  return buildProxySteps(proxy)
    if (mode === 'router') return buildRouterSteps(router)
    return buildSmartClassifierSteps(classifier, flowName).main
  })()

  const canDeploy = !!flowName.trim() && (() => {
    if (mode === 'proxy')  return !!(proxy.endpoint.trim() && proxy.model)
    if (mode === 'router') return !!(router.endpoint.trim() && router.defaultModel)
    return !!(classifier.endpoint.trim() && classifier.defaultClassifierModel && classifier.defaultModel)
  })()

  // Shared field helpers
  const endpointVal   = mode === 'proxy' ? proxy.endpoint   : mode === 'router' ? router.endpoint   : classifier.endpoint
  const inputFieldVal = mode === 'proxy' ? proxy.inputField : mode === 'router' ? router.inputField : classifier.inputField
  function setEndpoint(v: string)   { if (mode === 'proxy') setProxy(p => ({ ...p, endpoint: v }));   else if (mode === 'router') setRouter(r => ({ ...r, endpoint: v }));   else setClassifier(c => ({ ...c, endpoint: v })) }
  function setInputField(v: string) { if (mode === 'proxy') setProxy(p => ({ ...p, inputField: v })); else if (mode === 'router') setRouter(r => ({ ...r, inputField: v })); else setClassifier(c => ({ ...c, inputField: v })) }

  const modeIcons: Record<Mode, string> = { proxy: '⚡', router: '⚖️', classifier: '🧠' }
  const modeLabels: Record<Mode, string> = { proxy: 'LLM Proxy', router: 'Token Router', classifier: 'Smart Router' }

  return (
    <div style={{ display: 'flex', gap: 14, alignItems: 'flex-start' }}>

      {/* ── Left sidebar: route list ── */}
      <div style={{ width: 210, flexShrink: 0, display: 'flex', flexDirection: 'column', gap: 6 }}>
        <div style={{ fontSize: 11, fontWeight: 700, letterSpacing: 0.5, color: 'var(--muted)', textTransform: 'uppercase', marginBottom: 2 }}>AI Routes</div>

        {routes.map(r => (
          <div
            key={r.id}
            onClick={() => { if (editingLabelId !== r.id) { setActiveId(r.id); setDeployResult(null) } }}
            style={{
              background: r.id === activeId ? 'rgba(87,181,255,0.1)' : 'var(--block-bg)',
              border: r.id === activeId ? '1.5px solid var(--accent)' : '1px solid var(--border-hi)',
              borderRadius: 8, padding: '8px 10px', cursor: 'pointer',
              transition: 'background 0.12s, border-color 0.12s',
            }}
          >
            {editingLabelId === r.id ? (
              <input
                autoFocus
                className="input"
                style={{ fontSize: 12, padding: '2px 6px', marginBottom: 2, width: '100%' }}
                value={labelDraft}
                onChange={e => setLabelDraft(e.target.value)}
                onBlur={() => {
                  if (labelDraft.trim()) setRoutes(rs => rs.map(x => x.id === r.id ? { ...x, label: labelDraft.trim() } : x))
                  setEditingLabelId(null)
                }}
                onKeyDown={e => {
                  if (e.key === 'Enter') e.currentTarget.blur()
                  if (e.key === 'Escape') { setEditingLabelId(null) }
                }}
                onClick={e => e.stopPropagation()}
              />
            ) : (
              <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginBottom: 2 }}>
                <span style={{ fontSize: 13, fontWeight: 600, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: r.id === activeId ? 'var(--accent)' : 'var(--text)' }}>{r.label}</span>
                <button
                  type="button"
                  title="Rename"
                  onClick={e => { e.stopPropagation(); setLabelDraft(r.label); setEditingLabelId(r.id) }}
                  style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 11, padding: '0 2px', opacity: 0.6, lineHeight: 1 }}
                >✎</button>
              </div>
            )}
            <div style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
              <span style={{ fontSize: 10 }}>{modeIcons[r.mode]}</span>
              <span style={{ fontSize: 10, color: 'var(--muted)' }}>{modeLabels[r.mode]}</span>
              {r.deployedAt && <span style={{ fontSize: 9, color: '#22c55e', marginLeft: 'auto' }}>✓ deployed</span>}
            </div>
            {r.flowName && <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 2, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.flowName}</div>}
            <div style={{ display: 'flex', gap: 4, marginTop: 6 }}>
              <button type="button" title="Duplicate" onClick={e => { e.stopPropagation(); duplicateRoute(r) }}
                style={{ flex: 1, fontSize: 10, padding: '2px 0', background: 'transparent', border: '1px solid var(--border-hi)', borderRadius: 4, cursor: 'pointer', color: 'var(--muted)' }}>⧉ copy</button>
              <button type="button" title="Delete" onClick={e => { e.stopPropagation(); if (window.confirm(`Delete "${r.label}"?`)) deleteRoute(r.id) }}
                style={{ flex: 1, fontSize: 10, padding: '2px 0', background: 'transparent', border: '1px solid var(--border-hi)', borderRadius: 4, cursor: 'pointer', color: '#ef4444' }}>✕ delete</button>
            </div>
          </div>
        ))}

        {/* Import from gateway */}
        <div style={{ marginTop: 8 }}>
          <button type="button" onClick={handleImportFromGateway} disabled={importing}
            style={{ width: '100%', fontSize: 11, padding: '5px 0', background: 'rgba(87,181,255,0.07)', border: '1px solid var(--accent)', borderRadius: 6, cursor: importing ? 'wait' : 'pointer', color: 'var(--accent)', opacity: importing ? 0.6 : 1 }}>
            {importing ? 'Importing…' : '↓ Import from gateway'}
          </button>
          {importMsg && (
            <div style={{ fontSize: 10, color: importMsg.startsWith('Import failed') ? '#ef4444' : '#22c55e', marginTop: 4, lineHeight: 1.4 }}>{importMsg}</div>
          )}
        </div>

        {/* Add new route buttons */}
        <div style={{ marginTop: 4, display: 'flex', flexDirection: 'column', gap: 4 }}>
          <div style={{ fontSize: 10, color: 'var(--muted)', marginBottom: 2 }}>+ New route from template</div>
          {(['proxy', 'router', 'classifier'] as Mode[]).map(m => (
            <button key={m} type="button" onClick={() => addRoute(m)}
              style={{ fontSize: 11, padding: '4px 0', background: 'var(--block-bg)', border: '1px dashed var(--border-hi)', borderRadius: 6, cursor: 'pointer', color: 'var(--muted)', textAlign: 'left', paddingLeft: 8 }}>
              {modeIcons[m]} {modeLabels[m]}
            </button>
          ))}
          {/* Use existing flow */}
          <button type="button"
            onClick={() => { loadExistingFlowNames(); setShowExistingFlowPicker(v => !v); setExistingFlowSearch('') }}
            style={{ fontSize: 11, padding: '4px 0', background: 'var(--block-bg)', border: '1px dashed var(--accent)', borderRadius: 6, cursor: 'pointer', color: 'var(--accent)', textAlign: 'left', paddingLeft: 8 }}>
            🔗 Use existing flow…
          </button>
          {showExistingFlowPicker && (
            <div style={{ border: '1px solid var(--border)', borderRadius: 6, padding: 8, background: 'var(--surface)', marginTop: 2 }}>
              <input className="input" style={{ fontSize: 11, marginBottom: 6 }}
                placeholder="Search flows…" value={existingFlowSearch}
                onChange={e => setExistingFlowSearch(e.target.value)} />
              {existingFlowNames.length === 0 && (
                <div style={{ fontSize: 11, color: 'var(--muted)' }}>No deployed flows found.</div>
              )}
              {existingFlowNames
                .filter(n => !existingFlowSearch || n.toLowerCase().includes(existingFlowSearch.toLowerCase()))
                .map(flowName => (
                  <button key={flowName} type="button"
                    onClick={() => {
                      const r = makeRoute('proxy', { flowName, label: flowName })
                      setRoutes(rs => [...rs, r])
                      setActiveId(r.id)
                      setShowExistingFlowPicker(false)
                      setDeployResult(null)
                    }}
                    style={{ display: 'block', width: '100%', textAlign: 'left', background: 'none', border: 'none', cursor: 'pointer', fontSize: 11, padding: '3px 4px', borderRadius: 4, color: 'var(--text)' }}
                    onMouseEnter={e => (e.currentTarget.style.background = 'var(--block-bg)')}
                    onMouseLeave={e => (e.currentTarget.style.background = 'none')}>
                    {flowName}
                  </button>
                ))}
            </div>
          )}
        </div>
      </div>

      {/* ── Right: editor ── */}
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 14 }}>

      {/* Mode picker */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 10 }}>
        {([
          { id: 'proxy' as Mode, icon: '⚡', title: 'LLM Proxy', subtitle: 'Simple AI endpoint', desc: 'All requests go to one model. Optional fallback chain.' },
          { id: 'router' as Mode, icon: '⚖️', title: 'Token Router', subtitle: 'Route actual model by size', desc: 'Select the actual LLM based on prompt token count. Per-rule fallback chains.' },
          { id: 'classifier' as Mode, icon: '🧠', title: 'AI Classifier Router', subtitle: '2-tier: cheap classifier → best model', desc: 'Tier 1 picks a cheap classifier by token count. Tier 2 picks the actual model by classifier output. Both tiers have per-rule fallbacks. Optionally let the classifier decide if history or tools are needed per-request.' },
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

      {loadingModels && <div style={{ fontSize: 12, color: 'var(--muted)' }}>Loading models…</div>}
      {!loadingModels && models.length === 0 && (
        <div className="status-err" style={{ fontSize: 12 }}>No models registered. Go to the Models tab first.</div>
      )}

      {/* Config panel */}
      <div className="panel">
        <div className="panel-header">
          {mode === 'proxy' ? 'LLM Proxy' : mode === 'router' ? 'Token Router' : 'AI Classifier Router'} — Configuration
        </div>
        <div className="panel-body">

          {/* Shared: endpoint + input field */}
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10 }}>
            <Field label="Endpoint path" hint="URL path clients POST to">
              <input className="input" value={endpointVal} onChange={e => setEndpoint(e.target.value)} placeholder="/chat" />
            </Field>
            <Field label="Body field" hint="JSON key for the user message">
              <input className="input" value={inputFieldVal} onChange={e => setInputField(e.target.value)} placeholder="message" />
            </Field>
          </div>

          {/* ── LLM PROXY ── */}
          {mode === 'proxy' && (<>
            <Field label="System prompt" hint="Optional — persona / instructions sent to the model on every request. Leave blank to let callers control the system prompt.">
              <textarea className="input" rows={2} value={proxy.systemPrompt} onChange={e => setProxy(p => ({ ...p, systemPrompt: e.target.value }))} placeholder="(optional) You are a helpful assistant." />
            </Field>
            <SectionLabel>Model</SectionLabel>
            <Field label="Primary model" hint="Called on every request">
              <ModelSelect value={proxy.model} onChange={v => setProxy(p => ({ ...p, model: v }))} models={models} />
            </Field>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <Field label="Max tokens"><input className="input" type="number" value={proxy.maxTokens} min={1} onChange={e => setProxy(p => ({ ...p, maxTokens: e.target.value }))} /></Field>
              <Field label="Temperature"><input className="input" type="number" value={proxy.temperature} min={0} max={2} step={0.1} onChange={e => setProxy(p => ({ ...p, temperature: e.target.value }))} /></Field>
            </div>
            <SectionLabel>Fallback chain <span style={{ fontWeight: 400 }}>(tried in order on any failure)</span></SectionLabel>
            <FallbackChainEditor chain={proxy.fallbackChain} onChange={c => setProxy(p => ({ ...p, fallbackChain: c }))} models={models} />
          </>)}

          {/* ── TOKEN ROUTER ── */}
          {mode === 'router' && (<>
            <Field label="System prompt (for the actual LLM)" hint="Optional — persona / instructions passed to whichever model is selected. Leave blank to let callers include the system prompt in their request.">
              <textarea className="input" rows={2} value={router.systemPrompt} onChange={e => setRouter(r => ({ ...r, systemPrompt: e.target.value }))} placeholder="(optional) You are a helpful assistant." />
            </Field>
            <SectionLabel>Routing rules — select actual model by token count</SectionLabel>
            <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
              Pre-populated with sensible thresholds — update to match your workload. Rules are evaluated in order; first match wins. Each rule has its own fallback chain for when that model rate-limits or errors.
            </div>
            {router.routingRules.map(rule => (
              <TokenRuleRow key={rule.id} label="— select model —"
                rule={rule}
                onChangeOp={op => updateRouterRule(rule.id, { ...rule, operator: op })}
                onChangeThreshold={n => updateRouterRule(rule.id, { ...rule, threshold: n })}
                onChangeModel={v => updateRouterRule(rule.id, { ...rule, model: v })}
                onRemove={() => removeRouterRule(rule.id)}
                onChangeFallback={fc => updateRouterRule(rule.id, { ...rule, fallbackChain: fc })}
                models={models} />
            ))}
            <DefaultRow label="— select default model —" model={router.defaultModel} fallbackChain={router.defaultFallback}
              onChangeModel={v => setRouter(r => ({ ...r, defaultModel: v }))}
              onChangeFallback={fc => setRouter(r => ({ ...r, defaultFallback: fc }))}
              models={models} />
            <button className="btn" type="button" onClick={addRouterRule} style={{ width: 'auto', padding: '0 16px', marginBottom: 6, fontSize: 12 }}>+ Add Rule</button>

            <SectionLabel>Conversation history</SectionLabel>
            <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 8 }}>
              History is stored server-side by the gateway (requires a datastore). Each request is still a single call — the gateway loads the session's history, passes it to the LLM, then saves the new turn back. The client only needs to pass a session ID header.
            </div>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10, cursor: 'pointer' }}>
              <input type="checkbox" checked={router.historyEnabled} onChange={e => setRouter(r => ({ ...r, historyEnabled: e.target.checked }))} />
              <span style={{ fontSize: 13 }}>Enable per-session conversation history (server-side)</span>
            </label>
            {router.historyEnabled && (
              <div style={{ marginLeft: 24 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10 }}>
                  <Field label="Session ID header" hint="Each unique header value = one conversation thread">
                    <input className="input" value={router.sessionHeader} onChange={e => setRouter(r => ({ ...r, sessionHeader: e.target.value }))} placeholder="X-Session-Id" />
                  </Field>
                  <Field label="Max turns" hint="How many turns to keep in memory">
                    <input className="input" type="number" value={router.maxTurns} min={1} onChange={e => setRouter(r => ({ ...r, maxTurns: e.target.value }))} />
                  </Field>
                </div>
              </div>
            )}
          </>)}

          {/* ── AI CLASSIFIER ROUTER ── */}
          {mode === 'classifier' && (<>

            {/* TIER 1 */}
            <div style={{ background: 'rgba(87,181,255,0.04)', border: '1px solid var(--border-hi)', borderRadius: 8, padding: '12px 14px', marginBottom: 14 }}>
              <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--accent)', marginBottom: 4 }}>
                Tier 1 — Classifier selection
              </div>
              <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 12 }}>
                Select which <em>cheap fast model</em> to use as the classifier, based on prompt token count. For short prompts a smaller/faster model is fine; for long prompts you may want a larger-context classifier. Each rule has its own fallback chain.
              </div>

              <Field label="Classifier output field" hint={`The JSON key the classifier returns, e.g. "complexity". Tier 2 rules match on this value.`}>
                <input className="input" value={classifier.classifierField}
                  onChange={e => setClassifier(c => ({ ...c, classifierField: e.target.value }))}
                  placeholder="complexity" style={{ maxWidth: 200 }} />
              </Field>

              <Field
                label="Classifier system prompt"
                hint="Optional — instructs the classifier to return structured JSON. Leave blank to pass no system prompt (e.g. if the model is fine-tuned for structured output). Auto-appended fields appear below when checkboxes are enabled."
              >
                <textarea className="input" rows={4} value={classifier.classifierSystemPrompt}
                  onChange={e => setClassifier(c => ({ ...c, classifierSystemPrompt: e.target.value }))}
                  placeholder="(optional) Analyze the request and return ONLY valid JSON…" />
              </Field>
              <EffectivePromptPreview cfg={classifier} />

              <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 8, marginTop: 12 }}>
                Classifier selection rules — <em>evaluated in order by token count, first match wins</em>:
              </div>
              {classifier.classifierRules.map(rule => (
                <TokenRuleRow key={rule.id} label="— select classifier model —"
                  rule={{ operator: rule.operator, threshold: rule.threshold, model: rule.classifierModel, fallbackChain: rule.fallbackChain }}
                  onChangeOp={op => updateClsRule(rule.id, { ...rule, operator: op })}
                  onChangeThreshold={n => updateClsRule(rule.id, { ...rule, threshold: n })}
                  onChangeModel={v => updateClsRule(rule.id, { ...rule, classifierModel: v })}
                  onRemove={() => removeClsRule(rule.id)}
                  onChangeFallback={fc => updateClsRule(rule.id, { ...rule, fallbackChain: fc })}
                  models={models} />
              ))}
              <DefaultRow label="— select default classifier —"
                model={classifier.defaultClassifierModel}
                fallbackChain={classifier.defaultClassifierFallback}
                onChangeModel={v => setClassifier(c => ({ ...c, defaultClassifierModel: v }))}
                onChangeFallback={fc => setClassifier(c => ({ ...c, defaultClassifierFallback: fc }))}
                models={models} />
              <button className="btn" type="button" onClick={addClsRule} style={{ width: 'auto', padding: '0 16px', marginTop: 4, fontSize: 12 }}>+ Add Rule</button>
            </div>

            {/* TIER 2 */}
            <div style={{ background: 'rgba(34,197,94,0.04)', border: '1px solid var(--border-hi)', borderRadius: 8, padding: '12px 14px', marginBottom: 14 }}>
              <div style={{ fontSize: 13, fontWeight: 700, color: '#22c55e', marginBottom: 4 }}>
                Tier 2 — Actual model selection
              </div>
              <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 12 }}>
                Select the <em>actual model</em> to call based on what the classifier returned. Rules match the <code>{classifier.classifierField || 'complexity'}</code> field value. Pre-populated with typical complexity levels — update to match your classifier's output.
              </div>

              <Field
                label="System prompt for the actual LLM"
                hint="Optional — persona / instructions passed to the final model. The classifier never sees this. Leave blank if the client will send a system prompt, or if none is needed."
              >
                <textarea className="input" rows={2} value={classifier.actualSystemPrompt}
                  onChange={e => setClassifier(c => ({ ...c, actualSystemPrompt: e.target.value }))}
                  placeholder="(optional) You are a helpful assistant." />
              </Field>

              {classifier.modelRules.map(rule => (
                <ClassifierOutputRuleRow key={rule.id} rule={rule} field={classifier.classifierField || 'complexity'}
                  models={models}
                  onChange={updated => updateModelRule(rule.id, updated)}
                  onRemove={() => removeModelRule(rule.id)} />
              ))}
              <DefaultRow label="— select default model —"
                model={classifier.defaultModel}
                fallbackChain={classifier.defaultFallback}
                onChangeModel={v => setClassifier(c => ({ ...c, defaultModel: v }))}
                onChangeFallback={fc => setClassifier(c => ({ ...c, defaultFallback: fc }))}
                models={models} />
              <button className="btn" type="button" onClick={addModelRule} style={{ width: 'auto', padding: '0 16px', marginTop: 4, fontSize: 12 }}>+ Add Rule</button>
            </div>

            {/* Classifier-driven conditional features */}
            <div style={{ background: 'rgba(168,85,247,0.04)', border: '1px solid var(--border-hi)', borderRadius: 8, padding: '12px 14px', marginBottom: 14 }}>
              <div style={{ fontSize: 13, fontWeight: 700, color: '#a855f7', marginBottom: 4 }}>
                Classifier-driven decisions (optional)
              </div>
              <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 12 }}>
                Let the classifier decide per-request whether history or tools are needed. When enabled, the classifier system prompt is automatically extended with instructions for those fields. The classifier should omit the field entirely when not needed — presence means "yes".
              </div>

              <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, marginBottom: 10, cursor: 'pointer' }}>
                <input type="checkbox" style={{ marginTop: 2 }}
                  checked={classifier.classifierDecideHistory}
                  onChange={e => setClassifier(c => ({ ...c, classifierDecideHistory: e.target.checked }))} />
                <div>
                  <div style={{ fontSize: 13, fontWeight: 600 }}>Ask classifier if history is needed</div>
                  <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 2 }}>
                    Classifier returns <code>needs_history: "yes"</code> only when the request references prior turns. The gateway conditionally loads/saves session history — generates an <code>if/else</code> branch in the deployed flow. Each request is still a single HTTP call.
                  </div>
                </div>
              </label>

              <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: 'pointer' }}>
                <input type="checkbox" style={{ marginTop: 2 }}
                  checked={classifier.classifierDecideTools}
                  onChange={e => setClassifier(c => ({ ...c, classifierDecideTools: e.target.checked }))} />
                <div>
                  <div style={{ fontSize: 13, fontWeight: 600 }}>Ask classifier if tools are needed</div>
                  <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 2 }}>
                    Classifier returns <code>needs_tools: "yes"</code> only when external tools (search, APIs, calculations) are beneficial. The <code>var.needs_tools</code> slot is available in the flow for conditional MCP tool steps. Wiring to specific tools requires additional flow configuration.
                  </div>
                </div>
              </label>
            </div>

            {/* History config — shown when history is enabled (always-on or classifier-driven) */}
            <SectionLabel>Conversation history</SectionLabel>
            <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 8 }}>
              Each request is still a single HTTP call. The gateway loads session history from a datastore (Redis / Postgres), passes it to the actual model, then saves the new turn. The client sends a session ID header — no client-side state tracking needed.
            </div>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10, cursor: 'pointer' }}>
              <input type="checkbox"
                checked={classifier.historyEnabled}
                onChange={e => setClassifier(c => ({ ...c, historyEnabled: e.target.checked }))} />
              <span style={{ fontSize: 13 }}>
                Enable per-session conversation history (server-side)
                {classifier.classifierDecideHistory && !classifier.historyEnabled && <span style={{ fontSize: 11, color: '#a855f7', marginLeft: 6 }}>— enable to store history when classifier says yes</span>}
              </span>
            </label>
            {(classifier.historyEnabled || classifier.classifierDecideHistory) && (
              <div style={{ marginLeft: 24 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 10 }}>
                  <Field label="Session ID header" hint="Each unique value = one conversation thread">
                    <input className="input" value={classifier.sessionHeader} onChange={e => setClassifier(c => ({ ...c, sessionHeader: e.target.value }))} placeholder="X-Session-Id" />
                  </Field>
                  <Field label="Max turns">
                    <input className="input" type="number" value={classifier.maxTurns} min={1} onChange={e => setClassifier(c => ({ ...c, maxTurns: e.target.value }))} />
                  </Field>
                </div>
              </div>
            )}
          </>)}
        </div>
      </div>

      {/* Deploy panel */}
      <div className="panel">
        <div className="panel-header">Deploy</div>
        <div className="panel-body">
          <Field label="Flow name" hint="Internal identifier — lowercase, letters, digits, hyphens">
            <input className="input" value={flowName} onChange={e => { setFlowName(e.target.value); setDeployResult(null) }} placeholder="my-llm-proxy" />
          </Field>
          <div style={{ marginBottom: 14 }}>
            <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 6 }}>
              Generated flow — {previewSteps.length} steps (main)
              {mode === 'classifier' && (classifier.classifierDecideHistory) &&
                <span style={{ fontWeight: 400, color: 'var(--muted)', marginLeft: 6 }}>+ 2 sub-flows for if/else history branch</span>}
            </div>
            <StepPreview steps={previewSteps} />
          </div>
          <button className="btn" onClick={handleDeploy} disabled={deploying || !canDeploy} style={{ opacity: canDeploy && !deploying ? 1 : 0.45, cursor: canDeploy && !deploying ? 'pointer' : 'not-allowed' }}>
            {deploying ? 'Deploying…' : 'Deploy →'}
          </button>
          {!canDeploy && !deployResult && (
            <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 6 }}>
              {mode === 'proxy' ? 'Select a model and set an endpoint.'
                : mode === 'router' ? 'Select a default model and set an endpoint.'
                : 'Select a default classifier model, a default actual model, and set an endpoint.'}
            </div>
          )}
          {deployResult && (
            <div style={{ marginTop: 12, padding: '10px 14px', borderRadius: 6, fontSize: 12, background: deployResult.ok ? 'rgba(34,197,94,0.07)' : 'rgba(239,68,68,0.07)', border: `1px solid ${deployResult.ok ? '#22c55e44' : '#ef444444'}`, color: deployResult.ok ? '#22c55e' : '#ef4444', fontFamily: deployResult.ok ? 'monospace' : 'inherit' }}>
              {deployResult.msg}
            </div>
          )}
        </div>
      </div>

      </div>
    </div>
  )
}
