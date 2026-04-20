/**
 * reconstructRoute.ts
 *
 * Reverse-engineers a SavedRoute from a deployed flow's SyncStep[] array
 * plus the API path registered for that flow.
 *
 * Handles the three modes the Studio can produce:
 *   proxy      — bind_body → (set_const) → llm_call → respond
 *   router     — bind_body → check_context_fit → route_llm → llm_call → respond
 *   classifier — bind_body → check_context_fit → route_llm → (set_const) →
 *                classify_llm → route_llm → llm_call / if → respond
 */

import type { SyncStep } from './api'

// ── Minimal type mirrors (avoids importing the full AIRoutes types) ────────────

type Op = '>' | '<' | '>=' | '<='
interface RoutingRule   { id: string; operator: Op; threshold: number; model: string; fallbackChain: string[] }
interface ClassifierTierRule { id: string; operator: Op; threshold: number; classifierModel: string; fallbackChain: string[] }
interface ModelTierRule { id: string; conditionValue: string; model: string; fallbackChain: string[] }

interface ProxyConfig {
  endpoint: string; inputField: string; model: string; fallbackChain: string[]
  systemPrompt: string; maxTokens: string; temperature: string
}
interface RouterConfig {
  endpoint: string; inputField: string; systemPrompt: string
  routingRules: RoutingRule[]; defaultModel: string; defaultFallback: string[]
  historyEnabled: boolean; sessionHeader: string; maxTurns: string
}
interface ClassifierRouterConfig {
  endpoint: string; inputField: string
  classifierSystemPrompt: string; classifierField: string
  classifierRules: ClassifierTierRule[]; defaultClassifierModel: string; defaultClassifierFallback: string[]
  actualSystemPrompt: string
  modelRules: ModelTierRule[]; defaultModel: string; defaultFallback: string[]
  classifierDecideHistory: boolean; classifierDecideTools: boolean
  historyEnabled: boolean; sessionHeader: string; maxTurns: string
}

export interface ReconstructedRoute {
  flowName: string
  label:    string
  mode:     'proxy' | 'router' | 'classifier'
  proxy:      ProxyConfig
  router:     RouterConfig
  classifier: ClassifierRouterConfig
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function step(steps: SyncStep[], action: string): SyncStep | undefined {
  return steps.find(s => s.action === action)
}
function steps(ss: SyncStep[], action: string): SyncStep[] {
  return ss.filter(s => s.action === action)
}

function parseRules(rulesJson: string): Array<{ condition: string; model: string; fallback?: string[] }> {
  try { return JSON.parse(rulesJson) } catch { return [] }
}

function parseFBM(fbmJson: string | undefined): Record<string, string[]> {
  if (!fbmJson) return {}
  try { return JSON.parse(fbmJson) } catch { return {} }
}

function inferFallbackChain(model: string, fbm: Record<string, string[]>): string[] {
  return fbm[model] ?? []
}

// token_count > 8000  →  { operator: '>', threshold: 8000 }
function parseCondition(cond: string): { operator: Op; threshold: number } | null {
  const m = cond.match(/token_count\s*(>=|<=|>|<)\s*(\d+)/)
  if (!m) return null
  return { operator: m[1] as Op, threshold: parseInt(m[2]) }
}

// var.complexity == low  →  'low'
function parseOutputCondition(cond: string): string | null {
  const m = cond.match(/==\s*(\S+)/)
  return m ? m[1] : null
}

const DEFAULT_PROXY: ProxyConfig = {
  endpoint: '/chat', inputField: 'message', model: '', fallbackChain: [],
  systemPrompt: '', maxTokens: '2000', temperature: '0.7',
}
const DEFAULT_ROUTER: RouterConfig = {
  endpoint: '/ai/chat', inputField: 'message', systemPrompt: '',
  routingRules: [], defaultModel: '', defaultFallback: [],
  historyEnabled: false, sessionHeader: 'X-Session-Id', maxTurns: '20',
}
const DEFAULT_CLASSIFIER: ClassifierRouterConfig = {
  endpoint: '/ai/smart-chat', inputField: 'message',
  classifierSystemPrompt: '', classifierField: 'complexity',
  classifierRules: [], defaultClassifierModel: '', defaultClassifierFallback: [],
  actualSystemPrompt: '', modelRules: [], defaultModel: '', defaultFallback: [],
  classifierDecideHistory: false, classifierDecideTools: false,
  historyEnabled: false, sessionHeader: 'X-Session-Id', maxTurns: '20',
}

// ── Main reconstruction function ─────────────────────────────────────────────

export function reconstructRoute(
  flowName: string,
  instructions: SyncStep[],
  endpoint: string,
): ReconstructedRoute | null {
  const hasClassify = instructions.some(s => s.action === 'classify_llm')
  const hasRouteLLM = instructions.some(s => s.action === 'route_llm')
  const hasCtxFit   = instructions.some(s => s.action === 'check_context_fit')

  const mode: 'proxy' | 'router' | 'classifier' =
    hasClassify ? 'classifier' : (hasRouteLLM || hasCtxFit) ? 'router' : 'proxy'

  const bindBody   = step(instructions, 'bind_body')
  const inputField = bindBody?.key || bindBody?.key_identifier || 'message'

  if (mode === 'proxy') {
    const llmCall = step(instructions, 'llm_call')
    if (!llmCall) return null
    const fbm = parseFBM(llmCall.input?.fallback_by_model)
    const model = llmCall.input?.model || ''
    const sysConst = step(instructions, 'set_const')
    return {
      flowName, label: flowName, mode,
      proxy: {
        ...DEFAULT_PROXY,
        endpoint, inputField,
        model,
        fallbackChain: inferFallbackChain(model, fbm),
        systemPrompt: sysConst?.value || '',
        maxTokens: llmCall.input?.max_tokens || '2000',
        temperature: llmCall.input?.temperature || '0.7',
      },
      router: DEFAULT_ROUTER,
      classifier: DEFAULT_CLASSIFIER,
    }
  }

  if (mode === 'router') {
    const routeLLM = step(instructions, 'route_llm')
    const llmCall  = step(instructions, 'llm_call')
    const sysConst = step(instructions, 'set_const')
    const hasHistory = instructions.some(s => s.action === 'load_history')
    const bindHdr  = step(instructions, 'bind_header')
    const appendMsg = step(instructions, 'append_message')

    const rawRules = parseRules(routeLLM?.input?.rules || '[]')
    const fbm      = parseFBM(llmCall?.input?.fallback_by_model)
    const defaultModel = routeLLM?.input?.default || llmCall?.input?.model || ''

    const routingRules: RoutingRule[] = rawRules
      .filter(r => r.condition !== 'true')
      .map((r, i) => {
        const cond = parseCondition(r.condition)
        return {
          id: String(i + 1),
          operator: cond?.operator ?? '>',
          threshold: cond?.threshold ?? 1000,
          model: r.model,
          fallbackChain: r.fallback ?? inferFallbackChain(r.model, fbm),
        }
      })
    const defaultRule = rawRules.find(r => r.condition === 'true')
    const defaultFallback = defaultRule?.fallback ?? inferFallbackChain(defaultModel, fbm)

    return {
      flowName, label: flowName, mode,
      proxy: DEFAULT_PROXY,
      router: {
        ...DEFAULT_ROUTER,
        endpoint, inputField,
        systemPrompt: sysConst?.value || '',
        routingRules,
        defaultModel,
        defaultFallback,
        historyEnabled: hasHistory,
        sessionHeader: bindHdr?.key || 'X-Session-Id',
        maxTurns: appendMsg?.input?.max_turns || '20',
      },
      classifier: DEFAULT_CLASSIFIER,
    }
  }

  // ── classifier mode ──────────────────────────────────────────────────────────
  const routeLLMSteps = steps(instructions, 'route_llm')
  const clsRouteLLM   = routeLLMSteps[0]  // tier-1: selects classifier
  const mdlRouteLLM   = routeLLMSteps[1]  // tier-2: selects actual model

  const classifyStep  = step(instructions, 'classify_llm')
  const llmCallStep   = step(instructions, 'llm_call')
  const ifStep        = step(instructions, 'if')

  // System prompts: first set_const is actual LLM system, second is classifier system
  const constSteps    = steps(instructions, 'set_const')
  // Identify which slot each set_const writes to
  const actualSysConst = constSteps.find(s => s.as === 'var.system')
  const clsSysConst    = constSteps.find(s => s.as === 'var.cls_system')

  // Classifier field (e.g. "complexity") from classify_llm input keys
  const classifyInput = classifyStep?.input || {}
  // The classify_llm input has entries like { complexity: "var.complexity", needs_history: "var.needs_history" }
  // We want the field name that is NOT a reserved key
  const reservedKeys = new Set(['model', 'model_slot', 'fallback_by_model', 'system_slot',
    'needs_history', 'needs_tools'])
  const classifierField = Object.keys(classifyInput).find(k => !reservedKeys.has(k)) || 'complexity'

  const classifierDecideHistory = 'needs_history' in classifyInput
  const classifierDecideTools   = 'needs_tools' in classifyInput

  // Tier-1 rules
  const clsRawRules = parseRules(clsRouteLLM?.input?.rules || '[]')
  const clsFBM      = parseFBM(classifyStep?.input?.fallback_by_model)
  const defaultClassifierModel = clsRouteLLM?.input?.default || classifyStep?.input?.model || ''
  const clsRules: ClassifierTierRule[] = clsRawRules
    .filter(r => r.condition !== 'true')
    .map((r, i) => {
      const cond = parseCondition(r.condition)
      return {
        id: String(i + 1),
        operator: cond?.operator ?? '>',
        threshold: cond?.threshold ?? 1000,
        classifierModel: r.model,
        fallbackChain: r.fallback ?? inferFallbackChain(r.model, clsFBM),
      }
    })
  const clsDefaultRule    = clsRawRules.find(r => r.condition === 'true')
  const defaultClsFallback = clsDefaultRule?.fallback ?? inferFallbackChain(defaultClassifierModel, clsFBM)

  // Tier-2 rules
  const mdlRawRules = parseRules(mdlRouteLLM?.input?.rules || '[]')
  const mdlFBM      = parseFBM(llmCallStep?.input?.fallback_by_model)
  const defaultModel = mdlRouteLLM?.input?.default || llmCallStep?.input?.model || ''
  const modelRules: ModelTierRule[] = mdlRawRules
    .filter(r => r.condition !== 'true')
    .map((r, i) => {
      const val = parseOutputCondition(r.condition)
      return {
        id: String(i + 1),
        conditionValue: val ?? '',
        model: r.model,
        fallbackChain: r.fallback ?? inferFallbackChain(r.model, mdlFBM),
      }
    })
  const mdlDefaultRule  = mdlRawRules.find(r => r.condition === 'true')
  const defaultFallback = mdlDefaultRule?.fallback ?? inferFallbackChain(defaultModel, mdlFBM)

  // History settings — check if hist-on sub-flow is referenced, or load_history in main flow
  const historyEnabled    = instructions.some(s => s.action === 'load_history')
  const bindHdr           = step(instructions, 'bind_header')
  const appendMsg         = instructions.find(s => s.action === 'append_message')
  const sessionHeader     = bindHdr?.key || 'X-Session-Id'
  const maxTurns          = appendMsg?.input?.max_turns || '20'

  // If/else history branch — classifierDecideHistory is true when there's an `if` step
  const realClassifierDecideHistory = classifierDecideHistory || !!ifStep

  return {
    flowName, label: flowName, mode,
    proxy: DEFAULT_PROXY,
    router: DEFAULT_ROUTER,
    classifier: {
      ...DEFAULT_CLASSIFIER,
      endpoint, inputField,
      classifierSystemPrompt: clsSysConst?.value || '',
      classifierField,
      classifierRules: clsRules,
      defaultClassifierModel,
      defaultClassifierFallback: defaultClsFallback,
      actualSystemPrompt: actualSysConst?.value || '',
      modelRules,
      defaultModel,
      defaultFallback,
      classifierDecideHistory: realClassifierDecideHistory,
      classifierDecideTools,
      historyEnabled,
      sessionHeader,
      maxTurns,
    },
  }
}
