export type TabId = 'dashboard' | 'flows' | 'apis' | 'flowmap' | 'ai' | 'deploy' | 'gateway' | 'observability' | 'tenants' | 'settings'

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
// then_steps/else_steps are Studio-only inline branch steps (not sent to gateway).
export interface FlowStep {
  action: string
  then_steps?: FlowStep[]   // Studio-only: inline then branch steps
  else_steps?: FlowStep[]   // Studio-only: inline else branch steps
  [key: string]: unknown    // all other step params (strings, numbers, etc.)
}

// ── Step Groups (Studio UI only — not compiled to gateway) ───────────

export interface StepGroup {
  id: string         // uuid or timestamp-based
  label: string      // user-defined name, e.g. "Authentication"
  startIndex: number // first step index (inclusive)
  endIndex: number   // last step index (inclusive)
  collapsed: boolean // default: false
}

// Per-step user labels: key = step index (as string), value = label text
// Stored alongside the flow in Studio but NOT compiled into gateway instructions.
export type StepLabels = Record<string, string>

export interface SavedFlow {
  name: string
  steps: FlowStep[]
  groups?: StepGroup[]
  stepLabels?: Record<string, string>
}

// Sub-path + method entry under an API
export interface EndpointDef {
  id: string          // uuid (client-generated, use crypto.randomUUID())
  subPath: string     // e.g. "/" or "/{id}" or "/search"
  method: string      // GET | POST | PUT | PATCH | DELETE
  flowName?: string   // if set, overrides the API's defaultFlow for this endpoint
  rateLimitName?: string   // optional rate limit config name for this endpoint
  constants?: Record<string, string>   // NEW: pre-loaded named slots for this endpoint
}

// Basepath-level API with multiple endpoints
export interface ApiDef {
  id: string              // uuid (client-generated)
  name: string            // display name, e.g. "Users API"
  basePath: string        // primary basepath, e.g. "/api/v1/users"  (must start with /)
  aliasPaths?: string[]   // additional basepaths → same ApiID in gateway router
  defaultFlow: string     // flow inherited by all endpoints that don't override
  endpoints: EndpointDef[]
  rateLimitName?: string   // optional rate limit config name for this API
  constants?: Record<string, string>   // NEW: pre-loaded named slots (endpoint overrides api)
}

// ── Request bodies ────────────────────────────────────────────────

export interface DeployPayload {
  sync_uuid: string
  flows: Array<{ name: string; instructions: Array<Record<string, unknown>>; action: 'upsert' }>
  apis: Array<{
    name: string
    path: string
    flow_name: string
    method?: string
    endpoint_configs?: Array<{ path: string; method: string; flow_name?: string }>
    action: 'upsert'
  }>
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

// ── AI / LLM ──────────────────────────────────────────────────────

export interface AIEnvelope<T> {
  ok: boolean
  data?: T
  error?: string
}

export interface ModelCapabilities {
  max_context_tokens?: number
  max_system_prompt_tokens?: number
  max_history_turns?: number
  supported_features?: string[]
  supported_tool_formats?: string[]
}

export type LLMAdapter = 'anthropic' | 'openai' | 'gemini' | 'ollama' | 'deepseek' | 'custom'

export interface LLMModel {
  alias: string
  // model_id is the actual identifier sent to the provider API (e.g. "gpt-4o-2024-08-06").
  // Defaults to alias when empty, allowing friendly aliases while pinning to exact model versions.
  model_id?: string
  provider: string
  adapter: LLMAdapter
  base_url?: string
  api_key_ref?: string
  max_tokens?: number
  capabilities: ModelCapabilities
  // Only used when adapter === 'custom'. Defaults: name="Authorization", prefix="Bearer "
  auth_header_name?: string
  auth_header_prefix?: string
  // Provider-specific fields merged into the wire-format request body at call time.
  // Examples: {"service_tier":"flex"} for OpenAI, {"thinking":{"type":"enabled","budget_tokens":5000}} for Anthropic
  provider_params?: Record<string, any>
  // When true, the OpenAI adapter sends max_completion_tokens instead of max_tokens.
  // Required for o-series and newer GPT-5+ models.
  use_completion_tokens?: boolean
  cost_per_input_token?: number   // USD per 1M input tokens
  cost_per_output_token?: number  // USD per 1M output tokens
  // Bypasses the adapter's URL construction entirely — the request is POSTed to this exact URL.
  // Use for models like Gemma where the model ID is embedded in the path.
  endpoint_override?: string
  // Additional HTTP headers sent with every request to this model (merged after auth header).
  extra_headers?: Record<string, string>
  // Proactive provider-facing rate limits. Windows: "second" | "minute" | "hour" | "day".
  // The gateway checks local counters before calling the provider; on exhaustion it
  // falls through to the fallback chain instead of burning a real API call.
  rate_limits?: Array<{ window: string; limit: number }>
  // Gemini only: Google Generative Language API version.
  // "v1beta" (default) — supports system_instruction, tools, and thinking (Gemini 1.5+, 2.0+, Gemma).
  // "v1"               — stable but limited; no system_instruction or function calling.
  // Ignored when endpoint_override or a versioned base_url is set.
  api_version?: 'v1' | 'v1beta'
}

export interface LLMTestResult {
  response: string
  latency_ms: number
  input_tokens: number
  output_tokens: number
}

export interface LLMTestDebug {
  endpoint: string
  model_id_sent: string
  http_status: number
  response_body: string
  latency_ms?: number
}

// ── MCP ───────────────────────────────────────────────────────────

export type MCPTransport = 'http' | 'sse' | 'stdio'

export interface MCPServer {
  alias: string
  transport: MCPTransport
  url?: string
  command?: string[]
  api_key_ref?: string
  timeout_ms?: number
}

export interface MCPPingResult {
  alias: string
  reachable: boolean
  status_code?: number
  latency_ms: number
  error?: string
}

export type ToolSourceKind = 'api_tool' | 'mcp_tool' | 'mcp_all'

export interface APIToolDef {
  name: string
  description: string
  input_schema?: Record<string, unknown>
  path: string
  method: string
  auth_kind?: string
  auth_header?: string
  auth_key_ref?: string
}

export interface ToolSource {
  kind: ToolSourceKind
  api_tool?: APIToolDef
  server_alias?: string
  tool_name?: string
}

export interface VirtualMCPServer {
  name: string
  description?: string
  tenant_id?: number
  sources: ToolSource[]
}

// ── Credentials ───────────────────────────────────────────────────

export interface CredentialListResponse {
  credentials: string[]  // names only, e.g. ["llm:openai", "mcp:brave-search"]
}

// ── Subflow impact map ────────────────────────────────────────────

/** Which flows and APIs reference a given flow by name. */
export interface FlowImpact {
  flows: string[]   // names of flows that call this flow
  apis:  string[]   // names of APIs whose defaultFlow or endpoint flowName points here
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
  method: string
  flow_name: string
  action: string
}

export interface GatewayState {
  sync_uuid: string
  flows: GatewayFlow[]
  apis: GatewayApi[]
}
