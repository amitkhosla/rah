import { useEffect, useState } from 'react'
import { deploy, fetchTargets } from '../api'
import type { ApiDef, DeployRecord, ReleaseRecord, SavedFlow, Target } from '../types'

interface Props {
  savedFlows: SavedFlow[]
  apis: ApiDef[]
}

function csvToArray(s: string): string[] {
  return s.split(',').map(x => x.trim()).filter(Boolean)
}

function formatTime(t: { T: string } | string): string {
  const raw = typeof t === 'object' ? t.T : t
  if (!raw) return '?'
  try { return new Date(raw).toLocaleString() } catch { return raw }
}

export default function Deploy({ savedFlows, apis }: Props) {
  const [targets,  setTargets]  = useState<Target[]>([])
  const [history,  setHistory]  = useState<DeployRecord[]>([])
  const [releases, setReleases] = useState<ReleaseRecord[]>([])
  const [loadErr,  setLoadErr]  = useState('')

  const [releaseId,     setReleaseId]     = useState('')
  const [instrVersion,  setInstrVersion]  = useState('')
  const [apiVersionsRaw,setApiVersionsRaw]= useState('')
  const [levels,        setLevels]        = useState('')
  const [targetNames,   setTargetNames]   = useState('')

  const [status,    setStatus]    = useState('')
  const [statusErr, setStatusErr] = useState(false)
  const [deploying, setDeploying] = useState(false)

  async function load() {
    setLoadErr('')
    try {
      const d = await fetchTargets()
      setTargets(d.targets ?? [])
      setHistory(d.history ?? [])
      setReleases(d.releases ?? [])
    } catch (e) {
      setLoadErr(e instanceof Error ? e.message : 'Failed to load targets')
    }
  }

  useEffect(() => { void load() }, [])

  // Derive the set of flows referenced by the current API list
  const usedFlowNames  = [...new Set(apis.map(a => a.flow_name).filter(Boolean))]
  const flowsMissing   = usedFlowNames.filter(n => !savedFlows.find(f => f.name === n)?.steps.length)

  function parseApiVersions(): Record<string, string> {
    const t = apiVersionsRaw.trim()
    if (!t) return {}
    return JSON.parse(t) as Record<string, string>
  }

  async function handleReleaseDeploy() {
    if (apis.length === 0) {
      setStatusErr(true); setStatus('No APIs defined — add endpoints in the APIs tab.'); return
    }
    if (usedFlowNames.length === 0) {
      setStatusErr(true); setStatus('APIs have no flow assignments.'); return
    }
    if (flowsMissing.length > 0) {
      setStatusErr(true)
      setStatus(`Flow(s) have no steps yet: ${flowsMissing.join(', ')} — open Flow Designer to build them.`)
      return
    }

    let apiVersions: Record<string, string> = {}
    try { apiVersions = parseApiVersions() } catch {
      setStatusErr(true); setStatus('api_versions must be valid JSON'); return
    }

    setDeploying(true); setStatus(''); setStatusErr(false)
    try {
      // Build payload: one entry per used flow (with its steps), every API with its own flow_name
      const flowsPayload = usedFlowNames.map(name => {
        const saved = savedFlows.find(f => f.name === name)
        return { name, instructions: saved?.steps ?? [], action: 'upsert' as const }
      })
      const apisPayload = apis.map(a => ({
        name: a.name, path: a.path, flow_name: a.flow_name, action: 'upsert' as const,
      }))

      const res = await deploy({
        release_id:              releaseId.trim() || undefined,
        instruction_set_version: instrVersion.trim() || undefined,
        api_versions:            Object.keys(apiVersions).length ? apiVersions : undefined,
        levels:                  csvToArray(levels),
        target_names:            csvToArray(targetNames),
        payload: {
          sync_uuid: `ui-${Date.now()}`,
          flows:     flowsPayload,
          apis:      apisPayload,
        },
      })
      setStatus(`Release ${res.release_id} deployed to ${res.results.length} target(s).`)
      setStatusErr(false)
      await load()
    } catch (e) {
      setStatusErr(true)
      setStatus(e instanceof Error ? e.message : 'Deploy failed')
    } finally {
      setDeploying(false)
    }
  }

  async function handleDeployExisting() {
    const rid = releaseId.trim()
    if (!rid) { setStatusErr(true); setStatus('Enter or select a release ID.'); return }
    setDeploying(true); setStatus(''); setStatusErr(false)
    try {
      const res = await deploy({
        release_id:   rid,
        levels:       csvToArray(levels),
        target_names: csvToArray(targetNames),
      })
      setStatus(`Release ${res.release_id} re-deployed to ${res.results.length} target(s).`)
      setStatusErr(false)
      await load()
    } catch (e) {
      setStatusErr(true)
      setStatus(e instanceof Error ? e.message : 'Deploy failed')
    } finally {
      setDeploying(false)
    }
  }

  return (
    <div className="two-col">

      {/* Targets panel */}
      <div className="panel">
        <div className="panel-header">Targets</div>
        <div className="panel-body">
          {loadErr && <p className="status-err mt8">{loadErr}</p>}
          {targets.length === 0 && !loadErr && <p className="hint">No targets loaded yet.</p>}
          {targets.map(t => (
            <div key={t.name} className="step mt8">
              <strong>{t.name}</strong>
              <span className="hint"> [{t.level}]</span>
              {t.urls.map(u => (
                <div key={u} className="hint mt4" style={{ wordBreak: 'break-all' }}>{u}</div>
              ))}
            </div>
          ))}

          <input
            className="input mt12"
            placeholder="levels csv: dev,stage,prod"
            value={levels}
            onChange={e => setLevels(e.target.value)}
          />
          <input
            className="input mt8"
            placeholder="target names csv (optional)"
            value={targetNames}
            onChange={e => setTargetNames(e.target.value)}
          />

          {/* payload summary */}
          {usedFlowNames.length > 0 && (
            <div style={{ marginTop: 16 }}>
              <div style={{ fontSize: 11, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 8 }}>
                Payload summary
              </div>
              {usedFlowNames.map(name => {
                const count   = apis.filter(a => a.flow_name === name).length
                const hasSteps = !!savedFlows.find(f => f.name === name)?.steps.length
                return (
                  <div key={name} className="hint mt4" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <span style={{ fontFamily: 'monospace', color: hasSteps ? 'var(--text)' : '#f97316' }}>
                      {name}
                    </span>
                    <span style={{ color: 'var(--muted)' }}>
                      {count} API{count !== 1 ? 's' : ''}{!hasSteps ? ' ⚠ no steps' : ''}
                    </span>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>

      {/* Release + Deploy panel */}
      <div className="panel">
        <div className="panel-header">Release + Deploy</div>
        <div className="panel-body">
          <input
            className="input"
            placeholder="release id (auto-generated if empty)"
            value={releaseId}
            onChange={e => setReleaseId(e.target.value)}
          />
          <input
            className="input mt8"
            placeholder="instruction set version (optional)"
            value={instrVersion}
            onChange={e => setInstrVersion(e.target.value)}
          />
          <textarea
            className="input mt8"
            placeholder='api versions JSON e.g. {"orders":"v2"}'
            value={apiVersionsRaw}
            onChange={e => setApiVersionsRaw(e.target.value)}
            style={{ minHeight: 60 }}
          />

          <button className="btn mt8" onClick={handleReleaseDeploy} disabled={deploying}>
            {deploying ? 'Deploying…' : 'Create Release + Deploy'}
          </button>

          {releases.length > 0 && (
            <select
              className="input mt8"
              value={releaseId}
              onChange={e => setReleaseId(e.target.value)}
            >
              <option value="">— select existing release —</option>
              {releases.map(r => (
                <option key={r.release_id} value={r.release_id}>
                  {r.release_id}{r.instruction_set_version ? ` (${r.instruction_set_version})` : ''}
                </option>
              ))}
            </select>
          )}

          <button className="btn muted mt8" onClick={handleDeployExisting} disabled={deploying}>
            Deploy Existing Release
          </button>
          <button className="btn muted mt8" onClick={load}>Refresh</button>

          {status && (
            <p className={`mt8 ${statusErr ? 'status-err' : 'status-ok'}`}>{status}</p>
          )}

          {history.length > 0 && (
            <div className="mt12">
              <strong style={{ fontSize: 12 }}>Recent deploys</strong>
              {history.slice(0, 5).map((h, i) => (
                <div key={i} className="hint mt4">
                  {formatTime(h.at)} · {h.release_id}
                  {(h.results ?? []).map(r => (
                    <span key={r.target} style={{ marginLeft: 6 }}>
                      {r.target}:{r.error ? `❌${r.error}` : `✓${r.status}`}
                    </span>
                  ))}
                </div>
              ))}
            </div>
          )}

          {releases.length > 0 && (
            <div className="mt12">
              <strong style={{ fontSize: 12 }}>Releases</strong>
              {releases.slice(0, 20).map(r => (
                <div key={r.release_id} className="hint mt4">
                  {r.release_id}
                  {r.instruction_set_version && ` | instr=${r.instruction_set_version}`}
                  {r.api_versions && ` | apis=${Object.keys(r.api_versions).length}`}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
