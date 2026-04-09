import { useEffect, useState } from 'react'
import { listLLMModels, upsertLLMModel, deleteLLMModel } from '../api'
import type { LLMModel, LLMAdapter, ModelCapabilities } from '../types'

// ── Adapter metadata ────────────────────────────────────────────────

const ADAPTER_META: Record<LLMAdapter, { label: string; color: string }> = {
  anthropic: { label: 'Anthropic', color: '#a78bfa' },
  openai:    { label: 'OpenAI',    color: '#22c55e' },
  gemini:    { label: 'Gemini',    color: '#3b82f6' },
  ollama:    { label: 'Ollama',    color: '#f97316' },
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
    setSaving(true); setSaveMsg(''); setSaveMsgErr(false)
    try {
      await upsertLLMModel(form)
      setSaveMsg(`Model "${form.alias}" saved.`)
      setSaveMsgErr(false)
      setForm(blankModel())
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
              <Field label="Adapter *" hint="Wire format">
                <select className="input" value={form.adapter}
                  onChange={e => setField('adapter', e.target.value as LLMAdapter)}>
                  <option value="openai">openai</option>
                  <option value="anthropic">anthropic</option>
                  <option value="gemini">gemini</option>
                  <option value="ollama">ollama</option>
                </select>
              </Field>
              <Field label="API Key Ref" hint="Credential name or secret ref">
                <input className="input" value={form.api_key_ref ?? ''} placeholder="e.g. llm:openai or gsm://..."
                  onChange={e => setField('api_key_ref', e.target.value)} />
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
                  onClick={() => { setForm({ ...m }); setShowForm(true) }}
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
