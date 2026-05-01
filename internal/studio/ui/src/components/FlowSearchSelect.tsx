import { useEffect, useRef, useState } from 'react'
import type { PaletteBlock, SavedFlow } from '../types'

interface Props {
  flows: SavedFlow[]
  value: string          // currently selected flow name ('' = none)
  onChange: (name: string) => void
  placeholder?: string   // input placeholder text
  onDropPayload?: (block: PaletteBlock) => void  // called when a palette block is dropped
  primaryFlowNames?: Set<string>   // flows assigned to APIs — shown in a separate section
}

export default function FlowSearchSelect({ flows, value, onChange, placeholder, onDropPayload, primaryFlowNames }: Props) {
  const [query, setQuery]       = useState('')
  const [open, setOpen]         = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const containerRef            = useRef<HTMLDivElement>(null)

  // Close dropdown when clicking outside
  useEffect(() => {
    function onClickOutside(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
        setQuery('')
      }
    }
    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [])

  const selectedFlow = flows.find(f => f.name === value)
  const isGhostValue = value !== '' && !selectedFlow
  const filtered = flows.filter(f =>
    !query || f.name.toLowerCase().includes(query.toLowerCase())
  )

  function select(name: string) {
    onChange(name)
    setOpen(false)
    setQuery('')
  }

  function clear() {
    onChange('')
    setQuery('')
    setOpen(false)
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'Enter' && filtered.length > 0) {
      select(filtered[0].name)
    } else if (e.key === 'Escape') {
      setOpen(false)
      setQuery('')
    }
  }

  function handleDragOver(e: React.DragEvent) {
    e.preventDefault()
    setDragOver(true)
  }

  function handleDragLeave() {
    setDragOver(false)
  }

  function handleDrop(e: React.DragEvent) {
    e.preventDefault()
    setDragOver(false)
    try {
      const block: PaletteBlock = JSON.parse(e.dataTransfer.getData('application/json'))
      if (block.defaults?.flow_name) {
        onChange(block.defaults.flow_name)
        onDropPayload?.(block)
      }
    } catch { /* ignore bad drops */ }
  }

  const borderColor = dragOver ? 'var(--accent)' : 'rgba(255,255,255,0.12)'

  return (
    <div
      ref={containerRef}
      style={{ position: 'relative', width: '100%' }}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {/* Selected pill */}
      {selectedFlow && (
        <div style={{
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          padding: '4px 8px',
          background: 'rgba(87,181,255,0.12)',
          border: '1px solid var(--accent)',
          borderRadius: 6,
          marginBottom: 4,
          fontSize: 12,
        }}>
          <span style={{ color: 'var(--accent)', fontWeight: 600, flex: 1 }}>
            ✓ {selectedFlow.name}
          </span>
          <span style={{ color: 'var(--muted)', fontSize: 11 }}>
            {selectedFlow.steps.length} step{selectedFlow.steps.length !== 1 ? 's' : ''}
          </span>
          <button
            style={{
              background: 'none', border: 'none', cursor: 'pointer',
              color: 'var(--muted)', fontSize: 14, lineHeight: 1, padding: '0 2px',
            }}
            onClick={clear}
            title="Clear selection"
          >×</button>
        </div>
      )}

      {/* Ghost pill — value is set but flow is not in current session */}
      {isGhostValue && (
        <div style={{
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          padding: '4px 8px',
          background: 'rgba(251,191,36,0.08)',
          border: '1px dashed rgba(251,191,36,0.45)',
          borderRadius: 6,
          marginBottom: 4,
          fontSize: 12,
        }}>
          <span style={{ color: '#fbbf24', fontWeight: 600, flex: 1 }}>
            ⚠ {value}
          </span>
          <span style={{ color: 'rgba(251,191,36,0.55)', fontSize: 11 }}>
            · not in session
          </span>
          <button
            style={{
              background: 'none', border: 'none', cursor: 'pointer',
              color: 'rgba(251,191,36,0.55)', fontSize: 14, lineHeight: 1, padding: '0 2px',
            }}
            onClick={clear}
            title="Clear selection"
          >×</button>
        </div>
      )}

      {/* Search input */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        border: `1px solid ${borderColor}`,
        borderRadius: 6,
        background: dragOver ? 'rgba(87,181,255,0.06)' : 'var(--step-bg)',
        transition: 'border-color 0.15s, background 0.15s',
      }}>
        <span style={{ padding: '0 6px', color: 'var(--muted)', fontSize: 13 }}>🔍</span>
        <input
          style={{
            flex: 1,
            background: 'transparent',
            border: 'none',
            outline: 'none',
            color: 'var(--text)',
            fontSize: 12,
            padding: '6px 4px',
          }}
          placeholder={dragOver ? '⬇ drop here…' : (placeholder ?? 'search flows…')}
          value={query}
          onChange={e => { setQuery(e.target.value); setOpen(true) }}
          onFocus={() => setOpen(true)}
          onKeyDown={handleKeyDown}
        />
        {flows.length > 0 && (
          <span style={{ padding: '0 6px', color: 'var(--muted)', fontSize: 10 }}>
            {flows.length} flow{flows.length !== 1 ? 's' : ''}
          </span>
        )}
      </div>

      {/* Dropdown list */}
      {open && (
        <div style={{
          position: 'absolute',
          top: '100%',
          left: 0,
          right: 0,
          zIndex: 100,
          background: 'var(--panel)',
          border: '1px solid rgba(255,255,255,0.12)',
          borderRadius: 6,
          marginTop: 2,
          maxHeight: 200,
          overflowY: 'auto',
          boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
        }}>
          {filtered.length === 0 && (
            <div style={{ padding: '10px 12px', fontSize: 11, color: 'var(--muted)' }}>
              {flows.length === 0
                ? 'No flows saved yet — design one in the canvas and save it first.'
                : 'No flows match your search.'}
            </div>
          )}
          {(() => {
            // When primaryFlowNames is provided, split into two sections
            if (!primaryFlowNames || primaryFlowNames.size === 0) {
              return filtered.map(f => <FlowOption key={f.name} f={f} value={value} onSelect={select} />)
            }
            const apiFlows = filtered.filter(f =>  primaryFlowNames.has(f.name))
            const subFlows = filtered.filter(f => !primaryFlowNames.has(f.name))
            return (
              <>
                {apiFlows.length > 0 && (
                  <>
                    <div style={{
                      padding: '4px 12px',
                      fontSize: 10,
                      fontWeight: 700,
                      color: 'var(--accent)',
                      textTransform: 'uppercase',
                      letterSpacing: '0.06em',
                      background: 'rgba(87,181,255,0.06)',
                      borderBottom: '1px solid rgba(255,255,255,0.04)',
                    }}>
                      ⚡ API Flows
                    </div>
                    {apiFlows.map(f => <FlowOption key={f.name} f={f} value={value} onSelect={select} />)}
                  </>
                )}
                {subFlows.length > 0 && (
                  <>
                    <div style={{
                      padding: '4px 12px',
                      fontSize: 10,
                      fontWeight: 700,
                      color: 'var(--muted)',
                      textTransform: 'uppercase',
                      letterSpacing: '0.06em',
                      background: 'rgba(255,255,255,0.02)',
                      borderBottom: '1px solid rgba(255,255,255,0.04)',
                    }}>
                      Sub-Flows
                    </div>
                    {subFlows.map(f => <FlowOption key={f.name} f={f} value={value} onSelect={select} />)}
                  </>
                )}
              </>
            )
          })()}
          <div style={{
            padding: '6px 12px',
            fontSize: 10,
            color: 'var(--muted)',
            borderTop: '1px solid rgba(255,255,255,0.06)',
            fontStyle: 'italic',
          }}>
            ⬇ or drag a "My Flows" block from the palette
          </div>
        </div>
      )}
    </div>
  )
}

function FlowOption({
  f, value, onSelect,
}: { f: SavedFlow; value: string; onSelect: (name: string) => void }) {
  return (
    <div
      onClick={() => onSelect(f.name)}
      style={{
        padding: '8px 12px',
        cursor: 'pointer',
        fontSize: 12,
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        background: f.name === value ? 'rgba(87,181,255,0.1)' : 'transparent',
        color: f.name === value ? 'var(--accent)' : 'var(--text)',
        borderBottom: '1px solid rgba(255,255,255,0.04)',
      }}
      onMouseOver={e => { if (f.name !== value) (e.currentTarget as HTMLElement).style.background = 'rgba(255,255,255,0.04)' }}
      onMouseOut={e => { if (f.name !== value) (e.currentTarget as HTMLElement).style.background = 'transparent' }}
    >
      <span style={{ fontWeight: f.name === value ? 600 : 400 }}>{f.name}</span>
      <span style={{ color: 'var(--muted)', fontSize: 10 }}>
        {f.steps.length} step{f.steps.length !== 1 ? 's' : ''}
      </span>
    </div>
  )
}
