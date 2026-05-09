/**
 * Expression utilities for the FlowDesigner.
 *
 * Supports source-reference syntax in condition / slot fields:
 *   header.<Name>          — HTTP request header
 *   queryparam.<key>       — URL query parameter   (alias: query.<key>)
 *   body.<jsonpath>        — JSON request body field
 *   path.<param>           — URL path parameter
 *
 * Example conditions:
 *   header.X-TID == "first"
 *   queryparam.mode != "test" && header.Authorization
 *   body.userId
 *
 * expandSteps() rewrites a FlowStep list so every source reference is
 * preceded by an auto-injected `extract` step that binds the value to a
 * deterministic slot name.  The original step's field values are rewritten
 * to use that slot name so the Gateway compiler sees clean slot identifiers.
 */

import type { FlowStep } from '../types'

// ── Types ────────────────────────────────────────────────────────

export type SourceKind = 'header' | 'query' | 'body' | 'path'

export interface SourceRef {
  /** Original matched text, e.g. "header.X-TID" */
  raw: string
  /** Normalised kind */
  source: SourceKind
  /** Key after the dot, e.g. "X-TID" */
  key: string
  /** Auto-generated slot name, e.g. "_h_x_tid" */
  slotName: string
}

// ── Regex ────────────────────────────────────────────────────────

// Matches header.X / queryparam.X / query.X / body.X / path.X
// Note: uses a capturing group for aliased names (queryparam ↔ query)
const SOURCE_RE = /\b(header|queryparam|query|body|path)\.([A-Za-z0-9._-]+)/g

// ── Helpers ──────────────────────────────────────────────────────

function normaliseKind(raw: string): SourceKind {
  if (raw === 'queryparam' || raw === 'query') return 'query'
  return raw as SourceKind
}

const KIND_PREFIX: Record<SourceKind, string> = {
  header: '_h_',
  query:  '_q_',
  body:   '_b_',
  path:   '_p_',
}

export function autoSlotName(source: SourceKind, key: string): string {
  const clean = key
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')   // non-alphanum → _
    .replace(/^_+|_+$/g, '')        // trim leading/trailing _
  return KIND_PREFIX[source] + clean
}

// ── Core parsing ─────────────────────────────────────────────────

/**
 * Find all source references in a text string.
 * Each unique raw match appears at most once.
 */
export function findSourceRefs(text: string): SourceRef[] {
  if (!text) return []
  const refs: SourceRef[] = []
  const seen = new Set<string>()
  SOURCE_RE.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = SOURCE_RE.exec(text)) !== null) {
    const raw = m[0]
    if (seen.has(raw)) continue
    seen.add(raw)
    const source = normaliseKind(m[1])
    const key    = m[2]
    refs.push({ raw, source, key, slotName: autoSlotName(source, key) })
  }
  return refs
}

/**
 * Replace all source references in `text` with their slot names.
 * Also handles the queryparam ↔ query alias.
 */
export function substituteRefs(text: string, refs: SourceRef[]): string {
  let result = text
  for (const ref of refs) {
    // Replace the matched variant
    result = result.split(ref.raw).join(ref.slotName)
    // Replace the alias variant if applicable
    if (ref.source === 'query') {
      const alias = ref.raw.startsWith('queryparam.')
        ? ref.raw.replace('queryparam.', 'query.')
        : ref.raw.replace('query.', 'queryparam.')
      result = result.split(alias).join(ref.slotName)
    }
  }
  return result
}

// ── Step expansion ───────────────────────────────────────────────

/**
 * Rewrite a step list so every source reference is auto-extracted.
 *
 * Rules:
 * - `extract` steps are passed through as-is; their `as` slot is registered
 *   so we never double-inject for the same slot name.
 * - For all other steps every field value (except `action`) is scanned.
 * - For each new source ref found, an `extract` step is injected immediately
 *   before the step that first uses it.
 * - The using step's field values are rewritten with the slot name.
 */
export function expandSteps(rawSteps: FlowStep[]): FlowStep[] {
  const result:   FlowStep[]    = []
  const injected: Set<string>   = new Set()   // slot names already handled

  for (const step of rawSteps) {
    // ── Explicit extract steps ────────────────────────────────
    if (step.action === 'extract') {
      const asVal = step['as'] as string | undefined
      if (asVal) injected.add(asVal)
      result.push(step)
      continue
    }

    // ── All other steps ───────────────────────────────────────
    // Collect source refs from every field value (skip nested branch arrays and non-string fields)
    // trace_vars is a string[] and must not be stringified through substituteRefs
    const ARRAY_FIELDS = new Set(['then_steps', 'else_steps', 'trace_vars'])
    const allRefs: SourceRef[] = []
    for (const [k, v] of Object.entries(step)) {
      if (k === 'action' || ARRAY_FIELDS.has(k)) continue
      allRefs.push(...findSourceRefs(String(v ?? '')))
    }

    // Inject extract steps for refs not yet handled
    for (const ref of allRefs) {
      if (injected.has(ref.slotName)) continue
      injected.add(ref.slotName)
      result.push({
        action:    'extract',
        condition: ref.raw,
        as:        ref.slotName,
      } as unknown as FlowStep)
    }

    // Substitute refs in field values (preserve array fields verbatim)
    let resultStep: FlowStep
    if (allRefs.length > 0) {
      const expanded: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(step)) {
        if (ARRAY_FIELDS.has(k)) { expanded[k] = v; continue }
        expanded[k] = k === 'action'
          ? (v as string)
          : substituteRefs(String(v ?? ''), allRefs)
      }
      resultStep = expanded as unknown as FlowStep
    } else {
      resultStep = { ...step }
    }

    // Recurse into inline branches
    if (step.then_steps?.length) resultStep.then_steps = expandSteps(step.then_steps)
    if (step.else_steps?.length) resultStep.else_steps = expandSteps(step.else_steps)

    result.push(resultStep)
  }

  return result
}

// ── Smart condition builder ──────────────────────────────────────

/**
 * Attempt to make sense of minimal user input and produce a well-formed
 * expression string.  Examples:
 *
 *   "X-TID"              → "header.X-TID"          (header shorthand)
 *   "X-TID == first"     → "header.X-TID == \"first\""
 *   "mode"               → "queryparam.mode"        (query shorthand)
 *   "mode=debug"         → "queryparam.mode == \"debug\""
 *   "body.userId"        → "body.userId"            (already qualified)
 *   "header.X-TID"       → "header.X-TID"           (unchanged)
 *
 * Heuristics:
 * - If the token contains a dot and starts with a known source prefix → keep as-is.
 * - If the token looks like an HTTP header name (contains - or starts with X-) → prefix header.
 * - Otherwise → prefix queryparam.
 */
export function smartCondition(raw: string): string {
  const trimmed = raw.trim()
  if (!trimmed) return trimmed

  // Already has a fully-qualified source prefix — return as-is
  if (/^(header|queryparam|query|body|path)\./i.test(trimmed)) return trimmed

  // Split on == / != / = (capture separator and RHS)
  const opMatch = trimmed.match(/^([^\s=!<>]+)\s*(==|!=|>=|<=|>|<|=)\s*(.+)$/)
  if (opMatch) {
    const lhs = opMatch[1].trim()
    const op  = opMatch[2] === '=' ? '==' : opMatch[2]
    let rhs   = opMatch[3].trim()
    // Quote RHS if it's bare (not already quoted, not a number, not a source ref)
    if (!/^["']/.test(rhs) && !/^\d/.test(rhs) && !SOURCE_RE.test(rhs)) {
      rhs = `"${rhs}"`
    }
    const qualLhs = qualifyLHS(lhs)
    return `${qualLhs} ${op} ${rhs}`
  }

  // No operator — just a presence check
  return qualifyLHS(trimmed)
}

function qualifyLHS(token: string): string {
  if (/^(header|queryparam|query|body|path)\./i.test(token)) return token
  // Looks like a header (contains - or starts with X- or common header names)
  if (/[-]/.test(token) || /^(X-|Authorization|Accept|Content-|User-Agent)/i.test(token)) {
    return `header.${token}`
  }
  // Default → query param
  return `queryparam.${token}`
}
