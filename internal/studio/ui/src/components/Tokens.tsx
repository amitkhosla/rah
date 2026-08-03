import { useEffect, useState } from 'react'
import ConfirmDialog from './ConfirmDialog'

// ── Token types ────────────────────────────────────────────────────

interface Token {
  id: string
  name: string
  role: string
  allowed_envs: string[]
  scope?: string
  created_at: string
  expires_at?: string
  created_by: string
}

interface TokenCreateRequest {
  name: string
  role: string
  allowed_envs: string[]
  scope?: string
  expires_in_days?: number
}

interface TokenCreateResponse {
  token: string
  id: string
  name: string
  role: string
  allowed_envs: string[]
  scope?: string
  created_at: string
  expires_at?: string
  created_by: string
}

// ── Main component ────────────────────────────────────────────────

export default function Tokens({ isAdmin }: { isAdmin: boolean }) {
  const [tokens, setTokens] = useState<Token[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<TokenCreateRequest>({
    name: '',
    role: 'viewer',
    allowed_envs: [],
  })
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState('')
  const [msgErr, setMsgErr] = useState(false)
  const [createdToken, setCreatedToken] = useState<{ token: string; name: string } | null>(null)
  const [confirmDialog, setConfirmDialog] = useState<{ title: string; message: string; onConfirm: () => void } | null>(null)

  async function loadTokens() {
    setLoading(true)
    setErr('')
    try {
      const res = await fetch('/api/tokens', { credentials: 'include' })
      if (!res.ok) throw new Error(await res.text())
      const data = await res.json()
      setTokens(data.tokens ?? [])
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to load tokens')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void loadTokens()
  }, [])

  function setF<K extends keyof TokenCreateRequest>(k: K, v: TokenCreateRequest[K]) {
    setForm(f => ({ ...f, [k]: v }))
  }

  async function handleSave() {
    if (!form.name.trim()) {
      setMsgErr(true)
      setMsg('Token name is required')
      return
    }
    if (form.allowed_envs.length === 0) {
      setMsgErr(true)
      setMsg('At least one environment is required')
      return
    }

    setSaving(true)
    setMsg('')
    setMsgErr(false)

    try {
      const res = await fetch('/api/tokens', {
        method: 'POST',
        credentials: 'include',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(form),
      })
      if (!res.ok) throw new Error(await res.text())
      const data: TokenCreateResponse = await res.json()
      setCreatedToken({ token: data.token, name: data.name })
      setForm({ name: '', role: 'viewer', allowed_envs: [] })
      setShowForm(false)
      await loadTokens()
    } catch (e) {
      setMsgErr(true)
      setMsg(e instanceof Error ? e.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  async function handleRevoke(id: string, name: string) {
    setConfirmDialog({
      title: 'Revoke Token',
      message: `Revoke token "${name}"? This action cannot be undone.`,
      onConfirm: async () => {
        try {
          const res = await fetch(`/api/tokens/${encodeURIComponent(id)}`, {
            method: 'DELETE',
            credentials: 'include',
          })
          if (!res.ok) throw new Error(await res.text())
          await loadTokens()
        } catch (e) {
          alert(e instanceof Error ? e.message : 'Revoke failed')
        }
        setConfirmDialog(null)
      },
    })
  }

  function copyToClipboard(text: string) {
    navigator.clipboard.writeText(text).catch(() => {
      alert('Failed to copy to clipboard')
    })
  }

  return (
    <div style={{ padding: '20px 24px' }}>
      <SectionHeader
        title="Machine Tokens"
        count={tokens.length}
        hint="API tokens for authenticating machine-to-machine requests"
        onAdd={() => {
          setForm({ name: '', role: 'viewer', allowed_envs: [] })
          setShowForm(v => !v)
        }}
        addLabel={showForm ? 'Cancel' : '+ Create Token'}
        hideAdd={!isAdmin}
      />
      {err && <p className="status-err" style={{ marginBottom: 12 }}>{err}</p>}

      {showForm && isAdmin && (
        <div className="panel" style={{ marginBottom: 20 }}>
          <div className="panel-header">{form.name || 'New Token'}</div>
          <div className="panel-body">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <Field label="Token Name *" hint="Human-readable identifier">
                <input
                  className="input"
                  value={form.name}
                  placeholder="e.g. ci-deployment"
                  onChange={e => setF('name', e.target.value)}
                />
              </Field>
              <Field label="Role *" hint="Token permission level">
                <select className="input" value={form.role} onChange={e => setF('role', e.target.value as TokenCreateRequest['role'])}>
                  <option value="admin">admin</option>
                  <option value="deployer">deployer</option>
                  <option value="publisher">publisher</option>
                  <option value="reviewer">reviewer</option>
                  <option value="viewer">viewer</option>
                </select>
              </Field>

              <Field label="Allowed Environments *" hint="Comma-separated list (e.g. dev, staging, prod)">
                <input
                  className="input"
                  value={form.allowed_envs.join(', ')}
                  placeholder="dev, staging, prod"
                  onChange={e => setF('allowed_envs', e.target.value.split(',').map(s => s.trim()).filter(Boolean))}
                />
              </Field>
              <Field label="Scope" hint="Optional scope restriction (e.g. flows:read)">
                <input
                  className="input"
                  value={form.scope ?? ''}
                  placeholder="optional scope"
                  onChange={e => setF('scope', e.target.value || undefined)}
                />
              </Field>

              <Field label="Expires In (days)" hint="Leave empty for no expiration">
                <input
                  className="input"
                  type="number"
                  value={form.expires_in_days ?? ''}
                  placeholder="90"
                  onChange={e => setF('expires_in_days', e.target.value ? parseInt(e.target.value) : undefined)}
                />
              </Field>
            </div>

            <div style={{ marginTop: 16, display: 'flex', gap: 10, alignItems: 'center' }}>
              <button className="btn" style={{ width: 'auto', padding: '0 24px' }} onClick={handleSave} disabled={saving}>
                {saving ? 'Creating…' : 'Create Token'}
              </button>
              {msg && <span className={msgErr ? 'status-err' : 'status-ok'}>{msg}</span>}
            </div>
          </div>
        </div>
      )}

      {createdToken && (
        <div className="panel" style={{ marginBottom: 20, background: 'rgba(34, 197, 94, 0.08)', borderColor: 'rgba(34, 197, 94, 0.3)' }}>
          <div className="panel-header" style={{ color: '#22c55e' }}>Token Created Successfully</div>
          <div className="panel-body">
            <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 12 }}>
              Copy your token now. You won't be able to see it again.
            </div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 16 }}>
              <input
                type="text"
                value={createdToken.token}
                readOnly
                style={{
                  flex: 1,
                  padding: '8px 12px',
                  borderRadius: 6,
                  border: '1px solid var(--border)',
                  background: 'var(--bg)',
                  color: 'var(--fg)',
                  fontSize: 12,
                  fontFamily: 'monospace',
                }}
              />
              <button
                className="btn"
                style={{ width: 'auto', padding: '0 16px', marginTop: 0 }}
                onClick={() => copyToClipboard(createdToken.token)}
              >
                Copy
              </button>
            </div>
            <button
              className="btn"
              style={{ width: 'auto', padding: '0 16px' }}
              onClick={() => setCreatedToken(null)}
            >
              Done
            </button>
          </div>
        </div>
      )}

      {loading ? (
        <p className="hint">Loading…</p>
      ) : tokens.length === 0 ? (
        <EmptyState icon="🔑" text="No machine tokens created yet." />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {tokens.map(t => (
            <div key={t.id} className="panel">
              <div style={{ padding: '10px 14px', display: 'flex', alignItems: 'center', gap: 10 }}>
                <span style={{ fontFamily: 'monospace', fontSize: 14, fontWeight: 600 }}>{t.name}</span>
                <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 4, color: '#fff', background: roleColor(t.role), flexShrink: 0 }}>
                  {t.role.toUpperCase()}
                </span>
                <span style={{ color: 'var(--muted)', fontSize: 12 }}>{t.allowed_envs.join(', ')}</span>
                {t.expires_at && (
                  <span style={{ color: 'var(--muted)', fontSize: 11, marginLeft: 'auto' }}>
                    Expires: {new Date(t.expires_at).toLocaleDateString()}
                  </span>
                )}
                <span style={{ color: 'var(--muted)', fontSize: 11 }}>
                  by {t.created_by}
                </span>
                {isAdmin && (
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 12, color: '#ef4444' }}
                    onClick={() => handleRevoke(t.id, t.name)}
                  >
                    Revoke
                  </button>
                )}
              </div>
              {t.scope && (
                <div style={{ borderTop: '1px solid var(--border)', padding: '8px 14px', fontSize: 11, color: 'var(--muted)' }}>
                  Scope: {t.scope}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
      {confirmDialog && (
        <ConfirmDialog
          title={confirmDialog.title}
          message={confirmDialog.message}
          onConfirm={confirmDialog.onConfirm}
          onCancel={() => setConfirmDialog(null)}
        />
      )}
    </div>
  )
}

// ── Shared helpers ────────────────────────────────────────────────

function roleColor(role: string): string {
  const colors: Record<string, string> = {
    admin: '#ef4444',
    deployer: '#3b82f6',
    publisher: '#8b5cf6',
    reviewer: '#f59e0b',
    viewer: '#64748b',
  }
  return colors[role] ?? '#64748b'
}

function SectionHeader({
  title,
  count,
  hint,
  onAdd,
  addLabel,
  hideAdd,
}: {
  title: string
  count: number
  hint: string
  onAdd: () => void
  addLabel: string
  hideAdd?: boolean
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 20 }}>
      <div>
        <span style={{ fontSize: 15, fontWeight: 700 }}>{title}</span>
        <span style={{ color: 'var(--muted)', fontSize: 13, marginLeft: 10 }}>{count} total</span>
        <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>{hint}</div>
      </div>
      {!hideAdd && (
        <button className="btn" style={{ width: 'auto', padding: '0 18px', flexShrink: 0 }} onClick={onAdd}>
          {addLabel}
        </button>
      )}
    </div>
  )
}

function EmptyState({ icon, text }: { icon: string; text: string }) {
  return (
    <div style={{ textAlign: 'center', padding: '40px 0', color: 'var(--muted)' }}>
      <div style={{ fontSize: 32, opacity: 0.3, marginBottom: 12 }}>{icon}</div>
      <p>{text}</p>
    </div>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="field-label">{label}</label>
      {children}
      {hint && <span className="field-desc">{hint}</span>}
    </div>
  )
}
