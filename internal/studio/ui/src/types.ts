export type TabId = 'flows' | 'apis' | 'deploy' | 'gateway' | 'tenants' | 'settings'

export type ConnStatus = 'connecting' | 'ok' | 'error'

// ── Palette / Schema ──────────────────────────────────────────────

export interface FieldDef {
  key: string
  label: string
  description: string
  placeholder: string
}

export interface PaletteBlock {
  type: string
  title: string
  description: string
  category: string
  capability: string
  supports_nested: boolean
  next_hints?: string[]
  defaults: Record<string, string>
  fields?: FieldDef[]
}

export interface SchemaResponse {
  version: string
  categories: string[]
  blocks: PaletteBlock[]
}

// ── Targets / Deploy ──────────────────────────────────────────────

export interface Target {
  name: string
  level: string
  urls: string[]
}

export interface DeployResult {
  target: string
  url: string
  status: number
  release_id: string
  error?: string
}

// Go marshals timeJSON as {"T":"2006-01-02T15:04:05Z"}
export interface TimeJSON {
  T: string
}

export interface DeployRecord {
  at: TimeJSON
  release_id: string
  levels: string[]
  targets: string[]
  results: DeployResult[]
}

export interface ReleaseRecord {
  release_id: string
  created_at: TimeJSON
  instruction_set_version?: string
  api_versions?: Record<string, string>
}

export interface TargetsResponse {
  targets: Target[]
  history: DeployRecord[]
  releases: ReleaseRecord[]
  stores_supported: string[]
}

// ── Flow / APIs ───────────────────────────────────────────────────

// A step in a flow. `action` is always set; remaining keys are step parameters.
export type FlowStep = { action: string } & Record<string, string>

export interface SavedFlow {
  name: string
  steps: FlowStep[]
}

export interface ApiDef {
  name: string
  path: string
  method: string
}

// ── Request bodies ────────────────────────────────────────────────

export interface DeployPayload {
  sync_uuid: string
  flows: Array<{ name: string; instructions: FlowStep[]; action: 'upsert' }>
  apis: Array<{ name: string; path: string; flow_name: string; action: 'upsert' }>
}

export interface DeployRequest {
  release_id?: string
  instruction_set_version?: string
  api_versions?: Record<string, string>
  levels: string[]
  target_names: string[]
  payload?: DeployPayload
}

export interface DeployResponse {
  release_id: string
  results: DeployResult[]
}

export interface ImportedAPI {
  name: string
  path: string
  method: string
}

export interface OpenAPIImportResponse {
  source: string
  apis: ImportedAPI[]
}

// ── Tenant Management ─────────────────────────────────────────────

export interface TenantSummary {
  tenant_id: number
  aliases: string[]
  service_url_count: number
  identifier_count: number
  metadata_count: number
}

export interface TenantListResponse {
  items: TenantSummary[]
  next_cursor: number
  count: number
}

export interface TenantDetail {
  tenant_id: number
  aliases: string[]
  properties: Record<string, string>  // prefixed: "url:primary", "id:api_key", "meta:tier"
}

export interface RateLimitConfig {
  per_sec: number
  per_min: number
  burst_factor: number
}

export interface RateLimitRecord {
  name: string
  config: RateLimitConfig
}

export interface RateLimitListResponse {
  items: RateLimitRecord[]
  count: number
}

export interface UpsertTenantRequest {
  aliases: string[]
  service_urls?: Record<string, string>
  identifiers?: Record<string, string>
  metadata?: Record<string, string>
}

export interface UpsertRateLimitRequest {
  name: string
  per_sec: number
  per_min: number
  burst_factor: number
}

// ── Credentials ───────────────────────────────────────────────────

export interface CredentialListResponse {
  credentials: string[]  // names only, e.g. ["llm:openai", "mcp:brave-search"]
}

// ── Gateway live state ────────────────────────────────────────────

export interface GatewayFlow {
  name: string
  instructions: FlowStep[]
  action: string
}

export interface GatewayApi {
  name: string
  path: string
  flow_name: string
  action: string
}

export interface GatewayState {
  sync_uuid: string
  flows: GatewayFlow[]
  apis: GatewayApi[]
}
