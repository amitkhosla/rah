# RAH Studio — Flow Code Editor (DSL)

> **What the user writes**: Named variables, action calls, template strings. No slot indices, no internal names.
> **Bidirectional**: Visual ↔ Code tab in Flow Designer.
> **Three sessions**: DSL-0 (parser), DSL-1 (serializer + re-export), DSL-2 (UI tab).
>
> Build command after EVERY session:
> ```bash
> cd internal/studio/ui && npm run build
> ```

---

## Complete DSL Reference (what the user writes)

```
// ── Request extraction ────────────────────────────────────────────────────
token      = header("Authorization")
userId     = body("userId")
nested     = body("order.items.0.id")        // nested JSON path
mode       = query("mode")
profileId  = path("user_profile")            // URL path parameter
ip         = client_ip()
corrId     = correlation_id()               // reads header; generates if missing
txId       = transaction_id()              // gateway-assigned internal TX id

// ── Constants ─────────────────────────────────────────────────────────────
label      = "Hello"
greeting   = "Hello {userId}, your profile is {profileId}."   // template

// ── String operations ─────────────────────────────────────────────────────
lower      = to_lower(token)
upper      = to_upper(mode)
sliced     = substring(userId, start: 0, length: 8)
length     = byte_length(token)
combined   = concat(label, userId)          // join two variables

// ── Math ─────────────────────────────────────────────────────────────────
total      = add(price, tax)
diff       = sub(price, credit)
area       = mul(width, height)
perUnit    = div(total, quantity)

// ── Per-tenant cache (private, isolated per tenant) ───────────────────────
cached     = cache.get(token)               // empty string on miss
cache.set(token, result, ttl: 300)

// ── Shared cache (global, all tenants see same data) ──────────────────────
shared     = shared_cache.get("config:v1")
shared_cache.set("config:v1", result, ttl: 3600)

// ── Batched cache + JSON extraction ──────────────────────────────────────
// Queue multiple ops then flush in a single round-trip:
cache.get_batched(token, into: cachedUser)
cache.get_batched(userId, into: cachedProfile)
batch_flush()                               // results available after this line

// Extract fields from a JSON response and batch-cache them:
json_extract(result, ops: '[{"path":"user.id","key_prefix":"u:","op_type":"put","target":"cache","ttl":"300"}]')
json_foreach(result, array: "items", ops: '[{"path":"id","key_prefix":"item:","op_type":"put","target":"cache","ttl":"60"}]')
batch_flush()

// ── Registry — multi-tenant identity & routing ────────────────────────────
// Step 1: resolve incoming header/param to a TenantID (must run before registry.* ops)
registry.lookup(header("X-Tenant-ID"))      // or: registry.lookup(query("tenant"))

// Step 2: load tenant-specific data (requires registry.lookup above)
upstream   = registry.url("primary")        // load named service URL
fallback   = registry.url("fallback")
apiKey     = registry.id("api_key")         // load named identifier/credential
tier       = registry.meta("tier")          // load metadata value

// Write operations (admin/onboarding flows only):
registry.set_url("primary", newUrl)
registry.set_id("api_key", newKey)
registry.set_meta("tier", newTier)

// ── Rate limiting ─────────────────────────────────────────────────────────
rate_limit()                                // uses config attached to this endpoint

// Tiered rate limits (different limits per plan):
assign_quota(tier, groups: '{"free":"1","pro":"2","enterprise":"3"}')
rate_limit(groups: '{"1":"free_rl","2":"pro_rl","3":"enterprise_rl"}')

// ── Token validation (JWT) ────────────────────────────────────────────────
validate_token(token)                       // signature + expiry only (default)

validate_token(token,
  checks:         "signature,expiry",
  jwks_url:       "https://auth.example.com/.well-known/jwks.json",
  issuer:         "https://auth.example.com",
  audience:       "my-api",
  on_failure:     "continue",
  result_var:     tokenOk,
  claims_var:     claims,
  subject_var:    subject
)

// JWKS URL from registry (multi-tenant IdP):
validate_token(token, jwks_url_var: upstream, checks: "signature,expiry")

// ── Secrets (GSM / Vault / AWS SM / env) ─────────────────────────────────
secret     = load_secret("gsm://my-project/secrets/api-key")
secret     = load_secret("env:MY_API_KEY")
secret     = load_secret("vault://secret/data/myapp/key")

// ── HTTP upstream calls ───────────────────────────────────────────────────
result     = http.get("https://api.example.com/v1/users")
result     = http.post("https://api.example.com/v1/orders", timeout: 5000)

// Dynamic URL from registry (multi-tenant routing):
result     = http.get(url: upstream, timeout: 5000)
result     = http.get(url: upstream, timeout: 5000, retry_on: "status >= 500", max_retries: 3)

// Set a header on the upstream request before http.*:
set_upstream_header("X-User-ID", userId)
set_upstream_header("X-Correlation-ID", corrId)

// ── LLM calls ────────────────────────────────────────────────────────────
response   = llm(prompt, model: "claude-sonnet-4-6", max_tokens: 2000)
response   = llm(prompt, model: "claude-sonnet-4-6", max_tokens: 2000, history: chatHistory)

// ── IP restriction ────────────────────────────────────────────────────────
ip_allow("10.0.0.0/8", "192.168.0.0/16")               // 403 if not in ranges
ip_deny("203.0.113.0/24", on_violation: 403)
ip_allow(ip, cidrs: "10.0.0.0/8")                      // using pre-resolved ip var

// ── Observability ─────────────────────────────────────────────────────────
log("user_id", userId)                      // write named field to access log
log("tenant_tier", tier)

// ── Sub-flow calls (shared variable context — no params needed) ───────────
call auth_validation                        // name of any saved flow in this studio
call tenant_routing
call my_custom_sub_flow

// ── Conditional ───────────────────────────────────────────────────────────
if (cached) {
  output.body = cached
  return(200)
} else {
  validate_token(token)
  result = http.get(url: upstream)
  cache.set(token, result, ttl: 300)
  output.body = result
  return(200)
}

// Nested is fine:
if (mode == "debug") {
  if (userId) {
    call debug_flow
  } else {
    return(400, "missing userId")
  }
}

// ── Switch (multi-branch on variable value) ───────────────────────────────
switch (tier) {
  "free":       call free_flow
  "pro":        call pro_flow
  "enterprise": call enterprise_flow
}

// ── For-each (iterate a list; sub-flow handles each item) ─────────────────
foreach (items as item) {
  call process_item_flow
}

// ── Error handling ────────────────────────────────────────────────────────
on_error(code: errorCode, message: errorMsg)    // capture error, continue flow

// ── Output ────────────────────────────────────────────────────────────────
output.body            = result
output.body            = "Profile: {profileId}. Let me know if you need help."
output.status          = 200
output.header("X-Correlation-ID") = corrId
output.header("X-Tenant-ID")      = tenantId

// ── Terminate ─────────────────────────────────────────────────────────────
return                                      // 200 empty body
return(200)
return(200, result)                         // variable as body
return(401, "unauthorized")                 // literal string body
fail(500, "upstream error")
```

---

## SESSION DSL-0 — Create dsl_parse.ts [Sonnet]

**FILE**: Create NEW `internal/studio/ui/src/utils/dsl_parse.ts`

**WHAT THIS DOES**: Exports `parseDSL(text): FlowStep[]` and the helper `parseParams`. Contains the complete parser: tokenizer, block parser, all source-function and action-call mappings, template string compiler.

**PREREQUISITE**: None. Touch no other file.

**VERIFY**: File must NOT exist yet.

### Create the file with exactly this content:

```typescript
/**
 * dsl_parse.ts  —  RAH Flow DSL parser
 * Exports: parseDSL, parseParams
 */
import type { FlowStep } from '../types'

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

// ── Source function → extract/bind step ─────────────────────────────────────
function srcStep(fn: string, rawArgs: string, slot: string): FlowStep | null {
  const a = unquote(rawArgs)
  const mk = (o: object) => o as unknown as FlowStep
  switch (fn) {
    case 'header':         return mk({ action: 'extract',              condition: `header.${a}`,      as: slot })
    case 'query':          return mk({ action: 'extract',              condition: `queryparam.${a}`,  as: slot })
    case 'body':           return mk({ action: 'extract',              condition: `body.${a}`,        as: slot })
    case 'path':           return mk({ action: 'extract',              condition: `path.${a}`,        as: slot })
    case 'client_ip':      return mk({ action: 'bind_client_ip',       key_identifier: slot })
    case 'correlation_id': return mk({ action: 'bind_correlation_id',  as: slot, generate_if_missing: 'true' })
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
    case 'rate_limit':
      return [mk({ action: 'check_rate_limit', ...(p['groups'] ? { input: p['groups'] } : {}) })]
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
      const mode = action === 'ip_allow' ? 'allow' : 'deny'
      const pos = positional(10).filter(v => v.includes('.') || v.includes(':'))
      const cidrs = pos.join(',') || p['cidrs'] || ''
      const status = p['on_violation'] ?? '403'
      const src = p['source'] ?? 'header.X-Forwarded-For'
      const cfg = JSON.stringify({ mode, cidrs, source: src, on_violation_status: status, on_violation_body: 'ip not allowed' })
      return [mk({ action: 'ip_restriction', input: cfg, ...(p['ip'] ? { key_identifier: p['ip'] } : {}) })]
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

// ── Public export ────────────────────────────────────────────────────────────
/** Parse DSL text → FlowStep[]. Unknown lines are skipped (best-effort). */
export function parseDSL(text: string): FlowStep[] {
  _tc = 0
  return parseBlock(text.split('\n'), 0).steps
}
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Expected: zero errors. File not yet imported — no visual change.

### DO NOT TOUCH
- No other files.

---

## SESSION DSL-1 — Create dsl_serialize.ts + dsl.ts [Sonnet]

**FILES**:
1. Create NEW `internal/studio/ui/src/utils/dsl_serialize.ts`
2. Create NEW `internal/studio/ui/src/utils/dsl.ts` (4 lines — re-export only)

**WHAT THIS DOES**: Adds `serializeDSL(steps): string` which converts FlowStep[] back to readable DSL. The `dsl.ts` re-export file is the single import point for the UI.

**PREREQUISITE**: DSL-0 complete and build passes.

**VERIFY**: `dsl_parse.ts` must exist. `dsl_serialize.ts` and `dsl.ts` must NOT exist yet.

### Create dsl_serialize.ts with exactly this content:

```typescript
/**
 * dsl_serialize.ts  —  FlowStep[] → DSL text
 * Exports: serializeDSL
 */
import type { FlowStep } from '../types'

const q = (s: string) => /[,:{}"'\s()]/.test(s) ? `"${s.replace(/\\/g,'\\\\').replace(/"/g,'\\"')}"` : s

function params(step: FlowStep, skip: Set<string>): string {
  return Object.entries(step)
    .filter(([k, v]) => k !== 'action' && k !== 'then_steps' && k !== 'else_steps' && !skip.has(k) && v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${k}: ${q(String(v))}`)
    .join(', ')
}

const str = (v: unknown) => String(v ?? '')

export function serializeDSL(steps: FlowStep[], indent = ''): string {
  const lines: string[] = []
  const I = indent; const I2 = indent + '  '

  for (const step of steps) {
    const a   = str(step.action)
    const as_ = str(step['as'])
    const src = str(step['source'])

    switch (a) {
      // ── extraction ──────────────────────────────────────────────────────
      case 'extract': {
        const cond = str(step['condition']); const slot = str(step['as'])
        const m = cond.match(/^(header|queryparam|query|body|path)\.(.+)$/)
        if (m && slot) {
          const fn = m[1] === 'queryparam' || m[1] === 'query' ? 'query' : m[1] === 'path' ? 'path' : m[1]
          lines.push(`${I}${slot} = ${fn}("${m[2]}")`)
        } else lines.push(`${I}extract(${params(step, new Set())})`)
        break
      }
      case 'bind_client_ip':
        lines.push(`${I}${str(step['key_identifier']) || as_ || 'ip'} = client_ip()`); break
      case 'bind_correlation_id':
        lines.push(`${I}${as_ || 'corrId'} = correlation_id()`); break
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
        if (as_) lines.push(`${I}${as_} = ${a}(${l}, ${r})`)
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
        const input = str(step['input'])
        lines.push(`${I}rate_limit(${input ? 'groups: '+q(input) : ''})`); break
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
        let cfg: Record<string, string> = {}
        try { cfg = JSON.parse(str(step['input'])) } catch { /**/ }
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
        let cfg: Record<string, string> = {}
        try { cfg = JSON.parse(str(step['input'])) } catch { /**/ }
        const mode = cfg['mode'] ?? 'allow'; const cidrs = cfg['cidrs'] ?? ''
        const fn = mode === 'allow' ? 'ip_allow' : 'ip_deny'
        const status = cfg['on_violation_status'] !== '403' ? `, on_violation: ${cfg['on_violation_status']}` : ''
        lines.push(`${I}${fn}(${cidrs.split(',').map(c => `"${c.trim()}"`).join(', ')}${status})`); break
      }

      // ── observability ─────────────────────────────────────────────────────
      case 'log_field':
        lines.push(`${I}log("${str(step['key'])}", ${str(step['source'])})`); break

      // ── response output ───────────────────────────────────────────────────
      case 'set_response_body':
        if (src.startsWith('__t')) lines.push(`${I}output.body = /* compiled template */ ${src}`)
        else lines.push(`${I}output.body = ${src}`); break
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
        const cond  = str(step['condition'])
        const thenS = (step.then_steps ?? []) as FlowStep[]
        const elseS = (step.else_steps ?? []) as FlowStep[]
        const thenR = str(step['then']); const elseR = str(step['else'])
        lines.push(`${I}if (${cond}) {`)
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
        const match = str(step['as']); const cases = str(step['cases'])
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
```

### Create dsl.ts with exactly this content (4 lines):

```typescript
// dsl.ts — re-export DSL public API
export { parseDSL, parseParams } from './dsl_parse'
export { serializeDSL } from './dsl_serialize'
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Zero errors expected.

### DO NOT TOUCH
- `dsl_parse.ts` — do not change it.
- No other files.

---

## SESSION DSL-2 — Add Code tab to FlowDesigner.tsx [Haiku OK]

**FILE**: `internal/studio/ui/src/components/FlowDesigner.tsx` only.

**WHAT THIS DOES**: Adds a Visual/Code tab toggle. Code tab = monospace textarea with DSL for the current steps. "Apply → Visual" button parses and replaces step list.

**PREREQUISITE**: DSL-1 complete and build passes.

**VERIFY FILE STATE BEFORE STARTING** — these EXACT strings must be found in the file:
1. `import { expandSteps, findSourceRefs, smartCondition } from '../utils/expressions'`
2. `const [showFlowMap, setShowFlowMap]   = useState(false)`
3. `function handleSave() {`
4. `className={`canvas${dragOver ? ' drag-over' : ''}`}`

### CHANGE 1 — Import parseDSL + serializeDSL

**FIND** (exact):
```
import { expandSteps, findSourceRefs, smartCondition } from '../utils/expressions'
```
**REPLACE WITH**:
```
import { expandSteps, findSourceRefs, smartCondition } from '../utils/expressions'
import { parseDSL, serializeDSL } from '../utils/dsl'
```

### CHANGE 2 — Add state variables

**FIND** (exact):
```
  const [showFlowMap, setShowFlowMap]   = useState(false)
```
**REPLACE WITH**:
```
  const [showFlowMap, setShowFlowMap]   = useState(false)
  const [viewMode,    setViewMode]      = useState<'visual' | 'code'>('visual')
  const [dslText,     setDslText]       = useState('')
  const [dslError,    setDslError]      = useState<string | null>(null)
```

### CHANGE 3 — Add switchToCode + applyDSL handlers

**FIND** (exact):
```
  function handleSave() {
    if (!flowName.trim() || steps.length === 0) return
```
**REPLACE WITH**:
```
  function switchToCode() {
    setDslText(serializeDSL(steps))
    setDslError(null)
    setViewMode('code')
  }

  function applyDSL() {
    try {
      const parsed = parseDSL(dslText)
      setSteps(parsed)
      setDslError(null)
      setExpanded(new Set())
      setNestedExpanded(new Set())
    } catch (e) {
      setDslError(e instanceof Error ? e.message : 'Parse error — check syntax.')
    }
  }

  function handleSave() {
    if (!flowName.trim() || steps.length === 0) return
```

### CHANGE 4 — Add Visual/Code toggle

Find where the flow name `<input>` is (or the breadcrumb if NAV-1b is done). Insert the toggle BEFORE that block.

**FIND** (exact — use whichever is present in the file):

If breadcrumb exists:
```
          {navStack.length > 0 && (
```
If breadcrumb does NOT exist:
```
          <input
            className="input"
            placeholder="flow name (required to save)"
            value={flowName}
```

In both cases **INSERT before that line** (do not replace):
```
          <div style={{ display: 'flex', marginBottom: 6, borderRadius: 6, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.12)', width: 'fit-content' }}>
            <button style={{ fontSize: 12, padding: '3px 14px', border: 'none', cursor: 'pointer', background: viewMode === 'visual' ? 'var(--accent)' : 'transparent', color: viewMode === 'visual' ? '#fff' : 'var(--muted)' }} onClick={() => setViewMode('visual')}>Visual</button>
            <button style={{ fontSize: 12, padding: '3px 14px', border: 'none', cursor: 'pointer', background: viewMode === 'code'   ? 'var(--accent)' : 'transparent', color: viewMode === 'code'   ? '#fff' : 'var(--muted)' }} onClick={switchToCode}>Code</button>
          </div>
```

### CHANGE 5 — Add code editor panel; hide canvas in code mode

**FIND** (exact):
```
          <div
            className={`canvas${dragOver ? ' drag-over' : ''}`}
            onDragOver={e => { e.preventDefault(); setDragOver(true) }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
          >
```
**REPLACE WITH**:
```
          {viewMode === 'code' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, flex: 1 }}>
              <textarea
                spellCheck={false}
                value={dslText}
                onChange={e => { setDslText(e.target.value); setDslError(null) }}
                style={{ flex: 1, minHeight: 440, width: '100%', resize: 'vertical', fontFamily: 'monospace', fontSize: 13, lineHeight: 1.65, padding: '12px 14px', background: 'rgba(0,0,0,0.28)', color: 'var(--fg)', border: `1px solid ${dslError ? '#ef4444' : 'rgba(255,255,255,0.12)'}`, borderRadius: 8, outline: 'none', boxSizing: 'border-box' }}
                placeholder={'// RAH Flow DSL\nprofileId = path("user_profile")\ntoken     = header("Authorization")\nvalidate_token(token)\noutput.body = "Hello {profileId}"\nreturn(200)'}
              />
              {dslError && <div style={{ fontSize: 12, color: '#ef4444', background: 'rgba(239,68,68,0.08)', padding: '5px 10px', borderRadius: 5 }}>{dslError}</div>}
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <button className="btn accent" style={{ fontSize: 13 }} onClick={applyDSL}>Apply → Visual</button>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>Replaces the visual steps. Cannot be undone.</span>
              </div>
            </div>
          )}
          <div
            className={`canvas${dragOver ? ' drag-over' : ''}`}
            style={{ display: viewMode === 'code' ? 'none' : undefined }}
            onDragOver={e => { e.preventDefault(); setDragOver(true) }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
          >
```

### BUILD
```bash
cd internal/studio/ui && npm run build
```
Zero errors expected.

### BROWSER TESTS
1. Open Flow Designer. Build a small flow (cache.get + if + return). Click **Code** → DSL appears.
2. Clear textarea. Type:
   ```
   profileId = path("user_profile")
   token     = header("Authorization")
   validate_token(token, checks: "signature,expiry")
   output.body = "The profile is: {profileId}. Let me know if you need anything else."
   return(200)
   ```
3. Click **Apply → Visual** → canvas shows: `extract` (path), `extract` (header), `token_validation`, `set_const`+`concat` chain, `set_response_body`, `return`.
4. Click **Visual** → canvas drag-drop still works.
5. Type `if (broken {` → Apply → red error, steps unchanged.
6. Test `call my_sub_flow` → a single `call` step with `flow_name: my_sub_flow`.
7. Test switch:
   ```
   switch (tier) {
     "free": call free_flow
     "pro":  call pro_flow
   }
   ```
   → `switch` step with `cases: free=free_flow,pro=pro_flow`.

### DO NOT TOUCH
- `dsl_parse.ts`, `dsl_serialize.ts`, `dsl.ts` — do not change.
- No other files.
