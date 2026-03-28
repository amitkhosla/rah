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
