import { useEffect, useState } from 'react'
import ConfirmDialog from './ConfirmDialog'
import { listSchedules, upsertSchedule, deleteSchedule, getScheduleHistory } from '../api'
import type { Schedule, ScheduleHistory } from '../api'

export default function Schedules() {
  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<Schedule>(blankSchedule())
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState('')
  const [msgErr, setMsgErr] = useState(false)
  const [expandedHistory, setExpandedHistory] = useState<string | null>(null)
  const [history, setHistory] = useState<Record<string, ScheduleHistory[]>>({})
  const [loadingHistory, setLoadingHistory] = useState<Record<string, boolean>>({})
  const [confirmDialog, setConfirmDialog] = useState<{ title: string; message: string; onConfirm: () => void } | null>(null)
  const [constants, setConstants] = useState<{ key: string; value: string }[]>([])

  async function load() {
    setLoading(true)
    setErr('')
    try {
      setSchedules(await listSchedules())
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to load schedules')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  function setF<K extends keyof Schedule>(k: K, v: Schedule[K]) {
    setForm(f => ({ ...f, [k]: v }))
  }

  async function handleSave() {
    if (!form.name.trim()) {
      setMsgErr(true)
      setMsg('Schedule name is required')
      return
    }
    if (!form.cron.trim()) {
      setMsgErr(true)
      setMsg('Cron expression is required')
      return
    }
    if (!form.flow_name.trim()) {
      setMsgErr(true)
      setMsg('Flow name is required')
      return
    }
    if (!form.tenant_alias.trim()) {
      setMsgErr(true)
      setMsg('Tenant alias is required')
      return
    }

    setSaving(true)
    setMsg('')
    setMsgErr(false)

    try {
      const constObj = constants.reduce((acc, { key, value }) => {
        if (key.trim()) acc[key] = value
        return acc
      }, {} as Record<string, string>)

      await upsertSchedule({
        ...form,
        constants: Object.keys(constObj).length > 0 ? constObj : undefined,
      })
      setMsg(`Schedule "${form.name}" saved.`)
      setForm(blankSchedule())
      setConstants([])
      setShowForm(false)
      await load()
    } catch (e) {
      setMsgErr(true)
      setMsg(e instanceof Error ? e.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(name: string) {
    setConfirmDialog({
      title: 'Delete Schedule',
      message: `Delete schedule "${name}"?`,
      onConfirm: async () => {
        try {
          await deleteSchedule(name)
          await load()
        } catch (e) {
          alert(e instanceof Error ? e.message : 'Delete failed')
        }
        setConfirmDialog(null)
      },
    })
  }

  async function loadHistory(name: string) {
    if (history[name]) {
      setExpandedHistory(expandedHistory === name ? null : name)
      return
    }
    setLoadingHistory(p => ({ ...p, [name]: true }))
    try {
      const h = await getScheduleHistory(name)
      setHistory(p => ({ ...p, [name]: h }))
      setExpandedHistory(name)
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Failed to load history')
    } finally {
      setLoadingHistory(p => ({ ...p, [name]: false }))
    }
  }

  function openEditForm(s: Schedule) {
    setForm({ ...s })
    const consts = Object.entries(s.constants ?? {}).map(([key, value]) => ({ key, value }))
    setConstants(consts.length > 0 ? consts : [{ key: '', value: '' }])
    setShowForm(true)
  }

  return (
    <div style={{ padding: '20px 24px' }}>
      <SectionHeader
        title="Scheduled Flows"
        count={schedules.length}
        hint="Automatically trigger flows on a cron schedule"
        onAdd={() => {
          setForm(blankSchedule())
          setConstants([{ key: '', value: '' }])
          setShowForm(v => !v)
        }}
        addLabel={showForm ? 'Cancel' : '+ Create Schedule'}
      />
      {err && <p className="status-err" style={{ marginBottom: 12 }}>{err}</p>}

      {showForm && (
        <div className="panel" style={{ marginBottom: 20 }}>
          <div className="panel-header">{form.name || 'New Schedule'}</div>
          <div className="panel-body">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <Field label="Schedule Name *" hint="Unique identifier">
                <input
                  className="input"
                  value={form.name}
                  placeholder="e.g. daily-sync"
                  onChange={e => setF('name', e.target.value)}
                />
              </Field>
              <Field label="Cron Expression *" hint="Standard cron format (e.g. 0 9 * * *)">
                <input
                  className="input"
                  value={form.cron}
                  placeholder="0 9 * * * (daily at 9 AM)"
                  onChange={e => setF('cron', e.target.value)}
                />
              </Field>

              <Field label="Flow Name *" hint="Flow to execute">
                <input
                  className="input"
                  value={form.flow_name}
                  placeholder="e.g. sync-handler"
                  onChange={e => setF('flow_name', e.target.value)}
                />
              </Field>
              <Field label="Tenant Alias *" hint="Tenant context for this execution">
                <input
                  className="input"
                  value={form.tenant_alias}
                  placeholder="e.g. customer-42"
                  onChange={e => setF('tenant_alias', e.target.value)}
                />
              </Field>

              <Field label="Timeout (seconds)" hint="Max execution time">
                <input
                  className="input"
                  type="number"
                  value={form.timeout_sec}
                  onChange={e => setF('timeout_sec', parseInt(e.target.value) || 30)}
                />
              </Field>
              <Field label="Enabled" hint="Activate this schedule">
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
                  <input
                    type="checkbox"
                    checked={form.enabled}
                    onChange={e => setF('enabled', e.target.checked)}
                    style={{ width: 18, height: 18, cursor: 'pointer' }}
                  />
                  <span style={{ fontSize: 13 }}>Schedule is active</span>
                </div>
              </Field>
            </div>

            {/* On Failure */}
            <details style={{ marginTop: 14, marginBottom: 14 }}>
              <summary style={{ fontSize: 11, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 8, cursor: 'pointer', userSelect: 'none' }}>
                On Failure
              </summary>
              <div style={{ marginTop: 8, paddingLeft: 8 }}>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
                  <Field label="Retry Count" hint="Number of retries on failure">
                    <input
                      className="input"
                      type="number"
                      min="0"
                      value={form.on_failure?.retry_count ?? 0}
                      onChange={e => setF('on_failure', {
                        ...form.on_failure,
                        retry_count: Math.max(0, parseInt(e.target.value) || 0),
                      })}
                    />
                  </Field>
                  <Field label="Retry Interval (sec)" hint="Delay between retries">
                    <input
                      className="input"
                      type="number"
                      min="0"
                      value={form.on_failure?.retry_interval_sec ?? 30}
                      onChange={e => setF('on_failure', {
                        ...form.on_failure,
                        retry_interval_sec: Math.max(0, parseInt(e.target.value) || 30),
                      })}
                    />
                  </Field>
                </div>
                <Field label="Dead Letter Flow" hint="Flow to run when all retries exhausted">
                  <input
                    className="input"
                    value={form.on_failure?.dead_letter_flow ?? ''}
                    placeholder="e.g. error-handler"
                    onChange={e => setF('on_failure', {
                      ...form.on_failure,
                      dead_letter_flow: e.target.value,
                    })}
                  />
                </Field>
              </div>
            </details>

            {/* Max Concurrent */}
            <div style={{ marginBottom: 14 }}>
              <Field label="Max Concurrent" hint="Maximum concurrent executions (default 1)">
                <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                  <input
                    className="input"
                    type="number"
                    min="1"
                    value={form.max_concurrent ?? 1}
                    onChange={e => setF('max_concurrent', Math.max(1, parseInt(e.target.value) || 1))}
                    style={{ flex: 1 }}
                  />
                  <span style={{ fontSize: 11, color: 'var(--muted)' }}>Prevents overlapping runs</span>
                </div>
              </Field>
            </div>

            {/* Constants */}
            <div style={{ marginTop: 14 }}>
              <div style={{ fontSize: 11, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 8 }}>
                Constants (key-value pairs)
              </div>
              {constants.map((c, idx) => (
                <div key={idx} style={{ display: 'flex', gap: 6, alignItems: 'center', marginBottom: 4 }}>
                  <input
                    className="input"
                    value={c.key}
                    placeholder="key"
                    onChange={e => {
                      const updated = [...constants]
                      updated[idx] = { ...updated[idx], key: e.target.value }
                      setConstants(updated)
                    }}
                    style={{ flex: '0 0 140px', fontSize: 12 }}
                  />
                  <input
                    className="input"
                    value={c.value}
                    placeholder="value"
                    onChange={e => {
                      const updated = [...constants]
                      updated[idx] = { ...updated[idx], value: e.target.value }
                      setConstants(updated)
                    }}
                    style={{ flex: 1, fontSize: 12 }}
                  />
                  <button
                    type="button"
                    style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--muted)', fontSize: 14, padding: '0 4px' }}
                    onClick={() => setConstants(constants.filter((_, i) => i !== idx))}
                  >
                    ✕
                  </button>
                </div>
              ))}
              <button
                type="button"
                className="btn"
                style={{ fontSize: 11, padding: '2px 8px', marginTop: 6, width: 'auto' }}
                onClick={() => setConstants([...constants, { key: '', value: '' }])}
              >
                + Add Constant
              </button>
            </div>

            <div style={{ marginTop: 16, display: 'flex', gap: 10, alignItems: 'center' }}>
              <button className="btn" style={{ width: 'auto', padding: '0 24px' }} onClick={handleSave} disabled={saving}>
                {saving ? 'Saving…' : 'Save Schedule'}
              </button>
              {msg && <span className={msgErr ? 'status-err' : 'status-ok'}>{msg}</span>}
            </div>
          </div>
        </div>
      )}

      {loading ? (
        <p className="hint">Loading…</p>
      ) : schedules.length === 0 ? (
        <EmptyState icon="⏱" text="No schedules configured yet." />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={{ display: 'grid', gridTemplateColumns: '140px 120px 140px 100px 80px 80px 60px', gap: 6, fontSize: 11, color: 'var(--muted)', fontWeight: 600, marginBottom: 6, paddingLeft: 4 }}>
            <span>Name</span>
            <span>Cron</span>
            <span>Flow</span>
            <span>Tenant</span>
            <span>Enabled</span>
            <span>Last Run</span>
            <span></span>
          </div>
          {schedules.map(s => (
            <div key={s.name}>
              <div style={{ display: 'grid', gridTemplateColumns: '140px 120px 140px 100px 80px 80px 60px', gap: 6, alignItems: 'center', padding: '10px 8px', background: 'var(--block-bg)', border: '1px solid var(--border)', borderRadius: 6 }}>
                <span style={{ fontFamily: 'monospace', fontSize: 13, fontWeight: 600 }}>{s.name}</span>
                <span style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--muted)' }}>{s.cron}</span>
                <span style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--accent)' }}>{s.flow_name}</span>
                <span style={{ fontSize: 12 }}>{s.tenant_alias}</span>
                <span style={{ fontSize: 11, color: s.enabled ? '#22c55e' : 'var(--muted)' }}>
                  {s.enabled ? '✓ On' : '○ Off'}
                </span>
                <span style={{ fontSize: 11, color: 'var(--muted)' }}>
                  {s.last_run_at ? new Date(s.last_run_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : '—'}
                </span>
                <div style={{ display: 'flex', gap: 4 }}>
                  <button
                    className="btn muted"
                    style={{ width: 'auto', padding: '3px 8px', marginTop: 0, fontSize: 11 }}
                    onClick={() => loadHistory(s.name)}
                  >
                    {loadingHistory[s.name] ? '…' : '◆'}
                  </button>
                </div>
              </div>

              {/* History panel */}
              {expandedHistory === s.name && (
                <div style={{ padding: '12px', background: 'rgba(0,0,0,0.1)', borderRadius: '0 0 6px 6px', border: '1px solid var(--border)', borderTop: 'none' }}>
                  {loadingHistory[s.name] ? (
                    <p className="hint">Loading…</p>
                  ) : history[s.name] && history[s.name].length === 0 ? (
                    <p className="hint" style={{ margin: 0 }}>No execution history.</p>
                  ) : (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                      {(history[s.name] ?? []).slice(0, 10).map((h, idx) => (
                        <div key={idx} style={{ padding: '6px 8px', background: 'var(--surface)', borderRadius: 4, fontSize: 11 }}>
                          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                            <span style={{ color: h.status === 'success' ? '#22c55e' : h.status === 'running' ? '#f59e0b' : '#ef4444', fontWeight: 600 }}>
                              {h.status}
                            </span>
                            <span style={{ color: 'var(--muted)', fontSize: 10 }}>
                              {h.completed_at ? new Date(h.completed_at).toLocaleTimeString() : new Date(h.scheduled_at).toLocaleTimeString()}
                            </span>
                          </div>
                          {h.duration_ms && (
                            <div style={{ color: 'var(--muted)', fontSize: 10, marginTop: 2 }}>
                              {h.duration_ms}ms
                            </div>
                          )}
                          {h.error && (
                            <div style={{ color: '#ef4444', fontSize: 10, marginTop: 4, fontFamily: 'monospace', wordBreak: 'break-word' }}>
                              {h.error}
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  )}

                  <div style={{ display: 'flex', gap: 6, marginTop: 8 }}>
                    <button
                      className="btn muted"
                      style={{ width: 'auto', padding: '3px 10px', fontSize: 11 }}
                      onClick={() => setExpandedHistory(null)}
                    >
                      Hide
                    </button>
                    <button
                      className="btn muted"
                      style={{ width: 'auto', padding: '3px 10px', fontSize: 11 }}
                      onClick={() => openEditForm(s)}
                    >
                      Edit
                    </button>
                    <button
                      className="btn muted"
                      style={{ width: 'auto', padding: '3px 8px', fontSize: 11, color: '#ef4444' }}
                      onClick={() => handleDelete(s.name)}
                    >
                      ✕
                    </button>
                  </div>
                </div>
              )}

              {/* Collapsed row buttons */}
              {expandedHistory !== s.name && (
                <div style={{ padding: '8px 8px', display: 'flex', gap: 4 }}>
                  <button className="btn muted" style={{ width: 'auto', padding: '3px 10px', fontSize: 11 }} onClick={() => openEditForm(s)}>
                    Edit
                  </button>
                  <button className="btn muted" style={{ width: 'auto', padding: '3px 8px', fontSize: 11, color: '#ef4444' }} onClick={() => handleDelete(s.name)}>
                    ✕
                  </button>
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

// ── Helpers ────────────────────────────────────────────────────────

function blankSchedule(): Schedule {
  return {
    name: '',
    cron: '',
    flow_name: '',
    tenant_alias: '',
    enabled: true,
    timeout_sec: 30,
    on_failure: {
      retry_count: 0,
      retry_interval_sec: 30,
    },
    max_concurrent: 1,
  }
}

function SectionHeader({ title, count, hint, onAdd, addLabel }: {
  title: string; count: number; hint: string; onAdd: () => void; addLabel: string
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 20 }}>
      <div>
        <span style={{ fontSize: 15, fontWeight: 700 }}>{title}</span>
        <span style={{ color: 'var(--muted)', fontSize: 13, marginLeft: 10 }}>{count} total</span>
        <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>{hint}</div>
      </div>
      <button className="btn" style={{ width: 'auto', padding: '0 18px', flexShrink: 0 }} onClick={onAdd}>
        {addLabel}
      </button>
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
