import { useEffect, useState, useCallback } from 'react'
import {
  listApps,
  listAppReleases,
  createAppRelease,
  promoteAppRelease,
  rollbackAppRelease,
} from '../api'
import type { App } from '../types'
import type { AppRelease } from '../api'

// ── Helpers ───────────────────────────────────────────────────────────────────

function fmtDate(ts: string): string {
  if (!ts) return '—'
  try {
    return new Date(ts).toLocaleString()
  } catch {
    return ts
  }
}

// ── Release Row ───────────────────────────────────────────────────────────────

function ReleaseRow({
  appName,
  release,
  onPromoted,
  onRolledback,
}: {
  appName: string
  release: AppRelease
  onPromoted: (r: AppRelease) => void
  onRolledback: (r: AppRelease) => void
}) {
  const [promoting, setPromoting] = useState(false)
  const [rollingBack, setRollingBack] = useState(false)
  const [err, setErr] = useState('')

  async function handlePromote() {
    setPromoting(true)
    setErr('')
    try {
      const result = await promoteAppRelease(appName, release.version, release.channel)
      onPromoted(result)
    } catch (e) {
      setErr(String(e))
    } finally {
      setPromoting(false)
    }
  }

  async function handleRollback() {
    setRollingBack(true)
    setErr('')
    try {
      const result = await rollbackAppRelease(appName, release.version, release.channel)
      onRolledback(result)
    } catch (e) {
      setErr(String(e))
    } finally {
      setRollingBack(false)
    }
  }

  return (
    <div
      style={{
        background: '#1e1e2e',
        border: '1px solid #313244',
        borderRadius: 8,
        padding: 12,
        marginBottom: 8,
      }}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 8 }}>
        <div style={{ flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
            <span style={{ fontSize: 13, fontWeight: 700, color: '#cdd6f4' }}>{release.version}</span>
            <span
              style={{
                fontSize: 11,
                fontWeight: 700,
                color: '#89b4fa',
                background: 'rgba(137,180,250,0.1)',
                padding: '2px 8px',
                borderRadius: 4,
              }}
            >
              {release.channel}
            </span>
            {release.active && (
              <span
                style={{
                  fontSize: 11,
                  fontWeight: 700,
                  color: '#fff',
                  background: '#22c55e',
                  padding: '2px 8px',
                  borderRadius: 4,
                }}
              >
                ACTIVE
              </span>
            )}
          </div>
          <div style={{ fontSize: 12, color: '#a6adc8', marginBottom: 4 }}>
            Flows: {release.flow_names.length}
          </div>
          {release.notes && (
            <div style={{ fontSize: 12, color: '#a6adc8', marginBottom: 4 }}>
              {release.notes}
            </div>
          )}
          <div style={{ fontSize: 11, color: '#6c7086' }}>
            {fmtDate(release.created_at)}
            {release.created_by ? ` · by ${release.created_by}` : ''}
          </div>
        </div>
        <div style={{ display: 'flex', gap: 6, flexShrink: 0 }}>
          <button
            onClick={handlePromote}
            disabled={promoting || rollingBack}
            style={{
              fontSize: 11,
              padding: '4px 10px',
              borderRadius: 6,
              border: '1px solid #89b4fa',
              background: 'transparent',
              color: '#89b4fa',
              cursor: promoting || rollingBack ? 'not-allowed' : 'pointer',
              opacity: promoting || rollingBack ? 0.6 : 1,
              fontWeight: 600,
            }}
          >
            {promoting ? 'Promoting…' : 'Promote'}
          </button>
          <button
            onClick={handleRollback}
            disabled={promoting || rollingBack}
            style={{
              fontSize: 11,
              padding: '4px 10px',
              borderRadius: 6,
              border: '1px solid #f38ba8',
              background: 'transparent',
              color: '#f38ba8',
              cursor: promoting || rollingBack ? 'not-allowed' : 'pointer',
              opacity: promoting || rollingBack ? 0.6 : 1,
              fontWeight: 600,
            }}
          >
            {rollingBack ? 'Rolling back…' : 'Rollback'}
          </button>
        </div>
      </div>
      {err && (
        <div style={{ fontSize: 11, color: '#f38ba8', marginTop: 6 }}>
          Error: {err}
        </div>
      )}
    </div>
  )
}

// ── New Release Form ──────────────────────────────────────────────────────────

function NewReleaseForm({
  appName,
  onCreated,
  onCancel,
}: {
  appName: string
  onCreated: (r: AppRelease) => void
  onCancel: () => void
}) {
  const [version, setVersion] = useState('')
  const [channel, setChannel] = useState('staging')
  const [flowNames, setFlowNames] = useState('')
  const [notes, setNotes] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  async function handleCreate() {
    if (!version.trim()) {
      setErr('Version required')
      return
    }
    if (!flowNames.trim()) {
      setErr('At least one flow name required')
      return
    }

    setSaving(true)
    setErr('')
    try {
      const flows = flowNames.split(',').map(s => s.trim()).filter(Boolean)
      const result = await createAppRelease(appName, {
        version: version.trim(),
        channel,
        flow_names: flows,
        notes: notes.trim() || undefined,
        created_by: undefined,
      })
      onCreated(result)
      setVersion('')
      setChannel('staging')
      setFlowNames('')
      setNotes('')
    } catch (e) {
      setErr(String(e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      style={{
        background: '#1e1e2e',
        border: '1px solid #313244',
        borderRadius: 8,
        padding: 12,
        marginBottom: 16,
      }}
    >
      <div style={{ fontSize: 13, fontWeight: 700, color: '#cdd6f4', marginBottom: 10 }}>
        New Release
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
        <div>
          <label style={{ fontSize: 11, color: '#a6adc8', display: 'block', marginBottom: 4 }}>
            Version
          </label>
          <input
            type="text"
            value={version}
            onChange={e => setVersion(e.target.value)}
            placeholder="e.g. 1.0.0"
            style={{
              width: '100%',
              fontSize: 12,
              padding: '6px 8px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1e',
              color: '#cdd6f4',
              boxSizing: 'border-box',
            }}
          />
        </div>
        <div>
          <label style={{ fontSize: 11, color: '#a6adc8', display: 'block', marginBottom: 4 }}>
            Channel
          </label>
          <select
            value={channel}
            onChange={e => setChannel(e.target.value)}
            style={{
              width: '100%',
              fontSize: 12,
              padding: '6px 8px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1e',
              color: '#cdd6f4',
              boxSizing: 'border-box',
            }}
          >
            <option value="staging">Staging</option>
            <option value="production">Production</option>
          </select>
        </div>
      </div>

      <div style={{ marginBottom: 10 }}>
        <label style={{ fontSize: 11, color: '#a6adc8', display: 'block', marginBottom: 4 }}>
          Flow Names (comma-separated)
        </label>
        <input
          type="text"
          value={flowNames}
          onChange={e => setFlowNames(e.target.value)}
          placeholder="flow1, flow2, flow3"
          style={{
            width: '100%',
            fontSize: 12,
            padding: '6px 8px',
            borderRadius: 6,
            border: '1px solid #313244',
            background: '#0f0f1e',
            color: '#cdd6f4',
            boxSizing: 'border-box',
          }}
        />
      </div>

      <div style={{ marginBottom: 10 }}>
        <label style={{ fontSize: 11, color: '#a6adc8', display: 'block', marginBottom: 4 }}>
          Notes (optional)
        </label>
        <textarea
          value={notes}
          onChange={e => setNotes(e.target.value)}
          placeholder="Release notes…"
          rows={3}
          style={{
            width: '100%',
            fontSize: 12,
            padding: '6px 8px',
            borderRadius: 6,
            border: '1px solid #313244',
            background: '#0f0f1e',
            color: '#cdd6f4',
            boxSizing: 'border-box',
            resize: 'vertical',
          }}
        />
      </div>

      {err && (
        <div style={{ fontSize: 11, color: '#f38ba8', marginBottom: 10 }}>
          Error: {err}
        </div>
      )}

      <div style={{ display: 'flex', gap: 8 }}>
        <button
          onClick={handleCreate}
          disabled={saving}
          style={{
            fontSize: 12,
            padding: '6px 12px',
            borderRadius: 6,
            border: 'none',
            background: '#89b4fa',
            color: '#fff',
            cursor: saving ? 'not-allowed' : 'pointer',
            fontWeight: 700,
            opacity: saving ? 0.6 : 1,
          }}
        >
          {saving ? 'Creating…' : 'Create Release'}
        </button>
        <button
          onClick={onCancel}
          disabled={saving}
          style={{
            fontSize: 12,
            padding: '6px 12px',
            borderRadius: 6,
            border: '1px solid #313244',
            background: 'transparent',
            color: '#a6adc8',
            cursor: 'pointer',
            fontWeight: 600,
          }}
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

// ── App Release List ──────────────────────────────────────────────────────────

function AppReleasesList({
  app,
  releases,
  loading,
  error,
  onRefresh,
}: {
  app: App
  releases: AppRelease[]
  loading: boolean
  error: string | null
  onRefresh: () => void
}) {
  const [showNewForm, setShowNewForm] = useState(false)
  const [expanded, setExpanded] = useState(true)

  function handleReleaseCreated(r: AppRelease) {
    setShowNewForm(false)
    onRefresh()
  }

  function handleReleaseUpdated() {
    onRefresh()
  }

  return (
    <div style={{ marginBottom: 20 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
        <button
          onClick={() => setExpanded(!expanded)}
          style={{
            fontSize: 14,
            background: 'none',
            border: 'none',
            color: '#89b4fa',
            cursor: 'pointer',
            padding: 0,
            fontWeight: 600,
          }}
        >
          {expanded ? '▼' : '▶'}
        </button>
        <span style={{ fontSize: 14, fontWeight: 700, color: '#cdd6f4', flex: 1 }}>
          {app.name}
        </span>
        {expanded && !showNewForm && (
          <button
            onClick={() => setShowNewForm(true)}
            style={{
              fontSize: 11,
              padding: '4px 10px',
              borderRadius: 6,
              border: '1px solid #89b4fa',
              background: 'transparent',
              color: '#89b4fa',
              cursor: 'pointer',
              fontWeight: 600,
            }}
          >
            + New Release
          </button>
        )}
      </div>

      {expanded && (
        <div style={{ marginLeft: 24 }}>
          {showNewForm && (
            <NewReleaseForm
              appName={app.name}
              onCreated={handleReleaseCreated}
              onCancel={() => setShowNewForm(false)}
            />
          )}

          {loading && (
            <div style={{ fontSize: 12, color: '#a6adc8' }}>Loading releases…</div>
          )}

          {error && (
            <div style={{ fontSize: 12, color: '#f38ba8', marginBottom: 8 }}>
              Error: {error}
            </div>
          )}

          {!loading && releases.length === 0 && !error && (
            <div style={{ fontSize: 12, color: '#6c7086' }}>
              No releases yet.
            </div>
          )}

          {releases.map(release => (
            <ReleaseRow
              key={`${release.version}-${release.channel}`}
              appName={app.name}
              release={release}
              onPromoted={handleReleaseUpdated}
              onRolledback={handleReleaseUpdated}
            />
          ))}
        </div>
      )}
    </div>
  )
}

// ── Main App Releases Component ───────────────────────────────────────────────

export default function AppReleases() {
  const [apps, setApps] = useState<App[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [appReleases, setAppReleases] = useState<Map<string, AppRelease[]>>(new Map())
  const [releaseLoading, setReleaseLoading] = useState<Set<string>>(new Set())
  const [releaseErrors, setReleaseErrors] = useState<Map<string, string>>(new Map())

  const loadApps = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const result = await listApps()
      setApps(result ?? [])
    } catch (e) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadApps()
  }, [loadApps])

  const loadReleases = useCallback(async (appName: string) => {
    setReleaseLoading(prev => new Set([...prev, appName]))
    setReleaseErrors(prev => new Map(prev))
    try {
      const result = await listAppReleases(appName)
      setAppReleases(prev => new Map([...prev, [appName, result.releases ?? []]]))
      setReleaseErrors(prev => {
        const updated = new Map(prev)
        updated.delete(appName)
        return updated
      })
    } catch (e) {
      setReleaseErrors(prev => new Map([...prev, [appName, String(e)]]))
    } finally {
      setReleaseLoading(prev => {
        const updated = new Set(prev)
        updated.delete(appName)
        return updated
      })
    }
  }, [])

  if (loading) {
    return (
      <div style={{ padding: '20px 24px', color: '#a6adc8', fontSize: 13 }}>
        Loading…
      </div>
    )
  }

  if (error) {
    return (
      <div style={{ padding: '20px 24px', color: '#f38ba8', fontSize: 13 }}>
        Error: {error}
      </div>
    )
  }

  return (
    <div style={{ padding: '20px 24px', height: '100%', overflowY: 'auto' }}>
      <div style={{ marginBottom: 16 }}>
        <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 6 }}>
          App Releases
        </h2>
        <p style={{ fontSize: 13, color: '#6c7086', marginBottom: 12 }}>
          Manage versioned releases for apps. Each release groups specific flows by version and channel.
        </p>
      </div>

      {apps.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>No apps configured.</div>
      ) : (
        apps.map(app => (
          <AppReleasesList
            key={app.name}
            app={app}
            releases={appReleases.get(app.name) ?? []}
            loading={releaseLoading.has(app.name)}
            error={releaseErrors.get(app.name) ?? null}
            onRefresh={() => loadReleases(app.name)}
          />
        ))
      )}
    </div>
  )
}
