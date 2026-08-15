import React, { useEffect, useState } from 'react'
import { listStorageConnectors } from '../api'
import type { StorageConnectorDef } from '../api'

export default function StorageConnectors() {
  const [connectors, setConnectors] = useState<StorageConnectorDef[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listStorageConnectors()
      .then(r => setConnectors(r.connectors ?? []))
      .catch(e => setError(String(e)))
      .finally(() => setLoading(false))
  }, [])

  const getBadgeColor = (type: string): { bg: string; text: string } => {
    switch (type) {
      case 's3':
        return { bg: 'rgba(250,179,135,0.1)', text: '#fab387' }
      case 'gcs':
        return { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' }
      case 'local':
        return { bg: 'rgba(166,227,161,0.1)', text: '#a6e3a1' }
      default:
        return { bg: 'rgba(137,180,250,0.1)', text: '#89b4fa' }
    }
  }

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
        <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 6 }}>Storage Connectors</h2>
        <p style={{ fontSize: 13, color: '#6c7086', marginBottom: 12 }}>Configured via gateway.yaml — read only.</p>
      </div>

      {connectors.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>No storage connectors configured.</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {connectors.map(c => {
            const colors = getBadgeColor(c.type)
            return (
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
                <span style={{ fontSize: 13, color: '#cdd6f4', fontWeight: 600 }}>{c.name}</span>
                <span style={{
                  fontSize: 11,
                  color: colors.text,
                  background: colors.bg,
                  padding: '2px 8px',
                  borderRadius: 4,
                }}>{c.type}</span>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
