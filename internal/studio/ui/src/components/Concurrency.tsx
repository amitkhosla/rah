import { useEffect, useState } from 'react'
import { getConcurrencyStatus, patchConcurrencyConfig } from '../api'
import type { ConcurrencyStatus, ConcurrencyPatch } from '../types'

export default function Concurrency() {
  const [status, setStatus] = useState<ConcurrencyStatus | null>(null)
  const [form, setForm] = useState<ConcurrencyPatch>({})
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [formTouched, setFormTouched] = useState(false)

  // Fetch status on mount and every 5 seconds
  useEffect(() => {
    async function fetch() {
      try {
        const s = await getConcurrencyStatus()
        setStatus(s)
        // Sync form from response only if not touched by user
        if (!formTouched) {
          setForm({})
        }
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : String(e))
      }
    }

    fetch()
    const interval = setInterval(fetch, 5000)
    return () => clearInterval(interval)
  }, [formTouched])

  async function handleApply() {
    setSaving(true)
    setError('')
    try {
      const updated = await patchConcurrencyConfig(form)
      setStatus(updated)
      setForm({})
      setFormTouched(false)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  function handleFieldChange(key: keyof ConcurrencyPatch, value: string | number | boolean) {
    setForm(prev => ({ ...prev, [key]: value }))
    setFormTouched(true)
  }

  const statusBox = status ? (
    <div style={{ background: '#1e1e2e', borderRadius: 8, padding: '1rem 1.5rem', marginBottom: '1rem' }}>
      <h3 style={{ margin: '0 0 1rem', fontSize: '0.9rem', color: '#a0a0b0', textTransform: 'uppercase', letterSpacing: '0.05em' }}>Live Status</h3>

      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', fontSize: 13 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ color: '#a0a0b0' }}>Status</span>
          <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontWeight: 600 }}>
            <span style={{ color: status.enabled ? '#3a7' : '#666' }}>
              {status.enabled ? '●' : '○'}
            </span>
            {status.enabled ? 'Enabled' : 'Disabled'}
          </span>
        </div>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ color: '#a0a0b0' }}>Mode</span>
          <span style={{ fontWeight: 600 }}>
            {status.adaptive ? 'Adaptive (AIMD)' : 'Fixed Limit'}
          </span>
        </div>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ color: '#a0a0b0' }}>Current Limit</span>
          <span style={{ fontWeight: 600, fontFamily: 'monospace' }}>
            {status.limit.toLocaleString()}
          </span>
        </div>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ color: '#a0a0b0' }}>Active Requests</span>
          <span style={{ fontWeight: 600, fontFamily: 'monospace' }}>
            {status.active}
          </span>
        </div>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={{ color: '#a0a0b0' }}>Rejected (total)</span>
          <span style={{ fontWeight: 600, fontFamily: 'monospace' }}>
            {status.rejected.toLocaleString()}
          </span>
        </div>
      </div>
    </div>
  ) : null

  const configBox = (
    <div style={{ background: '#1e1e2e', borderRadius: 8, padding: '1rem 1.5rem', marginBottom: '1rem' }}>
      <h3 style={{ margin: '0 0 1rem', fontSize: '0.9rem', color: '#a0a0b0', textTransform: 'uppercase', letterSpacing: '0.05em' }}>Configuration</h3>

      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
        {/* Enabled checkbox */}
        <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
          <input
            type="checkbox"
            checked={form.enabled ?? false}
            onChange={e => handleFieldChange('enabled', e.target.checked)}
            disabled={saving}
            style={{ width: 16, height: 16, cursor: 'pointer' }}
          />
          <span style={{ color: 'inherit' }}>Enabled</span>
        </label>

        {/* Fixed limit mode checkbox - only show if enabled */}
        {form.enabled !== false && (
          <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12, marginLeft: 28 }}>
            <input
              type="checkbox"
              checked={form.disabled ?? false}
              onChange={e => handleFieldChange('disabled', e.target.checked)}
              disabled={saving}
              style={{ width: 16, height: 16, cursor: 'pointer' }}
            />
            <span style={{ color: 'inherit' }}>Fixed limit mode (AIMD off)</span>
          </label>
        )}

        {/* Target Overhead */}
        <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ width: 180, color: '#a0a0b0' }}>Target Overhead (ms)</span>
          <input
            type="number"
            value={form.target_overhead_ms ?? ''}
            onChange={e => handleFieldChange('target_overhead_ms', e.target.value ? Number(e.target.value) : '')}
            placeholder="50"
            disabled={saving}
            style={{
              flex: 1, padding: '6px 10px', borderRadius: 4,
              border: '1px solid var(--border)', background: 'var(--block-bg)',
              color: 'inherit', fontSize: 13,
            }}
          />
        </label>

        {/* Min Limit */}
        <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ width: 180, color: '#a0a0b0' }}>Min Limit</span>
          <input
            type="number"
            value={form.min_limit ?? ''}
            onChange={e => handleFieldChange('min_limit', e.target.value ? Number(e.target.value) : '')}
            placeholder="2000"
            disabled={saving}
            style={{
              flex: 1, padding: '6px 10px', borderRadius: 4,
              border: '1px solid var(--border)', background: 'var(--block-bg)',
              color: 'inherit', fontSize: 13,
            }}
          />
        </label>

        {/* Max Limit */}
        <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ width: 180, color: '#a0a0b0' }}>Max Limit</span>
          <input
            type="number"
            value={form.max_limit ?? ''}
            onChange={e => handleFieldChange('max_limit', e.target.value ? Number(e.target.value) : '')}
            placeholder="16000"
            disabled={saving}
            style={{
              flex: 1, padding: '6px 10px', borderRadius: 4,
              border: '1px solid var(--border)', background: 'var(--block-bg)',
              color: 'inherit', fontSize: 13,
            }}
          />
        </label>

        {/* Add Step */}
        <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ width: 180, color: '#a0a0b0' }}>Add Step</span>
          <input
            type="number"
            value={form.add_step ?? ''}
            onChange={e => handleFieldChange('add_step', e.target.value ? Number(e.target.value) : '')}
            placeholder="50"
            disabled={saving}
            style={{
              flex: 1, padding: '6px 10px', borderRadius: 4,
              border: '1px solid var(--border)', background: 'var(--block-bg)',
              color: 'inherit', fontSize: 13,
            }}
          />
        </label>

        {/* Cut Factor */}
        <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
          <span style={{ width: 180, color: '#a0a0b0' }}>Cut Factor</span>
          <input
            type="number"
            step="0.01"
            min="0.1"
            max="0.99"
            value={form.cut_factor ?? ''}
            onChange={e => handleFieldChange('cut_factor', e.target.value ? Number(e.target.value) : '')}
            placeholder="0.85"
            disabled={saving}
            style={{
              flex: 1, padding: '6px 10px', borderRadius: 4,
              border: '1px solid var(--border)', background: 'var(--block-bg)',
              color: 'inherit', fontSize: 13,
            }}
          />
        </label>

        {/* Manual Limit Override - only show if Fixed limit mode is checked */}
        {form.disabled && (
          <label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 12 }}>
            <span style={{ width: 180, color: '#a0a0b0' }}>Manual Limit Override</span>
            <input
              type="number"
              value={form.limit ?? ''}
              onChange={e => handleFieldChange('limit', e.target.value ? Number(e.target.value) : '')}
              placeholder="leave blank for AIMD control"
              disabled={saving}
              style={{
                flex: 1, padding: '6px 10px', borderRadius: 4,
                border: '1px solid var(--border)', background: 'var(--block-bg)',
                color: 'inherit', fontSize: 13,
              }}
            />
          </label>
        )}
      </div>

      {/* Apply button and error */}
      <div style={{ marginTop: '1rem', display: 'flex', alignItems: 'center', gap: 10 }}>
        <button
          className="btn"
          onClick={handleApply}
          disabled={saving}
        >
          {saving ? 'Applying...' : 'Apply'}
        </button>
        {error && (
          <span style={{ fontSize: 13, color: '#c44' }}>{error}</span>
        )}
      </div>
    </div>
  )

  return (
    <div style={{ padding: '1.5rem', maxWidth: 700 }}>
      <h2 style={{ marginTop: 0, marginBottom: 20, fontSize: 18 }}>Concurrency Control</h2>
      {statusBox}
      {configBox}
    </div>
  )
}
