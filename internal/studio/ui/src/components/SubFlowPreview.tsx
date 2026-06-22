import React, { useState } from 'react'

export type FlowStep = { action: string } & Record<string, string>

export interface SavedFlow {
  name: string
  steps: FlowStep[]
}

interface Props {
  steps: FlowStep[]
  savedFlows: SavedFlow[]
  depth?: number
}

function truncate(s: string, max: number): string {
  if (!s) return ''
  return s.length > max ? s.slice(0, max) + '…' : s
}

function findFlow(name: string, savedFlows: SavedFlow[]): SavedFlow | undefined {
  return savedFlows.find(f => f.name === name)
}

function parseCases(raw: unknown): Array<{ value: string; flowName: string }> {
  if (!raw) return []
  // backend may return an object {"val":"flow"} instead of the string "val=flow,..."
  const casesStr = (typeof raw === 'object' && !Array.isArray(raw))
    ? Object.entries(raw as Record<string, string>).map(([k, v]) => `${k}=${v}`).join(',')
    : String(raw)
  if (!casesStr) return []
  return casesStr.split(',').map(part => {
    const eqIdx = part.indexOf('=')
    if (eqIdx === -1) return { value: part.trim(), flowName: '' }
    return {
      value: part.slice(0, eqIdx).trim(),
      flowName: part.slice(eqIdx + 1).trim(),
    }
  }).filter(c => c.flowName !== '')
}

export default function SubFlowPreview({ steps, savedFlows, depth = 0 }: Props) {
  const [expandedSteps, setExpandedSteps] = useState<Set<number>>(new Set())

  if (steps.length === 0) {
    return (
      <span style={{ fontSize: 11, color: 'var(--muted)', fontStyle: 'italic' }}>
        Flow is empty
      </span>
    )
  }

  function toggleStep(idx: number) {
    setExpandedSteps(prev => {
      const next = new Set(prev)
      if (next.has(idx)) {
        next.delete(idx)
      } else {
        next.add(idx)
      }
      return next
    })
  }

  return (
    <div style={{ paddingLeft: depth * 10 }}>
      {steps.map((step, idx) => {
        const isIf = step.action === 'if'
        const isSwitch = step.action === 'switch'
        const isBranching = isIf || isSwitch
        const isExpanded = expandedSteps.has(idx)
        const isLast = idx === steps.length - 1

        return (
          <div
            key={idx}
            style={{
              borderLeft: '2px solid rgba(87,181,255,0.3)',
              paddingLeft: 8,
              borderBottom: isLast ? undefined : '1px solid rgba(255,255,255,0.04)',
            }}
          >
            {/* Step row */}
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                padding: '4px 0',
                fontSize: 11,
              }}
            >
              {/* Step number */}
              <span style={{ fontWeight: 700, color: 'var(--accent)', minWidth: 18 }}>
                {idx + 1}.
              </span>

              {isIf && (
                <>
                  <span style={{ color: 'var(--accent)', fontWeight: 700 }}>if</span>
                  <span style={{ opacity: 0.65, color: 'var(--muted)' }}>
                    {truncate(step['condition'] || '', 40)}
                  </span>
                  {depth < 4 ? (
                    <button
                      className="btn muted"
                      style={{ fontSize: 10, padding: '1px 5px' }}
                      onClick={() => toggleStep(idx)}
                    >
                      {isExpanded ? '▼' : '▶'}
                    </button>
                  ) : (
                    <span style={{ opacity: 0.65, color: 'var(--muted)' }}>…(nested further)</span>
                  )}
                </>
              )}

              {isSwitch && (
                <>
                  <span style={{ color: 'var(--accent)', fontWeight: 700 }}>switch</span>
                  <span style={{ opacity: 0.65, color: 'var(--muted)' }}>
                    {truncate(step['as'] || '', 40)}
                  </span>
                  {depth < 4 ? (
                    <button
                      className="btn muted"
                      style={{ fontSize: 10, padding: '1px 5px' }}
                      onClick={() => toggleStep(idx)}
                    >
                      {isExpanded ? '▼' : '▶'}
                    </button>
                  ) : (
                    <span style={{ opacity: 0.65, color: 'var(--muted)' }}>…(nested further)</span>
                  )}
                </>
              )}

              {!isBranching && (
                <>
                  <span style={{ color: 'var(--text)', fontWeight: 600 }}>{step.action}</span>
                  {Object.entries(step)
                    .filter(([k]) => k !== 'action')
                    .slice(0, 2)
                    .map(([k, v]) => (
                      <span key={k} style={{ opacity: 0.65, color: 'var(--muted)' }}>
                        {k}: {truncate(String(v), 24)}
                      </span>
                    ))}
                </>
              )}
            </div>

            {/* Expanded if branches */}
            {isIf && isExpanded && depth < 4 && (
              <>
                {/* Then branch */}
                <div>
                  <div
                    style={{
                      fontSize: 10,
                      color: 'var(--muted)',
                      textTransform: 'uppercase',
                      letterSpacing: 0.8,
                      margin: '4px 0 2px 20px',
                    }}
                  >
                    Then:
                  </div>
                  <div
                    style={{
                      marginLeft: 20,
                      marginBottom: 6,
                      padding: '4px 8px',
                      borderRadius: 4,
                      background: 'rgba(255,255,255,0.02)',
                    }}
                  >
                    {step['then'] ? (
                      (() => {
                        const thenFlow = findFlow(step['then'], savedFlows)
                        return thenFlow ? (
                          <SubFlowPreview
                            steps={thenFlow.steps}
                            savedFlows={savedFlows}
                            depth={depth + 1}
                          />
                        ) : (
                          <span style={{ fontSize: 11, color: 'var(--muted)', fontStyle: 'italic' }}>
                            flow not found: {step['then']}
                          </span>
                        )
                      })()
                    ) : (
                      <span style={{ fontSize: 11, color: 'var(--muted)', fontStyle: 'italic' }}>
                        no then flow specified
                      </span>
                    )}
                  </div>
                </div>

                {/* Else branch (optional) */}
                {step['else'] && (
                  <div>
                    <div
                      style={{
                        fontSize: 10,
                        color: 'var(--muted)',
                        textTransform: 'uppercase',
                        letterSpacing: 0.8,
                        margin: '4px 0 2px 20px',
                      }}
                    >
                      Else:
                    </div>
                    <div
                      style={{
                        marginLeft: 20,
                        marginBottom: 6,
                        padding: '4px 8px',
                        borderRadius: 4,
                        background: 'rgba(255,255,255,0.02)',
                      }}
                    >
                      {(() => {
                        const elseFlow = findFlow(step['else'], savedFlows)
                        return elseFlow ? (
                          <SubFlowPreview
                            steps={elseFlow.steps}
                            savedFlows={savedFlows}
                            depth={depth + 1}
                          />
                        ) : (
                          <span style={{ fontSize: 11, color: 'var(--muted)', fontStyle: 'italic' }}>
                            flow not found: {step['else']}
                          </span>
                        )
                      })()}
                    </div>
                  </div>
                )}
              </>
            )}

            {/* Expanded switch cases */}
            {isSwitch && isExpanded && depth < 4 && (
              <>
                {parseCases(step['cases']).map((c, cIdx) => {
                  const caseFlow = findFlow(c.flowName, savedFlows)
                  return (
                    <div key={cIdx}>
                      <div
                        style={{
                          fontSize: 10,
                          color: 'var(--muted)',
                          textTransform: 'uppercase',
                          letterSpacing: 0.8,
                          margin: '4px 0 2px 20px',
                        }}
                      >
                        {c.value} → {c.flowName}
                      </div>
                      <div
                        style={{
                          marginLeft: 20,
                          marginBottom: 6,
                          padding: '4px 8px',
                          borderRadius: 4,
                          background: 'rgba(255,255,255,0.02)',
                        }}
                      >
                        {caseFlow ? (
                          <SubFlowPreview
                            steps={caseFlow.steps}
                            savedFlows={savedFlows}
                            depth={depth + 1}
                          />
                        ) : (
                          <span style={{ fontSize: 11, color: 'var(--muted)', fontStyle: 'italic' }}>
                            flow not found: {c.flowName}
                          </span>
                        )}
                      </div>
                    </div>
                  )
                })}
              </>
            )}
          </div>
        )
      })}
    </div>
  )
}
