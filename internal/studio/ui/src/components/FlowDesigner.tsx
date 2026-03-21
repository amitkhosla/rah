import { useMemo, useState } from 'react'
import type { FieldDef, FlowStep, PaletteBlock, SavedFlow } from '../types'
import { expandSteps, findSourceRefs, smartCondition } from '../utils/expressions'

interface Props {
  blocks: PaletteBlock[]
  steps: FlowStep[]
  setSteps: (steps: FlowStep[]) => void
  flowName: string
  setFlowName: (name: string) => void
  savedFlows: SavedFlow[]
  onSaveFlow: () => void
}

// ── Switch case helpers ───────────────────────────────────────────
type SwitchCase = { match: string; flow: string }

function parseCases(raw: string): SwitchCase[] {
  if (!raw.trim()) return []
  return raw.split(',').reduce<SwitchCase[]>((acc, part) => {
    const eq = part.indexOf('=')
    if (eq === -1) {
      if (part.trim()) acc.push({ match: part.trim(), flow: '' })
    } else {
      acc.push({ match: part.slice(0, eq).trim(), flow: part.slice(eq + 1).trim() })
    }
    return acc
  }, [])
}

function serializeCases(cases: SwitchCase[]): string {
  return cases.map(c => `${c.match}=${c.flow}`).join(',')
}

// ── Component ─────────────────────────────────────────────────────
export default function FlowDesigner({
  blocks, steps, setSteps, flowName, setFlowName, savedFlows, onSaveFlow,
}: Props) {
  const [filter, setFilter]       = useState('')
  const [dragOver, setDragOver]   = useState(false)
  const [expanded, setExpanded]   = useState<Set<number>>(new Set())
  const [justSaved, setJustSaved] = useState(false)

  // map: block-type → { fieldKey → FieldDef }
  const fieldMap = useMemo<Record<string, Record<string, FieldDef>>>(() =>
    Object.fromEntries(
      blocks.map(b => [b.type, Object.fromEntries((b.fields ?? []).map(f => [f.key, f]))])
    ),
    [blocks],
  )

  // Synthetic call-blocks for each saved flow
  const callFieldDefs = useMemo(() => blocks.find(b => b.type === 'call')?.fields ?? [], [blocks])
  const savedFlowBlocks: PaletteBlock[] = useMemo(() =>
    savedFlows.map(sf => ({
      type: 'call',
      title: sf.name,
      description: `${sf.steps.length} step${sf.steps.length !== 1 ? 's' : ''}`,
      category: 'my-flows',
      capability: 'sub-flow',
      supports_nested: false,
      defaults: { flow_name: sf.name },
      fields: callFieldDefs,
    })),
    [savedFlows, callFieldDefs],
  )

  // Filtered lists
  const q = filter.toLowerCase()
  const filteredSaved = savedFlowBlocks.filter(b =>
    !filter || b.title.toLowerCase().includes(q) || 'my-flows'.includes(q),
  )
  const filteredBlocks = blocks.filter(b =>
    !filter ||
    b.category.toLowerCase().includes(q) ||
    b.title.toLowerCase().includes(q)     ||
    b.type.toLowerCase().includes(q),
  )

  // ── Mutations ───────────────────────────────────────────────────
  function handleDrop(e: React.DragEvent) {
    e.preventDefault()
    setDragOver(false)
    const raw = e.dataTransfer.getData('application/json')
    if (!raw) return
    const b: PaletteBlock = JSON.parse(raw)
    const idx = steps.length
    setSteps([...steps, { action: b.type, ...b.defaults }])
    setExpanded(prev => new Set([...prev, idx]))
  }

  function toggleExpand(i: number) {
    setExpanded(prev => {
      const next = new Set(prev)
      next.has(i) ? next.delete(i) : next.add(i)
      return next
    })
  }

  function updateStep(i: number, key: string, value: string) {
    setSteps(steps.map((s, idx) => idx === i ? { ...s, [key]: value } : s))
  }

  function removeStep(i: number) {
    setSteps(steps.filter((_, idx) => idx !== i))
    setExpanded(prev => {
      const next = new Set<number>()
      prev.forEach(idx => { if (idx < i) next.add(idx); else if (idx > i) next.add(idx - 1) })
      return next
    })
  }

  function handleSave() {
    if (!flowName.trim() || steps.length === 0) return
    onSaveFlow()
    setJustSaved(true)
    setTimeout(() => setJustSaved(false), 1800)
  }

  // ── Field input with source-ref chips ──────────────────────────
  function fieldInput(
    stepIdx: number,
    key: string,
    value: string,
    def?: FieldDef,
    opts?: { smart?: boolean; onBlur?: (v: string) => void },
  ) {
    const refs = findSourceRefs(value)
    return (
      <div key={key} className="field-row">
        <label className="field-label">{def?.label ?? key}</label>
        <input
          className="input"
          value={value ?? ''}
          placeholder={def?.placeholder ?? key}
          onChange={e => updateStep(stepIdx, key, e.target.value)}
          onBlur={opts?.smart ? e => {
            const normalised = smartCondition(e.target.value)
            if (normalised !== e.target.value) updateStep(stepIdx, key, normalised)
            opts.onBlur?.(normalised)
          } : undefined}
        />
        {/* Source-ref chips */}
        {refs.length > 0 && (
          <div className="ref-chips">
            {refs.map(ref => (
              <span key={ref.raw} className="ref-chip">
                {ref.raw} <span className="ref-chip-arrow">→</span> slot: {ref.slotName}
              </span>
            ))}
          </div>
        )}
        {def?.description && <span className="field-desc">{def.description}</span>}
      </div>
    )
  }

  // ── Special step bodies ─────────────────────────────────────────
  function renderIfBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    return (
      <div className="step-body">
        {fieldInput(i, 'condition', step['condition'] ?? '', defs['condition'], { smart: true })}
        <div className="branch-row">
          <div className="branch-cell branch-then">
            <div className="branch-label">✓ Then</div>
            <input
              className="input"
              value={step['then'] ?? ''}
              placeholder={defs['then']?.placeholder ?? 'flow name'}
              onChange={e => updateStep(i, 'then', e.target.value)}
            />
            {defs['then']?.description && (
              <span className="field-desc">{defs['then'].description}</span>
            )}
          </div>
          <div className="branch-cell branch-else">
            <div className="branch-label branch-label-else">✗ Else</div>
            <input
              className="input"
              value={step['else'] ?? ''}
              placeholder={defs['else']?.placeholder ?? 'flow name (optional)'}
              onChange={e => updateStep(i, 'else', e.target.value)}
            />
            {defs['else']?.description && (
              <span className="field-desc">{defs['else'].description}</span>
            )}
          </div>
        </div>
      </div>
    )
  }

  function renderSwitchBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    const cases = parseCases(step['cases'] ?? '')
    return (
      <div className="step-body">
        {/* Match slot — smart: accepts header.X-TID or bare "X-TID" */}
        {fieldInput(i, 'as', step['as'] ?? '', defs['as'], { smart: true })}
        <div className="switch-cases-header">
          <span className="field-label">Cases</span>
          {defs['cases']?.description && (
            <span className="field-desc">{defs['cases'].description}</span>
          )}
        </div>
        <div className="switch-cases">
          {cases.length === 0 && <span className="hint">No cases yet — add one below.</span>}
          {cases.map((c, ci) => (
            <div key={ci} className="switch-case-row">
              <input
                className="input case-match"
                value={c.match}
                placeholder="match value"
                onChange={e => {
                  const upd = [...cases]; upd[ci] = { ...upd[ci], match: e.target.value }
                  updateStep(i, 'cases', serializeCases(upd))
                }}
              />
              <span className="case-arrow">→</span>
              <input
                className="input case-flow"
                value={c.flow}
                placeholder="flow name"
                onChange={e => {
                  const upd = [...cases]; upd[ci] = { ...upd[ci], flow: e.target.value }
                  updateStep(i, 'cases', serializeCases(upd))
                }}
              />
              <button
                className="btn muted case-remove"
                onClick={() => updateStep(i, 'cases', serializeCases(cases.filter((_, j) => j !== ci)))}
              >×</button>
            </div>
          ))}
          <button
            className="btn muted mt4"
            onClick={() => updateStep(i, 'cases', serializeCases([...cases, { match: '', flow: '' }]))}
          >+ Add case</button>
        </div>
      </div>
    )
  }

  function renderGenericBody(step: FlowStep, i: number, defs: Record<string, FieldDef>) {
    const params = Object.entries(step).filter(([k]) => k !== 'action')
    if (params.length === 0) {
      return (
        <div className="step-body">
          <span className="hint">No parameters for this step.</span>
        </div>
      )
    }
    // Fields that benefit from smart expression parsing
    const smartFields = new Set(['condition', 'source', 'as', 'url'])
    return (
      <div className="step-body">
        {params.map(([k, v]) =>
          fieldInput(i, k, v, defs[k], smartFields.has(k) ? { smart: true } : undefined)
        )}
      </div>
    )
  }

  // ── Preview JSON (compiled — source refs auto-expanded) ─────────
  const compiledSteps = expandSteps(steps)
  const hasExpansions = compiledSteps.length !== steps.length
  const previewJson = JSON.stringify(
    { name: flowName || 'new_flow', instructions: compiledSteps, action: 'upsert' },
    null, 2,
  )

  const canSave = flowName.trim().length > 0 && steps.length > 0

  // ── Render ──────────────────────────────────────────────────────
  return (
    <div className="two-col">
      {/* ── Palette ─────────────────────────────────────────────── */}
      <div className="panel">
        <div className="panel-header">Instruction Palette</div>
        <div className="panel-body">
          <input
            className="input"
            placeholder="filter by name or category"
            value={filter}
            onChange={e => setFilter(e.target.value)}
          />
          <div className="block-list">
            {filteredSaved.length > 0 && (
              <>
                <div className="palette-section-label">My Flows</div>
                {filteredSaved.map(b => (
                  <div
                    key={`saved-${b.title}`}
                    className="block block-saved"
                    draggable
                    onDragStart={e => e.dataTransfer.setData('application/json', JSON.stringify(b))}
                  >
                    <strong>{b.title}</strong>
                    <div className="sub">call · {b.description}</div>
                  </div>
                ))}
                <div className="palette-section-label" style={{ marginTop: 8 }}>Instructions</div>
              </>
            )}
            {filteredBlocks.length === 0 && filteredSaved.length === 0 && (
              <span className="hint">No blocks match.</span>
            )}
            {filteredBlocks.map(b => (
              <div
                key={b.type}
                className="block"
                draggable
                onDragStart={e => e.dataTransfer.setData('application/json', JSON.stringify(b))}
              >
                <strong>{b.title}</strong>
                <div className="sub">{b.category} · {b.capability}</div>
                <div className="sub" style={{ marginTop: 2, opacity: 0.75 }}>{b.description}</div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* ── Canvas ──────────────────────────────────────────────── */}
      <div className="panel">
        <div className="panel-header">Flow Canvas</div>
        <div className="panel-body">
          <input
            className="input"
            placeholder="flow name (required to save)"
            value={flowName}
            onChange={e => setFlowName(e.target.value)}
          />
          <div
            className={`canvas${dragOver ? ' drag-over' : ''}`}
            onDragOver={e => { e.preventDefault(); setDragOver(true) }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
          >
            {steps.length === 0 && (
              <span className="hint">Drop instruction blocks here to build a flow</span>
            )}
            {steps.map((step, i) => {
              const defs = fieldMap[step.action] ?? {}
              const isExpanded = expanded.has(i)
              return (
                <div key={i} className="step">
                  <div className="step-header" onClick={() => toggleExpand(i)}>
                    <span className="step-toggle">{isExpanded ? '▼' : '▶'}</span>
                    <strong className="step-title">{i + 1}. {step.action}</strong>
                    <button
                      className="btn muted step-remove"
                      title="Remove step"
                      onClick={e => { e.stopPropagation(); removeStep(i) }}
                    >×</button>
                  </div>
                  {isExpanded && (
                    step.action === 'if'     ? renderIfBody(step, i, defs)     :
                    step.action === 'switch' ? renderSwitchBody(step, i, defs) :
                                               renderGenericBody(step, i, defs)
                  )}
                </div>
              )
            })}
          </div>

          {/* Actions */}
          <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
            <button
              className={`btn${canSave ? '' : ' muted'}`}
              style={{ flex: 1 }}
              disabled={!canSave}
              title={!flowName.trim() ? 'Enter a flow name first' : steps.length === 0 ? 'Add at least one step' : undefined}
              onClick={handleSave}
            >
              {justSaved ? '✓ Saved to palette' : 'Save to My Flows'}
            </button>
            <button
              className="btn muted"
              style={{ flex: 1 }}
              onClick={() => { setSteps([]); setExpanded(new Set()) }}
            >Clear Flow</button>
          </div>

          {/* Compiled JSON preview */}
          {hasExpansions && (
            <div className="expansion-note">
              Preview shows compiled flow — source references auto-extracted
            </div>
          )}
          <textarea className="input mt8" readOnly value={previewJson} />
        </div>
      </div>
    </div>
  )
}
