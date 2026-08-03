import { useState, FormEvent, useEffect } from 'react'

interface LoginProps {
  onLogin: (user: { username: string; role?: string; mustChangePassword?: boolean }) => void
}

export default function Login({ onLogin }: LoginProps) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError]       = useState('')
  const [loading, setLoading]   = useState(false)
  const [oidcProviders, setOidcProviders] = useState<string[]>([])
  const [loadingOidc, setLoadingOidc] = useState(true)

  useEffect(() => {
    async function loadOidcProviders() {
      setLoadingOidc(true)
      try {
        const res = await fetch('/api/oidc/providers', { credentials: 'include' })
        if (res.ok) {
          const data = await res.json()
          setOidcProviders(data.providers ?? [])
        }
      } catch {
        // OIDC not configured, silently ignore
      } finally {
        setLoadingOidc(false)
      }
    }
    void loadOidcProviders()
  }, [])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!username.trim()) return
    setLoading(true)
    setError('')
    try {
      const res = await fetch('/api/auth/login', {
        method: 'POST',
        credentials: 'include',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ username, password }),
      })
      if (!res.ok) {
        const text = await res.text().catch(() => `HTTP ${res.status}`)
        // Try to parse JSON error message
        try {
          const parsed = JSON.parse(text)
          setError(parsed.error ?? text)
        } catch {
          setError(text || `HTTP ${res.status}`)
        }
        return
      }
      const data = await res.json()
      onLogin({ username: data.username, role: data.role, mustChangePassword: data.must_change_password })
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Network error')
    } finally {
      setLoading(false)
    }
  }

  function handleOidcLogin(provider: string) {
    window.location.href = `/api/oidc/login?provider=${encodeURIComponent(provider)}`
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
        width: 340,
        padding: '36px 32px',
        background: 'var(--surface)',
        borderRadius: 12,
        border: '1px solid var(--border)',
        boxShadow: '0 8px 32px rgba(0,0,0,0.4)',
      }}>
        {/* Logo / title */}
        <div style={{ textAlign: 'center', marginBottom: 28 }}>
          <div style={{ fontSize: 28, marginBottom: 6 }}>⊛</div>
          <div style={{ fontWeight: 700, fontSize: 18, color: 'var(--fg)' }}>RAH Studio</div>
          <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4 }}>Sign in to continue</div>
        </div>

        {/* OIDC Provider buttons */}
        {!loadingOidc && oidcProviders.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginBottom: 20 }}>
            {oidcProviders.map(provider => (
              <button
                key={provider}
                type="button"
                onClick={() => handleOidcLogin(provider)}
                style={{
                  padding: '10px 0',
                  borderRadius: 6,
                  border: '1px solid var(--border)',
                  background: 'var(--step-bg)',
                  color: 'var(--fg)',
                  fontWeight: 600,
                  fontSize: 14,
                  cursor: 'pointer',
                  transition: 'background-color 0.15s',
                }}
                onMouseEnter={e => {
                  const el = e.currentTarget as HTMLButtonElement
                  el.style.background = 'var(--accent)'
                  el.style.color = '#031427'
                }}
                onMouseLeave={e => {
                  const el = e.currentTarget as HTMLButtonElement
                  el.style.background = 'var(--step-bg)'
                  el.style.color = 'var(--fg)'
                }}
              >
                Sign in with {provider.charAt(0).toUpperCase() + provider.slice(1)}
              </button>
            ))}
            <div style={{ textAlign: 'center', color: 'var(--muted)', fontSize: 12, margin: '8px 0' }}>
              or
            </div>
          </div>
        )}

        <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
              Username
            </label>
            <input
              type="text"
              autoComplete="username"
              autoFocus
              value={username}
              onChange={e => setUsername(e.target.value)}
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
              placeholder="admin"
            />
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
              Password
            </label>
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={e => setPassword(e.target.value)}
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
              placeholder="••••••••"
            />
          </div>

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
            disabled={loading || !username.trim()}
            style={{
              marginTop: 6,
              padding: '10px 0',
              borderRadius: 6,
              border: 'none',
              background: 'var(--accent)',
              color: '#fff',
              fontWeight: 700,
              fontSize: 14,
              cursor: loading || !username.trim() ? 'not-allowed' : 'pointer',
              opacity: loading || !username.trim() ? 0.6 : 1,
              transition: 'opacity 0.15s',
            }}
          >
            {loading ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </div>
    </div>
  )
}
