import { useEffect, useState } from 'react'
import {
  listMessagingPublishers,
  addMessagingPublisher,
  updateMessagingPublisher,
  deleteMessagingPublisher,
} from '../api'
import type { MessagingPublisherDef } from '../api'

const YAML_SNIPPET = `messaging_publishers:
  - name: my-kafka
    kind: kafka
    brokers:
      - kafka1:9092
      - kafka2:9092
    tls_enabled: false

  - name: my-rabbitmq
    kind: rabbitmq
    uri: "amqp://user:pass@localhost:5672/"
    exchange: my-exchange

  - name: my-sqs
    kind: sqs
    region: us-east-1
    credential_ref: "env:AWS_CREDENTIALS"

  - name: my-pubsub
    kind: pubsub
    project_id: my-gcp-project
    credential_ref: "env:GOOGLE_APPLICATION_CREDENTIALS"

  - name: my-redis-streams
    kind: redis_streams
    redis_addr: localhost:6379
    redis_db: 0`

const KIND_COLORS: Record<string, { bg: string; text: string }> = {
  kafka:         { bg: 'rgba(250,179,135,0.1)', text: '#fab387' },
  rabbitmq:      { bg: 'rgba(166,227,161,0.1)', text: '#a6e3a1' },
  sqs:           { bg: 'rgba(250,179,135,0.15)', text: '#fab387' },
  pubsub:        { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' },
  redis_streams: { bg: 'rgba(243,139,168,0.1)', text: '#f38ba8' },
}

const KINDS = ['kafka', 'rabbitmq', 'sqs', 'pubsub', 'redis_streams'] as const

const EMPTY: MessagingPublisherDef = {
  name: '', kind: 'kafka',
  brokers: [], project_id: '', uri: '', exchange: '',
  region: '', redis_addr: '', redis_db: 0,
  credential_ref: '', tls_enabled: false, timeout_ms: undefined,
}

type FormState = MessagingPublisherDef & { brokersRaw: string }

function toForm(p: MessagingPublisherDef): FormState {
  return { ...p, brokersRaw: (p.brokers ?? []).join(', ') }
}

function fromForm(f: FormState): MessagingPublisherDef {
  const out: MessagingPublisherDef = { name: f.name, kind: f.kind }
  if (f.tls_enabled) out.tls_enabled = true
  if (f.timeout_ms)  out.timeout_ms = Number(f.timeout_ms)
  if (f.kind === 'kafka') {
    out.brokers = f.brokersRaw.split(',').map(s => s.trim()).filter(Boolean)
  }
  if (f.kind === 'rabbitmq') {
    if (f.uri)      out.uri      = f.uri
    if (f.exchange) out.exchange = f.exchange
  }
  if (f.kind === 'sqs') {
    if (f.region)         out.region         = f.region
    if (f.credential_ref) out.credential_ref = f.credential_ref
  }
  if (f.kind === 'pubsub') {
    if (f.project_id)     out.project_id     = f.project_id
    if (f.credential_ref) out.credential_ref = f.credential_ref
  }
  if (f.kind === 'redis_streams') {
    if (f.redis_addr)     out.redis_addr     = f.redis_addr
    if (f.redis_db != null) out.redis_db     = Number(f.redis_db)
    if (f.credential_ref) out.credential_ref = f.credential_ref
  }
  return out
}

const inputStyle: React.CSSProperties = {
  width: '100%', boxSizing: 'border-box', background: '#181825',
  border: '1px solid #313244', borderRadius: 6, color: '#cdd6f4',
  fontSize: 12, padding: '6px 8px', outline: 'none',
}
const labelStyle: React.CSSProperties = { fontSize: 11, color: '#a6adc8', marginBottom: 3, display: 'block' }
const fieldStyle: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 2 }
const btnPrimary: React.CSSProperties = {
  fontSize: 12, padding: '5px 14px', borderRadius: 6, border: 'none',
  background: '#89b4fa', color: '#1e1e2e', cursor: 'pointer', fontWeight: 600,
}
const btnSecondary: React.CSSProperties = {
  fontSize: 12, padding: '5px 14px', borderRadius: 6,
  border: '1px solid #313244', background: '#1e1e2e', color: '#cdd6f4', cursor: 'pointer',
}
const btnDanger: React.CSSProperties = {
  fontSize: 11, padding: '4px 10px', borderRadius: 5,
  border: '1px solid #f38ba8', background: 'transparent', color: '#f38ba8', cursor: 'pointer',
}
const btnEdit: React.CSSProperties = {
  fontSize: 11, padding: '4px 10px', borderRadius: 5,
  border: '1px solid #313244', background: 'transparent', color: '#cdd6f4', cursor: 'pointer',
}

export default function MessagingPublishers() {
  const [publishers, setPublishers] = useState<MessagingPublisherDef[]>([])
  const [loading, setLoading]       = useState(true)
  const [pageError, setPageError]   = useState<string | null>(null)
  const [showSnippet, setShowSnippet] = useState(false)

  // form state: null = hidden, 'add' = new, string = editing that name
  const [formMode, setFormMode]     = useState<null | 'add' | string>(null)
  const [form, setForm]             = useState<FormState>({ ...EMPTY, brokersRaw: '' })
  const [formError, setFormError]   = useState<string | null>(null)
  const [saving, setSaving]         = useState(false)

  function load() {
    setLoading(true)
    listMessagingPublishers()
      .then(r => setPublishers(r.publishers ?? []))
      .catch(e => setPageError(String(e)))
      .finally(() => setLoading(false))
  }

  useEffect(load, [])

  function openAdd() {
    setForm({ ...EMPTY, brokersRaw: '' })
    setFormError(null)
    setFormMode('add')
  }

  function openEdit(p: MessagingPublisherDef) {
    setForm(toForm(p))
    setFormError(null)
    setFormMode(p.name)
  }

  function closeForm() {
    setFormMode(null)
    setFormError(null)
  }

  function set<K extends keyof FormState>(key: K, val: FormState[K]) {
    setForm(f => ({ ...f, [key]: val }))
  }

  async function handleSave() {
    setFormError(null)
    if (!form.name.trim()) { setFormError('Name is required'); return }
    if (!form.kind)         { setFormError('Kind is required'); return }
    setSaving(true)
    try {
      const cfg = fromForm(form)
      if (formMode === 'add') {
        await addMessagingPublisher(cfg)
      } else {
        await updateMessagingPublisher(formMode as string, cfg)
      }
      closeForm()
      load()
    } catch (e) {
      setFormError(String(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(name: string) {
    if (!window.confirm(`Delete publisher "${name}"?`)) return
    try {
      await deleteMessagingPublisher(name)
      load()
    } catch (e) {
      setPageError(String(e))
    }
  }

  if (loading) return <div style={{ padding: '20px 24px', color: '#a6adc8', fontSize: 13 }}>Loading…</div>
  if (pageError) return <div style={{ padding: '20px 24px', color: '#f38ba8', fontSize: 13 }}>Error: {pageError}</div>

  return (
    <div style={{ padding: '20px 24px', height: '100%', overflowY: 'auto' }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>Messaging Publishers</h2>
          <p style={{ fontSize: 12, color: '#6c7086' }}>
            Defined in <code>gateway.yaml</code> → <code>messaging_publishers</code>. Used in <code>message_publish</code> flow steps and referenced by Event Listeners.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button style={btnPrimary} onClick={openAdd}>+ Add Publisher</button>
          <button
            style={btnSecondary}
            onClick={() => setShowSnippet(v => !v)}
          >{showSnippet ? 'Hide config' : 'How to configure'}</button>
        </div>
      </div>

      {/* YAML snippet */}
      {showSnippet && (
        <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 14, marginBottom: 16 }}>
          <div style={{ fontSize: 11, color: '#6c7086', marginBottom: 8 }}>Add to gateway.yaml, then restart the gateway:</div>
          <pre style={{ margin: 0, fontSize: 12, color: '#cdd6f4', whiteSpace: 'pre', overflowX: 'auto' }}>{YAML_SNIPPET}</pre>
          <div style={{ fontSize: 11, color: '#6c7086', marginTop: 8 }}>
            Supported kinds: <code>kafka</code>, <code>rabbitmq</code>, <code>sqs</code>, <code>pubsub</code>, <code>redis_streams</code>.
          </div>
        </div>
      )}

      {/* Inline form */}
      {formMode !== null && (
        <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 16, marginBottom: 16 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: '#cdd6f4', marginBottom: 12 }}>
            {formMode === 'add' ? 'New Publisher' : `Edit: ${formMode}`}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
            {/* Name */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Name *</label>
              <input
                style={{ ...inputStyle, opacity: formMode !== 'add' ? 0.5 : 1 }}
                value={form.name}
                disabled={formMode !== 'add'}
                onChange={e => set('name', e.target.value)}
                placeholder="my-publisher"
              />
            </div>
            {/* Kind */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Kind *</label>
              <select
                style={{ ...inputStyle }}
                value={form.kind}
                onChange={e => set('kind', e.target.value)}
              >
                {KINDS.map(k => <option key={k} value={k}>{k}</option>)}
              </select>
            </div>

            {/* Kafka: brokers */}
            {form.kind === 'kafka' && (
              <div style={{ ...fieldStyle, gridColumn: '1 / -1' }}>
                <label style={labelStyle}>Brokers (comma-separated)</label>
                <textarea
                  style={{ ...inputStyle, minHeight: 52, resize: 'vertical' }}
                  value={form.brokersRaw}
                  onChange={e => set('brokersRaw', e.target.value)}
                  placeholder="kafka1:9092, kafka2:9092"
                />
              </div>
            )}

            {/* RabbitMQ */}
            {form.kind === 'rabbitmq' && <>
              <div style={fieldStyle}>
                <label style={labelStyle}>URI</label>
                <input style={inputStyle} value={form.uri ?? ''} onChange={e => set('uri', e.target.value)} placeholder="amqp://user:pass@host:5672/" />
              </div>
              <div style={fieldStyle}>
                <label style={labelStyle}>Exchange</label>
                <input style={inputStyle} value={form.exchange ?? ''} onChange={e => set('exchange', e.target.value)} placeholder="my-exchange" />
              </div>
            </>}

            {/* SQS */}
            {form.kind === 'sqs' && <>
              <div style={fieldStyle}>
                <label style={labelStyle}>Region</label>
                <input style={inputStyle} value={form.region ?? ''} onChange={e => set('region', e.target.value)} placeholder="us-east-1" />
              </div>
              <div style={fieldStyle}>
                <label style={labelStyle}>Credential Ref</label>
                <input style={inputStyle} value={form.credential_ref ?? ''} onChange={e => set('credential_ref', e.target.value)} placeholder="env:AWS_CREDENTIALS" />
              </div>
            </>}

            {/* PubSub */}
            {form.kind === 'pubsub' && <>
              <div style={fieldStyle}>
                <label style={labelStyle}>Project ID</label>
                <input style={inputStyle} value={form.project_id ?? ''} onChange={e => set('project_id', e.target.value)} placeholder="my-gcp-project" />
              </div>
              <div style={fieldStyle}>
                <label style={labelStyle}>Credential Ref</label>
                <input style={inputStyle} value={form.credential_ref ?? ''} onChange={e => set('credential_ref', e.target.value)} placeholder="env:GOOGLE_APPLICATION_CREDENTIALS" />
              </div>
            </>}

            {/* Redis Streams */}
            {form.kind === 'redis_streams' && <>
              <div style={fieldStyle}>
                <label style={labelStyle}>Redis Addr</label>
                <input style={inputStyle} value={form.redis_addr ?? ''} onChange={e => set('redis_addr', e.target.value)} placeholder="localhost:6379" />
              </div>
              <div style={fieldStyle}>
                <label style={labelStyle}>Redis DB</label>
                <input style={inputStyle} type="number" value={form.redis_db ?? 0} onChange={e => set('redis_db', Number(e.target.value))} placeholder="0" />
              </div>
              <div style={fieldStyle}>
                <label style={labelStyle}>Credential Ref</label>
                <input style={inputStyle} value={form.credential_ref ?? ''} onChange={e => set('credential_ref', e.target.value)} placeholder="env:REDIS_PASS" />
              </div>
            </>}

            {/* TLS (always shown) */}
            <div style={{ ...fieldStyle, justifyContent: 'flex-end', paddingBottom: 2 }}>
              <label style={{ ...labelStyle, marginBottom: 0 }}>&nbsp;</label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: '#cdd6f4', cursor: 'pointer' }}>
                <input
                  type="checkbox"
                  checked={!!form.tls_enabled}
                  onChange={e => set('tls_enabled', e.target.checked)}
                  style={{ accentColor: '#89b4fa' }}
                />
                TLS Enabled
              </label>
            </div>

            {/* Timeout */}
            <div style={fieldStyle}>
              <label style={labelStyle}>Timeout MS (optional)</label>
              <input
                style={inputStyle} type="number"
                value={form.timeout_ms ?? ''}
                onChange={e => set('timeout_ms', e.target.value === '' ? undefined : Number(e.target.value))}
                placeholder="5000"
              />
            </div>
          </div>

          {formError && (
            <div style={{ fontSize: 12, color: '#f38ba8', marginBottom: 10 }}>{formError}</div>
          )}

          <div style={{ display: 'flex', gap: 8 }}>
            <button style={btnPrimary} onClick={handleSave} disabled={saving}>
              {saving ? 'Saving…' : 'Save'}
            </button>
            <button style={btnSecondary} onClick={closeForm} disabled={saving}>Cancel</button>
          </div>
        </div>
      )}

      {/* List */}
      {publishers.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>
          No messaging publishers configured.{' '}
          <span style={{ cursor: 'pointer', textDecoration: 'underline', color: '#89b4fa' }} onClick={openAdd}>
            Add one now.
          </span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {publishers.map(p => {
            const colors = KIND_COLORS[p.kind] ?? { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' }
            return (
              <div key={p.name} style={{ background: '#1e1e2e', border: '1px solid #313244', borderRadius: 8, padding: '10px 14px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>{p.name}</span>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontSize: 11, color: colors.text, background: colors.bg, padding: '2px 8px', borderRadius: 4 }}>{p.kind}</span>
                  <button style={btnEdit} onClick={() => openEdit(p)}>Edit</button>
                  <button style={btnDanger} onClick={() => handleDelete(p.name)}>Delete</button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
