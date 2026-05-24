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
} from './types'

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, options)
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
  flows: Array<{ name: string; instructions: SyncStep[]; action: 'upsert' }>
  apis: Array<{
    name: string
    path: string
    flow_name: string
    action: 'upsert'
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
  const res = await fetch('/api/getAllApis')
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

export interface SyncResponse {
  status: string
  rate_limit_warnings?: RateLimitWarning[]
}

export async function syncFlows(payload: SyncPayload): Promise<SyncResponse> {
  const normalized: SyncPayload = {
    ...payload,
    flows: payload.flows.map(f => ({ ...f, instructions: f.instructions.map(normalizeStep) })),
  }
  const res = await fetch('/api/sync', {
    method: 'POST',
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
  const r = await fetch(`/api/observability/metrics${params}`)
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function fetchObsAccessLog(params?: { api?: string; tenant?: string; status?: number; limit?: number }): Promise<any> {
  const q = new URLSearchParams()
  if (params?.api) q.set('api', params.api)
  if (params?.tenant) q.set('tenant', params.tenant)
  if (params?.status) q.set('status', String(params.status))
  if (params?.limit) q.set('limit', String(params.limit))
  const r = await fetch(`/api/observability/access-log?${q}`)
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function fetchObsApis(): Promise<any> {
  const r = await fetch('/api/observability/apis')
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function fetchObsTraces(params?: { api?: string; limit?: number }): Promise<any> {
  const q = new URLSearchParams()
  if (params?.api) q.set('api', params.api)
  if (params?.limit) q.set('limit', String(params.limit))
  const r = await fetch(`/api/observability/traces?${q}`)
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export interface ObsDetailLogConfig {
  enabled: boolean
  path: string
}

export async function fetchObsDetailLogConfig(): Promise<ObsDetailLogConfig> {
  const r = await fetch('/api/observability/detail-log')
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

export async function updateObsDetailLogConfig(cfg: ObsDetailLogConfig): Promise<ObsDetailLogConfig> {
  const r = await fetch('/api/observability/detail-log', {
    method: 'PUT',
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
  const r = await fetch('/api/observability/config')
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
  return request<App[]>('/api/apps')
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

// ─── API Keys ────────────────────────────────────────────────────────────────

export function listKeys(appId: number): Promise<APIKeyView[]> {
  return request<APIKeyView[]>(`/api/apps/${appId}/keys`)
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
