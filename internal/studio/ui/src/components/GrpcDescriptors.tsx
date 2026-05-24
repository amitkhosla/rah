import { useEffect, useRef, useState } from 'react'
import {
  listGrpcDescriptors,
  uploadGrpcDescriptor,
  deleteGrpcDescriptor,
} from '../api'
import type { GrpcDescriptorSummary } from '../types'

const C = {
  base:    '#1e1e2e',
  surface: '#181825',
  overlay: '#313244',
  muted:   '#6c7086',
  text:    '#cdd6f4',
  green:   '#a6e3a1',
  red:     '#f38ba8',
  yellow:  '#f9e2af',
  blue:    '#89b4fa',
  mauve:   '#cba6f7',
  border:  'rgba(205,214,244,0.10)',
}

export default function GrpcDescriptors() {
  const [sets, setSets] = useState<GrpcDescriptorSummary[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  // Upload state
  const [uploading, setUploading] = useState(false)
  const [uploadName, setUploadName] = useState('')
  const [uploadFile, setUploadFile] = useState<File | null>(null)
  const [uploadErr, setUploadErr] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Delete confirm
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)

  const load = () => {
    setLoading(true)
    setErr(null)
    listGrpcDescriptors()
      .then(setSets)
      .catch(e => setErr(String(e)))
      .finally(() => setLoading(false))
  }

  useEffect(load, [])

  const selectedSet = sets.find(s => s.name === selected) ?? null

  async function handleUpload() {
    if (!uploadName.trim()) { setUploadErr('Name is required'); return }
    if (!uploadFile) { setUploadErr('Select a .pb file'); return }
    setUploading(true)
    setUploadErr(null)
    try {
      await uploadGrpcDescriptor(uploadName.trim(), uploadFile)
      setUploadName('')
      setUploadFile(null)
      if (fileInputRef.current) fileInputRef.current.value = ''
      load()
    } catch (e) {
      setUploadErr(String(e))
    } finally {
      setUploading(false)
    }
  }

  async function handleDelete(name: string) {
    try {
      await deleteGrpcDescriptor(name)
      if (selected === name) setSelected(null)
      setConfirmDelete(null)
      load()
    } catch (e) {
      setErr(String(e))
    }
  }

  const panelStyle: React.CSSProperties = {
    background: C.surface,
    border: `1px solid ${C.border}`,
    borderRadius: 8,
    overflow: 'hidden',
    display: 'flex',
    flexDirection: 'column',
  }

  return (
    <div style={{ padding: '20px 24px', height: '100%', boxSizing: 'border-box', display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <span style={{ fontWeight: 700, fontSize: 16, color: C.text }}>gRPC Descriptors</span>
        <span style={{ fontSize: 12, color: C.muted }}>Compiled FileDescriptorSet blobs for grpc_call transcoding</span>
        <button onClick={load} style={{ marginLeft: 'auto', fontSize: 12, padding: '4px 12px', borderRadius: 6, border: `1px solid ${C.border}`, background: 'transparent', color: C.muted, cursor: 'pointer' }}>
          ↺ Refresh
        </button>
      </div>

      {err && <div style={{ color: C.red, fontSize: 12, padding: '6px 10px', background: 'rgba(243,139,168,0.08)', borderRadius: 6 }}>{err}</div>}

      <div style={{ display: 'flex', gap: 16, flex: 1, minHeight: 0 }}>

        {/* ── Left: list + upload ── */}
        <div style={{ width: 280, display: 'flex', flexDirection: 'column', gap: 12, flexShrink: 0 }}>

          {/* Upload card */}
          <div style={{ ...panelStyle, padding: 14 }}>
            <div style={{ fontSize: 12, fontWeight: 700, color: C.mauve, marginBottom: 10 }}>Upload Descriptor Set</div>
            <div style={{ fontSize: 11, color: C.muted, marginBottom: 8 }}>
              Generate with: <code style={{ fontSize: 10, color: C.blue }}>protoc --descriptor_set_out=svc.pb --include_imports svc.proto</code>
            </div>
            <input
              placeholder="Set name (e.g. user-service)"
              value={uploadName}
              onChange={e => setUploadName(e.target.value)}
              style={{ width: '100%', boxSizing: 'border-box', marginBottom: 8, padding: '5px 8px', borderRadius: 6, border: `1px solid ${C.border}`, background: C.overlay, color: C.text, fontSize: 12 }}
            />
            <input
              ref={fileInputRef}
              type="file"
              accept=".pb,application/octet-stream"
              onChange={e => setUploadFile(e.target.files?.[0] ?? null)}
              style={{ fontSize: 11, color: C.muted, marginBottom: 8, width: '100%' }}
            />
            {uploadErr && <div style={{ color: C.red, fontSize: 11, marginBottom: 6 }}>{uploadErr}</div>}
            <button
              onClick={handleUpload}
              disabled={uploading}
              style={{ padding: '6px 0', borderRadius: 6, border: 'none', background: 'var(--accent)', color: '#fff', fontSize: 12, fontWeight: 700, cursor: uploading ? 'not-allowed' : 'pointer', width: '100%', opacity: uploading ? 0.6 : 1 }}
            >
              {uploading ? 'Uploading…' : '↑ Upload'}
            </button>
          </div>

          {/* Descriptor list */}
          <div style={{ ...panelStyle, flex: 1 }}>
            <div style={{ padding: '10px 14px', borderBottom: `1px solid ${C.border}`, fontSize: 12, fontWeight: 700, color: C.text }}>
              Loaded Sets {loading ? '…' : `(${sets.length})`}
            </div>
            <div style={{ overflowY: 'auto', flex: 1 }}>
              {sets.length === 0 && !loading && (
                <div style={{ padding: '20px 14px', color: C.muted, fontSize: 12, textAlign: 'center' }}>
                  No descriptor sets uploaded yet
                </div>
              )}
              {sets.map(s => (
                <div
                  key={s.name}
                  onClick={() => setSelected(s.name)}
                  style={{
                    padding: '9px 14px',
                    borderBottom: `1px solid ${C.border}`,
                    cursor: 'pointer',
                    background: selected === s.name ? 'rgba(137,180,250,0.08)' : 'transparent',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 8,
                  }}
                >
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 13, color: selected === s.name ? C.blue : C.text, fontWeight: selected === s.name ? 700 : 400, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.name}</div>
                    <div style={{ fontSize: 11, color: C.muted }}>{s.services?.length ?? 0} service{(s.services?.length ?? 0) !== 1 ? 's' : ''}</div>
                  </div>
                  <button
                    onClick={e => { e.stopPropagation(); setConfirmDelete(s.name) }}
                    style={{ padding: '2px 6px', borderRadius: 4, border: `1px solid ${C.border}`, background: 'transparent', color: C.red, fontSize: 11, cursor: 'pointer', flexShrink: 0 }}
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* ── Right: detail ── */}
        <div style={{ ...panelStyle, flex: 1, minWidth: 0 }}>
          {!selectedSet ? (
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', flex: 1, color: C.muted, fontSize: 13 }}>
              Select a descriptor set to browse services and methods
            </div>
          ) : (
            <>
              <div style={{ padding: '12px 16px', borderBottom: `1px solid ${C.border}`, display: 'flex', alignItems: 'center', gap: 10 }}>
                <span style={{ fontWeight: 700, fontSize: 14, color: C.text }}>{selectedSet.name}</span>
                {selectedSet.uploaded_at && (
                  <span style={{ fontSize: 11, color: C.muted }}>uploaded {new Date(selectedSet.uploaded_at).toLocaleString()}</span>
                )}
              </div>
              <div style={{ overflowY: 'auto', flex: 1, padding: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
                {(selectedSet.services ?? []).length === 0 && (
                  <div style={{ color: C.muted, fontSize: 12 }}>No services found in this descriptor set.</div>
                )}
                {(selectedSet.services ?? []).map(svc => (
                  <div key={svc.full_name} style={{ border: `1px solid ${C.border}`, borderRadius: 8, overflow: 'hidden' }}>
                    <div style={{ padding: '8px 14px', background: C.overlay, display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 13, fontWeight: 700, color: C.mauve }}>{svc.full_name}</span>
                      <span style={{ fontSize: 11, color: C.muted, marginLeft: 'auto' }}>{svc.methods?.length ?? 0} method{(svc.methods?.length ?? 0) !== 1 ? 's' : ''}</span>
                    </div>
                    <div style={{ padding: '8px 14px', display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                      {(svc.methods ?? []).map(m => (
                        <span key={m} style={{ fontSize: 12, padding: '3px 10px', borderRadius: 12, background: 'rgba(166,227,161,0.10)', color: C.green, border: `1px solid rgba(166,227,161,0.20)` }}>{m}</span>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      </div>

      {/* Delete confirm modal */}
      {confirmDelete && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: C.base, border: `1px solid ${C.border}`, borderRadius: 10, padding: 24, maxWidth: 360, width: '90%' }}>
            <div style={{ fontWeight: 700, fontSize: 15, color: C.text, marginBottom: 10 }}>Delete descriptor set?</div>
            <div style={{ fontSize: 13, color: C.muted, marginBottom: 20 }}>
              <strong style={{ color: C.red }}>{confirmDelete}</strong> will be removed from the gateway and all grpc_call steps referencing it will fail at runtime.
            </div>
            <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
              <button onClick={() => setConfirmDelete(null)} style={{ padding: '6px 16px', borderRadius: 6, border: `1px solid ${C.border}`, background: 'transparent', color: C.muted, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
              <button onClick={() => handleDelete(confirmDelete)} style={{ padding: '6px 16px', borderRadius: 6, border: 'none', background: C.red, color: '#fff', cursor: 'pointer', fontSize: 13, fontWeight: 700 }}>Delete</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
