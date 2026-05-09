import type { FlowStep } from '../types'

export type SlotSource = 'registry' | 'cache' | 'upstream' | 'computed' | 'extract'

export interface SlotProducer {
  stepIndex: number
  action: string
  source: SlotSource
  // For registry: what registry key/type it loaded from (derived from step fields)
  registryKey?: string
}

export interface LineageMap {
  // slot name (e.g. "var.auth_ok") → who produces it
  producers: Map<string, SlotProducer>
  // slot name → which step indices consume it
  consumers: Map<string, number[]>
}

const REGISTRY_ACTIONS = new Set([
  'load_service_url', 'load_service_url_var', 'load_identifier', 'set_meta', 'registry_lookup',
  'get_service_url',
])
const CACHE_ACTIONS = new Set([
  'semantic_cache_get', 'cache_get', 'load_history',
])
const UPSTREAM_ACTIONS = new Set([
  'http_call', 'llm_call', 'mcp_call_tool', 'execute_plan',
  'vector_search', 'embed_text', 'route_llm',
])
const EXTRACT_ACTIONS = new Set([
  'extract', 'bind_body', 'bind_header', 'bind_query', 'bind_path',
])

function classifySource(action: string): SlotSource {
  if (REGISTRY_ACTIONS.has(action)) return 'registry'
  if (CACHE_ACTIONS.has(action)) return 'cache'
  if (UPSTREAM_ACTIONS.has(action)) return 'upstream'
  if (EXTRACT_ACTIONS.has(action)) return 'extract'
  return 'computed'
}

// Find all var.xxx references in a string value
function findVarRefs(text: string): string[] {
  const matches = text.match(/\bvar\.[a-z_][a-z0-9_.]*\b/gi)
  return matches ? [...new Set(matches)] : []
}

export function buildLineage(steps: FlowStep[]): LineageMap {
  const producers = new Map<string, SlotProducer>()
  const consumers = new Map<string, number[]>()

  for (let i = 0; i < steps.length; i++) {
    const step = steps[i]

    // Record what this step produces (via 'as' field)
    const asSlot = step['as'] as string | undefined
    if (asSlot && asSlot.startsWith('var.')) {
      const registryKey = (step['key_identifier'] as string | undefined) || (step['key'] as string | undefined) || undefined
      producers.set(asSlot, {
        stepIndex: i,
        action: step.action,
        source: classifySource(step.action),
        registryKey,
      })
    }

    // Record what this step consumes (all var. refs in all field values except 'as')
    for (const [k, v] of Object.entries(step)) {
      if (k === 'action' || k === 'as') continue
      const refs = findVarRefs(String(v ?? ''))
      for (const ref of refs) {
        const list = consumers.get(ref) ?? []
        if (!list.includes(i)) list.push(i)
        consumers.set(ref, list)
      }
    }
  }

  return { producers, consumers }
}

// Returns a human-readable annotation for where a slot value came from.
// E.g.: "step 3 · load_service_url · from tenant registry (key: primary)"
export function producerAnnotation(
  lineage: LineageMap,
  slotName: string,
  stepLabels: Record<string, string>,
): string | null {
  const p = lineage.producers.get(slotName)
  if (!p) return null

  const stepNum = p.stepIndex + 1
  const label = stepLabels[String(p.stepIndex)] || p.action
  const sourceStr =
    p.source === 'registry' ? `from tenant registry${p.registryKey ? ` · key "${p.registryKey}"` : ''}` :
    p.source === 'cache'    ? 'from cache' :
    p.source === 'upstream' ? 'from upstream call' :
    p.source === 'extract'  ? 'extracted from request' :
    ''

  return `step ${stepNum} · ${label}${sourceStr ? ` · ${sourceStr}` : ''}`
}
