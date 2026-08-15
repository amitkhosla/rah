import type {
  SchemaResponse,
  TargetsResponse,
  DeployRequest,
  DeployResponse,
  OpenAPIImportResponse,
  GatewayState,
  TenantListResponse,
  TenantDetail,
  RateLimitListResponse,
  RateLimitRecord,
  UpsertTenantRequest,
  UpsertRateLimitRequest,
  RateLimitConfigV2,
  RateLimitConfigV2ListResponse,
  TierDef,
  TierListResponse,
  UpstreamServiceDef,
  UpstreamServiceListResponse,
  CredentialListResponse,
  AIEnvelope,
  LLMModel,
  LLMTestResult,
  LLMTestDebug,
  MCPServer,
  MCPPingResult,
  APIToolDef,
  VirtualMCPServer,
  RateLimitWarning,
  App,
  APIKeyView,
  APIKeyCreateResponse,
  EgressProfileConfig,
  EgressCodeRuleConfig,
  EgressPatternRuleConfig,
  FieldSchema,
  ReleaseRecord,
  ReleaseListResponse,
  CreateReleaseResponse,
  ReleaseDiff,
  ConcurrencyStatus,
  ConcurrencyPatch,
} from './types'

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, { credentials: 'include', ...options })
  if (!res.ok) {
    const text = await res.text().catch(() => `HTTP ${res.status}`)
    throw new Error(text || `HTTP ${res.status}`)
  }
  return res.json() as Promise<T>
}

export function fetchSchema(): Promise<SchemaResponse> {
  return request<SchemaResponse>('/api/schema')
}

export function fetchTargets(): Promise<TargetsResponse> {
  return request<TargetsResponse>('/api/targets')
}

export function deploy(body: DeployRequest): Promise<DeployResponse> {
  const normalized: DeployRequest = body.payload
    ? {
        ...body,
        payload: {
          ...body.payload,
          flows: body.payload.flows.map(f => ({ ...f, instructions: f.instructions.map(normalizeStep) })),
        },
      }
    : body
  return request<DeployResponse>('/api/deploy', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(normalized),
  })
}

export function importOpenAPI(spec: string): Promise<OpenAPIImportResponse> {
  return request<OpenAPIImportResponse>('/api/openapi/import', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ spec }),
  })
}

export function fetchGatewayApis(): Promise<GatewayState> {
  return request<GatewayState>('/api/getAllApis')
}

export function deleteFlow(name: string): Promise<void> {
  return request<void>(`/api/flows/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

// ── Tenant Management ─────────────────────────────────────────────

export function listTenants(cursor = 0, limit = 100): Promise<TenantListResponse> {
  return request<TenantListResponse>(`/api/tenants?cursor=${cursor}&limit=${limit}`)
}

export function getTenant(alias: string): Promise<TenantDetail> {
  return request<TenantDetail>(`/api/tenants/${encodeURIComponent(alias)}`)
}

export function upsertTenant(body: UpsertTenantRequest): Promise<void> {
  return request<void>('/api/tenants', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function deleteTenant(alias: string): Promise<void> {
  return request<void>(`/api/tenants/${encodeURIComponent(alias)}`, { method: 'DELETE' })
}

export function addAlias(existingAlias: string, newAlias: string): Promise<void> {
  return request<void>(`/api/tenants/${encodeURIComponent(existingAlias)}/aliases`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ alias: newAlias }),
  })
}

export function listRateLimitConfigs(): Promise<RateLimitListResponse> {
  return request<RateLimitListResponse>('/api/rate-limit-configs')
}

export function getRateLimitConfig(name: string): Promise<RateLimitRecord> {
  return request<RateLimitRecord>(`/api/rate-limit-configs/${encodeURIComponent(name)}`)
}

export function upsertRateLimitConfig(body: UpsertRateLimitRequest): Promise<void> {
  return request<void>('/api/rate-limit-configs', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// ── Rate Limit Configs V2 ──────────────────────────────────────────

export function listRateLimitConfigsV2(): Promise<RateLimitConfigV2ListResponse> {
  return request<RateLimitConfigV2ListResponse>('/api/rate-limit-configs-v2')
}

export function upsertRateLimitConfigV2(body: RateLimitConfigV2): Promise<void> {
  return request<void>('/api/rate-limit-configs-v2', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function deleteRateLimitConfigV2(name: string): Promise<void> {
  return request<void>(`/api/rate-limit-configs-v2/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

// ── Tenant Tiers ───────────────────────────────────────────────────

export function listTiers(): Promise<TierListResponse> {
  return request<TierListResponse>('/api/tiers')
}

export function upsertTier(body: TierDef): Promise<void> {
  return request<void>('/api/tiers', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function deleteTier(name: string): Promise<void> {
  return request<void>(`/api/tiers/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

// ── Upstream Services ──────────────────────────────────────────────

export function listUpstreamServices(): Promise<UpstreamServiceListResponse> {
  return request<UpstreamServiceListResponse>('/api/upstream-services')
}

export function upsertUpstreamService(body: UpstreamServiceDef): Promise<void> {
  return request<void>('/api/upstream-services', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function deleteUpstreamService(name: string): Promise<void> {
  return request<void>(`/api/upstream-services/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

// ── Credentials ────────────────────────────────────────────────────

export function listCredentials(alias: string): Promise<CredentialListResponse> {
  return request<CredentialListResponse>(`/api/tenants/${encodeURIComponent(alias)}/credentials`)
}

export function setCredential(alias: string, name: string, value: string): Promise<void> {
  return request<void>(`/api/tenants/${encodeURIComponent(alias)}/credentials/${encodeURIComponent(name)}`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ value }),
  })
}

export function deleteCredential(alias: string, name: string): Promise<void> {
  return request<void>(`/api/tenants/${encodeURIComponent(alias)}/credentials/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  })
}

// ── AI helpers ─────────────────────────────────────────────────────

// Unwraps the { ok, data, error } envelope returned by all /api/ai/* routes.
async function aiReq<T>(url: string, options?: RequestInit): Promise<T> {
  const env = await request<AIEnvelope<T>>(url, options)
  if (!env.ok) throw new Error(env.error ?? 'AI request failed')
  return env.data as T
}

const AI_JSON = { 'content-type': 'application/json' }

// ── LLM Models ─────────────────────────────────────────────────────

export function listLLMModels(): Promise<LLMModel[]> {
  return aiReq<LLMModel[]>('/api/ai/llm/models')
}

export function upsertLLMModel(model: LLMModel): Promise<LLMModel> {
  return aiReq<LLMModel>('/api/ai/llm/models', {
    method: 'POST',
    headers: AI_JSON,
    body: JSON.stringify(model),
  })
}

export function deleteLLMModel(alias: string): Promise<void> {
  return aiReq<void>(`/api/ai/llm/models/${encodeURIComponent(alias)}`, { method: 'DELETE' })
}

// LLMTestResponse is the full envelope from the test endpoint.
// On failure it includes a debug object with endpoint, model_id_sent, http_status, response_body.
export interface LLMTestResponse {
  ok: boolean
  data?: LLMTestResult
  error?: string
  debug?: LLMTestDebug
}

export async function testLLMModel(
  alias: string,
  prompt = 'Say OK',
  maxTokens = 10,
): Promise<LLMTestResponse> {
  // Use raw request — we want the full envelope including debug on failure,
  // not just the error string that aiReq() would throw.
  const env = await request<LLMTestResponse>(
    `/api/ai/llm/models/${encodeURIComponent(alias)}/test`,
    { method: 'POST', headers: AI_JSON, body: JSON.stringify({ prompt, max_tokens: maxTokens }) },
  )
  return env
}

// ── MCP Servers (external) ──────────────────────────────────────────

export function listMCPServers(): Promise<MCPServer[]> {
  return aiReq<MCPServer[]>('/api/ai/mcp/servers')
}

export function upsertMCPServer(srv: MCPServer): Promise<MCPServer> {
  return aiReq<MCPServer>('/api/ai/mcp/servers', {
    method: 'POST',
    headers: AI_JSON,
    body: JSON.stringify(srv),
  })
}

export function deleteMCPServer(alias: string): Promise<void> {
  return aiReq<void>(`/api/ai/mcp/servers/${encodeURIComponent(alias)}`, { method: 'DELETE' })
}

export function pingMCPServer(alias: string): Promise<MCPPingResult> {
  return aiReq<MCPPingResult>(`/api/ai/mcp/servers/${encodeURIComponent(alias)}/ping`, { method: 'POST' })
}

export function probeMCPTools(alias: string): Promise<unknown[]> {
  return aiReq<unknown[]>(`/api/ai/mcp/servers/${encodeURIComponent(alias)}/tools`)
}

// ── API Tools ──────────────────────────────────────────────────────

export function listAPITools(): Promise<APIToolDef[]> {
  return aiReq<APIToolDef[]>('/api/ai/tools/apis')
}

export function upsertAPITool(tool: APIToolDef): Promise<APIToolDef> {
  return aiReq<APIToolDef>('/api/ai/tools/apis', {
    method: 'POST',
    headers: AI_JSON,
    body: JSON.stringify(tool),
  })
}

export function deleteAPITool(name: string): Promise<void> {
  return aiReq<void>(`/api/ai/tools/apis/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

// ── Virtual MCP Servers ────────────────────────────────────────────

export function listVirtualMCPServers(tenantId = 0): Promise<VirtualMCPServer[]> {
  return aiReq<VirtualMCPServer[]>(`/api/ai/mcp/virtual${tenantId ? `?tenant_id=${tenantId}` : ''}`)
}

export function upsertVirtualMCPServer(def: VirtualMCPServer): Promise<VirtualMCPServer> {
  return aiReq<VirtualMCPServer>('/api/ai/mcp/virtual', {
    method: 'POST',
    headers: AI_JSON,
    body: JSON.stringify(def),
  })
}

export function deleteVirtualMCPServer(name: string, tenantId = 0): Promise<void> {
  return aiReq<void>(
    `/api/ai/mcp/virtual/${encodeURIComponent(name)}${tenantId ? `?tenant_id=${tenantId}` : ''}`,
    { method: 'DELETE' },
  )
}

// ── Flow sync (deploy flows + API endpoints directly) ──────────────

export interface SyncStep {
  action: string
  key_identifier?: string
  as?: string
  key?: string
  source?: string
  value?: string
  condition?: string
  then?: string   // for "if" action: name of the sub-flow to run when condition is true
  else?: string   // for "if" action: name of the sub-flow to run when condition is false
  input?: Record<string, string>
  [k: string]: unknown
}

export interface SyncPayload {
  sync_uuid: string
  flows: Array<{ name: string; instructions: SyncStep[]; action: 'upsert' | 'delete'; constants?: Record<string, string> }>
  apis: Array<{
    name: string
    path: string
    flow_name: string
    action: 'upsert' | 'delete'
    rate_limit?: string
    alias_paths?: string[]
    rl_count_by?: string
    rl_slot?: string
    rl_slots?: string
    rl_static_key?: string
    rl_xff_index?: number
    rl_on_empty?: string
    rl_fail_fast?: boolean
    rl_config?: string
    rl_dyn_source?: string
    rl_dyn_key?: string
    upstream_svc?: string
    rate_limit_policies?: import('./types').APIRateLimitEntry[]
    skip_rate_limit?: boolean
    endpoint_configs?: Array<{
      path: string
      method?: string
      flow_name?: string
      rate_limit?: string
      rl_count_by?: string
      rl_slot?: string
      rl_slots?: string
      rl_static_key?: string
      rl_xff_index?: number
      rl_on_empty?: string
      rl_fail_fast?: boolean
      rl_config?: string
      rl_dyn_source?: string
      rl_dyn_key?: string
      upstream_svc?: string
      rate_limit_policies?: import('./types').APIRateLimitEntry[]
      skip_rate_limit?: boolean
    }>
  }>
}

export interface GatewaySnapshot {
  flows: Array<{ name: string; instructions: SyncStep[] }>
  apis: Array<{
    name: string
    path: string
    method?: string
    flow_name: string
    rate_limit?: string
    alias_paths?: string[]
    rl_count_by?: string
    rl_slot?: string
    rl_slots?: string
    rl_static_key?: string
    rl_xff_index?: number
    rl_on_empty?: string
    rl_fail_fast?: boolean
    rl_config?: string
    rl_dyn_source?: string
    rl_dyn_key?: string
    upstream_svc?: string
    endpoint_configs?: Array<{
      path: string
      method?: string
      flow_name?: string
      rate_limit?: string
      rl_count_by?: string
      rl_slot?: string
      rl_slots?: string
      rl_static_key?: string
      rl_xff_index?: number
      rl_on_empty?: string
      rl_fail_fast?: boolean
      rl_config?: string
      rl_dyn_source?: string
      rl_dyn_key?: string
      upstream_svc?: string
    }>
  }>
}

export async function fetchGatewaySnapshot(): Promise<GatewaySnapshot> {
  const res = await fetch('/api/getAllApis', { credentials: 'include' })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

// Fields that the Go backend expects as numbers (int or uint32) but the UI stores as strings
const NUMERIC_STEP_FIELDS = new Set(['status', 'max_retries', 'timeout', 'ttl'])
// Fields that the Go backend expects as booleans but palette defaults store as strings
const BOOL_STEP_FIELDS = new Set(['generate_if_missing', 'trace_capture'])

function normalizeStep(step: Record<string, unknown>): SyncStep {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(step)) {
    if (k === 'then_steps' || k === 'else_steps') continue  // handled by flattenBranches
    if (NUMERIC_STEP_FIELDS.has(k) && typeof v === 'string' && v !== '') {
      const n = Number(v)
      out[k] = Number.isFinite(n) ? n : v
    } else if (BOOL_STEP_FIELDS.has(k) && typeof v === 'string') {
      out[k] = v === 'true'
    } else {
      out[k] = v
    }
  }
  return out as SyncStep
}

// Converts FlowStep[] that may have inline then_steps/else_steps into flat SyncStep[] for the
// main flow plus anonymous sub-flow entries for each branch, mirroring what the Go DSL parser does.
function flattenBranches(
  steps: Record<string, unknown>[],
  counter: { n: number },
): { mainSteps: SyncStep[]; extraFlows: Array<{ name: string; instructions: SyncStep[] }> } {
  const mainSteps: SyncStep[] = []
  const extraFlows: Array<{ name: string; instructions: SyncStep[] }> = []

  for (const step of steps) {
    const thenSteps = step['then_steps'] as Record<string, unknown>[] | undefined
    const elseSteps = step['else_steps'] as Record<string, unknown>[] | undefined

    if (step['action'] === 'if' && (thenSteps?.length || elseSteps?.length)) {
      const flat: Record<string, unknown> = { ...step }
      delete flat['then_steps']
      delete flat['else_steps']

      if (thenSteps?.length) {
        const thenName = `__dsl_then_${counter.n++}`
        flat['then'] = thenName
        const { mainSteps: ts, extraFlows: te } = flattenBranches(thenSteps, counter)
        extraFlows.push(...te, { name: thenName, instructions: ts })
      }
      if (elseSteps?.length) {
        const elseName = `__dsl_else_${counter.n++}`
        flat['else'] = elseName
        const { mainSteps: es, extraFlows: ee } = flattenBranches(elseSteps, counter)
        extraFlows.push(...ee, { name: elseName, instructions: es })
      }

      mainSteps.push(normalizeStep(flat))
    } else {
      mainSteps.push(normalizeStep(step))
    }
  }

  return { mainSteps, extraFlows }
}

export interface SyncResponse {
  status: string
  rate_limit_warnings?: RateLimitWarning[]
}

export async function syncFlows(payload: SyncPayload): Promise<SyncResponse> {
  const expandedFlows: SyncPayload['flows'] = []
  for (const f of payload.flows) {
    if (f.action === 'delete') { expandedFlows.push(f); continue }
    const { mainSteps, extraFlows } = flattenBranches(f.instructions as unknown as Record<string, unknown>[], { n: 0 })
    for (const ef of extraFlows) {
      expandedFlows.push({ name: ef.name, instructions: ef.instructions, action: 'upsert' })
    }
    expandedFlows.push({ ...f, instructions: mainSteps })
  }
  const normalized: SyncPayload = {
    ...payload,
    flows: expandedFlows,
  }
  const res = await fetch('/api/sync', {
    method: 'POST',
    credentials: 'include',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(normalized),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => `HTTP ${res.status}`)
    throw new Error(text || `HTTP ${res.status}`)
  }
  return res.json().catch(() => ({ status: 'success' })) as Promise<SyncResponse>
}

// ── AI Route configs (server-side persistence) ─────────────────────
// Opaque to the gateway — stored/returned as a raw JSON array of SavedRoute objects.

export async function loadRouteConfigs(): Promise<unknown[] | null> {
  try {
    return await aiReq<unknown[]>('/api/ai/routes')
  } catch {
    return null  // gateway not configured / unavailable — fall back to localStorage
  }
}

export async function saveRouteConfigs(routes: unknown[]): Promise<void> {
  try {
    await aiReq<null>('/api/ai/routes', {
      method: 'PUT',
      headers: AI_JSON,
      body: JSON.stringify(routes),
    })
  } catch {
    // Non-fatal — localStorage is the fallback
  }
}

// ── Spend caps / quotas ────────────────────────────────────────────

export interface TenantQuota {
  tenant_id: string
  daily_cost_limit?: number
  monthly_cost_limit?: number
}

export function listQuotas(): Promise<TenantQuota[]> {
  return aiReq<TenantQuota[]>('/api/ai/quotas')
}

export function upsertQuota(q: TenantQuota): Promise<TenantQuota> {
  return aiReq<TenantQuota>('/api/ai/quotas', {
    method: 'POST',
    headers: AI_JSON,
    body: JSON.stringify(q),
  })
}

export function deleteQuota(tenantId: string): Promise<void> {
  return aiReq<void>(`/api/ai/quotas/${encodeURIComponent(tenantId)}`, { method: 'DELETE' })
}

// ── Observability ──────────────────────────────────────────────────

export async function fetchObsMetrics(apiName?: string): Promise<any> {
  const params = apiName ? `?api=${encodeURIComponent(apiName)}` : ''
  const r = await fetch(`/api/observability/metrics${params}`, { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function fetchObsAccessLog(params?: { api?: string; app?: string; tenant?: string; status?: number; limit?: number }): Promise<any> {
  const q = new URLSearchParams()
  if (params?.api) q.set('api', params.api)
  if (params?.app) q.set('app', params.app)
  if (params?.tenant) q.set('tenant', params.tenant)
  if (params?.status) q.set('status', String(params.status))
  if (params?.limit) q.set('limit', String(params.limit))
  const r = await fetch(`/api/observability/access-log?${q}`, { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function fetchObsApis(): Promise<any> {
  const r = await fetch('/api/observability/apis', { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function fetchObsTraces(params?: { api?: string; limit?: number }): Promise<any> {
  const q = new URLSearchParams()
  if (params?.api) q.set('api', params.api)
  if (params?.limit) q.set('limit', String(params.limit))
  const r = await fetch(`/api/observability/traces?${q}`, { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export interface InstrSchemaRow {
  api_name: string
  endpoint_id: number
  pc: number
  step_type: string
  step_name: string
}

export async function fetchInstrSchema(apiName: string): Promise<InstrSchemaRow[]> {
  const r = await fetch(`/api/observability/instr-schema?api=${encodeURIComponent(apiName)}`, { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  const body = await r.json()
  return Array.isArray(body) ? body : (body.data ?? [])
}

export interface ObsDetailLogConfig {
  enabled: boolean
  path: string
}

export async function fetchObsDetailLogConfig(): Promise<ObsDetailLogConfig> {
  const r = await fetch('/api/observability/detail-log', { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function updateObsDetailLogConfig(cfg: ObsDetailLogConfig): Promise<ObsDetailLogConfig> {
  const r = await fetch('/api/observability/detail-log', {
    method: 'PUT',
    credentials: 'include',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(cfg),
  })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

// ── Observability runtime config (trace mode, sampling, log fields) ──

export interface ObsRuntimeConfig {
  trace_mode: boolean
  trace_sample_rate: number          // 0.0–1.0
  instruction_timing_enabled: boolean
  info_log_enabled: boolean
  info_log_fields: string[]          // field names included in access log
}

export async function fetchObsConfig(): Promise<ObsRuntimeConfig> {
  const r = await fetch('/api/observability/config', { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  const body = await r.json()
  // The config snapshot is nested under body.config
  const c = body?.config ?? {}
  return {
    trace_mode:                  !!c.trace_mode,
    trace_sample_rate:           c.trace_sample_rate ?? 0,
    instruction_timing_enabled:  c.instruction_timing_enabled !== false,
    info_log_enabled:            c.info_log_enabled !== false,
    info_log_fields:             Array.isArray(c.info_log_fields) ? c.info_log_fields : [],
  }
}

export async function updateObsConfig(patch: Partial<ObsRuntimeConfig>): Promise<void> {
  const body: Record<string, unknown> = {}
  if (patch.trace_mode                !== undefined) body.trace_mode                 = patch.trace_mode
  if (patch.trace_sample_rate         !== undefined) body.trace_sample_rate          = patch.trace_sample_rate
  if (patch.instruction_timing_enabled !== undefined) body.instruction_timing_enabled = patch.instruction_timing_enabled
  if (patch.info_log_enabled          !== undefined) body.info_log_enabled           = patch.info_log_enabled
  if (patch.info_log_fields           !== undefined) body.info_log_fields            = patch.info_log_fields
  const r = await fetch('/api/observability/config', {
    method: 'POST',
    credentials: 'include',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!r.ok) throw new Error(await r.text())
}

// ── Cache Management ────────────────────────────────────────────────

export function getCacheEntry(tenant: string, key: string): Promise<{ found: boolean; value: string }> {
  return request<{ found: boolean; value: string }>(
    `/api/cache/${encodeURIComponent(tenant)}/${encodeURIComponent(key)}`
  )
}

export function deleteCacheEntry(tenant: string, key: string): Promise<void> {
  return request<void>(
    `/api/cache/${encodeURIComponent(tenant)}/${encodeURIComponent(key)}`,
    { method: 'DELETE' }
  )
}

// ─── Apps ────────────────────────────────────────────────────────────────────

export function listApps(): Promise<App[]> {
  return request<{ items: App[]; count: number }>('/api/apps').then(r => r.items ?? [])
}

export function createApp(body: { name: string; description: string; labels?: Record<string, string> }): Promise<App> {
  return request<App>('/api/apps', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function updateApp(id: number, body: Partial<{ name: string; description: string; labels?: Record<string, string> }>): Promise<App> {
  return request<App>(`/api/apps/${id}`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function deleteApp(id: number): Promise<void> {
  return request<void>(`/api/apps/${id}`, { method: 'DELETE' })
}

export interface AppBlueprintRequest {
  type: 'web' | 'api-service' | 'event-processor' | 'webhook'
  tenant_mode: 'tenant_aware' | 'tenant_agnostic'
  oauth_provider?: string
  callback_path?: string
  login_path?: string
  logout_path?: string
}

export interface FlowBlueprint {
  name: string
  yaml: string
}

export interface AppBlueprintResponse {
  app_name: string
  flows: FlowBlueprint[]
}

export async function generateAppBlueprint(appName: string, req: AppBlueprintRequest): Promise<AppBlueprintResponse> {
  return request<AppBlueprintResponse>('/api/apps/' + appName + '/blueprint', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(req) })
}

// ─── API Keys ────────────────────────────────────────────────────────────────

export function listKeys(appId: number): Promise<APIKeyView[]> {
  return request<{ items: APIKeyView[]; count: number }>(`/api/apps/${appId}/keys`).then(r => r.items ?? [])
}

export function generateKey(appId: number, body: { alias: string; allowed_tenants?: number[] }): Promise<APIKeyCreateResponse> {
  return request<APIKeyCreateResponse>(`/api/apps/${appId}/keys`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function updateKey(appId: number, keyId: number, body: Partial<{ alias: string; enabled: boolean; allowed_tenants?: number[] }>): Promise<APIKeyView> {
  return request<APIKeyView>(`/api/apps/${appId}/keys/${keyId}`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export function revokeKey(appId: number, keyId: number): Promise<void> {
  return request<void>(`/api/apps/${appId}/keys/${keyId}`, { method: 'DELETE' })
}

export function rotateKey(appId: number, keyId: number): Promise<APIKeyCreateResponse> {
  return request<APIKeyCreateResponse>(`/api/apps/${appId}/keys/${keyId}/rotate`, { method: 'POST' })
}

// ── Egress ───────────────────────────────────────────────────────────────

export function listEgressProfiles(): Promise<EgressProfileConfig[]> {
  return request<EgressProfileConfig[]>('/api/egress/profiles')
}
export function upsertEgressProfile(p: EgressProfileConfig): Promise<void> {
  return request<void>('/api/egress/profiles', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(p) })
}
export function deleteEgressProfile(name: string): Promise<void> {
  return request<void>(`/api/egress/profiles/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

export function listCodeRules(): Promise<EgressCodeRuleConfig[]> {
  return request<EgressCodeRuleConfig[]>('/api/egress/rules/codes')
}
export function upsertCodeRule(r: EgressCodeRuleConfig): Promise<void> {
  return request<void>('/api/egress/rules/codes', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(r) })
}
export function deleteCodeRule(code: string): Promise<void> {
  return request<void>(`/api/egress/rules/codes/${encodeURIComponent(code)}`, { method: 'DELETE' })
}

export function listPatternRules(): Promise<EgressPatternRuleConfig[]> {
  return request<EgressPatternRuleConfig[]>('/api/egress/rules/patterns')
}
export function upsertPatternRule(r: EgressPatternRuleConfig): Promise<void> {
  return request<void>('/api/egress/rules/patterns', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(r) })
}
export function deletePatternRule(idx: string): Promise<void> {
  return request<void>(`/api/egress/rules/patterns/${encodeURIComponent(idx)}`, { method: 'DELETE' })
}

// ── Schema Library ────────────────────────────────────────────────────────────

export function listSchemaSets(): Promise<string[]> {
  return request<string[]>('/api/schemas')
}

export function listSchemaFields(setName: string): Promise<FieldSchema[]> {
  return request<FieldSchema[]>(`/api/schemas/${encodeURIComponent(setName)}`)
}

export function upsertSchemaField(setName: string, field: FieldSchema): Promise<void> {
  return request<void>(`/api/schemas/${encodeURIComponent(setName)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(field),
  })
}

export function deleteSchemaField(setName: string, fieldName: string): Promise<void> {
  return request<void>(`/api/schemas/${encodeURIComponent(setName)}/${encodeURIComponent(fieldName)}`, { method: 'DELETE' })
}

export function deleteSchemaSet(setName: string): Promise<void> {
  return request<void>(`/api/schemas/${encodeURIComponent(setName)}`, { method: 'DELETE' })
}

// ── gRPC Descriptors ─────────────────────────────────────────────────────
export async function listGrpcDescriptors(): Promise<import('./types').GrpcDescriptorSummary[]> {
  const r = await fetch('/api/grpc/descriptors', { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function getGrpcDescriptor(name: string): Promise<import('./types').GrpcDescriptorSummary> {
  const r = await fetch(`/api/grpc/descriptors/${encodeURIComponent(name)}`, { credentials: 'include' })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function uploadGrpcDescriptor(name: string, file: File): Promise<import('./types').GrpcDescriptorSummary> {
  const data = await file.arrayBuffer()
  const r = await fetch('/api/grpc/descriptors', {
    method: 'POST',
    credentials: 'include',
    headers: { 'X-Descriptor-Name': name, 'Content-Type': 'application/octet-stream' },
    body: data,
  })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function deleteGrpcDescriptor(name: string): Promise<void> {
  const r = await fetch(`/api/grpc/descriptors/${encodeURIComponent(name)}`, { method: 'DELETE', credentials: 'include' })
  if (!r.ok && r.status !== 204) throw new Error(await r.text())
}

// ── Release Management ────────────────────────────────────────────────────────

export function listReleases(cursor?: string): Promise<ReleaseListResponse> {
  const q = cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''
  return request<ReleaseListResponse>(`/api/releases${q}`)
}

export function getReleaseById(id: string): Promise<ReleaseRecord> {
  return request<ReleaseRecord>(`/api/releases/${encodeURIComponent(id)}`)
}

/** Post a bundle to /api/releases. Uses multipart/form-data so raw YAML/JSON can
 *  be sent without parsing in the browser. Set dryRun=true for validation only. */
export async function createRelease(
  content: string,
  meta?: { tag?: string; author?: string; git_commit?: string; git_branch?: string },
  dryRun = false,
): Promise<CreateReleaseResponse> {
  const form = new FormData()
  form.append('bundle', new Blob([content], { type: 'text/plain' }), 'bundle.yaml')
  if (meta?.tag)        form.append('tag', meta.tag)
  if (meta?.author)     form.append('author', meta.author)
  if (meta?.git_commit) form.append('git_commit', meta.git_commit)
  if (meta?.git_branch) form.append('git_branch', meta.git_branch)
  const url = dryRun ? '/api/releases?dry_run=true' : '/api/releases'
  const res = await fetch(url, { method: 'POST', credentials: 'include', body: form })
  if (!res.ok) {
    const text = await res.text().catch(() => `HTTP ${res.status}`)
    throw new Error(text || `HTTP ${res.status}`)
  }
  return res.json() as Promise<CreateReleaseResponse>
}

export async function promoteRelease(
  id: string,
  env: string,
  byUser?: string,
): Promise<{ release_id: string; env: string; results: import('./types').ReleaseDeployResult[] }> {
  return request(`/api/releases/${encodeURIComponent(id)}/deploy`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ env, by_user: byUser ?? '' }),
  })
}

export function diffReleases(id: string, otherId: string): Promise<ReleaseDiff> {
  return request<ReleaseDiff>(`/api/releases/${encodeURIComponent(id)}/diff/${encodeURIComponent(otherId)}`)
}

export function getConcurrencyStatus(): Promise<ConcurrencyStatus> {
  return request<ConcurrencyStatus>('/api/concurrency')
}

export function patchConcurrencyConfig(body: ConcurrencyPatch): Promise<ConcurrencyStatus> {
  return request<ConcurrencyStatus>('/api/concurrency', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

// ── Audit Log ─────────────────────────────────────────────────────

export interface AuditRecord {
  id: string
  timestamp: string
  actor: string
  action: string
  resource_type: string
  resource_id: string
  status: string
  summary: string
  metadata?: Record<string, string>
}

export async function fetchAuditLog(): Promise<AuditRecord[]> {
  return request<AuditRecord[]>('/api/audit')
}

// ── Rate Limit V2 Overrides ────────────────────────────────────────

export async function upsertV2Override(alias: string, body: {
  rate_limit_v2: string;
  blocked?: boolean;
  rl_disabled?: boolean;
  scale_override_pct?: number;
  window_limits?: number[];
}): Promise<void> {
  const res = await fetch(`/api/management/tenants/${encodeURIComponent(alias)}/rate-limit-v2-overrides`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`upsertV2Override failed: ${res.status}`);
}

export async function deleteV2Override(alias: string, configName: string): Promise<void> {
  const res = await fetch(`/api/management/tenants/${encodeURIComponent(alias)}/rate-limit-v2-overrides/${encodeURIComponent(configName)}`, {
    method: 'DELETE',
  });
  if (!res.ok) throw new Error(`deleteV2Override failed: ${res.status}`);
}

export interface TenantOverrideRow {
  tenant_id: number
  alias: string
  global_blocked: boolean
  global_rl_disabled: boolean
  global_scale_pct: number
  overrides: {
    config_name: string
    blocked: boolean
    rl_disabled: boolean
    scale_override_pct: number
    window_limits?: number[]
  }[]
}

export async function listAllV2Overrides(): Promise<TenantOverrideRow[]> {
  return request<TenantOverrideRow[]>('/api/tenants/rate-limit-v2-overrides')
}

// ── Schedules ──────────────────────────────────────────────────────────

export interface ScheduleOnFailure {
  retry_count?: number
  retry_interval_sec?: number
  dead_letter_flow?: string
}

export interface Schedule {
  name: string
  cron: string
  flow_name: string
  tenant_alias: string
  enabled: boolean
  timeout_sec: number
  constants?: Record<string, string>
  next_run_at?: string
  last_run_at?: string
  last_status?: string
  on_failure?: ScheduleOnFailure
  max_concurrent?: number
  runtime_only?: boolean
}

export interface ScheduleHistory {
  execution_id: string
  scheduled_at: string
  started_at?: string
  completed_at?: string
  status: string
  error?: string
  duration_ms?: number
}

export async function listSchedules(): Promise<Schedule[]> {
  const res = await request<Schedule[] | null>('/api/schedules')
  return res ?? []
}

export async function upsertSchedule(s: Schedule): Promise<void> {
  return request<void>('/api/schedules', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(s),
  })
}

export async function deleteSchedule(name: string): Promise<void> {
  return request<void>(`/api/schedules/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

export async function getScheduleHistory(name: string): Promise<ScheduleHistory[]> {
  return request<ScheduleHistory[]>(`/api/schedules/${encodeURIComponent(name)}/history`)
}

export async function runFlow(name: string, tenantAlias: string, constants?: Record<string, string>): Promise<void> {
  return request<void>(`/api/flows/${encodeURIComponent(name)}/run`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ tenant_alias: tenantAlias, constants }),
  })
}

// ── WebSocket observability ────────────────────────────────────────────

export interface WSSession {
  id: string
  tenant_id: number
  api_id: number
  connected_at: string
}

export interface WSUpstream {
  name: string
  url: string
  status: string
  last_ping_rtt_ms?: number
}

export async function listWSSessions(): Promise<WSSession[]> {
  return request<WSSession[]>('/api/ws/sessions')
}

export async function listWSUpstreams(): Promise<WSUpstream[]> {
  return request<WSUpstream[]>('/api/ws/upstreams')
}

// ── Redis Sources ──────────────────────────────────────────────────────────────

export interface RedisSourceDef {
  name: string
  addr?: string
  addrs?: string[]
  db?: number
  tls?: boolean
  tenant_prefix?: string
  key_separator?: string
}

export function listRedisSources(): Promise<{ sources: string[] }> {
  return request<{ sources: string[] }>('/api/redis-sources')
}

// ── Document Connectors ────────────────────────────────────────────────────────

export interface DocumentConnectorDef {
  name: string
  kind: string
}

export function listDocumentConnectors(): Promise<{ connectors: DocumentConnectorDef[] }> {
  return request<{ connectors: DocumentConnectorDef[] }>('/api/document-connectors')
}

// ── Data Stores ────────────────────────────────────────────────────────────

export interface DataStoreDef {
  name: string
  type: string  // "postgres" | "redis" | "disk"
  host?: string
  database?: string
  path?: string
}

export interface DataStoreTestResult {
  ok: boolean
  latency_ms?: number
  error?: string
}

export async function listDataStores(): Promise<{ datastores: DataStoreDef[] }> {
  return request('/api/config/datastores')
}

export async function testDataStore(name: string): Promise<DataStoreTestResult> {
  return request('/api/config/datastores/' + name + '/test', { method: 'POST' })
}

export async function deleteDataStore(name: string): Promise<void> {
  await request('/api/config/datastores/' + name, { method: 'DELETE' })
}

// ── Storage Connectors ──────────────────────────────────────────────────────────

export interface StorageConnectorDef {
  name: string
  type: string
}

export function listStorageConnectors(): Promise<{ connectors: StorageConnectorDef[] }> {
  return request<{ connectors: StorageConnectorDef[] }>('/api/storage-connectors')
}

// ── Messaging Publishers ───────────────────────────────────────────────────────

export interface MessagingPublisherDef {
  name: string
  kind: string
}

export function listMessagingPublishers(): Promise<{ publishers: MessagingPublisherDef[] }> {
  return request<{ publishers: MessagingPublisherDef[] }>('/api/messaging-publishers')
}

// ── Event Listeners ────────────────────────────────────────────────────────────

export interface EventListenerDef {
  name: string
  publisher: string
  topic: string
  flow_name: string
  workers: number
}

export function listEventListeners(): Promise<{ listeners: EventListenerDef[] }> {
  return request<{ listeners: EventListenerDef[] }>('/api/event-listeners')
}

// ── Apps (flow grouping) ───────────────────────────────────────────────────────

export interface AppDef {
  name: string
  flows: string[]
}

export function listAppFlows(): Promise<{ apps: AppDef[] }> {
  return request<{ apps: AppDef[] }>('/api/apps')
}

export function listObservabilityApps(): Promise<{ apps: string[] }> {
  return request<{ apps: string[] }>('/api/observability/apps')
}

// ── App Releases ────────────────────────────────────────────────────────────

export interface AppRelease {
  app_name: string
  version: string
  channel: string
  flow_names: string[]
  notes?: string
  active: boolean
  created_at: string
  created_by?: string
}

export async function listAppReleases(appName: string): Promise<{ app_name: string; releases: AppRelease[] }> {
  return request('/api/apps/' + appName + '/releases')
}

export async function createAppRelease(appName: string, rel: Omit<AppRelease, 'app_name' | 'active' | 'created_at'>): Promise<AppRelease> {
  return request('/api/apps/' + appName + '/releases', { method: 'POST', body: JSON.stringify(rel), headers: { 'content-type': 'application/json' } })
}

export async function promoteAppRelease(appName: string, version: string, channel: string): Promise<AppRelease> {
  return request('/api/apps/' + appName + '/releases/' + version + '/promote', { method: 'POST', body: JSON.stringify({ channel }), headers: { 'content-type': 'application/json' } })
}

export async function rollbackAppRelease(appName: string, version: string, channel: string): Promise<AppRelease> {
  return request('/api/apps/' + appName + '/releases/' + version + '/rollback', { method: 'POST', body: JSON.stringify({ channel }), headers: { 'content-type': 'application/json' } })
}

// ── Named Queries ──────────────────────────────────────────────────────────────

export interface NamedQueryDef {
  sql: string
  batch_by?: string
  batch_window?: string
  batch_max?: number
}

export function listNamedQueries(): Promise<{ queries: Record<string, NamedQueryDef> }> {
  return request<{ queries: Record<string, NamedQueryDef> }>('/api/named-queries')
}

export function upsertNamedQuery(name: string, q: NamedQueryDef): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>('/api/named-queries', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ name, ...q }),
  })
}

export function deleteNamedQuery(name: string): Promise<void> {
  return request<void>(`/api/named-queries/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

// ── Migrations ─────────────────────────────────────────────────────────────

export interface MigrationStatus {
  version: number
  name: string
  applied_at: string
}

export function listMigrations(): Promise<{ migrations: MigrationStatus[] }> {
  return request<{ migrations: MigrationStatus[] }>('/api/migrations')
}

// ── Workflows ──────────────────────────────────────────────────────────────

export interface WorkflowNode {
  id: string
  flowName: string
  label: string
  x: number
  y: number
}

export interface WorkflowEdge {
  id: string
  sourceNodeId: string
  targetNodeId: string
  listenerName: string
  label: string
}

export interface WorkflowDef {
  name: string
  description: string
  appName: string
  nodes: WorkflowNode[]
  edges: WorkflowEdge[]
  createdAt?: number
  updatedAt?: number
}

export function listWorkflows(): Promise<{ workflows: string[] }> {
  return request<{ workflows: string[] }>('/api/workflows')
}

export function getWorkflow(name: string): Promise<WorkflowDef> {
  return request<WorkflowDef>(`/api/workflows/${encodeURIComponent(name)}`)
}

export function upsertWorkflow(wf: WorkflowDef): Promise<WorkflowDef> {
  return request<WorkflowDef>('/api/workflows', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(wf),
  })
}

export function deleteWorkflow(name: string): Promise<void> {
  return request<void>(`/api/workflows/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

export async function listFlowNames(): Promise<string[]> {
  const state = await fetchGatewayApis()
  const names: string[] = []
  if (state && state.flows) {
    for (const flow of state.flows) {
      if (flow.name && !names.includes(flow.name)) names.push(flow.name)
    }
  }
  return names.sort()
}

// ── Tests ─────────────────────────────────────────────────────────────────

export interface TestInput {
  method: string
  path: string
  headers?: Record<string, string>
  body?: string
  tenant_key?: string
}

export interface TestAssertion {
  type: 'status' | 'body_contains' | 'latency_ms' | 'step_executed' | 'side_effect'
  expected?: any
  path?: string
  max?: number
  step?: string
  key?: string
}

export interface StepMock {
  step_name: string
  status?: number
  body?: string
}

export interface TestCase {
  id: string
  api_name: string
  name: string
  flow_name: string
  call_mode: 'mock' | 'real' | 'schema_only'
  input: TestInput
  mocks?: StepMock[]
  assertions: TestAssertion[]
  created_at: number
}

export interface AssertionResult {
  type: string
  passed: boolean
  expected?: any
  actual?: any
  message?: string
}

export interface TestExecuteResponse {
  passed: boolean
  duration_ms: number
  response: { status: number; body: string }
  assertions: AssertionResult[]
}

export interface TestSuite {
  id: string
  name: string
  case_ids: string[]
  mode: string
  created_at: number
}

export interface SuiteCaseResult {
  case_id: string
  case_name: string
  passed: boolean
  duration_ms: number
  response: { status: number; body: string }
  assertions: AssertionResult[]
  error?: string
}

export interface SuiteRunResult {
  suite_id: string
  run_id: string
  passed: boolean
  total_cases: number
  passed_cases: number
  results: SuiteCaseResult[]
  test_tenant_alias?: string
}

export async function listTestCases(): Promise<TestCase[]> {
  const res = await request<{ cases: TestCase[] } | TestCase[]>('/api/test/cases')
  if (Array.isArray(res)) return res
  return (res as any).cases ?? []
}

export async function createTestCase(tc: Omit<TestCase, 'id' | 'created_at'>): Promise<TestCase> {
  return request('/api/test/cases', { method: 'POST', body: JSON.stringify(tc) })
}

export async function deleteTestCase(id: string): Promise<void> {
  await request(`/api/test/cases/${id}`, { method: 'DELETE' })
}

export async function executeTest(req: {
  flow_name: string
  mode?: string
  call_mode?: string
  input: TestInput
  mocks?: StepMock[]
  assertions?: TestAssertion[]
}): Promise<TestExecuteResponse> {
  return request('/api/test/execute', { method: 'POST', body: JSON.stringify(req) })
}

export async function listTestSuites(): Promise<TestSuite[]> {
  const res = await request<{ suites: TestSuite[] } | TestSuite[]>('/api/test/suites')
  if (Array.isArray(res)) return res
  return (res as any).suites ?? []
}

export async function createTestSuite(suite: { name: string; case_ids: string[] }): Promise<TestSuite> {
  return request('/api/test/suites', { method: 'POST', body: JSON.stringify(suite) })
}

export async function runTestSuite(id: string): Promise<SuiteRunResult> {
  return request(`/api/test/suites/${id}/run`, { method: 'POST' })
}

export async function deleteTestSuite(id: string): Promise<void> {
  await request(`/api/test/suites/${id}`, { method: 'DELETE' })
}
