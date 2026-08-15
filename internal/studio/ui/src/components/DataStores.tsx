import React, { useEffect, useState } from 'react'
import { listDataStores, testDataStore, deleteDataStore } from '../api'
import type { DataStoreDef, DataStoreTestResult } from '../api'

export default function DataStores() {
  const [datastores, setDatastores] = useState<DataStoreDef[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [testResults, setTestResults] = useState<Record<string, DataStoreTestResult | null>>({})
  const [testing, setTesting] = useState<Set<string>>(new Set())
  const [deleting, setDeleting] = useState<Set<string>>(new Set())

  useEffect(() => {
    listDataStores()
      .then(r => setDatastores(r.datastores ?? []))
      .catch(e => setError(String(e)))
      .finally(() => setLoading(false))
  }, [])

  const handleTest = async (name: string) => {
    setTesting(prev => new Set([...prev, name]))
    try {
      const result = await testDataStore(name)
      setTestResults(prev => ({ ...prev, [name]: result }))
    } catch (e) {
      setTestResults(prev => ({ ...prev, [name]: { ok: false, error: String(e) } }))
    } finally {
      setTesting(prev => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
    }
  }

  const handleDelete = async (name: string) => {
    if (!window.confirm(`Delete datastore "${name}"?`)) return
    setDeleting(prev => new Set([...prev, name]))
    try {
      await deleteDataStore(name)
      setDatastores(prev => prev.filter(d => d.name !== name))
      setTestResults(prev => {
        const next = { ...prev }
        delete next[name]
        return next
      })
    } catch (e) {
      alert(`Failed to delete: ${String(e)}`)
    } finally {
      setDeleting(prev => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
    }
  }

  const getTypeBadgeColor = (type: string): string => {
    switch (type.toLowerCase()) {
      case 'postgres':
        return 'rgba(188,139,247,0.1)'  // mauve
      case 'redis':
        return 'rgba(243,139,168,0.1)'  // red
      case 'disk':
      case 'local':
        return 'rgba(166,227,161,0.1)'  // green
      default:
        return 'rgba(137,180,250,0.1)'  // blue
    }
  }

  const getTypeBadgeTextColor = (type: string): string => {
    switch (type.toLowerCase()) {
      case 'postgres':
        return '#cba7f7'  // mauve
      case 'redis':
        return '#f38ba8'  // red
      case 'disk':
      case 'local':
        return '#a6e3a1'  // green
      default:
        return '#89b4fa'  // blue
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
        <h2 style={{ fontSize: 16, fontWeight: 700, color: '#cdd6f4', marginBottom: 6 }}>Data Stores</h2>
        <p style={{ fontSize: 13, color: '#6c7086', marginBottom: 12 }}>Configured datastores for persistence and caching.</p>
      </div>

      {datastores.length === 0 ? (
        <div style={{ color: '#6c7086', fontSize: 13 }}>No datastores configured.</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {datastores.map(ds => {
            const testResult = testResults[ds.name]
            const isTestingThis = testing.has(ds.name)
            const isDeletingThis = deleting.has(ds.name)

            return (
              <div
                key={ds.name}
                style={{
                  background: '#1e1e2e',
                  border: '1px solid #313244',
                  borderRadius: 8,
                  padding: 12,
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 8,
                }}
              >
                {/* Header row: name (bold left) + type badge (right) */}
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                  }}
                >
                  <span style={{ fontSize: 13, fontWeight: 700, color: '#cdd6f4' }}>{ds.name}</span>
                  <span
                    style={{
                      fontSize: 11,
                      color: getTypeBadgeTextColor(ds.type),
                      background: getTypeBadgeColor(ds.type),
                      padding: '2px 8px',
                      borderRadius: 4,
                    }}
                  >
                    {ds.type}
                  </span>
                </div>

                {/* Additional info if present */}
                {(ds.host || ds.database || ds.path) && (
                  <div style={{ fontSize: 12, color: '#6c7086', display: 'flex', flexDirection: 'column', gap: 2 }}>
                    {ds.host && <span>Host: {ds.host}</span>}
                    {ds.database && <span>Database: {ds.database}</span>}
                    {ds.path && <span>Path: {ds.path}</span>}
                  </div>
                )}

                {/* Test result if present */}
                {testResult && (
                  <div
                    style={{
                      fontSize: 12,
                      padding: '6px 8px',
                      borderRadius: 4,
                      background: testResult.ok ? 'rgba(166,227,161,0.1)' : 'rgba(243,139,168,0.1)',
                      color: testResult.ok ? '#a6e3a1' : '#f38ba8',
                    }}
                  >
                    {testResult.ok ? (
                      <>✓ Connected {testResult.latency_ms !== undefined && `(${testResult.latency_ms}ms)`}</>
                    ) : (
                      <>✕ {testResult.error || 'Connection failed'}</>
                    )}
                  </div>
                )}

                {/* Action buttons */}
                <div style={{ display: 'flex', gap: 8 }}>
                  <button
                    onClick={() => handleTest(ds.name)}
                    disabled={isTestingThis || isDeletingThis}
                    style={{
                      fontSize: 11,
                      padding: '4px 10px',
                      borderRadius: 4,
                      border: '1px solid #45475a',
                      background: '#313244',
                      color: '#cdd6f4',
                      cursor: isTestingThis || isDeletingThis ? 'not-allowed' : 'pointer',
                      opacity: isTestingThis || isDeletingThis ? 0.5 : 1,
                    }}
                  >
                    {isTestingThis ? 'Testing…' : 'Test'}
                  </button>
                  <button
                    onClick={() => handleDelete(ds.name)}
                    disabled={isDeletingThis || isTestingThis}
                    style={{
                      fontSize: 11,
                      padding: '4px 10px',
                      borderRadius: 4,
                      border: '1px solid #45475a',
                      background: '#313244',
                      color: '#f38ba8',
                      cursor: isDeletingThis || isTestingThis ? 'not-allowed' : 'pointer',
                      opacity: isDeletingThis || isTestingThis ? 0.5 : 1,
                    }}
                  >
                    {isDeletingThis ? 'Deleting…' : 'Delete'}
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
