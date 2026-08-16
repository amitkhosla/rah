import React, { useEffect, useState } from 'react'
import {
  listTestCases,
  createTestCase,
  deleteTestCase,
  executeTest,
  listTestSuites,
  createTestSuite,
  runTestSuite,
  deleteTestSuite,
  type TestCase,
  type TestSuite,
  type TestExecuteResponse,
  type SuiteRunResult,
  type TestAssertion,
} from '../api'

type FormTab = 'cases' | 'suites'

interface RunResult {
  type: 'test' | 'suite'
  testOrSuiteId: string
  result: TestExecuteResponse | SuiteRunResult
}

export default function Tests() {
  const [tab, setTab] = useState<FormTab>('cases')
  const [testCases, setTestCases] = useState<TestCase[]>([])
  const [testSuites, setTestSuites] = useState<TestSuite[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [showCreateForm, setShowCreateForm] = useState(false)
  const [runResult, setRunResult] = useState<RunResult | null>(null)
  const [running, setRunning] = useState(false)

  useEffect(() => {
    loadData()
  }, [])

  const loadData = async () => {
    try {
      setLoading(true)
      const [cases, suites] = await Promise.all([listTestCases(), listTestSuites()])
      setTestCases(cases)
      setTestSuites(suites)
      setError(null)
    } catch (e) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }

  const handleRunTest = async (testCase: TestCase) => {
    try {
      setRunning(true)
      const result = await executeTest({
        flow_name: testCase.flow_name,
        call_mode: testCase.call_mode,
        input: testCase.input,
        mocks: testCase.mocks,
        assertions: testCase.assertions,
      })
      setRunResult({ type: 'test', testOrSuiteId: testCase.id, result })
    } catch (e) {
      setError(String(e))
    } finally {
      setRunning(false)
    }
  }

  const handleDeleteTestCase = async (id: string) => {
    try {
      await deleteTestCase(id)
      setTestCases(prev => prev.filter(tc => tc.id !== id))
    } catch (e) {
      setError(String(e))
    }
  }

  const handleRunSuite = async (suite: TestSuite) => {
    try {
      setRunning(true)
      const result = await runTestSuite(suite.id)
      setRunResult({ type: 'suite', testOrSuiteId: suite.id, result })
    } catch (e) {
      setError(String(e))
    } finally {
      setRunning(false)
    }
  }

  const handleDeleteTestSuite = async (id: string) => {
    try {
      await deleteTestSuite(id)
      setTestSuites(prev => prev.filter(s => s.id !== id))
    } catch (e) {
      setError(String(e))
    }
  }

  if (loading) {
    return (
      <div style={{ padding: '20px 24px', color: '#a6adc8', fontSize: 13 }}>
        Loading…
      </div>
    )
  }

  if (error && !showCreateForm) {
    return (
      <div style={{ padding: '20px 24px', color: '#f38ba8', fontSize: 13 }}>
        Error: {error}
      </div>
    )
  }

  return (
    <div style={{ display: 'flex', height: '100%', overflow: 'hidden' }}>
      {/* Left panel: test cases or suites list */}
      <div style={{ flex: '0 0 40%', borderRight: '1px solid #313244', overflowY: 'auto', padding: '20px 24px' }}>
        <div style={{ display: 'flex', gap: 8, marginBottom: 16, borderBottom: '1px solid #313244', paddingBottom: 12 }}>
          <button
            onClick={() => setTab('cases')}
            style={{
              padding: '6px 12px',
              borderRadius: 6,
              border: 'none',
              background: tab === 'cases' ? 'var(--accent)' : 'transparent',
              color: tab === 'cases' ? '#fff' : '#a6adc8',
              fontSize: 12,
              fontWeight: 700,
              cursor: 'pointer',
            }}
          >
            Test Cases
          </button>
          <button
            onClick={() => setTab('suites')}
            style={{
              padding: '6px 12px',
              borderRadius: 6,
              border: 'none',
              background: tab === 'suites' ? 'var(--accent)' : 'transparent',
              color: tab === 'suites' ? '#fff' : '#a6adc8',
              fontSize: 12,
              fontWeight: 700,
              cursor: 'pointer',
            }}
          >
            Test Suites
          </button>
        </div>

        {tab === 'cases' && (
          <div>
            <button
              onClick={() => setShowCreateForm(true)}
              style={{
                display: 'block',
                width: '100%',
                marginBottom: 12,
                padding: '8px 12px',
                borderRadius: 6,
                border: '1px solid var(--accent)',
                background: 'transparent',
                color: 'var(--accent)',
                fontSize: 12,
                fontWeight: 700,
                cursor: 'pointer',
              }}
            >
              + New Test Case
            </button>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {testCases.length === 0 ? (
                <div style={{ color: '#6c7086', fontSize: 12 }}>No test cases yet.</div>
              ) : (
                testCases.map(tc => (
                  <div
                    key={tc.id}
                    style={{
                      background: '#1e1e2e',
                      border: '1px solid #313244',
                      borderRadius: 6,
                      padding: 10,
                    }}
                  >
                    <div style={{ marginBottom: 8 }}>
                      <div style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
                        {tc.name}
                      </div>
                      <div style={{ fontSize: 11, color: '#6c7086' }}>
                        Flow: {tc.flow_name}
                      </div>
                      <div style={{ fontSize: 10, color: '#89b4fa', marginTop: 4 }}>
                        {tc.call_mode}
                      </div>
                    </div>
                    <div style={{ display: 'flex', gap: 6 }}>
                      <button
                        onClick={() => handleRunTest(tc)}
                        disabled={running}
                        style={{
                          flex: 1,
                          padding: '4px 8px',
                          borderRadius: 4,
                          border: 'none',
                          background: 'rgba(137,180,250,0.2)',
                          color: '#89b4fa',
                          fontSize: 11,
                          fontWeight: 700,
                          cursor: running ? 'wait' : 'pointer',
                          opacity: running ? 0.5 : 1,
                        }}
                      >
                        {running ? 'Running…' : 'Run'}
                      </button>
                      <button
                        onClick={() => handleDeleteTestCase(tc.id)}
                        style={{
                          flex: 1,
                          padding: '4px 8px',
                          borderRadius: 4,
                          border: 'none',
                          background: 'rgba(243,139,168,0.2)',
                          color: '#f38ba8',
                          fontSize: 11,
                          fontWeight: 700,
                          cursor: 'pointer',
                        }}
                      >
                        Delete
                      </button>
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        )}

        {tab === 'suites' && (
          <div>
            <button
              onClick={() => setShowCreateForm(true)}
              style={{
                display: 'block',
                width: '100%',
                marginBottom: 12,
                padding: '8px 12px',
                borderRadius: 6,
                border: '1px solid var(--accent)',
                background: 'transparent',
                color: 'var(--accent)',
                fontSize: 12,
                fontWeight: 700,
                cursor: 'pointer',
              }}
            >
              + New Suite
            </button>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {testSuites.length === 0 ? (
                <div style={{ color: '#6c7086', fontSize: 12 }}>No test suites yet.</div>
              ) : (
                testSuites.map(suite => (
                  <div
                    key={suite.id}
                    style={{
                      background: '#1e1e2e',
                      border: '1px solid #313244',
                      borderRadius: 6,
                      padding: 10,
                    }}
                  >
                    <div style={{ marginBottom: 8 }}>
                      <div style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
                        {suite.name}
                      </div>
                      <div style={{ fontSize: 11, color: '#6c7086' }}>
                        {suite.case_ids.length} case{suite.case_ids.length !== 1 ? 's' : ''}
                      </div>
                    </div>
                    <div style={{ display: 'flex', gap: 6 }}>
                      <button
                        onClick={() => handleRunSuite(suite)}
                        disabled={running}
                        style={{
                          flex: 1,
                          padding: '4px 8px',
                          borderRadius: 4,
                          border: 'none',
                          background: 'rgba(137,180,250,0.2)',
                          color: '#89b4fa',
                          fontSize: 11,
                          fontWeight: 700,
                          cursor: running ? 'wait' : 'pointer',
                          opacity: running ? 0.5 : 1,
                        }}
                      >
                        {running ? 'Running…' : 'Run Suite'}
                      </button>
                      <button
                        onClick={() => handleDeleteTestSuite(suite.id)}
                        style={{
                          flex: 1,
                          padding: '4px 8px',
                          borderRadius: 4,
                          border: 'none',
                          background: 'rgba(243,139,168,0.2)',
                          color: '#f38ba8',
                          fontSize: 11,
                          fontWeight: 700,
                          cursor: 'pointer',
                        }}
                      >
                        Delete
                      </button>
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        )}
      </div>

      {/* Right panel: form or results */}
      <div style={{ flex: '0 0 60%', overflowY: 'auto', padding: '20px 24px' }}>
        {showCreateForm && tab === 'cases' && (
          <CreateTestCaseForm
            onSave={async (tc) => {
              try {
                const created = await createTestCase(tc)
                setTestCases(prev => [...prev, created])
                setShowCreateForm(false)
              } catch (e) {
                setError(String(e))
              }
            }}
            onCancel={() => setShowCreateForm(false)}
          />
        )}

        {showCreateForm && tab === 'suites' && (
          <CreateTestSuiteForm
            testCases={testCases}
            onSave={async (suite) => {
              try {
                const created = await createTestSuite(suite)
                setTestSuites(prev => [...prev, created])
                setShowCreateForm(false)
              } catch (e) {
                setError(String(e))
              }
            }}
            onCancel={() => setShowCreateForm(false)}
          />
        )}

        {!showCreateForm && runResult && runResult.type === 'test' && (
          <TestResultDisplay result={runResult.result as TestExecuteResponse} />
        )}

        {!showCreateForm && runResult && runResult.type === 'suite' && (
          <SuiteResultDisplay result={runResult.result as SuiteRunResult} />
        )}

        {!showCreateForm && !runResult && (
          <div style={{ color: '#6c7086', fontSize: 12 }}>
            Select or run a test to see results here.
          </div>
        )}
      </div>
    </div>
  )
}

interface CreateTestCaseFormProps {
  onSave: (tc: Omit<TestCase, 'id' | 'created_at'>) => void
  onCancel: () => void
}

function CreateTestCaseForm({ onSave, onCancel }: CreateTestCaseFormProps) {
  const [name, setName] = useState('')
  const [flowName, setFlowName] = useState('')
  const [apiName, setApiName] = useState('')
  const [callMode, setCallMode] = useState<'mock' | 'real' | 'schema_only'>('real')
  const [method, setMethod] = useState('GET')
  const [path, setPath] = useState('')
  const [headers, setHeaders] = useState('')
  const [body, setBody] = useState('')
  const [assertions, setAssertions] = useState<TestAssertion[]>([])

  const handleAddAssertion = () => {
    setAssertions(prev => [...prev, { type: 'status', expected: 200 }])
  }

  const handleRemoveAssertion = (idx: number) => {
    setAssertions(prev => prev.filter((_, i) => i !== idx))
  }

  const handleSave = () => {
    try {
      const parsedHeaders = headers.trim() ? JSON.parse(headers) : {}
      const tc: Omit<TestCase, 'id' | 'created_at'> = {
        name,
        api_name: apiName,
        flow_name: flowName,
        call_mode: callMode,
        input: {
          method,
          path,
          headers: parsedHeaders,
          body: body || undefined,
        },
        assertions: assertions.length > 0 ? assertions : [{ type: 'status', expected: 200 }],
      }
      onSave(tc)
    } catch (e) {
      alert(`Error: ${e}`)
    }
  }

  return (
    <div>
      <h2 style={{ fontSize: 15, fontWeight: 700, color: '#cdd6f4', marginBottom: 16 }}>
        Create Test Case
      </h2>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Name
          </label>
          <input
            type="text"
            value={name}
            onChange={e => setName(e.target.value)}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
            }}
            placeholder="e.g., Test login flow"
          />
        </div>

        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            API Name
          </label>
          <input
            type="text"
            value={apiName}
            onChange={e => setApiName(e.target.value)}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
            }}
            placeholder="e.g., auth_api"
          />
        </div>

        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Flow Name
          </label>
          <input
            type="text"
            value={flowName}
            onChange={e => setFlowName(e.target.value)}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
            }}
            placeholder="e.g., login_flow"
          />
        </div>

        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Call Mode
          </label>
          <select
            value={callMode}
            onChange={e => setCallMode(e.target.value as 'mock' | 'real' | 'schema_only')}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
            }}
          >
            <option value="real">Real</option>
            <option value="mock">Mock</option>
            <option value="schema_only">Schema Only</option>
          </select>
        </div>

        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Method
          </label>
          <select
            value={method}
            onChange={e => setMethod(e.target.value)}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
            }}
          >
            <option value="GET">GET</option>
            <option value="POST">POST</option>
            <option value="PUT">PUT</option>
            <option value="DELETE">DELETE</option>
            <option value="PATCH">PATCH</option>
          </select>
        </div>

        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Path
          </label>
          <input
            type="text"
            value={path}
            onChange={e => setPath(e.target.value)}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
            }}
            placeholder="/auth/login"
          />
        </div>

        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Headers (JSON)
          </label>
          <textarea
            value={headers}
            onChange={e => setHeaders(e.target.value)}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
              minHeight: 60,
              fontFamily: 'monospace',
            }}
            placeholder='{"Content-Type": "application/json"}'
          />
        </div>

        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Body
          </label>
          <textarea
            value={body}
            onChange={e => setBody(e.target.value)}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
              minHeight: 60,
              fontFamily: 'monospace',
            }}
            placeholder='{"username": "test"}'
          />
        </div>

        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
            <label style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4' }}>
              Assertions
            </label>
            <button
              onClick={handleAddAssertion}
              style={{
                fontSize: 11,
                padding: '2px 8px',
                borderRadius: 4,
                border: '1px solid var(--accent)',
                background: 'transparent',
                color: 'var(--accent)',
                cursor: 'pointer',
              }}
            >
              + Add
            </button>
          </div>
          {assertions.map((a, idx) => (
            <div key={idx} style={{ display: 'flex', gap: 8, marginBottom: 8, fontSize: 12 }}>
              <select
                value={a.type}
                onChange={e => {
                  const updated = [...assertions]
                  updated[idx].type = e.target.value as TestAssertion['type']
                  setAssertions(updated)
                }}
                style={{
                  flex: 1,
                  padding: '4px 6px',
                  borderRadius: 4,
                  border: '1px solid #313244',
                  background: '#0f0f1a',
                  color: '#cdd6f4',
                }}
              >
                <option value="status">Status</option>
                <option value="body_contains">Body Contains</option>
                <option value="latency_ms">Latency</option>
              </select>
              <input
                type="text"
                placeholder="Expected value"
                value={a.expected ?? ''}
                onChange={e => {
                  const updated = [...assertions]
                  updated[idx].expected = e.target.value || undefined
                  setAssertions(updated)
                }}
                style={{
                  flex: 1,
                  padding: '4px 6px',
                  borderRadius: 4,
                  border: '1px solid #313244',
                  background: '#0f0f1a',
                  color: '#cdd6f4',
                }}
              />
              <button
                onClick={() => handleRemoveAssertion(idx)}
                style={{
                  padding: '4px 8px',
                  borderRadius: 4,
                  border: 'none',
                  background: 'rgba(243,139,168,0.2)',
                  color: '#f38ba8',
                  cursor: 'pointer',
                }}
              >
                Remove
              </button>
            </div>
          ))}
        </div>

        <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
          <button
            onClick={handleSave}
            style={{
              flex: 1,
              padding: '8px 12px',
              borderRadius: 6,
              border: 'none',
              background: 'var(--accent)',
              color: '#fff',
              fontSize: 12,
              fontWeight: 700,
              cursor: 'pointer',
            }}
          >
            Create
          </button>
          <button
            onClick={onCancel}
            style={{
              flex: 1,
              padding: '8px 12px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: 'transparent',
              color: '#a6adc8',
              fontSize: 12,
              fontWeight: 700,
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}

interface CreateTestSuiteFormProps {
  testCases: TestCase[]
  onSave: (suite: { name: string; case_ids: string[] }) => void
  onCancel: () => void
}

function CreateTestSuiteForm({ testCases, onSave, onCancel }: CreateTestSuiteFormProps) {
  const [name, setName] = useState('')
  const [selectedCaseIds, setSelectedCaseIds] = useState<string[]>([])

  const handleToggleCase = (id: string) => {
    setSelectedCaseIds(prev =>
      prev.includes(id) ? prev.filter(cid => cid !== id) : [...prev, id]
    )
  }

  const handleSave = () => {
    if (!name.trim() || selectedCaseIds.length === 0) {
      alert('Please enter a name and select at least one test case')
      return
    }
    onSave({ name, case_ids: selectedCaseIds })
  }

  return (
    <div>
      <h2 style={{ fontSize: 15, fontWeight: 700, color: '#cdd6f4', marginBottom: 16 }}>
        Create Test Suite
      </h2>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            Suite Name
          </label>
          <input
            type="text"
            value={name}
            onChange={e => setName(e.target.value)}
            style={{
              width: '100%',
              padding: '6px 10px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: '#0f0f1a',
              color: '#cdd6f4',
              fontSize: 12,
            }}
            placeholder="e.g., Auth flow tests"
          />
        </div>

        <div>
          <label style={{ display: 'block', fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 8 }}>
            Select Test Cases
          </label>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxHeight: 300, overflowY: 'auto' }}>
            {testCases.length === 0 ? (
              <div style={{ color: '#6c7086', fontSize: 12 }}>No test cases available.</div>
            ) : (
              testCases.map(tc => (
                <label
                  key={tc.id}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 8,
                    padding: '6px',
                    borderRadius: 4,
                    background: '#1e1e2e',
                    cursor: 'pointer',
                  }}
                >
                  <input
                    type="checkbox"
                    checked={selectedCaseIds.includes(tc.id)}
                    onChange={() => handleToggleCase(tc.id)}
                    style={{ cursor: 'pointer' }}
                  />
                  <span style={{ fontSize: 12, color: '#cdd6f4' }}>
                    {tc.name}
                  </span>
                </label>
              ))
            )}
          </div>
        </div>

        <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
          <button
            onClick={handleSave}
            style={{
              flex: 1,
              padding: '8px 12px',
              borderRadius: 6,
              border: 'none',
              background: 'var(--accent)',
              color: '#fff',
              fontSize: 12,
              fontWeight: 700,
              cursor: 'pointer',
            }}
          >
            Create Suite
          </button>
          <button
            onClick={onCancel}
            style={{
              flex: 1,
              padding: '8px 12px',
              borderRadius: 6,
              border: '1px solid #313244',
              background: 'transparent',
              color: '#a6adc8',
              fontSize: 12,
              fontWeight: 700,
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}

interface TestResultDisplayProps {
  result: TestExecuteResponse
}

function TestResultDisplay({ result }: TestResultDisplayProps) {
  return (
    <div>
      <h2 style={{ fontSize: 15, fontWeight: 700, color: '#cdd6f4', marginBottom: 16 }}>
        Test Result
      </h2>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div style={{
          padding: 12,
          borderRadius: 8,
          background: result.passed ? 'rgba(34,197,94,0.15)' : 'rgba(243,139,168,0.15)',
          border: `1px solid ${result.passed ? '#22c55e' : '#f38ba8'}`,
        }}>
          <div style={{
            fontSize: 14,
            fontWeight: 700,
            color: result.passed ? '#22c55e' : '#f38ba8',
          }}>
            {result.passed ? '✓ PASSED' : '✗ FAILED'}
          </div>
          <div style={{ fontSize: 12, color: '#a6adc8', marginTop: 4 }}>
            Duration: {result.duration_ms}ms
          </div>
        </div>

        <div style={{ borderLeft: '3px solid #89b4fa', paddingLeft: 12 }}>
          <div style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 4 }}>
            HTTP Response
          </div>
          <div style={{ fontSize: 12, color: '#a6adc8', fontFamily: 'monospace' }}>
            Status: {result.response.status}
          </div>
          {result.response.body && (
            <div style={{ fontSize: 11, color: '#6c7086', fontFamily: 'monospace', marginTop: 4, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
              {result.response.body}
            </div>
          )}
        </div>

        {result.assertions.length > 0 && (
          <div>
            <div style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 8 }}>
              Assertions ({result.assertions.filter(a => a.passed).length}/{result.assertions.length} passed)
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {result.assertions.map((a, idx) => (
                <div
                  key={idx}
                  style={{
                    padding: 8,
                    borderRadius: 6,
                    background: a.passed ? '#0f0f1a' : 'rgba(243,139,168,0.1)',
                    border: `1px solid ${a.passed ? '#313244' : '#f38ba8'}`,
                  }}
                >
                  <div style={{
                    fontSize: 11,
                    fontWeight: 700,
                    color: a.passed ? '#22c55e' : '#f38ba8',
                  }}>
                    {a.passed ? '✓' : '✗'} {a.type}
                  </div>
                  {a.message && (
                    <div style={{ fontSize: 11, color: '#a6adc8', marginTop: 2 }}>
                      {a.message}
                    </div>
                  )}
                  {a.expected !== undefined && (
                    <div style={{ fontSize: 10, color: '#6c7086', marginTop: 2 }}>
                      Expected: {String(a.expected)}
                    </div>
                  )}
                  {a.actual !== undefined && (
                    <div style={{ fontSize: 10, color: '#6c7086' }}>
                      Actual: {String(a.actual)}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

interface SuiteResultDisplayProps {
  result: SuiteRunResult
}

function SuiteResultDisplay({ result }: SuiteResultDisplayProps) {
  return (
    <div>
      <h2 style={{ fontSize: 15, fontWeight: 700, color: '#cdd6f4', marginBottom: 16 }}>
        Suite Result
      </h2>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div style={{
          padding: 12,
          borderRadius: 8,
          background: result.passed ? 'rgba(34,197,94,0.15)' : 'rgba(243,139,168,0.15)',
          border: `1px solid ${result.passed ? '#22c55e' : '#f38ba8'}`,
        }}>
          <div style={{
            fontSize: 14,
            fontWeight: 700,
            color: result.passed ? '#22c55e' : '#f38ba8',
          }}>
            {result.passed ? '✓ PASSED' : '✗ FAILED'}
          </div>
          <div style={{ fontSize: 12, color: '#a6adc8', marginTop: 4 }}>
            {result.passed_cases}/{result.total_cases} cases passed
          </div>
          {result.test_tenant_alias && (
            <div style={{ fontSize: 11, color: '#89b4fa', marginTop: 2 }}>
              Test tenant: {result.test_tenant_alias}
            </div>
          )}
        </div>

        <div>
          <div style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 8 }}>
            Case Results
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {result.results.map((r, idx) => (
              <div
                key={idx}
                style={{
                  padding: 10,
                  borderRadius: 6,
                  background: '#1e1e2e',
                  border: `1px solid ${r.passed ? '#22c55e' : '#f38ba8'}`,
                }}
              >
                <div style={{
                  fontSize: 12,
                  fontWeight: 700,
                  color: r.passed ? '#22c55e' : '#f38ba8',
                  marginBottom: 4,
                }}>
                  {r.passed ? '✓' : '✗'} {r.case_name}
                </div>
                <div style={{ fontSize: 11, color: '#a6adc8' }}>
                  Status: {r.response.status} | Duration: {r.duration_ms}ms
                </div>
                {r.error && (
                  <div style={{ fontSize: 11, color: '#f38ba8', marginTop: 4 }}>
                    {r.error}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
