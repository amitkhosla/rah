import { useMemo, useState } from 'react'
import type { FieldDef, FlowStep, PaletteBlock, SavedFlow } from '../types'
import { expandSteps, findSourceRefs, smartCondition } from '../utils/expressions'

interface Props {
  blocks: PaletteBlock[]
  steps: FlowStep[]
  setSteps: (steps: FlowStep[]) => void
  flowName: string
  setFlowName: (name: string) => void
  savedFlows: SavedFlow[]
  onSaveFlow: () => void
}

// ── Visual Mode recipe definitions ───────────────────────────────
interface VisualRecipe {
  title: string
  wraps: string        // action type this maps to
  description: string
}

interface VisualGroup {
  label: string
  icon: string
  recipes: VisualRecipe[]
}

const VISUAL_GROUPS: VisualGroup[] = [
  {
    label: 'INFERENCE',
    icon: '🧠',
    recipes: [
      { title: 'Call LLM',        wraps: 'llm_call',            description: 'Send a prompt to an AI model' },
      { title: 'Stream Response', wraps: 'llm_call',            description: 'Stream tokens to client via SSE' },
      { title: 'Route to Model',  wraps: 'route_llm',           description: 'Conditionally route to different models' },
    ],
  },
  {
    label: 'KNOWLEDGE',
    icon: '📚',
    recipes: [
      { title: 'Search Vector Store', wraps: 'vector_search',        description: 'Find relevant content by semantic similarity' },
      { title: 'Embed Text',          wraps: 'embed_text',           description: 'Convert text to vector embedding' },
      { title: 'Chunk Document',      wraps: 'chunk_text',           description: 'Split document into overlapping chunks' },
      { title: 'Semantic Cache',      wraps: 'semantic_cache_get',   description: 'Check cache before calling LLM' },
    ],
  },
  {
    label: 'MEMORY',
    icon: '💾',
    recipes: [
      { title: 'Load History', wraps: 'load_history', description: 'Load conversation history from cache' },
      { title: 'Save History', wraps: 'save_history', description: 'Save conversation history to cache' },
      { title: 'Trim History', wraps: 'trim_history', description: 'Remove oldest messages when context full' },
    ],
  },
  {
    label: 'TOOLS & AGENTS',
    icon: '🔧',
    recipes: [
      { title: 'Call MCP Tool',      wraps: 'mcp_call_tool',   description: 'Call a specific tool on an MCP server' },
      { title: 'Execute Agent Plan', wraps: 'execute_plan',    description: 'Run LLM-generated multi-step tool plan' },
      { title: 'Parse Tool Calls',   wraps: 'parse_tool_calls',description: 'Extract tool calls from LLM response' },
      { title: 'Serve as MCP',       wraps: 'serve_mcp',       description: 'Expose this flow as an MCP server endpoint' },
    ],
  },
  {
    label: 'SECURITY',
    icon: '🔒',
    recipes: [
      { title: 'Validate Token',   wraps: 'token_validation',   description: 'Validate JWT or API key' },
      { title: 'Check Rate Limit', wraps: 'check_rate_limit',  description: 'Enforce request rate limits' },
      { title: 'Load Credential',  wraps: 'load_identifier',   description: 'Fetch a stored credential' },
      { title: 'IP Restriction',   wraps: 'ip_restriction',    description: 'Allow or deny requests by IP CIDR range' },
      { title: 'Assign Quota Group',wraps: 'assign_quota_group',description: 'Map tenant tier to a quota group for rate limiting' },
      { title: 'Load Secret',      wraps: 'load_secret',       description: 'Fetch a secret from GSM, Vault, or env' },
    ],
  },
  {
    label: 'ROUTING & FLOW',
    icon: '🔀',
    recipes: [
      { title: 'If / Else',         wraps: 'if',        description: 'Branch based on a condition' },
      { title: 'Call Another Flow', wraps: 'call',      description: 'Execute a sub-flow' },
      { title: 'HTTP Request',      wraps: 'http_call', description: 'Call an external HTTP endpoint' },
      { title: 'Return Response',   wraps: 'return',    description: 'Return a value and exit flow' },
      { title: 'Switch',            wraps: 'switch',    description: 'Branch to different flows based on a value' },
      { title: 'Fail',              wraps: 'fail',      description: 'Return an error response and stop the flow' },
    ],
  },
  {
    label: 'DATA',
    icon: '📊',
    recipes: [
      { title: 'Extract Field',   wraps: 'json_extract_emit', description: 'Extract a field from request/response' },
      { title: 'Lookup Tenant',   wraps: 'registry_lookup',  description: 'Resolve tenant from request header' },
      { title: 'Load Service URL',wraps: 'load_service_url', description: 'Load upstream URL for the current tenant' },
      { title: 'Load Identifier', wraps: 'load_identifier',  description: 'Load a stored credential or identifier' },
    ],
  },
  {
    label: 'REQUEST',
    icon: '📥',
    recipes: [
      { title: 'Read Header',         wraps: 'bind_header',        description: 'Extract an HTTP request header into a slot' },
      { title: 'Read Query Param',    wraps: 'bind_query',         description: 'Extract a URL query parameter into a slot' },
      { title: 'Read Path Param',     wraps: 'bind_path',          description: 'Extract a path parameter like {id} into a slot' },
      { title: 'Client IP',           wraps: 'bind_client_ip',     description: 'Extract the real client IP address' },
      { title: 'Set Upstream Header', wraps: 'set_request_header', description: 'Inject a header into the upstream request' },
    ],
  },
  {
    label: 'CACHE',
    icon: '🗄️',
    recipes: [
      { title: 'Cache Read',         wraps: 'cache_get',         description: 'Read a value from the tenant cache' },
      { title: 'Cache Write',        wraps: 'cache_put',         description: 'Write a value to the tenant cache with TTL' },
      { title: 'Global Cache Read',  wraps: 'cache_get_global',  description: 'Read from the shared global cache' },
      { title: 'Global Cache Write', wraps: 'cache_put_global',  description: 'Write to the shared global cache' },
      { title: 'Extract JSON',       wraps: 'json_extract_emit', description: 'Extract fields from a JSON response body' },
    ],
  },
  {
    label: 'RESPONSE',
    icon: '📤',
    recipes: [
      { title: 'Set Body',   wraps: 'set_response_body',   description: 'Set the HTTP response body' },
      { title: 'Set Header', wraps: 'set_response_header', description: 'Set a response header' },
      { title: 'Set Status', wraps: 'set_response_status', description: 'Set the HTTP response status code' },
    ],
  },
  {
    label: 'OBSERVABILITY',
    icon: '📊',
    recipes: [
      { title: 'Transaction ID',   wraps: 'store_internal_tx_id', description: 'Store gateway transaction ID into a slot' },
      { title: 'Correlation ID',   wraps: 'bind_correlation_id',  description: 'Read or generate a correlation ID header' },
      { title: 'Emit Event',       wraps: 'emit_event',           description: 'Emit a structured event to the ingest pipeline' },
      { title: 'Log Custom Field', wraps: 'log_field',            description: 'Write a slot value into the request access log' },
    ],
  },
]

// ── Switch case helpers ───────────────────────────────────────────
type SwitchCase = { match: string; flow: string }

function parseCases(raw: string): SwitchCase[] {
  if (!raw.trim()) return []
  return raw.split(',').reduce<SwitchCase[]>((acc, part) => {
    const eq = part.indexOf('=')
    if (eq === -1) {
      if (part.trim()) acc.push({ match: part.trim(), flow: '' })
    } else {
      acc.push({ match: part.slice(0, eq).trim(), flow: part.slice(eq + 1).trim() })
    }
    return acc
  }, [])
}

function serializeCases(cases: SwitchCase[]): string {
  return cases.map(c => `${c.match}=${c.flow}`).join(',')
}

// ── Component ─────────────────────────────────────────────────────
export default function FlowDesigner({
  blocks, steps, setSteps, flowName, setFlowName, savedFlows, onSaveFlow,
}: Props) {
  const [filter, setFilter]       = useState('')
  const [dragOver, setDragOver]   = useState(false)
  const [expanded, setExpanded]   = useState<Set<number>>(new Set())
  const [justSaved, setJustSaved] = useState(false)
  const [mode, setMode]           = useState<'visual' | 'expert'>('visual')
  // Pending (not-yet-saved) new claim rows, keyed by step index
  type DraftClaim = { key: string; mode: 'static' | 'var'; value: string }
  const [pendingClaims, setPendingClaims] = useState<Record<number, DraftClaim>>({})
  function clearPendingClaim(stepIdx: number) {
    setPendingClaims(prev => { const n = { ...prev }; delete n[stepIdx]; return n })
  }

  // map: block-type → { fieldKey → FieldDef }
  const fieldMap = useMemo<Record<string, Record<string, FieldDef>>>(() =>
    Object.fromEntries(
      blocks.map(b => [b.type, Object.fromEntries((b.fields ?? []).map(f => [f.key, f]))])
    ),
    [blocks],
  )

  // map: block-type → PaletteBlock (for visual mode drag payload lookup)
  const blockByType = useMemo<Record<string, PaletteBlock>>(() =>
    Object.fromEntries(blocks.map(b => [b.type, b])),
    [blocks],
  )

  // Build a PaletteBlock drag-payload from a visual recipe
  function recipeToBlock(recipe: VisualRecipe): PaletteBlock {
    const existing = blockByType[recipe.wraps]
    if (existing) return existing
    // Fallback for actions not in schema
    return {
      type: recipe.wraps,
      title: recipe.title,
      description: recipe.description,
      category: 'visual',
      capability: '',
      supports_nested: false,
      defaults: {},
      fields: [],
    }
  }

  // Synthetic call-blocks for each saved flow
  const callFieldDefs = useMemo(() => blocks.find(b => b.type === 'call')?.fields ?? [], [blocks])
  const savedFlowBlocks: PaletteBlock[] = useMemo(() =>
    savedFlows.map(sf => ({
      type: 'call',
      title: sf.name,
      description: `${sf.steps.length} step${sf.steps.length !== 1 ? 's' : ''}`,
      category: 'my-flows',
      capability: 'sub-flow',
      supports_nested: false,
      defaults: { flow_name: sf.name },
      fields: callFieldDefs,
    })),
    [savedFlows, callFieldDefs],
  )

  // Filtered lists
  const q = filter.toLowerCase()
  const filteredSaved = savedFlowBlocks.filter(b =>
    !filter || b.title.toLowerCase().includes(q) || 'my-flows'.includes(q),
  )
  const filteredBlocks = blocks.filter(b =>
    !filter ||
    b.category.toLowerCase().includes(q) ||
    b.title.toLowerCase().includes(q)     ||
    b.type.toLowerCase().includes(q),
  )

  // ── Mutations ───────────────────────────────────────────────────
  function handleDrop(e: React.DragEvent) {
    e.preventDefault()
    setDragOver(false)
    const raw = e.dataTransfer.getData('application/json')
    if (!raw) return
    const b: PaletteBlock = JSON.parse(raw)
    const idx = steps.length
    setSteps([...steps, { action: b.type, ...b.defaults }])
    setExpanded(prev => new Set([...prev, idx]))
  }

  function toggleExpand(i: number) {
    setExpanded(prev => {
      const next = new Set(prev)
      next.has(i) ? next.delete(i) : next.add(i)
      return next
    })
  }

  function updateStep(i: number, key: string, value: string) {
    setSteps(steps.map((s, idx) => idx === i ? { ...s, [key]: value } : s))
  }

  function removeStep(i: number) {
    setSteps(steps.filter((_, idx) => idx !== i))
    setExpanded(prev => {
      const next = new Set<number>()
      prev.forEach(idx => { if (idx < i) next.add(idx); else if (idx > i) next.add(idx - 1) })
      return next
    })
  }

  function handleSave() {
    if (!flowName.trim() || steps.length === 0) return
    onSaveFlow()
    setJustSaved(true)
    setTimeout(() => setJustSaved(false), 1800)
  }

  // ── Field input with source-ref chips ──────────────────────────
  function fieldInput(
    stepIdx: number,
    key: string,
    value: string,
    def?: FieldDef,
    opts?: { smart?: boolean; onBlur?: (v: string) => void },
  ) {
    const refs = findSourceRefs(value)
    return (
      <div key={key} className="field-row">
        <label className="field-label">{def?.label ?? key}</label>
        <input
          className="input"
          value={value ?? ''}
          placeholder={def?.placeholder ?? key}
          onChange={e => updateStep(stepIdx, key, e.target.value)}
          onBlur={opts?.smart ? e => {
            const normalised = smartCondition(e.target.value)
            if (normalised !== e.target.value) updateStep(stepIdx, key, normalised)
            opts.onBlur?.(normalised)
          } : undefined}
        />
        {/* Source-ref chips */}
        {refs.length > 0 && (
          <div className="ref-chips">
            {refs.map(ref => (
              <span key={ref.raw} className="ref-chip">
                {ref.raw} <span className="ref-chip-arrow">→</span> var: {ref.slotName}
              </span>
            ))}
          </div>
        )}
        {def?.description && <span className="field-desc">{def.description}</span>}
      </div>
    )
  }

  // ── Special step bodies ─────────────────────────────────────────
  function renderIfBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    return (
      <div className="step-body">
        {fieldInput(i, 'condition', step['condition'] ?? '', defs['condition'], { smart: true })}
        <div className="branch-row">
          <div className="branch-cell branch-then">
            <div className="branch-label">✓ Then</div>
            <input
              className="input"
              value={step['then'] ?? ''}
              placeholder={defs['then']?.placeholder ?? 'flow name'}
              onChange={e => updateStep(i, 'then', e.target.value)}
            />
            {defs['then']?.description && (
              <span className="field-desc">{defs['then'].description}</span>
            )}
          </div>
          <div className="branch-cell branch-else">
            <div className="branch-label branch-label-else">✗ Else</div>
            <input
              className="input"
              value={step['else'] ?? ''}
              placeholder={defs['else']?.placeholder ?? 'flow name (optional)'}
              onChange={e => updateStep(i, 'else', e.target.value)}
            />
            {defs['else']?.description && (
              <span className="field-desc">{defs['else'].description}</span>
            )}
          </div>
        </div>
      </div>
    )
  }

  function renderSwitchBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    const cases = parseCases(step['cases'] ?? '')
    return (
      <div className="step-body">
        {/* Match slot — smart: accepts header.X-TID or bare "X-TID" */}
        {fieldInput(i, 'as', step['as'] ?? '', defs['as'], { smart: true })}
        <div className="switch-cases-header">
          <span className="field-label">Cases</span>
          {defs['cases']?.description && (
            <span className="field-desc">{defs['cases'].description}</span>
          )}
        </div>
        <div className="switch-cases">
          {cases.length === 0 && <span className="hint">No cases yet — add one below.</span>}
          {cases.map((c, ci) => (
            <div key={ci} className="switch-case-row">
              <input
                className="input case-match"
                value={c.match}
                placeholder="match value"
                onChange={e => {
                  const upd = [...cases]; upd[ci] = { ...upd[ci], match: e.target.value }
                  updateStep(i, 'cases', serializeCases(upd))
                }}
              />
              <span className="case-arrow">→</span>
              <input
                className="input case-flow"
                value={c.flow}
                placeholder="flow name"
                onChange={e => {
                  const upd = [...cases]; upd[ci] = { ...upd[ci], flow: e.target.value }
                  updateStep(i, 'cases', serializeCases(upd))
                }}
              />
              <button
                className="btn muted case-remove"
                onClick={() => updateStep(i, 'cases', serializeCases(cases.filter((_, j) => j !== ci)))}
              >×</button>
            </div>
          ))}
          <button
            className="btn muted mt4"
            onClick={() => updateStep(i, 'cases', serializeCases([...cases, { match: '', flow: '' }]))}
          >+ Add case</button>
        </div>
      </div>
    )
  }

  function renderGenericBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    const params = Object.entries(step).filter(([k]) => k !== 'action')
    if (params.length === 0) {
      return (
        <div className="step-body">
          <span className="hint">No parameters for this step.</span>
        </div>
      )
    }
    // Fields that benefit from smart expression parsing
    const smartFields = new Set(['condition', 'source', 'as', 'url'])
    return (
      <div className="step-body">
        {params.map(([k, v]) =>
          fieldInput(i, k, v, defs[k], smartFields.has(k) ? { smart: true } : undefined)
        )}
      </div>
    )
  }

  // ── Lineage annotations ────────────────────────────────────────
  function renderLineageAnnotations(step: FlowStep) {
    const out = step['as'] ? String(step['as']) : ''
    if (!out) return null
    return (
      <div style={{ marginTop: 6, fontSize: 11, color: '#34d399', fontFamily: 'monospace', opacity: 0.85 }}>
        → writes to variable: <strong>{out}</strong>
      </div>
    )
  }

  // ── Collect variable names from prior steps (for pickers) ──────
  function slotsUpTo(upToIdx: number): string[] {
    const vars: string[] = []
    for (let j = 0; j < upToIdx && j < steps.length; j++) {
      const s = steps[j]
      if (s['as'] && typeof s['as'] === 'string') vars.push(s['as'])
      if (['bind_client_ip','store_internal_tx_id','bind_correlation_id'].includes(s.action) && s['key_identifier']) {
        vars.push(s['key_identifier'] as string)
      }
    }
    return [...new Set(vars.filter(Boolean))]
  }

  // ── B6: http_call editor ───────────────────────────────────────
  function renderHttpCallBody(step: FlowStep, i: number) {
    const url        = (step['url']        ?? '') as string
    const urlVar     = (step['url_var']    ?? '') as string
    const method     = (step['method']     ?? 'GET') as string
    const headersRaw = (step['headers_json'] ?? '') as string
    const bodySlot   = (step['body_slot']  ?? '') as string
    const responseSlot = (step['as']       ?? '') as string
    const timeout    = (step['timeout']    ?? '5000') as string
    const prevVars   = slotsUpTo(i)

    return (
      <div className="step-body">
        {/* Method + URL row */}
        <div style={{ display: 'flex', gap: 6, alignItems: 'flex-end', marginBottom: 6 }}>
          <div>
            <label className="field-label">Method</label>
            <select className="input" value={method} onChange={e => updateStep(i, 'method', e.target.value)}
              style={{ width: 90 }}>
              {['GET','POST','PUT','DELETE','PATCH'].map(m => <option key={m}>{m}</option>)}
            </select>
          </div>
          <div style={{ flex: 1 }}>
            <label className="field-label">URL</label>
            <input className="input" placeholder="https://api.example.com/v1/data"
              value={url} onChange={e => updateStep(i, 'url', e.target.value)} />
          </div>
        </div>
        {/* URL from variable */}
        {prevVars.length > 0 && (
          <div style={{ marginBottom: 6 }}>
            <label className="field-label">URL from variable (overrides static URL)</label>
            <select className="input" value={urlVar}
              onChange={e => updateStep(i, 'url_var', e.target.value)}>
              <option value="">— not set —</option>
              {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
            </select>
          </div>
        )}
        {/* Body slot */}
        <label className="field-label">Request body variable</label>
        {prevVars.length > 0 ? (
          <select className="input" value={bodySlot} onChange={e => updateStep(i, 'body_slot', e.target.value)}>
            <option value="">— none —</option>
            {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        ) : (
          <input className="input" placeholder="var.body" value={bodySlot}
            onChange={e => updateStep(i, 'body_slot', e.target.value)} />
        )}
        {/* Response variable */}
        <label className="field-label" style={{ marginTop: 6 }}>Store response as variable</label>
        <input className="input" placeholder="var.response" value={responseSlot}
          onChange={e => updateStep(i, 'as', e.target.value)} />
        {/* Timeout */}
        <label className="field-label" style={{ marginTop: 6 }}>Timeout (ms)</label>
        <input className="input" type="number" value={timeout}
          onChange={e => updateStep(i, 'timeout', e.target.value)} />
        {/* Headers JSON */}
        <label className="field-label" style={{ marginTop: 6 }}>Extra headers (JSON object)</label>
        <input className="input" placeholder='{"X-API-Key": "{{var.key}}"}' value={headersRaw}
          onChange={e => updateStep(i, 'headers_json', e.target.value)} />
        {/* Summary */}
        {(url || urlVar) && (
          <div style={{ marginTop: 6, fontSize: 11, color: 'var(--accent)', fontFamily: 'monospace', opacity: 0.85 }}>
            {method} {urlVar ? `[${urlVar}]` : url}
            {responseSlot ? `  →  ${responseSlot}` : ''}
          </div>
        )}
        {renderLineageAnnotations(step)}
      </div>
    )
  }

  // ── B6: token_validation editor (7 sections) ───────────────────
  function renderTokenValidationBody(step: FlowStep, i: number) {
    let inputObj: Record<string, string> = {}
    try { inputObj = JSON.parse((step['input'] ?? '{}') as string) } catch { /* ignore */ }

    // Atomic multi-key update — all changes go in a single setSteps call so
    // no stale-closure overwrite when two keys must change together.
    function updateInputKeys(updates: Record<string, string>) {
      const next = { ...inputObj }
      for (const [k, v] of Object.entries(updates)) {
        if (v) next[k] = v; else delete next[k]
      }
      updateStep(i, 'input', JSON.stringify(next))
    }
    function updateInputKey(key: string, value: string) {
      updateInputKeys({ [key]: value })
    }

    const prevVars = slotsUpTo(i)

    function splitComma(s: string): string[] {
      return s.split(',').map(x => x.trim()).filter(Boolean)
    }

    // Reusable Static | From variable toggle field
    function SourceField({ label, desc, staticKey, varKey, staticType = 'text', staticOptions, placeholder, defaultVal }: {
      label: string; desc?: string; staticKey: string; varKey: string
      staticType?: 'text' | 'number' | 'select'; staticOptions?: { value: string; label: string }[]
      placeholder?: string; defaultVal?: string
    }) {
      // Mode is determined by __varmode__ sentinel (set when "From variable" clicked before
      // a variable is picked) OR by the varKey already having a value.
      const mode = (inputObj['__varmode__' + varKey] || inputObj[varKey]) ? 'var' : 'static'
      const staticVal = inputObj[staticKey] ?? defaultVal ?? ''
      const varVal    = inputObj[varKey] ?? ''
      return (
        <div style={{ marginBottom: 10 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
            <label className="field-label" style={{ margin: 0, flex: 1 }}>{label}</label>
            <div style={{ display: 'flex', borderRadius: 4, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.12)', fontSize: 10 }}>
              <button style={{ padding: '2px 8px', background: mode === 'static' ? 'var(--accent)' : 'transparent', color: mode === 'static' ? '#000' : 'var(--muted)', border: 'none', cursor: 'pointer', fontWeight: 600 }}
                onClick={() => {
                  if (mode === 'var') {
                    // Atomically clear sentinel + variable; optionally restore default
                    const updates: Record<string, string> = {
                      ['__varmode__' + varKey]: '',
                      [varKey]: '',
                    }
                    if (defaultVal && !inputObj[staticKey]) updates[staticKey] = defaultVal
                    updateInputKeys(updates)
                  }
                }}>
                Static
              </button>
              <button style={{ padding: '2px 8px', background: mode === 'var' ? 'var(--accent)' : 'transparent', color: mode === 'var' ? '#000' : 'var(--muted)', border: 'none', cursor: 'pointer', fontWeight: 600 }}
                onClick={() => {
                  if (mode === 'static') {
                    // Atomically set sentinel + clear static so only one path is active
                    updateInputKeys({ ['__varmode__' + varKey]: '1', [staticKey]: '' })
                  }
                }}>
                From variable
              </button>
            </div>
          </div>
          {mode === 'static' ? (
            staticType === 'select' && staticOptions ? (
              <select className="input" value={staticVal} onChange={e => updateInputKey(staticKey, e.target.value)}>
                {staticOptions.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            ) : (
              <input className="input" type={staticType} placeholder={placeholder ?? defaultVal ?? ''} value={staticVal}
                onChange={e => updateInputKey(staticKey, e.target.value)} />
            )
          ) : (
            prevVars.length > 0 ? (
              <select className="input" value={varVal} onChange={e => {
                updateInputKey(varKey, e.target.value)
                updateInputKey('__varmode__' + varKey, '') // sentinel no longer needed once a var is picked
              }}>
                <option value="">— pick a variable —</option>
                {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            ) : (
              <div style={{ padding: '8px 10px', borderRadius: 6, background: 'rgba(255,255,255,0.04)', border: '1px dashed rgba(255,255,255,0.15)', fontSize: 11, color: 'var(--muted)' }}>
                No variables yet — add a step before this one (e.g. <code style={{ fontFamily: 'monospace' }}>load_service_url</code>, <code style={{ fontFamily: 'monospace' }}>cache_get</code>, <code style={{ fontFamily: 'monospace' }}>bind_body</code>).
              </div>
            )
          )}
          {desc && <span className="field-desc">{desc}</span>}
        </div>
      )
    }

    function SectionHeader({ label }: { label: string }) {
      return (
        <div style={{ padding: '5px 0', marginBottom: 8, borderBottom: '1px solid rgba(255,255,255,0.07)' }}>
          <span style={{ fontSize: 11, fontWeight: 700, color: 'var(--accent)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            {label}
          </span>
        </div>
      )
    }

    // ── Section 1: Token Source ──
    const keyId = (step['key_identifier'] ?? 'header.Authorization') as string
    const commonSources = ['header.Authorization', 'header.X-Auth-Token', 'header.X-Api-Key', 'query.token', 'cookie.token']

    // ── Section 3: Validate checks ──
    // Use 'in' check so we can distinguish "key explicitly set to empty" from "key never set".
    // "Never set" shows all defaults ticked; "explicitly set (even to '')" respects the stored value.
    const validateChecks = new Set(
      inputObj['jwt.validate_var'] ? [] :
      ('jwt.validate' in inputObj)
        ? splitComma(inputObj['jwt.validate'] ?? '')
        : splitComma('signature,issuer,audience,expiry,not_before')
    )
    const validateMode = inputObj['jwt.validate_var'] ? 'var' : 'static'
    function toggleValidate(key: string) {
      const next = new Set(validateChecks)
      next.has(key) ? next.delete(key) : next.add(key)
      updateInputKey('jwt.validate', Array.from(next).join(','))
    }
    const showClaimValues = validateChecks.has('issuer') || validateChecks.has('audience') || validateMode === 'var'

    // ── Section 6: Custom claims ──
    const staticClaims: Record<string,string> = (() => { try { return JSON.parse(inputObj['jwt.custom_claims'] || '{}') } catch { return {} } })()
    const varClaims: Record<string,string>    = (() => { try { return JSON.parse(inputObj['jwt.custom_claims_vars'] || '{}') } catch { return {} } })()
    type ClaimRow = { key: string; mode: 'static' | 'var'; value: string }
    const claimRows: ClaimRow[] = [
      ...Object.entries(staticClaims).map(([k, v]) => ({ key: k, mode: 'static' as const, value: v })),
      ...Object.entries(varClaims).map(([k, v])    => ({ key: k, mode: 'var'    as const, value: v })),
    ]
    function saveClaimRows(rows: ClaimRow[]) {
      const ns: Record<string,string> = {}
      const nv: Record<string,string> = {}
      rows.forEach(r => { if (!r.key) return; if (r.mode === 'static') ns[r.key] = r.value; else nv[r.key] = r.value })
      const next = { ...inputObj }
      if (Object.keys(ns).length > 0) next['jwt.custom_claims'] = JSON.stringify(ns); else delete next['jwt.custom_claims']
      if (Object.keys(nv).length > 0) next['jwt.custom_claims_vars'] = JSON.stringify(nv); else delete next['jwt.custom_claims_vars']
      updateStep(i, 'input', JSON.stringify(next))
    }

    // ── Section 7: failure mode ──
    const failureModeStatic = inputObj['jwt.on_failure'] || 'stop'
    const showStopFields    = !inputObj['jwt.on_failure_var'] && failureModeStatic !== 'continue'
    const showContinueFields= !inputObj['jwt.on_failure_var'] ? failureModeStatic === 'continue' : true

    return (
      <div className="step-body">

        {/* ── §1 Token Source ── */}
        <SectionHeader label="1 · Token Source" />
        <div style={{ marginBottom: 10 }}>
          <label className="field-label">Where to read the token</label>
          <select className="input" value={commonSources.includes(keyId) ? keyId : prevVars.includes(keyId) ? keyId : '__custom__'}
            onChange={e => { if (e.target.value !== '__custom__') updateStep(i, 'key_identifier', e.target.value) }}>
            {commonSources.map(s => <option key={s} value={s}>{s}</option>)}
            {prevVars.length > 0 && <option disabled>--- Variables ---</option>}
            {prevVars.map(v => <option key={v} value={v}>{v} (variable)</option>)}
            {!commonSources.includes(keyId) && !prevVars.includes(keyId) && keyId &&
              <option value="__custom__">custom: {keyId}</option>}
          </select>
          {!commonSources.includes(keyId) && !prevVars.includes(keyId) && (
            <input className="input" style={{ marginTop: 4 }} placeholder="header.Authorization"
              value={keyId} onChange={e => updateStep(i, 'key_identifier', e.target.value)} />
          )}
        </div>

        {/* ── §2 JWKS / Signature ── */}
        <SectionHeader label="2 · JWKS / Signature" />
        <SourceField label="JWKS endpoint URL" staticKey="jwt.jwks_uri" varKey="jwt.jwks_uri_var"
          placeholder="https://YOUR_IDP/.well-known/jwks.json"
          desc="The JSON Web Key Set URL of your identity provider." />
        <SourceField label="Algorithm" staticKey="jwt.alg" varKey="jwt.alg_var"
          staticType="select" defaultVal="RS256"
          staticOptions={[{value:'RS256',label:'RS256'},{value:'RS384',label:'RS384'},{value:'RS512',label:'RS512'},{value:'HS256',label:'HS256'}]} />
        <SourceField label="Clock leeway (seconds)" staticKey="jwt.leeway_seconds" varKey="jwt.leeway_var"
          staticType="number" placeholder="30" defaultVal="30" />
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
          <input type="checkbox" id={`prefetch_${i}`}
            checked={inputObj['jwt.prefetch_jwks'] !== 'false'}
            onChange={e => updateInputKey('jwt.prefetch_jwks', e.target.checked ? 'true' : 'false')} />
          <label htmlFor={`prefetch_${i}`} className="field-label" style={{ margin: 0 }}>
            Prefetch JWKS at deploy time
          </label>
        </div>

        {/* ── §3 What to Validate ── */}
        <SectionHeader label="3 · What to Validate" />
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
          <label className="field-label" style={{ margin: 0, flex: 1 }}>Validation checks</label>
          <div style={{ display: 'flex', borderRadius: 4, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.12)', fontSize: 10 }}>
            <button style={{ padding: '2px 8px', background: validateMode === 'static' ? 'var(--accent)' : 'transparent', color: validateMode === 'static' ? '#000' : 'var(--muted)', border: 'none', cursor: 'pointer', fontWeight: 600 }}
              onClick={() => updateInputKey('jwt.validate_var', '')}>Static</button>
            <button style={{ padding: '2px 8px', background: validateMode === 'var' ? 'var(--accent)' : 'transparent', color: validateMode === 'var' ? '#000' : 'var(--muted)', border: 'none', cursor: 'pointer', fontWeight: 600 }}
              onClick={() => updateInputKey('jwt.validate', '')}>From variable</button>
          </div>
        </div>
        {validateMode === 'static' ? (
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '4px 12px', marginBottom: 10 }}>
            {[['signature','Signature'],['expiry','Expiry'],['not_before','Not Before'],['issuer','Issuer (requires §4)'],['audience','Audience (requires §4)']].map(([k,lbl]) => (
              <label key={k} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--text)', cursor: 'pointer' }}>
                <input type="checkbox" checked={validateChecks.has(k)} onChange={() => toggleValidate(k)} />
                {lbl}
              </label>
            ))}
          </div>
        ) : (
          prevVars.length > 0 ? (
            <select className="input" style={{ marginBottom: 10 }} value={inputObj['jwt.validate_var'] ?? ''}
              onChange={e => updateInputKey('jwt.validate_var', e.target.value)}>
              <option value="">— pick a variable —</option>
              {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
            </select>
          ) : (
            <div style={{ padding: '8px 10px', borderRadius: 6, background: 'rgba(255,255,255,0.04)', border: '1px dashed rgba(255,255,255,0.15)', fontSize: 11, color: 'var(--muted)', marginBottom: 10 }}>
              No variables yet.
            </div>
          )
        )}

        {/* ── §4 Claim Values ── */}
        {showClaimValues && (
          <>
            <SectionHeader label="4 · Claim Values" />
            <SourceField label="Issuer (iss)" staticKey="jwt.issuer" varKey="jwt.issuer_var"
              placeholder="https://accounts.example.com" />
            <SourceField label="Audience (aud)" staticKey="jwt.audience" varKey="jwt.audience_var"
              placeholder="my-api" />
          </>
        )}

        {/* ── §5 Scopes ── */}
        {/* jwt.__scopes_enabled is the sentinel that records whether the user has toggled this on */}
        <SectionHeader label="5 · Scopes" />
        {(() => {
          const scopesOn = !!(inputObj['jwt.__scopes_enabled'] || inputObj['jwt.required_scopes'] || inputObj['jwt.required_scopes_var'] || inputObj['__varmode__jwt.required_scopes_var'])
          return (
            <>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
                <input type="checkbox" id={`validate_scopes_${i}`}
                  checked={scopesOn}
                  onChange={e => {
                    if (e.target.checked) {
                      updateInputKey('jwt.__scopes_enabled', '1')
                    } else {
                      updateInputKeys({
                        'jwt.__scopes_enabled': '',
                        'jwt.required_scopes': '',
                        'jwt.required_scopes_var': '',
                        '__varmode__jwt.required_scopes_var': '',
                      })
                    }
                  }} />
                <label htmlFor={`validate_scopes_${i}`} className="field-label" style={{ margin: 0 }}>
                  Validate scopes
                </label>
              </div>
              {scopesOn && (
                <>
                  <SourceField label="Token must have these scopes" staticKey="jwt.required_scopes" varKey="jwt.required_scopes_var"
                    placeholder="read:orders,write:orders"
                    desc="Comma-separated scope values the JWT must contain (e.g. read:orders,write:orders)" />
                  <details style={{ marginBottom: 10 }}>
                    <summary style={{ cursor: 'pointer', fontSize: 11, color: 'var(--muted)', userSelect: 'none' }}>Advanced</summary>
                    <div style={{ paddingTop: 8 }}>
                      <SourceField label="Scope claim keys" staticKey="jwt.scope_claims" varKey="jwt.scope_claims_var"
                        placeholder="scope,scp" defaultVal="scope,scp"
                        desc="JWT claim fields that hold scopes. Almost always scope or scp — only change for non-standard identity providers." />
                    </div>
                  </details>
                </>
              )}
            </>
          )
        })()}

        {/* ── §6 Custom Claims ── */}
        <SectionHeader label="6 · Custom Claims" />
        {claimRows.map((row, ci) => (
          <div key={ci} style={{ display: 'flex', gap: 6, alignItems: 'center', marginBottom: 6 }}>
            <input className="input" style={{ flex: 1 }} placeholder="claim key (e.g. role)"
              value={row.key}
              onChange={e => { const r = [...claimRows]; r[ci] = { ...r[ci], key: e.target.value }; saveClaimRows(r) }} />
            <div style={{ display: 'flex', borderRadius: 4, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.12)', fontSize: 10 }}>
              <button style={{ padding: '2px 6px', background: row.mode === 'static' ? 'var(--accent)' : 'transparent', color: row.mode === 'static' ? '#000' : 'var(--muted)', border: 'none', cursor: 'pointer' }}
                onClick={() => { const r = [...claimRows]; r[ci] = { ...r[ci], mode: 'static' }; saveClaimRows(r) }}>Static</button>
              <button style={{ padding: '2px 6px', background: row.mode === 'var' ? 'var(--accent)' : 'transparent', color: row.mode === 'var' ? '#000' : 'var(--muted)', border: 'none', cursor: 'pointer' }}
                onClick={() => { const r = [...claimRows]; r[ci] = { ...r[ci], mode: 'var' }; saveClaimRows(r) }}>Var</button>
            </div>
            {row.mode === 'static' ? (
              <input className="input" style={{ flex: 1 }} placeholder="expected value"
                value={row.value}
                onChange={e => { const r = [...claimRows]; r[ci] = { ...r[ci], value: e.target.value }; saveClaimRows(r) }} />
            ) : (
              <select className="input" style={{ flex: 1 }} value={row.value}
                onChange={e => { const r = [...claimRows]; r[ci] = { ...r[ci], value: e.target.value }; saveClaimRows(r) }}>
                <option value="">— variable —</option>
                {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            )}
            <button style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 16, lineHeight: 1 }}
              onClick={() => saveClaimRows(claimRows.filter((_, j) => j !== ci))}>×</button>
          </div>
        ))}
        {/* Pending new claim row — lives in component state until the user types a key */}
        {pendingClaims[i] && (() => {
          const draft = pendingClaims[i]
          function updateDraft(patch: Partial<DraftClaim>) {
            setPendingClaims(prev => ({ ...prev, [i]: { ...draft, ...patch } }))
          }
          function commitDraft() {
            if (!draft.key.trim()) { clearPendingClaim(i); return }
            saveClaimRows([...claimRows, draft])
            clearPendingClaim(i)
          }
          return (
            <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginBottom: 6, padding: '6px 8px', borderRadius: 6, border: '1px dashed rgba(87,181,255,0.4)', background: 'rgba(87,181,255,0.04)' }}>
              <input className="input" style={{ flex: 1 }} placeholder="claim key (e.g. role)" autoFocus
                value={draft.key}
                onChange={e => updateDraft({ key: e.target.value })}
                onKeyDown={e => { if (e.key === 'Enter') commitDraft(); if (e.key === 'Escape') clearPendingClaim(i) }} />
              <div style={{ display: 'flex', borderRadius: 4, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.12)', fontSize: 10 }}>
                <button style={{ padding: '2px 6px', background: draft.mode === 'static' ? 'var(--accent)' : 'transparent', color: draft.mode === 'static' ? '#000' : 'var(--muted)', border: 'none', cursor: 'pointer' }}
                  onClick={() => updateDraft({ mode: 'static' })}>Static</button>
                <button style={{ padding: '2px 6px', background: draft.mode === 'var' ? 'var(--accent)' : 'transparent', color: draft.mode === 'var' ? '#000' : 'var(--muted)', border: 'none', cursor: 'pointer' }}
                  onClick={() => updateDraft({ mode: 'var' })}>Var</button>
              </div>
              {draft.mode === 'static' ? (
                <input className="input" style={{ flex: 1 }} placeholder="expected value"
                  value={draft.value}
                  onChange={e => updateDraft({ value: e.target.value })}
                  onKeyDown={e => { if (e.key === 'Enter') commitDraft(); if (e.key === 'Escape') clearPendingClaim(i) }} />
              ) : (
                <select className="input" style={{ flex: 1 }} value={draft.value}
                  onChange={e => updateDraft({ value: e.target.value })}>
                  <option value="">— variable —</option>
                  {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
                </select>
              )}
              <button style={{ padding: '2px 7px', borderRadius: 4, border: 'none', background: 'var(--accent)', color: '#000', cursor: 'pointer', fontWeight: 700, fontSize: 11 }}
                onClick={commitDraft} title="Save (Enter)">✓</button>
              <button style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 16, lineHeight: 1 }}
                onClick={() => clearPendingClaim(i)} title="Cancel (Esc)">×</button>
            </div>
          )
        })()}
        <button className="btn muted" style={{ marginBottom: 10, fontSize: 11 }}
          onClick={() => setPendingClaims(prev => ({ ...prev, [i]: { key: '', mode: 'static', value: '' } }))}>
          ＋ Add claim check
        </button>
        <span className="field-desc">All listed claims must match the JWT exactly. Press Enter to save or Esc to cancel.</span>

        {/* ── §7 On Failure & Outputs ── */}
        <SectionHeader label="7 · On Failure &amp; Outputs" />
        <SourceField label="On failure mode" staticKey="jwt.on_failure" varKey="jwt.on_failure_var"
          staticType="select" defaultVal="stop"
          staticOptions={[{value:'stop',label:'Stop — return error response'},{value:'continue',label:'Continue — write result variable and proceed'}]}
          desc="Stop halts the flow and returns an HTTP error. Continue lets the flow proceed and writes a result value." />
        {showStopFields && (
          <>
            <SourceField label="Failure status code" staticKey="jwt.failure_status" varKey="jwt.failure_status_var"
              staticType="number" defaultVal="401" />
            <SourceField label="Failure response body" staticKey="jwt.failure_body" varKey="jwt.failure_body_var"
              defaultVal="unauthorized" />
          </>
        )}
        {showContinueFields && (
          <>
            <div style={{ marginBottom: 10 }}>
              <label className="field-label">Result variable (output)</label>
              <input className="input" placeholder="var.auth_result"
                value={inputObj['jwt.result_var'] ?? ''}
                onChange={e => updateInputKey('jwt.result_var', e.target.value)} />
              <span className="field-desc">Variable name to write the result value into.</span>
            </div>
            <SourceField label="Success result value" staticKey="jwt.result_success" varKey="jwt.result_success_var"
              defaultVal="true" />
            <SourceField label="Failure result value" staticKey="jwt.result_failure" varKey="jwt.result_failure_var"
              defaultVal="false" />
            {inputObj['jwt.result_var'] && (
              <div style={{ padding: '8px 10px', borderRadius: 6, background: 'rgba(52,211,153,0.06)', border: '1px solid rgba(52,211,153,0.2)', fontSize: 11, color: '#34d399', marginBottom: 10 }}>
                Tip: Add an <code style={{ fontFamily: 'monospace' }}>if</code> step after this one — condition: <code style={{ fontFamily: 'monospace' }}>{inputObj['jwt.result_var'] || 'result_var'} == {inputObj['jwt.result_failure'] || 'false'}</code> — then route to an error handler flow.
              </div>
            )}
          </>
        )}
        {([
          { key: 'jwt.claims_var',    label: 'Claims output variable',    placeholder: 'var.jwt_claims',     desc: 'Variable to write all JWT claims JSON into on success.' },
          { key: 'jwt.subject_var',   label: 'Subject output variable',   placeholder: 'var.jwt_subject',    desc: 'Variable to write the JWT sub (subject) claim into on success.' },
          { key: 'jwt.client_id_var', label: 'Client ID output variable', placeholder: 'var.jwt_client_id',  desc: 'Variable to write the client_id / azp claim into on success.' },
          { key: 'jwt.scopes_out_var',label: 'Scopes output variable',    placeholder: 'var.jwt_scopes',     desc: 'Variable to write comma-separated parsed scopes into on success.' },
        ] as const).map(({ key, label, placeholder, desc }) => {
          const val = inputObj[key] ?? ''
          return (
            <div key={key} style={{ marginBottom: 10 }}>
              <label className="field-label">{label} (optional)</label>
              {/* Offer existing variables as quick-pick but always allow typing a new name */}
              {prevVars.length > 0 && (
                <select className="input" style={{ marginBottom: 4 }} value={prevVars.includes(val) ? val : ''}
                  onChange={e => updateInputKey(key, e.target.value)}>
                  <option value="">— pick existing or type below —</option>
                  {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
                </select>
              )}
              <input className="input" placeholder={placeholder} value={val}
                onChange={e => updateInputKey(key, e.target.value)} />
              <span className="field-desc">{desc}</span>
            </div>
          )
        })}

        {renderLineageAnnotations(step)}
      </div>
    )
  }

  // ── B6: set_response_body editor ──────────────────────────────
  function renderSetResponseBody(step: FlowStep, i: number) {
    const source   = (step['source'] ?? '') as string
    const prevVars = slotsUpTo(i)
    return (
      <div className="step-body">
        <label className="field-label">Send variable as response body</label>
        {prevVars.length > 0 ? (
          <>
            <select className="input" value={prevVars.includes(source) ? source : ''}
              onChange={e => updateStep(i, 'source', e.target.value)}>
              <option value="">— pick a variable —</option>
              {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
            </select>
            {!prevVars.includes(source) && (
              <input className="input" style={{ marginTop: 4 }} placeholder="var.body" value={source}
                onChange={e => updateStep(i, 'source', e.target.value)} />
            )}
          </>
        ) : (
          <input className="input" placeholder="var.body" value={source}
            onChange={e => updateStep(i, 'source', e.target.value)} />
        )}
        <span className="field-desc">The variable's value becomes the HTTP response body sent to the caller.</span>
        {source && (
          <div style={{ marginTop: 6, fontSize: 11, color: '#34d399', fontFamily: 'monospace', opacity: 0.85 }}>
            → caller receives: {source}
          </div>
        )}
        {renderLineageAnnotations(step)}
      </div>
    )
  }

  // ── B6: append_message editor ──────────────────────────────────
  function renderAppendMessageBody(step: FlowStep, i: number) {
    const historyVar = (step['key_identifier'] ?? '') as string
    const contentVar = (step['as']             ?? '') as string
    const prevVars   = slotsUpTo(i)
    let inputJson: Record<string, string> = {}
    try { inputJson = JSON.parse((step['input'] ?? '{}') as string) } catch { /* ignore */ }
    const role     = inputJson['role']      ?? 'user'
    const maxTurns = inputJson['max_turns'] ?? ''

    function updateMsgInput(key: string, value: string) {
      const next = { ...inputJson }
      if (value) next[key] = value; else delete next[key]
      updateStep(i, 'input', JSON.stringify(next))
    }

    return (
      <div className="step-body">
        <label className="field-label">History variable</label>
        {prevVars.length > 0 ? (
          <select className="input" value={historyVar} onChange={e => updateStep(i, 'key_identifier', e.target.value)}>
            <option value="">— pick a variable —</option>
            {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        ) : (
          <input className="input" placeholder="var.history" value={historyVar}
            onChange={e => updateStep(i, 'key_identifier', e.target.value)} />
        )}

        <label className="field-label" style={{ marginTop: 6 }}>Role</label>
        <select className="input" value={role} onChange={e => updateMsgInput('role', e.target.value)}>
          <option value="user">user</option>
          <option value="assistant">assistant</option>
          <option value="system">system</option>
        </select>

        <label className="field-label" style={{ marginTop: 6 }}>Content variable</label>
        {prevVars.length > 0 ? (
          <select className="input" value={contentVar} onChange={e => updateStep(i, 'as', e.target.value)}>
            <option value="">— pick a variable —</option>
            {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        ) : (
          <input className="input" placeholder="var.user_message" value={contentVar}
            onChange={e => updateStep(i, 'as', e.target.value)} />
        )}

        <label className="field-label" style={{ marginTop: 6 }}>Max turns (optional)</label>
        <input className="input" type="number" placeholder="20" value={maxTurns}
          onChange={e => updateMsgInput('max_turns', e.target.value)} />
        <span className="field-desc">Trim oldest messages when turn count exceeds this limit.</span>

        {renderLineageAnnotations(step)}
      </div>
    )
  }

  // ── B6: transform_messages editor ─────────────────────────────
  function renderTransformMessagesBody(step: FlowStep, i: number) {
    const histVar  = (step['key_identifier'] ?? '') as string
    const prevVars = slotsUpTo(i)
    let inputJson: Record<string, string> = {}
    try { inputJson = JSON.parse((step['input'] ?? '{}') as string) } catch { /* ignore */ }

    function updateOpt(key: string, value: string) {
      const next = { ...inputJson }
      if (value) next[key] = value; else delete next[key]
      updateStep(i, 'input', JSON.stringify(next))
    }

    return (
      <div className="step-body">
        <label className="field-label">History variable</label>
        {prevVars.length > 0 ? (
          <select className="input" value={histVar} onChange={e => updateStep(i, 'key_identifier', e.target.value)}>
            <option value="">— pick a variable —</option>
            {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        ) : (
          <input className="input" placeholder="var.history" value={histVar}
            onChange={e => updateStep(i, 'key_identifier', e.target.value)} />
        )}

        <label className="field-label" style={{ marginTop: 6 }}>Mode</label>
        <select className="input" value={inputJson['mode'] ?? 'filter_role'}
          onChange={e => updateOpt('mode', e.target.value)}>
          <option value="filter_role">filter_role — keep only messages matching role</option>
          <option value="keep_last_n">keep_last_n — keep the N most recent turns</option>
          <option value="inject_system">inject_system — prepend/replace system message</option>
          <option value="map_role">map_role — rename a role across all messages</option>
        </select>

        {inputJson['mode'] === 'keep_last_n' && (
          <>
            <label className="field-label" style={{ marginTop: 6 }}>Keep last N turns</label>
            <input className="input" type="number" placeholder="10" value={inputJson['n'] ?? ''}
              onChange={e => updateOpt('n', e.target.value)} />
          </>
        )}
        {(inputJson['mode'] === 'filter_role' || inputJson['mode'] === 'map_role') && (
          <>
            <label className="field-label" style={{ marginTop: 6 }}>Role</label>
            <select className="input" value={inputJson['role'] ?? 'user'}
              onChange={e => updateOpt('role', e.target.value)}>
              <option value="user">user</option>
              <option value="assistant">assistant</option>
              <option value="system">system</option>
            </select>
          </>
        )}
        {inputJson['mode'] === 'map_role' && (
          <>
            <label className="field-label" style={{ marginTop: 6 }}>Rename to</label>
            <select className="input" value={inputJson['to'] ?? 'user'}
              onChange={e => updateOpt('to', e.target.value)}>
              <option value="user">user</option>
              <option value="assistant">assistant</option>
              <option value="system">system</option>
            </select>
          </>
        )}
        {inputJson['mode'] === 'inject_system' && (
          <>
            <label className="field-label" style={{ marginTop: 6 }}>System message variable</label>
            {prevVars.length > 0 ? (
              <select className="input" value={inputJson['content_var'] ?? ''}
                onChange={e => updateOpt('content_var', e.target.value)}>
                <option value="">— pick a variable —</option>
                {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            ) : (
              <input className="input" placeholder="var.system_prompt" value={inputJson['content_var'] ?? ''}
                onChange={e => updateOpt('content_var', e.target.value)} />
            )}
          </>
        )}

        {renderLineageAnnotations(step)}
      </div>
    )
  }

  // ── Preview JSON (compiled — source refs auto-expanded) ─────────
  const compiledSteps = expandSteps(steps)
  const hasExpansions = compiledSteps.length !== steps.length
  const previewJson = JSON.stringify(
    { name: flowName || 'new_flow', instructions: compiledSteps, action: 'upsert' },
    null, 2,
  )

  const canSave = flowName.trim().length > 0 && steps.length > 0

  // ── Render ──────────────────────────────────────────────────────
  return (
    <div className="two-col">
      {/* ── Palette ─────────────────────────────────────────────── */}
      <div className="panel">
        <div className="panel-header">Instruction Palette</div>
        <div className="panel-body">
          {/* Mode toggle */}
          <div style={{ display: 'flex', gap: 4, marginBottom: 8 }}>
            <button
              style={{
                flex: 1,
                padding: '6px 0',
                borderRadius: 8,
                border: 'none',
                cursor: 'pointer',
                fontSize: 12,
                fontWeight: 700,
                background: mode === 'visual' ? 'var(--accent)' : '#27406b',
                color: mode === 'visual' ? '#031427' : 'var(--muted)',
                transition: 'background 0.15s, color 0.15s',
              }}
              onClick={() => setMode('visual')}
            >
              Visual {mode === 'visual' ? '●' : '○'}
            </button>
            <button
              style={{
                flex: 1,
                padding: '6px 0',
                borderRadius: 8,
                border: 'none',
                cursor: 'pointer',
                fontSize: 12,
                fontWeight: 700,
                background: mode === 'expert' ? 'var(--accent)' : '#27406b',
                color: mode === 'expert' ? '#031427' : 'var(--muted)',
                transition: 'background 0.15s, color 0.15s',
              }}
              onClick={() => setMode('expert')}
            >
              Expert {mode === 'expert' ? '●' : '○'}
            </button>
          </div>

          {mode === 'expert' ? (
            /* ── Expert mode: original searchable block list ── */
            <>
              <input
                className="input"
                placeholder="filter by name or category"
                value={filter}
                onChange={e => setFilter(e.target.value)}
              />
              <div className="block-list">
                {filteredSaved.length > 0 && (
                  <>
                    <div className="palette-section-label">My Flows</div>
                    {filteredSaved.map(b => (
                      <div
                        key={`saved-${b.title}`}
                        className="block block-saved"
                        draggable
                        onDragStart={e => e.dataTransfer.setData('application/json', JSON.stringify(b))}
                      >
                        <strong>{b.title}</strong>
                        <div className="sub">call · {b.description}</div>
                      </div>
                    ))}
                  </>
                )}
                {filteredBlocks.length === 0 && filteredSaved.length === 0 && (
                  <span className="hint">No blocks match.</span>
                )}
                {/* Group Instructions by category */}
                {(() => {
                  const CAT_LABELS: Record<string,string> = {
                    auth:      '🔒 Auth',
                    routing:   '🔀 Routing',
                    http:      '🌐 HTTP',
                    llm:       '🧠 AI / LLM',
                    transform: '⚙ Transform',
                    cache:     '💾 Cache',
                    registry:  '📋 Registry',
                    mcp:       '🔧 MCP',
                    vector:    '🔍 Vector',
                    data:      '📊 Data',
                    cost:      '💰 Cost',
                    history:   '📜 History',
                    ingest:    '📥 Ingest',
                  }
                  const grouped = filteredBlocks.reduce<Record<string, typeof filteredBlocks>>((acc, b) => {
                    const key = CAT_LABELS[b.category] ?? b.category ?? 'Other'
                    ;(acc[key] ??= []).push(b)
                    return acc
                  }, {})
                  return Object.entries(grouped).map(([catLabel, catBlocks]) => (
                    <div key={catLabel}>
                      <div className="palette-section-label" style={{ marginTop: 8 }}>{catLabel}</div>
                      {catBlocks.map(b => (
                        <div key={b.type} className="block" draggable
                          onDragStart={e => e.dataTransfer.setData('application/json', JSON.stringify(b))}>
                          <strong>{b.title}</strong>
                          <div className="sub" style={{ marginTop: 2, opacity: 0.75 }}>{b.description}</div>
                        </div>
                      ))}
                    </div>
                  ))
                })()}
              </div>
            </>
          ) : (
            /* ── Visual mode: grouped recipe blocks ── */
            <div className="block-list">
              {VISUAL_GROUPS.map(group => (
                <div key={group.label}>
                  <div
                    className="palette-section-label"
                    style={{ display: 'flex', alignItems: 'center', gap: 5 }}
                  >
                    <span>{group.icon}</span>
                    <span>{group.label}</span>
                  </div>
                  {group.recipes.map(recipe => {
                    const payload = recipeToBlock(recipe)
                    return (
                      <div
                        key={`${group.label}-${recipe.wraps}-${recipe.title}`}
                        className="block"
                        draggable
                        onDragStart={e =>
                          e.dataTransfer.setData('application/json', JSON.stringify(payload))
                        }
                      >
                        <strong>{recipe.title}</strong>
                        <div className="sub" style={{ marginTop: 2, opacity: 0.75 }}>
                          {recipe.description}
                        </div>
                      </div>
                    )
                  })}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* ── Canvas ──────────────────────────────────────────────── */}
      <div className="panel">
        <div className="panel-header">Flow Canvas</div>
        <div className="panel-body">
          <input
            className="input"
            placeholder="flow name (required to save)"
            value={flowName}
            onChange={e => setFlowName(e.target.value)}
          />
          <div
            className={`canvas${dragOver ? ' drag-over' : ''}`}
            onDragOver={e => { e.preventDefault(); setDragOver(true) }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
          >
            {steps.length === 0 && (
              <span className="hint">Drop instruction blocks here to build a flow</span>
            )}
            {steps.map((step, i) => {
              const defs = fieldMap[step.action] ?? {}
              const isExpanded = expanded.has(i)
              return (
                <div key={i} className="step">
                  <div className="step-header" onClick={() => toggleExpand(i)}>
                    <span className="step-toggle">{isExpanded ? '▼' : '▶'}</span>
                    <strong className="step-title">{i + 1}. {step.action}</strong>
                    <button
                      className="btn muted step-remove"
                      title="Remove step"
                      onClick={e => { e.stopPropagation(); removeStep(i) }}
                    >×</button>
                  </div>
                  {isExpanded && (
                    step.action === 'if'                ? renderIfBody(step, i, defs)           :
                    step.action === 'switch'            ? renderSwitchBody(step, i, defs)       :
                    step.action === 'http_call'         ? renderHttpCallBody(step, i)            :
                    step.action === 'token_validation'  ? renderTokenValidationBody(step, i)     :
                    step.action === 'set_response_body' ? renderSetResponseBody(step, i)         :
                    step.action === 'append_message'    ? renderAppendMessageBody(step, i)       :
                    step.action === 'transform_messages'? renderTransformMessagesBody(step, i)   :
                                                          renderGenericBody(step, i, defs)
                  )}
                </div>
              )
            })}
          </div>

          {/* Actions */}
          <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
            <button
              className={`btn${canSave ? '' : ' muted'}`}
              style={{ flex: 1 }}
              disabled={!canSave}
              title={!flowName.trim() ? 'Enter a flow name first' : steps.length === 0 ? 'Add at least one step' : undefined}
              onClick={handleSave}
            >
              {justSaved ? '✓ Saved to palette' : 'Save to My Flows'}
            </button>
            <button
              className="btn muted"
              style={{ flex: 1 }}
              onClick={() => { setSteps([]); setExpanded(new Set()) }}
            >Clear Flow</button>
          </div>

          {/* Compiled JSON preview */}
          {hasExpansions && (
            <div className="expansion-note">
              Preview shows compiled flow — source references auto-extracted
            </div>
          )}
          <textarea className="input mt8" readOnly value={previewJson} />
        </div>
      </div>
    </div>
  )
}
