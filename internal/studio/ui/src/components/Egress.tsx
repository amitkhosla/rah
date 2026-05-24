import { useEffect, useState, useCallback } from 'react'
import type { EgressProfileConfig, EgressCodeRuleConfig, EgressPatternRuleConfig } from '../types'
import {
  listEgressProfiles, upsertEgressProfile, deleteEgressProfile,
  listCodeRules, upsertCodeRule, deleteCodeRule,
  listPatternRules, upsertPatternRule, deletePatternRule,
} from '../api'

// ── Profiles tab ──────────────────────────────────────────────────────────────

function ProfilesTab() {
  const [profiles, setProfiles] = useState<EgressProfileConfig[]>([])
  const [loading, setLoading]   = useState(true)
  const [err, setErr]           = useState('')

  // form state
  const [name, setName]               = useState('')
  const [type, setType]               = useState('auto')
  const [tlsSkip, setTlsSkip]         = useState(false)
  const [dialMs, setDialMs]           = useState('')
  const [reqMs, setReqMs]             = useState('')
  const [saving, setSaving]           = useState(false)
  const [saveErr, setSaveErr]         = useState('')

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listEgressProfiles()
      .then(r => setProfiles(r ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  async function handleSave() {
    const n = name.trim()
    if (!n) { setSaveErr('Name required'); return }
    setSaving(true); setSaveErr('')
    const profile: EgressProfileConfig = {
      name: n,
      type,
      tls_skip_verify: type === 'https' ? tlsSkip : undefined,
      dial_timeout_ms: dialMs ? parseInt(dialMs) : undefined,
      req_timeout_ms:  reqMs  ? parseInt(reqMs)  : undefined,
    }
    try {
      await upsertEgressProfile(profile)
      setName(''); setType('auto'); setTlsSkip(false); setDialMs(''); setReqMs('')
      load()
    } catch (e) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  async function handleDelete(profileName: string) {
    if (!window.confirm(`Delete profile "${profileName}"?`)) return
    try {
      await deleteEgressProfile(profileName)
      load()
    } catch (e) { setErr(String(e)) }
  }

  return (
    <div style={{ padding: 20 }}>
      <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 16 }}>Egress Profiles</div>

      {loading && <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 12 }}>Loading…</div>}
      {err && <div style={{ color: '#f87171', fontSize: 13, marginBottom: 12 }}>{err}</div>}

      {profiles.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13, marginBottom: 24 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)', color: 'var(--text-muted)' }}>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Name</th>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Type</th>
              <th style={{ textAlign: 'center', padding: '4px 8px' }}>Skip TLS</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Dial ms</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Req ms</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {profiles.map(p => (
              <tr key={p.name} style={{ borderBottom: '1px solid var(--border)' }}>
                <td style={{ padding: '6px 8px', fontWeight: 600 }}>{p.name}</td>
                <td style={{ padding: '6px 8px' }}>
                  <span style={{
                    background: 'var(--block-bg)', border: '1px solid var(--border)',
                    borderRadius: 4, padding: '2px 8px', fontSize: 12,
                  }}>{p.type}</span>
                </td>
                <td style={{ padding: '6px 8px', textAlign: 'center' }}>
                  {p.tls_skip_verify ? '✓' : '—'}
                </td>
                <td style={{ padding: '6px 8px', textAlign: 'right' }}>{p.dial_timeout_ms ?? '—'}</td>
                <td style={{ padding: '6px 8px', textAlign: 'right' }}>{p.req_timeout_ms ?? '—'}</td>
                <td style={{ padding: '6px 8px', textAlign: 'right' }}>
                  <button
                    className="btn"
                    style={{ fontSize: 11, background: '#ef4444' }}
                    onClick={() => handleDelete(p.name)}
                  >Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {!loading && profiles.length === 0 && !err && (
        <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 16 }}>No profiles configured.</div>
      )}

      <div style={{ fontWeight: 600, marginBottom: 10, fontSize: 14 }}>Add / Update Profile</div>
      <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', gap: 8, marginBottom: 8 }}>
        <input
          className="input"
          placeholder="Profile name"
          value={name}
          onChange={e => { setName(e.target.value); setSaveErr('') }}
        />
        <select
          className="input"
          value={type}
          onChange={(e: React.ChangeEvent<HTMLSelectElement>) => setType(e.target.value)}
        >
          <option value="auto">auto</option>
          <option value="http1">http1</option>
          <option value="https">https</option>
          <option value="h2c">h2c</option>
        </select>
        <input
          className="input"
          type="number"
          placeholder="Dial timeout ms"
          value={dialMs}
          onChange={e => setDialMs(e.target.value)}
        />
        <input
          className="input"
          type="number"
          placeholder="Req timeout ms"
          value={reqMs}
          onChange={e => setReqMs(e.target.value)}
        />
      </div>
      {type === 'https' && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, fontSize: 13 }}>
          <input
            type="checkbox"
            id="tls-skip"
            checked={tlsSkip}
            onChange={e => setTlsSkip(e.target.checked)}
          />
          <label htmlFor="tls-skip" style={{ color: 'var(--text-muted)', cursor: 'pointer' }}>
            Skip TLS verification
          </label>
        </div>
      )}
      {saveErr && <div style={{ color: '#f87171', fontSize: 12, marginBottom: 6 }}>{saveErr}</div>}
      <button className="btn" onClick={handleSave} disabled={saving}>
        {saving ? 'Saving…' : 'Save Profile'}
      </button>
    </div>
  )
}

// ── Code Rules tab ────────────────────────────────────────────────────────────

function CodeRulesTab() {
  const [rules, setRules]   = useState<EgressCodeRuleConfig[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr]         = useState('')

  // form state
  const [code, setCode]       = useState('')
  const [profile, setProfile] = useState('')
  const [saving, setSaving]   = useState(false)
  const [saveErr, setSaveErr] = useState('')

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listCodeRules()
      .then(r => setRules(r ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  async function handleAdd() {
    const c = code.trim()
    const p = profile.trim()
    if (!c) { setSaveErr('Service code required'); return }
    if (!p) { setSaveErr('Profile required'); return }
    setSaving(true); setSaveErr('')
    try {
      await upsertCodeRule({ service_code: c, profile: p })
      setCode(''); setProfile('')
      load()
    } catch (e) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  async function handleDelete(serviceCode: string) {
    if (!window.confirm(`Delete code rule for "${serviceCode}"?`)) return
    try {
      await deleteCodeRule(serviceCode)
      load()
    } catch (e) { setErr(String(e)) }
  }

  return (
    <div style={{ padding: 20 }}>
      <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 16 }}>Code Rules</div>

      {loading && <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 12 }}>Loading…</div>}
      {err && <div style={{ color: '#f87171', fontSize: 13, marginBottom: 12 }}>{err}</div>}

      {rules.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13, marginBottom: 24 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)', color: 'var(--text-muted)' }}>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Service Code</th>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Profile</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {rules.map(r => (
              <tr key={r.service_code} style={{ borderBottom: '1px solid var(--border)' }}>
                <td style={{ padding: '6px 8px', fontFamily: 'monospace', fontSize: 12 }}>{r.service_code}</td>
                <td style={{ padding: '6px 8px' }}>
                  <span style={{
                    background: 'var(--block-bg)', border: '1px solid var(--border)',
                    borderRadius: 4, padding: '2px 8px', fontSize: 12,
                  }}>{r.profile}</span>
                </td>
                <td style={{ padding: '6px 8px', textAlign: 'right' }}>
                  <button
                    className="btn"
                    style={{ fontSize: 11, background: '#ef4444' }}
                    onClick={() => handleDelete(r.service_code)}
                  >Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {!loading && rules.length === 0 && !err && (
        <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 16 }}>No code rules configured.</div>
      )}

      <div style={{ fontWeight: 600, marginBottom: 10, fontSize: 14 }}>Add / Update Rule</div>
      <div style={{ display: 'flex', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <input
          className="input"
          style={{ flex: '1 1 180px' }}
          placeholder="Service code (e.g. llm:openai)"
          value={code}
          onChange={e => { setCode(e.target.value); setSaveErr('') }}
          onKeyDown={e => e.key === 'Enter' && handleAdd()}
        />
        <input
          className="input"
          style={{ flex: '1 1 180px' }}
          placeholder="Profile name"
          value={profile}
          onChange={e => { setProfile(e.target.value); setSaveErr('') }}
          onKeyDown={e => e.key === 'Enter' && handleAdd()}
        />
        <button className="btn" onClick={handleAdd} disabled={saving} style={{ whiteSpace: 'nowrap' }}>
          {saving ? 'Saving…' : 'Add Rule'}
        </button>
      </div>
      {saveErr && <div style={{ color: '#f87171', fontSize: 12 }}>{saveErr}</div>}
    </div>
  )
}

// ── Pattern Rules tab ─────────────────────────────────────────────────────────

function PatternRulesTab() {
  const [rules, setRules]     = useState<EgressPatternRuleConfig[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr]         = useState('')

  // form state
  const [pattern, setPattern] = useState('')
  const [profile, setProfile] = useState('')
  const [saving, setSaving]   = useState(false)
  const [saveErr, setSaveErr] = useState('')

  const load = useCallback(() => {
    setLoading(true); setErr('')
    listPatternRules()
      .then(r => setRules(r ?? []))
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  async function handleAdd() {
    const pa = pattern.trim()
    const pr = profile.trim()
    if (!pa) { setSaveErr('Pattern required'); return }
    if (!pr) { setSaveErr('Profile required'); return }
    setSaving(true); setSaveErr('')
    try {
      await upsertPatternRule({ pattern: pa, profile: pr })
      setPattern(''); setProfile('')
      load()
    } catch (e) { setSaveErr(String(e)) }
    finally { setSaving(false) }
  }

  async function handleDelete(index: number, pat: string) {
    if (!window.confirm(`Delete pattern rule "${pat}"?`)) return
    const idx = String(index).padStart(6, '0')
    try {
      await deletePatternRule(idx)
      load()
    } catch (e) { setErr(String(e)) }
  }

  return (
    <div style={{ padding: 20 }}>
      <div style={{ fontWeight: 700, fontSize: 16, marginBottom: 16 }}>Pattern Rules</div>

      {loading && <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 12 }}>Loading…</div>}
      {err && <div style={{ color: '#f87171', fontSize: 13, marginBottom: 12 }}>{err}</div>}

      {rules.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13, marginBottom: 24 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid var(--border)', color: 'var(--text-muted)' }}>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>#</th>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Pattern</th>
              <th style={{ textAlign: 'left', padding: '4px 8px' }}>Profile</th>
              <th style={{ textAlign: 'right', padding: '4px 8px' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {rules.map((r, i) => (
              <tr key={i} style={{ borderBottom: '1px solid var(--border)' }}>
                <td style={{ padding: '6px 8px', color: 'var(--text-muted)', fontSize: 11 }}>
                  {String(i).padStart(6, '0')}
                </td>
                <td style={{ padding: '6px 8px', fontFamily: 'monospace', fontSize: 12, wordBreak: 'break-all' }}>
                  {r.pattern}
                </td>
                <td style={{ padding: '6px 8px' }}>
                  <span style={{
                    background: 'var(--block-bg)', border: '1px solid var(--border)',
                    borderRadius: 4, padding: '2px 8px', fontSize: 12,
                  }}>{r.profile}</span>
                </td>
                <td style={{ padding: '6px 8px', textAlign: 'right' }}>
                  <button
                    className="btn"
                    style={{ fontSize: 11, background: '#ef4444' }}
                    onClick={() => handleDelete(i, r.pattern)}
                  >Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {!loading && rules.length === 0 && !err && (
        <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 16 }}>No pattern rules configured.</div>
      )}

      <div style={{ fontWeight: 600, marginBottom: 10, fontSize: 14 }}>Add Rule</div>
      <div style={{ display: 'flex', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <input
          className="input"
          style={{ flex: '2 1 240px' }}
          placeholder="URL pattern (e.g. https://api.openai.com/*)"
          value={pattern}
          onChange={e => { setPattern(e.target.value); setSaveErr('') }}
          onKeyDown={e => e.key === 'Enter' && handleAdd()}
        />
        <input
          className="input"
          style={{ flex: '1 1 180px' }}
          placeholder="Profile name"
          value={profile}
          onChange={e => { setProfile(e.target.value); setSaveErr('') }}
          onKeyDown={e => e.key === 'Enter' && handleAdd()}
        />
        <button className="btn" onClick={handleAdd} disabled={saving} style={{ whiteSpace: 'nowrap' }}>
          {saving ? 'Saving…' : 'Add Rule'}
        </button>
      </div>
      {saveErr && <div style={{ color: '#f87171', fontSize: 12 }}>{saveErr}</div>}
    </div>
  )
}

// ── Main Egress component ─────────────────────────────────────────────────────

type EgressTab = 'profiles' | 'codes' | 'patterns'

export default function Egress() {
  const [tab, setTab] = useState<EgressTab>('profiles')

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      {/* Tab bar */}
      <div style={{
        display: 'flex', gap: 4,
        padding: '10px 16px 0',
        borderBottom: '1px solid var(--border)',
        flexShrink: 0,
      }}>
        <button
          className={`tab-btn${tab === 'profiles' ? ' active' : ''}`}
          style={{ fontSize: 13 }}
          onClick={() => setTab('profiles')}
        >Profiles</button>
        <button
          className={`tab-btn${tab === 'codes' ? ' active' : ''}`}
          style={{ fontSize: 13 }}
          onClick={() => setTab('codes')}
        >Code Rules</button>
        <button
          className={`tab-btn${tab === 'patterns' ? ' active' : ''}`}
          style={{ fontSize: 13 }}
          onClick={() => setTab('patterns')}
        >Pattern Rules</button>
      </div>

      {/* Content */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {tab === 'profiles' && <ProfilesTab />}
        {tab === 'codes'    && <CodeRulesTab />}
        {tab === 'patterns' && <PatternRulesTab />}
      </div>
    </div>
  )
}
