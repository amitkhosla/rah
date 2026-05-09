import { useState } from 'react'
import type { FlowStep, SavedFlow } from '../types'

// ── Step icon map ─────────────────────────────────────────────────────

const STEP_ICONS: Record<string, string> = {
  'if': '🔀', 'switch': '🔀', 'call': '📞', 'return': '↩', 'fail': '✗',
  'token_validation': '🔒', 'http_call': '🌐', 'llm_call': '🧠',
  'cache_get': '🗄️', 'cache_get_global': '🗄️', 'cache_put': '🗄️', 'cache_put_global': '🗄️',
  'bind_header': '📥', 'bind_query': '📥', 'bind_path': '📥', 'bind_body': '📥',
  'bind_client_ip': '🌐', 'emit_event': '📊', 'log_field': '📋',
  'registry_lookup': '🏷️', 'load_service_url': '🔗', 'load_identifier': '🔑',
  'check_rate_limit': '⏱', 'set_response_body': '📤', 'set_response_header': '📤',
  'set_response_status': '📤', 'extract': '✂️', 'json_extract_emit': '✂️',
  'mcp_call_tool': '🔧', 'vector_search': '🔍', 'embed_text': '🔢',
  'store_internal_tx_id': '🔖', 'bind_correlation_id': '🔖',
}
function stepIcon(action: string): string { return STEP_ICONS[action] ?? '•' }

// ── Helpers ──────────────────────────────────────────────────────────

export function collectFlowRefs(steps: FlowStep[]): string[] {
  const refs: string[] = []
  for (const step of steps) {
    if (step['then'])      refs.push(step['then'] as string)
    if (step['else'])      refs.push(step['else'] as string)
    if (step['flow_name']) refs.push(step['flow_name'] as string)
    if (step['cases']) {
      ;(step['cases'] as string).split(',').forEach(c => {
        const eq = c.indexOf('=')
        if (eq >= 0) refs.push(c.slice(eq + 1).trim())
      })
    }
    if (step.then_steps) refs.push(...collectFlowRefs(step.then_steps))
    if (step.else_steps)  refs.push(...collectFlowRefs(step.else_steps))
  }
  return refs.filter(Boolean)
}

export function buildCallGraph(flows: SavedFlow[]): Map<string, string[]> {
  const graph = new Map<string, string[]>()
  for (const flow of flows) {
    const refs = collectFlowRefs(flow.steps)
    graph.set(flow.name, [...new Set(refs)])
  }
  return graph
}

// ── Node component ────────────────────────────────────────────────────

interface NodeProps {
  name: string
  callGraph: Map<string, string[]>
  allNames: Set<string>
  stepCount: Map<string, number>
  savedFlows: SavedFlow[]
  onNavigate: (name: string) => void
  currentFlow: string
  depth: number
  visited: Set<string>
  hideAuto: boolean
}

function FlowMapNode({
  name, callGraph, allNames, stepCount, savedFlows, onNavigate, currentFlow, depth, visited, hideAuto,
}: NodeProps) {
  const [open, setOpen] = useState(depth === 0)
  const [stepsOpen, setStepsOpen] = useState(false)

  const isCycle = visited.has(name)
  const rawChildren = isCycle ? [] : (callGraph.get(name) ?? [])
  const children = hideAuto ? rawChildren.filter(c => !c.startsWith('__auto_')) : rawChildren

  const isActive  = name === currentFlow
  const isMissing = !allNames.has(name) && depth > 0
  const count     = stepCount.get(name)

  const newVisited = new Set(visited)
  newVisited.add(name)

  const hasChildren = children.length > 0 && !isCycle
  const isAuto      = name.startsWith('__auto_')

  return (
    <div style={{ marginLeft: depth === 0 ? 0 : 12 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 4,
          padding: '3px 0',
          borderLeft: depth > 0 ? '1px solid rgba(255,255,255,0.08)' : 'none',
          paddingLeft: depth > 0 ? 10 : 0,
          marginBottom: 1,
        }}
      >
        {/* Expand/collapse arrow for nodes with children */}
        <button
          onClick={() => hasChildren && setOpen(o => !o)}
          style={{
            width: 14,
            height: 14,
            background: 'none',
            border: 'none',
            padding: 0,
            cursor: hasChildren ? 'pointer' : 'default',
            color: hasChildren ? 'var(--muted)' : 'transparent',
            fontSize: 9,
            flexShrink: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          {hasChildren ? (open ? '▼' : '▶') : ''}
        </button>

        {/* Flow name button */}
        <button
          onClick={() => !isMissing && onNavigate(name)}
          title={isMissing ? 'Flow not yet created in Studio' : `Open "${name}"`}
          style={{
            background: isActive ? 'rgba(var(--accent-rgb,87,181,255),0.15)' : 'none',
            border: isActive ? '1px solid var(--accent)' : '1px solid transparent',
            borderRadius: 4,
            padding: '2px 7px',
            color: isMissing ? 'var(--muted)' : isAuto ? 'var(--muted)' : 'var(--fg)',
            cursor: isMissing ? 'default' : 'pointer',
            fontSize: 12,
            fontFamily: 'inherit',
            textAlign: 'left',
            fontStyle: isAuto ? 'italic' : 'normal',
          }}
        >
          ⛶ {name}
        </button>

        {/* Step count badge + steps toggle */}
        {count !== undefined && count > 0 && (
          <>
            <span style={{ fontSize: 10, color: 'var(--muted)', flexShrink: 0 }}>
              {count}
            </span>
            {!isMissing && (
              <button
                onClick={e => { e.stopPropagation(); setStepsOpen(o => !o) }}
                style={{
                  marginLeft: 2,
                  fontSize: 9,
                  padding: '1px 4px',
                  background: 'rgba(255,255,255,0.06)',
                  border: '1px solid rgba(255,255,255,0.12)',
                  borderRadius: 3,
                  cursor: 'pointer',
                  color: 'var(--muted)',
                  flexShrink: 0,
                }}
                title="Show/hide steps"
              >
                {stepsOpen ? '▲ steps' : '▼ steps'}
              </button>
            )}
          </>
        )}

        {/* Missing indicator */}
        {isMissing && (
          <span title="Referenced but not created in Studio" style={{ fontSize: 10, color: '#f59e0b' }}>?</span>
        )}

        {/* Cycle indicator */}
        {isCycle && (
          <span title="Cycle detected — not expanded" style={{ fontSize: 12, color: '#f59e0b' }}>⟳</span>
        )}
      </div>

      {/* Step list (expanded inline) */}
      {stepsOpen && (() => {
        const flow = savedFlows.find(f => f.name === name)
        if (!flow) return null
        return (
          <div style={{
            marginLeft: depth === 0 ? 14 : 26,
            marginTop: 2,
            marginBottom: 4,
            paddingLeft: 8,
            borderLeft: '2px solid rgba(255,255,255,0.08)',
          }}>
            {flow.steps.map((step, si) => {
              const isCallable = ['call', 'if', 'switch'].includes(step.action)
              const target = (step['flow_name'] || step['then'] || step['else']) as string | undefined
              return (
                <div
                  key={si}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 5,
                    padding: '2px 0',
                    fontSize: 11,
                    color: 'var(--muted)',
                  }}
                >
                  <span style={{ width: 16, textAlign: 'center', fontSize: 12, flexShrink: 0 }}>{stepIcon(step.action)}</span>
                  <span style={{ color: 'var(--fg)', opacity: 0.8 }}>{step.action}</span>
                  {step['as'] != null && (
                    <span style={{ color: '#34d399', fontFamily: 'monospace', fontSize: 10 }}>→ {String(step['as'] as string)}</span>
                  )}
                  {isCallable && target && (
                    <button
                      onClick={e => { e.stopPropagation(); onNavigate(target) }}
                      style={{
                        background: 'none', border: 'none', color: 'var(--accent)',
                        fontSize: 10, cursor: 'pointer', padding: '0 2px', textDecoration: 'underline',
                      }}
                    >
                      {target} ↗
                    </button>
                  )}
                </div>
              )
            })}
          </div>
        )
      })()}

      {/* Children */}
      {open && hasChildren && (
        <div style={{ marginLeft: 14 }}>
          {children.map(child => (
            <FlowMapNode
              key={child}
              name={child}
              callGraph={callGraph}
              allNames={allNames}
              stepCount={stepCount}
              savedFlows={savedFlows}
              onNavigate={onNavigate}
              currentFlow={currentFlow}
              depth={depth + 1}
              visited={newVisited}
              hideAuto={hideAuto}
            />
          ))}
        </div>
      )}
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────

interface Props {
  savedFlows: SavedFlow[]
  currentFlow: string
  onNavigate: (name: string) => void
}

export default function FlowMap({ savedFlows, currentFlow, onNavigate }: Props) {
  const [hideAuto, setHideAuto] = useState(true)

  const visibleFlows = hideAuto
    ? savedFlows.filter(f => !f.name.startsWith('__auto_'))
    : savedFlows

  const callGraph = buildCallGraph(savedFlows)  // graph always uses all flows
  const allNames  = new Set(savedFlows.map(f => f.name))

  const stepCount = new Map(savedFlows.map(f => [f.name, f.steps.length]))

  // Root flows: flows that no other flow references
  const referenced = new Set<string>()
  for (const [, refs] of callGraph) {
    for (const r of refs) referenced.add(r)
  }
  const roots = visibleFlows.filter(f => !referenced.has(f.name))
  // Fallback: if everything is referenced (cyclic graph), show all visible flows as roots
  const treeRoots = roots.length > 0 ? roots : visibleFlows

  if (savedFlows.length === 0) {
    return (
      <div style={{ padding: '16px 12px', color: 'var(--muted)', fontSize: 12 }}>
        No flows saved yet. Save a flow to see the map.
      </div>
    )
  }

  return (
    <div style={{
      border: '1px solid var(--border)',
      borderRadius: 6,
      marginBottom: 10,
      background: 'rgba(255,255,255,0.02)',
      overflow: 'hidden',
    }}>
      {/* Header */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        padding: '6px 10px',
        borderBottom: '1px solid var(--border)',
        background: 'rgba(255,255,255,0.03)',
      }}>
        <span style={{ fontSize: 12, fontWeight: 700, color: 'var(--muted)', letterSpacing: '0.05em', flex: 1 }}>
          FLOW MAP
        </span>
        <label style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 11, color: 'var(--muted)', cursor: 'pointer' }}>
          <input
            type="checkbox"
            checked={hideAuto}
            onChange={e => setHideAuto(e.target.checked)}
            style={{ cursor: 'pointer' }}
          />
          Hide auto-fragments
        </label>
      </div>

      {/* Tree */}
      <div style={{ padding: '8px 10px', maxHeight: 260, overflowY: 'auto' }}>
        {treeRoots.map(f => (
          <FlowMapNode
            key={f.name}
            name={f.name}
            callGraph={callGraph}
            allNames={allNames}
            stepCount={stepCount}
            savedFlows={savedFlows}
            onNavigate={onNavigate}
            currentFlow={currentFlow}
            depth={0}
            visited={new Set()}
            hideAuto={hideAuto}
          />
        ))}
      </div>
    </div>
  )
}
