import { useState } from 'react'
import type { CondConfig, OnMatchConfig, RuleConfig } from '../types'

const card: React.CSSProperties = {
  background: '#181825', border: '1px solid #313244', borderRadius: 6, padding: 12, marginBottom: 8,
}
const sel: React.CSSProperties = {
  background: '#181825', border: '1px solid #313244', color: '#cdd6f4',
  borderRadius: 4, padding: '3px 6px', fontSize: 12,
}
const inp: React.CSSProperties = {
  background: '#181825', border: '1px solid #313244', color: '#cdd6f4',
  borderRadius: 4, padding: '3px 6px', fontSize: 12, minWidth: 80,
}
const btn = (color = '#89b4fa'): React.CSSProperties => ({
  background: 'transparent', border: `1px solid ${color}`, color,
  borderRadius: 4, padding: '2px 8px', cursor: 'pointer', fontSize: 11,
})
const row: React.CSSProperties = { display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap', marginBottom: 4 }

const SOURCES = ['req_body', 'resp_body', 'req_header', 'resp_header', 'slot'] as const
const CHECKS  = ['exists', 'missing', 'eq', 'neq', 'lt', 'gt', 'regex', 'in'] as const
const DESTS   = ['jump', 'fail', 'continue', 'retry', 'default'] as const
const OPS     = ['direct', 'and', 'or'] as const

const NEEDS_VALUE = new Set(['eq', 'neq', 'lt', 'gt', 'regex', 'in'])

function emptyLeaf(): CondConfig {
  return { source: 'req_body', path: '', check: 'exists' }
}
function emptyRule(): RuleConfig {
  return { when: emptyLeaf(), on_match: { dest: 'continue' } }
}

// ── CondLeafEditor ─────────────────────────────────────────────────
function CondLeafEditor({
  cond, onChange,
}: { cond: CondConfig; onChange: (c: CondConfig) => void }) {
  return (
    <div style={row}>
      <select style={sel} value={cond.source ?? 'req_body'} onChange={e => onChange({ ...cond, source: e.target.value as CondConfig['source'] })}>
        {SOURCES.map(s => <option key={s}>{s}</option>)}
      </select>
      <input style={inp} placeholder="path / field" value={cond.path ?? ''} onChange={e => onChange({ ...cond, path: e.target.value })} />
      <select style={sel} value={cond.check ?? 'exists'} onChange={e => onChange({ ...cond, check: e.target.value as CondConfig['check'] })}>
        {CHECKS.map(c => <option key={c}>{c}</option>)}
      </select>
      {NEEDS_VALUE.has(cond.check ?? 'exists') && (
        cond.check === 'in'
          ? <input style={{ ...inp, minWidth: 120 }} placeholder="val1,val2,..." value={(cond.in_values ?? []).join(',')} onChange={e => onChange({ ...cond, in_values: e.target.value ? e.target.value.split(',') : [] })} />
          : (cond.check === 'lt' || cond.check === 'gt')
            ? <input style={{ ...inp, width: 70 }} type="number" placeholder="0" value={cond.value_num ?? ''} onChange={e => onChange({ ...cond, value_num: Number(e.target.value) })} />
            : <input style={inp} placeholder="value" value={cond.value ?? ''} onChange={e => onChange({ ...cond, value: e.target.value })} />
      )}
    </div>
  )
}

// ── CondNodeEditor — recursive ─────────────────────────────────────
function CondNodeEditor({
  cond, onChange, depth = 0,
}: { cond: CondConfig; onChange: (c: CondConfig) => void; depth?: number }) {
  const op = cond.op ?? (cond.children && cond.children.length > 0 ? 'and' : 'direct')
  const isComposite = op === 'and' || op === 'or'

  function setOp(newOp: string) {
    if (newOp === 'direct') {
      onChange({ ...emptyLeaf() })
    } else {
      onChange({ op: newOp as 'and' | 'or', children: cond.children?.length ? cond.children : [emptyLeaf()] })
    }
  }

  function updateChild(idx: number, child: CondConfig) {
    const children = [...(cond.children ?? [])]
    children[idx] = child
    onChange({ ...cond, children })
  }

  function addChild() {
    onChange({ ...cond, children: [...(cond.children ?? []), emptyLeaf()] })
  }

  function removeChild(idx: number) {
    const children = (cond.children ?? []).filter((_, i) => i !== idx)
    onChange({ ...cond, children })
  }

  return (
    <div style={{ paddingLeft: depth > 0 ? 12 : 0, borderLeft: depth > 0 ? '2px solid #313244' : 'none', marginBottom: 4 }}>
      <div style={{ ...row, marginBottom: 6 }}>
        <span style={{ fontSize: 11, color: '#a6adc8' }}>operator</span>
        <select style={sel} value={op} onChange={e => setOp(e.target.value)}>
          {OPS.map(o => <option key={o}>{o}</option>)}
        </select>
        {isComposite && <button style={btn('#a6e3a1')} onClick={addChild}>+ condition</button>}
      </div>

      {isComposite ? (
        (cond.children ?? []).map((child, idx) => (
          <div key={idx} style={{ display: 'flex', gap: 4, alignItems: 'flex-start', marginBottom: 4 }}>
            <div style={{ flex: 1 }}>
              <CondNodeEditor cond={child} onChange={c => updateChild(idx, c)} depth={depth + 1} />
            </div>
            <button style={{ ...btn('#f38ba8'), padding: '1px 5px', marginTop: 2 }} onClick={() => removeChild(idx)}>✕</button>
          </div>
        ))
      ) : (
        <CondLeafEditor cond={cond} onChange={onChange} />
      )}
    </div>
  )
}

// ── OnMatchEditor ──────────────────────────────────────────────────
function OnMatchEditor({
  om, onChange, stepNames,
}: { om: OnMatchConfig; onChange: (m: OnMatchConfig) => void; stepNames: string[] }) {
  return (
    <div style={{ marginTop: 6 }}>
      <div style={row}>
        <span style={{ fontSize: 11, color: '#a6adc8' }}>on match →</span>
        <select style={sel} value={om.dest} onChange={e => onChange({ ...om, dest: e.target.value as OnMatchConfig['dest'] })}>
          {DESTS.map(d => <option key={d}>{d}</option>)}
        </select>
        {om.dest === 'jump' && (
          <select style={sel} value={om.target_step ?? ''} onChange={e => onChange({ ...om, target_step: e.target.value })}>
            <option value="">— pick step —</option>
            {stepNames.map(n => <option key={n}>{n}</option>)}
          </select>
        )}
        {om.dest === 'fail' && (
          <>
            <input style={{ ...inp, width: 60 }} type="number" placeholder="400" value={om.status ?? 400} onChange={e => onChange({ ...om, status: Number(e.target.value) })} />
            <input style={{ ...inp, minWidth: 140 }} placeholder="error message" value={om.message ?? ''} onChange={e => onChange({ ...om, message: e.target.value })} />
          </>
        )}
      </div>
    </div>
  )
}

// ── ValidateRouteBuilder — main export ────────────────────────────
interface Props {
  rules: RuleConfig[]
  defaultNext: string
  stepNames: string[]
  onChange: (rules: RuleConfig[], defaultNext: string) => void
}

export default function ValidateRouteBuilder({ rules, defaultNext, stepNames, onChange }: Props) {
  const [expanded, setExpanded] = useState<Set<number>>(new Set([0]))

  function toggleExpand(i: number) {
    setExpanded(prev => {
      const s = new Set(prev)
      s.has(i) ? s.delete(i) : s.add(i)
      return s
    })
  }

  function updateRule(i: number, rule: RuleConfig) {
    const next = [...rules]
    next[i] = rule
    onChange(next, defaultNext)
  }

  function addRule() {
    const next = [...rules, emptyRule()]
    setExpanded(prev => new Set([...prev, next.length - 1]))
    onChange(next, defaultNext)
  }

  function removeRule(i: number) {
    if (!window.confirm('Remove this rule?')) return
    onChange(rules.filter((_, idx) => idx !== i), defaultNext)
  }

  return (
    <div>
      {/* Default next step */}
      <div style={{ ...row, marginBottom: 10 }}>
        <span style={{ fontSize: 12, color: '#a6adc8', whiteSpace: 'nowrap' }}>Default next:</span>
        <select style={sel} value={defaultNext} onChange={e => onChange(rules, e.target.value)}>
          <option value="">— fall through —</option>
          {stepNames.map(n => <option key={n}>{n}</option>)}
        </select>
      </div>

      {/* Rules */}
      {rules.map((rule, i) => (
        <div key={i} style={card}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6, cursor: 'pointer' }} onClick={() => toggleExpand(i)}>
            <span style={{ fontSize: 12, color: '#cdd6f4', fontWeight: 600 }}>
              {expanded.has(i) ? '▼' : '▶'} Rule {i + 1}{rule.label ? `: ${rule.label}` : ''}
            </span>
            <button style={btn('#f38ba8')} onClick={e => { e.stopPropagation(); removeRule(i) }}>Remove</button>
          </div>
          {expanded.has(i) && (
            <div>
              <div style={{ marginBottom: 4 }}>
                <span style={{ fontSize: 11, color: '#a6adc8' }}>Label (optional)</span>
                <input style={{ ...inp, width: '100%', marginTop: 2 }} placeholder="e.g. check auth scope" value={rule.label ?? ''} onChange={e => updateRule(i, { ...rule, label: e.target.value })} />
              </div>
              <div style={{ marginTop: 8, marginBottom: 4, fontSize: 11, color: '#a6adc8' }}>Condition</div>
              <CondNodeEditor cond={rule.when} onChange={when => updateRule(i, { ...rule, when })} />
              <OnMatchEditor om={rule.on_match} onChange={on_match => updateRule(i, { ...rule, on_match })} stepNames={stepNames} />
            </div>
          )}
        </div>
      ))}

      <button style={btn()} onClick={addRule}>+ Add Rule</button>
    </div>
  )
}
