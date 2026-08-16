import React, { useEffect, useState } from 'react'
import { listDocumentConnectors } from '../api'
import type { DocumentConnectorDef } from '../api'

export default function DocumentConnectors() {
  const [connectors, setConnectors] = useState<DocumentConnectorDef[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listDocumentConnectors()
      .then(r => setConnectors(r.connectors ?? []))
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
        <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 6 }}>Document Connectors</h2>
        <p style={{ fontSize: 13, color: '#6c7086', marginBottom: 12 }}>Configured via gateway.yaml — read only.</p>
      </div>

      {connectors.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>No document connectors configured.</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {connectors.map(c => (
            <div
              key={c.name}
              style={{
                background: '#1e1e2e',
                border: '1px solid #313244',
                borderRadius: 8,
                padding: 12,
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
              }}
            >
              <span style={{ fontSize: 13, color: '#cdd6f4' }}>{c.name}</span>
              <span style={{
                fontSize: 11,
                color: '#89b4fa',
                background: 'rgba(137,180,250,0.1)',
                padding: '2px 8px',
                borderRadius: 4,
              }}>{c.kind}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
