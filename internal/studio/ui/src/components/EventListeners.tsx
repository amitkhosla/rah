import { useEffect, useState } from 'react'
import { listEventListeners } from '../api'
import type { EventListenerDef } from '../api'

const YAML_SNIPPET = `# First configure a messaging_publisher (e.g. Kafka), then add event listeners:
event_listeners:
  - name: order-created-listener
    publisher: my-kafka        # references a messaging_publishers entry
    topic: orders.created
    flow_name: handle_order_created
    payload_var: order_payload
    group_id: studio-consumers
    workers: 2

  - name: payment-listener
    publisher: my-kafka
    topic: payments.processed
    flow_name: handle_payment
    payload_var: payment_data
    workers: 1
    # Optional deduplication:
    dedup_key: "{payment_id}"
    dedup_window_sec: 300`

export default function EventListeners() {
  const [listeners, setListeners] = useState<EventListenerDef[]>([])
  const [loading, setLoading]     = useState(true)
  const [error, setError]         = useState<string | null>(null)
  const [showSnippet, setShowSnippet] = useState(false)

  useEffect(() => {
    listEventListeners()
      .then(r => setListeners(r.listeners ?? []))
      .catch(e => setError(String(e)))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <div style={{ padding: '20px 24px', color: '#a6adc8', fontSize: 13 }}>Loading…</div>

  if (error) return <div style={{ padding: '20px 24px', color: '#f38ba8', fontSize: 13 }}>Error: {error}</div>

  return (
    <div style={{ padding: '20px 24px', height: '100%', overflowY: 'auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 16 }}>
        <div>
          <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>Event Listeners</h2>
          <p style={{ fontSize: 12, color: '#6c7086' }}>Defined in <code>gateway.yaml</code> → <code>event_listeners</code>. Each listener subscribes to a topic and triggers a flow on each message.</p>
        </div>
        <button
          style={{ fontSize: 12, padding: '5px 12px', border: '1px solid #313244', borderRadius: 6, background: '#1e1e2e', color: '#cdd6f4', cursor: 'pointer' }}
          onClick={() => setShowSnippet(v => !v)}
        >{showSnippet ? 'Hide config' : 'How to configure'}</button>
      </div>

      {showSnippet && (
        <div style={{ background: '#181825', border: '1px solid #313244', borderRadius: 8, padding: 14, marginBottom: 16 }}>
          <div style={{ fontSize: 11, color: '#6c7086', marginBottom: 8 }}>Add to gateway.yaml, then restart the gateway:</div>
          <pre style={{ margin: 0, fontSize: 12, color: '#cdd6f4', whiteSpace: 'pre', overflowX: 'auto' }}>{YAML_SNIPPET}</pre>
          <div style={{ fontSize: 11, color: '#6c7086', marginTop: 8 }}>
            Requires a <code>messaging_publishers</code> entry to be configured first.
          </div>
        </div>
      )}

      {listeners.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>
          No event listeners configured.{' '}
          <span style={{ cursor: 'pointer', textDecoration: 'underline', color: '#89b4fa' }} onClick={() => setShowSnippet(true)}>
            See how to add one.
          </span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {listeners.map(l => (
            <div key={l.name} style={{ background: '#1e1e2e', border: '1px solid #313244', borderRadius: 8, padding: 12 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
                <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>{l.name}</span>
                <span style={{ fontSize: 11, color: '#f5c2e7', background: 'rgba(245,194,231,0.1)', padding: '2px 8px', borderRadius: 4 }}>
                  {l.publisher} → {l.topic}
                </span>
              </div>
              <div style={{ display: 'flex', gap: 16, fontSize: 12, color: '#a6adc8' }}>
                <span>Flow: <span style={{ color: '#94e2d5' }}>{l.flow_name}</span></span>
                <span>Workers: <span style={{ color: '#cdd6f4' }}>{l.workers || 1}</span></span>
                {l.group_id && <span>Group: <span style={{ color: '#cdd6f4' }}>{l.group_id}</span></span>}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
