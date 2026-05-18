/**
 * PatternConditionBuilder.tsx
 *
 * Visual form for building PatternCondition objects.
 * Used inside FlowDesigner to create/edit pattern-match conditions on `if` steps.
 */

import { useState, useEffect, useCallback } from 'react'
import type { PatternCondition } from '../types'

export interface PatternConditionBuilderProps {
  condition: PatternCondition | null
  onChange: (condition: PatternCondition) => void
  onClose?: () => void
}

// ── Source options ──────────────────────────────────────────────────────────
type SourceKind = PatternCondition['source']
type StrategyKind = NonNullable<PatternCondition['strategy']>

const SOURCE_OPTIONS: Array<{ value: SourceKind; label: string; needsKey: boolean }> = [
  { value: 'header', label: 'Header',      needsKey: true  },
  { value: 'query',  label: 'Query Param', needsKey: true  },
  { value: 'body',   label: 'Body',        needsKey: false },
  { value: 'path',   label: 'Path',        needsKey: false },
]

const STRATEGY_OPTIONS: Array<{ value: StrategyKind; label: string; hint: string }> = [
  { value: 'auto',       label: 'Auto',       hint: 'Detect strategy from pattern syntax' },
  { value: 'regex',      label: 'Regex',      hint: 'Full regular expression match'       },
  { value: 'exact',      label: 'Exact',      hint: 'Exact string equality'               },
  { value: 'prefix',     label: 'Prefix',     hint: 'Value starts with pattern'           },
  { value: 'suffix',     label: 'Suffix',     hint: 'Value ends with pattern'             },
  { value: 'contains',   label: 'Contains',   hint: 'Value contains pattern substring'    },
  { value: 'sequential', label: 'Sequential', hint: 'Try strategies in order'             },
]

// ── Flag definitions ────────────────────────────────────────────────────────
const FLAG_DEFS: Array<{ flag: string; label: string; description: string }> = [
  { flag: 'i', label: 'i', description: 'Case insensitive'           },
  { flag: 'm', label: 'm', description: 'Multiline (^ and $ per line)' },
  { flag: 's', label: 's', description: 'Dot matches newlines'        },
  { flag: 'x', label: 'x', description: 'Extended (ignore whitespace)' },
]

// Strategies that support regex flags
const REGEX_STRATEGIES = new Set<StrategyKind>(['auto', 'regex', 'sequential'])

// ── Default empty condition ─────────────────────────────────────────────────
function defaultCondition(): PatternCondition {
  return {
    type: 'pattern_match',
    source: 'header',
    sourceKey: '',
    pattern: '',
    strategy: 'auto',
    flags: '',
  }
}

// ── Helpers ─────────────────────────────────────────────────────────────────
function toggleFlag(current: string, flag: string): string {
  return current.includes(flag)
    ? current.replace(flag, '')
    : current + flag
}

function validatePattern(pattern: string): string | null {
  if (!pattern) return null
  try {
    new RegExp(pattern)
    return null
  } catch (e) {
    return e instanceof Error ? e.message : 'Invalid pattern'
  }
}

// ── Component ───────────────────────────────────────────────────────────────
export default function PatternConditionBuilder({
  condition,
  onChange,
  onClose,
}: PatternConditionBuilderProps) {
  // ── Local form state ──────────────────────────────────────────────────────
  const [local, setLocal] = useState<PatternCondition>(() =>
    condition ?? defaultCondition()
  )
  const [patternError, setPatternError] = useState<string | null>(null)

  // Sync from props when condition changes externally
  useEffect(() => {
    setLocal(condition ?? defaultCondition())
    setPatternError(null)
  }, [condition])

  // ── Field updaters ────────────────────────────────────────────────────────
  const setSource = useCallback((source: SourceKind) => {
    const needsKey = SOURCE_OPTIONS.find(o => o.value === source)?.needsKey ?? false
    setLocal(prev => ({
      ...prev,
      source,
      // Clear sourceKey when switching to a source that doesn't need it
      sourceKey: needsKey ? prev.sourceKey : '',
    }))
  }, [])

  const setSourceKey = useCallback((sourceKey: string) => {
    setLocal(prev => ({ ...prev, sourceKey }))
  }, [])

  const setPattern = useCallback((pattern: string) => {
    setPatternError(validatePattern(pattern))
    setLocal(prev => ({ ...prev, pattern }))
  }, [])

  const setStrategy = useCallback((strategy: StrategyKind) => {
    setLocal(prev => ({
      ...prev,
      strategy,
      // Clear flags when switching to non-regex strategy
      flags: REGEX_STRATEGIES.has(strategy) ? prev.flags : '',
    }))
  }, [])

  const toggleFlagCallback = useCallback((flag: string) => {
    setLocal(prev => ({
      ...prev,
      flags: toggleFlag(prev.flags ?? '', flag),
    }))
  }, [])

  // ── Actions ───────────────────────────────────────────────────────────────
  function handleSave() {
    if (patternError) return
    onChange({ ...local })
    onClose?.()
  }

  function handleCancel() {
    onClose?.()
  }

  // ── Derived ───────────────────────────────────────────────────────────────
  const strategy = local.strategy ?? 'auto'
  const showFlags = REGEX_STRATEGIES.has(strategy)
  const flags = local.flags ?? ''
  const sourceNeedsKey = SOURCE_OPTIONS.find(o => o.value === local.source)?.needsKey ?? false
  const strategyHint = STRATEGY_OPTIONS.find(o => o.value === strategy)?.hint ?? ''

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <div style={styles.overlay} onClick={e => { if (e.target === e.currentTarget) handleCancel() }}>
      <div style={styles.panel}>
        {/* Header */}
        <div style={styles.header}>
          <span style={styles.title}>Pattern Condition Builder</span>
          <button
            style={styles.closeBtn}
            onClick={handleCancel}
            title="Close"
            aria-label="Close"
          >×</button>
        </div>

        {/* Body */}
        <div style={styles.body}>

          {/* ── Source row ── */}
          <div style={styles.fieldRow}>
            <label style={styles.label}>Source</label>
            <div style={styles.row}>
              <select
                style={{ ...styles.input, ...styles.selectNarrow }}
                value={local.source}
                onChange={e => setSource(e.target.value as SourceKind)}
              >
                {SOURCE_OPTIONS.map(o => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
              {sourceNeedsKey && (
                <input
                  style={{ ...styles.input, flex: 1 }}
                  type="text"
                  value={local.sourceKey ?? ''}
                  placeholder={local.source === 'header' ? 'x-service' : 'param-name'}
                  onChange={e => setSourceKey(e.target.value)}
                />
              )}
              {!sourceNeedsKey && (
                <span style={styles.hint}>
                  {local.source === 'path' ? 'Matches on full request path' : 'Matches on raw request body'}
                </span>
              )}
            </div>
          </div>

          {/* ── Pattern input ── */}
          <div style={styles.fieldRow}>
            <label style={styles.label}>Pattern</label>
            <input
              style={{
                ...styles.input,
                borderColor: patternError ? '#e87070' : undefined,
              }}
              type="text"
              value={local.pattern}
              placeholder="e.g., ^(api|data).*"
              onChange={e => setPattern(e.target.value)}
              spellCheck={false}
            />
            {patternError && (
              <span style={styles.error}>{patternError}</span>
            )}
            {!patternError && local.pattern && (
              <span style={styles.ok}>Pattern is valid</span>
            )}
          </div>

          {/* ── Strategy selector ── */}
          <div style={styles.fieldRow}>
            <label style={styles.label}>Strategy</label>
            <div style={styles.row}>
              <select
                style={{ ...styles.input, ...styles.selectNarrow }}
                value={strategy}
                onChange={e => setStrategy(e.target.value as StrategyKind)}
              >
                {STRATEGY_OPTIONS.map(o => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
              {strategyHint && (
                <span style={styles.hint}>{strategyHint}</span>
              )}
            </div>
          </div>

          {/* ── Regex flags ── */}
          {showFlags && (
            <div style={styles.fieldRow}>
              <label style={styles.label}>Regex Flags</label>
              <div style={styles.flagsGrid}>
                {FLAG_DEFS.map(def => {
                  const checked = flags.includes(def.flag)
                  return (
                    <label key={def.flag} style={styles.flagRow}>
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleFlagCallback(def.flag)}
                        style={styles.checkbox}
                      />
                      <span style={styles.flagCode}>{def.flag}</span>
                      <span style={styles.flagDesc}>{def.description}</span>
                    </label>
                  )
                })}
              </div>
            </div>
          )}

          {/* ── Preview ── */}
          {local.pattern && (
            <div style={styles.fieldRow}>
              <label style={styles.label}>Preview</label>
              <code style={styles.preview}>
                {local.source}
                {local.sourceKey ? `[${local.sourceKey}]` : ''}
                {' matches '}
                <span style={{ color: '#57b5ff' }}>{local.pattern}</span>
                {flags ? ` (flags: ${flags})` : ''}
                {strategy !== 'auto' ? ` [${strategy}]` : ''}
              </code>
            </div>
          )}

        </div>

        {/* Footer */}
        <div style={styles.footer}>
          <button
            style={{
              ...styles.btn,
              ...(patternError || !local.pattern ? styles.btnDisabled : {}),
            }}
            onClick={handleSave}
            disabled={!!patternError || !local.pattern}
          >
            Save Condition
          </button>
          <button
            style={{ ...styles.btn, ...styles.btnMuted }}
            onClick={handleCancel}
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}

// ── Inline styles (no CSS file dependency) ──────────────────────────────────
// Mirrors the existing dark-blue palette from index.css
const styles: Record<string, React.CSSProperties> = {
  overlay: {
    position: 'fixed',
    inset: 0,
    background: 'rgba(0, 0, 0, 0.6)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 1000,
  },
  panel: {
    background: '#111a2d',
    border: '1px solid #38568c',
    borderRadius: 12,
    width: 420,
    maxWidth: '94vw',
    display: 'flex',
    flexDirection: 'column',
    boxShadow: '0 8px 32px rgba(0,0,0,0.55)',
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    padding: '11px 14px',
    borderBottom: '1px solid #2a3a63',
    flexShrink: 0,
  },
  title: {
    fontWeight: 600,
    fontSize: 13,
    color: '#ecf0f9',
  },
  closeBtn: {
    background: 'none',
    border: 'none',
    color: '#93a1bf',
    fontSize: 18,
    cursor: 'pointer',
    lineHeight: 1,
    padding: '0 2px',
  },
  body: {
    padding: '14px',
    display: 'flex',
    flexDirection: 'column',
    gap: 12,
  },
  footer: {
    padding: '10px 14px',
    borderTop: '1px solid #2a3a63',
    display: 'flex',
    gap: 8,
    flexShrink: 0,
  },
  fieldRow: {
    display: 'flex',
    flexDirection: 'column',
    gap: 5,
  },
  label: {
    fontSize: 12,
    fontWeight: 600,
    color: '#93a1bf',
    textTransform: 'uppercase' as const,
    letterSpacing: '0.04em',
  },
  row: {
    display: 'flex',
    gap: 8,
    alignItems: 'center',
  },
  input: {
    width: '100%',
    padding: '7px 9px',
    borderRadius: 8,
    border: '1px solid #38568c',
    background: '#0f1b31',
    color: '#ecf0f9',
    fontSize: 13,
    outline: 'none',
    boxSizing: 'border-box' as const,
    fontFamily: 'inherit',
    transition: 'border-color 0.15s',
  },
  selectNarrow: {
    width: 130,
    flexShrink: 0,
    cursor: 'pointer',
  },
  hint: {
    fontSize: 12,
    color: '#93a1bf',
    flex: 1,
  },
  error: {
    fontSize: 11,
    color: '#e87070',
    marginTop: 2,
  },
  ok: {
    fontSize: 11,
    color: '#5fd68e',
    marginTop: 2,
  },
  flagsGrid: {
    display: 'flex',
    flexDirection: 'column' as const,
    gap: 7,
  },
  flagRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    cursor: 'pointer',
    fontSize: 13,
    color: '#ecf0f9',
  },
  checkbox: {
    accentColor: '#57b5ff',
    width: 14,
    height: 14,
    cursor: 'pointer',
    flexShrink: 0,
  },
  flagCode: {
    fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
    fontSize: 12,
    color: '#57b5ff',
    width: 14,
  },
  flagDesc: {
    color: '#93a1bf',
    fontSize: 12,
  },
  preview: {
    display: 'block',
    background: '#0b1220',
    border: '1px solid #22355d',
    borderRadius: 6,
    padding: '7px 10px',
    fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
    fontSize: 12,
    color: '#ecf0f9',
    wordBreak: 'break-all' as const,
  },
  btn: {
    flex: 1,
    padding: '8px',
    borderRadius: 8,
    border: 'none',
    background: '#57b5ff',
    color: '#031427',
    fontWeight: 700,
    fontSize: 13,
    cursor: 'pointer',
    transition: 'opacity 0.15s',
  },
  btnMuted: {
    background: '#27406b',
    color: '#fff',
  },
  btnDisabled: {
    opacity: 0.45,
    cursor: 'not-allowed',
  },
}
