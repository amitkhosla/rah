import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import type { FieldDef, FlowImpact, FlowStep, PaletteBlock, PatternCondition, SavedFlow, TemplatePatternStep } from '../types'
import type { LLMModel } from '../types'
import FlowMap   from './FlowMap'
import FlowGraph from './FlowGraph'
import { expandSteps, findSourceRefs, smartCondition } from '../utils/expressions'
import { parseDSL, serializeDSL, normalizeCases } from '../utils/dsl'
import PatternConditionBuilder from './PatternConditionBuilder'
import TemplatePatternBuilder from './TemplatePatternBuilder'
import ValidateRouteBuilder from './ValidateRouteBuilder'
import { isPatternCondition } from '../types'
import { listLLMModels } from '../api'

interface Props {
  blocks: PaletteBlock[]
  steps: FlowStep[]
  setSteps: (steps: FlowStep[]) => void
  flowName: string
  setFlowName: (name: string) => void
  savedFlows: SavedFlow[]
  onSaveFlow: () => void
  onNavigateToFlow?: (name: string) => void
  /** Ordered list of flow names we navigated from (breadcrumb trail, excluding current). */
  navStack?: string[]
  /** Jump back to navStack[idx], popping everything above it. */
  onNavigateBack?: (idx: number) => void
  /** Reverse dependency map: which flows and APIs reference each named flow. */
  impactMap?: Map<string, FlowImpact>
  /** Navigate to the APIs tab (used from the impact warning banner). */
  onNavigateToApis?: () => void
  /** Open a named flow fresh for editing (clears navStack, loads into canvas). */
  onOpenFlow?: (name: string) => void
  /** Permanently delete a saved flow by name. */
  onDeleteFlow?: (name: string) => void
}

// ── Branch path type for n-level nesting ─────────────────────────
/**
 * Addresses a nested step inside the top-level steps array.
 * Example: [{ branch: 'then_steps', idx: 0 }, { branch: 'else_steps', idx: 2 }]
 * means steps[topIdx].then_steps[0].else_steps[2]
 */
export type BranchPath = Array<{ branch: 'then_steps' | 'else_steps'; idx: number }>

// ── Visual Mode recipe definitions ───────────────────────────────
interface VisualRecipe {
  title: string
  wraps: string        // action type this maps to
  description: string
  defaults?: Record<string, string>
  fields?: import('../types').FieldDef[]
}

interface VisualGroup {
  label: string
  icon: string
  recipes: VisualRecipe[]
}

const VISUAL_GROUPS: VisualGroup[] = [
  {
    label: 'LLM',
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
      { title: 'CORS',              wraps: 'cors',               description: 'Handle cross-origin requests and preflight (OPTIONS) with zero allocations. Auto-replies 204 to preflight; sets Access-Control headers on real requests.' },
      { title: 'Validate Token',   wraps: 'token_validation',   description: 'Validate JWT or API key' },
      { title: 'API Rate Limits',  wraps: 'api_rate_limits',    description: 'Enforce rate limits configured in the API definition. Drag to control where in the flow enforcement happens. If absent, limits are auto-injected at the start of the flow.' },
      { title: 'Check Rate Limit', wraps: 'check_rate_limit',  description: 'Enforce request rate limits' },
      { title: 'Rate Limit V2',    wraps: 'check_rate_limit_v2',   description: 'Multi-window V2 rate limiting with per-tenant overrides. Configure a named rate-limit config, count_by mode, and optional enforcement (approximate/strict/token_bucket).' },
      { title: 'Rate Limit Tier',  wraps: 'check_rate_limit_tier',  description: 'Tier-based rate limiting: reads the tenant\'s tier from the registry and routes to the matching V2 rate-limit config automatically.' },
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
    label: 'TRANSFORM',
    icon: '🔄',
    recipes: [
      { title: 'Base64 Encode',  wraps: 'base64_encode', description: 'Encode bytes to base64 (std, url, raw_url, raw_std variants)' },
      { title: 'Base64 Decode',  wraps: 'base64_decode', description: 'Decode a base64 string to raw bytes (default: raw_url for JWT)' },
      { title: 'Hex Encode',     wraps: 'hex_encode',    description: 'Encode bytes as a lowercase hex string' },
      { title: 'Hex Decode',     wraps: 'hex_decode',    description: 'Decode a hex string back to raw bytes' },
      { title: 'URL Encode',     wraps: 'url_encode',    description: 'Percent-encode a string (RFC 3986, space → %20)' },
      { title: 'URL Decode',     wraps: 'url_decode',    description: 'Decode a percent-encoded string (+ → space)' },
      { title: 'SHA-256 Hash',   wraps: 'sha256_hash',   description: 'Compute SHA-256 and output lowercase hex (no key)' },
      { title: 'HMAC-SHA256',    wraps: 'hmac_sha256',   description: 'Sign data with a secret key using HMAC-SHA256 (webhooks, request signing)' },
      { title: 'HMAC-SHA1',      wraps: 'hmac_sha1',     description: 'HMAC-SHA1 signature — legacy integrations only' },
      { title: 'MD5 Hash',       wraps: 'md5_hash',      description: 'MD5 checksum — insecure, use for legacy/checksums only' },
      { title: 'AES Encrypt',    wraps: 'aes_encrypt',   description: 'Encrypt with AES-GCM (nonce||ciphertext output)' },
      { title: 'AES Decrypt',    wraps: 'aes_decrypt',   description: 'Decrypt AES-GCM ciphertext, sets Failed on auth error' },
    ],
  },
  {
    label: 'REQUEST',
    icon: '📥',
    recipes: [
      { title: 'Read Header',      wraps: 'bind_header', description: 'Extract an HTTP request header into a slot',
        defaults: { key: 'X-Tenant-ID', as: 'tenant_id' },
        fields: [
          { key: 'key', label: 'Header name', description: 'Name of the HTTP request header to read', placeholder: 'X-Tenant-ID' },
          { key: 'as',  label: 'Store as',    description: 'Slot name to save the header value into',  placeholder: 'tenant_id' },
        ] },
      { title: 'Read Query Param', wraps: 'bind_query_param', description: 'Extract a URL query parameter into a slot',
        defaults: { key: 'param_name', as: 'param_value' },
        fields: [
          { key: 'key', label: 'Param name', description: 'Name of the URL query parameter to read', placeholder: 'param_name' },
          { key: 'as',  label: 'Store as',   description: 'Slot name to save the parameter value into', placeholder: 'param_value' },
        ] },
      { title: 'Read Path Param',  wraps: 'bind_path', description: 'Extract a path parameter like {id} into a slot',
        defaults: { key: 'id', as: 'path_id' },
        fields: [
          { key: 'key', label: 'Path param', description: 'Name of the path parameter as declared in the route (e.g. id for /users/{id})', placeholder: 'id' },
          { key: 'as',  label: 'Store as',   description: 'Slot name to save the path parameter value into', placeholder: 'path_id' },
        ] },
      { title: 'Client IP',           wraps: 'bind_client_ip',     description: 'Extract the real client IP address' },
      { title: 'Set Upstream Header', wraps: 'set_request_header', description: 'Inject a header into the upstream request' },
    ],
  },
  {
    label: 'CACHE',
    icon: '🗄️',
    recipes: [
      { title: 'Cache Read · Per-Tenant',  wraps: 'cache_get',         description: 'Read a cached value — private to this tenant' },
      { title: 'Cache Write · Per-Tenant', wraps: 'cache_put',         description: 'Store a value in this tenant\'s private cache with TTL' },
      { title: 'Cache Read · Shared',        wraps: 'cache_get_global',    description: 'Read from the shared cache (same data for all tenants)' },
      { title: 'Cache Write · Shared',       wraps: 'cache_put_global',    description: 'Write to the shared cache (visible to all tenants)' },
      { title: 'Cache Invalidate · Per-Tenant', wraps: 'cache_delete',     description: 'Remove a specific key from this tenant\'s private cache' },
      { title: 'Cache Invalidate · Shared',     wraps: 'cache_delete_global', description: 'Remove a specific key from the shared cache' },
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
    label: 'STRING',
    icon: '✂️',
    recipes: [
      { title: 'Concat',          wraps: 'concat',          description: 'Join two variables into one string (left + separator + right)' },
      { title: 'Set Literal',     wraps: 'set_const',       description: 'Write a static string value into a variable' },
      { title: 'Template',        wraps: 'render_template', description: 'Build a string by interpolating ${varname} placeholders' },
      { title: 'To Lower',        wraps: 'to_lower',        description: 'Convert a string variable to lower-case' },
      { title: 'To Upper',        wraps: 'to_upper',        description: 'Convert a string variable to upper-case' },
      { title: 'Trim',            wraps: 'trim',            description: 'Remove leading and trailing whitespace' },
      { title: 'Replace',         wraps: 'replace',         description: 'Replace all occurrences of a substring' },
      { title: 'Contains',        wraps: 'contains',        description: 'Check if a string contains a fixed substring (→ bool)' },
      { title: 'Starts With',     wraps: 'starts_with',     description: 'Check if a string starts with a fixed prefix (→ bool)' },
      { title: 'Ends With',       wraps: 'ends_with',       description: 'Check if a string ends with a fixed suffix (→ bool)' },
      { title: 'Split',           wraps: 'split',           description: 'Split a string by separator into a JSON array' },
      { title: 'Substring',       wraps: 'substring',       description: 'Slice a string by byte offset' },
      { title: 'String Length',   wraps: 'byte_length',     description: 'Write the byte length of a variable into an integer slot' },
      { title: 'To Int',          wraps: 'to_int',          description: 'Parse a string variable as a 64-bit integer' },
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
  blocks, steps, setSteps, flowName, setFlowName, savedFlows, onSaveFlow, onNavigateToFlow,
  navStack = [], onNavigateBack, impactMap, onNavigateToApis, onOpenFlow, onDeleteFlow,
}: Props) {
  const [filter, setFilter]             = useState('')
  const [dragOver, setDragOver]         = useState(false)
  const [expanded, setExpanded]         = useState<Set<number>>(new Set())
  const [justSaved, setJustSaved]       = useState(false)
  const [collapsedSections, setCollapsedSections] = useState<Set<string>>(new Set())
  const [nestedExpanded, setNestedExpanded] = useState<Set<string>>(new Set())
  const [dragOverBranch, setDragOverBranch] = useState<string | null>(null)
  const [dropTargetIdx, setDropTargetIdx]   = useState<number | null>(null)
  const [insertCursor, setInsertCursor]     = useState<number | null>(null)
  const [viewMode,    setViewMode]      = useState<'visual' | 'code'>('visual')
  const [dslText,     setDslText]       = useState('')
  const [dslError,    setDslError]      = useState<string | null>(null)
  const [showDslRef,  setShowDslRef]    = useState(false)
  // Variable picker popup: which step+field is currently showing the popup
  const [varPopup, setVarPopup] = useState<{ stepIdx: number; field: string; anchor: DOMRect } | null>(null)
  // Variable validation warnings: key = "stepIdx:fieldKey", value = warning message
  const [validationWarnings, setValidationWarnings] = useState<Map<string, string>>(new Map())
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null)
  const [showThisFlow,   setShowThisFlow]   = useState(false)
  const [thisFlowTab,    setThisFlowTab]    = useState<'steps' | 'tree' | 'graph'>('steps')
  const [expandedCalls,  setExpandedCalls]  = useState<Set<string>>(new Set())
  const [llmModels,      setLlmModels]      = useState<LLMModel[]>([])

  useEffect(() => {
    listLLMModels().then(setLlmModels).catch(() => { /* unavailable */ })
  }, [])

  // ── Pattern condition builder state ───────────────────────────────────────
  // patternBuilderTarget: which step index is currently being edited (null = closed)
  const [patternBuilderTarget, setPatternBuilderTarget] = useState<number | null>(null)
  // Staged condition: the condition being edited before Save is pressed
  const [patternBuilderCondition, setPatternBuilderCondition] = useState<PatternCondition | null>(null)

  // Track which side last triggered a change to break the sync loop
  const dslChangeSource = useRef<'visual' | 'code'>('visual')

  // Visual → Code: whenever steps change from visual edits, keep DSL text fresh
  useEffect(() => {
    if (dslChangeSource.current === 'code') {
      // Steps were just updated by a code edit — don't re-serialize back
      dslChangeSource.current = 'visual'
      return
    }
    setDslText(serializeDSL(steps))
  }, [steps])
  const [selectMode, setSelectMode]     = useState(false)
  const [selectedSteps, setSelectedSteps] = useState<Set<number>>(new Set())
  const [extractName, setExtractName]   = useState('')
  // Pending (not-yet-saved) new claim rows, keyed by step index or nested key string
  type DraftClaim = { key: string; mode: 'static' | 'var'; value: string }
  const [pendingClaims, setPendingClaims] = useState<Record<string | number, DraftClaim>>({})
  function clearPendingClaim(stepIdx: string | number) {
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
    return {
      type: recipe.wraps,
      title: recipe.title,
      description: recipe.description,
      category: 'visual',
      capability: '',
      supports_nested: false,
      defaults: recipe.defaults ?? {},
      fields: recipe.fields ?? [],
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

  // For "This Flow" panel: include the current canvas state even if not yet explicitly saved
  // so Tree/Graph tabs always show the flow being edited.
  const thisFlowEffective = useMemo(() => {
    if (!flowName || steps.length === 0) return savedFlows
    const exists = savedFlows.some(f => f.name === flowName)
    if (exists) return savedFlows
    return [...savedFlows, { name: flowName, steps }]
  }, [savedFlows, flowName, steps])

  // Filtered lists
  const q = filter.toLowerCase()
  const filteredSaved = savedFlowBlocks.filter(b =>
    !filter || b.title.toLowerCase().includes(q) || 'my-flows'.includes(q),
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

  function insertStepAt(block: PaletteBlock, atIdx: number) {
    const newStep = { action: block.type, ...block.defaults }
    const newSteps = [...steps]
    newSteps.splice(atIdx, 0, newStep)
    setSteps(newSteps)
    setExpanded(prev => {
      const next = new Set<number>()
      prev.forEach(i => next.add(i >= atIdx ? i + 1 : i))
      next.add(atIdx)
      return next
    })
  }

  function clickAddBlock(block: PaletteBlock) {
    const newStep = { action: block.type, ...block.defaults }
    if (insertCursor !== null) {
      const cur = insertCursor
      const newSteps = [...steps]
      newSteps.splice(cur, 0, newStep)
      setSteps(newSteps)
      setExpanded(prev => {
        const next = new Set<number>()
        prev.forEach(i => next.add(i >= cur ? i + 1 : i))
        next.add(cur)
        return next
      })
      setInsertCursor(null)
    } else {
      setSteps([...steps, newStep])
      setExpanded(prev => new Set([...prev, steps.length]))
    }
  }

  function InterStepDropZone({ idx }: { idx: number }) {
    const isOver = dropTargetIdx === idx
    return (
      <div
        onDragOver={e => { e.preventDefault(); setDropTargetIdx(idx) }}
        onDragLeave={() => setDropTargetIdx(null)}
        onDrop={e => {
          e.preventDefault()
          e.stopPropagation()
          setDropTargetIdx(null)
          const raw = e.dataTransfer.getData('application/json')
          if (!raw) return
          insertStepAt(JSON.parse(raw) as PaletteBlock, idx)
        }}
        style={{
          height: isOver ? 28 : 6,
          margin: '0 4px',
          borderRadius: 4,
          background: isOver ? 'rgba(87,181,255,0.15)' : 'transparent',
          border: isOver ? '1px dashed rgba(87,181,255,0.5)' : '1px dashed transparent',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          transition: 'height 0.1s, background 0.1s',
          cursor: 'copy',
        }}
      >
        {isOver && (
          <span style={{ fontSize: 11, color: 'var(--accent)', fontWeight: 600 }}>
            + Insert here
          </span>
        )}
      </div>
    )
  }

  // ── This Flow — Steps tab ──────────────────────────────────────────
  const SICONS: Record<string, string> = {
    'if':'🔀','switch':'🔀','call':'📞','return':'↩','fail':'✗',
    'token_validation':'🔒','http_call':'🌐','llm_call':'🧠',
    'cache_get':'🗄️','cache_put':'🗄️','cache_get_global':'🗄️','cache_put_global':'🗄️','cache_delete':'🗄️','cache_delete_global':'🗄️',
    'bind_header':'📥','bind_query_param':'📥','bind_path':'📥','bind_body':'📥',
    'emit_event':'📊','log_field':'📋','registry_lookup':'🏷️',
    'load_service_url':'🔗','load_identifier':'🔑','check_rate_limit':'⏱','check_rate_limit_v2':'⏱','check_rate_limit_tier':'🏷️','api_rate_limits':'📍',
    'set_response_body':'📤','set_response_header':'📤','set_response_status':'📤',
    'extract':'✂️','json_extract_emit':'✂️','mcp_call_tool':'🔧',
    'concat':'✂️','set_const':'📝','render_template':'📝',
    'to_lower':'✂️','to_upper':'✂️','trim':'✂️','replace':'✂️',
    'contains':'🔍','starts_with':'🔍','ends_with':'🔍',
    'split':'✂️','substring':'✂️','byte_length':'🔢','to_int':'🔢',
    'vector_search':'🔍','embed_text':'🔢','store_internal_tx_id':'🔖',
    'bind_correlation_id':'🔖','bind_client_ip':'🌐',
  }
  function sicon(a: string) { return SICONS[a] ?? '•' }

  function FlowStepList({ stepList, depth, flowLabel }: {
    stepList: FlowStep[]; depth: number; flowLabel?: string
  }) {
    if (depth > 8) return <div style={{ fontSize: 10, color: 'var(--muted)', padding: '1px 6px' }}>…</div>
    if (stepList.length === 0) return (
      <div style={{ fontSize: 11, color: 'var(--muted)', padding: '2px 6px', fontStyle: 'italic' }}>(empty)</div>
    )
    return (
      <div style={{ borderLeft: depth > 0 ? '1px solid rgba(255,255,255,0.08)' : 'none', paddingLeft: depth > 0 ? 8 : 0 }}>
        {flowLabel && (
          <div style={{ fontSize: 10, color: 'var(--accent)', fontWeight: 700, padding: '2px 0', letterSpacing: '0.05em', textTransform: 'uppercase' }}>
            {flowLabel}
          </div>
        )}
        {stepList.map((s, si) => {
          const ck      = `${depth}:${si}:${String(s['flow_name'] ?? '')}`
          const isCall  = s.action === 'call'
          const isIf    = s.action === 'if' || s.action === 'switch'
          const outVar  = s['as'] as string | undefined
          const target  = s['flow_name'] as string | undefined
          const sub     = isCall && target ? savedFlows.find(f => f.name === target) : undefined
          const open    = expandedCalls.has(ck)
          return (
            <div key={si}>
              <div
                style={{ display:'flex', alignItems:'center', gap:5, padding:'2px 5px', borderRadius:3, marginBottom:1, background:'rgba(255,255,255,0.015)', cursor: isCall && sub ? 'pointer' : 'default' }}
                onClick={() => {
                  if (!isCall || !sub) return
                  setExpandedCalls(prev => { const s2=new Set(prev); s2.has(ck)?s2.delete(ck):s2.add(ck); return s2 })
                }}
              >
                <span style={{ fontSize:12, width:16, textAlign:'center', flexShrink:0 }}>{sicon(s.action)}</span>
                <span style={{ fontSize:11, color:'var(--fg)', flex:1 }}>{s.action}</span>
                {outVar && <span style={{ fontSize:10, color:'#34d399', fontFamily:'monospace' }}>→ {outVar}</span>}
                {isIf && <span style={{ fontSize:10, color:'#f59e0b' }}>{String(s['then']??'?')}/{String(s['else']??'?')}</span>}
                {isCall && target && sub  && <span style={{ fontSize:10, color:'var(--accent)' }}>{open?'▲':'▶'} {target}</span>}
                {isCall && target && !sub && <span style={{ fontSize:10, color:'#f59e0b' }}>⚠ {target}</span>}
              </div>
              {isCall && open && sub && (
                <div style={{ marginLeft:8, marginTop:1, marginBottom:3 }}>
                  <FlowStepList stepList={sub.steps} depth={depth+1} flowLabel={target} />
                </div>
              )}
            </div>
          )
        })}
      </div>
    )
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
    // Validate variable references
    if (['source','input','as','key_identifier','condition','then','else','flow_name','url','url_var'].includes(key)) {
      const mapKey = `${i}:${key}`
      if (!isVarDefined(value, i)) {
        setValidationWarnings(prev => new Map(prev).set(mapKey, `Variable "${value}" is not defined before step ${i + 1}`))
      } else {
        setValidationWarnings(prev => { const m = new Map(prev); m.delete(mapKey); return m })
      }
    }
  }

  function removeStep(i: number) {
    setSteps(steps.filter((_, idx) => idx !== i))
    setExpanded(prev => {
      const next = new Set<number>()
      prev.forEach(idx => { if (idx < i) next.add(idx); else if (idx > i) next.add(idx - 1) })
      return next
    })
  }

  // ── Pattern condition builder helpers ────────────────────────────────────
  /** Open the pattern builder for a specific `if` step index. */
  function openPatternBuilder(stepIdx: number) {
    const step = steps[stepIdx]
    const existingCond = step?.condition
    const initial: PatternCondition | null =
      existingCond && typeof existingCond === 'object' && isPatternCondition(existingCond)
        ? (existingCond as PatternCondition)
        : null
    setPatternBuilderCondition(initial)
    setPatternBuilderTarget(stepIdx)
  }

  /** Called when user clicks Save in the builder. */
  function applyPatternCondition(cond: PatternCondition) {
    if (patternBuilderTarget === null) return
    setSteps(steps.map((s, i) =>
      i === patternBuilderTarget ? { ...s, condition: cond as unknown as string } : s
    ))
  }

  /** Remove a pattern condition from an `if` step (revert to text condition). */
  function clearPatternCondition(stepIdx: number) {
    setSteps(steps.map((s, i) =>
      i === stepIdx ? { ...s, condition: '' } : s
    ))
  }

  // ── N-level tree updater ──────────────────────────────────────
  /**
   * Returns a new copy of `node` with the step at `path` replaced by `updater(step)`.
   * `path` is a BranchPath relative to `node`.
   * If path is empty, returns updater(node) directly.
   */
  function setNestedStep(
    node: FlowStep,
    path: BranchPath,
    updater: (s: FlowStep) => FlowStep | null,  // null = delete
  ): FlowStep {
    if (path.length === 0) return updater(node) ?? node
    const [head, ...rest] = path
    const arr = [...((node[head.branch] as FlowStep[]) ?? [])]
    if (rest.length === 0) {
      // At the target level
      const result = updater(arr[head.idx])
      if (result === null) {
        arr.splice(head.idx, 1)
      } else {
        arr[head.idx] = result
      }
    } else {
      arr[head.idx] = setNestedStep(arr[head.idx], rest, updater)
    }
    return { ...node, [head.branch]: arr }
  }

  /** Update a field on a step at arbitrary depth. topIdx = index in top-level steps[]. */
  function updateNestedStep(topIdx: number, path: BranchPath, key: string, value: unknown) {
    setSteps(steps.map((s, i) =>
      i !== topIdx ? s : setNestedStep(s, path, step => ({ ...step, [key]: value }))
    ))
  }

  /** Remove a step at arbitrary depth. */
  function removeNestedStep(topIdx: number, path: BranchPath) {
    setSteps(steps.map((s, i) =>
      i !== topIdx ? s : setNestedStep(s, path, () => null)
    ))
  }

  /** Append a new step to a branch at arbitrary depth. */
  function addToNestedBranch(topIdx: number, path: BranchPath, branch: 'then_steps' | 'else_steps', b: PaletteBlock) {
    setSteps(steps.map((s, i) => {
      if (i !== topIdx) return s
      // Navigate to the parent node, then append
      const navigate = (node: FlowStep, remaining: BranchPath): FlowStep => {
        if (remaining.length === 0) {
          const arr = [...((node[branch] as FlowStep[]) ?? []), { action: b.type, ...b.defaults }]
          return { ...node, [branch]: arr }
        }
        const [head, ...rest] = remaining
        const arr = [...((node[head.branch] as FlowStep[]) ?? [])]
        arr[head.idx] = navigate(arr[head.idx], rest)
        return { ...node, [head.branch]: arr }
      }
      return navigate(s, path)
    }))
  }

  function switchToCode() {
    setDslText(serializeDSL(steps))
    setDslError(null)
    setViewMode('code')
  }

  function applyDSL() {
    // Steps are already live-synced from the textarea — just navigate to visual
    setDslError(null)
    setExpanded(new Set())
    setNestedExpanded(new Set())
    setViewMode('visual')
  }

  function handleSave() {
    if (!flowName.trim() || steps.length === 0) return
    onSaveFlow()
    setJustSaved(true)
    setTimeout(() => setJustSaved(false), 1800)
  }

  function extractSubFlow() {
    if (!extractName.trim() || selectedSteps.size === 0) return
    const sorted = [...selectedSteps].sort((a, b) => a - b)
    const subSteps = sorted.map(i => steps[i])
    // Save the sub-flow
    onSaveFlow  // We can't call onSaveFlow directly with different steps; use the prop
    // We need the parent (App) to save, but we only have onSaveFlow which saves current steps.
    // Instead, we fire the navigate callback with a sentinel to create a new named flow.
    // For now: persist to localStorage directly under savedFlows key, then reload.
    // This is the simplest approach that does not require a new prop.
    const raw = localStorage.getItem('rah_studio_v1')
    const snap = raw ? JSON.parse(raw) as { savedFlows?: Array<{ name: string; steps: FlowStep[] }> } : {}
    const existingFlows: Array<{ name: string; steps: FlowStep[] }> = snap.savedFlows ?? []
    const already = existingFlows.find(f => f.name === extractName.trim())
    if (!already) {
      existingFlows.push({ name: extractName.trim(), steps: subSteps })
      localStorage.setItem('rah_studio_v1', JSON.stringify({ ...snap, savedFlows: existingFlows }))
    }
    // Replace selected steps with a single call step
    const firstIdx = sorted[0]
    const newSteps = steps.filter((_, i) => !selectedSteps.has(i))
    const callStep: FlowStep = { action: 'call', flow_name: extractName.trim() }
    newSteps.splice(firstIdx, 0, callStep)
    setSteps(newSteps)
    setSelectMode(false)
    setSelectedSteps(new Set())
    setExtractName('')
    // Reload savedFlows from localStorage (App will re-read on next render cycle via its own useEffect)
    window.dispatchEvent(new StorageEvent('storage', { key: 'rah_studio_v1' }))
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
        <div style={{ display: 'flex', alignItems: 'center' }}>
          <input
            className="input"
            style={{ flex: 1 }}
            value={value ?? ''}
            placeholder={def?.placeholder ?? key}
            onChange={e => updateStep(stepIdx, key, e.target.value)}
            onBlur={opts?.smart ? e => {
              const normalised = smartCondition(e.target.value)
              if (normalised !== e.target.value) updateStep(stepIdx, key, normalised)
              opts.onBlur?.(normalised)
            } : undefined}
          />
          <button
            tabIndex={-1}
            title="Browse available variables"
            onClick={e => {
              const rect = e.currentTarget.getBoundingClientRect()
              setVarPopup({ stepIdx, field: key, anchor: rect })
            }}
            style={{
              marginLeft: 4,
              padding: '2px 5px',
              background: 'rgba(87,181,255,0.1)',
              border: '1px solid rgba(87,181,255,0.25)',
              borderRadius: 4,
              color: 'var(--accent)',
              fontSize: 11,
              cursor: 'pointer',
              flexShrink: 0,
            }}
          >$</button>
        </div>
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
        {/* Validation warning */}
        {(() => {
          const warn = validationWarnings.get(`${stepIdx}:${key}`)
          if (!warn) return null
          return (
            <div style={{ fontSize: 11, color: '#f59e0b', marginTop: 2, display: 'flex', alignItems: 'center', gap: 4 }}>
              <span>⚠</span><span>{warn}</span>
            </div>
          )
        })()}
        {def?.description && <span className="field-desc">{def.description}</span>}
      </div>
    )
  }

  // ── Nested step card (inside if/else branches, any depth) ───────────
  /**
   * topIdx    = index in the top-level steps[] array
   * path      = BranchPath from the top-level step down to (but not including) this step
   *             e.g. [{ branch: 'then_steps', idx: 0 }] means this step is inside steps[topIdx].then_steps[0]
   * ni        = index of this step within its immediate parent branch
   * branch    = which branch of the immediate parent ('then_steps' | 'else_steps')
   */
  function renderNestedStepCard(
    step: FlowStep,
    ni: number,
    topIdx: number,
    branch: 'then_steps' | 'else_steps',
    path: BranchPath,
  ) {
    const key = `${topIdx}-${path.map(p => `${p.branch}[${p.idx}]`).join('.')}-${branch}-${ni}`
    const isExp = nestedExpanded.has(key)
    const defs = fieldMap[step.action as string] ?? {}
    // Path to THIS step (used for update/remove)
    const stepPath: BranchPath = [...path, { branch, idx: ni }]
    const fields = Object.entries(step).filter(([k]) => k !== 'action' && k !== 'then_steps' && k !== 'else_steps')
    const isIf = step.action === 'if'

    return (
      <div key={key} style={{ margin: '4px 8px', borderRadius: 5, border: '1px solid rgba(255,255,255,0.07)', background: 'rgba(255,255,255,0.02)' }}>
        <div
          style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 8px', cursor: 'pointer', userSelect: 'none' }}
          onClick={() => setNestedExpanded(prev => {
            const s = new Set(prev)
            s.has(key) ? s.delete(key) : s.add(key)
            return s
          })}
        >
          <span style={{ fontSize: 10 }}>{isExp ? '▼' : '▶'}</span>
          <strong style={{ fontSize: 12, flex: 1 }}>{ni + 1}. {step.action as string}</strong>
          <button
            className="btn muted step-remove"
            style={{ fontSize: 11 }}
            onClick={e => { e.stopPropagation(); removeNestedStep(topIdx, stepPath) }}
          >×</button>
        </div>
        {isExp && (
          <div style={{ padding: '0 8px 8px' }}>
            {step.action === 'token_validation'
              ? renderTokenValidationBody(
                  step,
                  (k, v) => updateNestedStep(topIdx, stepPath, k, v),
                  slotsUpTo(topIdx),
                  key,
                )
              : (<>
                  {fields.map(([k, v]) => (
                    <div key={k} className="field-row">
                      <label className="field-label">{defs[k]?.label || k}</label>
                      <input className="input"
                        placeholder={defs[k]?.placeholder || k}
                        value={String(v ?? '')}
                        onChange={e => updateNestedStep(topIdx, stepPath, k, e.target.value)} />
                    </div>
                  ))}
                  {fields.length === 0 && !isIf && (
                    <span style={{ fontSize: 11, color: 'var(--muted)' }}>No fields to configure.</span>
                  )}
                  {isIf && renderNestedBranches(step, topIdx, stepPath, defs)}
                </>)
            }
          </div>
        )}
      </div>
    )
  }

  /**
   * Renders the THEN/ELSE sub-branches for an `if` step that is itself nested.
   * stepPath = path to the `if` step itself (already includes its own branch+idx).
   */
  function renderNestedBranches(
    step: FlowStep,
    topIdx: number,
    stepPath: BranchPath,
    defs: Record<string, FieldDef>,
  ) {
    const thenSteps = (step.then_steps as FlowStep[]) ?? []
    const elseSteps = (step.else_steps as FlowStep[]) ?? []
    return (
      <>
        {thenSteps.length === 0 && (
          <div className="field-row">
            <label className="field-label">{defs['then']?.label ?? 'then'}</label>
            <input className="input" placeholder={defs['then']?.placeholder ?? 'then'}
              value={(step['then'] as string) ?? ''}
              onChange={e => updateNestedStep(topIdx, stepPath, 'then', e.target.value)} />
          </div>
        )}
        {renderBranch('✓ THEN', false, thenSteps, topIdx, 'then_steps', stepPath)}
        {elseSteps.length === 0 && (
          <div className="field-row">
            <label className="field-label">{defs['else']?.label ?? 'else'}</label>
            <input className="input" placeholder={defs['else']?.placeholder ?? 'else'}
              value={(step['else'] as string) ?? ''}
              onChange={e => updateNestedStep(topIdx, stepPath, 'else', e.target.value)} />
          </div>
        )}
        {renderBranch('✗ ELSE', true, elseSteps, topIdx, 'else_steps', stepPath)}
      </>
    )
  }

  // ── Inline branch drop zone (then/else, any depth) ───────────────────
  /**
   * topIdx   = index in the top-level steps[] array (never changes as we recurse)
   * branch   = 'then_steps' | 'else_steps' of the immediate parent
   * parentPath = BranchPath to the parent `if` step (empty [] for top-level if steps)
   */
  function renderBranch(
    label: string,
    isElse: boolean,
    branchSteps: FlowStep[],
    topIdx: number,
    branch: 'then_steps' | 'else_steps',
    parentPath: BranchPath = [],
  ) {
    const branchKey = `${topIdx}-${parentPath.map(p => `${p.branch}[${p.idx}]`).join('.')}-${branch}`
    const isDragOver = dragOverBranch === branchKey
    const borderColor = isElse ? '#ef4444' : '#22c55e'
    const labelColor  = isElse ? '#ef4444' : '#22c55e'

    return (
      <div style={{ marginTop: 8, borderRadius: 6, border: '1px solid rgba(255,255,255,0.08)', borderLeft: `3px solid ${borderColor}` }}>
        <div style={{ fontSize: 11, fontWeight: 700, padding: '4px 10px', color: labelColor, background: 'rgba(255,255,255,0.03)', letterSpacing: '0.06em' }}>
          {label}
        </div>
        {branchSteps.map((ns, ni) => renderNestedStepCard(ns, ni, topIdx, branch, parentPath))}
        <div
          style={{
            margin: '6px 8px',
            padding: '7px 10px',
            border: `1.5px dashed ${isDragOver ? borderColor : 'rgba(255,255,255,0.15)'}`,
            borderRadius: 5,
            fontSize: 11,
            color: isDragOver ? borderColor : 'var(--muted)',
            background: isDragOver ? `${borderColor}10` : 'transparent',
            cursor: 'default',
            textAlign: 'center' as const,
            transition: 'border-color 0.15s, background 0.15s, color 0.15s',
          }}
          onDragOver={e => { e.preventDefault(); e.stopPropagation(); setDragOverBranch(branchKey) }}
          onDragLeave={e => { e.stopPropagation(); setDragOverBranch(null) }}
          onDrop={e => {
            e.preventDefault()
            e.stopPropagation()
            setDragOverBranch(null)
            const raw = e.dataTransfer.getData('application/json')
            if (!raw) return
            const b: PaletteBlock = JSON.parse(raw) as PaletteBlock
            addToNestedBranch(topIdx, parentPath, branch, b)
          }}
        >
          {branchSteps.length === 0 ? '+ Drop step here' : '+ Drop another step'}
        </div>
      </div>
    )
  }

  // ── Special step bodies ─────────────────────────────────────────
  function renderIfBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    const thenSteps = (step.then_steps as FlowStep[]) ?? []
    const elseSteps = (step.else_steps as FlowStep[]) ?? []
    const rawCond = step['condition']
    const isPatternCond = rawCond != null && typeof rawCond === 'object' && isPatternCondition(rawCond)
    const patternCond = isPatternCond ? (rawCond as PatternCondition) : null
    return (
      <div className="step-body">
        {/* Condition row: pattern badge OR text field + toggle button */}
        <div className="field-row">
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
            <label className="field-label" style={{ margin: 0 }}>
              {defs['condition']?.label ?? 'Condition'}
            </label>
            <button
              className="btn muted"
              style={{ width: 'auto', padding: '2px 8px', fontSize: 11, marginLeft: 'auto' }}
              title={isPatternCond ? 'Edit pattern condition' : 'Build a pattern condition visually'}
              onClick={() => openPatternBuilder(i)}
            >
              {isPatternCond ? '✎ Edit Pattern' : '+ Pattern'}
            </button>
            {isPatternCond && (
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '2px 8px', fontSize: 11, color: '#e87070' }}
                title="Remove pattern condition and use text condition instead"
                onClick={() => clearPatternCondition(i)}
              >
                ✕
              </button>
            )}
          </div>
          {isPatternCond ? (
            /* Show pattern condition summary */
            <div style={{
              background: '#0b1220',
              border: '1px solid #22355d',
              borderRadius: 6,
              padding: '6px 10px',
              fontFamily: "'JetBrains Mono','Fira Code',monospace",
              fontSize: 12,
              color: '#ecf0f9',
            }}>
              <span style={{ color: '#93a1bf' }}>
                {patternCond!.source}
                {patternCond!.sourceKey ? `[${patternCond!.sourceKey}]` : ''}
                {' matches '}
              </span>
              <span style={{ color: '#57b5ff' }}>{patternCond!.pattern}</span>
              {patternCond!.flags && (
                <span style={{ color: '#93a1bf' }}> (flags: {patternCond!.flags})</span>
              )}
              {patternCond!.strategy && patternCond!.strategy !== 'auto' && (
                <span style={{ color: '#93a1bf' }}> [{patternCond!.strategy}]</span>
              )}
            </div>
          ) : (
            /* Show normal text condition input */
            fieldInput(i, 'condition', (rawCond as string) ?? '', defs['condition'], { smart: true })
          )}
        </div>
        {thenSteps.length === 0 && fieldInput(i, 'then', (step['then'] as string) ?? '', defs['then'])}
        {renderBranch('✓ THEN', false, thenSteps, i, 'then_steps', [])}
        {elseSteps.length === 0 && fieldInput(i, 'else', (step['else'] as string) ?? '', defs['else'])}
        {renderBranch('✗ ELSE', true, elseSteps, i, 'else_steps', [])}
      </div>
    )
  }

  function renderSwitchBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    const cases = parseCases(normalizeCases(step['cases']))
    return (
      <div className="step-body">
        {/* Match slot — smart: accepts header.X-TID or bare "X-TID" */}
        {fieldInput(i, 'as', (step['as'] as string) ?? '', defs['as'], { smart: true })}
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

  // ── Template pattern editor ──────────────────────────────────────
  function renderTemplatePatternBody(step: TemplatePatternStep, i: number, action: 'validate_pattern' | 'extract_pattern') {
    return (
      <TemplatePatternBuilder
        action={action}
        step={step}
        onChange={(updatedStep) => {
          // Update the step with all changed fields
          const newStep = { ...step }
          if (updatedStep.source !== step.source) newStep.source = updatedStep.source
          if (updatedStep.input !== step.input) newStep.input = updatedStep.input
          if (action === 'validate_pattern' && updatedStep.as !== step.as) newStep.as = updatedStep.as

          // Apply changes to steps array
          setSteps(steps.map((s, idx) => idx === i ? newStep : s))
        }}
      />
    )
  }

  // ── Cache step editor ─────────────────────────────────────────
  function renderCacheBody(step: FlowStep, i: number) {
    const action      = step.action as string
    const isGlobal    = action.includes('_global')
    const isWrite     = action.includes('_put')
    const keyVar      = (step['key_identifier'] ?? '') as string
    const outputVar   = (step['as']             ?? '') as string
    const valueVar    = (step['source']          ?? '') as string
    const ttl         = (step['ttl']             ?? '300') as string
    const prevVars    = slotsUpTo(i)

    const scopeColor  = isGlobal ? '#f59e0b' : '#57b5ff'
    const scopeLabel  = isGlobal ? 'SHARED · all tenants see this data' : 'PER-TENANT · private to this tenant'
    const scopeTip    = isGlobal
      ? 'The same cached value is readable by every tenant. Use for data that does not vary per tenant (e.g. a public product list).'
      : 'Each tenant has their own isolated copy. Tenant A cannot read Tenant B\'s cache.'

    return (
      <div className="step-body">

        {/* Scope badge */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8,
          padding: '6px 10px', marginBottom: 12,
          borderRadius: 6,
          background: isGlobal ? 'rgba(245,158,11,0.08)' : 'rgba(87,181,255,0.08)',
          border: `1px solid ${isGlobal ? 'rgba(245,158,11,0.3)' : 'rgba(87,181,255,0.3)'}`,
        }}>
          <span style={{ fontSize: 14 }}>{isGlobal ? '🌐' : '🔒'}</span>
          <div>
            <div style={{ fontSize: 11, fontWeight: 700, color: scopeColor, letterSpacing: '0.04em' }}>
              {scopeLabel}
            </div>
            <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 1 }}>{scopeTip}</div>
          </div>
        </div>

        {/* Operation flow diagram */}
        <div style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '7px 10px', marginBottom: 12,
          borderRadius: 6, background: 'rgba(255,255,255,0.03)',
          fontSize: 11, color: 'var(--muted)', fontFamily: 'monospace',
        }}>
          {isWrite ? (
            <>
              <span style={{ color: 'var(--text)' }}>{keyVar || '‹key var›'}</span>
              <span>+</span>
              <span style={{ color: 'var(--text)' }}>{valueVar || '‹value var›'}</span>
              <span style={{ color: scopeColor }}>──→</span>
              <span>cache write</span>
              <span style={{ color: scopeColor }}>──→</span>
              <span>expires in {ttl}s</span>
            </>
          ) : (
            <>
              <span style={{ color: 'var(--text)' }}>{keyVar || '‹key var›'}</span>
              <span style={{ color: scopeColor }}>──→</span>
              <span>cache lookup</span>
              <span style={{ color: scopeColor }}>──→</span>
              <span style={{ color: '#34d399' }}>{outputVar || '‹output var›'}</span>
              <span style={{ color: 'var(--muted)', marginLeft: 4 }}>(on hit)</span>
            </>
          )}
        </div>

        {/* Cache key variable */}
        <label className="field-label">
          Cache key <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(variable name)</span>
        </label>
        {prevVars.length > 0 ? (
          <select className="input" value={keyVar} onChange={e => updateStep(i, 'key_identifier', e.target.value)}>
            <option value="">— pick a variable —</option>
            {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        ) : (
          <input className="input" placeholder="cache_key"
            value={keyVar} onChange={e => updateStep(i, 'key_identifier', e.target.value)} />
        )}
        <span className="field-desc">The variable whose value acts as the lookup key — e.g. a user ID or request path.</span>

        {isWrite ? (
          <>
            {/* Value variable */}
            <label className="field-label" style={{ marginTop: 8 }}>
              Value to cache <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(variable name)</span>
            </label>
            {prevVars.length > 0 ? (
              <select className="input" value={valueVar} onChange={e => updateStep(i, 'source', e.target.value)}>
                <option value="">— pick a variable —</option>
                {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            ) : (
              <input className="input" placeholder="upstream_response"
                value={valueVar} onChange={e => updateStep(i, 'source', e.target.value)} />
            )}
            <span className="field-desc">The variable holding the data you want to store — e.g. an upstream API response.</span>

            {/* TTL */}
            <label className="field-label" style={{ marginTop: 8 }}>
              Expires after <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(seconds)</span>
            </label>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <input className="input" type="number" style={{ width: 120 }} placeholder="300"
                value={ttl} onChange={e => updateStep(i, 'ttl', e.target.value)} />
              <span style={{ fontSize: 11, color: 'var(--muted)' }}>
                {Number(ttl) >= 3600 ? `${(Number(ttl)/3600).toFixed(1)}h`
                  : Number(ttl) >= 60 ? `${Math.round(Number(ttl)/60)}m`
                  : `${ttl}s`}
              </span>
            </div>
            <span className="field-desc">Entry is removed automatically after this many seconds. Common values: 300 (5 min), 3600 (1 h).</span>
          </>
        ) : (
          <>
            {/* Output variable */}
            <label className="field-label" style={{ marginTop: 8 }}>
              Output variable <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(where to save the result)</span>
            </label>
            <input className="input" placeholder="cached_body"
              value={outputVar} onChange={e => updateStep(i, 'as', e.target.value)} />
            <span className="field-desc">
              On a <span style={{ color: '#34d399', fontWeight: 600 }}>cache hit</span> the stored value is written here.{' '}
              On a <span style={{ color: '#f87171', fontWeight: 600 }}>miss</span> this variable is <strong>empty</strong>{' '}
              (if it is new) or keeps whatever value it had before this step.{' '}
              Add an <code>if</code> step after this and check <code>{outputVar || 'this variable'} != ""</code> to branch on hit vs miss.
            </span>
          </>
        )}
      </div>
    )
  }

  // ── LLM Call step editor ─────────────────────────────────────────
  function renderLlmCallBody(step: FlowStep, i: number) {
    const rawInput = step['input']
    let cfg: Record<string, string> = {}
    if (typeof rawInput === 'string') {
      try { cfg = JSON.parse(rawInput) } catch { /**/ }
    } else if (rawInput && typeof rawInput === 'object') {
      cfg = { ...(rawInput as Record<string, string>) }
    }

    function updateCfg(key: string, value: string) {
      const next = { ...cfg, [key]: value }
      if (!value) delete next[key]
      updateStep(i, 'input', JSON.stringify(next))
    }

    const model         = cfg['model'] ?? ''
    const fallbackRaw   = cfg['fallback_chain'] ?? ''
    const fallbackChain = fallbackRaw ? fallbackRaw.split(',').filter(Boolean) : []
    const promptVar     = (step['key_identifier'] ?? '') as string
    const outputVar     = (step['as'] ?? '') as string
    const maxTokens     = cfg['max_tokens'] ?? '2000'
    const temperature   = cfg['temperature'] ?? '0.7'
    const messagesSlot  = cfg['messages_slot'] ?? ''
    const systemSlot    = cfg['system_slot'] ?? ''

    const modelAliases = llmModels.map(m => m.alias)
    const prevVars     = slotsUpTo(i)

    const usedInFallback = new Set(fallbackChain)
    const availableForFallback = (idx: number) =>
      modelAliases.filter(m => m !== model && (!usedInFallback.has(m) || fallbackChain[idx] === m))

    return (
      <div className="step-body">
        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 10px', marginBottom: 12, borderRadius: 6, background: 'rgba(139,92,246,0.08)', border: '1px solid rgba(139,92,246,0.3)' }}>
          <span style={{ fontSize: 14 }}>🧠</span>
          <div>
            <div style={{ fontSize: 11, fontWeight: 700, color: '#a78bfa', letterSpacing: '0.04em' }}>LLM CALL</div>
            <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 1 }}>
              {model ? `→ ${model}${fallbackChain.length ? ` (${fallbackChain.length} fallback${fallbackChain.length > 1 ? 's' : ''})` : ''}` : 'No model selected'}
            </div>
          </div>
        </div>

        {/* Primary model */}
        <label className="field-label">Primary model</label>
        {modelAliases.length > 0 ? (
          <select className="input" value={model} onChange={e => updateCfg('model', e.target.value)}>
            <option value="">— select a model —</option>
            {modelAliases.map(m => <option key={m} value={m}>{m}</option>)}
          </select>
        ) : (
          <input className="input" placeholder="e.g. claude-3-5-sonnet" value={model}
            onChange={e => updateCfg('model', e.target.value)} />
        )}
        <span className="field-desc">
          {modelAliases.length === 0
            ? 'No models configured — add them under Settings → AI Models.'
            : 'The LLM to call. Aliases are configured under Settings → AI Models.'}
        </span>

        {/* Fallback chain */}
        <label className="field-label" style={{ marginTop: 10 }}>
          Fallback chain <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(tried in order on rate-limit / 5xx)</span>
        </label>
        {fallbackChain.map((fb, fi) => (
          <div key={fi} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
            <span style={{ fontSize: 10, color: 'var(--muted)', width: 18, textAlign: 'right', flexShrink: 0 }}>{fi + 1}.</span>
            {modelAliases.length > 0 ? (
              <select className="input" style={{ flex: 1 }} value={fb} onChange={e => {
                const next = [...fallbackChain]; next[fi] = e.target.value
                updateCfg('fallback_chain', next.join(','))
              }}>
                <option value="">— select —</option>
                {availableForFallback(fi).map(m => <option key={m} value={m}>{m}</option>)}
                {/* Keep current value visible even if not in list */}
                {fb && !modelAliases.includes(fb) && <option value={fb}>{fb}</option>}
              </select>
            ) : (
              <input className="input" style={{ flex: 1 }} placeholder="model alias" value={fb}
                onChange={e => { const next = [...fallbackChain]; next[fi] = e.target.value; updateCfg('fallback_chain', next.join(',')) }} />
            )}
            <button className="btn muted" style={{ padding: '2px 8px', fontSize: 13, flexShrink: 0 }}
              onClick={() => updateCfg('fallback_chain', fallbackChain.filter((_, j) => j !== fi).join(','))}>
              ×
            </button>
          </div>
        ))}
        <button className="btn muted mt4" style={{ fontSize: 11 }}
          onClick={() => updateCfg('fallback_chain', [...fallbackChain, ''].join(','))}>
          + Add fallback
        </button>
        {fallbackChain.length === 0 && (
          <span className="field-desc">No fallback configured — if the primary model fails the request will error.</span>
        )}

        {/* Input / output variables */}
        <label className="field-label" style={{ marginTop: 10 }}>
          Prompt variable <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(slot holding user message)</span>
        </label>
        {prevVars.length > 0 ? (
          <select className="input" value={promptVar} onChange={e => updateStep(i, 'key_identifier', e.target.value)}>
            <option value="">— pick a variable —</option>
            {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
        ) : (
          <input className="input" placeholder="var.prompt" value={promptVar}
            onChange={e => updateStep(i, 'key_identifier', e.target.value)} />
        )}

        <label className="field-label" style={{ marginTop: 8 }}>
          Output variable <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(where response is stored)</span>
        </label>
        <input className="input" placeholder="var.reply" value={outputVar}
          onChange={e => updateStep(i, 'as', e.target.value)} />

        {/* Parameters */}
        <div style={{ display: 'flex', gap: 10, marginTop: 10 }}>
          <div style={{ flex: 1 }}>
            <label className="field-label">Max tokens</label>
            <input className="input" type="number" placeholder="2000" value={maxTokens}
              onChange={e => updateCfg('max_tokens', e.target.value)} />
          </div>
          <div style={{ flex: 1 }}>
            <label className="field-label">Temperature</label>
            <input className="input" type="number" step="0.1" min="0" max="2" placeholder="0.7" value={temperature}
              onChange={e => updateCfg('temperature', e.target.value)} />
          </div>
        </div>

        {/* Advanced */}
        <details style={{ marginTop: 10 }}>
          <summary style={{ fontSize: 11, color: 'var(--muted)', cursor: 'pointer', userSelect: 'none' as const, padding: '4px 0' }}>
            Advanced options
          </summary>
          <div style={{ paddingTop: 8 }}>
            <label className="field-label">Messages slot <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(multi-turn)</span></label>
            {prevVars.length > 0 ? (
              <select className="input" value={messagesSlot} onChange={e => updateCfg('messages_slot', e.target.value)}>
                <option value="">— none —</option>
                {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            ) : (
              <input className="input" placeholder="var.messages" value={messagesSlot}
                onChange={e => updateCfg('messages_slot', e.target.value)} />
            )}
            <span className="field-desc">Slot holding the full messages array for multi-turn conversations (from parse_message_format).</span>

            <label className="field-label" style={{ marginTop: 8 }}>System prompt slot</label>
            {prevVars.length > 0 ? (
              <select className="input" value={systemSlot} onChange={e => updateCfg('system_slot', e.target.value)}>
                <option value="">— none —</option>
                {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            ) : (
              <input className="input" placeholder="var.system" value={systemSlot}
                onChange={e => updateCfg('system_slot', e.target.value)} />
            )}
            <span className="field-desc">Slot holding a dynamic system prompt injected before the conversation.</span>
          </div>
        </details>
      </div>
    )
  }

  function renderGenericBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    // 'action' is the step type; obs meta-fields are rendered separately by renderObsFooter
    // 'then_steps', 'else_steps', and 'trace_vars' are Studio-only fields, not generic params
    const obsKeys = new Set(['action', 'log_as', 'trace_capture', 'trace_vars', 'then_steps', 'else_steps'])
    const params = Object.entries(step).filter(([k]) => !obsKeys.has(k))
    if (params.length === 0) {
      return (
        <div className="step-body">
          <span className="hint">No parameters for this step.</span>
        </div>
      )
    }
    // Fields that benefit from smart expression parsing
    // 'as' excluded: it's an output slot name, not a source expression (switch has its own renderer)
    const smartFields = new Set(['condition', 'source', 'url'])
    return (
      <div className="step-body">
        {params.map(([k, v]) =>
          fieldInput(i, k, String(v ?? ''), defs[k], smartFields.has(k) ? { smart: true } : undefined)
        )}
      </div>
    )
  }

  // ── Per-step observability footer ─────────────────────────────
  // Rendered below the step body when the step is expanded.
  // "Log result as" → emits a log_field instruction in the compiler.
  // "Capture for trace" → emits trace_capture instructions in the compiler
  //   for each variable in the trace_vars list (multi-variable selector).
  //   Legacy trace_capture: 'true' (single checkbox) is still honoured for
  //   backward-compat but migrated to trace_vars on first user interaction.
  function renderObsFooter(step: FlowStep, i: number) {
    const action = step.action as string
    // Skip control-flow steps that don't produce a single output variable
    const skipActions = new Set(['if','switch','call','return','fail','capture_error','batch_flush'])
    if (skipActions.has(action)) return null

    const logAs     = (step['log_as'] ?? '') as string
    const outputVar = (step['as'] ?? step['destination'] ?? '') as string

    // Resolve current trace vars — support legacy trace_capture bool
    let traceVars: string[] = Array.isArray(step['trace_vars'])
      ? (step['trace_vars'] as string[])
      : ((step['trace_capture'] as string) === 'true' && outputVar ? [outputVar] : [])

    // All variables reachable from this step (prior steps + own output)
    const prevVars     = slotsUpTo(i)
    const allVars      = [...new Set([...prevVars, outputVar].filter(Boolean))]
    const unselected   = allVars.filter(v => !traceVars.includes(v))

    function setTraceVars(next: string[]) {
      setSteps(steps.map((s, idx) => idx !== i ? s : ({
        ...s,
        trace_vars: next,
        trace_capture: '',   // clear legacy flag
      } as FlowStep)))
    }

    function addTraceVar(varName: string) {
      if (!varName || traceVars.includes(varName)) return
      setTraceVars([...traceVars, varName])
    }

    function removeTraceVar(varName: string) {
      setTraceVars(traceVars.filter(v => v !== varName))
    }

    return (
      <div style={{
        marginTop: 8,
        padding: '8px 10px',
        borderTop: '1px solid rgba(255,255,255,0.07)',
        background: 'rgba(0,0,0,0.15)',
        borderRadius: '0 0 6px 6px',
      }}>
        <div style={{ fontSize: 10, fontWeight: 700, color: 'var(--muted)', letterSpacing: '0.05em', marginBottom: 8, textTransform: 'uppercase' }}>
          Trace &amp; Log
        </div>
        <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start', flexWrap: 'wrap' }}>

          {/* Log result as */}
          <div style={{ flex: '1 1 160px', minWidth: 140 }}>
            <label className="field-label" style={{ fontSize: 10 }}>
              Log output as <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(access log field name)</span>
            </label>
            <input
              className="input"
              style={{ fontSize: 11 }}
              placeholder={outputVar ? `e.g. ${outputVar}` : 'field_name'}
              value={logAs}
              onChange={e => updateStep(i, 'log_as', e.target.value)}
            />
            {logAs && outputVar && (
              <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 2 }}>
                Writes <code style={{ color: '#34d399' }}>{outputVar}</code> → access log as <code style={{ color: '#f59e0b' }}>{logAs}</code>
              </div>
            )}
            {logAs && !outputVar && (
              <div style={{ fontSize: 10, color: '#f87171', marginTop: 2 }}>
                Set an output variable (the "as" field) on this step first.
              </div>
            )}
          </div>

          {/* Capture for trace — multi-variable selector */}
          <div style={{ flex: '1 1 200px', minWidth: 180 }}>
            <label className="field-label" style={{ fontSize: 10 }}>
              Capture for trace <span style={{ color: 'var(--muted)', fontWeight: 400 }}>(variables to record)</span>
            </label>

            {/* Current trace vars as chips */}
            {traceVars.length > 0 && (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 6 }}>
                {traceVars.map(v => (
                  <span key={v} style={{
                    display: 'inline-flex', alignItems: 'center', gap: 4,
                    padding: '2px 7px', borderRadius: 4,
                    background: 'rgba(167,139,250,0.12)', border: '1px solid rgba(167,139,250,0.3)',
                    fontSize: 11, color: '#a78bfa', fontFamily: 'monospace',
                  }}>
                    {v}
                    <button
                      onClick={() => removeTraceVar(v)}
                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(167,139,250,0.6)', padding: 0, lineHeight: 1, fontSize: 13 }}
                      title={`Remove ${v} from trace`}
                    >×</button>
                  </span>
                ))}
              </div>
            )}

            {/* Add variable picker */}
            {unselected.length > 0 ? (
              <select
                className="input"
                style={{ fontSize: 11 }}
                value=""
                onChange={e => { if (e.target.value) addTraceVar(e.target.value) }}
              >
                <option value="">+ Add variable…</option>
                {unselected.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            ) : allVars.length === 0 ? (
              <div style={{ fontSize: 10, color: 'var(--muted)' }}>
                No variables available yet — add steps with output variables above.
              </div>
            ) : (
              <div style={{ fontSize: 10, color: 'var(--muted)' }}>
                All available variables are selected.
              </div>
            )}

            {traceVars.length > 0 && (
              <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 4 }}>
                {traceVars.length} variable{traceVars.length > 1 ? 's' : ''} will appear in trace output for this step.
              </div>
            )}
          </div>
        </div>
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

  // ── Variable picker popup close handler ────────────────────────
  useEffect(() => {
    if (!varPopup) return
    function handleClick() { setVarPopup(null) }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [varPopup])

  // ── VarPopup component ─────────────────────────────────────────
  function VarPopup() {
    if (!varPopup) return null
    const available = slotsUpTo(varPopup.stepIdx)
    if (available.length === 0) return null
    return (
      <div
        style={{
          position: 'fixed',
          top: varPopup.anchor.bottom + 4,
          left: varPopup.anchor.left,
          zIndex: 9999,
          background: 'var(--panel)',
          border: '1px solid var(--border)',
          borderRadius: 8,
          boxShadow: '0 8px 24px rgba(0,0,0,0.4)',
          minWidth: 200,
          maxWidth: 320,
          maxHeight: 280,
          overflowY: 'auto',
          padding: 8,
        }}
        onMouseDown={e => e.preventDefault()}
      >
        <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 6, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
          Variables available at step {varPopup.stepIdx + 1}
        </div>
        {available.map(v => (
          <div
            key={v}
            onClick={() => {
              const active = document.activeElement as HTMLInputElement | null
              if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA')) {
                const start = active.selectionStart ?? active.value.length
                const end = active.selectionEnd ?? active.value.length
                const newVal = active.value.slice(0, start) + v + active.value.slice(end)
                const nativeInput = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')
                nativeInput?.set?.call(active, newVal)
                active.dispatchEvent(new Event('input', { bubbles: true }))
              }
              setVarPopup(null)
            }}
            style={{
              padding: '4px 8px',
              borderRadius: 4,
              cursor: 'pointer',
              fontSize: 12,
              fontFamily: 'monospace',
              color: 'var(--fg)',
              background: 'rgba(255,255,255,0.03)',
              marginBottom: 2,
            }}
            onMouseEnter={e => (e.currentTarget.style.background = 'rgba(87,181,255,0.1)')}
            onMouseLeave={e => (e.currentTarget.style.background = 'rgba(255,255,255,0.03)')}
          >
            {v}
          </div>
        ))}
      </div>
    )
  }

  // ── Collect variable names from prior steps (for pickers) ──────
  function slotsUpTo(upToIdx: number): string[] {
    const vars: string[] = []
    function collectFromSteps(stepList: FlowStep[], limit: number) {
      for (let j = 0; j < limit && j < stepList.length; j++) {
        const s = stepList[j]
        // Primary output variable
        if (s['as'] && typeof s['as'] === 'string') vars.push(s['as'] as string)
        // Steps that bind into key_identifier field or out/output aliases
        if (['bind_client_ip','store_internal_tx_id','bind_correlation_id',
             'bind_header','bind_query_param','bind_path','bind_body',
             'registry_lookup','load_service_url','load_identifier','load_secret',
             'cache_get','cache_get_global','json_extract_emit',
             'llm_call','http_call','mcp_call_tool','vector_search','embed_text',
             'semantic_cache_get','token_validation','load_history',
             'parse_tool_calls','execute_plan'].includes(s.action as string)) {
          if (s['key_identifier'] && typeof s['key_identifier'] === 'string') vars.push(s['key_identifier'] as string)
          if (s['out'] && typeof s['out'] === 'string') vars.push(s['out'] as string)
        }
        // For call steps: look into the referenced subflow's steps
        if (s.action === 'call' && typeof s['flow_name'] === 'string') {
          const subFlow = savedFlows.find(f => f.name === s['flow_name'])
          if (subFlow) collectFromSteps(subFlow.steps, subFlow.steps.length)
        }
        // For if/else with inline steps: collect from branches
        if (s.then_steps) collectFromSteps(s.then_steps as FlowStep[], (s.then_steps as FlowStep[]).length)
        if (s.else_steps) collectFromSteps(s.else_steps as FlowStep[], (s.else_steps as FlowStep[]).length)
      }
    }
    collectFromSteps(steps, upToIdx)
    return [...new Set(vars.filter(Boolean))]
  }

  // ── Variable validation helper ─────────────────────────────────
  function isVarDefined(varName: string, atIdx: number): boolean {
    if (!varName || !varName.trim()) return true // empty = ok, no warning
    const available = slotsUpTo(atIdx)
    // If varName starts with 'var.' or prefix, check it directly
    if (varName.startsWith('var.') || varName.startsWith('_h_') ||
        varName.startsWith('_q_') || varName.startsWith('_b_') ||
        varName.startsWith('_p_')) {
      return available.includes(varName)
    }
    // If it's a source ref like header.X-TID, body.userId — these are always valid
    if (/^(header|queryparam|query|body|path)\./.test(varName)) return true
    // Plain literal or number — always valid
    return true
  }

  function computeFlowOutputs(flowSteps: FlowStep[]): string[] {
    const vars: string[] = []
    for (const s of flowSteps) {
      if (s['as'] && typeof s['as'] === 'string') vars.push(s['as'] as string)
      if (s['key_identifier'] && typeof s['key_identifier'] === 'string') vars.push(s['key_identifier'] as string)
      if (s.then_steps) vars.push(...computeFlowOutputs(s.then_steps as FlowStep[]))
      if (s.else_steps) vars.push(...computeFlowOutputs(s.else_steps as FlowStep[]))
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
  function renderTokenValidationBody(
    step: FlowStep,
    onUpdateField: (key: string, value: string) => void,
    prevVars: string[],
    claimKey: string | number,
  ) {
    let inputObj: Record<string, string> = {}
    try { inputObj = JSON.parse((step['input'] ?? '{}') as string) } catch { /* ignore */ }

    // Atomic multi-key update — all changes go in a single setSteps call so
    // no stale-closure overwrite when two keys must change together.
    function updateInputKeys(updates: Record<string, string>) {
      const next = { ...inputObj }
      for (const [k, v] of Object.entries(updates)) {
        if (v) next[k] = v; else delete next[k]
      }
      onUpdateField('input', JSON.stringify(next))
    }
    function updateInputKey(key: string, value: string) {
      updateInputKeys({ [key]: value })
    }

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
        : splitComma('signature,expiry')
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
      onUpdateField('input', JSON.stringify(next))
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
            onChange={e => { if (e.target.value !== '__custom__') onUpdateField('key_identifier', e.target.value) }}>
            {commonSources.map(s => <option key={s} value={s}>{s}</option>)}
            {prevVars.length > 0 && <option disabled>--- Variables ---</option>}
            {prevVars.map(v => <option key={v} value={v}>{v} (variable)</option>)}
            {!commonSources.includes(keyId) && !prevVars.includes(keyId) && keyId &&
              <option value="__custom__">custom: {keyId}</option>}
          </select>
          {!commonSources.includes(keyId) && !prevVars.includes(keyId) && (
            <input className="input" style={{ marginTop: 4 }} placeholder="header.Authorization"
              value={keyId} onChange={e => onUpdateField('key_identifier', e.target.value)} />
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
          <input type="checkbox" id={`prefetch_${claimKey}`}
            checked={inputObj['jwt.prefetch_jwks'] !== 'false'}
            onChange={e => updateInputKey('jwt.prefetch_jwks', e.target.checked ? 'true' : 'false')} />
          <label htmlFor={`prefetch_${claimKey}`} className="field-label" style={{ margin: 0 }}>
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
                <input type="checkbox" id={`validate_scopes_${claimKey}`}
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
                <label htmlFor={`validate_scopes_${claimKey}`} className="field-label" style={{ margin: 0 }}>
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
        {pendingClaims[claimKey] && (() => {
          const draft = pendingClaims[claimKey]
          function updateDraft(patch: Partial<DraftClaim>) {
            setPendingClaims(prev => ({ ...prev, [claimKey]: { ...draft, ...patch } }))
          }
          function commitDraft() {
            if (!draft.key.trim()) { clearPendingClaim(claimKey); return }
            saveClaimRows([...claimRows, draft])
            clearPendingClaim(claimKey)
          }
          return (
            <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginBottom: 6, padding: '6px 8px', borderRadius: 6, border: '1px dashed rgba(87,181,255,0.4)', background: 'rgba(87,181,255,0.04)' }}>
              <input className="input" style={{ flex: 1 }} placeholder="claim key (e.g. role)" autoFocus
                value={draft.key}
                onChange={e => updateDraft({ key: e.target.value })}
                onKeyDown={e => { if (e.key === 'Enter') commitDraft(); if (e.key === 'Escape') clearPendingClaim(claimKey) }} />
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
                  onKeyDown={e => { if (e.key === 'Enter') commitDraft(); if (e.key === 'Escape') clearPendingClaim(claimKey) }} />
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
                onClick={() => clearPendingClaim(claimKey)} title="Cancel (Esc)">×</button>
            </div>
          )
        })()}
        <button className="btn muted" style={{ marginBottom: 10, fontSize: 11 }}
          onClick={() => setPendingClaims(prev => ({ ...prev, [claimKey]: { key: '', mode: 'static' as const, value: '' } }))}>
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
        <label className="field-label">Response body — variable or literal</label>
        {prevVars.length > 0 ? (
          <>
            <select className="input" value={prevVars.includes(source) ? source : ''}
              onChange={e => updateStep(i, 'source', e.target.value)}>
              <option value="">— pick a variable, or type a literal below —</option>
              {prevVars.map(v => <option key={v} value={v}>{v}</option>)}
            </select>
            {(!prevVars.includes(source) || source === '') && (
              <input className="input" style={{ marginTop: 4 }} placeholder='{"status":"ok"} or a variable name'
                value={source} onChange={e => updateStep(i, 'source', e.target.value)} />
            )}
          </>
        ) : (
          <input className="input" placeholder='{"status":"ok"} or a variable name' value={source}
            onChange={e => updateStep(i, 'source', e.target.value)} />
        )}
        <span className="field-desc">Type a variable name to use a computed value, or type a literal string / JSON directly.</span>
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

  // ── Delete impact panel ───────────────────────────────────────────
  function DeleteImpactPanel({ flowName, impact, onConfirm, onCancel, onOpenFlow, onNavigateToApis }: {
    flowName: string
    impact?: { flows: string[]; apis: string[] }
    onConfirm: () => void
    onCancel: () => void
    onOpenFlow?: (name: string) => void
    onNavigateToApis?: () => void
  }) {
    const callerFlows = impact?.flows ?? []
    const callerApis  = impact?.apis  ?? []
    const hasImpact   = callerFlows.length + callerApis.length > 0
    return (
      <div
        style={{
          position: 'absolute', right: 8, top: 32, zIndex: 200,
          background: 'var(--panel)', border: `1px solid ${hasImpact ? 'rgba(245,158,11,0.4)' : 'rgba(239,68,68,0.3)'}`,
          borderRadius: 8, padding: 10, minWidth: 220, maxWidth: 300,
          boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
        }}
        onClick={e => e.stopPropagation()}
      >
        <div style={{ fontWeight: 700, fontSize: 12, marginBottom: 7, color: hasImpact ? '#f59e0b' : 'var(--fg)' }}>
          {hasImpact ? `⚠ "${flowName}" is in use` : `Delete "${flowName}"?`}
        </div>
        {callerFlows.length > 0 && (
          <div style={{ marginBottom: 6 }}>
            <div style={{ fontSize: 10, color: 'var(--muted)', marginBottom: 3, textTransform: 'uppercase', letterSpacing: '0.05em' }}>Used by flows</div>
            {callerFlows.map(f => (
              <button
                key={f}
                onClick={() => onOpenFlow?.(f)}
                style={{ display: 'block', fontSize: 11, color: 'var(--accent)', background: 'none', border: 'none', cursor: 'pointer', padding: '1px 0', textDecoration: 'underline', textAlign: 'left' }}
              >
                ⛶ {f} ↗
              </button>
            ))}
          </div>
        )}
        {callerApis.length > 0 && (
          <div style={{ marginBottom: 6 }}>
            <div style={{ fontSize: 10, color: 'var(--muted)', marginBottom: 3, textTransform: 'uppercase', letterSpacing: '0.05em' }}>Used by APIs</div>
            {callerApis.map(a => (
              <button
                key={a}
                onClick={() => onNavigateToApis?.()}
                style={{ display: 'block', fontSize: 11, color: '#34d399', background: 'none', border: 'none', cursor: 'pointer', padding: '1px 0', textDecoration: 'underline', textAlign: 'left' }}
              >
                ⬡ {a} ↗
              </button>
            ))}
          </div>
        )}
        {hasImpact && (
          <div style={{ fontSize: 10, color: 'var(--muted)', marginBottom: 8, lineHeight: 1.4 }}>
            Update or remove these references before deleting, or delete anyway.
          </div>
        )}
        <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end', marginTop: 6 }}>
          <button
            style={{ fontSize: 11, padding: '2px 8px', background: 'rgba(239,68,68,0.15)', border: '1px solid rgba(239,68,68,0.35)', borderRadius: 4, cursor: 'pointer', color: '#ef4444' }}
            onClick={onConfirm}
          >
            Delete
          </button>
          <button
            style={{ fontSize: 11, padding: '2px 8px', background: 'none', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 4, cursor: 'pointer', color: 'var(--muted)' }}
            onClick={onCancel}
          >
            Cancel
          </button>
        </div>
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
    <Fragment>
    <div className="two-col">
      {/* ── Palette ─────────────────────────────────────────────── */}
      <div className="panel">
        <div className="panel-header">
          <span>Step Palette</span>
        </div>

        <div className="panel-body">
          {/* Filter input */}
          <input
            className="input"
            placeholder="filter by name, category, or type"
            value={filter}
            onChange={e => setFilter(e.target.value)}
            style={{ marginBottom: 8 }}
          />

          {/* Unified palette with collapsible sections */}
          <div className="block-list">
            {/* My Flows section */}
            <div>
              <div
                className="palette-section-label"
                style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer', userSelect: 'none', marginTop: 8 }}
                onClick={() => setCollapsedSections(prev => {
                  const s = new Set(prev)
                  s.has('My Flows') ? s.delete('My Flows') : s.add('My Flows')
                  return s
                })}
              >
                <span>📋</span>
                <span style={{ flex: 1 }}>My Flows {savedFlows.length > 0 ? `(${savedFlows.length})` : ''}</span>
                <span style={{ fontSize: 10, opacity: 0.5 }}>{collapsedSections.has('My Flows') ? '▶' : '▼'}</span>
              </div>
              {!collapsedSections.has('My Flows') && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                  {savedFlows.length === 0 && (
                    <span className="hint">No flows saved yet. Design a flow in the canvas and save it.</span>
                  )}
                  {savedFlows
                    .filter(sf => !filter || sf.name.toLowerCase().includes(q))
                    .map(sf => {
                      const dragPayload = savedFlowBlocks.find(b => b.title === sf.name) ?? {
                        type: 'call', title: sf.name, description: '', category: 'my-flows',
                        capability: 'sub-flow', supports_nested: false,
                        defaults: { flow_name: sf.name }, fields: callFieldDefs,
                      }
                      return (
                        <div
                          key={sf.name}
                          draggable
                          onDragStart={e => e.dataTransfer.setData('application/json', JSON.stringify(dragPayload))}
                          onClick={() => onOpenFlow?.(sf.name)}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            padding: '8px 10px',
                            borderRadius: 6,
                            border: '1px solid rgba(255,255,255,0.08)',
                            background: sf.name === flowName ? 'rgba(87,181,255,0.07)' : 'rgba(255,255,255,0.03)',
                            borderColor: sf.name === flowName ? 'rgba(87,181,255,0.3)' : 'rgba(255,255,255,0.08)',
                            cursor: 'pointer',
                            gap: 8,
                            position: 'relative',
                          }}
                        >
                          <div style={{ flex: 1, minWidth: 0 }}>
                            <div style={{
                              fontWeight: 600,
                              fontSize: 13,
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                              color: sf.name === flowName ? 'var(--accent)' : 'var(--fg)',
                            }}>
                              {sf.name}
                            </div>
                            <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 2 }}>
                              {sf.steps.length} step{sf.steps.length !== 1 ? 's' : ''} · drag to call
                            </div>
                            {(() => {
                              const outputs = computeFlowOutputs(sf.steps)
                              if (outputs.length === 0) return null
                              return (
                                <div style={{ marginTop: 3, fontSize: 10, color: '#34d399', fontFamily: 'monospace' }}>
                                  → {outputs.slice(0, 3).join(', ')}{outputs.length > 3 ? ` +${outputs.length - 3} more` : ''}
                                </div>
                              )
                            })()}
                          </div>
                          <div style={{ display: 'flex', gap: 4, flexShrink: 0 }}>
                            <button
                              style={{ fontSize: 13, padding: '2px 5px', background: 'none', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 4, cursor: 'pointer', color: 'var(--accent)' }}
                              title={`Edit "${sf.name}"`}
                              onClick={e => { e.stopPropagation(); onOpenFlow?.(sf.name) }}
                            >
                              ✎
                            </button>
                            {deleteConfirm === sf.name ? (
                              <DeleteImpactPanel
                                flowName={sf.name}
                                impact={impactMap?.get(sf.name)}
                                onConfirm={() => { onDeleteFlow?.(sf.name); setDeleteConfirm(null) }}
                                onCancel={() => setDeleteConfirm(null)}
                                onOpenFlow={onOpenFlow}
                                onNavigateToApis={onNavigateToApis}
                              />
                            ) : (
                              <button
                                style={{ fontSize: 12, padding: '2px 5px', background: 'none', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 4, cursor: 'pointer', color: 'var(--muted)' }}
                                title={`Delete "${sf.name}"`}
                                onClick={e => { e.stopPropagation(); setDeleteConfirm(sf.name) }}
                              >
                                🗑
                              </button>
                            )}
                          </div>
                        </div>
                      )
                    })
                  }
                </div>
              )}
            </div>

            {/* Step recipe groups */}
            {VISUAL_GROUPS.map(group => {
              const isSearching = filter.length > 0
              const matchingRecipes = group.recipes.filter(r =>
                !isSearching ||
                r.title.toLowerCase().includes(q) ||
                r.wraps.toLowerCase().includes(q) ||
                (r.description ?? '').toLowerCase().includes(q),
              )
              if (isSearching && matchingRecipes.length === 0) return null
              const isCollapsed = !isSearching && collapsedSections.has(group.label)
              return (
                <div key={group.label}>
                  <div
                    className="palette-section-label"
                    style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer', userSelect: 'none', marginTop: 8 }}
                    onClick={() => setCollapsedSections(prev => {
                      const s = new Set(prev)
                      s.has(group.label) ? s.delete(group.label) : s.add(group.label)
                      return s
                    })}
                  >
                    <span>{group.icon}</span>
                    <span style={{ flex: 1 }}>{group.label}</span>
                    <span style={{ fontSize: 10, opacity: 0.5 }}>{isCollapsed ? '▶' : '▼'}</span>
                  </div>
                  {!isCollapsed && matchingRecipes.map(recipe => {
                    const payload = recipeToBlock(recipe)
                    return (
                      <div
                        key={`${group.label}-${recipe.wraps}-${recipe.title}`}
                        className="block"
                        draggable
                        onDragStart={e => e.dataTransfer.setData('application/json', JSON.stringify(payload))}
                        onClick={() => clickAddBlock(payload)}
                        style={{ cursor: 'pointer' }}
                      >
                        <strong>{recipe.title}</strong>
                        <div className="sub" style={{ marginTop: 2, opacity: 0.75 }}>
                          {recipe.description}
                        </div>
                        <div className="sub" style={{ marginTop: 1, opacity: 0.4, fontSize: 10 }}>
                          {recipe.wraps}
                        </div>
                      </div>
                    )
                  })}
                </div>
              )
            })}

            {/* No matches message */}
            {filter.length > 0 &&
              savedFlows.every(sf => !sf.name.toLowerCase().includes(q)) &&
              VISUAL_GROUPS.every(g => g.recipes.every(r =>
                !r.title.toLowerCase().includes(q) &&
                !r.wraps.toLowerCase().includes(q) &&
                !(r.description ?? '').toLowerCase().includes(q),
              )) && (
                <span className="hint">No blocks match.</span>
              )}
          </div>
        </div>
      </div>

      {/* ── Canvas ──────────────────────────────────────────────── */}
      <div className="panel">
        <div className="panel-header" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <span>Flow Canvas</span>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            {selectMode && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <input
                  className="input"
                  placeholder="new sub-flow name"
                  value={extractName}
                  onChange={e => setExtractName(e.target.value)}
                  style={{ width: 160 }}
                />
                <button
                  className="btn accent"
                  style={{ fontSize: 12 }}
                  onClick={extractSubFlow}
                  disabled={selectedSteps.size === 0 || !extractName.trim()}
                >
                  Extract ({selectedSteps.size})
                </button>
                <button
                  className="btn muted"
                  style={{ fontSize: 12 }}
                  onClick={() => { setSelectMode(false); setSelectedSteps(new Set()); setExtractName('') }}
                >
                  Cancel
                </button>
              </div>
            )}
            {!selectMode && (
              <button
                className="btn muted"
                style={{ fontSize: 12 }}
                onClick={() => setSelectMode(true)}
                title="Select steps to extract as a sub-flow"
              >
                ⊡ Extract
              </button>
            )}
            <button
              className={`btn muted${showThisFlow ? ' active' : ''}`}
              style={{ fontSize: 12 }}
              onClick={() => setShowThisFlow(p => !p)}
              title="Show this flow's full hierarchy"
            >
              ⬡ This Flow
            </button>
          </div>
        </div>
        <div className="panel-body">
          {showThisFlow && (
            <div style={{ marginBottom: 12, borderRadius: 8, border: '1px solid var(--border)', background: 'rgba(255,255,255,0.015)', overflow: 'hidden' }}>
              {/* Panel header */}
              <div style={{ display:'flex', alignItems:'center', justifyContent:'space-between', padding:'7px 12px', borderBottom:'1px solid var(--border)' }}>
                <span style={{ fontWeight:700, fontSize:12 }}>
                  {flowName || '(untitled)'} &nbsp;
                  <span style={{ fontWeight:400, color:'var(--muted)', fontSize:11 }}>{steps.length} steps</span>
                </span>
                <div style={{ display:'flex', borderRadius:5, overflow:'hidden', border:'1px solid rgba(255,255,255,0.1)' }}>
                  {(['steps','tree','graph'] as const).map(t => (
                    <button key={t} onClick={() => setThisFlowTab(t)} style={{ fontSize:11, padding:'2px 9px', border:'none', cursor:'pointer', background: thisFlowTab===t ? 'var(--accent)' : 'transparent', color: thisFlowTab===t ? '#fff' : 'var(--muted)', textTransform:'capitalize' }}>
                      {t}
                    </button>
                  ))}
                </div>
              </div>
              {/* Panel body */}
              <div style={{ maxHeight: 320, overflowY: 'auto', padding: thisFlowTab === 'steps' ? 10 : 0 }}>
                {thisFlowTab === 'steps' && (
                  <>
                    <FlowStepList stepList={steps} depth={0} />
                    <button onClick={() => setExpandedCalls(new Set())} style={{ marginTop:8, fontSize:10, background:'none', border:'none', color:'var(--muted)', cursor:'pointer' }}>
                      collapse all
                    </button>
                  </>
                )}
                {thisFlowTab === 'tree' && flowName && (
                  <FlowMap
                    savedFlows={thisFlowEffective}
                    currentFlow={flowName}
                    onNavigate={name => onNavigateToFlow?.(name)}
                    focusFlow={flowName}
                  />
                )}
                {thisFlowTab === 'graph' && flowName && (
                  <FlowGraph
                    savedFlows={thisFlowEffective}
                    currentFlow={flowName}
                    onNavigate={name => onNavigateToFlow?.(name)}
                    focusFlow={flowName}
                  />
                )}
              </div>
            </div>
          )}
          <div style={{ display: 'flex', marginBottom: 6, borderRadius: 6, overflow: 'hidden', border: '1px solid rgba(255,255,255,0.12)', width: 'fit-content' }}>
            <button style={{ fontSize: 12, padding: '3px 14px', border: 'none', cursor: 'pointer', background: viewMode === 'visual' ? 'var(--accent)' : 'transparent', color: viewMode === 'visual' ? '#fff' : 'var(--muted)' }} onClick={() => setViewMode('visual')}>Visual</button>
            <button style={{ fontSize: 12, padding: '3px 14px', border: 'none', cursor: 'pointer', background: viewMode === 'code'   ? 'var(--accent)' : 'transparent', color: viewMode === 'code'   ? '#fff' : 'var(--muted)' }} onClick={switchToCode}>Code</button>
          </div>
          {navStack.length > 0 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 0 2px', fontSize: 12, flexWrap: 'wrap' }}>
              {navStack.map((name, idx) => (
                <span key={idx} style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                  <button
                    className="btn muted"
                    style={{ fontSize: 12, padding: '1px 6px', borderRadius: 4 }}
                    onClick={() => onNavigateBack?.(idx)}
                  >
                    {name}
                  </button>
                  <span style={{ color: 'var(--muted)', fontSize: 10 }}>›</span>
                </span>
              ))}
              <span style={{ fontSize: 12, color: 'var(--fg)', fontWeight: 600 }}>{flowName}</span>
            </div>
          )}
          {/* ── Impact warning: shown when this flow is referenced by others ── */}
          {(() => {
            const impact = flowName ? impactMap?.get(flowName) : undefined
            const hasImpact = impact && (impact.flows.length > 0 || impact.apis.length > 0)
            if (!hasImpact) return null
            return (
              <div style={{
                margin: '6px 0',
                padding: '8px 12px',
                borderRadius: 6,
                background: 'rgba(245,158,11,0.08)',
                border: '1px solid rgba(245,158,11,0.35)',
                fontSize: 12,
                lineHeight: 1.6,
              }}>
                <div style={{ fontWeight: 700, color: '#f59e0b', marginBottom: 4 }}>
                  ⚠ Shared subflow — changes affect all callers
                </div>
                {impact.flows.length > 0 && (
                  <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 4, marginBottom: impact.apis.length > 0 ? 4 : 0 }}>
                    <span style={{ color: 'var(--muted)' }}>Used by flows:</span>
                    {impact.flows.map(name => (
                      <button
                        key={name}
                        onClick={() => onNavigateToFlow?.(name)}
                        style={{
                          background: 'rgba(245,158,11,0.12)',
                          border: '1px solid rgba(245,158,11,0.4)',
                          borderRadius: 4,
                          color: '#fbbf24',
                          fontSize: 11,
                          padding: '1px 7px',
                          cursor: 'pointer',
                          fontWeight: 600,
                        }}
                        title={`Navigate to flow: ${name}`}
                      >
                        {name} ›
                      </button>
                    ))}
                  </div>
                )}
                {impact.apis.length > 0 && (
                  <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 4 }}>
                    <span style={{ color: 'var(--muted)' }}>Used by APIs:</span>
                    {impact.apis.map(name => (
                      <span
                        key={name}
                        style={{
                          background: 'rgba(245,158,11,0.12)',
                          border: '1px solid rgba(245,158,11,0.4)',
                          borderRadius: 4,
                          color: '#fbbf24',
                          fontSize: 11,
                          padding: '1px 7px',
                          fontWeight: 600,
                        }}
                      >
                        {name}
                      </span>
                    ))}
                    {onNavigateToApis && (
                      <button
                        onClick={onNavigateToApis}
                        style={{
                          background: 'none',
                          border: 'none',
                          color: 'var(--accent)',
                          fontSize: 11,
                          cursor: 'pointer',
                          padding: '1px 4px',
                          textDecoration: 'underline',
                        }}
                      >
                        Open APIs tab
                      </button>
                    )}
                  </div>
                )}
              </div>
            )
          })()}
          <input
            className="input"
            placeholder="flow name (required to save)"
            value={flowName}
            onChange={e => setFlowName(e.target.value)}
          />
          {insertCursor !== null && (
            <div style={{
              padding: '5px 10px',
              borderRadius: 5,
              background: 'rgba(87,181,255,0.1)',
              border: '1px solid rgba(87,181,255,0.3)',
              fontSize: 12,
              color: 'var(--accent)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              marginBottom: 6,
            }}>
              <span>Click a step in the palette to insert before step {insertCursor + 1}</span>
              <button
                onClick={() => setInsertCursor(null)}
                style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', fontSize: 12 }}
              >
                ✕ cancel
              </button>
            </div>
          )}
          {viewMode === 'code' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, flex: 1 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ fontSize: 12, color: 'var(--muted)', whiteSpace: 'nowrap' }}>Flow name</span>
                <input
                  value={flowName}
                  onChange={e => setFlowName(e.target.value)}
                  placeholder="my_flow_name"
                  style={{ flex: 1, fontSize: 13, fontFamily: 'monospace', padding: '4px 10px', background: 'rgba(0,0,0,0.28)', color: 'var(--fg)', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 6, outline: 'none' }}
                />
              </div>
              <textarea
                spellCheck={false}
                value={dslText}
                onChange={e => {
                  const text = e.target.value
                  setDslText(text)
                  setDslError(null)
                  // Code → Visual: parse and update steps live so visual canvas stays in sync
                  try {
                    const parsed = parseDSL(text)
                    dslChangeSource.current = 'code'
                    setSteps(parsed)
                  } catch (err) {
                    setDslError(err instanceof Error ? err.message : 'Parse error')
                  }
                }}
                style={{ flex: 1, minHeight: 440, width: '100%', resize: 'vertical', fontFamily: 'monospace', fontSize: 13, lineHeight: 1.65, padding: '12px 14px', background: 'rgba(0,0,0,0.28)', color: 'var(--fg)', border: `1px solid ${dslError ? '#ef4444' : 'rgba(255,255,255,0.12)'}`, borderRadius: 8, outline: 'none', boxSizing: 'border-box' }}
                placeholder={'// RAH Flow DSL\nprofileId = path("user_profile")\ntoken     = header("Authorization")\nvalidate_token(token)\noutput.body = "Hello {profileId}"\nreturn(200)'}
              />
              {dslError && <div style={{ fontSize: 12, color: '#ef4444', background: 'rgba(239,68,68,0.08)', padding: '5px 10px', borderRadius: 5 }}>{dslError}</div>}
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <button className="btn accent" style={{ fontSize: 13 }} onClick={applyDSL}>View Visual →</button>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>Canvas updates live as you type.</span>
              </div>
              {/* ── Quick Reference ── */}
              <div style={{ borderRadius: 8, border: '1px solid rgba(255,255,255,0.10)', overflow: 'hidden' }}>
                <button
                  onClick={() => setShowDslRef(p => !p)}
                  style={{ width: '100%', display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '7px 12px', background: 'rgba(255,255,255,0.04)', border: 'none', cursor: 'pointer', color: 'var(--fg)', fontSize: 12, fontWeight: 600 }}
                >
                  <span>📖 DSL Quick Reference</span>
                  <span style={{ fontSize: 10, color: 'var(--muted)' }}>{showDslRef ? '▲ hide' : '▼ show'}</span>
                </button>
                {showDslRef && (
                  <div style={{ padding: '10px 14px', fontSize: 12, lineHeight: 1.7, color: 'var(--fg)', display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 24px' }}>
                    {([
                      ['Extract from request', [
                        'token    = header("Authorization")',
                        'userId   = body("user.id")',
                        'mode     = query("mode")',
                        'id       = path("user_id")',
                        'ip       = client_ip()',
                        'corrId   = correlation_id()   // echoes X-Correlation-ID header, or generates UUID',
                        'txId     = transaction_id()   // gateway internal ID, unique per request',
                      ]],
                      ['Constants & templates', [
                        'label    = "Hello"',
                        'msg      = "Hi {userId}, welcome!"',
                        'lower    = to_lower(token)',
                        'len      = byte_length(token)',
                        'combined = concat(label, userId)',
                      ]],
                      ['Cache', [
                        'cached   = cache.get(token)',
                        'cache.set(token, result, ttl: 300)',
                        'shared   = shared_cache.get("cfg:v1")',
                        'shared_cache.set("cfg:v1", val, ttl: 3600)',
                      ]],
                      ['Registry (multi-tenant)', [
                        'registry.lookup(header("X-Tenant-ID"))',
                        'upstream = registry.url("primary")',
                        'apiKey   = registry.id("api_key")',
                        'tier     = registry.meta("tier")',
                      ]],
                      ['HTTP & upstream', [
                        'result = http.get("https://api.example.com/v1")',
                        'result = http.post(url: upstream, timeout: 5000)',
                        'set_upstream_header("X-User-ID", userId)',
                      ]],
                      ['Auth & secrets', [
                        'validate_token(token)',
                        'validate_token(token, checks: "signature,expiry",',
                        '  jwks_url: "https://idp/.well-known/jwks.json")',
                        'secret = load_secret("gsm://proj/secrets/key")',
                        'secret = load_secret("env:MY_API_KEY")',
                      ]],
                      ['Rate limiting', [
                        'rate_limit()',
                        'assign_quota(tier, groups: \'{"free":"1","pro":"2"}\')',
                        'rate_limit(groups: \'{"1":"free_rl","2":"pro_rl"}\')',
                      ]],
                      ['Output & return', [
                        'output.body   = result',
                        'output.body   = "Hello {userId}"',
                        'output.status = 200',
                        'output.header("X-ID") = corrId',
                        'return(200)',
                        'return(200, result)',
                        'return(401, "unauthorized")',
                        'fail(500, "upstream error")',
                      ]],
                      ['Conditionals', [
                        'if (cached) {',
                        '  output.body = cached',
                        '  return(200)',
                        '} else {',
                        '  result = http.get(url: upstream)',
                        '  return(200)',
                        '}',
                      ]],
                      ['Switch & loops', [
                        'switch (tier) {',
                        '  "free": call free_flow',
                        '  "pro":  call pro_flow',
                        '}',
                        'foreach (items as item) {',
                        '  call process_item_flow',
                        '}',
                        'call auth_validation',
                      ]],
                      ['Logging & observability', [
                        '# Write to access log',
                        'log("client_id", var.client_id)',
                        'log("tenant", var.tenant_alias)',
                        '',
                        '# Emit to ingest pipeline',
                        'emit_event(kind="custom", payload=var.data)',
                        'emit_event(kind="llm_request", deferred=true)',
                      ]],
                    ] as [string, string[]][]).map(([heading, lines]) => (
                      <div key={heading} style={{ marginBottom: 12 }}>
                        <div style={{ fontSize: 10, fontWeight: 700, color: 'var(--accent)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>{heading}</div>
                        <pre style={{ margin: 0, fontFamily: 'monospace', fontSize: 11, color: 'rgba(255,255,255,0.75)', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{lines.join('\n')}</pre>
                      </div>
                    ))}
                  </div>
                )}
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
            {steps.length === 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%' }}>
                <span className="hint">Drop instruction blocks here to build a flow</span>
                <div style={{ marginTop: 12, fontSize: 11, color: 'var(--muted)', lineHeight: 1.7, maxWidth: 280, textAlign: 'center' }}>
                  <strong style={{ color: 'var(--fg)' }}>Tip — Logging:</strong><br/>
                  Use <code style={{ background: 'rgba(255,255,255,0.08)', padding: '1px 4px', borderRadius: 3 }}>log</code> to write a value to the access log,
                  or <code style={{ background: 'rgba(255,255,255,0.08)', padding: '1px 4px', borderRadius: 3 }}>emit_event</code> to send structured events to the ingest pipeline.
                  Both are in the <strong>Obs</strong> palette tab.
                </div>
              </div>
            )}
            {steps.map((step, i) => {
              const defs = fieldMap[step.action] ?? {}
              const isExpanded = expanded.has(i)
              return (
                <Fragment key={i}>
                <InterStepDropZone idx={i} />
                <div className="step" style={{ position: 'relative' }}>
                  <div
                    onClick={e => { e.stopPropagation(); setInsertCursor(i) }}
                    title={`Insert new step before step ${i + 1}`}
                    style={{
                      position: 'absolute',
                      left: -18,
                      top: '50%',
                      transform: 'translateY(-50%)',
                      width: 14,
                      height: 14,
                      borderRadius: '50%',
                      border: `1px solid ${insertCursor === i ? 'var(--accent)' : 'var(--muted)'}`,
                      background: insertCursor === i ? 'var(--accent)' : 'transparent',
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      fontSize: 10,
                      color: insertCursor === i ? '#fff' : 'var(--muted)',
                      opacity: 0.7,
                    }}
                  >
                    +
                  </div>
                  <div className="step-header" onClick={() => toggleExpand(i)}>
                    {selectMode && (
                      <input
                        type="checkbox"
                        checked={selectedSteps.has(i)}
                        onChange={e => {
                          e.stopPropagation()
                          const next = new Set(selectedSteps)
                          if (e.target.checked) {
                            next.add(i)
                          } else {
                            next.delete(i)
                          }
                          setSelectedSteps(next)
                        }}
                        style={{ marginRight: 6, cursor: 'pointer' }}
                      />
                    )}
                    <span className="step-toggle">{isExpanded ? '▼' : '▶'}</span>
                    <strong className="step-title">{i + 1}. {step.action}</strong>
                    {step.action === 'api_rate_limits' && (
                      <span style={{
                        fontSize: 9, fontWeight: 700, letterSpacing: '0.06em',
                        padding: '2px 6px', borderRadius: 8, marginLeft: 4,
                        background: 'rgba(139,92,246,0.15)', border: '1px solid rgba(139,92,246,0.35)',
                        color: '#a78bfa', whiteSpace: 'nowrap',
                      }}>API POLICY</span>
                    )}
                    <button
                      className="btn muted step-remove"
                      title="Remove step"
                      onClick={e => { e.stopPropagation(); removeStep(i) }}
                    >×</button>
                  </div>
                  {isExpanded && (<>
                    {step.action === 'api_rate_limits'   ? (() => (
                       <div style={{ padding: '10px 12px' }}>
                         <div style={{ fontSize: 12, color: 'var(--muted)', lineHeight: 1.6, marginBottom: 10 }}>
                           Enforces the rate limits configured in the API definition at this position in the flow.
                           If this block is absent, limits are auto-injected at the start of the flow.
                         </div>
                         <div style={{
                           padding: '6px 10px', borderRadius: 5,
                           background: 'rgba(139,92,246,0.08)', border: '1px solid rgba(139,92,246,0.25)',
                           fontSize: 11, color: '#a78bfa', lineHeight: 1.5, marginBottom: 8,
                         }}>
                           📍 Position marker — no configuration needed here. Rate limit rules are defined in the API definition.
                         </div>
                         {onNavigateToApis && (
                           <button
                             className="btn muted"
                             style={{ fontSize: 11 }}
                             onClick={e => { e.stopPropagation(); onNavigateToApis() }}
                           >
                             Configure in API Definition →
                           </button>
                         )}
                       </div>
                     ))() :
                     step.action === 'if'                ? renderIfBody(step, i, defs)           :
                     step.action === 'switch'            ? renderSwitchBody(step, i, defs)       :
                     step.action === 'http_call'         ? renderHttpCallBody(step, i)            :
                     step.action === 'token_validation'  ? renderTokenValidationBody(step, (k, v) => updateStep(i, k, v), slotsUpTo(i), i) :
                     step.action === 'set_response_body' ? renderSetResponseBody(step, i)         :
                     step.action === 'append_message'    ? renderAppendMessageBody(step, i)       :
                     step.action === 'transform_messages'? renderTransformMessagesBody(step, i)   :
                     step.action === 'log_field'         ? (() => (
                       <div>
                         {renderGenericBody(step, i, defs)}
                         <div style={{
                           marginTop: 8, padding: '6px 10px', borderRadius: 5,
                           background: 'rgba(52,211,153,0.08)', border: '1px solid rgba(52,211,153,0.2)',
                           fontSize: 11, color: '#34d399', lineHeight: 1.5,
                         }}>
                           💡 Writes <strong>{String(step['key'] || 'field')}</strong> to the request access log.
                           Value is read from slot <code>{String(step['source'] || '—')}</code> after response completes.
                           View in access logs under the <strong>Extra</strong> field.
                         </div>
                       </div>
                     ))() :
                     step.action === 'emit_event'        ? (() => (
                       <div>
                         {renderGenericBody(step, i, defs)}
                         <div style={{
                           marginTop: 8, padding: '6px 10px', borderRadius: 5,
                           background: 'rgba(87,181,255,0.08)', border: '1px solid rgba(87,181,255,0.2)',
                           fontSize: 11, color: '#57b5ff', lineHeight: 1.5,
                         }}>
                           💡 Emits a structured event to the ingest pipeline (non-blocking).
                           Set <strong>deferred=true</strong> to emit after the response is sent.
                           Event kinds: <code>llm_request</code>, <code>llm_response</code>, <code>tool_call</code>, <code>cache_hit</code>, <code>custom</code>
                         </div>
                       </div>
                     ))() :
                     step.action === 'validate_pattern' ? renderTemplatePatternBody(step as TemplatePatternStep, i, 'validate_pattern') :
                     step.action === 'extract_pattern' ? renderTemplatePatternBody(step as TemplatePatternStep, i, 'extract_pattern') :
                     step.action === 'validate_route'  ? (() => (
                       <ValidateRouteBuilder
                         rules={(step['rules'] as unknown[] ?? []) as any}
                         defaultNext={(step['default_next'] as string) ?? ''}
                         stepNames={savedFlows.map(f => f.name)}
                         onChange={(rules, defaultNext) => {
                           updateStep(i, 'rules', JSON.stringify(rules) as unknown as string)
                           updateStep(i, 'default_next', defaultNext)
                         }}
                       />
                     ))() :
                     step.action === 'check_rate_limit_v2' ? (() => (
                       <div style={{ padding: '10px 12px' }}>
                         <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 4 }}>Rate Limit V2 Config</div>
                         <input
                           placeholder="config name (e.g. api_standard)"
                           value={(step.config ?? '') as string}
                           onChange={e => updateStep(i, 'config', e.target.value)}
                           style={{ width: '100%', fontSize: 12, padding: '4px 6px', borderRadius: 4, border: '1px solid var(--border)', background: 'var(--bg)', color: 'var(--text)', boxSizing: 'border-box' }}
                         />
                         <select
                           value={(step.count_by ?? 'tenant') as string}
                           onChange={e => updateStep(i, 'count_by', e.target.value)}
                           style={{ width: '100%', fontSize: 12, padding: '4px 6px', borderRadius: 4, border: '1px solid var(--border)', background: 'var(--bg)', color: 'var(--text)', marginTop: 4 }}
                         >
                           <option value="tenant">Count by tenant</option>
                           <option value="ip">Count by IP</option>
                           <option value="global">Count globally</option>
                           <option value="app">Count by app/caller</option>
                         </select>
                       </div>
                     ))() :
                     step.action === 'llm_call'          ? renderLlmCallBody(step, i)             :
                     ['cache_get','cache_put','cache_get_global','cache_put_global','cache_delete','cache_delete_global'].includes(step.action)
                                                         ? renderCacheBody(step, i)               :
                                                           renderGenericBody(step, i, defs)}
                    {renderObsFooter(step, i)}
                  </>)}
                </div>
                </Fragment>
              )
            })}
            <InterStepDropZone idx={steps.length} />
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
    <VarPopup />
    {/* ── Pattern Condition Builder modal ── */}
    {patternBuilderTarget !== null && (
      <PatternConditionBuilder
        condition={patternBuilderCondition}
        onChange={(cond) => {
          applyPatternCondition(cond)
          setPatternBuilderCondition(cond)
        }}
        onClose={() => {
          setPatternBuilderTarget(null)
          setPatternBuilderCondition(null)
        }}
      />
    )}
    </Fragment>
  )
}
