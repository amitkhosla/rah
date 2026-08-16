import React, { useEffect, useRef, useState } from 'react'
import { listWorkflows, getWorkflow, upsertWorkflow, deleteWorkflow, listFlowNames } from '../api'
import type { WorkflowDef, WorkflowNode, WorkflowEdge } from '../api'

export default function WorkflowDesigner() {
  const [workflows, setWorkflows] = useState<string[]>([])
  const [selectedWfName, setSelectedWfName] = useState<string>('')
  const [newWfName, setNewWfName] = useState<string>('')
  const [wf, setWf] = useState<WorkflowDef>({ name: '', description: '', appName: '', nodes: [], edges: [] })
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null)
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null)
  const [draggingNode, setDraggingNode] = useState<{ id: string; ox: number; oy: number } | null>(null)
  const [connectMode, setConnectMode] = useState(false)
  const [connectSource, setConnectSource] = useState<string | null>(null)
  const [listenerInput, setListenerInput] = useState<string>('')
  const [pendingTargetId, setPendingTargetId] = useState<string | null>(null)
  const [saveError, setSaveError] = useState<string>('')
  const [saveOk, setSaveOk] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [availableFlows, setAvailableFlows] = useState<string[]>([])
  const canvasRef = useRef<SVGSVGElement>(null)

  useEffect(() => {
    Promise.all([listWorkflows().then(r => r.workflows || []), listFlowNames()])
      .then(([wfs, flows]) => { setWorkflows(wfs); setAvailableFlows(flows); setLoading(false) })
      .catch(e => { setError(String(e)); setLoading(false) })
  }, [])

  useEffect(() => {
    if (!selectedWfName) { setWf({ name: '', description: '', appName: '', nodes: [], edges: [] }); return }
    getWorkflow(selectedWfName).then(wfData => setWf(wfData)).catch(e => setSaveError(String(e)))
  }, [selectedWfName])

  const handleCreateWorkflow = () => {
    if (!newWfName.trim()) return
    setWf({ name: newWfName, description: '', appName: '', nodes: [], edges: [] })
    setSelectedWfName(newWfName)
    setNewWfName('')
  }

  const handleSave = () => {
    setSaveError(''); setSaveOk(false)
    upsertWorkflow(wf)
      .then(() => {
        setSaveOk(true)
        setTimeout(() => setSaveOk(false), 2000)
        if (!workflows.includes(wf.name)) setWorkflows(prev => [...prev, wf.name])
      })
      .catch(e => {
        const msg = String(e)
        setSaveError(msg.includes('422') || msg.includes('Cycle') ? 'Cycle detected in workflow graph' : msg)
      })
  }

  const handleDeleteWorkflow = () => {
    if (!selectedWfName || !confirm(`Delete workflow "${selectedWfName}"?`)) return
    deleteWorkflow(selectedWfName).then(() => {
      setWorkflows(prev => prev.filter(w => w !== selectedWfName))
      setSelectedWfName('')
      setWf({ name: '', description: '', appName: '', nodes: [], edges: [] })
    }).catch(e => setSaveError(String(e)))
  }

  const handleCanvasDrop = (e: React.DragEvent<SVGSVGElement>) => {
    e.preventDefault()
    const flowName = e.dataTransfer.getData('flowName')
    if (!flowName || !canvasRef.current) return
    const rect = canvasRef.current.getBoundingClientRect()
    const nodeId = `node-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const newNode: WorkflowNode = { id: nodeId, flowName, label: flowName, x: e.clientX - rect.left - 70, y: e.clientY - rect.top - 25 }
    setWf(prev => ({ ...prev, nodes: [...prev.nodes, newNode] }))
    setSelectedNodeId(nodeId)
  }

  const handleNodeMouseDown = (nodeId: string, e: React.MouseEvent) => {
    e.stopPropagation()
    setSelectedNodeId(nodeId); setSelectedEdgeId(null)
    if (connectMode) {
      if (connectSource === null) { setConnectSource(nodeId) }
      else if (connectSource !== nodeId) { setPendingTargetId(nodeId); setListenerInput('') }
      return
    }
    const node = wf.nodes.find(n => n.id === nodeId)
    if (!node) return
    setDraggingNode({ id: nodeId, ox: e.clientX - node.x, oy: e.clientY - node.y })
  }

  const handleCanvasMouseMove = (e: React.MouseEvent<SVGSVGElement>) => {
    if (!draggingNode || !canvasRef.current) return
    const rect = canvasRef.current.getBoundingClientRect()
    const x = e.clientX - rect.left - draggingNode.ox
    const y = e.clientY - rect.top - draggingNode.oy
    setWf(prev => ({ ...prev, nodes: prev.nodes.map(n => n.id === draggingNode.id ? { ...n, x: Math.max(0, x), y: Math.max(0, y) } : n) }))
  }

  const handleEdgeClick = (edgeId: string, e: React.MouseEvent) => {
    e.stopPropagation(); setSelectedEdgeId(edgeId); setSelectedNodeId(null)
  }

  const handleCanvasClick = () => { setSelectedNodeId(null); setSelectedEdgeId(null) }

  const handleDeleteSelected = () => {
    if (selectedNodeId) {
      setWf(prev => ({
        ...prev,
        nodes: prev.nodes.filter(n => n.id !== selectedNodeId),
        edges: prev.edges.filter(e => e.sourceNodeId !== selectedNodeId && e.targetNodeId !== selectedNodeId),
      }))
      setSelectedNodeId(null)
    } else if (selectedEdgeId) {
      setWf(prev => ({ ...prev, edges: prev.edges.filter(e => e.id !== selectedEdgeId) }))
      setSelectedEdgeId(null)
    }
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Delete' || e.key === 'Backspace') handleDeleteSelected() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedNodeId, selectedEdgeId])

  const handleConfirmListener = () => {
    if (!connectSource || !pendingTargetId || !listenerInput.trim()) return
    const edgeId = `edge-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const newEdge: WorkflowEdge = { id: edgeId, sourceNodeId: connectSource, targetNodeId: pendingTargetId, listenerName: listenerInput, label: listenerInput }
    setWf(prev => ({ ...prev, edges: [...prev.edges, newEdge] }))
    setConnectSource(null); setPendingTargetId(null); setListenerInput(''); setConnectMode(false)
  }

  const updateSelectedNode = (key: keyof WorkflowNode, value: string | number) => {
    if (!selectedNodeId) return
    setWf(prev => ({ ...prev, nodes: prev.nodes.map(n => n.id === selectedNodeId ? { ...n, [key]: value } : n) }))
  }

  const updateSelectedEdge = (key: keyof WorkflowEdge, value: string) => {
    if (!selectedEdgeId) return
    setWf(prev => ({ ...prev, edges: prev.edges.map(e => e.id === selectedEdgeId ? { ...e, [key]: value } : e) }))
  }

  const selectedNode = wf.nodes.find(n => n.id === selectedNodeId)
  const selectedEdge = wf.edges.find(e => e.id === selectedEdgeId)
  const sourceNode = selectedEdge ? wf.nodes.find(n => n.id === selectedEdge.sourceNodeId) : null
  const targetNode = selectedEdge ? wf.nodes.find(n => n.id === selectedEdge.targetNodeId) : null

  if (loading) return <div style={{ padding: '20px 24px', color: '#a6adc8', fontSize: 13 }}>Loading…</div>
  if (error) return <div style={{ padding: '20px 24px', color: '#f38ba8', fontSize: 13 }}>Error: {error}</div>

  const inputStyle: React.CSSProperties = { width: '100%', padding: '6px 8px', borderRadius: 4, border: '1px solid #313244', background: '#1e1e2e', color: '#cdd6f4', fontSize: 11, boxSizing: 'border-box' }
  const labelStyle: React.CSSProperties = { fontSize: 11, fontWeight: 700, color: '#89b4fa', display: 'block', marginBottom: 4 }

  return (
    <div style={{ display: 'flex', height: '100%', background: '#1e1e2e', color: '#cdd6f4' }}>

      {/* ── Left sidebar ─────────────────────────────────────────── */}
      <aside style={{ width: 220, flexShrink: 0, background: '#181825', borderRight: '1px solid #313244', overflowY: 'auto', padding: 16, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div>
          <label style={labelStyle}>WORKFLOW</label>
          <select value={selectedWfName} onChange={e => setSelectedWfName(e.target.value)}
            style={{ ...inputStyle, marginBottom: 8 }}>
            <option value="">Select workflow…</option>
            {workflows.map(w => <option key={w} value={w}>{w}</option>)}
          </select>
          <div style={{ display: 'flex', gap: 6 }}>
            <input type="text" placeholder="New name" value={newWfName} onChange={e => setNewWfName(e.target.value)}
              style={{ ...inputStyle, flex: 1 }} />
            <button onClick={handleCreateWorkflow}
              style={{ padding: '6px 8px', borderRadius: 4, border: '1px solid #89b4fa', background: '#89b4fa', color: '#1e1e2e', fontSize: 11, fontWeight: 700, cursor: 'pointer' }}>
              New
            </button>
          </div>
        </div>

        <div>
          <label style={labelStyle}>FLOWS — drag to canvas</label>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {availableFlows.length === 0
              ? <div style={{ fontSize: 11, color: '#6c7086' }}>No flows available</div>
              : availableFlows.map(flow => (
                <div key={flow} draggable onDragStart={e => e.dataTransfer.setData('flowName', flow)}
                  style={{ padding: '6px 8px', background: '#1e1e2e', border: '1px solid #313244', borderRadius: 4, fontSize: 11, cursor: 'grab', userSelect: 'none' }}>
                  {flow}
                </div>
              ))}
          </div>
        </div>
      </aside>

      {/* ── Canvas ───────────────────────────────────────────────── */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 500 }}>
        {/* Toolbar */}
        <div style={{ padding: '10px 16px', borderBottom: '1px solid #313244', display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
          <button onClick={handleSave}
            style={{ padding: '5px 12px', borderRadius: 4, border: '1px solid #a6e3a1', background: '#a6e3a1', color: '#1e1e2e', fontSize: 11, fontWeight: 700, cursor: 'pointer' }}>
            Save
          </button>
          <button onClick={() => { setConnectMode(!connectMode); setConnectSource(null); setPendingTargetId(null) }}
            style={{ padding: '5px 12px', borderRadius: 4, border: '1px solid #fab387', background: connectMode ? '#fab387' : 'transparent', color: connectMode ? '#1e1e2e' : '#fab387', fontSize: 11, fontWeight: 700, cursor: 'pointer' }}>
            {connectMode ? 'Cancel Connect' : 'Connect'}
          </button>
          <button onClick={handleDeleteSelected} disabled={!selectedNodeId && !selectedEdgeId}
            style={{ padding: '5px 12px', borderRadius: 4, border: '1px solid #f38ba8', background: (selectedNodeId || selectedEdgeId) ? '#f38ba8' : 'transparent', color: (selectedNodeId || selectedEdgeId) ? '#1e1e2e' : '#f38ba8', fontSize: 11, fontWeight: 700, cursor: (selectedNodeId || selectedEdgeId) ? 'pointer' : 'not-allowed', opacity: (selectedNodeId || selectedEdgeId) ? 1 : 0.4 }}>
            Delete
          </button>
          {saveOk && <span style={{ fontSize: 11, color: '#a6e3a1' }}>✓ Saved</span>}
          {saveError && <span style={{ fontSize: 11, color: '#f38ba8' }}>⚠ {saveError}</span>}
          {connectMode && connectSource && !pendingTargetId && (
            <span style={{ fontSize: 11, color: '#fab387' }}>Now click target node…</span>
          )}
          {connectMode && !connectSource && (
            <span style={{ fontSize: 11, color: '#fab387' }}>Click source node…</span>
          )}
        </div>

        {/* SVG canvas */}
        <div style={{ flex: 1, overflow: 'hidden', position: 'relative' }}>
          <svg ref={canvasRef}
            onDragOver={e => e.preventDefault()}
            onDrop={handleCanvasDrop}
            onMouseMove={handleCanvasMouseMove}
            onMouseUp={() => setDraggingNode(null)}
            onMouseLeave={() => setDraggingNode(null)}
            onClick={handleCanvasClick}
            style={{ width: '100%', height: '100%', background: '#181825', cursor: connectMode ? 'crosshair' : draggingNode ? 'grabbing' : 'default' }}>

            <defs>
              <marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto">
                <polygon points="0 0,10 3,0 6" fill="#6c7086" />
              </marker>
              <marker id="arrow-sel" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto">
                <polygon points="0 0,10 3,0 6" fill="#89b4fa" />
              </marker>
            </defs>

            {/* Edges */}
            {wf.edges.map(edge => {
              const src = wf.nodes.find(n => n.id === edge.sourceNodeId)
              const tgt = wf.nodes.find(n => n.id === edge.targetNodeId)
              if (!src || !tgt) return null
              const x1 = src.x + 140; const y1 = src.y + 25
              const x2 = tgt.x;      const y2 = tgt.y + 25
              const mx = (x1 + x2) / 2; const my = (y1 + y2) / 2
              const sel = selectedEdgeId === edge.id
              return (
                <g key={edge.id}>
                  <path d={`M ${x1} ${y1} C ${x1 + 60} ${y1} ${x2 - 60} ${y2} ${x2} ${y2}`}
                    stroke={sel ? '#89b4fa' : '#45475a'} strokeWidth={sel ? 2 : 1.5}
                    fill="none" markerEnd={sel ? 'url(#arrow-sel)' : 'url(#arrow)'} />
                  {edge.label && (
                    <text x={mx} y={my - 8} textAnchor="middle" fontSize="10" fill="#6c7086" pointerEvents="none">
                      {edge.label}
                    </text>
                  )}
                  {/* Invisible wide hit target */}
                  <path d={`M ${x1} ${y1} C ${x1 + 60} ${y1} ${x2 - 60} ${y2} ${x2} ${y2}`}
                    stroke="transparent" strokeWidth={12} fill="none"
                    onClick={e => handleEdgeClick(edge.id, e)} style={{ cursor: 'pointer' }} />
                </g>
              )
            })}

            {/* Nodes */}
            {wf.nodes.map(node => {
              const sel = selectedNodeId === node.id
              const isSrc = connectMode && connectSource === node.id
              return (
                <g key={node.id} onMouseDown={e => handleNodeMouseDown(node.id, e)} style={{ cursor: connectMode ? 'crosshair' : 'grab' }}>
                  <rect x={node.x} y={node.y} width={140} height={50} rx={8}
                    fill={isSrc ? '#313244' : '#313244'}
                    stroke={isSrc ? '#fab387' : sel ? '#89b4fa' : '#45475a'}
                    strokeWidth={isSrc || sel ? 2 : 1} />
                  <text x={node.x + 70} y={node.y + 18} textAnchor="middle" fontSize="12" fill="#cdd6f4" pointerEvents="none" fontWeight={600}>
                    {node.label.length > 16 ? node.label.slice(0, 15) + '…' : node.label}
                  </text>
                  <text x={node.x + 70} y={node.y + 34} textAnchor="middle" fontSize="10" fill="#6c7086" pointerEvents="none">
                    {node.flowName !== node.label ? (node.flowName.length > 18 ? node.flowName.slice(0, 17) + '…' : node.flowName) : ''}
                  </text>
                </g>
              )
            })}

            {/* Empty state */}
            {wf.nodes.length === 0 && (
              <text x="50%" y="50%" textAnchor="middle" dominantBaseline="middle" fontSize="13" fill="#45475a">
                Drag flows from the sidebar to build your workflow
              </text>
            )}
          </svg>

          {/* Listener name modal */}
          {pendingTargetId && (
            <div style={{ position: 'absolute', top: '50%', left: '50%', transform: 'translate(-50%,-50%)', background: '#1e1e2e', border: '1px solid #313244', borderRadius: 8, padding: 16, zIndex: 10, minWidth: 240 }}>
              <div style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4', marginBottom: 10 }}>Name the event listener</div>
              <input type="text" value={listenerInput} onChange={e => setListenerInput(e.target.value)}
                placeholder="e.g. payment.validated" autoFocus
                onKeyDown={e => { if (e.key === 'Enter') handleConfirmListener(); if (e.key === 'Escape') { setConnectSource(null); setPendingTargetId(null) } }}
                style={{ ...inputStyle, marginBottom: 10 }} />
              <div style={{ display: 'flex', gap: 8 }}>
                <button onClick={handleConfirmListener}
                  style={{ flex: 1, padding: '6px 0', borderRadius: 4, border: 'none', background: '#a6e3a1', color: '#1e1e2e', fontSize: 11, fontWeight: 700, cursor: 'pointer' }}>
                  Add Edge
                </button>
                <button onClick={() => { setConnectSource(null); setPendingTargetId(null); setListenerInput('') }}
                  style={{ flex: 1, padding: '6px 0', borderRadius: 4, border: '1px solid #45475a', background: 'transparent', color: '#6c7086', fontSize: 11, cursor: 'pointer' }}>
                  Cancel
                </button>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* ── Properties panel ─────────────────────────────────────── */}
      <aside style={{ width: 240, flexShrink: 0, background: '#181825', borderLeft: '1px solid #313244', overflowY: 'auto', padding: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
        {selectedNode ? (
          <>
            <div style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4', borderBottom: '1px solid #313244', paddingBottom: 8, marginBottom: 4 }}>Node</div>
            <div>
              <label style={labelStyle}>Flow</label>
              <div style={{ fontSize: 11, color: '#a6adc8' }}>{selectedNode.flowName}</div>
            </div>
            <div>
              <label style={labelStyle}>Label</label>
              <input style={inputStyle} value={selectedNode.label} onChange={e => updateSelectedNode('label', e.target.value)} />
            </div>
            <div>
              <label style={labelStyle}>Position</label>
              <div style={{ fontSize: 11, color: '#6c7086' }}>x: {selectedNode.x.toFixed(0)}, y: {selectedNode.y.toFixed(0)}</div>
            </div>
            <div>
              <label style={{ ...labelStyle, color: '#6c7086', fontSize: 10 }}>ID</label>
              <div style={{ fontSize: 10, color: '#45475a', wordBreak: 'break-all' }}>{selectedNode.id}</div>
            </div>
          </>
        ) : selectedEdge ? (
          <>
            <div style={{ fontSize: 12, fontWeight: 700, color: '#cdd6f4', borderBottom: '1px solid #313244', paddingBottom: 8, marginBottom: 4 }}>Edge</div>
            <div>
              <label style={labelStyle}>Source</label>
              <div style={{ fontSize: 11, color: '#a6adc8' }}>{sourceNode?.label || selectedEdge.sourceNodeId}</div>
            </div>
            <div>
              <label style={labelStyle}>Target</label>
              <div style={{ fontSize: 11, color: '#a6adc8' }}>{targetNode?.label || selectedEdge.targetNodeId}</div>
            </div>
            <div>
              <label style={labelStyle}>Listener Name</label>
              <input style={inputStyle} value={selectedEdge.listenerName} onChange={e => { updateSelectedEdge('listenerName', e.target.value); updateSelectedEdge('label', e.target.value) }} />
            </div>
            <div>
              <label style={labelStyle}>Display Label</label>
              <input style={inputStyle} value={selectedEdge.label} onChange={e => updateSelectedEdge('label', e.target.value)} />
            </div>
          </>
        ) : (
          <div style={{ fontSize: 11, color: '#45475a', marginTop: 8 }}>
            Select a node or edge to edit its properties.
            <div style={{ marginTop: 16, color: '#313244', lineHeight: 1.6 }}>
              <div>• Drag flows → canvas to add nodes</div>
              <div>• Click Connect to draw edges</div>
              <div>• Delete key removes selection</div>
              <div>• Save persists the workflow</div>
            </div>
          </div>
        )}

        {selectedWfName && (
          <button onClick={handleDeleteWorkflow}
            style={{ marginTop: 'auto', padding: '6px 0', borderRadius: 4, border: '1px solid #f38ba8', background: 'transparent', color: '#f38ba8', fontSize: 11, fontWeight: 700, cursor: 'pointer' }}>
            Delete Workflow
          </button>
        )}
      </aside>
    </div>
  )
}
