import type { FlowStep, SavedFlow, ApiDef } from '../types'

export interface ValidationError {
  severity: 'error' | 'warn'
  flow: string
  stepIdx?: number
  message: string
}

// ── Reference helpers ──────────────────────────────────────────────

function collectRefs(steps: FlowStep[]): string[] {
  const refs: string[] = []
  for (const s of steps) {
    if (s['then'])      refs.push(s['then'] as string)
    if (s['else'])      refs.push(s['else'] as string)
    if (s['flow_name']) refs.push(s['flow_name'] as string)
    if (s['cases']) {
      String(s['cases']).split(',').forEach(c => {
        const eq = c.indexOf('='); if (eq >= 0) refs.push(c.slice(eq + 1).trim())
      })
    }
    if (s.then_steps) refs.push(...collectRefs(s.then_steps))
    if (s.else_steps)  refs.push(...collectRefs(s.else_steps))
  }
  return refs.filter(Boolean)
}

// ── Variable production per step ───────────────────────────────────

function producedBy(s: FlowStep): string[] {
  const out: string[] = []
  if (s['as'] && typeof s['as'] === 'string') out.push(s['as'])
  if (s['key_identifier'] && typeof s['key_identifier'] === 'string') out.push(s['key_identifier'])
  if (s['out'] && typeof s['out'] === 'string') out.push(s['out'])
  return out
}

// ── Extract variable references from a field value string ──────────

const VAR_PATTERN = /\b(var\.[a-zA-Z_][a-zA-Z0-9_]*|_h_[a-zA-Z_][a-zA-Z0-9_]*|_q_[a-zA-Z_][a-zA-Z0-9_]*|_b_[a-zA-Z_][a-zA-Z0-9_]*|_p_[a-zA-Z_][a-zA-Z0-9_]*)\b/g

function extractVarRefs(value: unknown): string[] {
  if (typeof value !== 'string') return []
  return [...value.matchAll(VAR_PATTERN)].map(m => m[1])
}

// Fields that may reference variables
const VAR_FIELDS = ['source','input','as','key_identifier','condition','then','else',
  'flow_name','url','url_var','payload_slot','model_slot','session_slot',
  'value','default','query','filter']

// Patterns that are always valid (request attributes, never user-defined)
function isAlwaysValid(varRef: string): boolean {
  return /^(header|queryparam|query|body|path|upstream|request|response)\./.test(varRef)
}

// ── Main validation function ───────────────────────────────────────

export function validateFlows(savedFlows: SavedFlow[], apis: ApiDef[]): ValidationError[] {
  const errors: ValidationError[] = []
  const flowNames = new Set(savedFlows.map(f => f.name))

  for (const flow of savedFlows) {
    const definedVars: string[] = [] // accumulates as we walk steps in order

    for (let i = 0; i < flow.steps.length; i++) {
      const step = flow.steps[i]

      // 1. Check subflow references
      const refs = collectRefs([step])
      for (const ref of refs) {
        if (ref && !ref.startsWith('__auto_') && !flowNames.has(ref)) {
          errors.push({
            severity: 'error',
            flow: flow.name,
            stepIdx: i,
            message: `Step ${i + 1} (${step.action}) references flow "${ref}" which does not exist.`,
          })
        }
      }

      // 2. Check variable usage against what's been defined so far
      for (const field of VAR_FIELDS) {
        const val = step[field]
        if (!val) continue
        for (const ref of extractVarRefs(val)) {
          if (!isAlwaysValid(ref) && !definedVars.includes(ref)) {
            errors.push({
              severity: 'warn',
              flow: flow.name,
              stepIdx: i,
              message: `Step ${i + 1} (${step.action}) uses "${ref}" in "${field}" but it has not been set by any earlier step.`,
            })
          }
        }
      }

      // After checking, record what this step produces
      definedVars.push(...producedBy(step))

      // For call steps, also include what the subflow produces
      if (step.action === 'call' && typeof step['flow_name'] === 'string') {
        const sub = savedFlows.find(f => f.name === step['flow_name'])
        if (sub) sub.steps.forEach(ss => definedVars.push(...producedBy(ss)))
      }
    }
  }

  // 3. API-level: every defaultFlow must exist with steps
  for (const api of apis) {
    if (api.defaultFlow && !savedFlows.find(f => f.name === api.defaultFlow)?.steps.length) {
      errors.push({
        severity: 'error',
        flow: api.defaultFlow,
        message: `API "${api.name}" defaultFlow "${api.defaultFlow}" has no steps.`,
      })
    }
  }

  return errors
}
