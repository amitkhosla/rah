import { useState } from 'react'
import type { PaletteBlock, FlowStep } from '../types'

interface Props {
  blocks: PaletteBlock[]
  steps: FlowStep[]
  setSteps: (steps: FlowStep[]) => void
  flowName: string
  setFlowName: (name: string) => void
}

export default function FlowDesigner({ blocks, steps, setSteps, flowName, setFlowName }: Props) {
  const [filter, setFilter] = useState('')
  const [dragOver, setDragOver] = useState(false)

  const filtered = filter
    ? blocks.filter(b => b.category.toLowerCase().includes(filter.toLowerCase()))
    : blocks

  function handleDrop(e: React.DragEvent) {
    e.preventDefault()
    setDragOver(false)
    const raw = e.dataTransfer.getData('application/json')
    if (!raw) return
    const b: PaletteBlock = JSON.parse(raw)
    setSteps([...steps, { action: b.type, ...b.defaults }])
  }

  function updateStep(i: number, key: string, value: string) {
    setSteps(steps.map((s, idx) => (idx === i ? { ...s, [key]: value } : s)))
  }

  function removeStep(i: number) {
    setSteps(steps.filter((_, idx) => idx !== i))
  }

  const previewJson = JSON.stringify(
    { name: flowName || 'new_flow', instructions: steps, action: 'upsert' },
    null,
    2,
  )

  return (
    <div className="two-col">
      {/* Palette */}
      <div className="panel">
        <div className="panel-header">Instruction Palette</div>
        <div className="panel-body">
          <input
            className="input"
            placeholder="filter category"
            value={filter}
            onChange={e => setFilter(e.target.value)}
          />
          <div className="block-list">
            {filtered.length === 0 && (
              <span className="hint">No blocks match.</span>
            )}
            {filtered.map(b => (
              <div
                key={b.type}
                className="block"
                draggable
                onDragStart={e =>
                  e.dataTransfer.setData('application/json', JSON.stringify(b))
                }
              >
                <strong>{b.title}</strong>
                <div className="sub">{b.category} · {b.capability}</div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Canvas */}
      <div className="panel">
        <div className="panel-header">Flow Designer</div>
        <div className="panel-body">
          <input
            className="input"
            placeholder="flow name"
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
              <span className="hint">Drop instruction blocks here</span>
            )}
            {steps.map((step, i) => {
              const params = Object.entries(step).filter(([k]) => k !== 'action')
              return (
                <div key={i} className="step">
                  <strong>{i + 1}. {step.action}</strong>
                  {params.map(([k, v]) => (
                    <input
                      key={k}
                      className="input mt4"
                      value={v}
                      placeholder={k}
                      onChange={e => updateStep(i, k, e.target.value)}
                    />
                  ))}
                  <button className="btn muted mt8" onClick={() => removeStep(i)}>
                    Remove
                  </button>
                </div>
              )
            })}
          </div>
          <button className="btn muted mt8" onClick={() => setSteps([])}>
            Clear Flow
          </button>
          <textarea className="input mt8" readOnly value={previewJson} />
        </div>
      </div>
    </div>
  )
}
