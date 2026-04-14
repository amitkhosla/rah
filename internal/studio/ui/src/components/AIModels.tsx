import { useEffect, useRef, useState } from 'react'
import { listLLMModels, upsertLLMModel, deleteLLMModel } from '../api'
import type { LLMModel, LLMAdapter, ModelCapabilities } from '../types'

// ── SecretRefBuilder ─────────────────────────────────────────────────
// Popover helper that constructs a secret reference URI without the user
// needing to know the exact format. Covers all supported backends.

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

function SecretRefBuilder({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [open, setOpen]       = useState(false)
  const [backend, setBackend] = useState<SecretBackend>('credential')
  const [parts, setParts]     = useState<Record<string, string>>({})
  const popoverRef            = useRef<HTMLDivElement>(null)

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

  return (
    <div style={{ position: 'relative', display: 'flex', gap: 6 }}>
      <input className="input" value={value} placeholder="e.g. llm:openai  or  gsm://projects/…"
        onChange={e => onChange(e.target.value)}
        style={{ flex: 1 }} />
      <button type="button" className="btn"
        style={{ width: 'auto', padding: '0 10px', fontSize: 12, flexShrink: 0 }}
        onClick={() => setOpen(v => !v)} title="Secret reference builder">
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
          {backend === 'credential' && (
            <RefField label="Credential name" hint='e.g. "llm:openai" — set the value in Tenants → Credentials'
              value={parts.name ?? ''} onChange={v => setPart('name', v)} placeholder="llm:openai" />
          )}
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
      base_url: 'https://api.openai.com/v1', api_key_ref: 'llm:openai',
      max_tokens: 4096,
      capabilities: { max_context_tokens: 128000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'GPT-4o mini',
    model: {
      alias: 'gpt-4o-mini', provider: 'OpenAI', adapter: 'openai',
      base_url: 'https://api.openai.com/v1', api_key_ref: 'llm:openai',
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
      api_key_ref: 'llm:gemini', max_tokens: 8192,
      capabilities: { max_context_tokens: 1000000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'Llama 3 (Ollama)',
    model: {
      alias: 'llama3', provider: 'Meta', adapter: 'ollama',
      base_url: 'http://localhost:11434/v1', max_tokens: 4096,
      capabilities: { max_context_tokens: 8192 },
    },
  },
  {
    label: 'DeepSeek Chat',
    model: {
      alias: 'deepseek-chat', provider: 'DeepSeek', adapter: 'deepseek',
      base_url: 'https://api.deepseek.com', api_key_ref: 'llm:deepseek',
      max_tokens: 4096,
      capabilities: { max_context_tokens: 64000, supported_tool_formats: ['openai-tools'] },
    },
  },
  {
    label: 'DeepSeek R1 (Reasoner)',
    model: {
      alias: 'deepseek-reasoner', provider: 'DeepSeek', adapter: 'deepseek',
      base_url: 'https://api.deepseek.com', api_key_ref: 'llm:deepseek',
      max_tokens: 8192,
      capabilities: { max_context_tokens: 64000 },
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
  const [saving, setSaving]     = useState(false)
  const [saveMsg, setSaveMsg]   = useState('')
  const [saveMsgErr, setSaveMsgErr] = useState(false)

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
    setShowForm(true)
  }

  function setField(key: keyof LLMModel, val: string | number) {
    setForm(f => ({ ...f, [key]: val }))
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
    setSaving(true); setSaveMsg(''); setSaveMsgErr(false)
    try {
      await upsertLLMModel({ ...form, provider_params: providerParams })
      setSaveMsg(`Model "${form.alias}" saved.`)
      setSaveMsgErr(false)
      setForm(blankModel())
      setProviderParamsJson('')
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
              <Field label="Alias *" hint="Stable ID used in flows">
                <input className="input" value={form.alias} placeholder="e.g. gpt-4o"
                  onChange={e => setField('alias', e.target.value)} />
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
                  onChange={v => setField('api_key_ref', v)} />
              </Field>
              <Field label="Base URL" hint="Leave blank to use provider default">
                <input className="input" value={form.base_url ?? ''} placeholder="https://api.openai.com/v1"
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
            </div>

            <div style={{ marginTop: 12 }}>
              <Field
                label="Provider Params (JSON)"
                hint='Provider-specific fields merged into the request body. e.g. {"service_tier":"flex"} for OpenAI, {"thinking":{"type":"enabled","budget_tokens":5000}} for Anthropic'
              >
                <textarea
                  className="input"
                  value={providerParamsJson}
                  placeholder={'{\n  "service_tier": "flex"\n}'}
                  onChange={e => setProviderParamsJson(e.target.value)}
                  rows={4}
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

                {/* alias */}
                <span style={{ fontFamily: 'monospace', fontSize: 14, fontWeight: 600, minWidth: 160 }}>
                  {m.alias}
                </span>

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
                  onClick={() => { setForm({ ...m }); setProviderParamsJson(m.provider_params ? JSON.stringify(m.provider_params, null, 2) : ''); setShowForm(true) }}
                >
                  Edit
                </button>

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
