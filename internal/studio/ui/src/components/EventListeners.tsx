import React, { useEffect, useState } from 'react'
import { listEventListeners } from '../api'
import type { EventListenerDef } from '../api'

export default function EventListeners() {
  const [listeners, setListeners] = useState<EventListenerDef[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listEventListeners()
      .then(r => setListeners(r.listeners ?? []))
      .catch(e => setError(String(e)))
      .finally(() => setLoading(false))
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
        <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 6 }}>Event Listeners</h2>
        <p style={{ fontSize: 13, color: '#6c7086', marginBottom: 12 }}>Configured via gateway.yaml — read only.</p>
      </div>

      {listeners.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>No event listeners configured.</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {listeners.map(l => (
            <div
              key={l.name}
              style={{
                background: '#1e1e2e',
                border: '1px solid #313244',
                borderRadius: 8,
                padding: 12,
                display: 'flex',
                flexDirection: 'column',
              }}
            >
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 500 }}>{l.name}</span>
                <span style={{
                  fontSize: 11,
                  color: '#f5c2e7',
                  background: 'rgba(245,194,231,0.1)',
                  padding: '2px 8px',
                  borderRadius: 4,
                }}>{l.publisher} → {l.topic}</span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <span style={{ fontSize: 12, color: '#a6adc8' }}>Flow: <span style={{ color: '#94e2d5' }}>{l.flow_name}</span></span>
                <span style={{ fontSize: 11, color: '#b4befe' }}>Workers: {l.workers}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
