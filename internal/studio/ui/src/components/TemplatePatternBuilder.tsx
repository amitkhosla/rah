import { useState, useMemo } from 'react'
import type { TemplatePatternStep } from '../types'

interface TemplatePatternBuilderProps {
  action: 'validate_pattern' | 'extract_pattern'
  step: TemplatePatternStep
  onChange: (s: TemplatePatternStep) => void
}

// ── Pattern segment parser ──────────────────────────────────────────
interface Segment {
  kind: 'literal' | 'capture' | 'slot_ref' | 'wildcard'
  value: string  // for literal: bytes; for capture/slot_ref: variable name; for wildcard: '*'
}

function parsePatternSegments(pattern: string): Segment[] {
  const segments: Segment[] = []
  let i = 0
  let literal = ''

  while (i < pattern.length) {
    const c = pattern[i]

    // Capture group: (name)
    if (c === '(') {
      if (literal) segments.push({ kind: 'literal', value: literal })
      literal = ''
      const start = i + 1
      let end = start
      while (end < pattern.length && pattern[end] !== ')') end++
      if (end < pattern.length) {
        segments.push({ kind: 'capture', value: pattern.slice(start, end) })
        i = end + 1
      } else {
        literal += c
        i++
      }
      continue
    }

    // Slot reference: {name}
    if (c === '{') {
      if (literal) segments.push({ kind: 'literal', value: literal })
      literal = ''
      const start = i + 1
      let end = start
      while (end < pattern.length && pattern[end] !== '}') end++
      if (end < pattern.length) {
        segments.push({ kind: 'slot_ref', value: pattern.slice(start, end) })
        i = end + 1
      } else {
        literal += c
        i++
      }
      continue
    }

    // Wildcard: *
    if (c === '*') {
      if (literal) segments.push({ kind: 'literal', value: literal })
      literal = ''
      segments.push({ kind: 'wildcard', value: '*' })
      i++
      continue
    }

    literal += c
    i++
  }

  if (literal) segments.push({ kind: 'literal', value: literal })
  return segments
}

export default function TemplatePatternBuilder({
  action,
  step,
  onChange,
}: TemplatePatternBuilderProps) {
  const pattern = step.input?.pattern ?? ''
  const source = step.source ?? ''
  const resultVar = step.as ?? ''

  const segments = useMemo(() => parsePatternSegments(pattern), [pattern])

  const handleSourceChange = (newSource: string) => {
    onChange({ ...step, source: newSource })
  }

  const handlePatternChange = (newPattern: string) => {
    onChange({ ...step, input: { pattern: newPattern } })
  }

  const handleResultVarChange = (newVar: string) => {
    onChange({ ...step, as: newVar })
  }

  // Generate code preview
  const codePreview = useMemo(() => {
    if (action === 'validate_pattern') {
      const lhs = resultVar ? `${resultVar} = ` : ''
      return `${lhs}validatePattern(${source}, '${pattern}')`
    } else {
      return `extract(${source}, '${pattern}')`
    }
  }, [action, source, pattern, resultVar])

  return (
    <div style={{
      display: 'flex',
      flexDirection: 'column',
      gap: 16,
      padding: '12px 0',
    }}>
      {/* Source slot input */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <label style={{ fontSize: 12, fontWeight: 500, color: '#888' }}>
          Source Slot
        </label>
        <input
          type="text"
          placeholder="e.g. header.X-My-Header or any variable name"
          value={source}
          onChange={(e) => handleSourceChange(e.target.value)}
          style={{
            padding: '6px 8px',
            fontSize: 13,
            border: '1px solid #ddd',
            borderRadius: 4,
            fontFamily: 'monospace',
          }}
        />
      </div>

      {/* Pattern textarea with live preview */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <label style={{ fontSize: 12, fontWeight: 500, color: '#888' }}>
          Pattern
        </label>
        <textarea
          placeholder="e.g. 'internal_(service)_{env}' or 'Bearer_(token)'"
          value={pattern}
          onChange={(e) => handlePatternChange(e.target.value)}
          style={{
            padding: '8px',
            fontSize: 13,
            border: '1px solid #ddd',
            borderRadius: 4,
            fontFamily: 'monospace',
            minHeight: 80,
            resize: 'vertical',
          }}
        />

        {/* Live segment preview */}
        {segments.length > 0 && (
          <div style={{
            padding: '8px',
            background: '#f5f5f5',
            borderRadius: 4,
            fontSize: 12,
            lineHeight: 1.6,
            color: '#333',
          }}>
            <div style={{ marginBottom: 4, color: '#888', fontSize: 11 }}>
              Pattern segments:
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
              {segments.map((seg, idx) => {
                const bgColor = seg.kind === 'literal' ? '#fff'
                                : seg.kind === 'capture' ? '#dcfce7'
                                : seg.kind === 'slot_ref' ? '#dbeafe'
                                : '#fef08a'
                const textColor = seg.kind === 'literal' ? '#333'
                                  : seg.kind === 'capture' ? '#166534'
                                  : seg.kind === 'slot_ref' ? '#1e40af'
                                  : '#713f12'
                const label = seg.kind === 'literal' ? seg.value
                              : seg.kind === 'capture' ? `(${seg.value})`
                              : seg.kind === 'slot_ref' ? `{${seg.value}}`
                              : '*'
                const title = seg.kind === 'capture' ? `writes to variable '${seg.value}'`
                              : seg.kind === 'slot_ref' ? `reads from '${seg.value}'`
                              : ''

                return (
                  <span
                    key={idx}
                    title={title}
                    style={{
                      background: bgColor,
                      color: textColor,
                      padding: '2px 6px',
                      borderRadius: 3,
                      fontSize: 11,
                      fontFamily: 'monospace',
                      border: `1px solid ${textColor}33`,
                    }}
                  >
                    {label}
                  </span>
                )
              })}
            </div>
          </div>
        )}
      </div>

      {/* Result variable field (only for validate_pattern) */}
      {action === 'validate_pattern' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <label style={{ fontSize: 12, fontWeight: 500, color: '#888' }}>
            Result Variable Name
          </label>
          <input
            type="text"
            placeholder="e.g. isValid"
            value={resultVar}
            onChange={(e) => handleResultVarChange(e.target.value)}
            style={{
              padding: '6px 8px',
              fontSize: 13,
              border: '1px solid #ddd',
              borderRadius: 4,
              fontFamily: 'monospace',
            }}
          />
        </div>
      )}

      {/* Code preview */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <label style={{ fontSize: 12, fontWeight: 500, color: '#888' }}>
          Code Preview
        </label>
        <code
          style={{
            padding: '8px',
            background: '#f5f5f5',
            borderRadius: 4,
            fontSize: 12,
            fontFamily: 'monospace',
            color: '#333',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            border: '1px solid #ddd',
          }}
        >
          {codePreview || '(empty)'}
        </code>
      </div>
    </div>
  )
}
