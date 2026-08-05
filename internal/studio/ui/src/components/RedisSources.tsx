import React, { useEffect, useState } from 'react'
import { listRedisSources } from '../api'

export default function RedisSources() {
  const [sources, setSources] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listRedisSources()
      .then(r => setSources(r.sources ?? []))
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
        <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 6 }}>Redis Sources</h2>
        <p style={{ fontSize: 13, color: '#6c7086', marginBottom: 12 }}>Configured via gateway.yaml — read only.</p>
      </div>

      {sources.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>No Redis sources configured.</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {sources.map(name => (
            <div
              key={name}
              style={{
                background: '#1e1e2e',
                border: '1px solid #313244',
                borderRadius: 8,
                padding: 12,
                fontSize: 13,
                color: '#cdd6f4',
              }}
            >
              {name}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
