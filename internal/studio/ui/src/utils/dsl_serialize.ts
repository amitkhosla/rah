/**
 * dsl_serialize.ts  —  FlowStep[] → DSL text
 * Exports: serializeDSL, serializePatternCondition
 */
import type { FlowStep, PatternCondition } from '../types'
import { isPatternCondition } from '../types'
import { normalizeCases } from './dsl'

const q = (s: string) => /[,:{}"'\s()]/.test(s) ? `"${s.replace(/\\/g,'\\\\').replace(/"/g,'\\"')}"` : s

function params(step: FlowStep, skip: Set<string>): string {
  return Object.entries(step)
    .filter(([k, v]) => k !== 'action' && k !== 'then_steps' && k !== 'else_steps' && !skip.has(k) && v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${k}: ${q(String(v))}`)
    .join(', ')
}

const str = (v: unknown) => String(v ?? '')

/** Parse the step `input` field regardless of whether the gateway returned it
 *  as a plain object or as a JSON-encoded string (Studio internal format). */
function parseInput(input: unknown): Record<string, string> {
  if (!input) return {}
  if (typeof input === 'object') return input as Record<string, string>
  try { return JSON.parse(String(input)) } catch { return {} }
}

/** Serialize a PatternCondition object back to DSL object format.
 *  Returns object with type, source, pattern, and optional fields.
 *  Only includes optional fields if they are set (keeps DSL clean).
 */
export function serializePatternCondition(cond: PatternCondition): Record<string, any> {
  const dslStep: Record<string, any> = {
    type: cond.type,
    source: cond.source,
    pattern: cond.pattern,
  }

  // Add optional fields only if set
  if (cond.sourceKey) {
    dslStep.sourceKey = cond.sourceKey
  }
  if (cond.strategy && cond.strategy !== 'auto') {
    dslStep.strategy = cond.strategy
  }
  if (cond.flags) {
    dslStep.flags = cond.flags
  }

  return dslStep
}

export function serializeDSL(steps: FlowStep[], indent = ''): string {
  const lines: string[] = []
  const I = indent; const I2 = indent + '  '

  // ── Template reconstruction pre-pass ────────────────────────────────────────
  // parseDSL expands "Hello {x}" into set_const(__t0) + concat(__t0, x, __t1) chains.
  // We reverse that here so round-trips stay human-readable.

  // Map: __tN → the step that produces it
  const tplStepOf = new Map<string, FlowStep>()
  for (const s of steps) {
    const a = str(s['as'])
    if (a.startsWith('__t')) tplStepOf.set(a, s)
  }

  // Map: __tN → reconstructed template string (populated lazily by resolveSlot)
  const tplResolved = new Map<string, string>()
  // Steps that are intermediate template nodes and should be skipped in output
  const consumed = new Set<FlowStep>()

  /** Walk a __tN chain and return its template text, or null if not resolvable. */
  function resolveSlot(slot: string): string | null {
    if (!slot.startsWith('__t')) return `{${slot}}`          // user variable → placeholder
    if (tplResolved.has(slot)) return tplResolved.get(slot)! // already resolved
    const s = tplStepOf.get(slot)
    if (!s) return null
    const a = str(s.action)
    if (a === 'set_const') {
      const v = str(s['value'])
      tplResolved.set(slot, v)
      consumed.add(s)
      return v
    }
    if (a === 'concat') {
      const l = resolveSlot(str(s['key_identifier']))
      const r = resolveSlot(str(s['source']))
      if (l !== null && r !== null) {
        const v = l + r
        tplResolved.set(slot, v)
        consumed.add(s)
        return v
      }
    }
    return null
  }

  // Eagerly consume all __t* chains that feed into non-template steps
  for (const s of steps) {
    const a = str(s.action)
    // set_response_body source
    if (a === 'set_response_body') {
      const src = str(s['source'])
      if (src.startsWith('__t')) resolveSlot(src)
    }
    // concat producing a user-named variable: operands may be __t* chains
    if (a === 'concat') {
      const as_ = str(s['as'])
      if (!as_.startsWith('__t')) {
        const ki  = str(s['key_identifier'])
        const src = str(s['source'])
        if (ki.startsWith('__t'))  resolveSlot(ki)
        if (src.startsWith('__t')) resolveSlot(src)
      }
    }
  }
  // ── End pre-pass ─────────────────────────────────────────────────────────────

  for (const step of steps) {
    if (consumed.has(step)) continue   // skip intermediate template steps

    const a   = str(step.action)
    const as_ = str(step['as'])
    const src = str(step['source'])

    switch (a) {
      // ── extraction ──────────────────────────────────────────────────────
      // Legacy: expandSteps emits action:'extract' with a condition field.
      case 'extract': {
        const cond = str(step['condition']); const slot = str(step['as'])
        const m = cond.match(/^(header|queryparam|query|body|path)\.(.+)$/)
        if (m && slot) {
          const fn = m[1] === 'queryparam' || m[1] === 'query' ? 'query' : m[1] === 'path' ? 'path' : m[1]
          lines.push(`${I}${slot} = ${fn}("${m[2]}")`)
        } else lines.push(`${I}extract(${params(step, new Set())})`)
        break
      }
      // Proper compiler action names emitted by dsl_parse.ts
      case 'bind_header':
        lines.push(`${I}${as_} = header("${str(step['key'])}")`); break
      case 'bind_query_param':
        lines.push(`${I}${as_} = query("${str(step['key'])}")`); break
      case 'bind_body':
        lines.push(`${I}${as_} = body("${str(step['key'])}")`); break
      case 'bind_client_ip':
        lines.push(`${I}${str(step['key_identifier']) || as_ || 'ip'} = client_ip()`); break
      case 'bind_correlation_id': {
        const hdr = str(step['key'])
        const hdrArg = hdr && hdr !== 'X-Correlation-ID' ? `"${hdr}"` : ''
        lines.push(`${I}${as_ || 'corrId'} = correlation_id(${hdrArg})`); break
      }
      case 'store_internal_tx_id':
        lines.push(`${I}${as_ || 'txId'} = transaction_id()`); break

      // ── constants / string ops ───────────────────────────────────────────
      case 'set_const': {
        const val = str(step['value'])
        if (as_) lines.push(`${I}${as_} = "${val.replace(/\\/g,'\\\\').replace(/"/g,'\\"')}"`)
        else lines.push(`${I}set_const(value: "${val.replace(/"/g,'\\"')}")`)
        break
      }
      case 'concat': {
        const left = str(step['key_identifier']); const right = src
        // Path param copy: no key_identifier, source = path.X → var = path("X")
        if (!left && as_ && right.startsWith('path.')) {
          lines.push(`${I}${as_} = path("${right.slice(5)}")`); break
        }
        // Reconstruct template string when operands include resolved __t* chains
        if (as_ && !as_.startsWith('__t')) {
          const leftStr  = left.startsWith('__t')  ? tplResolved.get(left)  ?? null : null
          const rightStr = right.startsWith('__t') ? tplResolved.get(right) ?? null : null
          if (leftStr !== null || rightStr !== null) {
            const lPart = leftStr  !== null ? leftStr  : `{${left}}`
            const rPart = rightStr !== null ? rightStr : `{${right}}`
            lines.push(`${I}${as_} = "${(lPart + rPart).replace(/\\/g,'\\\\').replace(/"/g,'\\"')}"`)
            break
          }
          // Both operands are plain identifiers → use operator shorthand
          if (left && !left.startsWith('__t') && right && !right.startsWith('__t')) {
            lines.push(`${I}${as_} = ${left} + ${right}`); break
          }
        }
        if (as_) lines.push(`${I}${as_} = concat(${left}, ${right})`)
        else lines.push(`${I}concat(${left}, ${right})`)
        break
      }
      case 'to_lower':   lines.push(`${I}${as_ ? as_+' = ' : ''}to_lower(${src})`);  break
      case 'to_upper':   lines.push(`${I}${as_ ? as_+' = ' : ''}to_upper(${src})`);  break
      case 'byte_length':lines.push(`${I}${as_ ? as_+' = ' : ''}byte_length(${src})`); break
      case 'substring':  lines.push(`${I}${as_ ? as_+' = ' : ''}substring(${src}, ${params(step, new Set(['source','as']))})`); break
      case 'to_int':     lines.push(`${I}${as_ ? as_+' = ' : ''}to_int(${src})`);    break

      // ── math ─────────────────────────────────────────────────────────────
      case 'add': case 'sub': case 'mul': case 'div': {
        const l = str(step['key_identifier']); const r = src
        const opSymbol: Record<string, string> = { add: '+', sub: '-', mul: '*', div: '/' }
        if (as_) lines.push(`${I}${as_} = ${l} ${opSymbol[a]} ${r}`)
        else lines.push(`${I}${a}(${l}, ${r})`)
        break
      }

      // ── cache ────────────────────────────────────────────────────────────
      case 'cache_get': {
        const key = str(step['key_identifier'])
        if (as_) lines.push(`${I}${as_} = cache.get(${key})`)
        else lines.push(`${I}cache.get(${key})`); break
      }
      case 'cache_put': {
        const key = str(step['key_identifier']); const ttl = str(step['ttl'])
        lines.push(`${I}cache.set(${key}, ${src}, ttl: ${ttl || '300'})`); break
      }
      case 'cache_get_global': {
        const key = str(step['key_identifier'])
        if (as_) lines.push(`${I}${as_} = shared_cache.get("${key}")`)
        else lines.push(`${I}shared_cache.get("${key}")`); break
      }
      case 'cache_put_global': {
        const key = str(step['key_identifier']); const ttl = str(step['ttl'])
        lines.push(`${I}shared_cache.set("${key}", ${src}, ttl: ${ttl || '3600'})`); break
      }
      case 'cache_delete': {
        const key = str(step['key_identifier'])
        lines.push(`${I}cache.delete(${key})`); break
      }
      case 'cache_delete_global': {
        const key = str(step['key_identifier'])
        lines.push(`${I}shared_cache.delete("${key}")`); break
      }
      case 'cache_get_batched': {
        const v = str(step['variable']); const d = str(step['destination'])
        lines.push(`${I}cache.get_batched(${v}, into: ${d})`); break
      }
      case 'batch_flush':        lines.push(`${I}batch_flush()`); break
      case 'json_extract_emit':  lines.push(`${I}json_extract(${str(step['variable'])}, ops: ${q(str(step['params']))})`); break
      case 'json_foreach_emit':  lines.push(`${I}json_foreach(${str(step['variable'])}, array: "${str(step['path'])}", ops: ${q(str(step['params']))})`); break

      // ── registry ─────────────────────────────────────────────────────────
      case 'registry_lookup': {
        const key = str(step['key_identifier'])
        const m = key.match(/^(header|queryparam|query|body|path)\.(.+)$/)
        if (m) { const fn = m[1] === 'queryparam' ? 'query' : m[1]; lines.push(`${I}registry.lookup(${fn}("${m[2]}")`) }
        else lines.push(`${I}registry.lookup(${key})`)
        break
      }
      case 'load_service_url':
        lines.push(`${I}${as_ ? as_+' = ' : ''}registry.url("${str(step['key'])}")`); break
      case 'load_service_url_var':
        lines.push(`${I}${as_ ? as_+' = ' : ''}registry.url_var(${str(step['key_identifier'])})`); break
      case 'load_identifier':
        lines.push(`${I}${as_ ? as_+' = ' : ''}registry.id("${str(step['key'])}")`); break
      case 'load_meta':
        lines.push(`${I}${as_ ? as_+' = ' : ''}registry.meta("${str(step['key'])}")`); break
      case 'set_service_url':
        lines.push(`${I}registry.set_url("${str(step['key'])}", ${str(step['source'])})`); break
      case 'set_identifier':
        lines.push(`${I}registry.set_id("${str(step['key'])}", ${str(step['source'])})`); break
      case 'set_meta':
        lines.push(`${I}registry.set_meta("${str(step['key'])}", ${str(step['source'])})`); break

      // ── rate limiting ─────────────────────────────────────────────────────
      case 'check_rate_limit': {
        const cfg = parseInput(step['input'])
        const inputStr = str(step['input'])
        if (cfg['scope'] === 'ip') {
          const ipPart = cfg['ip_slot'] ? `, ip: ${cfg['ip_slot']}` : ''
          lines.push(`${I}rate_limit(scope: ip${ipPart})`); break
        }
        if (cfg['scope'] === 'slot') {
          const keyPart = cfg['key_slot'] ? `, key: ${cfg['key_slot']}` : ''
          lines.push(`${I}rate_limit(scope: slot${keyPart})`); break
        }
        if (inputStr && !cfg['scope']) {
          lines.push(`${I}rate_limit(groups: ${q(inputStr)})`); break
        }
        lines.push(`${I}rate_limit()`); break
      }
      case 'assign_quota_group': {
        const ki = str(step['key_identifier']); const input = str(step['input'])
        lines.push(`${I}assign_quota(${ki}, groups: ${q(input)})`); break
      }

      // ── token validation ──────────────────────────────────────────────────
      case 'token_validation': {
        const ki = str(step['key_identifier'])
        const extras: string[] = []
        const fieldMap: Record<string, string> = {
          'input.jwt.validate':    'checks', 'input.jwt.jwks_uri':    'jwks_url',
          'input.jwt.jwks_uri_var':'jwks_url_var', 'input.jwt.issuer': 'issuer',
          'input.jwt.audience':    'audience',  'input.jwt.on_failure': 'on_failure',
          'input.jwt.result_var':  'result_var','input.jwt.claims_var':  'claims_var',
          'input.jwt.subject_var': 'subject_var','input.jwt.failure_status':'failure_status',
          'input.jwt.leeway_seconds':'leeway',
        }
        for (const [k, alias] of Object.entries(fieldMap)) {
          const v = str((step as Record<string,unknown>)[k])
          if (v) extras.push(`${alias}: ${q(v)}`)
        }
        lines.push(`${I}validate_token(${ki}${extras.length ? ', '+extras.join(', ') : ''})`); break
      }

      // ── secrets ───────────────────────────────────────────────────────────
      case 'load_secret': {
        const ref = str(step['ref'])
        if (as_) lines.push(`${I}${as_} = load_secret("${ref}")`)
        else lines.push(`${I}load_secret("${ref}")`); break
      }

      // ── HTTP ──────────────────────────────────────────────────────────────
      case 'http_call': {
        const method = (str(step['method']) || 'GET').toLowerCase()
        const url = str(step['url']); const urlVar = str(step['url_var'])
        const urlPart = url ? `"${url}"` : (urlVar ? `url: ${urlVar}` : '')
        const rest = params(step, new Set(['method','url','url_var','as']))
        const callStr = `http.${method}(${[urlPart, rest].filter(Boolean).join(', ')})`
        if (as_) lines.push(`${I}${as_} = ${callStr}`)
        else lines.push(`${I}${callStr}`); break
      }
      case 'set_request_header':
        lines.push(`${I}set_upstream_header("${str(step['key'])}", ${str(step['source'])})`); break

      // ── LLM ───────────────────────────────────────────────────────────────
      case 'llm_call': {
        const ki = str(step['key_identifier'])
        const cfg = parseInput(step['input'])
        const parts: string[] = []
        if (cfg['model'])       parts.push(`model: ${q(cfg['model'])}`)
        if (cfg['max_tokens'])  parts.push(`max_tokens: ${cfg['max_tokens']}`)
        if (cfg['temperature']) parts.push(`temperature: ${cfg['temperature']}`)
        const hs = str(step['history_slot']); if (hs) parts.push(`history: ${hs}`)
        const callStr = `llm(${[ki, ...parts].join(', ')})`
        if (as_) lines.push(`${I}${as_} = ${callStr}`)
        else lines.push(`${I}${callStr}`); break
      }

      // ── IP restriction ────────────────────────────────────────────────────
      case 'ip_restriction': {
        const cfg = parseInput(step['input'])
        const mode  = cfg['mode'] ?? 'allow'
        const cidrs = cfg['cidrs'] ?? ''
        const fn    = mode === 'allow' ? 'ip_allow' : 'ip_deny'
        const parts: string[] = cidrs.split(',').map(c => `"${c.trim()}"`)
        const extras: string[] = []
        if (cfg['on_violation_status'] && cfg['on_violation_status'] !== '403')
          extras.push(`on_violation: ${cfg['on_violation_status']}`)
        if (cfg['source'] && cfg['source'] !== 'header.X-Forwarded-For')
          extras.push(`source: "${cfg['source']}"`)
        const ki = str(step['key_identifier'])
        if (ki) extras.push(`ip: ${ki}`)
        lines.push(`${I}${fn}(${[...parts, ...extras].join(', ')})`); break
      }

      // ── template pattern matching ─────────────────────────────────────────
      case 'validate_pattern': {
        const src = str(step['source'])
        const pat = str(parseInput(step['input']).pattern ?? '')
        const as_ = str(step['as'])
        const lhs = as_ ? `${as_} = ` : ''
        lines.push(`${I}${lhs}validatePattern(${src}, '${pat}')`)
        break
      }
      case 'extract_pattern': {
        const src = str(step['source'])
        const pat = str(parseInput(step['input']).pattern ?? '')
        lines.push(`${I}extract(${src}, '${pat}')`)
        break
      }

      // ── observability ─────────────────────────────────────────────────────
      case 'log_field':
        lines.push(`${I}log("${str(step['key'])}", ${str(step['source'])})`); break

      // ── response output ───────────────────────────────────────────────────
      case 'set_response_body': {
        if (src.startsWith('__t') && tplResolved.has(src)) {
          const tpl = tplResolved.get(src)!
          lines.push(`${I}output.body = "${tpl.replace(/\\/g,'\\\\').replace(/"/g,'\\"')}"`)
        } else {
          lines.push(`${I}output.body = ${src}`)
        }
        break
      }
      case 'set_response_status':
        lines.push(`${I}output.status = ${str(step['value'])}`); break
      case 'set_response_header':
        lines.push(`${I}output.header("${str(step['key'])}") = ${src}`); break

      // ── control flow ──────────────────────────────────────────────────────
      case 'return': {
        const status = str(step['status']) || '200'
        const body = str(step['body'])
        const bodyVar = str(step['as'])
        if (body) lines.push(`${I}return(${status}, "${body.replace(/"/g,'\\"')}")`)
        else if (bodyVar) lines.push(`${I}return(${status}, ${bodyVar})`)
        else if (status === '200') lines.push(`${I}return`)
        else lines.push(`${I}return(${status})`)
        break
      }
      case 'fail': {
        const status = str(step['status']) || '500'; const body = str(step['body'])
        lines.push(`${I}fail(${status}${body ? `, "${body}"` : ''})`); break
      }
      case 'capture_error':
        lines.push(`${I}on_error(code: ${str(step['key'])}, message: ${str(step['as'])})`); break
      case 'call':
        lines.push(`${I}call ${str(step['flow_name'])}`); break

      case 'if': {
        const condRaw  = step['condition']
        let condStr = str(condRaw)

        // Check if condition is a PatternCondition object
        if (condRaw && typeof condRaw === 'object' && isPatternCondition(condRaw)) {
          const patternObj = serializePatternCondition(condRaw)
          // Serialize to JSON representation for DSL output
          condStr = JSON.stringify(patternObj)
        }

        const thenS = (step.then_steps ?? []) as FlowStep[]
        const elseS = (step.else_steps ?? []) as FlowStep[]
        const thenR = str(step['then']); const elseR = str(step['else'])
        lines.push(`${I}if (${condStr}) {`)
        if (thenS.length) lines.push(serializeDSL(thenS, I2))
        else if (thenR) lines.push(`${I2}call ${thenR}`)
        if (elseS.length || elseR) {
          lines.push(`${I}} else {`)
          if (elseS.length) lines.push(serializeDSL(elseS, I2))
          else if (elseR) lines.push(`${I2}call ${elseR}`)
        }
        lines.push(`${I}}`); break
      }

      case 'switch': {
        const match = str(step['as']); const cases = normalizeCases(step['cases'])
        lines.push(`${I}switch (${match}) {`)
        cases.split(',').forEach(c => {
          const eq = c.indexOf('=')
          if (eq >= 0) lines.push(`${I2}"${c.slice(0, eq).trim()}": call ${c.slice(eq + 1).trim()}`)
        })
        lines.push(`${I}}`); break
      }

      case 'foreach': {
        const source_ = str(step['source']); const item = str(step['as']); const doFlow = str(step['do'])
        lines.push(`${I}foreach (${source_} as ${item}) {`)
        if (doFlow) lines.push(`${I2}call ${doFlow}`)
        lines.push(`${I}}`); break
      }

      // ── generic fallback ──────────────────────────────────────────────────
      default: {
        const p2 = params(step, as_ ? new Set(['as']) : new Set())
        const call = p2 ? `${a}(${p2})` : `${a}()`
        if (as_) lines.push(`${I}${as_} = ${call}`)
        else lines.push(`${I}${call}`)
      }
    }
  }

  return lines.join('\n')
}
