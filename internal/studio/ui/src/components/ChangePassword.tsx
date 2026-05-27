import { useState, FormEvent } from 'react'

interface ChangePasswordProps {
  username: string
  onChanged: () => void // called after successful change; App re-fetches /me
}

export default function ChangePassword({ username, onChanged }: ChangePasswordProps) {
  const [current,  setCurrent]  = useState('')
  const [next,     setNext]     = useState('')
  const [confirm,  setConfirm]  = useState('')
  const [error,    setError]    = useState('')
  const [loading,  setLoading]  = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (next !== confirm) {
      setError('New passwords do not match')
      return
    }
    if (next.length < 8) {
      setError('New password must be at least 8 characters')
      return
    }
    setLoading(true)
    try {
      const res = await fetch('/api/auth/change-password', {
        method: 'POST',
        credentials: 'include',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ current_password: current, new_password: next }),
      })
      if (!res.ok) {
        const text = await res.text().catch(() => `HTTP ${res.status}`)
        try { setError(JSON.parse(text).error ?? text) } catch { setError(text) }
        return
      }
      onChanged()
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Network error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      height: '100vh',
      background: 'var(--bg)',
    }}>
      <div style={{
        width: 360,
        padding: '36px 32px',
        background: 'var(--surface)',
        borderRadius: 12,
        border: '1px solid var(--border)',
        boxShadow: '0 8px 32px rgba(0,0,0,0.4)',
      }}>
        <div style={{ marginBottom: 24 }}>
          <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--fg)', marginBottom: 6 }}>
            Change your password
          </div>
          <div style={{ fontSize: 12, color: 'var(--muted)' }}>
            You're signed in as <strong>{username}</strong>. A new password is required before continuing.
          </div>
        </div>

        <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          {[
            { label: 'Current password', value: current, onChange: setCurrent, complete: 'current-password' },
            { label: 'New password',     value: next,    onChange: setNext,    complete: 'new-password' },
            { label: 'Confirm new password', value: confirm, onChange: setConfirm, complete: 'new-password' },
          ].map(({ label, value, onChange, complete }) => (
            <div key={label} style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                {label}
              </label>
              <input
                type="password"
                autoComplete={complete}
                value={value}
                onChange={e => onChange(e.target.value)}
                disabled={loading}
                style={{
                  padding: '8px 12px',
                  borderRadius: 6,
                  border: '1px solid var(--border)',
                  background: 'var(--bg)',
                  color: 'var(--fg)',
                  fontSize: 14,
                  outline: 'none',
                }}
              />
            </div>
          ))}

          {error && (
            <div style={{
              padding: '8px 12px',
              borderRadius: 6,
              background: 'rgba(239,68,68,0.12)',
              border: '1px solid rgba(239,68,68,0.3)',
              color: '#ef4444',
              fontSize: 12,
            }}>
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={loading || !current || !next || !confirm}
            style={{
              marginTop: 6,
              padding: '10px 0',
              borderRadius: 6,
              border: 'none',
              background: 'var(--accent)',
              color: '#fff',
              fontWeight: 700,
              fontSize: 14,
              cursor: loading || !current || !next || !confirm ? 'not-allowed' : 'pointer',
              opacity: loading || !current || !next || !confirm ? 0.6 : 1,
            }}
          >
            {loading ? 'Saving…' : 'Set new password'}
          </button>
        </form>
      </div>
    </div>
  )
}
