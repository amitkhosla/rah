import { useMemo, useState } from 'react'
import type { SavedFlow } from '../types'
import { buildCallGraph } from './FlowMap'

// ── Step icon map (duplicated from FlowMap for self-containment) ───────
const STEP_ICONS: Record<string, string> = {
  'if': '🔀', 'switch': '🔀', 'call': '📞', 'return': '↩', 'fail': '✗',
  'token_validation': '🔒', 'http_call': '🌐', 'llm_call': '🧠',
  'cache_get': '🗄️', 'cache_get_global': '🗄️', 'cache_put': '🗄️', 'cache_put_global': '🗄️', 'cache_delete': '🗄️', 'cache_delete_global': '🗄️',
  'bind_header': '📥', 'bind_query': '📥', 'bind_path': '📥', 'bind_body': '📥',
  'bind_client_ip': '🌐', 'emit_event': '📊', 'log_field': '📋',
  'registry_lookup': '🏷️', 'load_service_url': '🔗', 'load_identifier': '🔑',
  'check_rate_limit': '⏱', 'api_rate_limits': '📍', 'set_response_body': '📤', 'set_response_header': '📤',
  'set_response_status': '📤', 'extract': '✂️', 'json_extract_emit': '✂️',
  'mcp_call_tool': '🔧', 'vector_search': '🔍', 'embed_text': '🔢',
  'store_internal_tx_id': '🔖', 'bind_correlation_id': '🔖',
}

interface Props {
  savedFlows: SavedFlow[]
  currentFlow: string
  onNavigate: (name: string) => void
  /** If set, only lay out the subgraph reachable from this flow. */
  focusFlow?: string
}

/** Returns all flow names reachable from `start` via the call graph (BFS). */
function getReachableFlows(start: string, graph: Map<string, string[]>, allFlows: SavedFlow[]): SavedFlow[] {
  const visited = new Set<string>()
  const queue = [start]
  while (queue.length > 0) {
    const name = queue.shift()!
    if (visited.has(name)) continue
    visited.add(name)
    for (const c of graph.get(name) ?? []) queue.push(c)
  }
  return allFlows.filter(f => visited.has(f.name))
}

// ── Theme definitions ────────────────────────────────────────────────
interface GraphTheme {
  name: string
  swatch: string   // colour shown in the picker dot
  bg: string
  node: string
  border: string
  active: string
  activeText: string
  text: string
  edge: string
  danger: string
}

const THEMES: Record<string, GraphTheme> = {
  dark: {
    name: 'Dark',      swatch: '#1a2b48',
    bg: '#0b1220',     node: '#1a2b48',    border: '#38568c',
    active: '#57b5ff', activeText: '#ffffff',
    text: '#ecf0f9',   edge: '#93a1bf',    danger: 'rgba(239,68,68,0.30)',
  },
  ocean: {
    name: 'Ocean',     swatch: '#0a3050',
    bg: '#040e18',     node: '#0a3050',    border: '#0d6090',
    active: '#00c8ff', activeText: '#001824',
    text: '#b8dff5',   edge: '#3a7090',    danger: 'rgba(239,68,68,0.30)',
  },
  forest: {
    name: 'Forest',    swatch: '#1a3520',
    bg: '#091409',     node: '#1a3520',    border: '#2d6040',
    active: '#4ade80', activeText: '#071207',
    text: '#c8f0d0',   edge: '#4a8060',    danger: 'rgba(239,68,68,0.30)',
  },
  ember: {
    name: 'Ember',     swatch: '#3a1c0c',
    bg: '#140a04',     node: '#3a1c0c',    border: '#7c3010',
    active: '#f97316', activeText: '#1a0800',
    text: '#fde8d0',   edge: '#905030',    danger: 'rgba(239,68,68,0.30)',
  },
  violet: {
    name: 'Violet',    swatch: '#1f1540',
    bg: '#0d0a1a',     node: '#1f1540',    border: '#4a2580',
    active: '#a855f7', activeText: '#100830',
    text: '#e8d5ff',   edge: '#6a40b0',    danger: 'rgba(239,68,68,0.30)',
  },
  mono: {
    name: 'Mono',      swatch: '#2a2a2a',
    bg: '#0f0f0f',     node: '#2a2a2a',    border: '#555555',
    active: '#e5e5e5', activeText: '#111111',
    text: '#dddddd',   edge: '#777777',    danger: 'rgba(239,68,68,0.30)',
  },
}

const THEME_ORDER = ['dark', 'ocean', 'forest', 'ember', 'violet', 'mono'] as const

const LS_KEY = 'rah_graph_theme'

// ── Layout ───────────────────────────────────────────────────────────
const NODE_W = 140
const NODE_H = 40
const H_GAP  = 70
const V_GAP  = 28

interface LayoutNode { name: string; x: number; y: number }

function layoutGraph(flows: SavedFlow[], graph: Map<string, string[]>): LayoutNode[] {
  const names = flows.map(f => f.name)
  const inDegree = new Map<string, number>()
  names.forEach(n => inDegree.set(n, 0))
  graph.forEach(children => {
    children.forEach(c => { if (names.includes(c)) inDegree.set(c, (inDegree.get(c) ?? 0) + 1) })
  })
  const cols = new Map<string, number>()
  const queue = names.filter(n => (inDegree.get(n) ?? 0) === 0)
  queue.forEach(n => cols.set(n, 0))
  let head = 0
  while (head < queue.length) {
    const n = queue[head++]
    const col = cols.get(n) ?? 0
    ;(graph.get(n) ?? []).filter(c => names.includes(c)).forEach(c => {
      if ((cols.get(c) ?? 0) <= col) { cols.set(c, col + 1); queue.push(c) }
    })
  }
  names.forEach(n => { if (!cols.has(n)) cols.set(n, 0) })

  const byCol = new Map<number, string[]>()
  names.forEach(n => {
    const c = cols.get(n) ?? 0
    if (!byCol.has(c)) byCol.set(c, [])
    byCol.get(c)!.push(n)
  })

  const nodes: LayoutNode[] = []
  byCol.forEach((colNames, col) => {
    colNames.forEach((name, row) => {
      nodes.push({ name, x: col * (NODE_W + H_GAP), y: row * (NODE_H + V_GAP) })
    })
  })
  return nodes
}

// ── Component ────────────────────────────────────────────────────────
export default function FlowGraph({ savedFlows, currentFlow, onNavigate, focusFlow }: Props) {
  const [themeKey, setThemeKey] = useState<string>(() => {
    const saved = localStorage.getItem(LS_KEY)
    return saved && saved in THEMES ? saved : 'dark'
  })
  const [pickerOpen, setPickerOpen] = useState(false)
  const [selectedFlow, setSelectedFlow] = useState<string | null>(null)

  const theme = THEMES[themeKey]

  function pickTheme(key: string) {
    setThemeKey(key)
    localStorage.setItem(LS_KEY, key)
    setPickerOpen(false)
  }

  const graph = useMemo(() => buildCallGraph(savedFlows), [savedFlows])
  const flowsToLayout = useMemo(
    () => focusFlow ? getReachableFlows(focusFlow, graph, savedFlows) : savedFlows,
    [focusFlow, graph, savedFlows]
  )
  const nodes = useMemo(() => layoutGraph(flowsToLayout, graph), [flowsToLayout, graph])

  if (nodes.length === 0) {
    return <div style={{ padding: 24, color: theme.edge, fontSize: 13 }}>No flows saved yet.</div>
  }

  const maxX = Math.max(...nodes.map(n => n.x)) + NODE_W
  const maxY = Math.max(...nodes.map(n => n.y)) + NODE_H
  const W = maxX + 48
  const H = maxY + 48

  const nodeMap = new Map(nodes.map(n => [n.name, n]))

  const edges: Array<{ x1: number; y1: number; x2: number; y2: number; key: string }> = []
  graph.forEach((children, parent) => {
    const from = nodeMap.get(parent)
    if (!from) return
    children.forEach(c => {
      const to = nodeMap.get(c)
      if (!to) return
      edges.push({
        x1: from.x + NODE_W + 24,
        y1: from.y + NODE_H / 2 + 24,
        x2: to.x + 20,
        y2: to.y + NODE_H / 2 + 24,
        key: `${parent}→${c}`,
      })
    })
  })

  return (
    <div style={{ borderRadius: 8, overflow: 'hidden', background: theme.bg, border: `1px solid ${theme.border}` }}>

      {/* Toolbar */}
      {!focusFlow && (
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        padding: '5px 10px', background: theme.node, borderBottom: `1px solid ${theme.border}`,
      }}>
        <span style={{ fontSize: 11, color: theme.edge, fontWeight: 600, letterSpacing: '0.05em' }}>
          FLOW GRAPH
        </span>

        {/* Theme picker */}
        <div style={{ position: 'relative' }}>
          <button
            onClick={() => setPickerOpen(p => !p)}
            title="Change graph theme"
            style={{
              background: 'none', border: `1px solid ${theme.border}`, borderRadius: 4,
              padding: '2px 8px', fontSize: 11, color: theme.edge,
              cursor: 'pointer', fontFamily: 'inherit', display: 'flex', alignItems: 'center', gap: 5,
            }}
          >
            <span style={{
              width: 10, height: 10, borderRadius: '50%',
              background: theme.active, display: 'inline-block', flexShrink: 0,
            }} />
            {theme.name}
          </button>

          {pickerOpen && (
            <div style={{
              position: 'absolute', right: 0, top: '110%', zIndex: 100,
              background: '#111827', border: `1px solid ${theme.border}`,
              borderRadius: 8, padding: 10,
              display: 'flex', flexDirection: 'column', gap: 4, minWidth: 120,
              boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
            }}>
              {THEME_ORDER.map(key => {
                const t = THEMES[key]
                const isSelected = key === themeKey
                return (
                  <button
                    key={key}
                    onClick={() => pickTheme(key)}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 8,
                      background: isSelected ? 'rgba(255,255,255,0.08)' : 'none',
                      border: isSelected ? `1px solid ${t.active}` : '1px solid transparent',
                      borderRadius: 5, padding: '4px 8px', cursor: 'pointer',
                      fontFamily: 'inherit', fontSize: 12, color: '#ecf0f9',
                      width: '100%', textAlign: 'left',
                    }}
                  >
                    <span style={{
                      width: 12, height: 12, borderRadius: '50%', flexShrink: 0,
                      background: t.active, boxShadow: `0 0 0 2px ${t.node}`,
                    }} />
                    {t.name}
                  </button>
                )
              })}
            </div>
          )}
        </div>
      </div>
      )}

      {/* Step detail panel */}
      {selectedFlow && (() => {
        const flow = savedFlows.find(f => f.name === selectedFlow)
        if (!flow) return null
        return (
          <div style={{
            margin: '0 8px 8px',
            padding: 12,
            borderRadius: 8,
            border: `1px solid ${theme.border}`,
            background: 'rgba(255,255,255,0.02)',
            maxHeight: 240,
            overflowY: 'auto',
          }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
              <span style={{ fontWeight: 700, fontSize: 13, color: theme.text }}>{selectedFlow}</span>
              <div style={{ display: 'flex', gap: 6 }}>
                <button
                  onClick={() => { onNavigate(selectedFlow); setSelectedFlow(null) }}
                  style={{
                    fontSize: 11, padding: '2px 8px',
                    background: theme.active, border: 'none', borderRadius: 4,
                    cursor: 'pointer', color: theme.activeText, fontFamily: 'inherit',
                  }}
                >
                  Edit ↗
                </button>
                <button
                  onClick={() => setSelectedFlow(null)}
                  style={{
                    fontSize: 13, padding: '0 6px',
                    background: 'none', border: 'none', cursor: 'pointer', color: theme.edge,
                  }}
                >
                  ✕
                </button>
              </div>
            </div>
            {flow.steps.map((step, si) => (
              <div
                key={si}
                style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  padding: '3px 0', fontSize: 12,
                  borderBottom: `1px solid rgba(255,255,255,0.04)`,
                  color: theme.text,
                }}
              >
                <span style={{ fontSize: 13, width: 18, textAlign: 'center', flexShrink: 0 }}>
                  {STEP_ICONS[step.action] ?? '•'}
                </span>
                <span style={{ fontWeight: 500 }}>{step.action}</span>
                {step['as'] != null && (
                  <span style={{ color: '#34d399', fontFamily: 'monospace', fontSize: 11 }}>
                    → {String(step['as'] as string)}
                  </span>
                )}
                {step['condition'] != null && (
                  <span style={{ color: theme.edge, fontFamily: 'monospace', fontSize: 10 }}>
                    if {String(step['condition'] as string).slice(0, 30)}
                  </span>
                )}
              </div>
            ))}
          </div>
        )
      })()}

      {/* SVG canvas */}
      <div style={{ overflowX: 'auto', overflowY: 'auto', maxHeight: 380, padding: 8 }}>
        <svg width={W} height={H} style={{ display: 'block' }}>
          <defs>
            <marker id={`arrow-${themeKey}`} markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
              <path d="M0,0 L0,6 L8,3 z" fill={theme.edge} />
            </marker>
          </defs>

          {edges.map(e => (
            <line
              key={e.key}
              x1={e.x1} y1={e.y1} x2={e.x2} y2={e.y2}
              stroke={theme.edge}
              strokeWidth={1.5}
              markerEnd={`url(#arrow-${themeKey})`}
            />
          ))}

          {nodes.map(n => {
            const isActive   = n.name === currentFlow
            const isSelected = n.name === selectedFlow
            const isMissing  = !savedFlows.find(f => f.name === n.name)
            const nx = n.x + 24
            const ny = n.y + 24
            const label = n.name.length > 16 ? n.name.slice(0, 15) + '…' : n.name
            return (
              <g
                key={n.name}
                transform={`translate(${nx},${ny})`}
                style={{ cursor: 'pointer' }}
                onClick={() => setSelectedFlow(prev => prev === n.name ? null : n.name)}
                onDoubleClick={() => onNavigate(n.name)}
              >
                <rect
                  x={0} y={0} width={NODE_W} height={NODE_H} rx={7}
                  fill={isActive ? theme.active : isMissing ? theme.danger : theme.node}
                  stroke={isSelected ? theme.active : isActive ? theme.active : theme.border}
                  strokeWidth={isSelected || isActive ? 2 : 1}
                  strokeDasharray={isSelected && !isActive ? '4 2' : undefined}
                />
                <text
                  x={NODE_W / 2} y={NODE_H / 2}
                  textAnchor="middle"
                  dominantBaseline="central"
                  fill={isActive ? theme.activeText : theme.text}
                  fontSize={12}
                  fontWeight={isActive || isSelected ? 600 : 400}
                  fontFamily="Inter, system-ui, sans-serif"
                >
                  {label}
                </text>
              </g>
            )
          })}
        </svg>
      </div>
    </div>
  )
}
