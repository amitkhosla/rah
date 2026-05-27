import { useEffect, useState, useCallback, useRef } from 'react'
import { listReleases, getReleaseById, createRelease, promoteRelease } from '../api'
import type {
  ReleaseRecord,
  LintIssue,
  EnvDeployment,
  ReleaseDeployResult,
  CreateReleaseResponse,
} from '../types'

// ── helpers ───────────────────────────────────────────────────────────────────

function fmtDate(raw: string | { T: string } | undefined): string {
  if (!raw) return '—'
  const s = typeof raw === 'string' ? raw : raw.T
  if (!s) return '—'
  try {
    return new Date(s).toLocaleString()
  } catch {
    return s
  }
}

function fmtShort(raw: string | { T: string } | undefined): string {
  if (!raw) return '—'
  const s = typeof raw === 'string' ? raw : raw.T
  if (!s) return '—'
  try {
    return new Date(s).toLocaleDateString()
  } catch {
    return s
  }
}

function shortID(id: string): string {
  return id.length > 12 ? id.slice(0, 12) + '…' : id
}

// ── LintBadge ─────────────────────────────────────────────────────────────────

function LintBadge({ count, color, label }: { count: number; color: string; label: string }) {
  if (count === 0) return null
  return (
    <span
      title={`${count} ${label}`}
      style={{
        fontSize: 10,
        fontWeight: 700,
        background: color,
        color: '#fff',
        borderRadius: 8,
        padding: '1px 6px',
        marginLeft: 4,
      }}
    >
      {count} {label[0]}
    </span>
  )
}

// ── status badge ──────────────────────────────────────────────────────────────

function StatusBadge({ status }: { status?: string }) {
  if (!status) return <span style={{ color: 'var(--muted)', fontSize: 11 }}>—</span>
  const color = status === 'deployed' ? '#22c55e' : status === 'failed' ? '#ef4444' : '#f59e0b'
  return (
    <span style={{
      fontSize: 10,
      fontWeight: 700,
      color,
      border: `1px solid ${color}`,
      borderRadius: 6,
      padding: '1px 6px',
      textTransform: 'uppercase',
    }}>
      {status}
    </span>
  )
}

// ── issue severity row ────────────────────────────────────────────────────────

function IssueRow({ issue }: { issue: LintIssue }) {
  const color = issue.severity === 'error' ? '#ef4444' : issue.severity === 'warning' ? '#f59e0b' : '#64748b'
  return (
    <div style={{
      display: 'flex',
      gap: 8,
      alignItems: 'flex-start',
      padding: '6px 0',
      borderBottom: '1px solid var(--border)',
      fontSize: 12,
    }}>
      <span style={{ flexShrink: 0, fontWeight: 700, color, minWidth: 50 }}>
        {issue.severity}
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ color: 'var(--text)' }}>{issue.message}</div>
        {issue.file && (
          <div style={{ color: 'var(--muted)', fontSize: 11, marginTop: 2 }}>
            {issue.file}{issue.line > 0 ? `:${issue.line}` : ''} · {issue.rule}
          </div>
        )}
        {issue.suggestion && (
          <div style={{ color: 'var(--accent)', fontSize: 11, marginTop: 2 }}>
            Hint: {issue.suggestion}
          </div>
        )}
      </div>
    </div>
  )
}

// ── environment grid ──────────────────────────────────────────────────────────

const KNOWN_ENVS = ['dev', 'uat', 'demo', 'prd']

interface EnvGridProps {
  releaseId: string
  environments?: Record<string, EnvDeployment>
  onPromoted: (updated: ReleaseRecord) => void
}

function EnvGrid({ releaseId, environments, onPromoted }: EnvGridProps) {
  const [promoting, setPromoting] = useState<string | null>(null)
  const [promoteErr, setPromoteErr] = useState<string | null>(null)
  const [customEnv, setCustomEnv] = useState('')

  // Collect all envs: known first, then any others from record
  const extra = Object.keys(environments ?? {}).filter(e => !KNOWN_ENVS.includes(e))
  const cols = [...KNOWN_ENVS, ...extra]

  async function handlePromote(env: string) {
    setPromoting(env)
    setPromoteErr(null)
    try {
      await promoteRelease(releaseId, env)
      // Reload to get updated environments
      const updated = await getReleaseById(releaseId)
      onPromoted(updated)
    } catch (e: any) {
      setPromoteErr(e.message ?? 'Promote failed')
    } finally {
      setPromoting(null)
    }
  }

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
        <span style={{ fontWeight: 700, fontSize: 13 }}>Environments</span>
        {promoteErr && (
          <span style={{ fontSize: 11, color: '#ef4444' }}>{promoteErr}</span>
        )}
      </div>
      <div style={{
        display: 'grid',
        gridTemplateColumns: `repeat(${cols.length}, 1fr)`,
        gap: 1,
        background: 'var(--border)',
        borderRadius: 8,
        overflow: 'hidden',
      }}>
        {/* Header row */}
        {cols.map(env => (
          <div key={env} style={{
            background: 'var(--panel)',
            padding: '8px 12px',
            fontSize: 11,
            fontWeight: 700,
            color: 'var(--muted)',
            textTransform: 'uppercase',
            textAlign: 'center',
          }}>
            {env}
          </div>
        ))}
        {/* Data row */}
        {cols.map(env => {
          const dep = environments?.[env]
          return (
            <div key={env} style={{
              background: 'var(--bg)',
              padding: '10px 12px',
              display: 'flex',
              flexDirection: 'column',
              gap: 6,
              alignItems: 'center',
            }}>
              <StatusBadge status={dep?.status} />
              {dep && (
                <>
                  <div style={{ fontSize: 10, color: 'var(--muted)', textAlign: 'center' }}>
                    {fmtShort(dep.deployed_at)}
                  </div>
                  {dep.by_user && (
                    <div style={{ fontSize: 10, color: 'var(--muted)' }}>by {dep.by_user}</div>
                  )}
                </>
              )}
              <button
                disabled={promoting === env}
                onClick={() => handlePromote(env)}
                style={{
                  marginTop: 4,
                  fontSize: 10,
                  padding: '3px 10px',
                  borderRadius: 6,
                  border: '1px solid var(--accent)',
                  background: 'transparent',
                  color: 'var(--accent)',
                  cursor: promoting === env ? 'wait' : 'pointer',
                  opacity: promoting === env ? 0.6 : 1,
                  fontWeight: 600,
                }}
              >
                {promoting === env ? 'Promoting…' : dep ? 'Re-promote' : 'Promote'}
              </button>
            </div>
          )
        })}
      </div>

      {/* Custom env promote */}
      <div style={{ marginTop: 12, display: 'flex', gap: 8, alignItems: 'center' }}>
        <input
          value={customEnv}
          onChange={e => setCustomEnv(e.target.value)}
          placeholder="Custom env name…"
          style={{
            flex: 1,
            fontSize: 12,
            padding: '5px 10px',
            borderRadius: 6,
            border: '1px solid var(--border)',
            background: 'var(--panel)',
            color: 'var(--text)',
          }}
        />
        <button
          disabled={!customEnv.trim() || promoting !== null}
          onClick={() => { if (customEnv.trim()) handlePromote(customEnv.trim()) }}
          style={{
            fontSize: 12,
            padding: '5px 12px',
            borderRadius: 6,
            border: '1px solid var(--accent)',
            background: 'transparent',
            color: 'var(--accent)',
            cursor: 'pointer',
            fontWeight: 600,
          }}
        >
          Promote to env
        </button>
      </div>
    </div>
  )
}

// ── deploy results table ───────────────────────────────────────────────────────

function DeployResultsTable({ results }: { results: ReleaseDeployResult[] }) {
  if (results.length === 0) return null
  return (
    <div style={{ marginTop: 8 }}>
      {results.map((r, i) => (
        <div key={i} style={{
          display: 'flex',
          gap: 8,
          alignItems: 'center',
          fontSize: 11,
          padding: '3px 0',
          color: r.success ? '#22c55e' : '#ef4444',
        }}>
          <span style={{ fontWeight: 700 }}>{r.success ? '✓' : '✗'}</span>
          <span style={{ color: 'var(--text)' }}>{r.target}</span>
          {r.message && <span style={{ color: 'var(--muted)' }}>{r.message}</span>}
        </div>
      ))}
    </div>
  )
}

// ── detail panel ──────────────────────────────────────────────────────────────

function DetailPanel({ release, onUpdated }: { release: ReleaseRecord; onUpdated: (r: ReleaseRecord) => void }) {
  const ls = release.lint_summary

  return (
    <div style={{ padding: '20px 24px', overflowY: 'auto', height: '100%' }}>
      {/* Release meta */}
      <div style={{
        background: 'var(--panel)',
        border: '1px solid var(--border)',
        borderRadius: 10,
        padding: '16px 20px',
        marginBottom: 20,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
          <span style={{ fontWeight: 700, fontSize: 15, fontFamily: 'monospace' }}>
            {release.release_id}
          </span>
          {release.tag && (
            <span style={{
              fontSize: 11,
              fontWeight: 700,
              background: 'var(--accent)',
              color: '#fff',
              borderRadius: 8,
              padding: '2px 8px',
            }}>
              {release.tag}
            </span>
          )}
          {ls && (
            <span style={{ marginLeft: 'auto', display: 'flex', gap: 4 }}>
              <LintBadge count={ls.errors} color="#ef4444" label="errors" />
              <LintBadge count={ls.warnings} color="#f59e0b" label="warnings" />
              <LintBadge count={ls.infos} color="#64748b" label="infos" />
            </span>
          )}
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '6px 20px', fontSize: 12, color: 'var(--muted)' }}>
          <div><strong style={{ color: 'var(--text)' }}>Created</strong>: {fmtDate(release.created_at)}</div>
          {release.author && <div><strong style={{ color: 'var(--text)' }}>Author</strong>: {release.author}</div>}
          {release.git_branch && <div><strong style={{ color: 'var(--text)' }}>Branch</strong>: {release.git_branch}</div>}
          {release.git_commit && (
            <div><strong style={{ color: 'var(--text)' }}>Commit</strong>: <code style={{ fontSize: 11 }}>{release.git_commit.slice(0, 12)}</code></div>
          )}
          {release.git_repo && <div><strong style={{ color: 'var(--text)' }}>Repo</strong>: {release.git_repo}</div>}
          {release.source_path && <div><strong style={{ color: 'var(--text)' }}>Source</strong>: {release.source_path}</div>}
          {release.bundle_hash && (
            <div style={{ gridColumn: '1 / -1' }}>
              <strong style={{ color: 'var(--text)' }}>Bundle hash</strong>: <code style={{ fontSize: 10 }}>{release.bundle_hash}</code>
            </div>
          )}
        </div>
      </div>

      {/* Environment grid */}
      <div style={{ marginBottom: 24 }}>
        <EnvGrid
          releaseId={release.release_id}
          environments={release.environments}
          onPromoted={onUpdated}
        />
      </div>
    </div>
  )
}

// ── import panel ──────────────────────────────────────────────────────────────

interface ImportPanelProps {
  onPublished: (r: ReleaseRecord) => void
}

function ImportPanel({ onPublished }: ImportPanelProps) {
  const [content, setContent] = useState('')
  const [tag, setTag] = useState('')
  const [author, setAuthor] = useState('')
  const [gitCommit, setGitCommit] = useState('')
  const [gitBranch, setGitBranch] = useState('')

  const [validating, setValidating] = useState(false)
  const [publishing, setPublishing] = useState(false)
  const [validateResult, setValidateResult] = useState<CreateReleaseResponse | null>(null)
  const [err, setErr] = useState<string | null>(null)

  const fileRef = useRef<HTMLInputElement>(null)

  function handleFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = ev => setContent((ev.target?.result as string) ?? '')
    reader.readAsText(file)
  }

  async function handleValidate() {
    if (!content.trim()) return
    setValidating(true)
    setErr(null)
    setValidateResult(null)
    try {
      const res = await createRelease(content, { tag, author, git_commit: gitCommit, git_branch: gitBranch }, true)
      setValidateResult(res)
    } catch (e: any) {
      setErr(e.message ?? 'Validation failed')
    } finally {
      setValidating(false)
    }
  }

  async function handlePublish() {
    if (!content.trim()) return
    setPublishing(true)
    setErr(null)
    try {
      const res = await createRelease(content, { tag, author, git_commit: gitCommit, git_branch: gitBranch }, false)
      if (res.release_id) {
        const record = await getReleaseById(res.release_id)
        onPublished(record)
        // Reset form
        setContent('')
        setTag('')
        setAuthor('')
        setGitCommit('')
        setGitBranch('')
        setValidateResult(null)
        if (fileRef.current) fileRef.current.value = ''
      }
    } catch (e: any) {
      setErr(e.message ?? 'Publish failed')
    } finally {
      setPublishing(false)
    }
  }

  const hasErrors = (validateResult?.lint_summary?.errors ?? 0) > 0
  const canPublish = content.trim().length > 0 && !hasErrors

  return (
    <div style={{ padding: '20px 24px', overflowY: 'auto', height: '100%' }}>
      <div style={{ fontWeight: 700, fontSize: 14, marginBottom: 16 }}>Import Bundle</div>

      {/* File picker */}
      <div style={{ marginBottom: 12, display: 'flex', alignItems: 'center', gap: 10 }}>
        <input ref={fileRef} type="file" accept=".yaml,.yml,.json" onChange={handleFile} style={{ fontSize: 12, color: 'var(--text)' }} />
        <span style={{ fontSize: 11, color: 'var(--muted)' }}>or paste YAML/JSON below</span>
      </div>

      {/* Text area */}
      <textarea
        value={content}
        onChange={e => { setContent(e.target.value); setValidateResult(null) }}
        placeholder="Paste bundle YAML or JSON here…"
        rows={10}
        style={{
          width: '100%',
          fontSize: 12,
          fontFamily: 'monospace',
          padding: '10px 12px',
          borderRadius: 8,
          border: '1px solid var(--border)',
          background: 'var(--panel)',
          color: 'var(--text)',
          resize: 'vertical',
          boxSizing: 'border-box',
          marginBottom: 14,
        }}
      />

      {/* Metadata form */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px 16px', marginBottom: 16 }}>
        {([
          ['Tag', tag, setTag, 'v1.2.3'],
          ['Author', author, setAuthor, 'alice@example.com'],
          ['Git commit', gitCommit, setGitCommit, 'abc1234'],
          ['Git branch', gitBranch, setGitBranch, 'main'],
        ] as [string, string, (v: string) => void, string][]).map(([label, val, setter, ph]) => (
          <div key={label}>
            <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 3 }}>{label}</div>
            <input
              value={val}
              onChange={e => setter(e.target.value)}
              placeholder={ph}
              style={{
                width: '100%',
                fontSize: 12,
                padding: '5px 10px',
                borderRadius: 6,
                border: '1px solid var(--border)',
                background: 'var(--panel)',
                color: 'var(--text)',
                boxSizing: 'border-box',
              }}
            />
          </div>
        ))}
      </div>

      {/* Actions */}
      <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginBottom: 16 }}>
        <button
          disabled={!content.trim() || validating}
          onClick={handleValidate}
          style={{
            fontSize: 13,
            padding: '7px 18px',
            borderRadius: 7,
            border: '1px solid var(--border)',
            background: 'var(--panel)',
            color: 'var(--text)',
            cursor: validating || !content.trim() ? 'not-allowed' : 'pointer',
            opacity: !content.trim() ? 0.5 : 1,
            fontWeight: 600,
          }}
        >
          {validating ? 'Validating…' : 'Validate'}
        </button>
        <button
          disabled={!canPublish || publishing}
          onClick={handlePublish}
          style={{
            fontSize: 13,
            padding: '7px 18px',
            borderRadius: 7,
            border: 'none',
            background: canPublish && !publishing ? 'var(--accent)' : 'var(--border)',
            color: canPublish && !publishing ? '#fff' : 'var(--muted)',
            cursor: !canPublish || publishing ? 'not-allowed' : 'pointer',
            fontWeight: 700,
          }}
        >
          {publishing ? 'Publishing…' : 'Publish'}
        </button>
        {err && <span style={{ fontSize: 12, color: '#ef4444' }}>{err}</span>}
      </div>

      {/* Lint results */}
      {validateResult && (
        <div style={{
          background: 'var(--panel)',
          border: '1px solid var(--border)',
          borderRadius: 8,
          padding: '14px 16px',
        }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 10 }}>
            <span style={{ fontWeight: 700, fontSize: 13 }}>Validation result</span>
            <LintBadge count={validateResult.lint_summary?.errors ?? 0} color="#ef4444" label="errors" />
            <LintBadge count={validateResult.lint_summary?.warnings ?? 0} color="#f59e0b" label="warnings" />
            <LintBadge count={validateResult.lint_summary?.infos ?? 0} color="#64748b" label="infos" />
          </div>
          {(validateResult.issues ?? []).length === 0 && (
            <div style={{ fontSize: 12, color: '#22c55e' }}>No issues found — ready to publish.</div>
          )}
          {(validateResult.issues ?? []).map((issue, i) => <IssueRow key={i} issue={issue} />)}
        </div>
      )}
    </div>
  )
}

// ── main Releases screen ──────────────────────────────────────────────────────

export default function Releases() {
  const [releases, setReleases] = useState<ReleaseRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState<string | null>(null)
  const [selected, setSelected] = useState<ReleaseRecord | null>(null)
  const [rightMode, setRightMode] = useState<'detail' | 'import'>('import')

  const load = useCallback(async () => {
    setLoading(true)
    setLoadErr(null)
    try {
      const res = await listReleases()
      setReleases(res.releases ?? [])
    } catch (e: any) {
      setLoadErr(e.message ?? 'Failed to load releases')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  function handleSelect(r: ReleaseRecord) {
    setSelected(r)
    setRightMode('detail')
  }

  function handleUpdated(r: ReleaseRecord) {
    setSelected(r)
    setReleases(prev => prev.map(x => x.release_id === r.release_id ? r : x))
  }

  function handlePublished(r: ReleaseRecord) {
    setReleases(prev => [r, ...prev])
    setSelected(r)
    setRightMode('detail')
  }

  return (
    <div style={{ display: 'flex', height: '100%', overflow: 'hidden' }}>
      {/* ── Left panel: release list ── */}
      <aside style={{
        width: 280,
        flexShrink: 0,
        borderRight: '1px solid var(--border)',
        display: 'flex',
        flexDirection: 'column',
        overflow: 'hidden',
      }}>
        {/* Header */}
        <div style={{
          padding: '14px 16px',
          borderBottom: '1px solid var(--border)',
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          flexShrink: 0,
        }}>
          <span style={{ fontWeight: 700, fontSize: 14, flex: 1 }}>Releases</span>
          <button
            onClick={() => { setSelected(null); setRightMode('import') }}
            style={{
              fontSize: 11,
              padding: '4px 10px',
              borderRadius: 6,
              border: '1px solid var(--accent)',
              background: rightMode === 'import' && selected === null ? 'var(--accent)' : 'transparent',
              color: rightMode === 'import' && selected === null ? '#fff' : 'var(--accent)',
              cursor: 'pointer',
              fontWeight: 600,
            }}
          >
            + Import
          </button>
          <button
            onClick={load}
            title="Refresh"
            style={{
              fontSize: 12,
              padding: '4px 8px',
              borderRadius: 6,
              border: '1px solid var(--border)',
              background: 'transparent',
              color: 'var(--muted)',
              cursor: 'pointer',
            }}
          >
            ↺
          </button>
        </div>

        {/* List */}
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {loading && (
            <div style={{ padding: 20, fontSize: 12, color: 'var(--muted)', textAlign: 'center' }}>
              Loading…
            </div>
          )}
          {loadErr && (
            <div style={{ padding: 16, fontSize: 12, color: '#ef4444' }}>{loadErr}</div>
          )}
          {!loading && !loadErr && releases.length === 0 && (
            <div style={{ padding: 20, fontSize: 12, color: 'var(--muted)', textAlign: 'center' }}>
              No releases yet.<br />Use "Import" to publish your first bundle.
            </div>
          )}
          {releases.map(r => {
            const isActive = selected?.release_id === r.release_id
            const ls = r.lint_summary
            return (
              <button
                key={r.release_id}
                onClick={() => handleSelect(r)}
                style={{
                  display: 'block',
                  width: '100%',
                  textAlign: 'left',
                  padding: '10px 14px',
                  borderBottom: '1px solid var(--border)',
                  background: isActive ? 'rgba(255,255,255,0.06)' : 'transparent',
                  border: 'none',
                  borderLeft: isActive ? '3px solid var(--accent)' : '3px solid transparent',
                  color: 'var(--text)',
                  cursor: 'pointer',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 3 }}>
                  <span style={{ fontFamily: 'monospace', fontSize: 11, flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {shortID(r.release_id)}
                  </span>
                  {r.tag && (
                    <span style={{
                      fontSize: 10,
                      fontWeight: 700,
                      background: 'var(--accent)',
                      color: '#fff',
                      borderRadius: 6,
                      padding: '1px 5px',
                    }}>
                      {r.tag}
                    </span>
                  )}
                </div>
                <div style={{ fontSize: 10, color: 'var(--muted)', marginBottom: ls ? 4 : 0 }}>
                  {fmtDate(r.created_at)}
                  {r.author ? ` · ${r.author}` : ''}
                </div>
                {ls && (ls.errors > 0 || ls.warnings > 0) && (
                  <div>
                    <LintBadge count={ls.errors} color="#ef4444" label="errors" />
                    <LintBadge count={ls.warnings} color="#f59e0b" label="warnings" />
                  </div>
                )}
                {/* env status dots */}
                {r.environments && Object.keys(r.environments).length > 0 && (
                  <div style={{ display: 'flex', gap: 4, marginTop: 4, flexWrap: 'wrap' }}>
                    {Object.entries(r.environments).map(([env, dep]) => (
                      <span key={env} title={`${env}: ${dep.status}`} style={{
                        fontSize: 9,
                        padding: '1px 5px',
                        borderRadius: 5,
                        border: `1px solid ${dep.status === 'deployed' ? '#22c55e' : '#f59e0b'}`,
                        color: dep.status === 'deployed' ? '#22c55e' : '#f59e0b',
                        fontWeight: 600,
                        textTransform: 'uppercase',
                      }}>
                        {env}
                      </span>
                    ))}
                  </div>
                )}
              </button>
            )
          })}
        </div>
      </aside>

      {/* ── Right panel ── */}
      <main style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
        {/* Tabs */}
        {selected && (
          <div style={{
            display: 'flex',
            gap: 0,
            borderBottom: '1px solid var(--border)',
            flexShrink: 0,
          }}>
            {(['detail', 'import'] as const).map(t => (
              <button
                key={t}
                onClick={() => setRightMode(t)}
                style={{
                  fontSize: 13,
                  fontWeight: rightMode === t ? 700 : 400,
                  padding: '10px 20px',
                  border: 'none',
                  borderBottom: rightMode === t ? '2px solid var(--accent)' : '2px solid transparent',
                  background: 'transparent',
                  color: rightMode === t ? 'var(--accent)' : 'var(--muted)',
                  cursor: 'pointer',
                  textTransform: 'capitalize',
                }}
              >
                {t === 'detail' ? 'Detail' : 'Import Bundle'}
              </button>
            ))}
          </div>
        )}

        <div style={{ flex: 1, overflow: 'hidden' }}>
          {rightMode === 'detail' && selected ? (
            <DetailPanel release={selected} onUpdated={handleUpdated} />
          ) : (
            <ImportPanel onPublished={handlePublished} />
          )}
        </div>
      </main>
    </div>
  )
}
