import { useState } from 'react'
import { getCacheEntry, deleteCacheEntry } from '../api'

type Result =
  | null
  | { kind: 'hit'; value: string }
  | { kind: 'miss' }
  | { kind: 'deleted' }
  | { kind: 'error'; msg: string }

export default function CachePanel() {
  const [alias, setAlias]               = useState('')
  const [key, setKey]                   = useState('')
  const [loading, setLoading]           = useState(false)
  const [result, setResult]             = useState<Result>(null)
  const [pendingConfirm, setPendingConfirm] = useState(false)
  const [inputError, setInputError]     = useState('')

  function validate(): boolean {
    if (!alias.trim() || !key.trim()) {
      setInputError('Both tenant alias and cache key are required.')
      return false
    }
    setInputError('')
    return true
  }

  function resetConfirm() {
    setPendingConfirm(false)
  }

  async function handleLookup() {
    if (!validate()) return
    resetConfirm()
    setResult(null)
    setLoading(true)
    try {
      const r = await getCacheEntry(alias.trim(), key.trim())
      setResult(r.found ? { kind: 'hit', value: r.value } : { kind: 'miss' })
    } catch (e: unknown) {
      setResult({ kind: 'error', msg: e instanceof Error ? e.message : String(e) })
    } finally {
      setLoading(false)
    }
  }

  async function handleInvalidate() {
    if (!validate()) return
    if (!pendingConfirm) {
      setPendingConfirm(true)
      return
    }
    setPendingConfirm(false)
    setResult(null)
    setLoading(true)
    try {
      await deleteCacheEntry(alias.trim(), key.trim())
      setResult({ kind: 'deleted' })
    } catch (e: unknown) {
      setResult({ kind: 'error', msg: e instanceof Error ? e.message : String(e) })
    } finally {
      setLoading(false)
    }
  }

  const resultBox = (() => {
    if (!result) return null
    let border = 'var(--border)'
    let label  = ''
    let body: string | null = null

    if (result.kind === 'hit') {
      border = '#3a7'
      label  = 'Found'
      body   = result.value.length > 500 ? result.value.slice(0, 500) + '…' : result.value
    } else if (result.kind === 'miss') {
      border = 'var(--border)'
      label  = 'Not in cache'
    } else if (result.kind === 'deleted') {
      border = '#3a7'
      label  = 'Entry invalidated'
    } else {
      border = '#c44'
      label  = result.msg
    }

    return (
      <div style={{
        marginTop: 16,
        border: `1px solid ${border}`,
        borderRadius: 6,
        padding: '10px 14px',
        background: 'var(--block-bg)',
        fontSize: 13,
      }}>
        <span style={{ fontWeight: 600, color: border === 'var(--border)' ? 'var(--text-muted)' : border }}>
          {label}
        </span>
        {body !== null && (
          <pre style={{
            margin: '6px 0 0',
            fontFamily: 'monospace',
            fontSize: 12,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
            color: 'var(--text-muted)',
          }}>{body}</pre>
        )}
      </div>
    )
  })()

  return (
    <div style={{ padding: '24px 28px', maxWidth: 600 }}>
      <h2 style={{ marginTop: 0, marginBottom: 20, fontSize: 18 }}>Cache Management</h2>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginBottom: 16 }}>
        <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ width: 100, color: 'var(--text-muted)' }}>Tenant alias</span>
          <input
            value={alias}
            onChange={e => { setAlias(e.target.value); resetConfirm() }}
            placeholder="e.g. acme"
            disabled={loading}
            style={{
              flex: 1, padding: '6px 10px', borderRadius: 4,
              border: '1px solid var(--border)', background: 'var(--block-bg)',
              color: 'inherit', fontSize: 13,
            }}
          />
        </label>
        <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ width: 100, color: 'var(--text-muted)' }}>Cache key</span>
          <input
            value={key}
            onChange={e => { setKey(e.target.value); resetConfirm() }}
            placeholder="e.g. user:42"
            disabled={loading}
            style={{
              flex: 1, padding: '6px 10px', borderRadius: 4,
              border: '1px solid var(--border)', background: 'var(--block-bg)',
              color: 'inherit', fontSize: 13,
            }}
          />
        </label>
      </div>

      {inputError && (
        <div style={{ marginBottom: 12, fontSize: 13, color: '#c44' }}>{inputError}</div>
      )}

      <div style={{ display: 'flex', gap: 10 }}>
        <button className="btn" onClick={handleLookup} disabled={loading}>
          Look up
        </button>
        <button
          className="btn"
          onClick={handleInvalidate}
          disabled={loading}
          style={pendingConfirm ? { background: '#c44', borderColor: '#c44', color: '#fff' } : undefined}
        >
          {pendingConfirm ? 'Confirm invalidate' : 'Invalidate'}
        </button>
      </div>

      {resultBox}
    </div>
  )
}
