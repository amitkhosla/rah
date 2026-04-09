import { useState, useEffect, useCallback } from 'react'
import type { LLMModel, MCPServer, VirtualMCPServer, FlowStep } from '../types'
import { listLLMModels, listMCPServers, listVirtualMCPServers } from '../api'

type MCPMode = 'virtual' | 'external'

interface AgentConfig {
  name: string
  description: string
  model: string
  systemPrompt: string
  maxTokens: number
  mcpMode: MCPMode
  virtualServer: string
  externalServer: string
  maxConcurrent: number
  timeoutMs: number
  skipUnavailable: boolean
}

const DEFAULT_CONFIG: AgentConfig = {
  name: '',
  description: '',
  model: '',
  systemPrompt: '',
  maxTokens: 4096,
  mcpMode: 'virtual',
  virtualServer: '',
  externalServer: '',
  maxConcurrent: 0,
  timeoutMs: 10000,
  skipUnavailable: false,
}

function SectionLabel({ label }: { label: string }) {
  return (
    <div style={{
      fontSize: 10,
      fontWeight: 700,
      textTransform: 'uppercase',
      letterSpacing: '0.07em',
      color: 'var(--muted)',
      padding: '4px 0 2px',
      borderTop: '1px solid var(--border)',
      marginTop: 16,
      marginBottom: 10,
    }}>
      {label}
    </div>
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
      <label className="field-label">{label}</label>
      {children}
      {desc && <span className="field-desc">{desc}</span>}
    </div>
  )
}

function generateSteps(cfg: AgentConfig): FlowStep[] {
  const serverAlias = cfg.mcpMode === 'virtual' ? cfg.virtualServer : cfg.externalServer
  return [
    { action: 'load_llm_key', model: cfg.model },
    { action: 'sanitize_prompt' },
    { action: 'check_context_fit', model: cfg.model },
    {
      action: 'llm_call',
      model: cfg.model,
      system_prompt: cfg.systemPrompt,
      max_tokens: String(cfg.maxTokens),
    },
    { action: 'parse_tool_calls' },
    {
      action: 'execute_plan',
      mcp_server: serverAlias,
      max_concurrent: String(cfg.maxConcurrent),
      timeout_ms: String(cfg.timeoutMs),
      skip_new_tool_required: String(cfg.skipUnavailable),
    },
    { action: 'format_response' },
  ]
}

const STEP_COLORS: Record<string, string> = {
  load_llm_key: '#57b5ff',
  sanitize_prompt: '#4caf50',
  check_context_fit: '#ff9800',
  llm_call: '#ab47bc',
  parse_tool_calls: '#26c6da',
  execute_plan: '#ef5350',
  format_response: '#66bb6a',
}

function StepPreviewCard({ step, index }: { step: FlowStep; index: number }) {
  const { action, ...params } = step
  const paramEntries = Object.entries(params).filter(([, v]) => v !== '' && v !== 'undefined')
  const color = STEP_COLORS[action] ?? 'var(--accent)'

  return (
    <div
      className="step"
      style={{
        marginBottom: 6,
        borderLeft: `3px solid ${color}`,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span
          style={{
            minWidth: 20,
            height: 20,
            borderRadius: '50%',
            background: color,
            color: '#031427',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: 10,
            fontWeight: 800,
            flexShrink: 0,
          }}
        >
          {index + 1}
        </span>
        <span
          style={{
            fontFamily: 'JetBrains Mono, Fira Code, monospace',
            fontSize: 12,
            color,
          }}
        >
          {action}
        </span>
      </div>
      {paramEntries.length > 0 && (
        <div
          style={{
            marginTop: 5,
            paddingLeft: 28,
            display: 'flex',
            flexDirection: 'column',
            gap: 2,
          }}
        >
          {paramEntries.map(([k, v]) => (
            <span key={k} style={{ fontSize: 10 }}>
              <span style={{ color: 'var(--muted)' }}>{k}: </span>
              <span style={{ color: 'var(--text)' }}>
                {String(v).length > 48 ? String(v).slice(0, 48) + '\u2026' : String(v)}
              </span>
            </span>
          ))}
        </div>
      )}
    </div>
  )
}

export default function AgentBuilder() {
  const [cfg, setCfg] = useState<AgentConfig>(DEFAULT_CONFIG)

  const [models, setModels] = useState<LLMModel[]>([])
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([])
  const [virtualServers, setVirtualServers] = useState<VirtualMCPServer[]>([])
  const [loadErr, setLoadErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [generated, setGenerated] = useState<FlowStep[] | null>(null)
  const [jsonOpen, setJsonOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const [buildErr, setBuildErr] = useState<string | null>(null)

  useEffect(() => {
    setLoading(true)
    Promise.allSettled([
      listLLMModels(),
      listMCPServers(),
      listVirtualMCPServers(),
    ]).then(([mRes, extRes, vRes]) => {
      const errs: string[] = []

      if (mRes.status === 'fulfilled') {
        setModels(mRes.value)
        if (mRes.value.length > 0) {
          setCfg(prev => ({
            ...prev,
            model: prev.model || mRes.value[0].alias,
          }))
        }
      } else {
        errs.push('models: ' + (mRes.reason as Error).message)
      }

      if (extRes.status === 'fulfilled') {
        setMcpServers(extRes.value)
        if (extRes.value.length > 0) {
          setCfg(prev => ({
            ...prev,
            externalServer: prev.externalServer || extRes.value[0].alias,
          }))
        }
      } else {
        errs.push('MCP servers: ' + (extRes.reason as Error).message)
      }

      if (vRes.status === 'fulfilled') {
        setVirtualServers(vRes.value)
        if (vRes.value.length > 0) {
          setCfg(prev => ({
            ...prev,
            virtualServer: prev.virtualServer || vRes.value[0].name,
          }))
        }
      } else {
        errs.push('virtual servers: ' + (vRes.reason as Error).message)
      }

      if (errs.length > 0) setLoadErr('Failed to load: ' + errs.join('; '))
      setLoading(false)
    })
  }, [])

  const set = useCallback(<K extends keyof AgentConfig>(key: K, value: AgentConfig[K]) => {
    setCfg(prev => ({ ...prev, [key]: value }))
    setGenerated(null)
    setBuildErr(null)
  }, [])

  const selectedServerAlias =
    cfg.mcpMode === 'virtual' ? cfg.virtualServer : cfg.externalServer

  const previewSteps = generateSteps(cfg)

  function handleBuild() {
    setBuildErr(null)
    if (!cfg.name.trim()) {
      setBuildErr('Agent name is required.')
      return
    }
    if (!cfg.model) {
      setBuildErr('Select a model.')
      return
    }
    if (!selectedServerAlias) {
      setBuildErr('Select an MCP server.')
      return
    }
    setGenerated(generateSteps(cfg))
    setJsonOpen(false)
    setCopied(false)
  }

  function handleCopyJson() {
    if (!generated) return
    const payload = { name: cfg.name.trim(), steps: generated }
    navigator.clipboard.writeText(JSON.stringify(payload, null, 2)).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  const stepsForPreview = generated ?? previewSteps

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '1fr 360px',
        gap: 16,
        alignItems: 'start',
      }}
    >
      {/* ── LEFT: Form ───────────────────────────────────────────── */}
      <div className="panel">
        <div className="panel-header" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span>Agent Builder</span>
          {loading && (
            <span style={{ fontSize: 11, color: 'var(--muted)' }}>loading\u2026</span>
          )}
        </div>
        <div className="panel-body">

          {loadErr && (
            <div
              style={{
                marginBottom: 12,
                padding: '8px 10px',
                background: 'rgba(255,112,67,0.1)',
                border: '1px solid rgba(255,112,67,0.35)',
                borderRadius: 8,
                fontSize: 12,
                color: '#ff7043',
              }}
            >
              {loadErr}
            </div>
          )}

          {/* IDENTITY */}
          <SectionLabel label="Identity" />

          <FieldRow label="Agent Name" desc="Becomes the flow name when deployed">
            <input
              className="input"
              placeholder="e.g. research-agent"
              value={cfg.name}
              onChange={e => set('name', e.target.value)}
            />
          </FieldRow>

          <FieldRow label="Description" desc="Optional — for your reference only">
            <input
              className="input"
              placeholder="What does this agent do?"
              value={cfg.description}
              onChange={e => set('description', e.target.value)}
            />
          </FieldRow>

          {/* MODEL */}
          <SectionLabel label="Model" />

          <FieldRow label="Model" desc="LLM used for reasoning and tool selection">
            <select
              className="input"
              value={cfg.model}
              onChange={e => set('model', e.target.value)}
              disabled={loading}
            >
              {models.length === 0 && (
                <option value="">— no models available —</option>
              )}
              {models.map(m => (
                <option key={m.alias} value={m.alias}>
                  {m.alias} ({m.provider})
                </option>
              ))}
            </select>
          </FieldRow>

          <FieldRow
            label="System Prompt"
            desc="Instructions for the agent's behavior, persona, and constraints"
          >
            <textarea
              className="input"
              placeholder="You are a helpful research assistant. When given a question, use available tools to find accurate, up-to-date information and summarize the results clearly."
              value={cfg.systemPrompt}
              onChange={e => set('systemPrompt', e.target.value)}
              style={{ minHeight: 120 }}
            />
          </FieldRow>

          <FieldRow
            label="Max Output Tokens"
            desc="Maximum tokens the LLM may produce per turn"
          >
            <input
              className="input"
              type="number"
              min={1}
              max={131072}
              value={cfg.maxTokens}
              onChange={e => set('maxTokens', Number(e.target.value))}
            />
          </FieldRow>

          {/* TOOLS */}
          <SectionLabel label="Tools" />

          <div className="field-row">
            <label className="field-label">MCP Server Mode</label>
            <div style={{ display: 'flex', gap: 16, marginTop: 6 }}>
              {(
                [
                  { id: 'virtual' as MCPMode, label: 'Use Virtual MCP Server' },
                  { id: 'external' as MCPMode, label: 'Use External MCP Server directly' },
                ] as const
              ).map(opt => (
                <label
                  key={opt.id}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 6,
                    cursor: 'pointer',
                    fontSize: 13,
                  }}
                >
                  <input
                    type="radio"
                    name="mcpMode"
                    value={opt.id}
                    checked={cfg.mcpMode === opt.id}
                    onChange={() => set('mcpMode', opt.id)}
                    style={{ accentColor: 'var(--accent)' }}
                  />
                  {opt.label}
                </label>
              ))}
            </div>
          </div>

          {cfg.mcpMode === 'virtual' ? (
            <FieldRow
              label="Virtual MCP Server"
              desc="Composed tool set that aggregates sources from multiple MCP servers and API tools"
            >
              <select
                className="input"
                value={cfg.virtualServer}
                onChange={e => set('virtualServer', e.target.value)}
                disabled={loading}
              >
                {virtualServers.length === 0 && (
                  <option value="">— no virtual servers configured —</option>
                )}
                {virtualServers.map(v => (
                  <option key={v.name} value={v.name}>
                    {v.name}
                    {v.description ? ` \u2014 ${v.description}` : ''}
                  </option>
                ))}
              </select>
            </FieldRow>
          ) : (
            <FieldRow
              label="External MCP Server"
              desc="Directly connected MCP server via HTTP, SSE, or stdio transport"
            >
              <select
                className="input"
                value={cfg.externalServer}
                onChange={e => set('externalServer', e.target.value)}
                disabled={loading}
              >
                {mcpServers.length === 0 && (
                  <option value="">— no external servers configured —</option>
                )}
                {mcpServers.map(s => (
                  <option key={s.alias} value={s.alias}>
                    {s.alias} ({s.transport})
                  </option>
                ))}
              </select>
            </FieldRow>
          )}

          {selectedServerAlias && (
            <div
              style={{
                padding: '7px 10px',
                background: 'rgba(87,181,255,0.07)',
                border: '1px solid rgba(87,181,255,0.22)',
                borderRadius: 7,
                fontSize: 12,
                color: 'var(--muted)',
                marginBottom: 4,
              }}
            >
              Agent will dispatch tool calls to{' '}
              <span
                style={{
                  color: 'var(--accent)',
                  fontFamily: 'JetBrains Mono, Fira Code, monospace',
                }}
              >
                {selectedServerAlias}
              </span>
            </div>
          )}

          {/* EXECUTION CONFIG */}
          <SectionLabel label="Execution Config" />

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
            <FieldRow
              label="Max Concurrent Tool Calls"
              desc="0 = sequential; >0 = parallel up to N calls"
            >
              <input
                className="input"
                type="number"
                min={0}
                value={cfg.maxConcurrent}
                onChange={e => set('maxConcurrent', Number(e.target.value))}
              />
            </FieldRow>

            <FieldRow
              label="Timeout Per Tool Call (ms)"
              desc="Abort a tool call if it exceeds this duration"
            >
              <input
                className="input"
                type="number"
                min={100}
                value={cfg.timeoutMs}
                onChange={e => set('timeoutMs', Number(e.target.value))}
              />
            </FieldRow>
          </div>

          <div className="field-row">
            <label
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                cursor: 'pointer',
                fontSize: 13,
              }}
            >
              <input
                type="checkbox"
                checked={cfg.skipUnavailable}
                onChange={e => set('skipUnavailable', e.target.checked)}
                style={{ accentColor: 'var(--accent)', width: 14, height: 14 }}
              />
              <span>Skip unavailable tools</span>
            </label>
            <span className="field-desc" style={{ marginLeft: 22 }}>
              Continue execution when a required tool is not registered, instead of failing the request
            </span>
          </div>

          {/* GENERATE */}
          <SectionLabel label="Generate" />

          {buildErr && (
            <div className="status-err" style={{ marginBottom: 10 }}>{buildErr}</div>
          )}

          <button className="btn" onClick={handleBuild}>
            Build Agent Flow
          </button>

          {generated && (
            <div style={{ marginTop: 14 }}>
              <div className="status-ok" style={{ marginBottom: 10 }}>
                Flow generated — {generated.length} instructions ready
              </div>

              <button
                className="btn muted"
                style={{ marginBottom: 6 }}
                onClick={() => setJsonOpen(o => !o)}
              >
                {jsonOpen ? 'Hide JSON' : 'View JSON'}
              </button>

              {jsonOpen && (
                <pre
                  style={{
                    background: 'var(--input-bg)',
                    border: '1px solid var(--border-hi)',
                    borderRadius: 8,
                    padding: 10,
                    fontSize: 11,
                    fontFamily: 'JetBrains Mono, Fira Code, monospace',
                    overflowX: 'auto',
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-all',
                    color: 'var(--text)',
                    marginBottom: 6,
                    maxHeight: 360,
                    overflowY: 'auto',
                  }}
                >
                  {JSON.stringify({ name: cfg.name.trim(), steps: generated }, null, 2)}
                </pre>
              )}

              <button className="btn muted" onClick={handleCopyJson}>
                {copied ? 'Copied!' : 'Copy JSON'}
              </button>
            </div>
          )}
        </div>
      </div>

      {/* ── RIGHT: Live Preview ──────────────────────────────────── */}
      <div style={{ position: 'sticky', top: 70 }}>
        <div className="panel">
          <div className="panel-header" style={{ display: 'flex', alignItems: 'center' }}>
            <span style={{ flex: 1 }}>Flow Preview</span>
            {generated ? (
              <span style={{ fontSize: 10, color: '#4caf50', fontWeight: 700, letterSpacing: '0.05em' }}>
                BUILT
              </span>
            ) : (
              <span style={{ fontSize: 10, color: 'var(--muted)', fontWeight: 400, letterSpacing: '0.05em' }}>
                LIVE
              </span>
            )}
          </div>
          <div className="panel-body">

            {/* Agent identity pill */}
            {(cfg.name || cfg.model || selectedServerAlias) && (
              <div
                style={{
                  marginBottom: 12,
                  padding: '8px 10px',
                  background: 'var(--step-bg)',
                  border: '1px solid var(--border-hi)',
                  borderRadius: 8,
                }}
              >
                {cfg.name && (
                  <div style={{ fontWeight: 700, fontSize: 13, color: 'var(--accent)', marginBottom: 4 }}>
                    {cfg.name}
                  </div>
                )}
                {cfg.description && (
                  <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 6 }}>
                    {cfg.description}
                  </div>
                )}
                <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                  {cfg.model && (
                    <span
                      style={{
                        fontSize: 10,
                        background: 'rgba(87,181,255,0.12)',
                        border: '1px solid rgba(87,181,255,0.3)',
                        borderRadius: 5,
                        padding: '1px 7px',
                        color: 'var(--accent)',
                        fontFamily: 'JetBrains Mono, monospace',
                      }}
                    >
                      {cfg.model}
                    </span>
                  )}
                  {selectedServerAlias && (
                    <span
                      style={{
                        fontSize: 10,
                        background: 'rgba(76,175,80,0.12)',
                        border: '1px solid rgba(76,175,80,0.3)',
                        borderRadius: 5,
                        padding: '1px 7px',
                        color: '#4caf50',
                        fontFamily: 'JetBrains Mono, monospace',
                      }}
                    >
                      {selectedServerAlias}
                    </span>
                  )}
                </div>
              </div>
            )}

            {/* Step cards */}
            {stepsForPreview.map((s, i) => (
              <StepPreviewCard key={i} step={s} index={i} />
            ))}

            {/* Execution summary */}
            <div
              style={{
                marginTop: 10,
                padding: '8px 10px',
                background: 'rgba(0,0,0,0.15)',
                border: '1px solid var(--border)',
                borderRadius: 7,
                fontSize: 11,
                color: 'var(--muted)',
                display: 'flex',
                flexDirection: 'column',
                gap: 3,
              }}
            >
              <div>
                <span style={{ color: 'var(--text)' }}>concurrent: </span>
                {cfg.maxConcurrent === 0
                  ? 'sequential'
                  : `up to ${cfg.maxConcurrent} parallel`}
              </div>
              <div>
                <span style={{ color: 'var(--text)' }}>timeout: </span>
                {cfg.timeoutMs.toLocaleString()}ms per tool call
              </div>
              <div>
                <span style={{ color: 'var(--text)' }}>skip unavailable: </span>
                {cfg.skipUnavailable ? 'yes' : 'no'}
              </div>
            </div>

            {!generated && (
              <div className="expansion-note" style={{ marginTop: 10 }}>
                Preview updates as you fill the form. Click "Build Agent Flow" to finalize.
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
