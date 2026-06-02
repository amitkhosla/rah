import { useState, useEffect } from 'react'
import { fetchGatewaySnapshot, importOpenAPI, listRateLimitConfigs, listRateLimitConfigsV2, syncFlows, upsertRateLimitConfig, upsertAPITool, fetchTargets } from '../api'
import type { ApiDef, EndpointDef, FlowStep, ImportedAPI, OpenAPIRuleConfig, SavedFlow, UpstreamUrlConfig, RateLimitVar, RateLimitCountBy, RateLimitConfigSource, RateLimitCountByV2, RateLimitConfigRef, APIRateLimitEntry, RLFixedWindow, RLDynamicMapping, RateLimitWarning, Target } from '../types'
import FlowSearchSelect from './FlowSearchSelect'

interface Props {
  flows: SavedFlow[]
  apis: ApiDef[]
  setApis: (apis: ApiDef[]) => void
  onCreateFlow: (name: string) => void
  onLoadFlow: (name: string, steps: FlowStep[]) => void
  onNavigateToDesigner: (flowName?: string) => void
  onNavigateToDeploy: () => void
}

// ── Zone system ──────────────────────────────────────────────────────────────

type Zone = 'security' | 'process' | 'upstream' | 'response' | 'post'

const ACTION_ZONE: Record<string, Zone> = {
  token_validation: 'security', check_rate_limit: 'security',
  load_credential: 'security', registry_lookup: 'security',
  load_identifier: 'security', load_service_url: 'security',
  http_call: 'upstream', llm_call: 'upstream', mcp_call_tool: 'upstream',
  execute_plan: 'upstream', vector_search: 'upstream', embed_text: 'upstream',
  semantic_cache_get: 'upstream', route_llm: 'upstream',
  set_response_body: 'response', return: 'response', format_response: 'response',
  respond: 'response', set_response_header: 'response', early_return: 'response',
  emit_event: 'post', save_history: 'post', append_message: 'post',
}

const ZONE_META: Record<Zone, { label: string; color: string; icon: string }> = {
  security: { label: 'SECURITY',      color: '#f97316', icon: '🔒' },
  process:  { label: 'PROCESS',       color: '#57b5ff', icon: '⚙'  },
  upstream: { label: 'UPSTREAM',      color: '#fbbf24', icon: '🔗' },
  response: { label: 'RESPONSE',      color: '#34d399', icon: '📤' },
  post:     { label: 'POST-RESPONSE', color: '#a78bfa', icon: '📊' },
}

function stepZone(action: string): Zone {
  return ACTION_ZONE[action] ?? 'process'
}

// ── Method badge colors ──────────────────────────────────────────────────────

const METHOD_COLOR: Record<string, string> = {
  GET:    '#22c55e',
  POST:   '#3b82f6',
  PUT:    '#f97316',
  PATCH:  '#eab308',
  DELETE: '#ef4444',
}

// ── Name auto-suggest ────────────────────────────────────────────────────────

function suggestName(path: string): string {
  return path
    .replace(/^\/+/, '')
    .replace(/[/:.-]/g, '_')
    .replace(/_+/g, '_')
    .replace(/^_|_$/g, '')
}

// ── Toolify: auto-generate MCP tool name ────────────────────────────────────

function makeToolName(apiName: string, subPath: string, method: string): string {
  const parts = [apiName, subPath, method].join('_')
  return parts.toLowerCase().replace(/[^a-z0-9_]/g, '_').replace(/_+/g, '_').replace(/^_|_$/g, '')
}

// ── Toolify state type ───────────────────────────────────────────────────────

type ToolifyResult = { ok: boolean; error?: string }

type ToolifyState = {
  open: boolean
  api: ApiDef | null
  targets: Target[]
  selectedTarget: string
  checked: Record<string, boolean>
  toolNames: Record<string, string>
  descriptions: Record<string, string>
  schemas: Record<string, string>
  submitting: boolean
  progress: string
  results: Record<string, ToolifyResult>
}

function blankToolify(): ToolifyState {
  return {
    open: false, api: null, targets: [], selectedTarget: '',
    checked: {}, toolNames: {}, descriptions: {}, schemas: {},
    submitting: false, progress: '', results: {},
  }
}

// ── Full path computation ─────────────────────────────────────────────────────

function fullPath(basePath: string, subPath: string): string {
  const base = basePath.replace(/\/+$/, '')
  const sub  = subPath.startsWith('/') ? subPath : '/' + subPath
  return sub === '/' ? base || '/' : base + sub
}

// ── Resolve flow for endpoint ─────────────────────────────────────────────────

function resolveFlow(ep: EndpointDef, api: ApiDef): string {
  return ep.flowName ?? api.defaultFlow
}

// ── API interface derivation ─────────────────────────────────────────────────

interface InputBinding  { source: string; field: string; variable: string }
interface OutputBinding { action: string; value: string }

function deriveApiInterface(steps: FlowStep[]): { inputs: InputBinding[]; outputs: OutputBinding[] } {
  const inputs:  InputBinding[]  = []
  const outputs: OutputBinding[] = []

  for (const step of steps) {
    if (step.action === 'bind_header') {
      inputs.push({ source: 'header', field: (step['key'] as string) ?? '', variable: (step['as'] as string) ?? '' })
    } else if (step.action === 'bind_query_param') {
      inputs.push({ source: 'query',  field: (step['key'] as string) ?? '', variable: (step['as'] as string) ?? '' })
    } else if (step.action === 'bind_body') {
      inputs.push({ source: 'body',   field: (step['key'] as string) ?? '', variable: (step['as'] as string) ?? '' })
    } else if (step.action === 'set_response_body') {
      outputs.push({ action: 'response body', value: (step['source'] as string) ?? '' })
    } else if (step.action === 'return') {
      const v = (step['body'] as string) || (step['as'] as string) || ''
      if (v) outputs.push({ action: 'return', value: v })
    } else if (step.action === 'set_response_header') {
      outputs.push({ action: `header ${(step['key'] as string) ?? ''}`, value: (step['source'] as string) ?? '' })
    }
  }

  return { inputs, outputs }
}

// ── Main component ───────────────────────────────────────────────────────────

export default function APIsSection({ flows, apis, setApis, onCreateFlow, onLoadFlow, onNavigateToDesigner, onNavigateToDeploy }: Props) {
  // Selection state
  const [selectedApiId,      setSelectedApiId]      = useState<string | null>(null)
  const [selectedEndpointId, setSelectedEndpointId] = useState<string | null>(null)
  const [showWizard,         setShowWizard]          = useState(false)

  // Wizard state
  const [wizardStep,   setWizardStep]   = useState<1 | 2 | 3>(1)
  const [wizardApiId,  setWizardApiId]  = useState<string | null>(null) // null = new API
  // Step 1
  const [wBasePaths,   setWBasePaths]   = useState<string[]>(['']) // [primary, ...aliases]
  const [wApiName,     setWApiName]     = useState('')
  const [wDefaultFlow, setWDefaultFlow] = useState('')
  const [wFlowMode,    setWFlowMode]    = useState<'create' | 'existing'>('create')
  // Step 2
  const [wEndpoints,   setWEndpoints]   = useState<Array<{ id: string; subPath: string; method: string; flowName?: string; overrideFlow: boolean }>>([])
  const [wizardErr,    setWizardErr]    = useState('')

  // Rate limit configs
  const [rateLimitConfigs, setRateLimitConfigs] = useState<string[]>([])
  const [rateLimitError, setRateLimitError] = useState(false)
  const [syncStatus, setSyncStatus] = useState<'idle'|'syncing'|'done'|'error'>('idle')
  const [rateLimitConfigsV2, setRateLimitConfigsV2] = useState<string[]>([])
  // Warnings from last sync, keyed by API name
  const [rlWarningsMap, setRlWarningsMap] = useState<Record<string, RateLimitWarning[]>>({})

  // OpenAPI import state
  const [showOpenAPIImport,   setShowOpenAPIImport]   = useState(false)
  const [openAPISpec,         setOpenAPISpec]         = useState('')
  const [openAPIImporting,    setOpenAPIImporting]    = useState(false)
  const [openAPIImportErr,    setOpenAPIImportErr]    = useState('')
  const [openAPIResults,      setOpenAPIResults]      = useState<ImportedAPI[]>([])
  // Track which imported APIs have "add validation step" checked
  const [openAPIAddValidation, setOpenAPIAddValidation] = useState<Record<string, boolean>>({})

  // Toolify modal state
  const [toolify, setToolify] = useState<ToolifyState>(blankToolify())

  useEffect(() => {
    listRateLimitConfigs()
      .then(r => { setRateLimitConfigs(r.items.map(c => c.name)); setRateLimitError(false) })
      .catch(() => setRateLimitError(true))
    listRateLimitConfigsV2()
      .then(r => setRateLimitConfigsV2(r.items.map(c => c.name)))
      .catch(() => { /* silently ignore */ })
  }, [])

  // Sync state
  const [syncing, setSyncing] = useState(true)

  // On mount: fetch gateway snapshot and seed local API list
  useEffect(() => {
    fetchGatewaySnapshot()
      .then(state => {
        if (state?.apis?.length) {
          const grouped = new Map<string, ApiDef>()
          for (const ga of state.apis) {
            const method = ga.method ?? 'POST'
            const gaAny = ga as any
            const eps: EndpointDef[] = ga.endpoint_configs?.length
              ? ga.endpoint_configs.map(ec => {
                  const ecAny = ec as any
                  // Migrate legacy rate limit fields to new RateLimitConfigSource
                  const epRlConfig: RateLimitConfigSource | undefined = ec.rate_limit
                    ? { kind: 'named', name: ec.rate_limit }
                    : ecAny.rate_limit_var
                      ? { kind: 'dynamic', source: (ecAny.rate_limit_var as RateLimitVar).source, key: (ecAny.rate_limit_var as RateLimitVar).key }
                      : undefined
                  // Restore Dimension A from rate_limit_mode
                  const epRlCountBy: RateLimitCountBy | undefined = ecAny.rate_limit_mode === 'global'
                    ? { kind: 'global' }
                    : undefined
                  return {
                    id: crypto.randomUUID(),
                    subPath: ec.path || '/',
                    method: ec.method || method,
                    ...(ec.flow_name ? { flowName: ec.flow_name } : {}),
                    ...(epRlConfig ? { rateLimitConfig: epRlConfig } : {}),
                    ...(epRlCountBy ? { rateLimitCountBy: epRlCountBy } : {}),
                    ...(ecAny.constants ? { constants: ecAny.constants as Record<string,string> } : {}),
                    ...(ecAny.upstream_url ? { upstreamUrl: ecAny.upstream_url as UpstreamUrlConfig } : {}),
                  }
                })
              : [{ id: crypto.randomUUID(), subPath: '/', method }]

            if (!grouped.has(ga.path)) {
              // Migrate legacy rate limit fields to new RateLimitConfigSource
              const apiRlConfig: RateLimitConfigSource | undefined = ga.rate_limit
                ? { kind: 'named', name: ga.rate_limit }
                : gaAny.rate_limit_var
                  ? { kind: 'dynamic', source: (gaAny.rate_limit_var as RateLimitVar).source, key: (gaAny.rate_limit_var as RateLimitVar).key }
                  : undefined
              // Restore Dimension A from rate_limit_mode
              const apiRlCountBy: RateLimitCountBy | undefined = gaAny.rate_limit_mode === 'global'
                ? { kind: 'global' }
                : undefined
              grouped.set(ga.path, {
                id: crypto.randomUUID(),
                name: ga.name,
                basePath: ga.path,
                defaultFlow: ga.flow_name ?? '',
                endpoints: eps,
                ...(apiRlConfig ? { rateLimitConfig: apiRlConfig } : {}),
                ...(apiRlCountBy ? { rateLimitCountBy: apiRlCountBy } : {}),
                ...(ga.alias_paths?.length ? { aliasPaths: ga.alias_paths } : {}),
                ...(gaAny.constants ? { constants: gaAny.constants as Record<string,string> } : {}),
                ...(gaAny.upstream_url ? { upstreamUrl: gaAny.upstream_url as UpstreamUrlConfig } : {}),
              })
            }
          }
          const incoming = Array.from(grouped.values()).filter(
            g => !apis.find(a => a.basePath === g.basePath)
          )
          if (incoming.length > 0) setApis([...apis, ...incoming])
        }
        for (const gf of (state?.flows ?? [])) {
          onLoadFlow(gf.name, gf.instructions as FlowStep[])
        }
        setSyncing(false)
      })
      .catch(() => setSyncing(false))
  }, [])

  // ── Derived ────────────────────────────────────────────────────────────────

  const flowNames = flows.map(f => f.name)

  function endpointStatus(ep: EndpointDef, api: ApiDef): 'ready' | 'empty' | 'unlinked' {
    const fn = resolveFlow(ep, api)
    if (!fn || !flowNames.includes(fn)) return 'unlinked'
    const flow = flows.find(f => f.name === fn)
    if (!flow || flow.steps.length === 0) return 'empty'
    return 'ready'
  }

  const totalEndpoints = apis.reduce((sum, a) => sum + a.endpoints.length, 0)

  // ── OpenAPI import helpers ────────────────────────────────────────────────

  function openOpenAPIImport() {
    setShowOpenAPIImport(true)
    setOpenAPISpec('')
    setOpenAPIImportErr('')
    setOpenAPIResults([])
    setOpenAPIAddValidation({})
  }

  function closeOpenAPIImport() {
    setShowOpenAPIImport(false)
  }

  // ── Toolify handlers ───────────────────────────────────────────────────────

  async function openToolify(api: ApiDef) {
    // Initialise state immediately so modal opens with a loading indicator
    const checked: Record<string, boolean> = {}
    const toolNames: Record<string, string> = {}
    const descriptions: Record<string, string> = {}
    const schemas: Record<string, string> = {}
    for (const ep of api.endpoints) {
      checked[ep.id] = true
      toolNames[ep.id] = makeToolName(api.name, ep.subPath, ep.method)
      descriptions[ep.id] = ''
      schemas[ep.id] = ''
    }
    setToolify({
      open: true, api, targets: [], selectedTarget: '',
      checked, toolNames, descriptions, schemas,
      submitting: false, progress: '', results: {},
    })
    // Fetch targets
    try {
      const res = await fetchTargets()
      const tgts = res.targets ?? []
      setToolify(prev => ({
        ...prev,
        targets: tgts,
        selectedTarget: tgts.length > 0 ? tgts[0].name : '',
      }))
    } catch {
      // targets will stay empty — user will see empty dropdown
    }
  }

  function closeToolify() {
    setToolify(blankToolify())
  }

  async function submitToolify() {
    const { api, targets, selectedTarget, checked, toolNames, descriptions, schemas } = toolify
    if (!api) return
    const target = targets.find(t => t.name === selectedTarget)
    const targetBaseURL = target?.urls?.[0] ?? ''
    const endpoints = api.endpoints.filter(ep => checked[ep.id])
    if (endpoints.length === 0) return

    setToolify(prev => ({ ...prev, submitting: true, results: {} }))

    const results: Record<string, ToolifyResult> = {}
    for (let i = 0; i < endpoints.length; i++) {
      const ep = endpoints[i]
      setToolify(prev => ({ ...prev, progress: `Registering ${i + 1} of ${endpoints.length}…` }))

      const schemaStr = schemas[ep.id]?.trim()
      let inputSchema: Record<string, unknown> | undefined
      if (schemaStr) {
        try {
          inputSchema = JSON.parse(schemaStr)
        } catch {
          results[ep.id] = { ok: false, error: 'Invalid JSON schema' }
          continue
        }
      }

      try {
        await upsertAPITool({
          name: toolNames[ep.id],
          description: descriptions[ep.id],
          path: fullPath(api.basePath, ep.subPath),
          method: ep.method,
          ...(inputSchema ? { input_schema: inputSchema } : {}),
          ...(targetBaseURL ? { auth_kind: 'none' } : {}),
        })
        results[ep.id] = { ok: true }
      } catch (e: any) {
        results[ep.id] = { ok: false, error: e?.message ?? 'Failed' }
      }
    }

    setToolify(prev => ({ ...prev, submitting: false, progress: '', results }))
  }

  async function runOpenAPIImport() {
    const spec = openAPISpec.trim()
    if (!spec) { setOpenAPIImportErr('Paste an OpenAPI spec first'); return }
    setOpenAPIImporting(true)
    setOpenAPIImportErr('')
    setOpenAPIResults([])
    setOpenAPIAddValidation({})
    try {
      const res = await importOpenAPI(spec)
      setOpenAPIResults(res.apis)
      // Pre-check "add validation" for any APIs that have validation rules
      const checks: Record<string, boolean> = {}
      for (const api of res.apis) {
        if (api.validateRouteRules && api.validateRouteRules.length > 0) {
          checks[api.name] = true
        }
      }
      setOpenAPIAddValidation(checks)
    } catch (e: any) {
      setOpenAPIImportErr(e?.message ?? 'Import failed')
    } finally {
      setOpenAPIImporting(false)
    }
  }

  function applyOpenAPIImport() {
    if (openAPIResults.length === 0) return
    const newApis: ApiDef[] = []
    const newFlows: Array<{ name: string; steps: FlowStep[] }> = []

    for (const api of openAPIResults) {
      const flowName = api.name + '_flow'
      const steps: FlowStep[] = []

      // If "add validation step" is checked and there are rules, prepend a validate_route step
      if (openAPIAddValidation[api.name] && api.validateRouteRules && api.validateRouteRules.length > 0) {
        // Convert openAPIRuleConfig → the shape validate_route expects
        const rules: OpenAPIRuleConfig[] = api.validateRouteRules
        steps.push({
          action: 'validate_route',
          rules: rules as any,
        } as FlowStep)
      }

      newFlows.push({ name: flowName, steps })

      newApis.push({
        id: crypto.randomUUID(),
        name: api.name,
        basePath: api.path,
        aliasPaths: [],
        defaultFlow: flowName,
        endpoints: [{ id: crypto.randomUUID(), subPath: '/', method: api.method }],
      })
    }

    // Add APIs to state
    setApis([...apis, ...newApis])

    // Create flows via callback (one per imported API)
    for (const f of newFlows) {
      onCreateFlow(f.name)
      // If there are steps (e.g. validate_route), load them into the flow
      if (f.steps.length > 0) {
        onLoadFlow(f.name, f.steps)
      }
    }

    setShowOpenAPIImport(false)
    if (newApis.length > 0) setSelectedApiId(newApis[0].id)
  }

  // ── Wizard helpers ─────────────────────────────────────────────────────────

  function openNewWizard() {
    setShowWizard(true)
    setSelectedApiId(null)
    setSelectedEndpointId(null)
    setWizardApiId(null)
    setWizardStep(1)
    setWBasePaths([''])
    setWApiName('')
    setWDefaultFlow('')
    setWFlowMode('create')
    setWEndpoints([{ id: crypto.randomUUID(), subPath: '/', method: 'GET', overrideFlow: false }])
    setWizardErr('')
  }

  function openAddEndpointWizard(api: ApiDef) {
    setShowWizard(true)
    setSelectedApiId(null)
    setSelectedEndpointId(null)
    setWizardApiId(api.id)
    setWizardStep(2)
    setWBasePaths([api.basePath, ...(api.aliasPaths ?? [])])
    setWApiName(api.name)
    setWDefaultFlow(api.defaultFlow)
    setWFlowMode('existing')
    setWEndpoints([{ id: crypto.randomUUID(), subPath: '/', method: 'GET', overrideFlow: false }])
    setWizardErr('')
  }

  function wizardStep1Next() {
    setWizardErr('')
    if (wBasePaths.some(p => !p.trim())) { setWizardErr('All basepaths must be non-empty'); return }
    if (wBasePaths.some(p => !p.trim().startsWith('/'))) { setWizardErr('All basepaths must start with /'); return }
    if (wBasePaths.length !== new Set(wBasePaths.map(p => p.trim())).size) {
      setWizardErr('Basepaths must be unique'); return
    }
    if (!wApiName.trim()) { setWizardErr('Name must not be empty'); return }
    if (wizardApiId === null && apis.some(a => a.name === wApiName.trim())) {
      setWizardErr('Name already exists'); return
    }
    if (wFlowMode === 'existing' && !wDefaultFlow) {
      setWizardErr('Please select an existing flow'); return
    }
    if (wFlowMode === 'create' && !wDefaultFlow.trim()) {
      setWizardErr('Please enter a name for the new flow'); return
    }
    setWizardStep(2)
  }

  function wizardStep2Next() {
    setWizardErr('')
    if (wEndpoints.length === 0) { setWizardErr('Add at least one endpoint'); return }
    for (const ep of wEndpoints) {
      if (!ep.subPath.startsWith('/')) { setWizardErr('Sub-paths must start with /'); return }
      if (!ep.method) { setWizardErr('Each endpoint needs a method'); return }
      if (ep.overrideFlow && !ep.flowName?.trim()) {
        setWizardErr('Each overridden endpoint needs a flow selected'); return
      }
    }
    setWizardStep(3)
  }

  function wizardConfirm() {
    const newEndpoints: EndpointDef[] = wEndpoints.map(e => ({
      id: e.id,
      subPath: e.subPath,
      method: e.method,
      flowName: e.overrideFlow && e.flowName ? e.flowName : undefined,
    }))

    if (wizardApiId === null) {
      // Create new API
      const newApi: ApiDef = {
        id: crypto.randomUUID(),
        name: wApiName.trim(),
        basePath: wBasePaths[0].trim(),
        aliasPaths: wBasePaths.slice(1).map(p => p.trim()).filter(Boolean),
        defaultFlow: wFlowMode === 'existing' ? wDefaultFlow : wDefaultFlow.trim(),
        endpoints: newEndpoints,
      }
      setApis([...apis, newApi])
      if (wFlowMode === 'create') onCreateFlow(newApi.defaultFlow)
      setShowWizard(false)
      setSelectedApiId(newApi.id)
    } else {
      // Add endpoints to existing API
      const updated = apis.map(a =>
        a.id === wizardApiId
          ? { ...a, endpoints: [...a.endpoints, ...newEndpoints] }
          : a
      )
      setApis(updated)
      setShowWizard(false)
      setSelectedApiId(wizardApiId)
    }
  }

  function cancelWizard() {
    setShowWizard(false)
  }

  // ── Remove handlers ────────────────────────────────────────────────────────

  function handleRemoveApi(apiId: string) {
    setApis(apis.filter(a => a.id !== apiId))
    if (selectedApiId === apiId) { setSelectedApiId(null); setSelectedEndpointId(null) }
  }

  function handleRemoveEndpoint(apiId: string, endpointId: string) {
    const api = apis.find(a => a.id === apiId)
    if (!api) return
    if (api.endpoints.length <= 1) {
      // Remove entire API
      handleRemoveApi(apiId)
    } else {
      const updated = apis.map(a =>
        a.id === apiId
          ? { ...a, endpoints: a.endpoints.filter(e => e.id !== endpointId) }
          : a
      )
      setApis(updated)
      if (selectedEndpointId === endpointId) setSelectedEndpointId(null)
    }
  }

  function handleUpdateApiDefaultFlow(apiId: string, flowName: string) {
    setApis(apis.map(a => a.id === apiId ? { ...a, defaultFlow: flowName } : a))
  }

  function handleRemoveAlias(apiId: string, index: number) {
    setApis(apis.map(a => a.id === apiId
      ? { ...a, aliasPaths: (a.aliasPaths ?? []).filter((_, j) => j !== index) }
      : a))
  }

  function handleSetEndpointFlow(apiId: string, endpointId: string, flowName: string | undefined) {
    setApis(apis.map(a =>
      a.id === apiId
        ? { ...a, endpoints: a.endpoints.map(e => e.id === endpointId ? { ...e, flowName } : e) }
        : a
    ))
  }

  function updateApi(id: string, patch: Partial<ApiDef>) {
    setApis(apis.map(a => a.id === id ? { ...a, ...patch } : a))
  }

  function handleUpdateApiConstants(apiId: string, c: Record<string, string>) {
    setApis(apis.map(a => a.id === apiId ? { ...a, constants: c } : a))
  }

  function handleUpdateEndpointConstants(apiId: string, epId: string, c: Record<string, string>) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a,
      endpoints: a.endpoints.map(e => e.id === epId ? { ...e, constants: c } : e),
    }))
  }

  function handleUpdateApiUpstreamUrl(apiId: string, u: UpstreamUrlConfig | undefined) {
    setApis(apis.map(a => a.id === apiId ? { ...a, upstreamUrl: u } : a))
  }

  function handleUpdateEndpointUpstreamUrl(apiId: string, epId: string, u: UpstreamUrlConfig | undefined) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a,
      endpoints: a.endpoints.map(e => e.id === epId ? { ...e, upstreamUrl: u } : e),
    }))
  }

  function handleUpdateApiRateLimitCountBy(apiId: string, v: RateLimitCountBy | undefined) {
    setApis(apis.map(a => a.id === apiId ? { ...a, rateLimitCountBy: v } : a))
  }

  function handleUpdateApiRateLimitConfig(apiId: string, v: RateLimitConfigSource | undefined) {
    setApis(apis.map(a => a.id === apiId ? { ...a, rateLimitConfig: v } : a))
  }

  function handleUpdateEndpointRateLimitCountBy(apiId: string, epId: string, v: RateLimitCountBy | undefined) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a,
      endpoints: a.endpoints.map(e => e.id === epId ? { ...e, rateLimitCountBy: v } : e),
    }))
  }

  function handleUpdateEndpointRateLimitConfig(apiId: string, epId: string, v: RateLimitConfigSource | undefined) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a,
      endpoints: a.endpoints.map(e => e.id === epId ? { ...e, rateLimitConfig: v } : e),
    }))
  }

  function handleUpdateApiRlCountBy(apiId: string, v: RateLimitCountByV2 | undefined) {
    setApis(apis.map(a => a.id === apiId ? { ...a, rlCountBy: v } : a))
  }
  function handleUpdateApiRlConfigRef(apiId: string, v: RateLimitConfigRef | undefined) {
    setApis(apis.map(a => a.id === apiId ? { ...a, rlConfigRef: v } : a))
  }
  function handleUpdateApiUpstreamService(apiId: string, v: string | undefined) {
    setApis(apis.map(a => a.id === apiId ? { ...a, upstreamService: v || undefined } : a))
  }
  function handleUpdateEndpointRlCountBy(apiId: string, epId: string, v: RateLimitCountByV2 | undefined) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a, endpoints: a.endpoints.map(e => e.id === epId ? { ...e, rlCountBy: v } : e),
    }))
  }
  function handleUpdateEndpointRlConfigRef(apiId: string, epId: string, v: RateLimitConfigRef | undefined) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a, endpoints: a.endpoints.map(e => e.id === epId ? { ...e, rlConfigRef: v } : e),
    }))
  }
  function handleUpdateEndpointUpstreamService(apiId: string, epId: string, v: string | undefined) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a, endpoints: a.endpoints.map(e => e.id === epId ? { ...e, upstreamService: v || undefined } : e),
    }))
  }
  function handleUpdateApiRateLimitPolicies(apiId: string, v: APIRateLimitEntry[]) {
    setApis(apis.map(a => a.id === apiId ? { ...a, rateLimitPolicies: v } : a))
  }
  function handleUpdateApiSkipRateLimit(apiId: string, v: boolean) {
    setApis(apis.map(a => a.id === apiId ? { ...a, skipRateLimit: v } : a))
  }
  function handleUpdateEndpointRateLimitPolicies(apiId: string, epId: string, v: APIRateLimitEntry[]) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a, endpoints: a.endpoints.map(e => e.id === epId ? { ...e, rateLimitPolicies: v } : e),
    }))
  }
  function handleUpdateEndpointSkipRateLimit(apiId: string, epId: string, v: boolean) {
    setApis(apis.map(a => a.id !== apiId ? a : {
      ...a, endpoints: a.endpoints.map(e => e.id === epId ? { ...e, skipRateLimit: v } : e),
    }))
  }

  async function handleCreateRateLimit(name: string, perSec: number, perMin: number, burst: number) {
    await upsertRateLimitConfig({ name, per_sec: perSec, per_min: perMin, burst_factor: burst })
    const r = await listRateLimitConfigs()
    setRateLimitConfigs(r.items.map(c => c.name))
    setRateLimitError(false)
  }

  function buildUpstreamPrependStep(cfg: UpstreamUrlConfig): FlowStep | null {
    if (!cfg.value || cfg.source === 'static') return null
    const actionMap: Record<string, string> = {
      registry:   'load_service_url',
      cache:      'cache_get',
      header:     'bind_header',
      queryparam: 'bind_query_param',
    }
    const action = actionMap[cfg.source]
    if (!action) return null
    return { action, key: cfg.value, as: 'upstream_url' }
  }

  async function syncThisApi(api: ApiDef) {
    setSyncStatus('syncing')
    const flowNamesSet = new Set<string>([
      api.defaultFlow,
      ...api.endpoints.map(e => e.flowName).filter((f): f is string => !!f),
    ])
    const flowsPayload = [...flowNamesSet].map(name => {
      const saved = flows.find(f => f.name === name)
      return { name, instructions: saved?.steps ?? [], action: 'upsert' as const }
    })

    // Build a map of flow names to dynamic upstream URL configs
    // Endpoint-level config takes precedence over API-level for the same flow
    const flowUpstreamMap: Record<string, UpstreamUrlConfig> = {}

    // First, set API-level upstream URL for default flow (if dynamic)
    if (api.upstreamUrl?.source !== 'static' && api.upstreamUrl?.value) {
      flowUpstreamMap[api.defaultFlow] = api.upstreamUrl
    }

    // Then, iterate endpoints and set/override with endpoint-level config
    for (const ep of api.endpoints) {
      const flowName = ep.flowName ?? api.defaultFlow
      if (ep.upstreamUrl?.source !== 'static' && ep.upstreamUrl?.value) {
        flowUpstreamMap[flowName] = ep.upstreamUrl
      }
    }

    // Build a map of flow names to xff index for bind_client_ip injection
    // Endpoint-level "Per IP" takes precedence over API-level for the same flow
    const flowBindIpMap: Record<string, number> = {}

    // First, set API-level: apply to default flow
    if (api.rateLimitCountBy?.kind === 'ip') {
      flowBindIpMap[api.defaultFlow] = api.rateLimitCountBy.xffIndex ?? 0
    }

    // Then, iterate endpoints — endpoint-level overrides API-level for the same flow
    for (const ep of api.endpoints) {
      if (ep.rateLimitCountBy?.kind === 'ip') {
        const flowName = ep.flowName ?? api.defaultFlow
        flowBindIpMap[flowName] = ep.rateLimitCountBy.xffIndex ?? 0
      }
    }

    // Prepend bind_client_ip steps (before upstream URL steps, so IP is available first)
    for (const payload of flowsPayload) {
      const xffIdx = flowBindIpMap[payload.name]
      if (xffIdx !== undefined) {
        const bindIpStep: FlowStep = {
          action: 'bind_client_ip',
          key_identifier: 'client_ip',
          input: { xff_index: String(xffIdx) },
        }
        payload.instructions = [bindIpStep, ...payload.instructions]
      }
    }

    // Prepend steps to flows that have dynamic upstream URLs
    for (const payload of flowsPayload) {
      const upstreamCfg = flowUpstreamMap[payload.name]
      if (upstreamCfg) {
        const prependStep = buildUpstreamPrependStep(upstreamCfg)
        if (prependStep) {
          payload.instructions = [prependStep, ...payload.instructions]
        }
      }
    }

    // For API-level upstream_url: translate static source to constant
    let apiConstants = { ...(api.constants ?? {}) }
    if (api.upstreamUrl?.source === 'static' && api.upstreamUrl.value) {
      apiConstants['upstream_url'] = api.upstreamUrl.value
    }

    // Translate Dimension B (RateLimitConfigSource) to SyncPayload fields
    function buildRlFields(cfg: typeof api.rateLimitConfig) {
      if (!cfg) return {}
      if (cfg.kind === 'named') return { rate_limit: cfg.name }
      return { rate_limit_var: { source: cfg.source, key: cfg.key } }
    }

    // Translate Dimension A (RateLimitCountBy) to rate_limit_mode field.
    // Only "global" emits a mode field; tenant/ip/slot are handled via flow injection
    // (bind_client_ip prepend) or left as default (tenant-scoped).
    function buildRlModeField(countBy: typeof api.rateLimitCountBy): { rate_limit_mode?: string } {
      if (countBy?.kind === 'global') return { rate_limit_mode: 'global' }
      return {}
    }

    const apisPayload = [{
      name: api.name,
      path: api.basePath,
      flow_name: api.defaultFlow,
      ...buildRlFields(api.rateLimitConfig),
      ...buildRlModeField(api.rateLimitCountBy),
      ...(api.rateLimitPolicies?.length ? { rate_limit_policies: api.rateLimitPolicies } : {}),
      ...(api.skipRateLimit ? { skip_rate_limit: true } : {}),
      ...(api.upstreamUrl ? { upstream_url: api.upstreamUrl } : {}),
      ...(api.aliasPaths?.length ? { alias_paths: api.aliasPaths } : {}),
      ...(Object.keys(apiConstants).length ? { constants: apiConstants } : {}),
      endpoint_configs: api.endpoints.map(ep => {
        // For endpoint-level upstream_url: translate static source to constant
        let epConstants = { ...(ep.constants ?? {}) }
        if (ep.upstreamUrl?.source === 'static' && ep.upstreamUrl.value) {
          epConstants['upstream_url'] = ep.upstreamUrl.value
        }

        return {
          path: ep.subPath,
          method: ep.method,
          ...(ep.flowName ? { flow_name: ep.flowName } : {}),
          ...buildRlFields(ep.rateLimitConfig),
          ...buildRlModeField(ep.rateLimitCountBy),
          ...(ep.rateLimitPolicies?.length ? { rate_limit_policies: ep.rateLimitPolicies } : {}),
          ...(ep.skipRateLimit ? { skip_rate_limit: true } : {}),
          ...(ep.upstreamUrl ? { upstream_url: ep.upstreamUrl } : {}),
          ...(Object.keys(epConstants).length ? { constants: epConstants } : {}),
        }
      }),
      action: 'upsert' as const,
    }]
    try {
      const result = await syncFlows({ sync_uuid: crypto.randomUUID(), flows: flowsPayload, apis: apisPayload })
      const warnings = result.rate_limit_warnings ?? []
      setRlWarningsMap(prev => ({ ...prev, [api.name]: warnings }))
      setSyncStatus('done')
    } catch {
      setSyncStatus('error')
    }
    setTimeout(() => setSyncStatus('idle'), 3000)
  }

  // ── Selection ──────────────────────────────────────────────────────────────

  const selectedApi = selectedApiId ? apis.find(a => a.id === selectedApiId) ?? null : null
  const selectedEndpoint = selectedApi && selectedEndpointId
    ? selectedApi.endpoints.find(e => e.id === selectedEndpointId) ?? null
    : null

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <>
    <div style={{ display: 'flex', height: 'calc(100vh - 58px)', gap: 0, margin: -14 }}>

      {/* ── Left sidebar ──────────────────────────────────────────────── */}
      <div style={{
        width: 290, flexShrink: 0,
        borderRight: '1px solid var(--border)',
        display: 'flex', flexDirection: 'column',
        background: 'var(--panel)',
      }}>
        {/* Header */}
        <div style={{
          padding: '12px 14px',
          borderBottom: '1px solid var(--border)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <span style={{ fontSize: 13, fontWeight: 600 }}>API Catalog</span>
          <div style={{ display: 'flex', gap: 6 }}>
            <button
              className="btn muted"
              style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 11 }}
              title="Import APIs from an OpenAPI / Swagger spec"
              onClick={openOpenAPIImport}
            >
              Import OpenAPI
            </button>
            <button
              className="btn"
              style={{ width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12 }}
              onClick={openNewWizard}
            >
              + New API
            </button>
          </div>
        </div>

        {/* API accordion list */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '8px 8px 0' }}>
          {apis.length === 0 && !showWizard && (
            <div style={{ padding: '24px 16px', textAlign: 'center', color: 'var(--muted)' }}>
              {syncing
                ? <span style={{ fontSize: 12 }}>Checking gateway…</span>
                : (
                  <>
                    <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>No APIs yet</div>
                    <div style={{ fontSize: 11, marginBottom: 12 }}>
                      Create your first API to start routing traffic through flows.
                    </div>
                    <button className="btn" onClick={openNewWizard}>＋ Create API</button>
                  </>
                )
              }
            </div>
          )}

          {apis.map(api => {
            const isApiSelected = selectedApiId === api.id && !selectedEndpointId && !showWizard
            return (
              <div key={api.id} style={{ marginBottom: 6 }}>
                {/* Accordion header: basePath + API name */}
                <div
                  onClick={() => {
                    setSelectedApiId(api.id)
                    setSelectedEndpointId(null)
                    setShowWizard(false)
                  }}
                  style={{
                    padding: '7px 9px',
                    borderRadius: 7,
                    cursor: 'pointer',
                    background: isApiSelected ? 'rgba(87,181,255,0.1)' : 'rgba(255,255,255,0.03)',
                    border: isApiSelected ? '1px solid var(--accent)' : '1px solid var(--border)',
                    display: 'flex', alignItems: 'center', gap: 6,
                  }}
                >
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{
                      fontFamily: 'monospace', fontSize: 12, fontWeight: 600,
                      color: isApiSelected ? 'var(--accent)' : 'var(--text)',
                      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    }}>
                      {api.basePath}
                    </div>
                    <div style={{ fontSize: 10, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {api.name}
                    </div>
                  </div>
                  {/* Toolify button */}
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '1px 7px', marginTop: 0, fontSize: 11, flexShrink: 0, color: '#a78bfa' }}
                    title="Convert endpoints to MCP tools"
                    onClick={e => { e.stopPropagation(); openToolify(api) }}
                  >
                    MCP
                  </button>
                  {/* + button to add endpoint */}
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '1px 7px', marginTop: 0, fontSize: 12, flexShrink: 0 }}
                    title="Add endpoint"
                    onClick={e => { e.stopPropagation(); openAddEndpointWizard(api) }}
                  >
                    +
                  </button>
                </div>

                {/* Endpoint child rows */}
                {api.endpoints.map(ep => {
                  const isEpSelected = selectedEndpointId === ep.id && selectedApiId === api.id && !showWizard
                  const status = endpointStatus(ep, api)
                  const resolvedFn = resolveFlow(ep, api)
                  return (
                    <div
                      key={ep.id}
                      onClick={() => {
                        setSelectedApiId(api.id)
                        setSelectedEndpointId(ep.id)
                        setShowWizard(false)
                      }}
                      style={{
                        padding: '5px 9px 5px 20px',
                        borderRadius: 5,
                        cursor: 'pointer',
                        marginTop: 2,
                        background: isEpSelected ? 'rgba(87,181,255,0.08)' : 'transparent',
                        border: isEpSelected ? '1px solid rgba(87,181,255,0.4)' : '1px solid transparent',
                        display: 'flex', alignItems: 'center', gap: 6,
                      }}
                    >
                      {/* Status dot */}
                      <span style={{
                        width: 7, height: 7, borderRadius: '50%', flexShrink: 0,
                        background: status === 'ready' ? '#22c55e' : status === 'empty' ? '#f59e0b' : '#ef4444',
                        boxShadow: status === 'ready' ? '0 0 4px #22c55e66' : undefined,
                      }} title={status === 'ready' ? 'Ready' : status === 'empty' ? 'Flow has no steps' : 'Flow not found'} />

                      <MethodBadge method={ep.method} />

                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{
                          fontFamily: 'monospace', fontSize: 11,
                          color: isEpSelected ? 'var(--accent)' : 'var(--text)',
                          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                        }}>
                          {ep.subPath}
                        </div>
                        <div style={{
                          fontSize: 10, color: 'var(--muted)',
                          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                        }}>
                          {ep.flowName ? `★ ${ep.flowName}` : resolvedFn || '(no flow)'}
                        </div>
                      </div>
                    </div>
                  )
                })}
              </div>
            )
          })}
        </div>

        {/* Deploy shortcut at bottom */}
        {totalEndpoints > 0 && (
          <div style={{ padding: '10px 12px', borderTop: '1px solid var(--border)' }}>
            <button
              className="btn"
              style={{ width: '100%', fontSize: 12 }}
              onClick={onNavigateToDeploy}
            >
              → Publish Now ({totalEndpoints} endpoint{totalEndpoints !== 1 ? 's' : ''})
            </button>
          </div>
        )}
      </div>

      {/* ── Right panel ────────────────────────────────────────────────── */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflowY: 'auto', background: 'var(--bg)' }}>
        {showWizard ? (
          <WizardPanel
            apis={apis}
            flows={flows}
            wizardStep={wizardStep}
            wizardApiId={wizardApiId}
            wBasePaths={wBasePaths}
            wApiName={wApiName}
            wDefaultFlow={wDefaultFlow}
            wFlowMode={wFlowMode}
            wEndpoints={wEndpoints}
            wizardErr={wizardErr}
            setWBasePaths={setWBasePaths}
            onPrimaryBasePathChange={p => { if (!wizardApiId) setWApiName(suggestName(p)) }}
            setWApiName={setWApiName}
            setWDefaultFlow={setWDefaultFlow}
            setWFlowMode={setWFlowMode}
            setWEndpoints={setWEndpoints}
            onStep1Next={wizardStep1Next}
            onStep2Next={wizardStep2Next}
            onBack={() => setWizardStep(s => (s > 1 ? (s - 1) as 1 | 2 | 3 : s))}
            onConfirm={wizardConfirm}
            onCancel={cancelWizard}
          />
        ) : selectedEndpoint !== null && selectedApi !== null ? (
          <EndpointDetailPanel
            api={selectedApi}
            endpoint={selectedEndpoint}
            flows={flows}
            onRemove={() => handleRemoveEndpoint(selectedApi.id, selectedEndpoint.id)}
            onSetFlow={(flowName) => handleSetEndpointFlow(selectedApi.id, selectedEndpoint.id, flowName)}
            onClearOverride={() => handleSetEndpointFlow(selectedApi.id, selectedEndpoint.id, undefined)}
            onNavigateToDesigner={onNavigateToDesigner}
            onNavigateToDeploy={onNavigateToDeploy}
            rateLimitConfigs={rateLimitConfigs}
            rateLimitError={rateLimitError}
            rateLimitCountBy={selectedEndpoint.rateLimitCountBy}
            rateLimitConfig={selectedEndpoint.rateLimitConfig}
            onUpdateRateLimitCountBy={v => handleUpdateEndpointRateLimitCountBy(selectedApi.id, selectedEndpoint.id, v)}
            onUpdateRateLimitConfig={v => handleUpdateEndpointRateLimitConfig(selectedApi.id, selectedEndpoint.id, v)}
            onCreateRateLimit={handleCreateRateLimit}
            constants={selectedEndpoint.constants ?? {}}
            onUpdateConstants={c => handleUpdateEndpointConstants(selectedApi.id, selectedEndpoint.id, c)}
            upstreamUrl={selectedEndpoint.upstreamUrl}
            onUpdateUpstreamUrl={u => handleUpdateEndpointUpstreamUrl(selectedApi.id, selectedEndpoint.id, u)}
            rlCountBy={selectedEndpoint.rlCountBy}
            rlConfigRef={selectedEndpoint.rlConfigRef}
            upstreamService={selectedEndpoint.upstreamService}
            v2Configs={rateLimitConfigsV2}
            onUpdateRlCountBy={v => handleUpdateEndpointRlCountBy(selectedApi.id, selectedEndpoint.id, v)}
            onUpdateRlConfigRef={v => handleUpdateEndpointRlConfigRef(selectedApi.id, selectedEndpoint.id, v)}
            onUpdateUpstreamService={v => handleUpdateEndpointUpstreamService(selectedApi.id, selectedEndpoint.id, v)}
            rateLimitPolicies={selectedEndpoint.rateLimitPolicies ?? []}
            skipRateLimit={selectedEndpoint.skipRateLimit ?? false}
            onUpdateRateLimitPolicies={v => handleUpdateEndpointRateLimitPolicies(selectedApi.id, selectedEndpoint.id, v)}
            onUpdateSkipRateLimit={v => handleUpdateEndpointSkipRateLimit(selectedApi.id, selectedEndpoint.id, v)}
          />
        ) : selectedApi !== null ? (
          <ApiDetailPanel
            api={selectedApi}
            flows={flows}
            onRemove={() => handleRemoveApi(selectedApi.id)}
            onUpdateDefaultFlow={fn => handleUpdateApiDefaultFlow(selectedApi.id, fn)}
            onSelectEndpoint={epId => setSelectedEndpointId(epId)}
            onAddEndpoint={() => openAddEndpointWizard(selectedApi)}
            onNavigateToDesigner={onNavigateToDesigner}
            onRemoveAlias={i => handleRemoveAlias(selectedApi.id, i)}
            rateLimitConfigs={rateLimitConfigs}
            rateLimitError={rateLimitError}
            rateLimitCountBy={selectedApi.rateLimitCountBy}
            rateLimitConfig={selectedApi.rateLimitConfig}
            onUpdateRateLimitCountBy={v => handleUpdateApiRateLimitCountBy(selectedApi.id, v)}
            onUpdateRateLimitConfig={v => handleUpdateApiRateLimitConfig(selectedApi.id, v)}
            onCreateRateLimit={handleCreateRateLimit}
            onSync={() => syncThisApi(selectedApi)}
            syncStatus={syncStatus}
            constants={selectedApi.constants ?? {}}
            onUpdateConstants={c => handleUpdateApiConstants(selectedApi.id, c)}
            upstreamUrl={selectedApi.upstreamUrl}
            onUpdateUpstreamUrl={u => handleUpdateApiUpstreamUrl(selectedApi.id, u)}
            rlCountBy={selectedApi.rlCountBy}
            rlConfigRef={selectedApi.rlConfigRef}
            upstreamService={selectedApi.upstreamService}
            v2Configs={rateLimitConfigsV2}
            onUpdateRlCountBy={v => handleUpdateApiRlCountBy(selectedApi.id, v)}
            onUpdateRlConfigRef={v => handleUpdateApiRlConfigRef(selectedApi.id, v)}
            onUpdateUpstreamService={v => handleUpdateApiUpstreamService(selectedApi.id, v)}
            rateLimitPolicies={selectedApi.rateLimitPolicies ?? []}
            skipRateLimit={selectedApi.skipRateLimit ?? false}
            onUpdateRateLimitPolicies={v => handleUpdateApiRateLimitPolicies(selectedApi.id, v)}
            onUpdateSkipRateLimit={v => handleUpdateApiSkipRateLimit(selectedApi.id, v)}
            rlWarnings={rlWarningsMap[selectedApi.name] ?? []}
          />
        ) : (
          <EmptyRight onNewApi={openNewWizard} onNavigateToDesigner={onNavigateToDesigner} />
        )}
      </div>
    </div>

    {/* ── OpenAPI Import Modal ─────────────────────────────────────────── */}
    {showOpenAPIImport && (
      <div style={{
        position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        zIndex: 1000,
      }} onClick={e => { if (e.target === e.currentTarget) closeOpenAPIImport() }}>
        <div style={{
          background: 'var(--panel)', border: '1px solid var(--border)',
          borderRadius: 10, width: 680, maxHeight: '85vh',
          display: 'flex', flexDirection: 'column', overflow: 'hidden',
        }}>
          {/* Modal header */}
          <div style={{
            padding: '14px 18px', borderBottom: '1px solid var(--border)',
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          }}>
            <span style={{ fontSize: 14, fontWeight: 700 }}>Import from OpenAPI / Swagger</span>
            <button
              className="btn muted"
              style={{ width: 'auto', padding: '2px 10px', marginTop: 0, fontSize: 12 }}
              onClick={closeOpenAPIImport}
            >
              Close
            </button>
          </div>

          {/* Modal body */}
          <div style={{ flex: 1, overflowY: 'auto', padding: 18 }}>
            {openAPIResults.length === 0 ? (
              <>
                <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 8 }}>
                  Paste an OpenAPI 3.x or Swagger 2.x spec (JSON or YAML). The gateway will extract
                  paths, methods, and request body validation rules automatically.
                </div>
                <textarea
                  className="input"
                  style={{ height: 280, fontFamily: 'monospace', fontSize: 11, resize: 'vertical' }}
                  placeholder={'{\n  "openapi": "3.0.0",\n  "paths": { ... }\n}'}
                  value={openAPISpec}
                  onChange={e => setOpenAPISpec(e.target.value)}
                />
                {openAPIImportErr && (
                  <div style={{ color: '#f87171', fontSize: 12, marginTop: 6 }}>{openAPIImportErr}</div>
                )}
                <div style={{ marginTop: 12 }}>
                  <button
                    className="btn"
                    style={{ width: 'auto', padding: '6px 20px' }}
                    disabled={openAPIImporting}
                    onClick={runOpenAPIImport}
                  >
                    {openAPIImporting ? 'Parsing…' : 'Parse Spec'}
                  </button>
                </div>
              </>
            ) : (
              <>
                <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 12 }}>
                  Found <strong>{openAPIResults.length}</strong> operation{openAPIResults.length !== 1 ? 's' : ''}.
                  Each will be added as an API with its own flow. Check the box to prepend a
                  <strong> validate_route</strong> step with auto-generated rules.
                </div>

                <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 16 }}>
                  {openAPIResults.map(api => {
                    const hasRules = api.validateRouteRules && api.validateRouteRules.length > 0
                    return (
                      <div key={api.name} style={{
                        background: 'rgba(255,255,255,0.04)', border: '1px solid var(--border)',
                        borderRadius: 7, padding: '10px 12px',
                      }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: hasRules ? 8 : 0 }}>
                          <span style={{
                            fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 4,
                            color: '#fff', background: METHOD_COLOR[api.method] ?? '#64748b',
                            minWidth: 50, textAlign: 'center', display: 'inline-block',
                          }}>
                            {api.method}
                          </span>
                          <code style={{ fontSize: 12, color: 'var(--text)' }}>{api.path}</code>
                          <span style={{ fontSize: 11, color: 'var(--muted)', flex: 1 }}>{api.name}</span>
                        </div>
                        {hasRules && (
                          <label style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 12 }}>
                            <input
                              type="checkbox"
                              checked={!!openAPIAddValidation[api.name]}
                              onChange={e => setOpenAPIAddValidation(prev => ({
                                ...prev, [api.name]: e.target.checked,
                              }))}
                            />
                            <span>
                              Add <strong>validate_route</strong> step
                              ({api.validateRouteRules!.length} rule{api.validateRouteRules!.length !== 1 ? 's' : ''})
                            </span>
                          </label>
                        )}
                      </div>
                    )
                  })}
                </div>

                <div style={{ display: 'flex', gap: 8 }}>
                  <button
                    className="btn"
                    style={{ width: 'auto', padding: '6px 20px' }}
                    onClick={applyOpenAPIImport}
                  >
                    Add {openAPIResults.length} API{openAPIResults.length !== 1 ? 's' : ''} to Catalog
                  </button>
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '6px 14px' }}
                    onClick={() => { setOpenAPIResults([]); setOpenAPISpec('') }}
                  >
                    Back
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    )}

    {/* ── Toolify Modal ────────────────────────────────────────────────── */}
    {toolify.open && toolify.api && (
      <div style={{
        position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        zIndex: 1000,
      }} onClick={e => { if (e.target === e.currentTarget && !toolify.submitting) closeToolify() }}>
        <div style={{
          background: 'var(--panel)', border: '1px solid var(--border)',
          borderRadius: 10, width: 760, maxHeight: '88vh',
          display: 'flex', flexDirection: 'column', overflow: 'hidden',
        }}>
          {/* Modal header */}
          <div style={{
            padding: '14px 18px', borderBottom: '1px solid var(--border)',
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          }}>
            <div>
              <span style={{ fontSize: 14, fontWeight: 700 }}>Convert Endpoints to MCP Tools</span>
              <span style={{ fontSize: 12, color: 'var(--muted)', marginLeft: 10 }}>{toolify.api.name}</span>
            </div>
            <button
              className="btn muted"
              style={{ width: 'auto', padding: '2px 10px', marginTop: 0, fontSize: 12 }}
              disabled={toolify.submitting}
              onClick={closeToolify}
            >
              Close
            </button>
          </div>

          {/* Modal body */}
          <div style={{ flex: 1, overflowY: 'auto', padding: 18 }}>

            {/* Success state */}
            {Object.keys(toolify.results).length > 0 && !toolify.submitting && (() => {
              const successes = Object.values(toolify.results).filter(r => r.ok).length
              const total = Object.keys(toolify.results).length
              return (
                <>
                  {successes === total ? (
                    <div style={{
                      background: 'rgba(34,197,94,0.1)', border: '1px solid rgba(34,197,94,0.3)',
                      borderRadius: 7, padding: '10px 14px', marginBottom: 14, fontSize: 12,
                    }}>
                      <strong style={{ color: '#22c55e' }}>{successes} tool{successes !== 1 ? 's' : ''} registered.</strong>
                      {' '}Go to <strong>AI &gt; MCP Servers &gt; API Tools</strong> to add them to a virtual server.
                    </div>
                  ) : (
                    <div style={{
                      background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.3)',
                      borderRadius: 7, padding: '10px 14px', marginBottom: 14, fontSize: 12,
                    }}>
                      <strong style={{ color: '#f87171' }}>{successes} of {total} succeeded.</strong>
                      {' '}See results below. Failed tools can be retried after fixing the issue.
                    </div>
                  )}
                </>
              )
            })()}

            {/* Target selector */}
            <div style={{ marginBottom: 14 }}>
              <label style={{ fontSize: 12, fontWeight: 600, display: 'block', marginBottom: 4 }}>
                Target Gateway
              </label>
              {toolify.targets.length === 0 ? (
                <div style={{ fontSize: 11, color: 'var(--muted)' }}>Loading targets…</div>
              ) : (
                <select
                  className="input"
                  style={{ maxWidth: 340, marginTop: 0 }}
                  value={toolify.selectedTarget}
                  disabled={toolify.submitting}
                  onChange={e => setToolify(prev => ({ ...prev, selectedTarget: e.target.value }))}
                >
                  {toolify.targets.map(t => (
                    <option key={t.name} value={t.name}>
                      {t.name}{t.urls?.[0] ? ` — ${t.urls[0]}` : ''}
                    </option>
                  ))}
                </select>
              )}
            </div>

            {/* Endpoints table */}
            <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 6 }}>Endpoints</div>
            <div style={{
              border: '1px solid var(--border)', borderRadius: 7, overflow: 'hidden',
            }}>
              {/* Table header */}
              <div style={{
                display: 'grid',
                gridTemplateColumns: '32px 1fr 1fr 1fr',
                gap: 0, padding: '7px 10px',
                background: 'rgba(255,255,255,0.04)',
                borderBottom: '1px solid var(--border)',
                fontSize: 11, fontWeight: 600, color: 'var(--muted)',
              }}>
                <div></div>
                <div>Tool Name</div>
                <div>Description <span style={{ color: '#ef4444' }}>*</span></div>
                <div>Schema (optional)</div>
              </div>

              {/* Endpoint rows */}
              {toolify.api.endpoints.map((ep, idx) => {
                const isChecked = !!toolify.checked[ep.id]
                const toolName = toolify.toolNames[ep.id] ?? ''
                const desc = toolify.descriptions[ep.id] ?? ''
                const schema = toolify.schemas[ep.id] ?? ''
                const result = toolify.results[ep.id]
                const descEmpty = isChecked && !desc.trim()
                return (
                  <div
                    key={ep.id}
                    style={{
                      display: 'grid',
                      gridTemplateColumns: '32px 1fr 1fr 1fr',
                      gap: 0, padding: '8px 10px',
                      borderBottom: idx < toolify.api!.endpoints.length - 1 ? '1px solid var(--border)' : 'none',
                      background: result
                        ? result.ok
                          ? 'rgba(34,197,94,0.05)'
                          : 'rgba(239,68,68,0.07)'
                        : 'transparent',
                      alignItems: 'start',
                    }}
                  >
                    {/* Checkbox + method+path label */}
                    <div style={{ paddingTop: 6 }}>
                      <input
                        type="checkbox"
                        checked={isChecked}
                        disabled={toolify.submitting}
                        onChange={e => setToolify(prev => ({
                          ...prev,
                          checked: { ...prev.checked, [ep.id]: e.target.checked },
                        }))}
                      />
                    </div>

                    {/* Tool name column — also shows method+path label above */}
                    <div style={{ paddingRight: 8 }}>
                      <div style={{ fontSize: 10, color: 'var(--muted)', marginBottom: 4 }}>
                        <span style={{
                          fontSize: 9, fontWeight: 700, padding: '1px 5px', borderRadius: 3,
                          color: '#fff', background: METHOD_COLOR[ep.method] ?? '#64748b',
                          marginRight: 5,
                        }}>{ep.method}</span>
                        <code style={{ fontFamily: 'monospace' }}>{ep.subPath}</code>
                      </div>
                      <input
                        className="input"
                        style={{ marginTop: 0, fontSize: 11, padding: '3px 6px' }}
                        value={toolName}
                        disabled={toolify.submitting || !isChecked}
                        onChange={e => setToolify(prev => ({
                          ...prev,
                          toolNames: { ...prev.toolNames, [ep.id]: e.target.value },
                        }))}
                      />
                    </div>

                    {/* Description column */}
                    <div style={{ paddingRight: 8 }}>
                      <input
                        className="input"
                        style={{
                          marginTop: 0, fontSize: 11, padding: '3px 6px',
                          borderColor: descEmpty ? '#ef4444' : undefined,
                        }}
                        placeholder="Required description"
                        value={desc}
                        disabled={toolify.submitting || !isChecked}
                        onChange={e => setToolify(prev => ({
                          ...prev,
                          descriptions: { ...prev.descriptions, [ep.id]: e.target.value },
                        }))}
                      />
                      {descEmpty && (
                        <div style={{ fontSize: 10, color: '#f87171', marginTop: 2 }}>Required</div>
                      )}
                      {result && !result.ok && (
                        <div style={{ fontSize: 10, color: '#f87171', marginTop: 2 }}>{result.error}</div>
                      )}
                      {result?.ok && (
                        <div style={{ fontSize: 10, color: '#22c55e', marginTop: 2 }}>Registered</div>
                      )}
                    </div>

                    {/* Schema column */}
                    <div>
                      <textarea
                        className="input"
                        style={{ marginTop: 0, fontSize: 10, padding: '3px 6px', height: 54, resize: 'vertical', fontFamily: 'monospace' }}
                        placeholder={'JSON object schema, leave empty if not needed'}
                        value={schema}
                        disabled={toolify.submitting || !isChecked}
                        onChange={e => setToolify(prev => ({
                          ...prev,
                          schemas: { ...prev.schemas, [ep.id]: e.target.value },
                        }))}
                      />
                    </div>
                  </div>
                )
              })}
            </div>
          </div>

          {/* Modal footer */}
          <div style={{
            padding: '12px 18px', borderTop: '1px solid var(--border)',
            display: 'flex', alignItems: 'center', gap: 10,
          }}>
            {(() => {
              const checkedEps = toolify.api!.endpoints.filter(ep => toolify.checked[ep.id])
              const allHaveDesc = checkedEps.every(ep => !!toolify.descriptions[ep.id]?.trim())
              const canSubmit = checkedEps.length > 0 && allHaveDesc && !toolify.submitting
              return (
                <>
                  <button
                    className="btn"
                    style={{ width: 'auto', padding: '6px 20px' }}
                    disabled={!canSubmit}
                    onClick={submitToolify}
                  >
                    Register Tools
                  </button>
                  {toolify.submitting && toolify.progress && (
                    <span style={{ fontSize: 12, color: 'var(--muted)' }}>{toolify.progress}</span>
                  )}
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '6px 14px', marginLeft: 'auto' }}
                    disabled={toolify.submitting}
                    onClick={closeToolify}
                  >
                    Cancel
                  </button>
                </>
              )
            })()}
          </div>
        </div>
      </div>
    )}
    </>
  )
}

// ── Empty right panel ────────────────────────────────────────────────────────

function EmptyRight({ onNewApi, onNavigateToDesigner }: { onNewApi: () => void; onNavigateToDesigner: (flowName?: string) => void }) {
  return (
    <div style={{
      flex: 1, display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center',
      gap: 14, padding: 40, color: 'var(--muted)',
    }}>
      <span style={{ fontSize: 36, opacity: 0.2 }}>⚡</span>
      <div style={{ textAlign: 'center' }}>
        <p style={{ fontSize: 14, color: 'var(--text)', marginBottom: 6 }}>Register your first API</p>
        <p style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 20, lineHeight: 1.6 }}>
          Group endpoints under a base path and connect them to flows.<br />
          Each endpoint routes incoming requests to a flow that handles<br />
          authentication, upstream calls, and responses.
        </p>
      </div>
      <div style={{ display: 'flex', gap: 10 }}>
        <button className="btn" style={{ width: 'auto', padding: '6px 18px', marginTop: 0 }} onClick={onNewApi}>
          + New API
        </button>
        <button className="btn muted" style={{ width: 'auto', padding: '6px 18px', marginTop: 0 }} onClick={() => onNavigateToDesigner()}>
          → Flow Designer
        </button>
      </div>
    </div>
  )
}

// ── Upstream URL Editor ──────────────────────────────────────────────────────

const SOURCE_LABELS: Record<string, string> = {
  static:     'Static URL',
  registry:   'Registry key',
  cache:      'Cache key',
  header:     'Request header',
  queryparam: 'Query param',
}

const SOURCE_PLACEHOLDERS: Record<string, string> = {
  static:     'https://api.example.com/v1',
  registry:   'primary_service_url',
  cache:      'backend_url_key',
  header:     'X-Backend-Url',
  queryparam: 'backend',
}

function UpstreamUrlEditor({
  value,
  onChange,
  inheritLabel,
}: {
  value: UpstreamUrlConfig | undefined
  onChange: (u: UpstreamUrlConfig | undefined) => void
  inheritLabel?: string
}) {
  return (
    <div>
      <select
        className="input"
        style={{ maxWidth: 220, marginTop: 0 }}
        value={value?.source ?? ''}
        onChange={e => {
          const src = e.target.value as UpstreamUrlConfig['source'] | ''
          if (!src) { onChange(undefined); return }
          onChange({ source: src, value: '' })
        }}
      >
        <option value="">{inheritLabel ?? '— none —'}</option>
        {Object.entries(SOURCE_LABELS).map(([k, v]) => (
          <option key={k} value={k}>{v}</option>
        ))}
      </select>

      {value && (
        <input
          className="input"
          style={{ marginTop: 6, maxWidth: 360 }}
          placeholder={SOURCE_PLACEHOLDERS[value.source]}
          value={value.value}
          onChange={e => onChange({ ...value, value: e.target.value })}
        />
      )}

      {value?.source === 'static' && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4, lineHeight: 1.5 }}>
          Stored as a pre-set variable named <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>upstream_url</code>. In your flow's <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>http_call</code> step, set <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>url_var: upstream_url</code> to use it.
        </p>
      )}

      {value?.source && value.source !== 'static' && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4, lineHeight: 1.5 }}>
          At runtime, the gateway reads this key from {SOURCE_LABELS[value.source].toLowerCase()} and uses it as the upstream URL.
        </p>
      )}
    </div>
  )
}

// ── Rate Limit Editor ────────────────────────────────────────────────────────

const RL_SOURCE_LABELS: Record<string, string> = {
  registry:   'Registry key',
  cache:      'Cache key',
  header:     'Request header',
  queryparam: 'Query param',
}

const COUNT_BY_LABELS: Record<string, string> = {
  tenant: 'Per tenant',
  ip:     'Per IP',
  slot:   'Per variable',
  global: 'Global (all tenants)',
}

type ConfigKind = 'none' | 'named' | 'dynamic'

function configKindOf(cfg: RateLimitConfigSource | undefined): ConfigKind {
  if (!cfg) return 'none'
  return cfg.kind === 'named' ? 'named' : 'dynamic'
}

function RateLimitEditor({
  rateLimitCountBy,
  rateLimitConfig,
  rateLimitConfigs,
  rateLimitError,
  isEndpoint,
  onUpdateCountBy,
  onUpdateConfig,
  onCreateConfig,
}: {
  rateLimitCountBy: RateLimitCountBy | undefined
  rateLimitConfig: RateLimitConfigSource | undefined
  rateLimitConfigs: string[]
  rateLimitError: boolean
  isEndpoint: boolean
  onUpdateCountBy: (v: RateLimitCountBy | undefined) => void
  onUpdateConfig: (v: RateLimitConfigSource | undefined) => void
  onCreateConfig: (name: string, perSec: number, perMin: number, burst: number) => Promise<void>
}) {
  const countByKind = rateLimitCountBy?.kind ?? 'tenant'
  const configKind  = configKindOf(rateLimitConfig)

  const [showCreate, setShowCreate] = useState(false)
  const [createName,   setCreateName]   = useState('')
  const [createPerSec, setCreatePerSec] = useState('')
  const [createPerMin, setCreatePerMin] = useState('')
  const [createBurst,  setCreateBurst]  = useState('100')
  const [creating,     setCreating]     = useState(false)
  const [createErr,    setCreateErr]    = useState('')

  function handleConfigKindChange(next: ConfigKind) {
    setShowCreate(false)
    if (next === 'none')    { onUpdateConfig(undefined); return }
    if (next === 'named')   { onUpdateConfig({ kind: 'named', name: rateLimitConfigs[0] ?? '' }); return }
    if (next === 'dynamic') { onUpdateConfig({ kind: 'dynamic', source: 'registry', key: '' }); return }
  }

  async function doCreate() {
    if (!createName.trim()) { setCreateErr('Name required'); return }
    const ps = Number(createPerSec); const pm = Number(createPerMin); const b = Number(createBurst)
    if (!Number.isFinite(ps) || ps < 0) { setCreateErr('Invalid per-second value'); return }
    if (!Number.isFinite(pm) || pm < 0) { setCreateErr('Invalid per-minute value'); return }
    if (!Number.isFinite(b)  || b  < 0) { setCreateErr('Invalid burst value'); return }
    setCreating(true); setCreateErr('')
    try {
      await onCreateConfig(createName.trim(), ps, pm, b)
      onUpdateConfig({ kind: 'named', name: createName.trim() })
      setShowCreate(false)
      setCreateName(''); setCreatePerSec(''); setCreatePerMin(''); setCreateBurst('100')
    } catch (e: any) {
      setCreateErr(e?.message ?? 'Failed to create')
    } finally {
      setCreating(false)
    }
  }

  const dynConfig = rateLimitConfig?.kind === 'dynamic' ? rateLimitConfig : undefined
  const namedConfig = rateLimitConfig?.kind === 'named' ? rateLimitConfig : undefined

  return (
    <div>
      {/* Row 1: Dimension A — Count by */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
        <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 52 }}>Count by:</span>
        <select
          className="input"
          style={{ maxWidth: 180, marginTop: 0 }}
          value={countByKind}
          onChange={e => {
            const kind = e.target.value as RateLimitCountBy['kind']
            if (kind === 'tenant') onUpdateCountBy(undefined)
            else if (kind === 'ip') onUpdateCountBy({ kind: 'ip', xffIndex: 0 })
            else if (kind === 'global') onUpdateCountBy({ kind: 'global' })
            else onUpdateCountBy({ kind: 'slot', variableName: '' })
          }}
        >
          {Object.entries(COUNT_BY_LABELS).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          {isEndpoint && <option value="tenant">Inherit from API</option>}
        </select>
        {countByKind === 'slot' && (
          <input
            className="input"
            style={{ flex: 1, maxWidth: 200, marginTop: 0 }}
            placeholder="variable name"
            value={(rateLimitCountBy as { kind: 'slot'; variableName: string })?.variableName ?? ''}
            onChange={e => onUpdateCountBy({ kind: 'slot', variableName: e.target.value })}
          />
        )}
        {countByKind === 'ip' && (
          <>
            <span style={{ fontSize: 11, color: 'var(--muted)' }}>XFF index:</span>
            <input
              type="number"
              className="input"
              style={{ width: 68, marginTop: 0, textAlign: 'center' }}
              title="X-Forwarded-For index: 0 = first/leftmost (original client), -1 = last/rightmost (nearest proxy)"
              value={(rateLimitCountBy as { kind: 'ip'; xffIndex?: number })?.xffIndex ?? 0}
              onChange={e => {
                const v = parseInt(e.target.value, 10)
                onUpdateCountBy({ kind: 'ip', xffIndex: Number.isFinite(v) ? v : 0 })
              }}
            />
          </>
        )}
      </div>
      {countByKind === 'ip' && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: -4, marginBottom: 8, lineHeight: 1.5 }}>
          A <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>bind_client_ip</code> step will be prepended automatically on sync.
          Add a <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>check_rate_limit_ip</code> step to your flow to activate IP-based limiting.
        </p>
      )}
      {countByKind === 'slot' && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: -4, marginBottom: 8, lineHeight: 1.5 }}>
          Add a <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>check_rate_limit_slot</code> step to your flow to activate slot-based limiting.
        </p>
      )}
      {countByKind === 'tenant' && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: -4, marginBottom: 8, lineHeight: 1.5 }}>
          Add a <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>check_rate_limit</code> step to your flow to activate rate limiting.
        </p>
      )}
      {countByKind === 'global' && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: -4, marginBottom: 8, lineHeight: 1.5 }}>
          All tenants share a single counter bucket — a single high-traffic tenant can deplete the limit for others.
          Add a <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>check_rate_limit_global</code> step to your flow, or sync to auto-wire via <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '2px 4px', borderRadius: 3 }}>rate_limit_mode: &quot;global&quot;</code>.
        </p>
      )}

      {/* Row 2: Dimension B — Config */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 52 }}>Config:</span>
        <select
          className="input"
          style={{ maxWidth: 180, marginTop: 0 }}
          value={configKind}
          onChange={e => handleConfigKindChange(e.target.value as ConfigKind)}
        >
          <option value="none">{isEndpoint ? '— inherit from API —' : '— none —'}</option>
          <option value="named">Named config</option>
          <option value="dynamic">Dynamic source</option>
        </select>

        {configKind === 'named' && (
          <>
            <select
              className="input"
              style={{ maxWidth: 200, marginTop: 0 }}
              value={namedConfig?.name ?? ''}
              onChange={e => onUpdateConfig({ kind: 'named', name: e.target.value })}
            >
              <option value="">— select config —</option>
              {rateLimitConfigs.map(n => <option key={n} value={n}>{n}</option>)}
            </select>
            <button
              className="btn muted"
              style={{ width: 'auto', padding: '2px 10px', marginTop: 0, fontSize: 11 }}
              onClick={() => setShowCreate(p => !p)}
            >{showCreate ? 'Cancel' : '+ Create'}</button>
          </>
        )}

        {configKind === 'dynamic' && (
          <>
            <select
              className="input"
              style={{ maxWidth: 160, marginTop: 0 }}
              value={dynConfig?.source ?? ''}
              onChange={e => {
                const src = e.target.value as 'registry' | 'cache' | 'header' | 'queryparam' | ''
                if (!src) return
                onUpdateConfig({ kind: 'dynamic', source: src as any, key: dynConfig?.key ?? '' })
              }}
            >
              <option value="">— select source —</option>
              {Object.entries(RL_SOURCE_LABELS).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
            {dynConfig?.source && (
              <input
                className="input"
                style={{ flex: 1, maxWidth: 200, marginTop: 0 }}
                placeholder="key name"
                value={dynConfig.key}
                onChange={e => onUpdateConfig({ kind: 'dynamic', source: dynConfig.source, key: e.target.value })}
              />
            )}
          </>
        )}
      </div>

      {rateLimitError && configKind === 'named' && (
        <p style={{ fontSize: 11, color: '#f59e0b', marginTop: 4 }}>
          Could not load configs — is the gateway running?
        </p>
      )}
      {!rateLimitError && rateLimitConfigs.length === 0 && configKind === 'named' && !showCreate && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4 }}>
          No rate limit configs yet. Use "+ Create" to add one inline.
        </p>
      )}
      {configKind === 'dynamic' && dynConfig?.source && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4, lineHeight: 1.5 }}>
          At runtime the gateway reads this key from {RL_SOURCE_LABELS[dynConfig.source].toLowerCase()} to get the rate limit config name.
        </p>
      )}

      {showCreate && (
        <div style={{
          marginTop: 10, padding: '12px 14px',
          borderRadius: 8, border: '1px solid var(--border)',
          background: 'var(--panel)',
        }}>
          <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 8 }}>Quick-create rate limit config</div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <label style={{ fontSize: 11, color: 'var(--muted)', display: 'flex', flexDirection: 'column', gap: 3 }}>
              Name
              <input className="input" style={{ width: 120, padding: '3px 8px', fontSize: 12 }}
                value={createName} onChange={e => setCreateName(e.target.value)} placeholder="e.g. standard" />
            </label>
            <label style={{ fontSize: 11, color: 'var(--muted)', display: 'flex', flexDirection: 'column', gap: 3 }}>
              Per second
              <input className="input" style={{ width: 80, padding: '3px 8px', fontSize: 12 }}
                type="number" min="0" value={createPerSec} onChange={e => setCreatePerSec(e.target.value)} placeholder="0" />
            </label>
            <label style={{ fontSize: 11, color: 'var(--muted)', display: 'flex', flexDirection: 'column', gap: 3 }}>
              Per minute
              <input className="input" style={{ width: 80, padding: '3px 8px', fontSize: 12 }}
                type="number" min="0" value={createPerMin} onChange={e => setCreatePerMin(e.target.value)} placeholder="0" />
            </label>
            <label style={{ fontSize: 11, color: 'var(--muted)', display: 'flex', flexDirection: 'column', gap: 3 }}>
              Burst %
              <input className="input" style={{ width: 70, padding: '3px 8px', fontSize: 12 }}
                type="number" min="0" value={createBurst} onChange={e => setCreateBurst(e.target.value)} />
            </label>
            <button
              className="btn"
              style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12, alignSelf: 'flex-end' }}
              onClick={doCreate}
              disabled={creating}
            >{creating ? 'Creating…' : 'Create & Apply'}</button>
          </div>
          {createErr && <p style={{ fontSize: 11, color: '#ef4444', marginTop: 6 }}>{createErr}</p>}
        </div>
      )}
    </div>
  )
}

// ── V2 Rate Limit Section (legacy) ───────────────────────────────────────────

type V2ConfigKind = 'none' | 'named' | 'dynamic'
type OnEmptyKey = 'fail' | 'skip' | 'fallback_tenant'

function RateLimitV2Section({
  rlCountBy, rlConfigRef, upstreamService,
  v2Configs, isEndpoint,
  onUpdateCountBy, onUpdateConfigRef, onUpdateUpstreamService,
}: {
  rlCountBy: RateLimitCountByV2 | undefined
  rlConfigRef: RateLimitConfigRef | undefined
  upstreamService: string | undefined
  v2Configs: string[]
  isEndpoint: boolean
  onUpdateCountBy: (v: RateLimitCountByV2 | undefined) => void
  onUpdateConfigRef: (v: RateLimitConfigRef | undefined) => void
  onUpdateUpstreamService: (v: string | undefined) => void
}) {
  const [collapsed, setCollapsed] = useState(true)

  const countByKind = rlCountBy?.kind ?? 'tenant'
  const configKind: V2ConfigKind = !rlConfigRef ? 'none' : rlConfigRef.kind === 'named' ? 'named' : 'dynamic'

  function handleCountByKindChange(kind: RateLimitCountByV2['kind']) {
    if (kind === 'tenant') { onUpdateCountBy(undefined); return }
    if (kind === 'global') { onUpdateCountBy({ kind: 'global' }); return }
    if (kind === 'ip')     { onUpdateCountBy({ kind: 'ip', xff_index: 0 }); return }
    if (kind === 'slot')   { onUpdateCountBy({ kind: 'slot', slot_name: '' }); return }
    if (kind === 'static') { onUpdateCountBy({ kind: 'static', static_value: '' }); return }
    if (kind === 'composite') { onUpdateCountBy({ kind: 'composite', composite_slots: [] }); return }
  }

  function handleConfigKindChange(kind: V2ConfigKind) {
    if (kind === 'none')    { onUpdateConfigRef(undefined); return }
    if (kind === 'named')   { onUpdateConfigRef({ kind: 'named', name: v2Configs[0] ?? '' }); return }
    if (kind === 'dynamic') { onUpdateConfigRef({ kind: 'dynamic', source: 'registry', key: '' }); return }
  }

  const showOnEmptyKey = rlCountBy && rlCountBy.kind !== 'tenant' && rlCountBy.kind !== 'global'

  return (
    <div style={{ marginTop: 20 }}>
      <div
        style={{
          display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer',
          padding: '6px 0', borderTop: '1px solid var(--border)',
          userSelect: 'none',
        }}
        onClick={() => setCollapsed(c => !c)}
      >
        <span style={{ fontSize: 10, color: 'var(--muted)', transform: collapsed ? 'rotate(-90deg)' : 'none', display: 'inline-block', transition: 'transform 0.15s' }}>▼</span>
        <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>Rate Limiting</span>
        {(rlCountBy || rlConfigRef || upstreamService) && (
          <span style={{ fontSize: 10, color: 'var(--accent)', marginLeft: 4 }}>●</span>
        )}
        {isEndpoint && <span style={{ fontSize: 10, color: 'var(--muted)', marginLeft: 'auto' }}>(endpoint)</span>}
      </div>
      {!collapsed && (
        <div style={{ paddingTop: 10 }}>

          {/* Count By */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 72 }}>Count by:</span>
            <select
              className="input"
              style={{ maxWidth: 180, marginTop: 0 }}
              value={countByKind}
              onChange={e => handleCountByKindChange(e.target.value as RateLimitCountByV2['kind'])}
            >
              <option value="tenant">Per tenant — one bucket per tenant ID</option>
              <option value="ip">Per IP — one bucket per client IP</option>
              <option value="slot">Per slot — one bucket per value in a named flow slot</option>
              <option value="static">Static key — single shared bucket</option>
              <option value="composite">Composite — bucket per combination of slots</option>
              <option value="global">Global — single bucket across all tenants</option>
            </select>

            {countByKind === 'slot' && (
              <input
                className="input"
                style={{ flex: 1, maxWidth: 200, marginTop: 0 }}
                placeholder="slot name (e.g. user_id)"
                value={rlCountBy?.kind === 'slot' ? (rlCountBy.slot_name ?? '') : ''}
                onChange={e => onUpdateCountBy({ kind: 'slot', slot_name: e.target.value })}
              />
            )}
            {countByKind === 'static' && (
              <input
                className="input"
                style={{ flex: 1, maxWidth: 200, marginTop: 0 }}
                placeholder="static value"
                value={rlCountBy?.kind === 'static' ? (rlCountBy.static_value ?? '') : ''}
                onChange={e => onUpdateCountBy({ kind: 'static', static_value: e.target.value })}
              />
            )}
            {countByKind === 'composite' && (
              <input
                className="input"
                style={{ flex: 1, maxWidth: 260, marginTop: 0 }}
                placeholder="slot1,slot2,slot3 (comma-separated)"
                value={rlCountBy?.kind === 'composite' ? (rlCountBy.composite_slots ?? []).join(',') : ''}
                onChange={e => onUpdateCountBy({ kind: 'composite', composite_slots: e.target.value.split(',').map(s => s.trim()).filter(Boolean) })}
              />
            )}
            {countByKind === 'ip' && (
              <>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>XFF index:</span>
                <input
                  type="number"
                  className="input"
                  style={{ width: 68, marginTop: 0, textAlign: 'center' }}
                  value={rlCountBy?.kind === 'ip' ? (rlCountBy.xff_index ?? 0) : 0}
                  onChange={e => {
                    const v = parseInt(e.target.value, 10)
                    onUpdateCountBy({ kind: 'ip', xff_index: Number.isFinite(v) ? v : 0, on_empty_key: rlCountBy?.kind === 'ip' ? rlCountBy.on_empty_key : undefined })
                  }}
                />
              </>
            )}
          </div>

          {showOnEmptyKey && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 72 }}>On empty key:</span>
              <select
                className="input"
                style={{ maxWidth: 200, marginTop: 0 }}
                value={rlCountBy?.on_empty_key ?? 'fail'}
                onChange={e => {
                  if (!rlCountBy) return
                  onUpdateCountBy({ ...rlCountBy, on_empty_key: e.target.value as OnEmptyKey })
                }}
              >
                <option value="fail">fail</option>
                <option value="skip">skip</option>
                <option value="fallback_tenant">fallback_tenant</option>
              </select>
            </div>
          )}

          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 14 }}>
            <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 72 }}>Fail fast:</span>
            <input
              type="checkbox"
              checked={rlCountBy?.fail_fast ?? false}
              onChange={e => {
                const base = rlCountBy ?? { kind: 'tenant' as const }
                onUpdateCountBy({ ...base, fail_fast: e.target.checked })
              }}
            />
            <span style={{ fontSize: 11, color: 'var(--muted)' }}>Stop after first window failure</span>
          </div>

          {/* Config Ref */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 72 }}>Config:</span>
            <select
              className="input"
              style={{ maxWidth: 180, marginTop: 0 }}
              value={configKind}
              onChange={e => handleConfigKindChange(e.target.value as V2ConfigKind)}
            >
              <option value="none">— none —</option>
              <option value="named">Named</option>
              <option value="dynamic">Dynamic</option>
            </select>

            {configKind === 'named' && (
              v2Configs.length === 0 ? (
                <span style={{
                  fontSize: 11, color: 'var(--muted)',
                  background: 'var(--accent-bg)', border: '1px solid var(--border)',
                  borderRadius: 4, padding: '4px 8px', flex: 1,
                }}>
                  No rate limit configs yet — go to <strong>Rate Limits</strong> in the sidebar to create one.
                </span>
              ) : (
                <select
                  className="input"
                  style={{ maxWidth: 220, marginTop: 0 }}
                  value={rlConfigRef?.kind === 'named' ? (rlConfigRef.name ?? '') : ''}
                  onChange={e => onUpdateConfigRef({ kind: 'named', name: e.target.value })}
                >
                  {v2Configs.map(n => <option key={n} value={n}>{n}</option>)}
                </select>
              )
            )}

            {configKind === 'dynamic' && (
              <>
                <select
                  className="input"
                  style={{ maxWidth: 160, marginTop: 0 }}
                  value={rlConfigRef?.kind === 'dynamic' ? (rlConfigRef.source ?? 'registry') : 'registry'}
                  onChange={e => onUpdateConfigRef({
                    kind: 'dynamic',
                    source: e.target.value as RateLimitConfigRef['source'],
                    key: rlConfigRef?.kind === 'dynamic' ? (rlConfigRef.key ?? '') : '',
                  })}
                >
                  <option value="registry">registry (tenant property)</option>
                  <option value="header">header</option>
                  <option value="queryparam">queryparam</option>
                  <option value="cache">cache</option>
                </select>
                <input
                  className="input"
                  style={{ flex: 1, maxWidth: 200, marginTop: 0 }}
                  placeholder={
                    rlConfigRef?.kind === 'dynamic' && rlConfigRef.source === 'header' ? 'e.g. X-Tenant-Tier' :
                    rlConfigRef?.kind === 'dynamic' && rlConfigRef.source === 'queryparam' ? 'e.g. tier' :
                    'e.g. rl_config_name'
                  }
                  value={rlConfigRef?.kind === 'dynamic' ? (rlConfigRef.key ?? '') : ''}
                  onChange={e => onUpdateConfigRef({
                    kind: 'dynamic',
                    source: rlConfigRef?.kind === 'dynamic' ? (rlConfigRef.source ?? 'registry') : 'registry',
                    key: e.target.value,
                  })}
                />
              </>
            )}
          </div>

          {/* Dynamic config help */}
          {configKind === 'dynamic' && (
            <div style={{ marginBottom: 10, marginLeft: 80, fontSize: 11, color: 'var(--muted)', lineHeight: 1.5 }}>
              {rlConfigRef?.source === 'registry' && 'Reads the config name from a tenant registry property (set via Tiers or Tenant detail → Meta).'}
              {rlConfigRef?.source === 'header' && 'Reads the config name from the named request header. Header value must match an existing rate limit config name.'}
              {rlConfigRef?.source === 'queryparam' && 'Reads the config name from the named URL query parameter.'}
              {rlConfigRef?.source === 'cache' && 'Reads the config name from the cache using the given key.'}
            </div>
          )}

          {/* Upstream Service */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 72 }}>Upstream svc:</span>
            <input
              className="input"
              style={{ flex: 1, maxWidth: 260, marginTop: 0 }}
              placeholder="e.g. openai-svc"
              value={upstreamService ?? ''}
              onChange={e => onUpdateUpstreamService(e.target.value || undefined)}
            />
          </div>

        </div>
      )}
    </div>
  )
}

// ── Multi-Entry Rate Limit Policies Section ───────────────────────────────────

const WINDOW_EPOCH_SECS = [1, 60, 3600, 86400] as const
const WINDOW_LABELS: Record<number, string> = {
  1:     'per second',
  60:    'per minute',
  3600:  'per hour',
  86400: 'per day',
}

function defaultRLPolicy(): APIRateLimitEntry {
  return { kind: 'named', count_by: 'tenant', config: '' }
}

// ── Per-row status (computed client-side from local data) ─────────────────────

function computeRowStatus(policy: APIRateLimitEntry, v2Configs: string[]): { icon: string; color: string; title: string } {
  if (policy.kind === 'named') {
    if (!policy.config) return { icon: '○', color: 'var(--muted)', title: 'No config selected' }
    return v2Configs.includes(policy.config)
      ? { icon: '✓', color: '#22c55e', title: 'Config found' }
      : { icon: '✗', color: '#ef4444', title: `Config "${policy.config}" not found` }
  }
  if (policy.kind === 'fixed') {
    const wins = policy.windows ?? []
    if (wins.length === 0) return { icon: '○', color: 'var(--muted)', title: 'No windows defined' }
    return wins.some(w => w.limit > 0)
      ? { icon: '✓', color: '#22c55e', title: 'Fixed limit configured' }
      : { icon: '⚠', color: '#f59e0b', title: 'All window limits are 0' }
  }
  if (policy.kind === 'dynamic') {
    const entries = Object.entries(policy.dynamic?.mappings ?? {})
    if (entries.length === 0) return { icon: '⚠', color: '#f59e0b', title: 'No mappings defined' }
    const allFound = entries.every(([, cfg]) => v2Configs.includes(cfg))
    return allFound
      ? { icon: '✓', color: '#22c55e', title: 'All mapped configs found' }
      : { icon: '⚠', color: '#f59e0b', title: 'Some mapped configs not found' }
  }
  return { icon: '○', color: 'var(--muted)', title: '' }
}

// ── Dynamic rate limit editor ─────────────────────────────────────────────────

function DynamicRLEditor({
  dynamic: dynamicProp,
  v2Configs,
  onChange,
}: {
  dynamic: RLDynamicMapping | undefined
  v2Configs: string[]
  onChange: (d: RLDynamicMapping) => void
}) {
  const dynamic = dynamicProp ?? { source: 'meta.', mappings: {} }
  const dotIdx = dynamic.source.indexOf('.')
  const sourcePrefix = dotIdx >= 0 ? dynamic.source.slice(0, dotIdx) : 'meta'
  const sourceKey    = dotIdx >= 0 ? dynamic.source.slice(dotIdx + 1) : ''
  const mappings     = dynamic.mappings ?? {}
  const mappingEntries = Object.entries(mappings)

  function updateSource(prefix: string, key: string) {
    onChange({ ...dynamic, source: `${prefix}.${key}` })
  }

  function updateMappingVal(oldKey: string, newKey: string, cfg: string) {
    const next = { ...mappings }
    if (oldKey !== newKey) delete next[oldKey]
    next[newKey] = cfg
    onChange({ ...dynamic, mappings: next })
  }

  function updateMappingCfg(key: string, cfg: string) {
    onChange({ ...dynamic, mappings: { ...mappings, [key]: cfg } })
  }

  function removeMapping(key: string) {
    const next = { ...mappings }
    delete next[key]
    onChange({ ...dynamic, mappings: next })
  }

  function addMapping() {
    const existing = Object.keys(mappings)
    let k = 'value'
    let n = 1
    while (existing.includes(k)) k = `value${n++}`
    onChange({ ...dynamic, mappings: { ...mappings, [k]: '' } })
  }

  const sourcePlaceholder = sourcePrefix === 'meta' ? 'tier' : sourcePrefix === 'header' ? 'X-Plan' : 'plan_slot'

  return (
    <div style={{ marginBottom: 8 }}>
      {/* Source picker */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
        <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 52 }}>Source:</span>
        <select
          className="input"
          style={{ width: 90, marginTop: 0 }}
          value={sourcePrefix}
          onChange={e => updateSource(e.target.value, sourceKey)}
        >
          <option value="meta">meta</option>
          <option value="header">header</option>
          <option value="slot">slot</option>
        </select>
        <span style={{ fontSize: 11, color: 'var(--muted)' }}>.</span>
        <input
          className="input"
          style={{ flex: 1, maxWidth: 180, marginTop: 0 }}
          placeholder={sourcePlaceholder}
          value={sourceKey}
          onChange={e => updateSource(sourcePrefix, e.target.value)}
        />
      </div>

      {/* Mapping table */}
      <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 4 }}>Runtime value → config name:</div>
      {mappingEntries.length === 0 && (
        <div style={{ fontSize: 11, color: '#f59e0b', marginBottom: 6 }}>No mappings — add at least one.</div>
      )}
      {mappingEntries.map(([val, cfg], i) => (
        <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 5, marginBottom: 5 }}>
          <input
            className="input"
            style={{ width: 100, marginTop: 0, fontSize: 11 }}
            placeholder="e.g. free"
            value={val}
            onChange={e => updateMappingVal(val, e.target.value, cfg)}
          />
          <span style={{ fontSize: 11, color: 'var(--muted)' }}>→</span>
          {v2Configs.length > 0 ? (
            <select
              className="input"
              style={{ flex: 1, maxWidth: 180, marginTop: 0, fontSize: 11 }}
              value={cfg}
              onChange={e => updateMappingCfg(val, e.target.value)}
            >
              <option value="">— config —</option>
              {v2Configs.map(n => <option key={n} value={n}>{n}</option>)}
            </select>
          ) : (
            <input
              className="input"
              style={{ flex: 1, maxWidth: 180, marginTop: 0, fontSize: 11 }}
              placeholder="config name"
              value={cfg}
              onChange={e => updateMappingCfg(val, e.target.value)}
            />
          )}
          <button
            style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 12, padding: '0 3px', lineHeight: 1 }}
            onClick={() => removeMapping(val)}
            title="Remove mapping"
          >✕</button>
        </div>
      ))}
      <button
        className="btn muted"
        style={{ width: 'auto', padding: '2px 10px', marginTop: 2, fontSize: 11 }}
        onClick={addMapping}
      >＋ Add mapping</button>
      {dynamic.source && dynamic.source !== `${sourcePrefix}.` && (
        <p style={{ fontSize: 11, color: 'var(--muted)', marginTop: 6, lineHeight: 1.5 }}>
          At runtime, reads{' '}
          <code style={{ fontFamily: 'monospace', background: 'rgba(255,255,255,0.05)', padding: '1px 3px', borderRadius: 2 }}>
            {dynamic.source}
          </code>{' '}
          and dispatches to the matching config.
        </p>
      )}
    </div>
  )
}

function PolicyRow({
  policy, v2Configs, onUpdate, onRemove, onToggleWindow, onSetWindowLimit, warning,
}: {
  policy: APIRateLimitEntry
  v2Configs: string[]
  onUpdate: (patch: Partial<APIRateLimitEntry>) => void
  onRemove: () => void
  onToggleWindow: (epochSec: number, checked: boolean) => void
  onSetWindowLimit: (epochSec: number, limit: number) => void
  warning?: RateLimitWarning
}) {
  const KIND_OPTIONS = [
    { value: 'named',   label: 'Named' },
    { value: 'fixed',   label: 'Fixed' },
    { value: 'dynamic', label: 'Dynamic' },
  ] as const

  const hasBackendWarning = !!warning
  const isConfigMissing   = warning?.code === 'config_missing'

  return (
    <div style={{
      border: `1px solid ${isConfigMissing ? '#ef4444' : 'var(--border)'}`,
      borderRadius: 6,
      padding: '10px 12px',
      marginBottom: 8,
      background: isConfigMissing ? 'rgba(239,68,68,0.04)' : 'rgba(255,255,255,0.02)',
    }}>
      {/* Row header: kind toggle + remove button */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
        {/* Per-row status icon — backend warning takes precedence over client-side */}
        {hasBackendWarning ? (
          <span
            style={{ fontSize: 12, color: '#ef4444', fontWeight: 600, minWidth: 14, textAlign: 'center', cursor: 'help' }}
            title={warning!.message}
          >
            ✗
          </span>
        ) : (() => {
          const s = computeRowStatus(policy, v2Configs)
          return (
            <span style={{ fontSize: 12, color: s.color, fontWeight: 600, minWidth: 14, textAlign: 'center' }} title={s.title}>
              {s.icon}
            </span>
          )
        })()}

        {/* Kind pill toggle */}
        <div style={{ display: 'flex', gap: 2, background: 'var(--bg)', borderRadius: 4, padding: 2, border: '1px solid var(--border)' }}>
          {KIND_OPTIONS.map(opt => (
            <button
              key={opt.value}
              style={{
                padding: '2px 10px',
                fontSize: 11,
                border: 'none',
                borderRadius: 3,
                cursor: 'pointer',
                background: policy.kind === opt.value ? 'var(--accent)' : 'transparent',
                color: policy.kind === opt.value ? '#fff' : 'var(--muted)',
                fontWeight: policy.kind === opt.value ? 600 : 400,
                transition: 'all 0.1s',
              }}
              onClick={() => {
                const patch: Partial<APIRateLimitEntry> = { kind: opt.value }
                if (opt.value === 'named')   { patch.windows = undefined; patch.dynamic = undefined }
                if (opt.value === 'fixed')   { patch.config = undefined; patch.dynamic = undefined; if (!policy.windows?.length) patch.windows = [] }
                if (opt.value === 'dynamic') { patch.config = undefined; patch.windows = undefined; patch.dynamic = patch.dynamic ?? { source: 'meta.', mappings: {} } }
                onUpdate(patch)
              }}
            >
              {opt.label}
            </button>
          ))}
        </div>

        {/* Remove button */}
        <button
          style={{ marginLeft: 'auto', background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 14, padding: '0 4px', lineHeight: 1 }}
          onClick={onRemove}
          title="Remove this rate limit entry"
        >
          ✕
        </button>
      </div>

      {/* Named: config dropdown */}
      {policy.kind === 'named' && (
        <div style={{ marginBottom: 8 }}>
          {v2Configs.length === 0 ? (
            <div style={{
              fontSize: 11, color: 'var(--muted)',
              background: 'var(--accent-bg)', border: '1px solid var(--border)',
              borderRadius: 4, padding: '6px 10px',
            }}>
              No configs yet — go to <strong>Rate Limits</strong> in the sidebar to create one.
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 48 }}>Config:</span>
                <select
                  className="input"
                  style={{ maxWidth: 240, marginTop: 0, borderColor: isConfigMissing ? '#ef4444' : undefined }}
                  value={policy.config ?? ''}
                  onChange={e => onUpdate({ config: e.target.value })}
                >
                  <option value="">— select config —</option>
                  {v2Configs.map(n => <option key={n} value={n}>{n}</option>)}
                </select>
              </div>
              {isConfigMissing && (
                <div style={{ fontSize: 11, color: '#ef4444', paddingLeft: 56 }}>
                  Config not found — go to <strong>Rate Limits</strong> to create it.
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Fixed: window checkboxes with limit inputs */}
      {policy.kind === 'fixed' && (
        <div style={{ marginBottom: 8 }}>
          <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 6 }}>Windows (check to enable):</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            {WINDOW_EPOCH_SECS.map(epochSec => {
              const win = (policy.windows ?? []).find(w => w.epoch_sec === epochSec)
              const checked = !!win
              return (
                <div key={epochSec} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={e => onToggleWindow(epochSec, e.target.checked)}
                  />
                  <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 70 }}>{WINDOW_LABELS[epochSec]}</span>
                  {checked && (
                    <>
                      <input
                        type="number"
                        className="input"
                        style={{ width: 90, marginTop: 0, textAlign: 'right' }}
                        placeholder="limit"
                        min={0}
                        value={win?.limit ?? 0}
                        onChange={e => {
                          const v = parseInt(e.target.value, 10)
                          onSetWindowLimit(epochSec, Number.isFinite(v) && v >= 0 ? v : 0)
                        }}
                      />
                      <span style={{ fontSize: 11, color: 'var(--muted)' }}>req</span>
                    </>
                  )}
                </div>
              )
            })}
          </div>
          {(policy.windows ?? []).length === 0 && (
            <div style={{ fontSize: 11, color: '#f59e0b', marginTop: 4 }}>At least one window required.</div>
          )}
        </div>
      )}

      {/* Dynamic: source picker + mapping table */}
      {policy.kind === 'dynamic' && (
        <DynamicRLEditor
          dynamic={policy.dynamic}
          v2Configs={v2Configs}
          onChange={d => onUpdate({ dynamic: d })}
        />
      )}

      {/* Count by */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 58 }}>Count by:</span>
        <select
          className="input"
          style={{ maxWidth: 200, marginTop: 0 }}
          value={policy.count_by}
          onChange={e => onUpdate({ count_by: e.target.value as APIRateLimitEntry['count_by'], slot_source: undefined })}
        >
          <option value="tenant">Per tenant</option>
          <option value="ip">Per IP</option>
          <option value="global">Global</option>
          <option value="slot">Per slot</option>
          <option value="static">Static key</option>
        </select>
      </div>

      {/* Slot source */}
      {policy.count_by === 'slot' && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 8 }}>
          <span style={{ fontSize: 11, color: 'var(--muted)', minWidth: 58 }}>Slot:</span>
          <input
            className="input"
            style={{ flex: 1, maxWidth: 200, marginTop: 0 }}
            placeholder="slot name (e.g. user_id)"
            value={policy.slot_source ?? ''}
            onChange={e => onUpdate({ slot_source: e.target.value || undefined })}
          />
        </div>
      )}
    </div>
  )
}

function RateLimitPoliciesSection({
  policies,
  skipRateLimit,
  v2Configs,
  isEndpoint,
  onUpdatePolicies,
  onUpdateSkip,
  warnings = [],
}: {
  policies: APIRateLimitEntry[]
  skipRateLimit: boolean
  v2Configs: string[]
  isEndpoint: boolean
  onUpdatePolicies: (v: APIRateLimitEntry[]) => void
  onUpdateSkip: (v: boolean) => void
  warnings?: RateLimitWarning[]
}) {
  const [collapsed, setCollapsed] = useState(true)

  function updateRow(idx: number, patch: Partial<APIRateLimitEntry>) {
    onUpdatePolicies(policies.map((p, i) => i === idx ? { ...p, ...patch } : p))
  }

  function removeRow(idx: number) {
    onUpdatePolicies(policies.filter((_, i) => i !== idx))
  }

  function addRow() {
    onUpdatePolicies([...policies, defaultRLPolicy()])
    setCollapsed(false)
  }

  function toggleWindow(rowIdx: number, epochSec: number, checked: boolean) {
    const p = policies[rowIdx]
    const windows: RLFixedWindow[] = p.windows ?? []
    if (checked) {
      onUpdatePolicies(policies.map((pp, i) => i !== rowIdx ? pp : {
        ...pp, windows: [...windows, { epoch_sec: epochSec, limit: 0 }],
      }))
    } else {
      onUpdatePolicies(policies.map((pp, i) => i !== rowIdx ? pp : {
        ...pp, windows: windows.filter(w => w.epoch_sec !== epochSec),
      }))
    }
  }

  function setWindowLimit(rowIdx: number, epochSec: number, limit: number) {
    onUpdatePolicies(policies.map((pp, i) => i !== rowIdx ? pp : {
      ...pp, windows: (pp.windows ?? []).map(w => w.epoch_sec === epochSec ? { ...w, limit } : w),
    }))
  }

  const hasActive = policies.length > 0

  return (
    <div style={{ marginTop: 16 }}>
      <div
        style={{
          display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer',
          padding: '6px 0', borderTop: '1px solid var(--border)',
          userSelect: 'none',
        }}
        onClick={() => setCollapsed(c => !c)}
      >
        <span style={{
          fontSize: 10, color: 'var(--muted)',
          transform: collapsed ? 'rotate(-90deg)' : 'none',
          display: 'inline-block', transition: 'transform 0.15s',
        }}>▼</span>
        <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
          Rate Limit Policies
        </span>
        {hasActive && (
          <span style={{ fontSize: 10, color: 'var(--accent)', marginLeft: 4 }}>
            {policies.length} entr{policies.length === 1 ? 'y' : 'ies'}
          </span>
        )}
        {skipRateLimit && (
          <span style={{ fontSize: 10, color: '#f59e0b', marginLeft: 4 }}>skipped</span>
        )}
        {warnings.length > 0 && !skipRateLimit && (
          <span style={{ fontSize: 10, color: '#ef4444', marginLeft: 4 }} title={warnings.map(w => w.message).join('\n')}>
            ⚠ {warnings.length} warning{warnings.length === 1 ? '' : 's'}
          </span>
        )}
        {isEndpoint && <span style={{ fontSize: 10, color: 'var(--muted)', marginLeft: 'auto' }}>(endpoint)</span>}
      </div>
      {!collapsed && (
        <div style={{ paddingTop: 10 }}>
          {/* Policy rows */}
          {policies.map((policy, idx) => {
            const rowWarn = warnings.find(w => w.row === idx && w.code !== 'not_enforced' && w.code !== 'no_flow')
            return (
              <PolicyRow
                key={idx}
                policy={policy}
                v2Configs={v2Configs}
                onUpdate={patch => updateRow(idx, patch)}
                onRemove={() => removeRow(idx)}
                onToggleWindow={(epochSec, checked) => toggleWindow(idx, epochSec, checked)}
                onSetWindowLimit={(epochSec, limit) => setWindowLimit(idx, epochSec, limit)}
                warning={rowWarn}
              />
            )
          })}

          {policies.length === 0 && !skipRateLimit && (
            <div style={{ fontSize: 12, color: 'var(--muted)', padding: '8px 0', fontStyle: 'italic' }}>
              No rate limit policies configured.
            </div>
          )}

          {/* Add button */}
          <button
            className="btn muted"
            style={{ width: 'auto', padding: '4px 12px', marginTop: 4, fontSize: 12 }}
            onClick={addRow}
          >
            ＋ Add rate limit
          </button>

          {/* Skip checkbox */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 12 }}>
            <input
              type="checkbox"
              id={`skip-rl-${isEndpoint ? 'ep' : 'api'}`}
              checked={skipRateLimit}
              onChange={e => onUpdateSkip(e.target.checked)}
            />
            <label
              htmlFor={`skip-rl-${isEndpoint ? 'ep' : 'api'}`}
              style={{ fontSize: 12, color: 'var(--muted)', cursor: 'pointer' }}
            >
              Skip rate limiting on this {isEndpoint ? 'endpoint' : 'API'}
            </label>
          </div>

          {/* Flow enforcement status + backend warnings */}
          <div style={{
            marginTop: 10, fontSize: 11, color: 'var(--muted)', lineHeight: 1.6,
            borderTop: '1px solid var(--border)', paddingTop: 10,
            display: 'flex', flexDirection: 'column', gap: 6,
          }}>
            {skipRateLimit ? (
              <span style={{ color: '#f59e0b' }}>
                Rate limiting is skipped for this {isEndpoint ? 'endpoint' : 'API'}.
              </span>
            ) : policies.length > 0 ? (() => {
              const noFlow       = warnings.find(w => w.code === 'no_flow')
              const notEnforced  = warnings.find(w => w.code === 'not_enforced')
              return (
                <>
                  {noFlow && (
                    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 6, color: '#ef4444' }}>
                      <span style={{ flexShrink: 0, marginTop: 1 }}>✗</span>
                      <span><strong>No flow:</strong> {noFlow.message}</span>
                    </div>
                  )}
                  {notEnforced && !noFlow && (
                    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 6, color: '#f59e0b' }}>
                      <span style={{ flexShrink: 0, marginTop: 1 }}>⚠</span>
                      <span>
                        <strong>Not enforced in flow</strong> — auto-inject will apply on next deploy.
                        Place an <strong>API Rate Limits</strong> block in the flow to control the position.
                      </span>
                    </div>
                  )}
                  {!noFlow && !notEnforced && (
                    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 6 }}>
                      <span style={{ color: '#57b5ff', flexShrink: 0, marginTop: 1 }}>ℹ</span>
                      <span>
                        <strong>Flow enforcement:</strong> these limits are auto-injected at the start of the flow.
                        Place an <strong>API Rate Limits</strong> block in the flow to control the exact position.
                      </span>
                    </div>
                  )}
                </>
              )
            })() : (
              <span>No rate limit policies configured. Use &ldquo;＋ Add rate limit&rdquo; to add one.</span>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Constants Editor ─────────────────────────────────────────────────────────

function ConstantsEditor({
  constants,
  onChange,
  isEndpoint,
}: {
  constants: Record<string, string>
  onChange: (c: Record<string, string>) => void
  isEndpoint?: boolean
}) {
  const [newKey, setNewKey] = useState('')
  const [newVal, setNewVal] = useState('')
  const [keyError, setKeyError] = useState('')
  const entries = Object.entries(constants)

  const handleAddConstant = () => {
    const trimmedKey = newKey.trim()

    // Validate: empty key
    if (!trimmedKey) {
      setKeyError('Key cannot be empty')
      return
    }

    // Validate: duplicate key
    if (constants.hasOwnProperty(trimmedKey)) {
      setKeyError(`Key "${trimmedKey}" already exists`)
      return
    }

    // All good
    setKeyError('')
    onChange({ ...constants, [trimmedKey]: newVal.trim() })
    setNewKey('')
    setNewVal('')
  }

  const handleKeyChange = (val: string) => {
    setNewKey(val)
    // Clear error if user starts typing a non-empty value
    if (keyError && val.trim()) {
      setKeyError('')
    }
  }

  return (
    <div>
      <p style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 8, lineHeight: 1.5 }}>
        These values are injected before the flow runs. Reference them in flow steps using
        the key name (e.g., <code style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>as: "service_code"</code>).
        {isEndpoint && ' Endpoint-level values override API-level values for the same key.'}
      </p>
      {entries.map(([k, v]) => (
        <div key={k} style={{ display: 'flex', gap: 8, marginBottom: 4, alignItems: 'center' }}>
          <code style={{ fontSize: 11, minWidth: 100, color: 'var(--accent)' }}>{k}</code>
          <span style={{ fontSize: 11, color: 'var(--muted)' }}>=</span>
          <input
            className="input"
            style={{ flex: 1, padding: '2px 8px', fontSize: 12 }}
            value={v}
            onChange={e => onChange({ ...constants, [k]: e.target.value })}
          />
          <button
            className="btn muted"
            style={{ padding: '2px 8px', fontSize: 11, marginTop: 0 }}
            onClick={() => { const c = { ...constants }; delete c[k]; onChange(c) }}
          >×</button>
        </div>
      ))}
      <div style={{ display: 'flex', gap: 6, marginTop: 6 }}>
        <input
          className="input"
          placeholder="name"
          style={{ width: 100, padding: '2px 8px', fontSize: 12 }}
          value={newKey}
          onChange={e => handleKeyChange(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handleAddConstant()}
        />
        <input
          className="input"
          placeholder="value"
          style={{ flex: 1, padding: '2px 8px', fontSize: 12 }}
          value={newVal}
          onChange={e => setNewVal(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handleAddConstant()}
        />
        <button
          className="btn muted"
          style={{ padding: '2px 8px', fontSize: 11, marginTop: 0 }}
          onClick={handleAddConstant}
        >+ Add</button>
      </div>
      {keyError && (
        <div style={{ fontSize: 11, color: '#ef4444', marginTop: 4, display: 'flex', alignItems: 'center', gap: 4 }}>
          <span>⚠</span>
          <span>{keyError}</span>
        </div>
      )}
    </div>
  )
}

// ── API Detail Panel ─────────────────────────────────────────────────────────

interface ApiDetailProps {
  api: ApiDef
  flows: SavedFlow[]
  onRemove: () => void
  onUpdateDefaultFlow: (name: string) => void
  onSelectEndpoint: (epId: string) => void
  onAddEndpoint: () => void
  onNavigateToDesigner: (flowName?: string) => void
  onRemoveAlias: (index: number) => void
  rateLimitConfigs: string[]
  rateLimitError: boolean
  rateLimitCountBy: RateLimitCountBy | undefined
  rateLimitConfig: RateLimitConfigSource | undefined
  onUpdateRateLimitCountBy: (v: RateLimitCountBy | undefined) => void
  onUpdateRateLimitConfig: (v: RateLimitConfigSource | undefined) => void
  onCreateRateLimit: (name: string, perSec: number, perMin: number, burst: number) => Promise<void>
  onSync: () => void
  syncStatus: 'idle'|'syncing'|'done'|'error'
  constants: Record<string, string>
  onUpdateConstants: (c: Record<string, string>) => void
  upstreamUrl: UpstreamUrlConfig | undefined
  onUpdateUpstreamUrl: (u: UpstreamUrlConfig | undefined) => void
  // V2 rate limit fields
  rlCountBy: RateLimitCountByV2 | undefined
  rlConfigRef: RateLimitConfigRef | undefined
  upstreamService: string | undefined
  v2Configs: string[]
  onUpdateRlCountBy: (v: RateLimitCountByV2 | undefined) => void
  onUpdateRlConfigRef: (v: RateLimitConfigRef | undefined) => void
  onUpdateUpstreamService: (v: string | undefined) => void
  // Multi-entry rate limit policies
  rateLimitPolicies: APIRateLimitEntry[]
  skipRateLimit: boolean
  onUpdateRateLimitPolicies: (v: APIRateLimitEntry[]) => void
  onUpdateSkipRateLimit: (v: boolean) => void
  /** Warnings returned from the last sync for this API */
  rlWarnings: RateLimitWarning[]
}

function ApiDetailPanel({
  api, flows, onRemove, onUpdateDefaultFlow, onSelectEndpoint, onAddEndpoint, onNavigateToDesigner, onRemoveAlias,
  rateLimitConfigs, rateLimitError, rateLimitCountBy, rateLimitConfig, onUpdateRateLimitCountBy, onUpdateRateLimitConfig, onCreateRateLimit,
  onSync, syncStatus,
  constants, onUpdateConstants,
  upstreamUrl, onUpdateUpstreamUrl,
  rlCountBy, rlConfigRef, upstreamService, v2Configs, onUpdateRlCountBy, onUpdateRlConfigRef, onUpdateUpstreamService,
  rateLimitPolicies, skipRateLimit, onUpdateRateLimitPolicies, onUpdateSkipRateLimit,
  rlWarnings,
}: ApiDetailProps) {
  const [confirmRemove, setConfirmRemove] = useState(false)
  const flowNames = flows.map(f => f.name)

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      {/* Header */}
      <div style={{
        padding: '16px 20px',
        borderBottom: '1px solid var(--border)',
        background: 'var(--panel)',
        flexShrink: 0,
      }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
          <div>
            <div style={{ fontFamily: 'monospace', fontSize: 18, fontWeight: 700, color: 'var(--accent)', marginBottom: 3 }}>
              {api.basePath}
            </div>
            {(api.aliasPaths ?? []).length > 0 && (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 4 }}>
                {(api.aliasPaths ?? []).map((alias, i) => (
                  <span key={i} style={{
                    display: 'inline-flex', alignItems: 'center', gap: 4,
                    padding: '2px 8px', borderRadius: 10, fontSize: 11,
                    background: 'rgba(87,181,255,0.08)', border: '1px solid rgba(87,181,255,0.2)',
                    fontFamily: 'monospace', color: 'var(--muted)',
                  }}>
                    {alias}
                    <button
                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', padding: 0, fontSize: 12, lineHeight: 1 }}
                      onClick={() => onRemoveAlias(i)}
                    >×</button>
                  </span>
                ))}
              </div>
            )}
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{api.name}</div>
          </div>
          <div style={{ display: 'flex', gap: 8, flexShrink: 0, alignItems: 'center' }}>
            <button
              className="btn"
              style={{
                width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12,
                background: syncStatus === 'done' ? '#22c55e' : syncStatus === 'error' ? '#ef4444' : undefined,
                opacity: syncStatus === 'syncing' ? 0.7 : 1,
              }}
              onClick={onSync}
              disabled={syncStatus === 'syncing'}
              title="Sync only this API to the gateway"
            >
              {syncStatus === 'syncing' ? 'Syncing…' : syncStatus === 'done' ? '✓ Synced' : syncStatus === 'error' ? '✗ Error' : '↑ Sync'}
            </button>
            {confirmRemove ? (
              <>
                <span style={{ fontSize: 12, color: '#ef4444', alignSelf: 'center' }}>Remove API?</span>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 12 }}
                  onClick={() => setConfirmRemove(false)}
                >Cancel</button>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 12, background: '#ef4444' }}
                  onClick={onRemove}
                >Confirm</button>
              </>
            ) : (
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12 }}
                onClick={() => setConfirmRemove(true)}
              >Remove API</button>
            )}
          </div>
        </div>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>
        {/* Default flow */}
        <Section label="Default Flow">
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ fontSize: 12, color: 'var(--muted)', flexShrink: 0 }}>Default:</span>
            <div style={{ flex: 1, maxWidth: 360 }}>
              <FlowSearchSelect
                flows={flows}
                value={api.defaultFlow}
                onChange={onUpdateDefaultFlow}
                placeholder="search or select a flow…"
              />
            </div>
          </div>
          {api.defaultFlow && !flowNames.includes(api.defaultFlow) && (
            <div style={{ fontSize: 11, color: '#f59e0b', marginTop: 6 }}>
              Flow "{api.defaultFlow}" not found in designer — build it first.
            </div>
          )}
        </Section>

        {/* Upstream URL */}
        <Section label="Upstream URL" style={{ marginTop: 20 }}>
          <UpstreamUrlEditor value={upstreamUrl} onChange={onUpdateUpstreamUrl} />
        </Section>

        {/* Rate limit */}
        <Section label="Rate Limit" style={{ marginTop: 20 }}>
          <RateLimitEditor
            rateLimitCountBy={rateLimitCountBy}
            rateLimitConfig={rateLimitConfig}
            rateLimitConfigs={rateLimitConfigs}
            rateLimitError={rateLimitError}
            isEndpoint={false}
            onUpdateCountBy={onUpdateRateLimitCountBy}
            onUpdateConfig={onUpdateRateLimitConfig}
            onCreateConfig={onCreateRateLimit}
          />
          <RateLimitV2Section
            rlCountBy={rlCountBy}
            rlConfigRef={rlConfigRef}
            upstreamService={upstreamService}
            v2Configs={v2Configs}
            isEndpoint={false}
            onUpdateCountBy={onUpdateRlCountBy}
            onUpdateConfigRef={onUpdateRlConfigRef}
            onUpdateUpstreamService={onUpdateUpstreamService}
          />
          <RateLimitPoliciesSection
            policies={rateLimitPolicies}
            skipRateLimit={skipRateLimit}
            v2Configs={v2Configs}
            isEndpoint={false}
            onUpdatePolicies={onUpdateRateLimitPolicies}
            onUpdateSkip={onUpdateSkipRateLimit}
            warnings={rlWarnings}
          />
        </Section>

        {/* Pre-set Variables */}
        <Section label="Pre-set Variables" style={{ marginTop: 20 }}>
          <ConstantsEditor constants={constants} onChange={onUpdateConstants} isEndpoint={false} />
        </Section>

        {/* Endpoints table */}
        <Section label="Endpoints" style={{ marginTop: 20 }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
            <thead>
              <tr style={{ color: 'var(--muted)', fontSize: 10, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Method</th>
                <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Sub-path</th>
                <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Flow</th>
                <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Override?</th>
              </tr>
            </thead>
            <tbody>
              {api.endpoints.map(ep => {
                const resolvedFn = resolveFlow(ep, api)
                return (
                  <tr
                    key={ep.id}
                    onClick={() => onSelectEndpoint(ep.id)}
                    style={{ cursor: 'pointer', borderTop: '1px solid var(--border)' }}
                    onMouseEnter={e => (e.currentTarget.style.background = 'rgba(87,181,255,0.05)')}
                    onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
                  >
                    <td style={{ padding: '7px 8px 7px 0' }}><MethodBadge method={ep.method} /></td>
                    <td style={{ padding: '7px 8px 7px 0', fontFamily: 'monospace', color: 'var(--text)' }}>
                      {ep.subPath}
                    </td>
                    <td style={{ padding: '7px 8px 7px 0', color: 'var(--muted)', maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {resolvedFn || <span style={{ color: '#ef4444' }}>(none)</span>}
                    </td>
                    <td style={{ padding: '7px 0 7px 0', color: ep.flowName ? '#fbbf24' : 'var(--muted)' }}>
                      {ep.flowName ? '★ yes' : '—'}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
          <button
            className="btn muted"
            style={{ width: 'auto', padding: '4px 14px', marginTop: 10, fontSize: 12 }}
            onClick={onAddEndpoint}
          >
            + Add Endpoint
          </button>
        </Section>

        {/* Navigate to designer */}
        <div style={{ marginTop: 20 }}>
          <button
            className="btn muted"
            style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
            onClick={() => onNavigateToDesigner(api.defaultFlow)}
          >
            → Edit Default Flow in Designer
          </button>
        </div>
      </div>
    </div>
  )
}

// ── Endpoint Detail Panel ─────────────────────────────────────────────────────

interface EndpointDetailProps {
  api: ApiDef
  endpoint: EndpointDef
  flows: SavedFlow[]
  onRemove: () => void
  onSetFlow: (flowName: string) => void
  onClearOverride: () => void
  onNavigateToDesigner: (flowName?: string) => void
  onNavigateToDeploy: () => void
  rateLimitConfigs: string[]
  rateLimitError: boolean
  rateLimitCountBy: RateLimitCountBy | undefined
  rateLimitConfig: RateLimitConfigSource | undefined
  onUpdateRateLimitCountBy: (v: RateLimitCountBy | undefined) => void
  onUpdateRateLimitConfig: (v: RateLimitConfigSource | undefined) => void
  onCreateRateLimit: (name: string, perSec: number, perMin: number, burst: number) => Promise<void>
  constants: Record<string, string>
  onUpdateConstants: (c: Record<string, string>) => void
  upstreamUrl: UpstreamUrlConfig | undefined
  onUpdateUpstreamUrl: (u: UpstreamUrlConfig | undefined) => void
  // V2 rate limit fields
  rlCountBy: RateLimitCountByV2 | undefined
  rlConfigRef: RateLimitConfigRef | undefined
  upstreamService: string | undefined
  v2Configs: string[]
  onUpdateRlCountBy: (v: RateLimitCountByV2 | undefined) => void
  onUpdateRlConfigRef: (v: RateLimitConfigRef | undefined) => void
  onUpdateUpstreamService: (v: string | undefined) => void
  // Multi-entry rate limit policies
  rateLimitPolicies: APIRateLimitEntry[]
  skipRateLimit: boolean
  onUpdateRateLimitPolicies: (v: APIRateLimitEntry[]) => void
  onUpdateSkipRateLimit: (v: boolean) => void
}

function EndpointDetailPanel({
  api, endpoint, flows, onRemove, onSetFlow, onClearOverride,
  onNavigateToDesigner, onNavigateToDeploy,
  rateLimitConfigs, rateLimitError, rateLimitCountBy, rateLimitConfig, onUpdateRateLimitCountBy, onUpdateRateLimitConfig, onCreateRateLimit,
  constants, onUpdateConstants,
  upstreamUrl, onUpdateUpstreamUrl,
  rlCountBy, rlConfigRef, upstreamService, v2Configs, onUpdateRlCountBy, onUpdateRlConfigRef, onUpdateUpstreamService,
  rateLimitPolicies, skipRateLimit, onUpdateRateLimitPolicies, onUpdateSkipRateLimit,
}: EndpointDetailProps) {
  const [showOverridePicker, setShowOverridePicker] = useState(false)
  const [confirmRemove,      setConfirmRemove]      = useState(false)

  const resolvedFn   = resolveFlow(endpoint, api)
  const resolvedFlow = flows.find(f => f.name === resolvedFn) ?? null
  const hasSteps     = !!(resolvedFlow && resolvedFlow.steps.length > 0)
  const fp           = fullPath(api.basePath, endpoint.subPath)

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
      {/* Header */}
      <div style={{
        padding: '16px 20px',
        borderBottom: '1px solid var(--border)',
        background: 'var(--panel)',
        flexShrink: 0,
      }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 4 }}>
              <MethodBadge method={endpoint.method} large />
              <span style={{ fontFamily: 'monospace', fontSize: 16, fontWeight: 600 }}>{fp}</span>
            </div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>
              Part of: <strong style={{ color: 'var(--text)' }}>{api.name}</strong>
              {'  '}
              Basepath: <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>{api.basePath}</span>
            </div>
          </div>
          <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
            {confirmRemove ? (
              <>
                <span style={{ fontSize: 12, color: '#ef4444', alignSelf: 'center' }}>Remove?</span>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 12 }}
                  onClick={() => setConfirmRemove(false)}
                >Cancel</button>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '4px 10px', marginTop: 0, fontSize: 12, background: '#ef4444' }}
                  onClick={onRemove}
                >Confirm</button>
              </>
            ) : (
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 12px', marginTop: 0, fontSize: 12 }}
                onClick={() => setConfirmRemove(true)}
              >Remove Endpoint</button>
            )}
          </div>
        </div>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>

        {/* Flow row */}
        <Section label="Flow">
          {endpoint.flowName ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              <div>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>Override: </span>
                <span style={{ fontFamily: 'monospace', color: '#fbbf24', fontSize: 12 }}>{endpoint.flowName}</span>
              </div>
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '3px 12px', marginTop: 0, fontSize: 11 }}
                onClick={onClearOverride}
              >
                Clear override (use {api.defaultFlow || 'default'})
              </button>
            </div>
          ) : (
            <div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', marginBottom: 8 }}>
                <div>
                  <span style={{ fontSize: 11, color: 'var(--muted)' }}>Inherited: </span>
                  <span style={{ fontFamily: 'monospace', color: 'var(--accent)', fontSize: 12 }}>
                    {api.defaultFlow || <span style={{ color: '#ef4444' }}>(no default flow)</span>}
                  </span>
                </div>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '3px 12px', marginTop: 0, fontSize: 11 }}
                  onClick={() => setShowOverridePicker(p => !p)}
                >
                  {showOverridePicker ? 'Cancel' : 'Set override'}
                </button>
              </div>
              {showOverridePicker && (
                <div style={{ maxWidth: 360 }}>
                  <FlowSearchSelect
                    flows={flows}
                    value={''}
                    onChange={fn => { onSetFlow(fn); setShowOverridePicker(false) }}
                    placeholder="search or select override flow…"
                  />
                </div>
              )}
            </div>
          )}
        </Section>

        {/* Upstream URL */}
        <Section label="Upstream URL" style={{ marginTop: 20 }}>
          <UpstreamUrlEditor
            value={endpoint.upstreamUrl}
            onChange={onUpdateUpstreamUrl}
            inheritLabel={
              api.upstreamUrl
                ? `Inherit from API (${SOURCE_LABELS[api.upstreamUrl.source]}: ${api.upstreamUrl.value || '…'})`
                : '— inherit from API (none set) —'
            }
          />
        </Section>

        {/* Rate limit */}
        <Section label="Rate Limit" style={{ marginTop: 20 }}>
          <RateLimitEditor
            rateLimitCountBy={rateLimitCountBy}
            rateLimitConfig={rateLimitConfig}
            rateLimitConfigs={rateLimitConfigs}
            rateLimitError={rateLimitError}
            isEndpoint={true}
            onUpdateCountBy={onUpdateRateLimitCountBy}
            onUpdateConfig={onUpdateRateLimitConfig}
            onCreateConfig={onCreateRateLimit}
          />
          <RateLimitV2Section
            rlCountBy={rlCountBy}
            rlConfigRef={rlConfigRef}
            upstreamService={upstreamService}
            v2Configs={v2Configs}
            isEndpoint={true}
            onUpdateCountBy={onUpdateRlCountBy}
            onUpdateConfigRef={onUpdateRlConfigRef}
            onUpdateUpstreamService={onUpdateUpstreamService}
          />
          <RateLimitPoliciesSection
            policies={rateLimitPolicies}
            skipRateLimit={skipRateLimit}
            v2Configs={v2Configs}
            isEndpoint={true}
            onUpdatePolicies={onUpdateRateLimitPolicies}
            onUpdateSkip={onUpdateSkipRateLimit}
          />
        </Section>

        {/* Pre-set Variables */}
        <Section label="Pre-set Variables" style={{ marginTop: 20 }}>
          <ConstantsEditor constants={constants} onChange={onUpdateConstants} isEndpoint={true} />
        </Section>

        {/* Flow states */}
        {!resolvedFlow && (
          <div style={{
            marginTop: 16, padding: '14px 16px',
            borderRadius: 8,
            background: 'rgba(239,68,68,0.06)',
            border: '1px solid rgba(239,68,68,0.2)',
          }}>
            <p style={{ fontSize: 13, color: 'var(--muted)', marginBottom: 10 }}>
              Flow "{resolvedFn}" not found — build it in the designer.
            </p>
            <button
              className="btn"
              style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
              onClick={() => onNavigateToDesigner(resolvedFn)}
            >
              + Create flow in Designer
            </button>
          </div>
        )}

        {resolvedFlow && !hasSteps && (
          <div style={{
            marginTop: 16, padding: '12px 16px',
            borderRadius: 8,
            background: 'rgba(251,191,36,0.06)',
            border: '1px solid rgba(251,191,36,0.25)',
          }}>
            <p style={{ fontSize: 13, color: '#fbbf24', marginBottom: 10 }}>
              Flow "{resolvedFlow.name}" has no steps yet.
            </p>
            <button
              className="btn"
              style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
              onClick={() => onNavigateToDesigner(resolvedFn)}
            >
              Build this flow in Designer →
            </button>
          </div>
        )}

        {/* Pipeline preview */}
        {resolvedFlow && hasSteps && (
          <Section label="Pipeline Preview" style={{ marginTop: 20 }}>
            <MiniPipeline method={endpoint.method} path={fp} steps={resolvedFlow.steps} />
            <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
                onClick={() => onNavigateToDesigner(resolvedFn)}
              >
                Edit Flow in Designer →
              </button>
              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12 }}
                onClick={onNavigateToDeploy}
              >
                Go to Deploy →
              </button>
            </div>
          </Section>
        )}

        {/* API Interface */}
        {resolvedFlow && hasSteps && (() => {
          const { inputs, outputs } = deriveApiInterface(resolvedFlow.steps)
          if (inputs.length === 0 && outputs.length === 0) return null
          return (
            <Section label="API Interface" style={{ marginTop: 20 }}>
              {inputs.length > 0 && (
                <div style={{ marginBottom: outputs.length > 0 ? 14 : 0 }}>
                  <div style={{ fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>
                    Expects (inputs)
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '56px 1fr 1fr', gap: '3px 8px', fontSize: 11 }}>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>SOURCE</span>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>FIELD</span>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>→ VARIABLE</span>
                    {inputs.map((b, i) => (
                      <>
                        <span key={`s${i}`} style={{
                          padding: '2px 5px', borderRadius: 4, fontSize: 10, fontWeight: 700,
                          background: b.source === 'header' ? 'rgba(87,181,255,0.15)' : b.source === 'body' ? 'rgba(52,211,153,0.15)' : 'rgba(251,191,36,0.15)',
                          color:      b.source === 'header' ? '#57b5ff'              : b.source === 'body' ? '#34d399'              : '#fbbf24',
                          alignSelf: 'center',
                        }}>{b.source}</span>
                        <code key={`f${i}`} style={{ fontFamily: 'monospace', color: 'var(--text)', alignSelf: 'center' }}>{b.field || '—'}</code>
                        <code key={`v${i}`} style={{ fontFamily: 'monospace', color: '#a78bfa', alignSelf: 'center' }}>{b.variable || '—'}</code>
                      </>
                    ))}
                  </div>
                </div>
              )}
              {outputs.length > 0 && (
                <div>
                  <div style={{ fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }}>
                    Returns (outputs)
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '120px 1fr', gap: '3px 8px', fontSize: 11 }}>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>ACTION</span>
                    <span style={{ color: 'var(--muted)', fontSize: 10 }}>VALUE</span>
                    {outputs.map((o, i) => (
                      <>
                        <span key={`oa${i}`} style={{ color: 'var(--muted)', alignSelf: 'center' }}>{o.action}</span>
                        <code key={`ov${i}`} style={{ fontFamily: 'monospace', color: '#34d399', alignSelf: 'center' }}>{o.value || '—'}</code>
                      </>
                    ))}
                  </div>
                </div>
              )}
            </Section>
          )
        })()}

        {/* Try it — curl snippet */}
        {resolvedFlow && hasSteps && (() => {
          const { inputs } = deriveApiInterface(resolvedFlow.steps)
          const headerInputs = inputs.filter(b => b.source === 'header')
          const bodyInputs   = inputs.filter(b => b.source === 'body')
          const queryInputs  = inputs.filter(b => b.source === 'query')

          const method   = endpoint.method ?? 'POST'
          const curlPath = fp.replace(/\{(\w+)\}/g, '<$1>')
          const bodyObj  = bodyInputs.length > 0
            ? JSON.stringify(Object.fromEntries(bodyInputs.map(b => [b.field, `<${b.field}>`])), null, 2)
            : (method !== 'GET' ? '{}' : null)
          const queryStr = queryInputs.length > 0
            ? '?' + queryInputs.map(b => `${b.field}=<${b.field}>`).join('&')
            : ''

          const curl = [
            `curl -X ${method} http://localhost:8080${curlPath}${queryStr}`,
            `  -H 'Content-Type: application/json'`,
            ...headerInputs.map(b => `  -H '${b.field}: <${b.field.toLowerCase().replace(/[^a-z0-9]/g, '_')}>'`),
            ...(bodyObj ? [`  -d '${bodyObj}'`] : []),
          ].join(' \\\n')

          return (
            <div style={{ marginTop: 16, marginBottom: 8 }}>
              <details style={{ borderRadius: 8, border: '1px solid var(--border)', overflow: 'hidden' }}>
                <summary style={{
                  padding: '10px 14px', cursor: 'pointer', fontSize: 12,
                  color: 'var(--muted)', background: 'var(--panel)',
                  userSelect: 'none', listStyle: 'none',
                  display: 'flex', alignItems: 'center', gap: 6,
                }}>
                  <span style={{ fontSize: 10, opacity: 0.6 }}>▶</span>
                  Try it — curl snippet
                </summary>
                <div style={{ padding: '14px 14px 16px', background: 'var(--bg)' }}>
                  <pre style={{
                    fontFamily: 'monospace', fontSize: 11, color: 'var(--text)',
                    background: 'rgba(0,0,0,0.2)', borderRadius: 6, padding: '10px 12px',
                    overflowX: 'auto', margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                  }}>{curl}</pre>
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '3px 12px', marginTop: 10, fontSize: 11 }}
                    onClick={() => navigator.clipboard.writeText(curl).catch(() => {})}
                  >
                    Copy
                  </button>
                  <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 8, opacity: 0.7 }}>
                    Replace &lt;placeholders&gt; with real values. Adjust host if not running locally.
                  </div>
                </div>
              </details>
            </div>
          )
        })()}

      </div>
    </div>
  )
}

// ── Mini pipeline preview ────────────────────────────────────────────────────

function MiniPipeline({ method, path, steps }: { method: string; path: string; steps: FlowStep[] }) {
  type RenderedItem =
    | { kind: 'zone-header'; zone: Zone }
    | { kind: 'step'; step: FlowStep; zone: Zone }
    | { kind: 'boundary' }

  const items: RenderedItem[] = []
  let lastZone: Zone | null   = null
  let hasResponseZone         = false

  steps.forEach(step => {
    const zone = stepZone(step.action)
    if (zone !== lastZone) {
      items.push({ kind: 'zone-header', zone })
      lastZone = zone
    }
    items.push({ kind: 'step', step, zone })
    if (zone === 'response' && !hasResponseZone) hasResponseZone = true
  })

  // Insert boundary after last response-zone step
  let boundaryInserted = false
  const finalItems: RenderedItem[] = []
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i]
    if (!boundaryInserted && item.kind === 'step' && item.zone === 'response') {
      finalItems.unshift({ kind: 'boundary' })
      boundaryInserted = true
    }
    finalItems.unshift(item)
  }

  return (
    <div style={{
      background: 'rgba(0,0,0,0.2)',
      borderRadius: 8,
      border: '1px solid var(--border)',
      overflow: 'hidden',
      fontFamily: 'monospace',
      fontSize: 12,
    }}>
      {/* Request line */}
      <div style={{
        padding: '7px 12px',
        background: 'rgba(87,181,255,0.08)',
        borderBottom: '1px solid var(--border)',
        fontSize: 11,
        color: 'var(--muted)',
        display: 'flex', alignItems: 'center', gap: 8,
      }}>
        <span style={{ color: '#57b5ff' }}>↓ REQUEST IN</span>
        <MethodBadge method={method} />
        <span style={{ color: 'var(--text)' }}>{path}</span>
      </div>

      {/* Steps */}
      {finalItems.map((item, i) => {
        if (item.kind === 'zone-header') {
          const meta = ZONE_META[item.zone]
          return (
            <div key={`zh-${i}`} style={{
              padding: '4px 12px',
              background: `${meta.color}14`,
              borderTop: i > 0 ? `1px solid ${meta.color}30` : undefined,
              borderBottom: `1px solid ${meta.color}30`,
              display: 'flex', alignItems: 'center', gap: 6,
            }}>
              <span style={{ fontSize: 10 }}>{meta.icon}</span>
              <span style={{
                fontSize: 9, fontWeight: 700, letterSpacing: 1,
                color: meta.color, textTransform: 'uppercase', fontFamily: 'sans-serif',
              }}>
                {meta.label}
              </span>
            </div>
          )
        }

        if (item.kind === 'boundary') {
          return (
            <div key={`boundary-${i}`} style={{
              padding: '5px 12px',
              borderTop: '1px dashed #34d39966',
              borderBottom: '1px dashed #34d39966',
              background: 'rgba(52,211,153,0.04)',
              fontSize: 10,
              color: '#34d399',
              display: 'flex', alignItems: 'center', gap: 6,
              fontFamily: 'sans-serif',
            }}>
              <span>↩</span>
              <span style={{ letterSpacing: 0.5 }}>CLIENT RECEIVES RESPONSE</span>
            </div>
          )
        }

        const { step, zone } = item
        const meta = ZONE_META[zone]
        const inputRef  = (step.key_identifier || step.source) as string | undefined
        const outputRef = step.as as string | undefined

        let conditionText: string | null = null
        if (step.action === 'if' && step.condition) {
          const thenStr = step.then_flow ? `✓ ${step.then_flow}` : ''
          const elseStr = step.else_flow ? `✗ ${step.else_flow}` : ''
          conditionText = `${step.condition}  ${thenStr} ${elseStr}`.trim()
        }

        return (
          <div key={`step-${i}`} style={{
            padding: '4px 12px 4px 0',
            display: 'flex', alignItems: 'baseline', gap: 0,
            borderBottom: '1px solid rgba(255,255,255,0.03)',
          }}>
            <div style={{
              width: 3, alignSelf: 'stretch', flexShrink: 0,
              background: meta.color,
              marginRight: 10,
              opacity: 0.6,
            }} />
            <span style={{ color: meta.color, marginRight: 6, fontSize: 10, flexShrink: 0 }}>•</span>
            <span style={{ color: 'var(--text)', fontWeight: 500, flexShrink: 0 }}>
              {step.action}
            </span>
            {inputRef && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11 }}>
                ← <span style={{ color: '#57b5ff' }}>{inputRef}</span>
              </span>
            )}
            {outputRef && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11 }}>
                → <span style={{ color: '#34d399' }}>{outputRef}</span>
              </span>
            )}
            {conditionText && (
              <span style={{ color: 'var(--muted)', marginLeft: 8, fontSize: 11, fontStyle: 'italic' }}>
                {conditionText}
              </span>
            )}
          </div>
        )
      })}

      {!hasResponseZone && (
        <div style={{
          padding: '5px 12px',
          borderTop: '1px dashed #34d39966',
          background: 'rgba(52,211,153,0.04)',
          fontSize: 10,
          color: '#34d399',
          display: 'flex', alignItems: 'center', gap: 6,
          fontFamily: 'sans-serif',
        }}>
          <span>↩</span>
          <span style={{ letterSpacing: 0.5 }}>CLIENT RECEIVES RESPONSE</span>
        </div>
      )}
    </div>
  )
}

// ── Wizard panel ─────────────────────────────────────────────────────────────

interface WizardProps {
  apis: ApiDef[]
  flows: SavedFlow[]
  wizardStep: 1 | 2 | 3
  wizardApiId: string | null
  wBasePaths: string[]
  wApiName: string
  wDefaultFlow: string
  wFlowMode: 'create' | 'existing'
  wEndpoints: Array<{ id: string; subPath: string; method: string; flowName?: string; overrideFlow: boolean }>
  wizardErr: string
  setWBasePaths: (v: string[]) => void
  onPrimaryBasePathChange: (v: string) => void
  setWApiName: (v: string) => void
  setWDefaultFlow: (v: string) => void
  setWFlowMode: (v: 'create' | 'existing') => void
  setWEndpoints: (v: Array<{ id: string; subPath: string; method: string; flowName?: string; overrideFlow: boolean }>) => void
  onStep1Next: () => void
  onStep2Next: () => void
  onBack: () => void
  onConfirm: () => void
  onCancel: () => void
}

function WizardPanel({
  apis, flows,
  wizardStep, wizardApiId,
  wBasePaths, wApiName, wDefaultFlow, wFlowMode, wEndpoints, wizardErr,
  setWBasePaths, onPrimaryBasePathChange, setWApiName, setWDefaultFlow, setWFlowMode, setWEndpoints,
  onStep1Next, onStep2Next, onBack, onConfirm, onCancel,
}: WizardProps) {
  const isAddEndpointMode = wizardApiId !== null

  function addEndpointRow() {
    setWEndpoints([
      ...wEndpoints,
      { id: crypto.randomUUID(), subPath: '/', method: 'GET', overrideFlow: false },
    ])
  }

  function updateEndpoint(id: string, patch: Partial<typeof wEndpoints[0]>) {
    setWEndpoints(wEndpoints.map(e => e.id === id ? { ...e, ...patch } : e))
  }

  function removeEndpointRow(id: string) {
    if (wEndpoints.length <= 1) return
    setWEndpoints(wEndpoints.filter(e => e.id !== id))
  }

  const stepCount = isAddEndpointMode ? 2 : 3
  const stepLabel = isAddEndpointMode
    ? (wizardStep === 2 ? 'Step 1 of 2 — Endpoints' : 'Step 2 of 2 — Review')
    : (wizardStep === 1 ? 'Step 1 of 3 — API Identity'
      : wizardStep === 2 ? 'Step 2 of 3 — Endpoints'
      : 'Step 3 of 3 — Review')

  const displayStep = isAddEndpointMode ? wizardStep - 1 : wizardStep

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '40px 20px' }}>
      <div style={{
        width: '100%', maxWidth: 560,
        background: 'var(--panel)',
        borderRadius: 10,
        border: '1px solid var(--border)',
        overflow: 'hidden',
      }}>
        {/* Header */}
        <div style={{
          padding: '16px 20px',
          borderBottom: '1px solid var(--border)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <div>
            <div style={{ fontSize: 14, fontWeight: 600, marginBottom: 4 }}>
              {stepLabel}
            </div>
            <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
              {Array.from({ length: stepCount }, (_, n) => (
                <div key={n} style={{
                  width: 20, height: 4, borderRadius: 2,
                  background: n < displayStep ? 'var(--accent)' : 'rgba(255,255,255,0.1)',
                  transition: 'background 0.2s',
                }} />
              ))}
            </div>
          </div>
          <button
            style={{ background: 'none', border: 'none', color: 'var(--muted)', cursor: 'pointer', fontSize: 18, lineHeight: 1 }}
            onClick={onCancel}
            title="Cancel"
          >×</button>
        </div>

        <div style={{ padding: '20px 20px 24px' }}>
          {/* Step 1: API Identity */}
          {wizardStep === 1 && (
            <>
              <div style={{ marginBottom: 14 }}>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 5 }}>
                  Basepaths
                  <span style={{ fontSize: 10, marginLeft: 8, opacity: 0.6 }}>first = primary · extras = aliases (same API, same endpoints)</span>
                </div>
                {wBasePaths.map((bp, i) => (
                  <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
                    <input
                      className="input"
                      placeholder={i === 0 ? '/api/v1/users' : '/v1/users  (alias)'}
                      value={bp}
                      onChange={e => {
                        const updated = [...wBasePaths]
                        updated[i] = e.target.value
                        setWBasePaths(updated)
                        if (i === 0) onPrimaryBasePathChange(e.target.value)
                      }}
                      onKeyDown={e => e.key === 'Enter' && onStep1Next()}
                      style={{ flex: 1, marginTop: 0, fontFamily: 'monospace' }}
                      autoFocus={i === 0}
                    />
                    {wBasePaths.length > 1 && (
                      <button
                        className="btn muted"
                        style={{ width: 'auto', padding: '0 10px', marginTop: 0, fontSize: 16, flexShrink: 0 }}
                        onClick={() => setWBasePaths(wBasePaths.filter((_, j) => j !== i))}
                      >×</button>
                    )}
                  </div>
                ))}
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '3px 12px', marginTop: 2, fontSize: 11 }}
                  onClick={() => setWBasePaths([...wBasePaths, ''])}
                >+ Add basepath</button>
              </div>

              <div style={{ marginBottom: 14 }}>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 5 }}>
                  API Name <span style={{ fontSize: 10, opacity: 0.6 }}>(auto-suggested)</span>
                </div>
                <input
                  className="input"
                  placeholder="users_api"
                  value={wApiName}
                  onChange={e => setWApiName(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && onStep1Next()}
                  style={{ width: '100%', marginTop: 0 }}
                />
                {wApiName && apis.some(a => a.name === wApiName) && (
                  <div style={{ fontSize: 11, color: '#f97316', marginTop: 4 }}>
                    Name "{wApiName}" already exists
                  </div>
                )}
              </div>

              <div style={{ marginBottom: 16 }}>
                <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 8 }}>Default Flow</div>

                <label style={{
                  display: 'flex', gap: 10, alignItems: 'flex-start',
                  padding: '11px 14px', borderRadius: 8, cursor: 'pointer',
                  marginBottom: 8,
                  background: wFlowMode === 'create' ? 'rgba(87,181,255,0.08)' : 'var(--step-bg)',
                  border: wFlowMode === 'create' ? '1px solid var(--accent)' : '1px solid transparent',
                }}>
                  <input type="radio" checked={wFlowMode === 'create'} onChange={() => setWFlowMode('create')} style={{ marginTop: 2 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, marginBottom: 4 }}>Create new flow</div>
                    {wFlowMode === 'create' && (
                      <input
                        className="input"
                        placeholder="my_api_flow"
                        value={wDefaultFlow}
                        onChange={e => setWDefaultFlow(e.target.value)}
                        style={{ width: '100%', marginTop: 0 }}
                        onClick={e => e.stopPropagation()}
                      />
                    )}
                    {wFlowMode !== 'create' && (
                      <div style={{ fontSize: 11, color: 'var(--muted)' }}>Opens Designer to build from scratch.</div>
                    )}
                  </div>
                </label>

                <label style={{
                  display: 'flex', gap: 10, alignItems: 'flex-start',
                  padding: '11px 14px', borderRadius: 8, cursor: 'pointer',
                  background: wFlowMode === 'existing' ? 'rgba(87,181,255,0.08)' : 'var(--step-bg)',
                  border: wFlowMode === 'existing' ? '1px solid var(--accent)' : '1px solid transparent',
                }}>
                  <input type="radio" checked={wFlowMode === 'existing'} onChange={() => setWFlowMode('existing')} style={{ marginTop: 2 }} />
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, marginBottom: 4 }}>Use existing flow</div>
                    {wFlowMode === 'existing' && (
                      <div onClick={e => e.stopPropagation()}>
                        <FlowSearchSelect
                          flows={flows}
                          value={wDefaultFlow}
                          onChange={setWDefaultFlow}
                          placeholder="search or select a flow…"
                        />
                      </div>
                    )}
                    {wFlowMode !== 'existing' && (
                      <div style={{ fontSize: 11, color: 'var(--muted)' }}>Route to an already-built flow.</div>
                    )}
                  </div>
                </label>
              </div>

              {wizardErr && (
                <div style={{ fontSize: 12, color: '#ef4444', marginBottom: 12 }}>{wizardErr}</div>
              )}

              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '6px 20px', marginTop: 0 }}
                  onClick={onStep1Next}
                >
                  Next →
                </button>
              </div>
            </>
          )}

          {/* Step 2: Endpoints */}
          {wizardStep === 2 && (
            <>
              {/* Base path read-only prefix */}
              <div style={{
                padding: '8px 12px', marginBottom: 16, borderRadius: 6,
                background: 'rgba(87,181,255,0.06)', border: '1px solid rgba(87,181,255,0.2)',
                fontSize: 12, color: 'var(--muted)',
              }}>
                Base path: <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>{wBasePaths[0]}</span>{wBasePaths.length > 1 && <span style={{ color: 'var(--muted)', fontSize: 10, marginLeft: 4 }}>+{wBasePaths.length - 1} alias{wBasePaths.length > 2 ? 'es' : ''}</span>}
                {!isAddEndpointMode && wDefaultFlow && (
                  <span> · Default flow: <span style={{ color: 'var(--text)' }}>{wDefaultFlow}</span></span>
                )}
              </div>

              {wEndpoints.map((ep, idx) => (
                <div key={ep.id} style={{
                  padding: '12px 14px', marginBottom: 10,
                  borderRadius: 8, border: '1px solid var(--border)',
                  background: 'var(--step-bg)',
                }}>
                  <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end', marginBottom: 8 }}>
                    {/* Method */}
                    <div style={{ flexShrink: 0 }}>
                      <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 4 }}>Method</div>
                      <select
                        className="input"
                        value={ep.method}
                        onChange={e => updateEndpoint(ep.id, { method: e.target.value })}
                        style={{ width: 90, marginTop: 0 }}
                      >
                        {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => <option key={m}>{m}</option>)}
                      </select>
                    </div>
                    {/* Sub-path */}
                    <div style={{ flex: 1 }}>
                      <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 4 }}>
                        Sub-path
                        <span style={{ color: 'var(--muted)', fontSize: 10, marginLeft: 8 }}>
                          preview: <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>
                            {fullPath(wBasePaths[0], ep.subPath)}
                          </span>
                        </span>
                      </div>
                      <input
                        className="input"
                        placeholder="/"
                        value={ep.subPath}
                        onChange={e => updateEndpoint(ep.id, { subPath: e.target.value })}
                        style={{ width: '100%', marginTop: 0 }}
                      />
                    </div>
                    {wEndpoints.length > 1 && (
                      <button
                        className="btn muted"
                        style={{ width: 'auto', padding: '4px 8px', marginTop: 0, fontSize: 12, flexShrink: 0 }}
                        onClick={() => removeEndpointRow(ep.id)}
                        title="Remove row"
                      >✕</button>
                    )}
                  </div>

                  {/* Optional flow override */}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <label style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 12, color: 'var(--muted)' }}>
                      <input
                        type="checkbox"
                        checked={ep.overrideFlow}
                        onChange={e => updateEndpoint(ep.id, { overrideFlow: e.target.checked, flowName: e.target.checked ? ep.flowName : undefined })}
                      />
                      Override flow for this endpoint
                    </label>
                  </div>
                  {ep.overrideFlow && (
                    <div style={{ marginTop: 8 }}>
                      <FlowSearchSelect
                        flows={flows}
                        value={ep.flowName ?? ''}
                        onChange={fn => updateEndpoint(ep.id, { flowName: fn })}
                        placeholder="search or select override flow…"
                      />
                    </div>
                  )}

                  {/* Resolved path hint */}
                  {idx === wEndpoints.length - 1 && (
                    <div style={{ fontSize: 10, color: 'var(--muted)', marginTop: 6 }}>
                      Full path: <span style={{ fontFamily: 'monospace' }}>{fullPath(wBasePaths[0], ep.subPath)}</span>
                    </div>
                  )}
                </div>
              ))}

              <button
                className="btn muted"
                style={{ width: 'auto', padding: '4px 14px', marginTop: 0, fontSize: 12, marginBottom: 16 }}
                onClick={addEndpointRow}
              >
                + Add endpoint
              </button>

              {wizardErr && (
                <div style={{ fontSize: 12, color: '#ef4444', marginBottom: 12 }}>{wizardErr}</div>
              )}

              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                {!isAddEndpointMode && (
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '6px 16px', marginTop: 0 }}
                    onClick={onBack}
                  >
                    ← Back
                  </button>
                )}
                {isAddEndpointMode && <div />}
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '6px 20px', marginTop: 0 }}
                  onClick={onStep2Next}
                >
                  Next →
                </button>
              </div>
            </>
          )}

          {/* Step 3: Review */}
          {wizardStep === 3 && (
            <>
              <div style={{ marginBottom: 16 }}>
                <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 10 }}>
                  API: <span style={{ color: 'var(--text)', fontWeight: 600 }}>{wApiName}</span>
                  {'  '}
                  Base path: <span style={{ fontFamily: 'monospace', color: 'var(--accent)' }}>{wBasePaths[0]}</span>{wBasePaths.length > 1 && <span style={{ color: 'var(--muted)', fontSize: 10, marginLeft: 4 }}>+{wBasePaths.length - 1} alias{wBasePaths.length > 2 ? 'es' : ''}</span>}
                  {'  '}
                  Default flow: <span style={{ color: 'var(--text)' }}>{wDefaultFlow}</span>
                  {wFlowMode === 'create' && <span style={{ color: '#fbbf24', marginLeft: 4 }}>(will be created)</span>}
                </div>

                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                  <thead>
                    <tr style={{ color: 'var(--muted)', fontSize: 10, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                      <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Method</th>
                      <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Full Path</th>
                      <th style={{ textAlign: 'left', padding: '4px 8px 8px 0' }}>Flow</th>
                    </tr>
                  </thead>
                  <tbody>
                    {wEndpoints.map(ep => (
                      <tr key={ep.id} style={{ borderTop: '1px solid var(--border)' }}>
                        <td style={{ padding: '7px 8px 7px 0' }}><MethodBadge method={ep.method} /></td>
                        <td style={{ padding: '7px 8px 7px 0', fontFamily: 'monospace', color: 'var(--text)' }}>
                          {fullPath(wBasePaths[0], ep.subPath)}
                        </td>
                        <td style={{ padding: '7px 0 7px 0', color: ep.overrideFlow && ep.flowName ? '#fbbf24' : 'var(--muted)' }}>
                          {ep.overrideFlow && ep.flowName
                            ? `★ ${ep.flowName}`
                            : wDefaultFlow || '(none)'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {wizardErr && (
                <div style={{ fontSize: 12, color: '#ef4444', marginBottom: 12 }}>{wizardErr}</div>
              )}

              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '6px 16px', marginTop: 0 }}
                  onClick={onBack}
                >
                  ← Back
                </button>
                <button
                  className="btn"
                  style={{ width: 'auto', padding: '6px 20px', marginTop: 0 }}
                  onClick={onConfirm}
                >
                  Confirm & Save
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

// ── Small helpers ────────────────────────────────────────────────────────────

function MethodBadge({ method, large }: { method: string; large?: boolean }) {
  return (
    <span style={{
      fontSize: large ? 12 : 10, fontWeight: 700,
      padding: large ? '3px 9px' : '2px 7px',
      borderRadius: 4,
      color: '#fff',
      background: METHOD_COLOR[method] ?? '#64748b',
      minWidth: large ? 55 : 50, textAlign: 'center',
      letterSpacing: 0.4, flexShrink: 0,
      display: 'inline-block',
    }}>
      {method}
    </span>
  )
}

function Section({ label, children, style }: { label: string; children: React.ReactNode; style?: React.CSSProperties }) {
  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14, ...style }}>
      <div style={{
        fontSize: 10, color: 'var(--muted)', textTransform: 'uppercase',
        letterSpacing: 1, marginBottom: 10,
      }}>
        {label}
      </div>
      {children}
    </div>
  )
}
