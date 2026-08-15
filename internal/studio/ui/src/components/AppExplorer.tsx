import React, { useEffect, useState } from 'react'
import { listAppFlows } from '../api'
import type { AppDef } from '../api'

export default function AppExplorer() {
  const [apps, setApps] = useState<AppDef[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    listAppFlows()
      .then(r => setApps(r.apps ?? []))
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
        <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 6 }}>App Explorer</h2>
        <p style={{ fontSize: 13, color: '#6c7086', marginBottom: 12 }}>Flows grouped by app.</p>
      </div>

      {apps.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>
          No apps configured. Add `app_name` to flow constants to group flows by app.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {apps.map(app => (
            <div
              key={app.name}
              style={{
                background: '#1e1e2e',
                border: '1px solid #313244',
                borderRadius: 8,
                padding: 16,
              }}
            >
              <div style={{ fontSize: 14, fontWeight: 700, color: '#cdd6f4', marginBottom: 10 }}>
                {app.name}
              </div>
              {app.flows.length === 0 ? (
                <div style={{ fontSize: 12, color: '#6c7086' }}>No flows</div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                  {app.flows.map(flow => (
                    <div key={flow} style={{ fontSize: 12, color: '#a6adc8' }}>
                      • {flow}
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
