// dsl.ts — re-export DSL public API
export { parseDSL, parseParams } from './dsl_parse'
export { serializeDSL } from './dsl_serialize'

// normalizeCases converts a `cases` field from either its string form
// ("val1=flow1,val2=flow2") or its JSON-object form ({"val1":"flow1",...})
// into the canonical comma-separated string the UI expects.
export function normalizeCases(val: unknown): string {
  if (!val) return ''
  if (typeof val === 'string') return val
  if (typeof val === 'object' && !Array.isArray(val)) {
    return Object.entries(val as Record<string, string>)
      .map(([k, v]) => `${k}=${v}`)
      .join(',')
  }
  return ''
}
