import { useState, useEffect } from 'react'
import { listLLMModels } from '../api'
import type { FlowStep, LLMModel } from '../types'

// ── Pattern definitions ──────────────────────────────────────────────────────

type PatternId = 'llm_proxy' | 'rag' | 'chat_history' | 'streaming'

interface PatternDef {
  id: PatternId
  title: string
  subtitle: string
  description: string
  icon: string
}

const PATTERNS: PatternDef[] = [
  {
    id: 'llm_proxy',
    title: 'LLM Proxy',
    subtitle: 'Simple AI endpoint',
    description: 'Route requests directly to an LLM. Minimal setup — pick a model, set a system prompt, go.',
    icon: 'Z',
  },
  {
    id: 'rag',
    title: 'RAG Pipeline',
    subtitle: 'Retrieval-augmented generation',
    description: 'Embed the user query, retrieve relevant chunks from a vector store, then call the LLM with enriched context.',
    icon: 'S',
  },
  {
    id: 'chat_history',
    title: 'Chat with History',
    subtitle: 'Conversational AI with memory',
    description: 'Load conversation history per session, call the LLM with context, and persist the updated history.',
    icon: 'C',
  },
  {
    id: 'streaming',
    title: 'Streaming Response',
    subtitle: 'Real-time token streaming via SSE',
    description: 'Stream LLM output token-by-token using Server-Sent Events. Best for chat UIs that need instant feedback.',
    icon: 'P',
  },
]

const PATTERN_ICONS: Record<PatternId, string> = {
  llm_proxy: '&#x26A1;',
  rag: '&#x1F50D;',
  chat_history: '&#x1F4AC;',
  streaming: '&#x1F4E1;',
}

// ── Config field types ───────────────────────────────────────────────────────

interface LLMProxyConfig {
  model: string
  system_prompt: string
  max_tokens: string
}

interface RAGConfig {
  model: string
  system_prompt: string
  vector_store: string
  top_k: string
}

interface ChatHistoryConfig {
  model: string
  system_prompt: string
  max_history_turns: string
  cache_key_field: string
}

interface StreamingConfig {
  model: string
  system_prompt: string
}

type PatternConfig = LLMProxyConfig | RAGConfig | ChatHistoryConfig | StreamingConfig

const DEFAULT_CONFIGS: Record<PatternId, PatternConfig> = {
  llm_proxy: {
    model: '',
    system_prompt: 'You are a helpful assistant.',
    max_tokens: '1024',
  },
  rag: {
    model: '',
    system_prompt: 'You are a helpful assistant. Use the provided context to answer questions.',
    vector_store: '',
    top_k: '5',
  },
  chat_history: {
    model: '',
    system_prompt: 'You are a helpful assistant.',
    max_history_turns: '10',
    cache_key_field: 'session_id',
  },
  streaming: {
    model: '',
    system_prompt: 'You are a helpful assistant.',
  },
}

// ── Step compiler ────────────────────────────────────────────────────────────

function compileSteps(patternId: PatternId, config: PatternConfig): FlowStep[] {
  switch (patternId) {
    case 'llm_proxy': {
      const c = config as LLMProxyConfig
      return [
        { action: 'load_llm_key', model: c.model },
        { action: 'sanitize_prompt' },
        { action: 'check_context_fit', model: c.model },
        {
          action: 'llm_call',
          model: c.model,
          system_prompt: c.system_prompt,
          ...(c.max_tokens ? { max_tokens: c.max_tokens } : {}),
        },
        { action: 'format_response' },
      ]
    }
    case 'rag': {
      const c = config as RAGConfig
      return [
        { action: 'load_llm_key', model: c.model },
        { action: 'embed_text', model: c.model },
        { action: 'vector_search', store: c.vector_store, top_k: c.top_k },
        { action: 'sanitize_prompt' },
        { action: 'check_context_fit', model: c.model },
        { action: 'llm_call', model: c.model, system_prompt: c.system_prompt },
        { action: 'format_response' },
      ]
    }
    case 'chat_history': {
      const c = config as ChatHistoryConfig
      return [
        { action: 'load_llm_key', model: c.model },
        { action: 'load_history', key_slot: c.cache_key_field, max_turns: c.max_history_turns },
        { action: 'check_context_fit', model: c.model },
        { action: 'llm_call', model: c.model, system_prompt: c.system_prompt },
        { action: 'save_history', key_slot: c.cache_key_field },
        { action: 'format_response' },
      ]
    }
    case 'streaming': {
      const c = config as StreamingConfig
      return [
        { action: 'load_llm_key', model: c.model },
        { action: 'sanitize_prompt' },
        { action: 'llm_call', model: c.model, system_prompt: c.system_prompt, stream: 'true' },
        { action: 'send_sse_event' },
      ]
    }
  }
}

// ── Shared sub-components ────────────────────────────────────────────────────

function ModelSelect({
  value,
  onChange,
  models,
  loading,
}: {
  value: string
  onChange: (v: string) => void
  models: LLMModel[]
  loading: boolean
}) {
  return (
    <select className="input" value={value} onChange={e => onChange(e.target.value)}>
      {loading ? (
        <option value="">Loading models...</option>
      ) : models.length === 0 ? (
        <option value="">No models registered</option>
      ) : (
        <>
          <option value="">— select model —</option>
          {models.map(m => (
            <option key={m.alias} value={m.alias}>
              {m.alias} ({m.provider})
            </option>
          ))}
        </>
      )}
    </select>
  )
}

function FieldRow({
  label,
  desc,
  children,
}: {
  label: string
  desc?: string
  children: React.ReactNode
}) {
  return (
    <div className="field-row">
      <span className="field-label">{label}</span>
      {children}
      {desc && <span className="field-desc">{desc}</span>}
    </div>
  )
}

// ── Config forms per pattern ─────────────────────────────────────────────────

function LLMProxyForm({
  config,
  onChange,
  models,
  loadingModels,
}: {
  config: LLMProxyConfig
  onChange: (c: LLMProxyConfig) => void
  models: LLMModel[]
  loadingModels: boolean
}) {
  return (
    <>
      <FieldRow label="Model" desc="The LLM to call for every request.">
        <ModelSelect
          value={config.model}
          onChange={v => onChange({ ...config, model: v })}
          models={models}
          loading={loadingModels}
        />
      </FieldRow>
      <FieldRow label="System Prompt">
        <textarea
          className="input"
          value={config.system_prompt}
          onChange={e => onChange({ ...config, system_prompt: e.target.value })}
          placeholder="You are a helpful assistant."
        />
      </FieldRow>
      <FieldRow label="Max Tokens" desc="Maximum tokens the model may generate per response.">
        <input
          type="number"
          className="input"
          value={config.max_tokens}
          min={1}
          onChange={e => onChange({ ...config, max_tokens: e.target.value })}
          placeholder="1024"
        />
      </FieldRow>
    </>
  )
}

function RAGForm({
  config,
  onChange,
  models,
  loadingModels,
}: {
  config: RAGConfig
  onChange: (c: RAGConfig) => void
  models: LLMModel[]
  loadingModels: boolean
}) {
  return (
    <>
      <FieldRow label="Model">
        <ModelSelect
          value={config.model}
          onChange={v => onChange({ ...config, model: v })}
          models={models}
          loading={loadingModels}
        />
      </FieldRow>
      <FieldRow label="System Prompt">
        <textarea
          className="input"
          value={config.system_prompt}
          onChange={e => onChange({ ...config, system_prompt: e.target.value })}
        />
      </FieldRow>
      <FieldRow label="Vector Store Name" desc="Name of the registered vector store to search.">
        <input
          type="text"
          className="input"
          value={config.vector_store}
          onChange={e => onChange({ ...config, vector_store: e.target.value })}
          placeholder="e.g. my_knowledge_base"
        />
      </FieldRow>
      <FieldRow label="Top K" desc="Number of document chunks to retrieve.">
        <input
          type="number"
          className="input"
          value={config.top_k}
          min={1}
          onChange={e => onChange({ ...config, top_k: e.target.value })}
          placeholder="5"
        />
      </FieldRow>
    </>
  )
}

function ChatHistoryForm({
  config,
  onChange,
  models,
  loadingModels,
}: {
  config: ChatHistoryConfig
  onChange: (c: ChatHistoryConfig) => void
  models: LLMModel[]
  loadingModels: boolean
}) {
  return (
    <>
      <FieldRow label="Model">
        <ModelSelect
          value={config.model}
          onChange={v => onChange({ ...config, model: v })}
          models={models}
          loading={loadingModels}
        />
      </FieldRow>
      <FieldRow label="System Prompt">
        <textarea
          className="input"
          value={config.system_prompt}
          onChange={e => onChange({ ...config, system_prompt: e.target.value })}
        />
      </FieldRow>
      <FieldRow label="Max History Turns" desc="How many previous turns to include in the prompt.">
        <input
          type="number"
          className="input"
          value={config.max_history_turns}
          min={1}
          onChange={e => onChange({ ...config, max_history_turns: e.target.value })}
          placeholder="10"
        />
      </FieldRow>
      <FieldRow
        label="Cache Key Field"
        desc="Request field used as the history cache key (e.g. session_id)."
      >
        <input
          type="text"
          className="input"
          value={config.cache_key_field}
          onChange={e => onChange({ ...config, cache_key_field: e.target.value })}
          placeholder="session_id"
        />
      </FieldRow>
    </>
  )
}

function StreamingForm({
  config,
  onChange,
  models,
  loadingModels,
}: {
  config: StreamingConfig
  onChange: (c: StreamingConfig) => void
  models: LLMModel[]
  loadingModels: boolean
}) {
  return (
    <>
      <FieldRow label="Model">
        <ModelSelect
          value={config.model}
          onChange={v => onChange({ ...config, model: v })}
          models={models}
          loading={loadingModels}
        />
      </FieldRow>
      <FieldRow label="System Prompt" desc="LLM output will stream token-by-token via SSE.">
        <textarea
          className="input"
          value={config.system_prompt}
          onChange={e => onChange({ ...config, system_prompt: e.target.value })}
        />
      </FieldRow>
    </>
  )
}

// ── Step badge in result panel ───────────────────────────────────────────────

function StepBadge({ step }: { step: FlowStep }) {
  const { action, ...params } = step
  const paramParts = Object.entries(params)
    .filter(([, v]) => v !== '')
    .map(([k, v]) => `${k}: ${v}`)
  return (
    <div className="step" style={{ marginBottom: 6 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <span
          style={{
            background: 'rgba(87,181,255,0.15)',
            border: '1px solid rgba(87,181,255,0.35)',
            borderRadius: 5,
            padding: '2px 8px',
            fontSize: 12,
            fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
            color: 'var(--accent)',
            fontWeight: 700,
            flexShrink: 0,
          }}
        >
          {action}
        </span>
        {paramParts.length > 0 && (
          <span
            className="hint"
            style={{ fontSize: 11, whiteSpace: 'normal', lineHeight: 1.5, color: 'var(--muted)' }}
          >
            {paramParts.join('  ·  ')}
          </span>
        )}
      </div>
    </div>
  )
}

// ── Pattern card ─────────────────────────────────────────────────────────────

function PatternCard({
  pattern,
  selected,
  onClick,
}: {
  pattern: PatternDef
  selected: boolean
  onClick: () => void
}) {
  const iconMap: Record<PatternId, string> = {
    llm_proxy: '\u26A1',
    rag: '\uD83D\uDD0D',
    chat_history: '\uD83D\uDCAC',
    streaming: '\uD83D\uDCE1',
  }
  return (
    <div
      onClick={onClick}
      style={{
        background: selected ? 'rgba(87,181,255,0.08)' : 'var(--block-bg)',
        border: selected ? '2px solid var(--accent)' : '1px solid var(--border-hi)',
        borderRadius: 10,
        padding: '14px 16px',
        cursor: 'pointer',
        transition: 'background 0.15s, border-color 0.15s',
        userSelect: 'none',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 6 }}>
        <span style={{ fontSize: 20, lineHeight: 1 }}>{iconMap[pattern.id]}</span>
        <div>
          <div
            style={{
              fontWeight: 700,
              fontSize: 14,
              color: selected ? 'var(--accent)' : 'var(--text)',
            }}
          >
            {pattern.title}
          </div>
          <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 1 }}>{pattern.subtitle}</div>
        </div>
      </div>
      <div style={{ fontSize: 12, color: 'var(--muted)', lineHeight: 1.55 }}>
        {pattern.description}
      </div>
    </div>
  )
}

// ── Main export ──────────────────────────────────────────────────────────────

export default function AIRoutes() {
  const [selectedPattern, setSelectedPattern] = useState<PatternId | null>(null)
  const [configs, setConfigs] = useState<Record<PatternId, PatternConfig>>({
    ...DEFAULT_CONFIGS,
  })
  const [flowName, setFlowName] = useState('')
  const [generatedSteps, setGeneratedSteps] = useState<FlowStep[] | null>(null)
  const [copied, setCopied] = useState(false)

  const [models, setModels] = useState<LLMModel[]>([])
  const [loadingModels, setLoadingModels] = useState(false)
  const [modelError, setModelError] = useState<string | null>(null)

  useEffect(() => {
    setLoadingModels(true)
    listLLMModels()
      .then(data => {
        setModels(data)
        setModelError(null)
      })
      .catch(err => setModelError(String(err)))
      .finally(() => setLoadingModels(false))
  }, [])

  function updateConfig(id: PatternId, next: PatternConfig) {
    setConfigs(prev => ({ ...prev, [id]: next }))
    setGeneratedSteps(null)
  }

  function handleSelectPattern(id: PatternId) {
    setSelectedPattern(id)
    setGeneratedSteps(null)
    setCopied(false)
  }

  function handleGenerate() {
    if (!selectedPattern) return
    const steps = compileSteps(selectedPattern, configs[selectedPattern])
    setGeneratedSteps(steps)
    setCopied(false)
  }

  function handleCopy() {
    if (!generatedSteps) return
    const payload = { name: flowName.trim(), steps: generatedSteps }
    navigator.clipboard.writeText(JSON.stringify(payload, null, 2)).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2200)
    })
  }

  const canGenerate = selectedPattern !== null && flowName.trim().length > 0

  const activePatternDef = selectedPattern
    ? PATTERNS.find(p => p.id === selectedPattern)
    : null

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>

      {/* ── Step 1: Pattern picker ── */}
      <div className="panel">
        <div className="panel-header">Step 1 — Choose a Pattern</div>
        <div className="panel-body">
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            {PATTERNS.map(p => (
              <PatternCard
                key={p.id}
                pattern={p}
                selected={selectedPattern === p.id}
                onClick={() => handleSelectPattern(p.id)}
              />
            ))}
          </div>

          {modelError && (
            <div className="status-err" style={{ marginTop: 10 }}>
              Could not load models: {modelError}
            </div>
          )}
        </div>
      </div>

      {/* ── Step 2: Config form ── */}
      {selectedPattern && (
        <div className="panel">
          <div className="panel-header">
            Step 2 — Configure
            {activePatternDef && (
              <span style={{ fontWeight: 400, color: 'var(--muted)', marginLeft: 6 }}>
                — {activePatternDef.title}
              </span>
            )}
          </div>
          <div className="panel-body">
            {selectedPattern === 'llm_proxy' && (
              <LLMProxyForm
                config={configs.llm_proxy as LLMProxyConfig}
                onChange={c => updateConfig('llm_proxy', c)}
                models={models}
                loadingModels={loadingModels}
              />
            )}
            {selectedPattern === 'rag' && (
              <RAGForm
                config={configs.rag as RAGConfig}
                onChange={c => updateConfig('rag', c)}
                models={models}
                loadingModels={loadingModels}
              />
            )}
            {selectedPattern === 'chat_history' && (
              <ChatHistoryForm
                config={configs.chat_history as ChatHistoryConfig}
                onChange={c => updateConfig('chat_history', c)}
                models={models}
                loadingModels={loadingModels}
              />
            )}
            {selectedPattern === 'streaming' && (
              <StreamingForm
                config={configs.streaming as StreamingConfig}
                onChange={c => updateConfig('streaming', c)}
                models={models}
                loadingModels={loadingModels}
              />
            )}
          </div>
        </div>
      )}

      {/* ── Step 3: Name + Generate ── */}
      {selectedPattern && (
        <div className="panel">
          <div className="panel-header">Step 3 — Name and Generate</div>
          <div className="panel-body">
            <span className="field-label">Flow Name</span>
            <input
              type="text"
              className="input"
              value={flowName}
              onChange={e => {
                setFlowName(e.target.value)
                setGeneratedSteps(null)
              }}
              placeholder="e.g. my-chat-api"
              style={{ marginTop: 4 }}
            />
            <span
              className="field-desc"
              style={{ display: 'block', marginBottom: 12 }}
            >
              This becomes the flow name in the gateway. Use lowercase letters, digits, and hyphens.
            </span>
            <button
              className="btn"
              disabled={!canGenerate}
              onClick={handleGenerate}
              style={{
                opacity: canGenerate ? 1 : 0.4,
                cursor: canGenerate ? 'pointer' : 'not-allowed',
              }}
            >
              Generate Flow
            </button>
          </div>
        </div>
      )}

      {/* ── Result panel ── */}
      {generatedSteps && (
        <div className="panel">
          <div
            className="panel-header"
            style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}
          >
            <span>
              Generated Flow
              {flowName && (
                <span
                  style={{
                    marginLeft: 8,
                    fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
                    fontSize: 12,
                    color: 'var(--accent)',
                  }}
                >
                  {flowName}
                </span>
              )}
            </span>
            <span style={{ fontSize: 11, color: 'var(--muted)', fontWeight: 400 }}>
              {generatedSteps.length} instruction{generatedSteps.length !== 1 ? 's' : ''}
            </span>
          </div>

          <div className="panel-body">
            {/* Step list */}
            <div style={{ marginBottom: 16 }}>
              {generatedSteps.map((step, i) => (
                <div
                  key={i}
                  style={{ display: 'flex', alignItems: 'flex-start', gap: 8, marginBottom: 4 }}
                >
                  <span
                    style={{
                      fontSize: 10,
                      color: 'var(--muted)',
                      minWidth: 18,
                      paddingTop: 6,
                      textAlign: 'right',
                      fontFamily: "'JetBrains Mono', monospace",
                      flexShrink: 0,
                    }}
                  >
                    {i + 1}
                  </span>
                  <div style={{ flex: 1 }}>
                    <StepBadge step={step} />
                  </div>
                </div>
              ))}
            </div>

            {/* JSON preview */}
            <div
              style={{
                background: 'var(--input-bg)',
                border: '1px solid var(--border-hi)',
                borderRadius: 8,
                padding: '10px 12px',
                marginBottom: 10,
                maxHeight: 280,
                overflowY: 'auto',
              }}
            >
              <pre
                className="hint"
                style={{ fontSize: 11, fontFamily: "'JetBrains Mono', 'Fira Code', monospace" }}
              >
                {JSON.stringify({ name: flowName.trim(), steps: generatedSteps }, null, 2)}
              </pre>
            </div>

            {/* Copy button */}
            <button className="btn" onClick={handleCopy} style={{ marginBottom: 8 }}>
              {copied ? 'Copied to clipboard!' : 'Copy JSON'}
            </button>

            {/* Guidance note */}
            <div className="expansion-note">
              Take this JSON to the Flow Designer tab to import and deploy it, or post it directly to
              the /sync API endpoint.
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
