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
  CredentialListResponse,
  AIEnvelope,
  LLMModel,
  LLMTestResult,
  LLMTestDebug,
  MCPServer,
  MCPPingResult,
  APIToolDef,
  VirtualMCPServer,
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
  return request<DeployResponse>('/api/deploy', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
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
  apis: Array<{ name: string; path: string; flow_name: string; action: 'upsert' }>
}

export interface GatewaySnapshot {
  flows: Array<{ name: string; instructions: SyncStep[] }>
  apis:  Array<{ name: string; path: string; flow_name: string }>
}

export async function fetchGatewaySnapshot(): Promise<GatewaySnapshot> {
  const res = await fetch('/api/getAllApis')
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function syncFlows(payload: SyncPayload): Promise<void> {
  const res = await fetch('/api/sync', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => `HTTP ${res.status}`)
    throw new Error(text || `HTTP ${res.status}`)
  }
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
