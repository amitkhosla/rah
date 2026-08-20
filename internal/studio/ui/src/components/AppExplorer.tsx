import React, { useEffect, useState } from 'react'
import { listApps, fetchGatewaySnapshot } from '../api'
import type { App, GatewayApi } from '../types'

interface Props {
  onNavigate?: (tab: string, appName?: string) => void
}

// Catppuccin palette
const PALETTE = {
  bg: '#1e1e2e',
  border: '#313244',
  borderHover: '#89b4fa',
  text: '#cdd6f4',
  textDim: '#6c7086',
  textMuted: '#a6adc8',
  sectionHeaderColor: '#89b4fa',
}

const METHOD_COLORS: Record<string, string> = {
  GET: '#4caf50',
  POST: '#2196f3',
  PUT: '#ff9800',
  PATCH: '#9c27b0',
  DELETE: '#f44336',
}

interface GatewaySnapshot {
  flows: Array<{ name: string }>
  apis: Array<{
    name: string
    path: string
    method?: string
    flow_name: string
    app_name?: string
    [key: string]: any
  }>
}

export default function AppExplorer({ onNavigate }: Props) {
  const [apps, setApps] = useState<App[]>([])
  const [apiList, setApiList] = useState<GatewayApi[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    Promise.all([listApps(), fetchGatewaySnapshot()])
      .then(([appList, snapshot]) => {
        setApps(appList)
        setApiList((snapshot?.apis ?? []) as GatewayApi[])
      })
      .catch(e => setError(String(e)))
      .finally(() => setLoading(false))
  }, [])

  if (loading) {
    return (
      <div style={{ padding: '20px 24px', color: PALETTE.textMuted, fontSize: 13 }}>
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
        <h2 style={{ fontSize: 16, fontWeight: 700, color: PALETTE.text, marginBottom: 6 }}>
          App Explorer
        </h2>
        <p style={{ fontSize: 13, color: PALETTE.textDim, marginBottom: 12 }}>
          Flows grouped by app. Click an app to manage it.
        </p>
      </div>

      {apps.length === 0 ? (
        <div style={{ color: PALETTE.textDim, fontSize: 13 }}>
          No apps configured. Add <code>app_name</code> to your API YAML to group flows by app.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {apps.map(app => (
            <div
              key={app.app_id}
              style={{
                background: PALETTE.bg,
                border: `1px solid ${PALETTE.border}`,
                borderRadius: 8,
                padding: 16,
                cursor: onNavigate ? 'pointer' : 'default',
                transition: 'border-color 0.15s',
              }}
              onMouseEnter={e => {
                if (onNavigate) (e.currentTarget as HTMLDivElement).style.borderColor = PALETTE.borderHover
              }}
              onMouseLeave={e => {
                (e.currentTarget as HTMLDivElement).style.borderColor = PALETTE.border
              }}
              onClick={() => onNavigate?.('apps', app.name)}
            >
              {/* App Header */}
              <div style={{ marginBottom: 14 }}>
                <div style={{ fontSize: 14, fontWeight: 700, color: PALETTE.text }}>
                  {app.name}
                </div>
                {app.description && (
                  <div style={{ fontSize: 12, color: PALETTE.textDim, marginTop: 2 }}>
                    {app.description}
                  </div>
                )}
              </div>

              {/* APIs Section */}
              <APISection app={app} apiList={apiList} />

              {/* Events Section */}
              <EventsSection app={app} />

              {/* Auth Section */}
              <AuthSection app={app} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function APISection({ app, apiList }: { app: App; apiList: GatewayApi[] }) {
  const filteredApis = apiList.filter(api => api.app_name === app.name)

  return (
    <div style={{ marginBottom: 12 }}>
      <div
        style={{
          fontSize: 11,
          fontWeight: 700,
          color: PALETTE.sectionHeaderColor,
          letterSpacing: 0.5,
          marginBottom: 6,
        }}
      >
        APIs
      </div>
      {filteredApis.length === 0 ? (
        <div style={{ fontSize: 11, color: PALETTE.textDim, fontStyle: 'italic' }}>
          No APIs linked
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {filteredApis.map(api => (
            <div
              key={`${api.name}::${api.method || 'GET'}`}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                fontSize: 11,
                color: PALETTE.textMuted,
              }}
            >
              {/* Method badge */}
              <span
                style={{
                  display: 'inline-block',
                  fontSize: 10,
                  fontWeight: 700,
                  color: '#fff',
                  background: METHOD_COLORS[api.method || 'GET'] || '#6c7086',
                  borderRadius: 3,
                  padding: '2px 5px',
                  minWidth: 40,
                  textAlign: 'center',
                }}
              >
                {api.method || 'GET'}
              </span>
              {/* Path */}
              <span style={{ fontFamily: 'monospace', color: PALETTE.textMuted }}>
                {api.path}
              </span>
              {/* Arrow + Flow name */}
              <span style={{ color: PALETTE.textDim }}>→</span>
              <span style={{ fontFamily: 'monospace', color: PALETTE.sectionHeaderColor }}>
                {api.flow_name}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function EventsSection({ app }: { app: App }) {
  // For now, we'll use a placeholder since app.event_bindings is not in the types yet
  // The backend should provide this in the App response
  const eventBindings = (app as any).event_bindings || []

  return (
    <div style={{ marginBottom: 12 }}>
      <div
        style={{
          fontSize: 11,
          fontWeight: 700,
          color: PALETTE.sectionHeaderColor,
          letterSpacing: 0.5,
          marginBottom: 6,
        }}
      >
        Events
      </div>
      {eventBindings.length === 0 ? (
        <div style={{ fontSize: 11, color: PALETTE.textDim, fontStyle: 'italic' }}>
          No event bindings
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {eventBindings.map((binding: any, idx: number) => (
            <div
              key={idx}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                fontSize: 11,
                color: PALETTE.textMuted,
              }}
            >
              <span style={{ fontFamily: 'monospace' }}>
                {binding.publisher || '?'}
              </span>
              <span style={{ color: PALETTE.textDim }}>/</span>
              <span style={{ fontFamily: 'monospace' }}>
                {binding.topic || '?'}
              </span>
              <span style={{ color: PALETTE.textDim }}>→</span>
              <span style={{ fontFamily: 'monospace', color: PALETTE.sectionHeaderColor }}>
                {binding.flow_name || '?'}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function AuthSection({ app }: { app: App }) {
  const flowBindings = (app as any).flow_bindings

  return (
    <div>
      <div
        style={{
          fontSize: 11,
          fontWeight: 700,
          color: PALETTE.sectionHeaderColor,
          letterSpacing: 0.5,
          marginBottom: 6,
        }}
      >
        Auth
      </div>
      {!flowBindings ? (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            fontSize: 11,
            color: '#4caf50',
          }}
        >
          <span>•</span>
          <span>Public (no login required)</span>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 3, fontSize: 11 }}>
          {flowBindings.login_flow && (
            <div style={{ color: PALETTE.textMuted }}>
              <span style={{ color: '#ff9800' }}>🔒</span>
              {' '}Login:{' '}
              <span style={{ fontFamily: 'monospace', color: PALETTE.sectionHeaderColor }}>
                {flowBindings.login_flow}
              </span>
            </div>
          )}
          {flowBindings.logout_flow && (
            <div style={{ color: PALETTE.textMuted }}>
              <span style={{ color: '#ff9800' }}>🔒</span>
              {' '}Logout:{' '}
              <span style={{ fontFamily: 'monospace', color: PALETTE.sectionHeaderColor }}>
                {flowBindings.logout_flow}
              </span>
            </div>
          )}
          {flowBindings.callback_flow && (
            <div style={{ color: PALETTE.textMuted }}>
              <span style={{ color: '#ff9800' }}>🔒</span>
              {' '}Callback:{' '}
              <span style={{ fontFamily: 'monospace', color: PALETTE.sectionHeaderColor }}>
                {flowBindings.callback_flow}
              </span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
