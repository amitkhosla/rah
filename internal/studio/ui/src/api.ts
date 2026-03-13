import type {
  SchemaResponse,
  TargetsResponse,
  DeployRequest,
  DeployResponse,
  OpenAPIImportResponse,
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
