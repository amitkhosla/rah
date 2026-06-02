import { useState } from 'react'
import AIModels       from './AIModels'
import MCPServers     from './MCPServers'
import AIRoutes       from './AIRoutes'
import AgentBuilder   from './AgentBuilder'
import PromptsLibrary from './PromptsLibrary'
import AIAssistant    from './AIAssistant'

type AITab = 'models' | 'mcp' | 'routes' | 'agents' | 'prompts' | 'assistant'

const AI_TABS: { id: AITab; label: string; desc: string }[] = [
  { id: 'models',    label: 'Models',      desc: 'LLM catalog — providers, adapters, context limits' },
  { id: 'mcp',       label: 'MCP Servers', desc: 'External servers, virtual tool sets, API tools' },
  { id: 'routes',    label: 'AI Routes',   desc: 'High-level LLM endpoint builder — no raw instructions needed' },
  { id: 'agents',    label: 'Agents',      desc: 'Visual agent builder — model + tools + execution config' },
  { id: 'prompts',   label: 'Prompts',     desc: 'Saved system prompts library' },
  { id: 'assistant', label: 'Assistant',   desc: 'Generate flows and APIs with AI' },
]

export default function AISection() {
  const [tab, setTab] = useState<AITab>('models')
  const active = AI_TABS.find(t => t.id === tab)!

  return (
    <div>
      {/* ── Section header ── */}
      <div style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 4 }}>
          <span style={{ fontSize: 18, fontWeight: 800, letterSpacing: -0.5 }}>AI Gateway</span>
          <span style={{ fontSize: 12, color: 'var(--muted)', background: 'var(--step-bg)', padding: '2px 10px', borderRadius: 10 }}>
            {active.desc}
          </span>
        </div>

        {/* sub-tab nav */}
        <div style={{ display: 'flex', gap: 0, borderBottom: '2px solid var(--border)', marginTop: 12 }}>
          {AI_TABS.map(t => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              style={{
                padding: '8px 20px',
                border: 'none',
                background: 'transparent',
                cursor: 'pointer',
                fontSize: 13,
                fontWeight: tab === t.id ? 700 : 400,
                color: tab === t.id ? 'var(--accent)' : 'var(--muted)',
                borderBottom: tab === t.id ? '2px solid var(--accent)' : '2px solid transparent',
                marginBottom: -2,
                transition: 'color 0.15s',
              }}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {/* ── Content ── */}
      {tab === 'models'     && <AIModels />}
      {tab === 'mcp'        && <MCPServers />}
      {tab === 'routes'     && <AIRoutes />}
      {tab === 'agents'     && <AgentBuilder />}
      {tab === 'prompts'    && <PromptsLibrary />}
      {tab === 'assistant'  && <AIAssistant />}
    </div>
  )
}
