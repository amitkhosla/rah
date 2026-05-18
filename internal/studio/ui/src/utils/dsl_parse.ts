/**
 * dsl_parse.ts  —  RAH Flow DSL parser
 * Exports: parseDSL, parseParams, parsePatternCondition, parseConditionFromStep
 */
import type { FlowStep, PatternCondition } from '../types'

// ── Template slot counter (reset per parseDSL call) ─────────────────────────
let _tc = 0
const tpl = () => `__t${_tc++}`

// ── Parameter parser ─────────────────────────────────────────────────────────
// Parses "key: val, key2: \"quoted\", key3: {json}" → Record<string,string>
export function parseParams(raw: string): Record<string, string> {
  const out: Record<string, string> = {}
  if (!raw.trim()) return out
  let i = 0; const n = raw.length
  const ws = () => { while (i < n && /\s/.test(raw[i])) i++ }
  while (i < n) {
    ws(); if (i >= n) break
    const ks = i; while (i < n && raw[i] !== ':') i++
    const key = raw.slice(ks, i).trim()
    if (!key || i >= n) break; i++; ws()
    let val = ''
    if (raw[i] === '"' || raw[i] === "'") {
      const q = raw[i++]; const vs = i
      while (i < n && raw[i] !== q) { if (raw[i] === '\\') i++; i++ }
      val = raw.slice(vs, i).replace(/\\(["'])/g, '$1'); if (i < n) i++
    } else if (raw[i] === '{' || raw[i] === '[') {
      const open = raw[i]; const close = open === '{' ? '}' : ']'
      let d = 0; const vs = i
      while (i < n) { if (raw[i] === open) d++; else if (raw[i] === close) { d--; if (!d) { i++; break } } i++ }
      val = raw.slice(vs, i).trim()
    } else {
      const vs = i; while (i < n && raw[i] !== ',') i++; val = raw.slice(vs, i).trim()
    }
    if (key) out[key] = val; ws(); if (i < n && raw[i] === ',') i++
  }
  return out
}

// ── Function call splitter ───────────────────────────────────────────────────
// "http.get(url: x, timeout: 5)" → { action: "http.get", rawParams: "url: x, timeout: 5" }
function split(text: string): { action: string; rawParams: string } | null {
  const lp = text.indexOf('('); if (lp < 0) return null
  const action = text.slice(0, lp).trim(); if (!action) return null
  let d = 0; let rp = -1
  for (let k = lp; k < text.length; k++) {
    if ('([{'.includes(text[k])) d++; else if (')]}'.includes(text[k])) { d--; if (!d) { rp = k; break } }
  }
  if (rp < 0) return null
  return { action, rawParams: text.slice(lp + 1, rp) }
}

// ── Strip surrounding quotes ─────────────────────────────────────────────────
const unquote = (s: string) => s.trim().replace(/^["']|["']$/g, '')

// ── Source prefix map ────────────────────────────────────────────────────────
const SRC_FNS = new Set(['header', 'query', 'body', 'path', 'client_ip', 'correlation_id', 'transaction_id'])

// ── Source function → bind step ──────────────────────────────────────────────
// Maps DSL source functions to the compiler actions the Go backend understands.
// - bind_header / bind_query_param / bind_body  → explicit binding instructions
// - path("x")  → no explicit bind needed; the sub-router auto-populates path.x
//   at request time.  We emit a concat (no key_identifier, empty value) which
//   simply copies path.x into the user's chosen variable name.
function srcStep(fn: string, rawArgs: string, slot: string): FlowStep | null {
  const a = unquote(rawArgs)
  const mk = (o: object) => o as unknown as FlowStep
  switch (fn) {
    case 'header':         return mk({ action: 'bind_header',        key: a,              as: slot })
    case 'query':          return mk({ action: 'bind_query_param',   key: a,              as: slot })
    case 'body':           return mk({ action: 'bind_body',          key: a,              as: slot })
    case 'path':           return mk({ action: 'concat', source: `path.${a}`, as: slot })
    case 'client_ip':      return mk({ action: 'bind_client_ip',     key_identifier: slot })
    case 'correlation_id': return mk({ action: 'bind_correlation_id', key: a || 'X-Correlation-ID', as: slot, generate_if_missing: true })
    case 'transaction_id': return mk({ action: 'store_internal_tx_id', as: slot })
    default:               return null
  }
}

// ── Template string → concat step chain ─────────────────────────────────────
// "Hello {name}, score {score}!" → set_const + concat steps, returns final slot name
function tplSteps(tmpl: string): { steps: FlowStep[]; slot: string } {
  // Split into alternating text/variable segments
  const segs: Array<{ k: 'text' | 'var'; v: string }> = []
  let last = 0
  const re = /\{(\w+)\}/g; let m: RegExpExecArray | null
  while ((m = re.exec(tmpl)) !== null) {
    if (m.index > last) segs.push({ k: 'text', v: tmpl.slice(last, m.index) })
    segs.push({ k: 'var', v: m[1] })
    last = m.index + m[0].length
  }
  if (last < tmpl.length) segs.push({ k: 'text', v: tmpl.slice(last) })

  // Pure text — no variables
  if (segs.every(s => s.k === 'text')) {
    const slot = tpl()
    return { steps: [{ action: 'set_const', value: segs.map(s => s.v).join(''), as: slot } as unknown as FlowStep], slot }
  }

  const steps: FlowStep[] = []
  const mk = (o: object) => o as unknown as FlowStep
  let acc: string | null = null   // current accumulated slot

  for (const seg of segs) {
    if (seg.k === 'text') {
      if (!seg.v) continue
      const s = tpl()
      steps.push(mk({ action: 'set_const', value: seg.v, as: s }))
      if (acc === null) { acc = s }
      else { const out = tpl(); steps.push(mk({ action: 'concat', key_identifier: acc, source: s, as: out })); acc = out }
    } else {
      if (acc === null) { acc = seg.v }
      else { const out = tpl(); steps.push(mk({ action: 'concat', key_identifier: acc, source: seg.v, as: out })); acc = out }
    }
  }

  return { steps, slot: acc ?? tpl() }
}

// ── output.* statement → FlowStep[] ─────────────────────────────────────────
function outputSteps(lhs: string, rhs: string): FlowStep[] {
  const mk = (o: object) => o as unknown as FlowStep
  const r = rhs.trim()

  if (lhs === 'output.status') return [mk({ action: 'set_response_status', value: r })]

  const hm = lhs.match(/^output\.header\(["']?([^"')]+)["']?\)$/)
  if (hm) return [mk({ action: 'set_response_header', key: hm[1], source: r })]

  if (lhs === 'output.body') {
    // Quoted template string
    if (r.startsWith('"') || r.startsWith("'")) {
      const inner = r.slice(1, r.lastIndexOf(r[0]))
      if (inner.includes('{')) {
        const { steps, slot } = tplSteps(inner)
        return [...steps, mk({ action: 'set_response_body', source: slot })]
      }
      const s = tpl()
      return [mk({ action: 'set_const', value: inner, as: s }), mk({ action: 'set_response_body', source: s })]
    }
    // Variable reference
    return [mk({ action: 'set_response_body', source: r })]
  }

  return []
}

// ── Action call → FlowStep[] ─────────────────────────────────────────────────
function actionSteps(action: string, rawParams: string, as_?: string): FlowStep[] {
  const p = parseParams(rawParams)
  const mk = (o: object) => o as unknown as FlowStep
  const withAs = as_ ? { as: as_ } : {}

  // Positional helpers — first N comma-separated tokens before any "key: val" pair
  const positional = (n: number): string[] => {
    const out: string[] = []
    let i = 0; let depth = 0; let cur = ''
    while (i < rawParams.length && out.length < n) {
      const c = rawParams[i]
      if ('([{'.includes(c)) depth++
      else if (')]}'.includes(c)) depth--
      else if (c === ',' && !depth) {
        if (!cur.includes(':')) { out.push(cur.trim()); cur = '' } else break
        i++; continue
      }
      cur += c; i++
    }
    if (cur.trim() && !cur.includes(':') && out.length < n) out.push(cur.trim())
    return out
  }

  switch (action) {
    // ── Cache ──────────────────────────────────────────────────────────────
    case 'cache.get':
      return [mk({ action: 'cache_get',      key_identifier: positional(1)[0] ?? rawParams.trim(), ...withAs })]
    case 'shared_cache.get':
      return [mk({ action: 'cache_get_global', key_identifier: unquote(positional(1)[0] ?? rawParams), ...withAs })]
    case 'cache.set': {
      const pos = positional(2)
      return [mk({ action: 'cache_put', key_identifier: pos[0] ?? '', source: pos[1] ?? '', ttl: p['ttl'] ?? '300' })]
    }
    case 'shared_cache.set': {
      const pos = positional(2)
      return [mk({ action: 'cache_put_global', key_identifier: unquote(pos[0] ?? ''), source: pos[1] ?? '', ttl: p['ttl'] ?? '3600' })]
    }
    case 'cache.delete':
      return [mk({ action: 'cache_delete', key_identifier: positional(1)[0] ?? rawParams.trim() })]
    case 'shared_cache.delete':
      return [mk({ action: 'cache_delete_global', key_identifier: unquote(positional(1)[0] ?? rawParams) })]
    case 'cache.get_batched':
      return [mk({ action: 'cache_get_batched', variable: positional(1)[0] ?? '', destination: p['into'] ?? '' })]
    case 'batch_flush':
      return [mk({ action: 'batch_flush' })]
    case 'json_extract':
      return [mk({ action: 'json_extract_emit', variable: positional(1)[0] ?? '', params: p['ops'] ?? '' })]
    case 'json_foreach':
      return [mk({ action: 'json_foreach_emit', variable: positional(1)[0] ?? '', path: p['array'] ?? '', params: p['ops'] ?? '' })]

    // ── Registry ───────────────────────────────────────────────────────────
    case 'registry.lookup': {
      const arg = rawParams.trim()
      const inner = split(arg)
      let keyId = arg
      if (inner && SRC_FNS.has(inner.action)) {
        const a2 = unquote(inner.rawParams)
        const prefix = inner.action === 'header' ? 'header' : inner.action === 'query' ? 'queryparam' : inner.action
        keyId = `${prefix}.${a2}`
      }
      return [mk({ action: 'registry_lookup', key_identifier: keyId })]
    }
    case 'registry.url':
      return [mk({ action: 'load_service_url', key: unquote(positional(1)[0] ?? ''), ...withAs })]
    case 'registry.url_var':
      return [mk({ action: 'load_service_url_var', key_identifier: unquote(positional(1)[0] ?? ''), ...withAs })]
    case 'registry.id':
      return [mk({ action: 'load_identifier', key: unquote(positional(1)[0] ?? ''), ...withAs })]
    case 'registry.meta':
      return [mk({ action: 'load_meta', key: unquote(positional(1)[0] ?? ''), ...withAs })]
    case 'registry.set_url': {
      const pos = positional(2)
      return [mk({ action: 'set_service_url', key: unquote(pos[0] ?? ''), source: pos[1] ?? '' })]
    }
    case 'registry.set_id': {
      const pos = positional(2)
      return [mk({ action: 'set_identifier', key: unquote(pos[0] ?? ''), source: pos[1] ?? '' })]
    }
    case 'registry.set_meta': {
      const pos = positional(2)
      return [mk({ action: 'set_meta', key: unquote(pos[0] ?? ''), source: pos[1] ?? '' })]
    }

    // ── Rate limiting ──────────────────────────────────────────────────────
    case 'rate_limit': {
      const scope  = p['scope'] ?? ''
      const ipSlot = p['ip'] ?? ''
      const keySlot = p['key'] ?? ''
      const groups = p['groups'] ?? ''
      if (scope === 'ip') {
        const input = JSON.stringify({ scope: 'ip', ip_slot: ipSlot })
        return [mk({ action: 'check_rate_limit', input })]
      }
      if (scope === 'slot') {
        const input = JSON.stringify({ scope: 'slot', key_slot: keySlot })
        return [mk({ action: 'check_rate_limit', input })]
      }
      if (groups) return [mk({ action: 'check_rate_limit', input: groups })]
      return [mk({ action: 'check_rate_limit' })]
    }
    case 'assign_quota': {
      const pos = positional(1)
      return [mk({ action: 'assign_quota_group', key_identifier: pos[0] ?? '', input: p['groups'] ?? '' })]
    }

    // ── Token validation ───────────────────────────────────────────────────
    case 'validate_token': {
      const pos = positional(1)
      const src = pos[0] ?? ''
      // Map friendly param names to gateway field names
      const fields: Record<string, string> = {}
      if (p['checks'])          fields['input.jwt.validate']    = p['checks']
      if (p['jwks_url'])        fields['input.jwt.jwks_uri']    = p['jwks_url']
      if (p['jwks_url_var'])    fields['input.jwt.jwks_uri_var'] = p['jwks_url_var']
      if (p['issuer'])          fields['input.jwt.issuer']      = p['issuer']
      if (p['audience'])        fields['input.jwt.audience']    = p['audience']
      if (p['on_failure'])      fields['input.jwt.on_failure']  = p['on_failure']
      if (p['result_var'])      fields['input.jwt.result_var']  = p['result_var']
      if (p['claims_var'])      fields['input.jwt.claims_var']  = p['claims_var']
      if (p['subject_var'])     fields['input.jwt.subject_var'] = p['subject_var']
      if (p['failure_status'])  fields['input.jwt.failure_status'] = p['failure_status']
      if (p['leeway'])          fields['input.jwt.leeway_seconds'] = p['leeway']
      return [mk({ action: 'token_validation', key_identifier: src, ...fields })]
    }

    // ── Secrets ────────────────────────────────────────────────────────────
    case 'load_secret': {
      const ref = unquote(positional(1)[0] ?? '')
      return [mk({ action: 'load_secret', ref, slot: p['slot'] ?? '0', ...withAs })]
    }

    // ── HTTP ───────────────────────────────────────────────────────────────
    case 'http.get':
    case 'http.post':
    case 'http.put':
    case 'http.delete':
    case 'http.patch': {
      const method = action.split('.')[1].toUpperCase()
      const pos = positional(1)
      const urlField = pos[0] && !pos[0].includes(':') ? { url: pos[0] } : {}
      return [mk({ action: 'http_call', method, ...urlField, ...p, ...withAs })]
    }
    case 'set_upstream_header': {
      const pos = positional(2)
      return [mk({ action: 'set_request_header', key: unquote(pos[0] ?? ''), source: pos[1] ?? '' })]
    }

    // ── LLM ────────────────────────────────────────────────────────────────
    case 'llm': {
      const pos = positional(1)
      const cfg: Record<string, string> = {}
      if (p['model'])      cfg['model']      = p['model']
      if (p['max_tokens']) cfg['max_tokens'] = p['max_tokens']
      if (p['temperature']) cfg['temperature'] = p['temperature']
      const input = Object.keys(cfg).length ? JSON.stringify(cfg) : ''
      const historyField = p['history'] ? { history_slot: p['history'] } : {}
      return [mk({ action: 'llm_call', key_identifier: pos[0] ?? '', ...(input ? { input } : {}), ...historyField, ...withAs })]
    }

    // ── IP restriction ─────────────────────────────────────────────────────
    case 'ip_allow':
    case 'ip_deny': {
      const mode   = action === 'ip_allow' ? 'allow' : 'deny'
      const pos    = positional(10).filter(v => v.includes('.') || v.includes(':') || v.includes('/'))
      const cidrs  = pos.join(',') || p['cidrs'] || ''
      const status = p['on_violation'] ?? '403'
      const src    = p['source'] ?? 'header.X-Forwarded-For'
      const cfg    = JSON.stringify({ mode, cidrs, source: src,
                       on_violation_status: status, on_violation_body: 'ip not allowed' })
      const ipField = p['ip'] ? { key_identifier: p['ip'] } : {}
      return [mk({ action: 'ip_restriction', input: cfg, ...ipField })]
    }

    // ── Observability ──────────────────────────────────────────────────────
    case 'log': {
      const pos = positional(2)
      return [mk({ action: 'log_field', key: unquote(pos[0] ?? ''), source: pos[1] ?? '' })]
    }

    // ── String ops ─────────────────────────────────────────────────────────
    case 'to_lower':  return [mk({ action: 'to_lower',   source: positional(1)[0] ?? '', ...withAs })]
    case 'to_upper':  return [mk({ action: 'to_upper',   source: positional(1)[0] ?? '', ...withAs })]
    case 'byte_length': return [mk({ action: 'byte_length', source: positional(1)[0] ?? '', ...withAs })]
    case 'substring': {
      const src = positional(1)[0] ?? ''
      return [mk({ action: 'substring', source: src, input: JSON.stringify({ start: p['start'] ?? '0', length: p['length'] ?? '' }), ...withAs })]
    }
    case 'concat': {
      const pos = positional(2)
      return [mk({ action: 'concat', key_identifier: pos[0] ?? '', source: pos[1] ?? '', ...withAs })]
    }
    case 'validate_pattern': {
      const pos = positional(2)  // arg0 = source slot, arg1 = quoted pattern string
      return [mk({
        action: 'validate_pattern',
        source: unquote(pos[0] ?? ''),
        input: JSON.stringify({ pattern: unquote(pos[1] ?? '') }),
        ...withAs,
      })]
    }
    case 'extract': {
      const pos = positional(2)  // arg0 = source slot, arg1 = quoted pattern string
      // No LHS assignment — capture variable names are embedded in the pattern
      return [mk({
        action: 'extract_pattern',
        source: unquote(pos[0] ?? ''),
        input: JSON.stringify({ pattern: unquote(pos[1] ?? '') }),
      })]
    }
    case 'to_int': return [mk({ action: 'to_int', source: positional(1)[0] ?? '', ...withAs })]

    // ── Math ───────────────────────────────────────────────────────────────
    case 'add': case 'sub': case 'mul': case 'div': {
      const pos = positional(2)
      return [mk({ action, key_identifier: pos[0] ?? '', source: pos[1] ?? '', ...withAs })]
    }

    // ── Error handling ─────────────────────────────────────────────────────
    case 'on_error':
      return [mk({ action: 'capture_error', key: p['code'] ?? '', as: p['message'] ?? '' })]

    // ── Generic fallback (any other gateway action) ────────────────────────
    default:
      return [mk({ action, ...p, ...withAs })]
  }
}

// ── Block parser (recursive) ─────────────────────────────────────────────────
interface BR { steps: FlowStep[]; next: number }
function parseBlock(lines: string[], start: number): BR {
  const steps: FlowStep[] = []
  let i = start
  const mk = (o: object) => o as unknown as FlowStep

  while (i < lines.length) {
    const line = lines[i].trim()
    if (line.startsWith('}')) break
    if (!line || line.startsWith('//')) { i++; continue }

    // ── return / fail ──────────────────────────────────────────────────────
    if (line === 'return') { steps.push(mk({ action: 'return', status: '200' })); i++; continue }
    const retM = line.match(/^return\((\d+)(?:,\s*(.+?))?\)$/)
    if (retM) {
      const body = retM[2] ? unquote(retM[2]) : ''
      steps.push(mk({ action: 'return', status: retM[1], ...(body ? { body } : {}) })); i++; continue
    }
    const failM = line.match(/^fail\((\d+)(?:,\s*(.+?))?\)$/)
    if (failM) {
      steps.push(mk({ action: 'fail', status: failM[1], body: failM[2] ? unquote(failM[2]) : '' })); i++; continue
    }

    // ── call sub-flow ─────────────────────────────────────────────────────
    const callM = line.match(/^call\s+(\S+)$/)
    if (callM) { steps.push(mk({ action: 'call', flow_name: callM[1] })); i++; continue }

    // ── if (cond) { ──────────────────────────────────────────────────────
    const ifM = line.match(/^if\s*\((.+)\)\s*\{$/)
    if (ifM) {
      const thenR = parseBlock(lines, i + 1); i = thenR.next
      const closingLine = (lines[i] ?? '').trim()
      let elseSteps: FlowStep[] = []
      if (closingLine === '} else {') { const elseR = parseBlock(lines, i + 1); elseSteps = elseR.steps; i = elseR.next + 1 }
      else i++
      const s: Record<string, unknown> = { action: 'if', condition: ifM[1].trim() }
      if (thenR.steps.length) s['then_steps'] = thenR.steps
      if (elseSteps.length)   s['else_steps'] = elseSteps
      steps.push(s as unknown as FlowStep); continue
    }

    // ── switch (var) { ────────────────────────────────────────────────────
    const swM = line.match(/^switch\s*\((\w+)\)\s*\{$/)
    if (swM) {
      const cases: string[] = []; i++
      while (i < lines.length) {
        const sl = lines[i].trim()
        if (sl === '}') { i++; break }
        if (!sl || sl.startsWith('//')) { i++; continue }
        // "value":  call flow_name
        const cm = sl.match(/^["']?([^"':]+)["']?\s*:\s*call\s+(\S+)$/)
        if (cm) cases.push(`${cm[1].trim()}=${cm[2].trim()}`)
        i++
      }
      steps.push(mk({ action: 'switch', as: swM[1], cases: cases.join(',') })); continue
    }

    // ── foreach (list as item) { ──────────────────────────────────────────
    const feM = line.match(/^foreach\s*\((\w+)\s+as\s+(\w+)\)\s*\{$/)
    if (feM) {
      // expect exactly "call flow_name" inside
      let doFlow = ''; i++
      while (i < lines.length) {
        const fl = lines[i].trim()
        if (fl === '}') { i++; break }
        if (!fl || fl.startsWith('//')) { i++; continue }
        const cm = fl.match(/^call\s+(\S+)$/)
        if (cm) doFlow = cm[1]
        i++
      }
      steps.push(mk({ action: 'foreach', source: feM[1], as: feM[2], do: doFlow })); continue
    }

    // ── output.* = rhs ────────────────────────────────────────────────────
    const outM = line.match(/^(output\.[^=]+?)\s*=\s*(.+)$/)
    if (outM) { steps.push(...outputSteps(outM[1].trim(), outM[2].trim())); i++; continue }

    // ── var = rhs ─────────────────────────────────────────────────────────
    const asgM = line.match(/^(\w+)\s*=\s*(.+)$/)
    if (asgM) {
      const [, slot, rhs] = asgM; const r = rhs.trim()
      // var = source_fn("key")
      const fn = split(r)
      if (fn && SRC_FNS.has(fn.action)) { const s = srcStep(fn.action, fn.rawParams, slot); if (s) { steps.push(s); i++; continue } }
      // var = "literal (possibly with {templates})"
      if (r.startsWith('"') || r.startsWith("'")) {
        const inner = r.slice(1, r.lastIndexOf(r[0]))
        if (inner.includes('{')) {
          const { steps: ts, slot: finalSlot } = tplSteps(inner)
          // rename last slot to the user's variable name
          if (ts.length) { const last = ts[ts.length - 1] as Record<string, unknown>; if (last['as'] === finalSlot) last['as'] = slot; else ts.push({ action: 'concat', key_identifier: finalSlot, source: finalSlot, as: slot } as unknown as FlowStep) }
          steps.push(...ts)
        } else {
          steps.push({ action: 'set_const', value: inner, as: slot } as unknown as FlowStep)
        }
        i++; continue
      }
      // var = action.call(params)
      if (fn) { steps.push(...actionSteps(fn.action, fn.rawParams, slot)); i++; continue }
      // var = bareWord → set_const with string value
      steps.push({ action: 'set_const', value: r, as: slot } as unknown as FlowStep)
      i++; continue
    }

    // ── bare action call ──────────────────────────────────────────────────
    const fn = split(line)
    if (fn) { steps.push(...actionSteps(fn.action, fn.rawParams)); i++; continue }

    i++ // skip unknown line
  }
  return { steps, next: i }
}

// ── Pattern Matching Condition ──────────────────────────────────────────────
/** Parse a pattern_match condition from a step object.
 *  Handles both nested (step.condition: { type: 'pattern_match', ... })
 *  and inline (step itself is pattern_match) formats.
 */
export function parsePatternCondition(step: any): PatternCondition | null {
  if (!step) return null

  // Check if condition is directly pattern_match
  if (step.type === 'pattern_match') {
    return {
      type: 'pattern_match',
      source: step.source || 'header',
      sourceKey: step.sourceKey || step.input?.sourceKey,
      pattern: step.pattern || step.input?.pattern || '',
      strategy: step.strategy || step.input?.strategy || 'auto',
      flags: step.flags || step.input?.flags,
    }
  }

  // Check nested condition object
  if (step.condition && typeof step.condition === 'object' && step.condition.type === 'pattern_match') {
    return {
      type: 'pattern_match',
      source: step.condition.source || 'header',
      sourceKey: step.condition.sourceKey || step.condition.input?.sourceKey,
      pattern: step.condition.pattern || step.condition.input?.pattern || '',
      strategy: step.condition.strategy || step.condition.input?.strategy || 'auto',
      flags: step.condition.flags || step.condition.input?.flags,
    }
  }

  return null
}

/** Parse condition from if/switch steps.
 *  Returns PatternCondition if found, null otherwise.
 */
export function parseConditionFromStep(step: any): PatternCondition | null {
  if (!step) return null

  // Handle if step with nested condition object
  if (step.action === 'if' && step.condition) {
    if (typeof step.condition === 'object') {
      return parsePatternCondition(step.condition)
    }
    // String conditions are not pattern conditions
    return null
  }

  // Handle inline pattern_match (action: 'pattern_match' with then_steps/else_steps)
  if (step.action === 'pattern_match') {
    return parsePatternCondition(step)
  }

  // Handle switch step
  if (step.action === 'switch' && step.condition) {
    return parsePatternCondition(step.condition)
  }

  return null
}

// ── Public export ────────────────────────────────────────────────────────────
/** Parse DSL text → FlowStep[]. Unknown lines are skipped (best-effort). */
export function parseDSL(text: string): FlowStep[] {
  _tc = 0
  return parseBlock(text.split('\n'), 0).steps
}
