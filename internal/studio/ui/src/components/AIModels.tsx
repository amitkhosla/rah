import { useEffect, useRef, useState } from 'react'
import { listLLMModels, upsertLLMModel, deleteLLMModel, testLLMModel, listQuotas, upsertQuota, deleteQuota } from '../api'
import type { TenantQuota } from '../api'
import type { LLMModel, LLMAdapter, ModelCapabilities, LLMTestDebug } from '../types'

// ── SecretRefBuilder ─────────────────────────────────────────────────
// Popover helper that constructs a secret reference URI without the user
// needing to know the exact format. Covers all supported backends.

// ── Flex-tier config ─────────────────────────────────────────────────
// Only adapters listed here support a "flex" tier. The key/value pair is
// merged into (or removed from) provider_params when the checkbox toggles.

interface FlexEntry {
  key: string
  value: unknown
  detect: (p: Record<string, unknown>) => boolean
  note: string
}

const FLEX_CONFIG: Partial<Record<string, FlexEntry>> = {
  openai: {
    key: 'service_tier',
    value: 'flex',
    detect: p => p['service_tier'] === 'flex',
    note: 'Async, lower-cost batch routing — OpenAI Flex tier',
  },
  gemini: {
    key: 'serviceTier',
    value: 'flex',
    detect: p => p['serviceTier'] === 'flex',
    note: 'Flex tier — lower cost, best-effort latency',
  },
}

function isFlexEnabled(adapter: string, paramsJson: string): boolean {
  const cfg = FLEX_CONFIG[adapter]
  if (!cfg || !paramsJson.trim()) return false
  try { return cfg.detect(JSON.parse(paramsJson) as Record<string, unknown>) }
  catch { return false }
}

function toggleFlex(adapter: string, enable: boolean, paramsJson: string): string {
  const cfg = FLEX_CONFIG[adapter]
  if (!cfg) return paramsJson
  let params: Record<string, unknown> = {}
  if (paramsJson.trim()) {
    try { params = JSON.parse(paramsJson) as Record<string, unknown> } catch { /* leave empty */ }
  }
  if (enable) { params[cfg.key] = cfg.value }
  else { delete params[cfg.key] }
  const keys = Object.keys(params)
  return keys.length === 0 ? '' : JSON.stringify(params, null, 2)
}

// Per-adapter defaults so users don't have to know the naming convention.
const ADAPTER_SUGGESTIONS: Record<string, { cred?: string; env?: string; note?: string }> = {
  openai:    { cred: 'llm:openai',    env: 'OPENAI_API_KEY' },
  anthropic: { cred: 'llm:anthropic', env: 'ANTHROPIC_API_KEY' },
  gemini:    { cred: 'llm:gemini',    env: 'GEMINI_API_KEY' },
  deepseek:  { cred: 'llm:deepseek',  env: 'DEEPSEEK_API_KEY' },
  ollama:    { note: 'Ollama runs locally — no API key required unless you set one.' },
  custom:    { cred: 'llm:custom',    env: 'CUSTOM_API_KEY' },
}

function SuggestionChip({ label, hint, active, onClick }: {
  label: string; hint: string; active?: boolean; onClick: () => void
}) {
  return (
    <button
      type="button"
      title={hint}
      onClick={onClick}
      style={{
        fontSize: 11, padding: '2px 8px', borderRadius: 4, cursor: 'pointer',
        border: `1px solid ${active ? 'var(--accent, #6366f1)' : 'var(--border)'}`,
        background: active ? 'var(--accent, #6366f1)' : 'transparent',
        color: active ? '#fff' : 'var(--muted)',
        fontFamily: 'monospace', lineHeight: '18px',
      }}
    >
      {label}
    </button>
  )
}

type SecretBackend = 'credential' | 'inline' | 'env' | 'file' | 'gsm' | 'vault' | 'awssm'

const BACKEND_OPTIONS: { value: SecretBackend; label: string; hint: string }[] = [
  { value: 'credential', label: 'Credential name',        hint: 'Named credential stored in this gateway (recommended)' },
  { value: 'inline',     label: 'Inline value',           hint: 'Paste key directly — dev/test only, stored encrypted' },
  { value: 'env',        label: 'Environment variable',   hint: 'Read from a server-side env var at runtime' },
  { value: 'file',       label: 'File path',              hint: 'Read from a file on the gateway host (e.g. K8s secret mount)' },
  { value: 'gsm',        label: 'Google Secret Manager',  hint: 'gsm:// URI — requires GSM enabled in gateway config' },
  { value: 'vault',      label: 'HashiCorp Vault',        hint: 'vault:// URI — requires Vault enabled in gateway config' },
  { value: 'awssm',      label: 'AWS Secrets Manager',    hint: 'awssm:// URI — requires AWS SM enabled in gateway config' },
]

function SecretRefBuilder({ value, onChange, adapter }: {
  value: string; onChange: (v: string) => void; adapter?: string
}) {
  const [open, setOpen]       = useState(false)
  const [backend, setBackend] = useState<SecretBackend>('credential')
  const [parts, setParts]     = useState<Record<string, string>>({})
  const popoverRef            = useRef<HTMLDivElement>(null)

  const sugg = adapter ? (ADAPTER_SUGGESTIONS[adapter] ?? {}) : {}

  // Close on outside click
  useEffect(() => {
    if (!open) return
    function handler(e: MouseEvent) {
      if (popoverRef.current && !popoverRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  function setPart(k: string, v: string) { setParts(p => ({ ...p, [k]: v })) }

  function buildRef(): string {
    const p = parts
    switch (backend) {
      case 'credential': return p.name ?? ''
      case 'inline':     return p.value ?? ''
      case 'env':        return `env:${p.var ?? ''}`
      case 'file':       return `file://${p.path ?? ''}`
      case 'gsm':        return `gsm://projects/${p.project ?? ''}/secrets/${p.secret ?? ''}/versions/${p.version || 'latest'}`
      case 'vault':      return `vault://${p.mount ?? 'kv'}/${p.path ?? ''}${p.field ? '#' + p.field : ''}`
      case 'awssm':      return `awssm://${p.region ?? 'us-east-1'}/${p.secret ?? ''}${p.field ? '#' + p.field : ''}`
    }
  }

  function apply() { onChange(buildRef()); setOpen(false); setParts({}) }

  // When opening the builder, pre-fill the credential name with the suggested value.
  function openBuilder() {
    if (!open && sugg.cred && !parts.name) setParts(p => ({ ...p, name: sugg.cred! }))
    setOpen(v => !v)
  }

  const envValue = sugg.env ? `env:${sugg.env}` : undefined

  return (
    <div>
      <div style={{ position: 'relative', display: 'flex', gap: 6 }}>
        <input className="input" value={value} placeholder="e.g. llm:openai  or  env:OPENAI_API_KEY"
          onChange={e => onChange(e.target.value)}
          style={{ flex: 1 }} />
        <button type="button" className="btn"
          style={{ width: 'auto', padding: '0 10px', fontSize: 12, flexShrink: 0 }}
          onClick={openBuilder} title="Secret reference builder">
          ⚙ Build
        </button>

      {open && (
        <div ref={popoverRef} style={{
          position: 'absolute', top: '110%', right: 0, zIndex: 200,
          background: 'var(--surface)', border: '1px solid var(--border)',
          borderRadius: 8, padding: 16, width: 360, boxShadow: '0 8px 24px rgba(0,0,0,.4)',
        }}>
          <div style={{ fontSize: 12, fontWeight: 700, marginBottom: 10 }}>Secret Reference Builder</div>

          {/* Backend selector */}
          <div style={{ marginBottom: 12 }}>
            <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 4 }}>Source</div>
            <select className="input" value={backend}
              onChange={e => { setBackend(e.target.value as SecretBackend); setParts({}) }}>
              {BACKEND_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
            <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4 }}>
              {BACKEND_OPTIONS.find(o => o.value === backend)?.hint}
            </div>
          </div>

          {/* Per-backend fields */}
          {backend === 'credential' && (<>
            {sugg.cred && (
              <div style={{ display: 'flex', gap: 6, marginBottom: 8, alignItems: 'center' }}>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>Suggested:</span>
                <SuggestionChip label={sugg.cred} hint="Recommended credential name for this adapter"
                  active={parts.name === sugg.cred}
                  onClick={() => setPart('name', sugg.cred!)} />
              </div>
            )}
            <RefField label="Credential name" hint='Set the value in Tenants → Credentials'
              value={parts.name ?? ''} onChange={v => setPart('name', v)} placeholder="llm:openai" />
          </>)}
          {backend === 'env' && (<>
            {sugg.env && (
              <div style={{ display: 'flex', gap: 6, marginBottom: 8, alignItems: 'center' }}>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>Suggested:</span>
                <SuggestionChip label={sugg.env} hint="Conventional env var name for this provider"
                  active={parts.var === sugg.env}
                  onClick={() => setPart('var', sugg.env!)} />
              </div>
            )}
          </>)}
          {backend === 'inline' && (
            <RefField label="Key value" hint="Stored encrypted. Use credential name for production."
              value={parts.value ?? ''} onChange={v => setPart('value', v)} placeholder="sk-…" password />
          )}
          {backend === 'env' && (
            <RefField label="Environment variable name" hint="Must be set on the gateway host"
              value={parts.var ?? ''} onChange={v => setPart('var', v)} placeholder="OPENAI_API_KEY" />
          )}
          {backend === 'file' && (
            <RefField label="File path" hint="Absolute path on the gateway host"
              value={parts.path ?? ''} onChange={v => setPart('path', v)} placeholder="/run/secrets/openai-key" />
          )}
          {backend === 'gsm' && (<>
            <RefField label="GCP Project ID" value={parts.project ?? ''} onChange={v => setPart('project', v)} placeholder="my-gcp-project" />
            <RefField label="Secret name" value={parts.secret ?? ''} onChange={v => setPart('secret', v)} placeholder="openai-api-key" />
            <RefField label="Version" hint='Default: "latest"' value={parts.version ?? ''} onChange={v => setPart('version', v)} placeholder="latest" />
          </>)}
          {backend === 'vault' && (<>
            <RefField label="Mount" hint='KV mount path, e.g. "kv" or "secret"' value={parts.mount ?? ''} onChange={v => setPart('mount', v)} placeholder="kv" />
            <RefField label="Secret path" value={parts.path ?? ''} onChange={v => setPart('path', v)} placeholder="llm/openai" />
            <RefField label="Field" hint="JSON key inside the secret (optional)" value={parts.field ?? ''} onChange={v => setPart('field', v)} placeholder="api_key" />
          </>)}
          {backend === 'awssm' && (<>
            <RefField label="AWS Region" value={parts.region ?? ''} onChange={v => setPart('region', v)} placeholder="us-east-1" />
            <RefField label="Secret name / ARN" value={parts.secret ?? ''} onChange={v => setPart('secret', v)} placeholder="prod/openai-key" />
            <RefField label="JSON field" hint="If the secret is a JSON object (optional)" value={parts.field ?? ''} onChange={v => setPart('field', v)} placeholder="api_key" />
          </>)}

          {/* Preview */}
          <div style={{ margin: '12px 0 10px', padding: '6px 8px', background: 'var(--bg)', borderRadius: 4,
            fontFamily: 'monospace', fontSize: 11, wordBreak: 'break-all', color: 'var(--muted)' }}>
            {buildRef() || <span style={{ opacity: 0.5 }}>fill in fields above…</span>}
          </div>

          <button className="btn" style={{ width: '100%' }} onClick={apply}
            disabled={!buildRef()}>
            Apply
          </button>
        </div>
      )}
      </div>{/* end inner flex row */}

      {/* ── Suggestion chips (always visible below the input) ── */}
      {(sugg.cred || sugg.env) && (
        <div style={{ display: 'flex', gap: 6, marginTop: 6, flexWrap: 'wrap', alignItems: 'center' }}>
          <span style={{ fontSize: 11, color: 'var(--muted)' }}>Quick-fill:</span>
          {sugg.cred && (
            <SuggestionChip
              label={sugg.cred}
              hint="Named credential stored in the gateway (set value in Tenants → Credentials)"
              active={value === sugg.cred}
              onClick={() => onChange(sugg.cred!)}
            />
          )}
          {envValue && (
            <SuggestionChip
              label={envValue}
              hint={`Server-side env var — set ${sugg.env} on the gateway host`}
              active={value === envValue}
              onClick={() => onChange(envValue)}
            />
          )}
        </div>
      )}
      {sugg.note && (
        <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 5 }}>{sugg.note}</div>
      )}
    </div>
  )
}

function RefField({ label, hint, value, onChange, placeholder, password }:
  { label: string; hint?: string; value: string; onChange: (v: string) => void; placeholder?: string; password?: boolean }) {
  return (
    <div style={{ marginBottom: 8 }}>
      <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 3 }}>{label}{hint && <span style={{ opacity: 0.6 }}> — {hint}</span>}</div>
      <input className="input" type={password ? 'password' : 'text'} value={value}
        placeholder={placeholder} onChange={e => onChange(e.target.value)} />
    </div>
  )
}

// ── Adapter metadata ────────────────────────────────────────────────

const ADAPTER_META: Record<LLMAdapter, { label: string; color: string }> = {
  anthropic: { label: 'Anthropic', color: '#a78bfa' },
  openai:    { label: 'OpenAI',    color: '#22c55e' },
  gemini:    { label: 'Gemini',    color: '#3b82f6' },
  ollama:    { label: 'Ollama',    color: '#f97316' },
  deepseek:  { label: 'DeepSeek', color: '#06b6d4' },
  custom:    { label: 'Custom',   color: '#94a3b8' },
}

// ── Quick-fill presets ──────────────────────────────────────────────

interface Preset { label: string; model: LLMModel }

const PRESETS: Preset[] = [
  {
    label: 'GPT-4o',
    model: {
      alias: 'gpt-4o', provider: 'OpenAI', adapter: 'openai',
      api_key_ref: 'llm:openai',
      max_tokens: 4096,
      capabilities: { max_context_tokens: 128000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'GPT-4o mini',
    model: {
      alias: 'gpt-4o-mini', provider: 'OpenAI', adapter: 'openai',
      api_key_ref: 'llm:openai',
      max_tokens: 4096,
      capabilities: { max_context_tokens: 128000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'Claude 3.5 Sonnet',
    model: {
      alias: 'claude-3-5-sonnet', provider: 'Anthropic', adapter: 'anthropic',
      api_key_ref: 'llm:anthropic', max_tokens: 8192,
      capabilities: { max_context_tokens: 200000, supported_tool_formats: ['claude-tools'] },
    },
  },
  {
    label: 'Claude 3 Haiku',
    model: {
      alias: 'claude-3-haiku', provider: 'Anthropic', adapter: 'anthropic',
      api_key_ref: 'llm:anthropic', max_tokens: 4096,
      capabilities: { max_context_tokens: 200000, supported_tool_formats: ['claude-tools'] },
    },
  },
  {
    label: 'Gemini 1.5 Pro',
    model: {
      alias: 'gemini-1.5-pro', provider: 'Google', adapter: 'gemini',
      api_key_ref: 'llm:gemini', max_tokens: 8192, api_version: 'v1beta',
      capabilities: { max_context_tokens: 1000000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'Llama 3 (Ollama)',
    model: {
      alias: 'llama3', provider: 'Meta', adapter: 'ollama',
      base_url: 'http://localhost:11434', max_tokens: 4096,
      capabilities: { max_context_tokens: 8192 },
    },
  },
  {
    label: 'HuggingFace TGI',
    model: {
      alias: 'hf-tgi', provider: 'HuggingFace', adapter: 'custom',
      base_url: 'https://api-inference.huggingface.co/models/<model-id>',
      api_key_ref: 'llm:huggingface', max_tokens: 2048,
      auth_header_name: 'Authorization', auth_header_prefix: 'Bearer ',
      capabilities: { max_context_tokens: 8192 },
    },
  },
  {
    label: 'Groq',
    model: {
      alias: 'groq-llama3', provider: 'Groq', adapter: 'custom',
      base_url: 'https://api.groq.com/openai', api_key_ref: 'llm:groq',
      max_tokens: 4096,
      auth_header_name: 'Authorization', auth_header_prefix: 'Bearer ',
      capabilities: { max_context_tokens: 128000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'Together AI',
    model: {
      alias: 'together-llama3', provider: 'Together AI', adapter: 'custom',
      base_url: 'https://api.together.xyz', api_key_ref: 'llm:together',
      max_tokens: 4096,
      auth_header_name: 'Authorization', auth_header_prefix: 'Bearer ',
      capabilities: { max_context_tokens: 8192, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'LM Studio (local)',
    model: {
      alias: 'lmstudio-local', provider: 'LM Studio', adapter: 'custom',
      base_url: 'http://localhost:1234', max_tokens: 4096,
      auth_header_name: 'Authorization', auth_header_prefix: 'Bearer ',
      capabilities: { max_context_tokens: 8192 },
    },
  },
  {
    label: 'vLLM (local)',
    model: {
      alias: 'vllm-local', provider: 'vLLM', adapter: 'custom',
      base_url: 'http://localhost:8000', max_tokens: 4096,
      auth_header_name: 'Authorization', auth_header_prefix: 'Bearer ',
      capabilities: { max_context_tokens: 8192 },
    },
  },

  // ── Gemma (Ollama) ───────────────────────────────────────────────
  {
    label: 'Gemma 3 2B (Ollama)',
    model: {
      alias: 'gemma3:2b', provider: 'Google (Ollama)', adapter: 'ollama',
      base_url: 'http://localhost:11434', max_tokens: 2048,
      capabilities: { max_context_tokens: 8192 },
    },
  },
  {
    label: 'Gemma 3 4B (Ollama)',
    model: {
      alias: 'gemma3:4b', provider: 'Google (Ollama)', adapter: 'ollama',
      base_url: 'http://localhost:11434', max_tokens: 4096,
      capabilities: { max_context_tokens: 32768 },
    },
  },
  {
    label: 'Gemma 2 27B (Ollama)',
    model: {
      alias: 'gemma2:27b', provider: 'Google (Ollama)', adapter: 'ollama',
      base_url: 'http://localhost:11434', max_tokens: 4096,
      capabilities: { max_context_tokens: 8192 },
    },
  },
  {
    label: 'Gemma 3 27B (Ollama)',
    model: {
      alias: 'gemma3:27b', provider: 'Google (Ollama)', adapter: 'ollama',
      base_url: 'http://localhost:11434', max_tokens: 8192,
      capabilities: { max_context_tokens: 32768 },
    },
  },

  // ── Gemma — Google AI (public API, endpoint_override per model) ─────
  // The Gemma models use the same Gemini wire format but the model ID is embedded in the URL path.
  // endpoint_override bypasses adapter URL construction; auth via x-goog-api-key header.
  {
    label: 'Gemma 3 4B IT (Google AI)',
    model: {
      alias: 'gemma-3-4b-it', provider: 'Google', adapter: 'gemini',
      api_key_ref: 'llm:gemini', max_tokens: 4096,
      endpoint_override: 'https://generativelanguage.googleapis.com/v1beta/models/gemma-3-4b-it:generateContent',
      capabilities: { max_context_tokens: 32768 },
    },
  },
  {
    label: 'Gemma 3 12B IT (Google AI)',
    model: {
      alias: 'gemma-3-12b-it', provider: 'Google', adapter: 'gemini',
      api_key_ref: 'llm:gemini', max_tokens: 4096,
      endpoint_override: 'https://generativelanguage.googleapis.com/v1beta/models/gemma-3-12b-it:generateContent',
      capabilities: { max_context_tokens: 32768 },
    },
  },
  {
    label: 'Gemma 3 27B IT (Google AI)',
    model: {
      alias: 'gemma-3-27b-it', provider: 'Google', adapter: 'gemini',
      api_key_ref: 'llm:gemini', max_tokens: 8192,
      endpoint_override: 'https://generativelanguage.googleapis.com/v1beta/models/gemma-3-27b-it:generateContent',
      capabilities: { max_context_tokens: 131072 },
    },
  },

  // ── Gemini 2.5 — Google AI (public API, api key) ─────────────────
  {
    label: 'Gemini 2.5 Flash-Lite',
    model: {
      alias: 'gemini-2.5-flash-lite', provider: 'Google', adapter: 'gemini',
      api_key_ref: 'llm:gemini', max_tokens: 8192, api_version: 'v1beta',
      capabilities: { max_context_tokens: 1000000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'Gemini 2.5 Flash',
    model: {
      alias: 'gemini-2.5-flash', provider: 'Google', adapter: 'gemini',
      api_key_ref: 'llm:gemini', max_tokens: 16384, api_version: 'v1beta',
      capabilities: { max_context_tokens: 1000000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'Gemini 2.5 Pro',
    model: {
      alias: 'gemini-2.5-pro', provider: 'Google', adapter: 'gemini',
      api_key_ref: 'llm:gemini', max_tokens: 16384, api_version: 'v1beta',
      capabilities: { max_context_tokens: 1000000, supported_tool_formats: ['openai-tools'] },
    },
  },

  // ── Gemini 2.5 — Vertex AI (managed decoding, routing_config) ────
  // Set base_url to: https://us-central1-aiplatform.googleapis.com/v1/projects/YOUR_PROJECT_ID/locations/us-central1/publishers/google/models
  // Auth: use a Google OAuth access token or a Vertex AI API key in api_key_ref.
  {
    label: 'Gemini 2.5 Flash-Lite (Vertex AI)',
    model: {
      alias: 'gemini-2.5-flash-lite-vertex', provider: 'Google Vertex AI', adapter: 'gemini',
      base_url: 'https://us-central1-aiplatform.googleapis.com/v1/projects/YOUR_PROJECT_ID/locations/us-central1/publishers/google/models',
      api_key_ref: 'llm:vertex', max_tokens: 8192,
      capabilities: { max_context_tokens: 1000000, supported_tool_formats: ['openai-tools'] },
      provider_params: { routing_config: { managed_decoding: {} } },
    },
  },
  {
    label: 'Gemini 2.5 Flash (Vertex AI)',
    model: {
      alias: 'gemini-2.5-flash-vertex', provider: 'Google Vertex AI', adapter: 'gemini',
      base_url: 'https://us-central1-aiplatform.googleapis.com/v1/projects/YOUR_PROJECT_ID/locations/us-central1/publishers/google/models',
      api_key_ref: 'llm:vertex', max_tokens: 16384,
      capabilities: { max_context_tokens: 1000000, supported_tool_formats: ['openai-tools'] },
      provider_params: { routing_config: { managed_decoding: {} } },
    },
  },
  {
    label: 'Gemini 2.5 Pro (Vertex AI)',
    model: {
      alias: 'gemini-2.5-pro-vertex', provider: 'Google Vertex AI', adapter: 'gemini',
      base_url: 'https://us-central1-aiplatform.googleapis.com/v1/projects/YOUR_PROJECT_ID/locations/us-central1/publishers/google/models',
      api_key_ref: 'llm:vertex', max_tokens: 16384,
      capabilities: { max_context_tokens: 1000000, supported_tool_formats: ['openai-tools'] },
      provider_params: { routing_config: { managed_decoding: {} } },
    },
  },

  // ── OpenAI (o-series / GPT-5+) ─────────────────────────────────
  // These models require max_completion_tokens instead of max_tokens.
  {
    label: 'GPT-5 Nano (Flex)',
    model: {
      alias: 'gpt-5-nano', provider: 'OpenAI', adapter: 'openai',
      api_key_ref: 'llm:openai',
      max_tokens: 8192, use_completion_tokens: true,
      capabilities: { max_context_tokens: 128000, supported_tool_formats: ['openai-tools'] },
      provider_params: { service_tier: 'flex' },
    },
  },
  {
    label: 'o4-mini (Flex)',
    model: {
      alias: 'o4-mini', provider: 'OpenAI', adapter: 'openai',
      api_key_ref: 'llm:openai',
      max_tokens: 65536, use_completion_tokens: true,
      capabilities: { max_context_tokens: 200000, supported_tool_formats: ['openai-tools'] },
      provider_params: { service_tier: 'flex' },
    },
  },
  {
    label: 'GPT-5 Mini (Flex)',
    model: {
      alias: 'gpt-5-mini', provider: 'OpenAI', adapter: 'openai',
      api_key_ref: 'llm:openai',
      max_tokens: 16384, use_completion_tokens: true,
      capabilities: { max_context_tokens: 128000, supported_tool_formats: ['openai-tools'] },
      provider_params: { service_tier: 'flex' },
    },
  },
  {
    label: 'GPT-5.1 (Flex)',
    model: {
      alias: 'gpt-5.1', provider: 'OpenAI', adapter: 'openai',
      api_key_ref: 'llm:openai',
      max_tokens: 32768, use_completion_tokens: true,
      capabilities: { max_context_tokens: 128000, supported_tool_formats: ['openai-tools'] },
      provider_params: { service_tier: 'flex' },
    },
  },

  // ── Anthropic ────────────────────────────────────────────────────
  {
    label: 'Claude Haiku 4.5',
    model: {
      alias: 'claude-haiku-4-5-20251001', provider: 'Anthropic', adapter: 'anthropic',
      api_key_ref: 'llm:anthropic', max_tokens: 8192,
      capabilities: { max_context_tokens: 200000, supported_tool_formats: ['claude-tools'] },
    },
  },
  {
    label: 'Claude Sonnet 4.6',
    model: {
      alias: 'claude-sonnet-4-6', provider: 'Anthropic', adapter: 'anthropic',
      api_key_ref: 'llm:anthropic', max_tokens: 8192,
      capabilities: { max_context_tokens: 200000, supported_tool_formats: ['claude-tools'] },
    },
  },

  // ── DeepSeek ─────────────────────────────────────────────────────
  {
    label: 'DeepSeek V3 Chat',
    model: {
      alias: 'deepseek-chat', provider: 'DeepSeek', adapter: 'deepseek',
      base_url: 'https://api.deepseek.com', api_key_ref: 'llm:deepseek',
      max_tokens: 8192,
      capabilities: { max_context_tokens: 64000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'DeepSeek R1 (Reasoner)',
    model: {
      alias: 'deepseek-reasoner', provider: 'DeepSeek', adapter: 'deepseek',
      base_url: 'https://api.deepseek.com', api_key_ref: 'llm:deepseek',
      max_tokens: 16384,
      capabilities: { max_context_tokens: 64000 },
    },
  },
]

// ── Blank form state ────────────────────────────────────────────────

function blankModel(): LLMModel {
  return {
    alias: '', provider: '', adapter: 'openai',
    base_url: '', api_key_ref: '', max_tokens: 4096,
    capabilities: { max_context_tokens: 128000, max_system_prompt_tokens: 0, max_history_turns: 0 },
  }
}

// ── Component ───────────────────────────────────────────────────────

export default function AIModels() {
  const [models, setModels]     = useState<LLMModel[]>([])
  const [loading, setLoading]   = useState(true)
  const [err, setErr]           = useState('')
  const [showForm, setShowForm] = useState(false)
  const [form, setForm]         = useState<LLMModel>(blankModel())
  const [providerParamsJson, setProviderParamsJson] = useState('')
  const [extraHeadersJson, setExtraHeadersJson]     = useState('')
  const [saving, setSaving]     = useState(false)
  const [saveMsg, setSaveMsg]   = useState('')
  const [saveMsgErr, setSaveMsgErr] = useState(false)
  const [testStatus, setTestStatus] = useState<Record<string, { state: 'idle'|'running'|'ok'|'err', msg?: string, fullErr?: string }>>({})
  const [testExpanded, setTestExpanded] = useState(false)
  const [testPrompt, setTestPrompt]   = useState('Say OK')
  const [testRunning, setTestRunning] = useState(false)
  const [testResult, setTestResult]   = useState<{ ok: boolean; response?: string; latency_ms?: number; input_tokens?: number; output_tokens?: number; error?: string; debug?: LLMTestDebug } | null>(null)

  async function load() {
    setLoading(true); setErr('')
    try { setModels(await listLLMModels()) }
    catch (e) { setErr(e instanceof Error ? e.message : 'Failed to load models') }
    finally { setLoading(false) }
  }

  useEffect(() => { void load() }, [])

  function applyPreset(p: Preset) {
    setForm({ ...p.model })
    setProviderParamsJson(p.model.provider_params ? JSON.stringify(p.model.provider_params, null, 2) : '')
    setExtraHeadersJson(p.model.extra_headers ? JSON.stringify(p.model.extra_headers, null, 2) : '')
    setShowForm(true)
  }

  function setField(key: keyof LLMModel, val: string | number | boolean | Array<{ window: string; limit: number }> | undefined) {
    setForm(f => ({ ...f, [key]: val }))
    if (key === 'alias') setTestResult(null)
  }

  function setCap(key: keyof ModelCapabilities, val: number) {
    setForm(f => ({ ...f, capabilities: { ...f.capabilities, [key]: val } }))
  }

  async function handleSave() {
    if (!form.alias.trim()) { setSaveMsgErr(true); setSaveMsg('Alias is required'); return }
    let providerParams: Record<string, any> | undefined
    if (providerParamsJson.trim()) {
      try { providerParams = JSON.parse(providerParamsJson) }
      catch { setSaveMsgErr(true); setSaveMsg('Provider params: invalid JSON'); return }
    }
    let extraHeaders: Record<string, string> | undefined
    if (extraHeadersJson.trim()) {
      try { extraHeaders = JSON.parse(extraHeadersJson) }
      catch { setSaveMsgErr(true); setSaveMsg('Extra headers: invalid JSON (must be {"Header-Name": "value"})'); return }
    }
    setSaving(true); setSaveMsg(''); setSaveMsgErr(false)
    try {
      await upsertLLMModel({ ...form, provider_params: providerParams, extra_headers: extraHeaders })
      setSaveMsg(`Model "${form.alias}" saved.`)
      setSaveMsgErr(false)
      setForm(blankModel())
      setProviderParamsJson('')
      setExtraHeadersJson('')
      setShowForm(false)
      await load()
    } catch (e) {
      setSaveMsgErr(true)
      setSaveMsg(e instanceof Error ? e.message : 'Save failed')
    } finally { setSaving(false) }
  }

  async function handleDelete(alias: string) {
    if (!confirm(`Delete model "${alias}"?`)) return
    try { await deleteLLMModel(alias); await load() }
    catch (e) { alert(e instanceof Error ? e.message : 'Delete failed') }
  }

  async function handleQuickTest(alias: string) {
    setTestStatus(s => ({ ...s, [alias]: { state: 'running' } }))
    try {
      const env = await testLLMModel(alias)
      if (env.ok && env.data) {
        setTestStatus(s => ({ ...s, [alias]: { state: 'ok', msg: `${env.data!.latency_ms}ms · ${env.data!.output_tokens} tok` } }))
      } else {
        const full = env.debug
          ? `HTTP ${env.debug.http_status} → ${env.debug.endpoint}\nModel sent: ${env.debug.model_id_sent}\n${env.debug.response_body}`
          : (env.error ?? 'unknown error')
        const short = full.length > 60 ? full.slice(0, 60) + '…' : full
        setTestStatus(s => ({ ...s, [alias]: { state: 'err', msg: short, fullErr: full } }))
      }
    } catch (e) {
      const full = e instanceof Error ? e.message : 'error'
      const short = full.length > 60 ? full.slice(0, 60) + '…' : full
      setTestStatus(s => ({ ...s, [alias]: { state: 'err', msg: short, fullErr: full } }))
    }
  }

  async function handleFormTest() {
    if (!form.alias.trim()) { setTestResult({ ok: false, error: 'Save the model first (alias required)' }); return }
    setTestRunning(true); setTestResult(null)
    try {
      const env = await testLLMModel(form.alias, testPrompt || 'Say OK')
      if (env.ok && env.data) {
        setTestResult({ ok: true, ...env.data })
      } else {
        setTestResult({ ok: false, error: env.error ?? 'unknown error', debug: env.debug })
      }
    } catch (e) {
      setTestResult({ ok: false, error: e instanceof Error ? e.message : 'error' })
    } finally { setTestRunning(false) }
  }

  return (
    <div>
      {/* ── Header ── */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
        <div>
          <span style={{ fontSize: 15, fontWeight: 700 }}>LLM Models</span>
          <span style={{ color: 'var(--muted)', fontSize: 13, marginLeft: 10 }}>
            {models.length} model{models.length !== 1 ? 's' : ''} registered
          </span>
        </div>
        <button
          className="btn"
          style={{ width: 'auto', padding: '0 18px' }}
          onClick={() => { setForm(blankModel()); setShowForm(v => !v) }}
        >
          {showForm ? 'Cancel' : '+ Add Model'}
        </button>
      </div>

      {err && <p className="status-err" style={{ marginBottom: 12 }}>{err}</p>}

      {/* ── Quick presets (only shown when form is open) ── */}
      {showForm && (
        <div style={{ marginBottom: 16 }}>
          <div style={{ fontSize: 11, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 8 }}>
            Quick presets
          </div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {PRESETS.map(p => (
              <button
                key={p.label}
                className="btn muted"
                style={{ width: 'auto', padding: '4px 12px', fontSize: 12, marginTop: 0 }}
                onClick={() => applyPreset(p)}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* ── Add / Edit form ── */}
      {showForm && (
        <div className="panel" style={{ marginBottom: 20 }}>
          <div className="panel-header">
            {form.alias ? `Editing: ${form.alias}` : 'New Model'}
          </div>
          <div className="panel-body">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <Field label="Alias *" hint="Internal name used in flows — can be any friendly label">
                <input className="input" value={form.alias} placeholder="e.g. gpt-4o-latest"
                  onChange={e => setField('alias', e.target.value)} />
              </Field>
              <Field label="Model ID" hint="Actual model name sent to the provider API — leave blank to use Alias">
                <input className="input" value={form.model_id ?? ''} placeholder={form.alias || 'e.g. gpt-4o-2024-08-06'}
                  onChange={e => setField('model_id', e.target.value)} />
              </Field>
              <Field label="Provider" hint="Display name">
                <input className="input" value={form.provider} placeholder="e.g. OpenAI"
                  onChange={e => setField('provider', e.target.value)} />
              </Field>
              <Field label="Adapter *" hint="Wire format — use 'custom' for any OpenAI-compatible endpoint (HuggingFace, vLLM, Groq, Together AI, LM Studio…)">
                <select className="input" value={form.adapter}
                  onChange={e => setField('adapter', e.target.value as LLMAdapter)}>
                  <option value="openai">openai</option>
                  <option value="anthropic">anthropic</option>
                  <option value="gemini">gemini</option>
                  <option value="ollama">ollama</option>
                  <option value="deepseek">deepseek</option>
                  <option value="custom">custom (OpenAI-compatible)</option>
                </select>
              </Field>
              {form.adapter === 'gemini' && (
                <Field label="API Version" hint='v1beta (default) supports system instructions, tools, and thinking. Use v1 only if you specifically need the stable-only endpoint.'>
                  <select className="input" value={form.api_version ?? 'v1beta'}
                    onChange={e => setField('api_version', e.target.value as 'v1' | 'v1beta')}>
                    <option value="v1beta">v1beta — recommended (system prompt, tools, thinking)</option>
                    <option value="v1">v1 — stable only (no system prompt or tools)</option>
                  </select>
                </Field>
              )}
              {form.adapter === 'custom' && <>
                <Field label="Auth Header Name" hint='HTTP header for the API key (default: "Authorization")'>
                  <input className="input" value={form.auth_header_name ?? ''}
                    placeholder="Authorization"
                    onChange={e => setField('auth_header_name', e.target.value)} />
                </Field>
                <Field label="Auth Header Prefix" hint='Prefix before the key value (default: "Bearer ")'>
                  <input className="input" value={form.auth_header_prefix ?? ''}
                    placeholder='Bearer '
                    onChange={e => setField('auth_header_prefix', e.target.value)} />
                </Field>
              </>}
              <Field label="API Key Ref" hint="Credential name, inline value, or secret backend URI">
                <SecretRefBuilder value={form.api_key_ref ?? ''}
                  onChange={v => setField('api_key_ref', v)}
                  adapter={form.adapter} />
              </Field>
              <Field
                label={form.adapter === 'custom' ? 'Endpoint URL' : 'Base URL'}
                hint={form.adapter === 'custom'
                  ? 'Full endpoint URL — use {model} as a placeholder for the model ID, e.g. https://api.groq.com/openai/v1/chat/completions or https://my-proxy/{model}/chat'
                  : 'Leave blank to use provider default'}
              >
                <input className="input" value={form.base_url ?? ''}
                  placeholder={form.adapter === 'custom'
                    ? 'https://api.groq.com/openai/v1/chat/completions'
                    : 'https://api.openai.com'}
                  onChange={e => setField('base_url', e.target.value)} />
              </Field>
              <Field label="Max Output Tokens" hint="Max tokens in each response">
                <input className="input" type="number" value={form.max_tokens ?? 4096}
                  onChange={e => setField('max_tokens', parseInt(e.target.value) || 0)} />
              </Field>
              <Field label="Max Context Tokens" hint="Total context window size">
                <input className="input" type="number" value={form.capabilities.max_context_tokens ?? 0}
                  onChange={e => setCap('max_context_tokens', parseInt(e.target.value) || 0)} />
              </Field>
              <Field label="Max System Prompt Tokens" hint="System prompt budget (0 = unlimited)">
                <input className="input" type="number" value={form.capabilities.max_system_prompt_tokens ?? 0}
                  onChange={e => setCap('max_system_prompt_tokens', parseInt(e.target.value) || 0)} />
              </Field>
              <Field label="Max History Turns" hint="Max conversation turns to retain (0 = unlimited)">
                <input className="input" type="number" value={form.capabilities.max_history_turns ?? 0}
                  onChange={e => setCap('max_history_turns', parseInt(e.target.value) || 0)} />
              </Field>
              <Field label="Cost per 1M input tokens (USD)" hint="Used to estimate call cost in traces">
                <input className="input" type="number" step="0.01" min="0"
                  value={form.cost_per_input_token ?? ''}
                  onChange={e => setField('cost_per_input_token', parseFloat(e.target.value) || 0)}
                  placeholder="e.g. 0.25" />
              </Field>
              <Field label="Cost per 1M output tokens (USD)" hint="Used to estimate call cost in traces">
                <input className="input" type="number" step="0.01" min="0"
                  value={form.cost_per_output_token ?? ''}
                  onChange={e => setField('cost_per_output_token', parseFloat(e.target.value) || 0)}
                  placeholder="e.g. 1.25" />
              </Field>
            </div>

            {/* ── Provider Rate Limits ── */}
            <div style={{ marginTop: 14 }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                  Provider Rate Limits
                </span>
                <button type="button" className="btn" style={{ fontSize: 11, padding: '2px 8px' }}
                  onClick={() => setField('rate_limits', [...(form.rate_limits ?? []), { window: 'minute', limit: 60 }])}>
                  + Add Window
                </button>
              </div>
              {(form.rate_limits ?? []).length === 0 && (
                <p style={{ fontSize: 12, color: 'var(--muted)', margin: '4px 0 8px' }}>
                  No limits — gateway relies on provider 429s. Add windows to proactively redirect to fallback before burning API quota.
                </p>
              )}
              {(form.rate_limits ?? []).map((rl, idx) => (
                <div key={idx} style={{ display: 'flex', gap: 6, alignItems: 'center', marginBottom: 4 }}>
                  <select className="input" style={{ flex: '0 0 110px', fontSize: 12 }}
                    value={rl.window}
                    onChange={e => {
                      const updated = [...(form.rate_limits ?? [])]
                      updated[idx] = { ...updated[idx], window: e.target.value }
                      setField('rate_limits', updated)
                    }}>
                    <option value="second">/ second</option>
                    <option value="minute">/ minute</option>
                    <option value="hour">/ hour</option>
                    <option value="day">/ day</option>
                  </select>
                  <input className="input" type="number" min="1" style={{ flex: 1, fontSize: 12 }}
                    value={rl.limit}
                    placeholder="limit"
                    onChange={e => {
                      const updated = [...(form.rate_limits ?? [])]
                      updated[idx] = { ...updated[idx], limit: parseInt(e.target.value) || 1 }
                      setField('rate_limits', updated)
                    }} />
                  <button type="button" style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 14, lineHeight: 1, padding: '0 4px' }}
                    title="Remove window"
                    onClick={() => {
                      const updated = (form.rate_limits ?? []).filter((_, i) => i !== idx)
                      setField('rate_limits', updated.length > 0 ? updated : undefined)
                    }}>✕</button>
                </div>
              ))}
            </div>

            {/* ── Inline test panel (collapsible) ── */}
            <div style={{ marginTop: 14, border: '1px solid var(--border)', borderRadius: 6, overflow: 'hidden' }}>
              {/* Header / toggle */}
              <button
                type="button"
                onClick={() => setTestExpanded(v => !v)}
                style={{ width: '100%', background: 'var(--surface)', border: 'none', cursor: 'pointer',
                  padding: '8px 14px', display: 'flex', alignItems: 'center', gap: 8, textAlign: 'left' }}>
                <span style={{ fontSize: 13, color: 'var(--muted)', fontWeight: 700, userSelect: 'none' }}>
                  {testExpanded ? '▾' : '▸'}
                </span>
                <span style={{ fontSize: 12, fontWeight: 700, flex: 1 }}>Test Model</span>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>
                  {form.alias.trim() ? `alias: ${form.alias}` : 'save model first'}
                </span>
              </button>

              {testExpanded && (
                <div style={{ padding: '10px 14px', borderTop: '1px solid var(--border)' }}>
                  <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 8 }}>
                    Sends a live request directly to the provider using the API key set above.
                    The model must already be saved (alias must exist in the catalog).
                  </div>
                  <div style={{ display: 'flex', gap: 8, marginBottom: testResult ? 10 : 0 }}>
                    <input className="input" value={testPrompt}
                      onChange={e => setTestPrompt(e.target.value)}
                      placeholder="Say OK"
                      style={{ flex: 1, fontSize: 13 }} />
                    <button className="btn" disabled={testRunning || !form.alias.trim()}
                      style={{ width: 'auto', padding: '0 18px', flexShrink: 0 }}
                      onClick={handleFormTest}>
                      {testRunning ? 'Running…' : 'Run'}
                    </button>
                  </div>
                  {testResult && (
                    <div style={{ marginTop: 10, padding: '10px 12px', borderRadius: 5,
                      background: testResult.ok ? 'rgba(34,197,94,0.07)' : 'rgba(239,68,68,0.07)',
                      border: `1px solid ${testResult.ok ? '#22c55e44' : '#ef444444'}` }}>
                      {testResult.ok ? (<>
                        <div style={{ display: 'flex', gap: 16, fontSize: 11, color: 'var(--muted)', marginBottom: 6 }}>
                          <span>⏱ {testResult.latency_ms}ms</span>
                          <span>↑ {testResult.input_tokens} tok in</span>
                          <span>↓ {testResult.output_tokens} tok out</span>
                        </div>
                        <div style={{ fontFamily: 'monospace', fontSize: 13, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                          {testResult.response}
                        </div>
                      </>) : (<>
                        <div style={{ fontSize: 12, fontWeight: 700, color: '#ef4444', marginBottom: 8 }}>
                          {testResult.error}
                        </div>
                        {testResult.debug && (
                          <table style={{ width: '100%', fontSize: 11, borderCollapse: 'collapse' }}>
                            <tbody>
                              {[
                                ['Endpoint', testResult.debug.endpoint],
                                ['Model ID sent', testResult.debug.model_id_sent],
                                ['HTTP status', String(testResult.debug.http_status)],
                                ['Provider response', testResult.debug.response_body],
                              ].map(([label, val]) => (
                                <tr key={label} style={{ borderTop: '1px solid #ef444422' }}>
                                  <td style={{ padding: '4px 8px 4px 0', color: 'var(--muted)', whiteSpace: 'nowrap', verticalAlign: 'top' }}>{label}</td>
                                  <td style={{ padding: '4px 0', fontFamily: 'monospace', wordBreak: 'break-all' }}>{val}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        )}
                      </>)}
                    </div>
                  )}
                </div>
              )}
            </div>

            {/* ── Flex tier toggle (only shown for adapters that support it) ── */}
            {FLEX_CONFIG[form.adapter] && (() => {
              const flexCfg = FLEX_CONFIG[form.adapter]!
              const enabled = isFlexEnabled(form.adapter, providerParamsJson)
              return (
                <div style={{ marginTop: 14, padding: '10px 14px', borderRadius: 6,
                  border: `1px solid ${enabled ? 'var(--accent, #6366f1)' : 'var(--border)'}`,
                  background: enabled ? 'rgba(99,102,241,0.06)' : 'transparent',
                  display: 'flex', alignItems: 'center', gap: 10 }}>
                  <input
                    id="flex-tier-toggle"
                    type="checkbox"
                    checked={enabled}
                    onChange={e => setProviderParamsJson(toggleFlex(form.adapter, e.target.checked, providerParamsJson))}
                    style={{ width: 16, height: 16, cursor: 'pointer', flexShrink: 0 }}
                  />
                  <label htmlFor="flex-tier-toggle" style={{ cursor: 'pointer', flex: 1 }}>
                    <span style={{ fontSize: 13, fontWeight: 600 }}>Flex tier</span>
                    <span style={{ fontSize: 11, color: 'var(--muted)', marginLeft: 8 }}>{flexCfg.note}</span>
                  </label>
                </div>
              )
            })()}

            {/* ── Use max_completion_tokens (o-series / GPT-5+) ── */}
            {(form.adapter === 'openai') && (
              <div style={{ marginTop: 10, padding: '10px 14px', borderRadius: 6,
                border: `1px solid ${form.use_completion_tokens ? 'var(--accent, #6366f1)' : 'var(--border)'}`,
                background: form.use_completion_tokens ? 'rgba(99,102,241,0.06)' : 'transparent',
                display: 'flex', alignItems: 'center', gap: 10 }}>
                <input
                  id="use-completion-tokens-toggle"
                  type="checkbox"
                  checked={!!form.use_completion_tokens}
                  onChange={e => setForm(f => ({ ...f, use_completion_tokens: e.target.checked }))}
                  style={{ width: 16, height: 16, cursor: 'pointer', flexShrink: 0 }}
                />
                <label htmlFor="use-completion-tokens-toggle" style={{ cursor: 'pointer', flex: 1 }}>
                  <span style={{ fontSize: 13, fontWeight: 600 }}>Use max_completion_tokens</span>
                  <span style={{ fontSize: 11, color: 'var(--muted)', marginLeft: 8 }}>
                    Required for o-series (o1, o3, o4-mini) and GPT-5+ models that reject the older max_tokens field
                  </span>
                </label>
              </div>
            )}

            <div style={{ marginTop: 12 }}>
              <Field
                label="Additional Provider Params (JSON)"
                hint='Any extra provider-specific fields merged into the request body — e.g. {"thinking":{"type":"enabled","budget_tokens":5000}} for Anthropic extended thinking'
              >
                <textarea
                  className="input"
                  value={providerParamsJson}
                  placeholder={'{\n  "thinking": { "type": "enabled", "budget_tokens": 5000 }\n}'}
                  onChange={e => setProviderParamsJson(e.target.value)}
                  rows={4}
                  style={{ fontFamily: 'monospace', fontSize: 12, resize: 'vertical' }}
                />
              </Field>
            </div>

            <div style={{ marginTop: 12 }}>
              <Field
                label="Full Endpoint URL (override)"
                hint="Bypasses all adapter URL construction — the request is POSTed to this exact URL. Use for models like Gemma where the model ID is part of the URL path, or for any provider with a non-standard endpoint."
              >
                <input className="input" value={form.endpoint_override ?? ''}
                  placeholder="https://generativelanguage.googleapis.com/v1beta/models/gemma-4-31b-it:generateContent"
                  onChange={e => setForm(f => ({ ...f, endpoint_override: e.target.value || undefined }))} />
              </Field>
            </div>

            <div style={{ marginTop: 12 }}>
              <Field
                label="Extra Request Headers (JSON)"
                hint='Additional HTTP headers sent with every request — merged after the auth header. Example: {"x-org-id": "my-org", "x-custom-flag": "1"}'
              >
                <textarea
                  className="input"
                  value={extraHeadersJson}
                  placeholder={'{\n  "x-org-id": "my-org"\n}'}
                  onChange={e => setExtraHeadersJson(e.target.value)}
                  rows={3}
                  style={{ fontFamily: 'monospace', fontSize: 12, resize: 'vertical' }}
                />
              </Field>
            </div>

            <div style={{ marginTop: 16, display: 'flex', gap: 10, alignItems: 'center' }}>
              <button className="btn" style={{ width: 'auto', padding: '0 24px' }}
                onClick={handleSave} disabled={saving}>
                {saving ? 'Saving…' : 'Save Model'}
              </button>
              {saveMsg && (
                <span className={saveMsgErr ? 'status-err' : 'status-ok'}>{saveMsg}</span>
              )}
            </div>
          </div>
        </div>
      )}

      {/* ── Models table ── */}
      {loading ? (
        <p className="hint">Loading…</p>
      ) : models.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 0', color: 'var(--muted)' }}>
          <div style={{ fontSize: 32, opacity: 0.3, marginBottom: 12 }}>⬡</div>
          <p>No models registered. Add one above or use a preset.</p>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {models.map(m => {
            const meta = ADAPTER_META[m.adapter] ?? { label: m.adapter, color: '#64748b' }
            return (
              <div key={m.alias} className="step" style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '10px 14px' }}>
                {/* adapter badge */}
                <span style={{
                  fontSize: 11, fontWeight: 700, padding: '2px 8px', borderRadius: 4,
                  color: '#fff', background: meta.color, flexShrink: 0,
                }}>
                  {meta.label}
                </span>

                {/* alias + model id */}
                <span style={{ fontFamily: 'monospace', fontSize: 14, fontWeight: 600, minWidth: 160 }}>
                  {m.alias}
                </span>
                {m.model_id && m.model_id !== m.alias && (
                  <span style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--muted)',
                    background: 'var(--bg)', padding: '1px 6px', borderRadius: 3 }}
                    title="Provider model ID (sent on the wire)">
                    → {m.model_id}
                  </span>
                )}

                {/* provider */}
                <span style={{ color: 'var(--muted)', fontSize: 12, flex: 1 }}>{m.provider}</span>

                {/* context */}
                {m.capabilities.max_context_tokens ? (
                  <span style={{ fontSize: 11, color: 'var(--muted)', fontFamily: 'monospace' }}>
                    {fmtK(m.capabilities.max_context_tokens)}ctx
                  </span>
                ) : null}

                {/* max tokens */}
                {m.max_tokens ? (
                  <span style={{ fontSize: 11, color: 'var(--muted)', fontFamily: 'monospace' }}>
                    {fmtK(m.max_tokens)}out
                  </span>
                ) : null}

                {/* cost */}
                {(m.cost_per_input_token || m.cost_per_output_token) ? (
                  <span style={{ fontSize: 11, color: 'var(--muted)', fontFamily: 'monospace' }} title="Cost per 1M tokens: input / output">
                    ${m.cost_per_input_token ?? 0}/${m.cost_per_output_token ?? 0}
                  </span>
                ) : null}

                {/* Gemini API version badge */}
                {m.adapter === 'gemini' && (
                  <span style={{ fontSize: 10, fontFamily: 'monospace', border: '1px solid var(--border)', borderRadius: 3, padding: '1px 4px', color: 'var(--muted)' }}
                    title="Google Generative Language API version">
                    {m.api_version ?? 'v1beta'}
                  </span>
                )}

                {/* completion tokens badge */}
                {m.use_completion_tokens && (
                  <span style={{ fontSize: 10, color: 'var(--accent)', fontFamily: 'monospace', border: '1px solid var(--accent)', borderRadius: 3, padding: '1px 4px' }} title="Uses max_completion_tokens">cmplt</span>
                )}

                {/* api key indicator */}
                {m.api_key_ref ? (
                  <span style={{ fontSize: 11, color: '#22c55e' }} title={m.api_key_ref}>🔑</span>
                ) : (
                  <span style={{ fontSize: 11, color: '#ef4444' }} title="No API key set">⚠</span>
                )}

                {/* edit button */}
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '3px 12px', marginTop: 0, fontSize: 12 }}
                  onClick={() => { setForm({ ...m }); setProviderParamsJson(m.provider_params ? JSON.stringify(m.provider_params, null, 2) : ''); setExtraHeadersJson(m.extra_headers ? JSON.stringify(m.extra_headers, null, 2) : ''); setShowForm(true) }}
                >
                  Edit
                </button>

                {/* test button + inline error */}
                {(() => {
                  const ts = testStatus[m.alias]
                  const running = ts?.state === 'running'
                  const ok = ts?.state === 'ok'
                  const err = ts?.state === 'err'
                  return (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexShrink: 0 }}>
                      <button
                        className="btn muted"
                        style={{ width: 'auto', padding: '3px 12px', marginTop: 0, fontSize: 12,
                          color: ok ? '#22c55e' : err ? '#ef4444' : undefined,
                          cursor: running ? 'default' : 'pointer', flexShrink: 0 }}
                        disabled={running}
                        onClick={() => handleQuickTest(m.alias)}
                      >
                        {running ? '⏳' : ok ? `✓ ${ts!.msg}` : err ? '✗ retry' : 'Test'}
                      </button>
                      {err && ts?.fullErr && (
                        <span style={{ fontSize: 11, color: '#ef4444', maxWidth: 260,
                          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                          title={ts.fullErr}>
                          {ts.msg}
                        </span>
                      )}
                    </div>
                  )
                })()}

                {/* delete button */}
                <button
                  className="btn muted"
                  style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 12, color: '#ef4444' }}
                  onClick={() => handleDelete(m.alias)}
                >
                  ✕
                </button>
              </div>
            )
          })}
        </div>
      )}
      <SpendCaps />
    </div>
  )
}

// ── SpendCaps ────────────────────────────────────────────────────────

function SpendCaps() {
  const [quotas, setQuotas]       = useState<TenantQuota[]>([])
  const [loading, setLoading]     = useState(true)
  const [err, setErr]             = useState('')
  const [expanded, setExpanded]   = useState(false)
  const [editItem, setEditItem]   = useState<TenantQuota | null>(null)
  const [saving, setSaving]       = useState(false)
  const [saveMsg, setSaveMsg]     = useState('')
  const blankQuota = (): TenantQuota => ({ tenant_id: '', daily_cost_limit: undefined, monthly_cost_limit: undefined })

  async function load() {
    setLoading(true); setErr('')
    try { setQuotas(await listQuotas()) }
    catch (e) { setErr(e instanceof Error ? e.message : 'Failed to load quotas') }
    finally { setLoading(false) }
  }

  useEffect(() => { void load() }, [])

  async function handleSave() {
    if (!editItem || !editItem.tenant_id.trim()) { setSaveMsg('Tenant ID required'); return }
    setSaving(true); setSaveMsg('')
    try {
      await upsertQuota(editItem)
      await load()
      setEditItem(null)
      setSaveMsg('Saved')
    } catch (e) {
      setSaveMsg(e instanceof Error ? e.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(tid: string) {
    if (!confirm(`Remove spend cap for tenant "${tid}"?`)) return
    try { await deleteQuota(tid); await load() }
    catch (e) { alert(e instanceof Error ? e.message : 'Delete failed') }
  }

  return (
    <div style={{ marginTop: 28, border: '1px solid var(--border)', borderRadius: 10, overflow: 'hidden' }}>
      <div
        style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '11px 16px', borderBottom: expanded ? '1px solid var(--border)' : 'none',
          cursor: 'pointer', fontWeight: 600, fontSize: 13 }}
        onClick={() => setExpanded(e => !e)}
      >
        <span>Spend Caps</span>
        <span style={{ fontSize: 11, color: 'var(--muted)' }}>{expanded ? '▲' : '▼'} {quotas.length} tenant{quotas.length !== 1 ? 's' : ''} configured</span>
      </div>
      {expanded && (
        <div style={{ padding: 16 }}>
          <p style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 14 }}>
            Set daily and monthly USD cost limits per tenant. Requests that would exceed the cap
            are rejected with 429 before reaching any LLM provider.
          </p>
          {loading && <p className="hint">Loading…</p>}
          {err && <p className="status-err">{err}</p>}

          {/* Table */}
          {!loading && quotas.length > 0 && (
            <div style={{ marginBottom: 16 }}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 120px 140px 80px', gap: 6,
                fontSize: 11, color: 'var(--muted)', fontWeight: 600, marginBottom: 6, paddingLeft: 4 }}>
                <span>Tenant ID</span>
                <span>Daily cap ($)</span>
                <span>Monthly cap ($)</span>
                <span></span>
              </div>
              {quotas.map(q => (
                <div key={q.tenant_id} style={{ display: 'grid', gridTemplateColumns: '1fr 120px 140px 80px', gap: 6,
                  alignItems: 'center', padding: '6px 8px', background: 'var(--block-bg)',
                  border: '1px solid var(--border)', borderRadius: 6, marginBottom: 4, fontSize: 13 }}>
                  <span style={{ fontFamily: 'monospace' }}>{q.tenant_id}</span>
                  <span style={{ fontFamily: 'monospace', color: q.daily_cost_limit ? 'var(--text)' : 'var(--muted)' }}>
                    {q.daily_cost_limit != null ? `$${q.daily_cost_limit}` : '—'}
                  </span>
                  <span style={{ fontFamily: 'monospace', color: q.monthly_cost_limit ? 'var(--text)' : 'var(--muted)' }}>
                    {q.monthly_cost_limit != null ? `$${q.monthly_cost_limit}` : '—'}
                  </span>
                  <div style={{ display: 'flex', gap: 6 }}>
                    <button className="btn muted" style={{ padding: '2px 10px', fontSize: 12 }}
                      onClick={() => setEditItem({ ...q })}>Edit</button>
                    <button className="btn muted" style={{ padding: '2px 8px', fontSize: 12, color: '#ef4444' }}
                      onClick={() => handleDelete(q.tenant_id)}>✕</button>
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Add / Edit form */}
          {editItem ? (
            <div style={{ background: 'var(--panel)', border: '1px solid var(--border)', borderRadius: 8, padding: 16 }}>
              <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 12 }}>
                {editItem.tenant_id && quotas.some(q => q.tenant_id === editItem.tenant_id) ? 'Edit Quota' : 'Add Quota'}
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12 }}>
                <div>
                  <label className="field-label">Tenant ID</label>
                  <input className="input" value={editItem.tenant_id}
                    placeholder="e.g. customer-42"
                    onChange={e => setEditItem(q => q ? { ...q, tenant_id: e.target.value } : q)} />
                </div>
                <div>
                  <label className="field-label">Daily Cost Limit (USD)</label>
                  <input className="input" type="number" min={0} step={0.01}
                    value={editItem.daily_cost_limit ?? ''}
                    placeholder="e.g. 5.00"
                    onChange={e => setEditItem(q => q ? { ...q, daily_cost_limit: e.target.value === '' ? undefined : parseFloat(e.target.value) } : q)} />
                </div>
                <div>
                  <label className="field-label">Monthly Cost Limit (USD)</label>
                  <input className="input" type="number" min={0} step={0.01}
                    value={editItem.monthly_cost_limit ?? ''}
                    placeholder="e.g. 50.00"
                    onChange={e => setEditItem(q => q ? { ...q, monthly_cost_limit: e.target.value === '' ? undefined : parseFloat(e.target.value) } : q)} />
                </div>
              </div>
              <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginTop: 14 }}>
                <button className="btn" style={{ width: 'auto', padding: '0 20px' }}
                  onClick={handleSave} disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
                <button className="btn muted" style={{ width: 'auto' }}
                  onClick={() => { setEditItem(null); setSaveMsg('') }}>Cancel</button>
                {saveMsg && <span className="status-err">{saveMsg}</span>}
              </div>
            </div>
          ) : (
            <button className="btn muted" style={{ width: 'auto', padding: '0 20px', fontSize: 13 }}
              onClick={() => setEditItem(blankQuota())}>+ Add Spend Cap</button>
          )}
        </div>
      )}
    </div>
  )
}

// ── helpers ─────────────────────────────────────────────────────────

function fmtK(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`
  return String(n)
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="field-label">{label}</label>
      {children}
      {hint && <span className="field-desc">{hint}</span>}
    </div>
  )
}
