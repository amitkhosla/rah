import { useEffect, useState } from 'react'
import ConfirmDialog from './ConfirmDialog'
import {
  listMCPServers, upsertMCPServer, deleteMCPServer, pingMCPServer, probeMCPTools,
  listAPITools, upsertAPITool, deleteAPITool,
  listVirtualMCPServers, upsertVirtualMCPServer, deleteVirtualMCPServer,
} from '../api'
import type { MCPServer, MCPTransport, MCPPingResult, APIToolDef, VirtualMCPServer, ToolSource, ToolSourceKind } from '../types'

type SubTab = 'external' | 'virtual' | 'apitools'

// ── transport badge ─────────────────────────────────────────────────

const TRANSPORT_COLOR: Record<MCPTransport, string> = {
  http:  '#3b82f6',
  sse:   '#a78bfa',
  stdio: '#f97316',
}

function TransportBadge({ t }: { t: MCPTransport }) {
  return (
    <span style={{
      fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 4,
      color: '#fff', background: TRANSPORT_COLOR[t] ?? '#64748b', flexShrink: 0,
    }}>
      {t.toUpperCase()}
    </span>
  )
}

// ── blank forms ─────────────────────────────────────────────────────

function blankServer(): MCPServer {
  return { alias: '', transport: 'http', url: '', api_key_ref: '', timeout_ms: 10000 }
}

function blankTool(): APIToolDef {
  return { name: '', description: '', path: '', method: 'POST', auth_kind: 'none' }
}

function blankVirtual(): VirtualMCPServer {
  return { name: '', description: '', sources: [] }
}

// ── main component ──────────────────────────────────────────────────

export default function MCPServers() {
  const [sub, setSub] = useState<SubTab>('external')

  return (
    <div>
      {/* sub-nav pills */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 24 }}>
        {([
          ['external', 'External Servers'],
          ['virtual',  'Virtual Servers'],
          ['apitools', 'API Tools'],
        ] as [SubTab, string][]).map(([id, label]) => (
          <button
            key={id}
            onClick={() => setSub(id)}
            style={{
              padding: '6px 16px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 13,
              background: sub === id ? 'var(--accent)' : 'var(--step-bg)',
              color: sub === id ? '#031427' : 'var(--text)',
              fontWeight: sub === id ? 700 : 400,
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {sub === 'external' && <ExternalServers />}
      {sub === 'virtual'  && <VirtualServers />}
      {sub === 'apitools' && <APITools />}
    </div>
  )
}

// ── External MCP Servers ─────────────────────────────────────────────

function ExternalServers() {
  const [servers, setServers]   = useState<MCPServer[]>([])
  const [loading, setLoading]   = useState(true)
  const [err, setErr]           = useState('')
  const [showForm, setShowForm] = useState(false)
  const [form, setForm]         = useState<MCPServer>(blankServer())
  const [saving, setSaving]     = useState(false)
  const [msg, setMsg]           = useState('')
  const [msgErr, setMsgErr]     = useState(false)
  const [pings, setPings]       = useState<Record<string, MCPPingResult>>({})
  const [probed, setProbed]     = useState<Record<string, unknown[]>>({})
  const [pinging, setPinging]   = useState<Record<string, boolean>>({})
  const [confirmDialog, setConfirmDialog] = useState<{ title: string; message: string; onConfirm: () => void } | null>(null)

  async function load() {
    setLoading(true); setErr('')
    try { setServers(await listMCPServers()) }
    catch (e) { setErr(e instanceof Error ? e.message : 'Failed to load') }
    finally { setLoading(false) }
  }

  useEffect(() => { void load() }, [])

  function setF(k: keyof MCPServer, v: string | number | string[]) {
    setForm(f => ({ ...f, [k]: v }))
  }

  async function handleSave() {
    if (!form.alias.trim()) { setMsgErr(true); setMsg('Alias is required'); return }
    setSaving(true); setMsg(''); setMsgErr(false)
    try {
      await upsertMCPServer(form)
      setMsg(`Server "${form.alias}" saved.`)
      setForm(blankServer()); setShowForm(false); await load()
    } catch (e) { setMsgErr(true); setMsg(e instanceof Error ? e.message : 'Save failed') }
    finally { setSaving(false) }
  }

  async function handleDelete(alias: string) {
    setConfirmDialog({
      title: 'Delete MCP Server',
      message: `Delete MCP server "${alias}"?`,
      onConfirm: async () => {
        try { await deleteMCPServer(alias); await load() }
        catch (e) { alert(e instanceof Error ? e.message : 'Delete failed') }
        setConfirmDialog(null)
      }
    })
  }

  async function handlePing(alias: string) {
    setPinging(p => ({ ...p, [alias]: true }))
    try {
      const r = await pingMCPServer(alias)
      setPings(p => ({ ...p, [alias]: r }))
    } catch (e) {
      setPings(p => ({ ...p, [alias]: { alias, reachable: false, latency_ms: 0, error: String(e) } }))
    } finally {
      setPinging(p => ({ ...p, [alias]: false }))
    }
  }

  async function handleProbe(alias: string) {
    try {
      const tools = await probeMCPTools(alias)
      setProbed(p => ({ ...p, [alias]: tools }))
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Probe failed')
    }
  }

  return (
    <div>
      <SectionHeader
        title="External MCP Servers"
        count={servers.length}
        hint="Real MCP servers reachable by the gateway (HTTP, SSE, or stdio)"
        onAdd={() => { setForm(blankServer()); setShowForm(v => !v) }}
        addLabel={showForm ? 'Cancel' : '+ Add Server'}
      />
      {err && <p className="status-err" style={{ marginBottom: 12 }}>{err}</p>}

      {showForm && (
        <div className="panel" style={{ marginBottom: 20 }}>
          <div className="panel-header">{form.alias || 'New MCP Server'}</div>
          <div className="panel-body">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <Field label="Alias *" hint="Unique identifier used in flows">
                <input className="input" value={form.alias} placeholder="e.g. brave-search"
                  onChange={e => setF('alias', e.target.value)} />
              </Field>
              <Field label="Transport *" hint="How the gateway connects">
                <select className="input" value={form.transport}
                  onChange={e => setF('transport', e.target.value as MCPTransport)}>
                  <option value="http">http — HTTP POST to endpoint</option>
                  <option value="sse">sse — Server-Sent Events</option>
                  <option value="stdio">stdio — child process</option>
                </select>
              </Field>

              {(form.transport === 'http' || form.transport === 'sse') && (
                <Field label="URL *" hint="MCP endpoint URL">
                  <input className="input" value={form.url ?? ''} placeholder="https://mcp.example.com/v1"
                    onChange={e => setF('url', e.target.value)} />
                </Field>
              )}

              {form.transport === 'stdio' && (
                <Field label="Command" hint="Space-separated command + args">
                  <input className="input" value={(form.command ?? []).join(' ')}
                    placeholder="npx -y @modelcontextprotocol/server-brave-search"
                    onChange={e => setF('command', e.target.value.split(' ').filter(Boolean))} />
                </Field>
              )}

              <Field label="API Key Ref" hint="Credential name or secret ref">
                <input className="input" value={form.api_key_ref ?? ''} placeholder="mcp:brave-search"
                  onChange={e => setF('api_key_ref', e.target.value)} />
              </Field>
              <Field label="Timeout (ms)" hint="Request timeout in milliseconds">
                <input className="input" type="number" value={form.timeout_ms ?? 10000}
                  onChange={e => setF('timeout_ms', parseInt(e.target.value) || 10000)} />
              </Field>
            </div>

            <div style={{ marginTop: 16, display: 'flex', gap: 10, alignItems: 'center' }}>
              <button className="btn" style={{ width: 'auto', padding: '0 24px' }}
                onClick={handleSave} disabled={saving}>
                {saving ? 'Saving…' : 'Save Server'}
              </button>
              {msg && <span className={msgErr ? 'status-err' : 'status-ok'}>{msg}</span>}
            </div>
          </div>
        </div>
      )}

      {loading ? <p className="hint">Loading…</p> : servers.length === 0 ? (
        <EmptyState icon="⬡" text="No external MCP servers registered." />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {servers.map(s => {
            const ping   = pings[s.alias]
            const tools  = probed[s.alias]
            const busy   = pinging[s.alias]
            return (
              <div key={s.alias} className="panel">
                <div style={{ padding: '10px 14px', display: 'flex', alignItems: 'center', gap: 10 }}>
                  <TransportBadge t={s.transport} />
                  <span style={{ fontFamily: 'monospace', fontSize: 14, fontWeight: 600 }}>{s.alias}</span>
                  {s.url && <span style={{ color: 'var(--muted)', fontSize: 12, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.url}</span>}
                  {s.api_key_ref && <span title={s.api_key_ref} style={{ fontSize: 11, color: '#22c55e' }}>🔑</span>}

                  {/* ping result inline */}
                  {ping && (
                    <span style={{ fontSize: 11, color: ping.reachable ? '#22c55e' : '#ef4444' }}>
                      {ping.reachable ? `✓ ${ping.latency_ms}ms` : `✗ ${ping.error ?? 'unreachable'}`}
                    </span>
                  )}

                  <button className="btn muted" style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 11 }}
                    onClick={() => handlePing(s.alias)} disabled={busy}>
                    {busy ? '…' : 'Ping'}
                  </button>
                  {s.transport !== 'stdio' && (
                    <button className="btn muted" style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 11 }}
                      onClick={() => handleProbe(s.alias)}>
                      Probe tools
                    </button>
                  )}
                  <button className="btn muted" style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 12 }}
                    onClick={() => { setForm({ ...s }); setShowForm(true) }}>
                    Edit
                  </button>
                  <button className="btn muted" style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 12, color: '#ef4444' }}
                    onClick={() => handleDelete(s.alias)}>
                    ✕
                  </button>
                </div>
                {tools && tools.length > 0 && (
                  <div style={{ borderTop: '1px solid var(--border)', padding: '8px 14px', display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                    {(tools as { name?: string }[]).map((t, i) => (
                      <span key={i} style={{ fontSize: 11, background: 'var(--step-bg)', padding: '2px 8px', borderRadius: 4, color: 'var(--accent)' }}>
                        {t.name ?? JSON.stringify(t)}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
      {confirmDialog && (
        <ConfirmDialog
          title={confirmDialog.title}
          message={confirmDialog.message}
          onConfirm={confirmDialog.onConfirm}
          onCancel={() => setConfirmDialog(null)}
        />
      )}
    </div>
  )
}

// ── Virtual MCP Servers ──────────────────────────────────────────────

function VirtualServers() {
  const [servers, setServers]   = useState<VirtualMCPServer[]>([])
  const [loading, setLoading]   = useState(true)
  const [err, setErr]           = useState('')
  const [form, setForm]         = useState<VirtualMCPServer>(blankVirtual())
  const [showForm, setShowForm] = useState(false)
  const [saving, setSaving]     = useState(false)
  const [msg, setMsg]           = useState('')
  const [msgErr, setMsgErr]     = useState(false)
  // new source being added
  const [srcKind, setSrcKind]   = useState<ToolSourceKind>('mcp_all')
  const [srcAlias, setSrcAlias] = useState('')
  const [srcTool, setSrcTool]   = useState('')
  const [confirmDialog, setConfirmDialog] = useState<{ title: string; message: string; onConfirm: () => void } | null>(null)

  async function load() {
    setLoading(true); setErr('')
    try { setServers(await listVirtualMCPServers()) }
    catch (e) { setErr(e instanceof Error ? e.message : 'Failed to load') }
    finally { setLoading(false) }
  }

  useEffect(() => { void load() }, [])

  function addSource() {
    if (!srcAlias.trim() && srcKind !== 'api_tool') return
    const src: ToolSource = { kind: srcKind, server_alias: srcAlias.trim(), tool_name: srcTool.trim() || undefined }
    setForm(f => ({ ...f, sources: [...f.sources, src] }))
    setSrcAlias(''); setSrcTool('')
  }

  function removeSource(i: number) {
    setForm(f => ({ ...f, sources: f.sources.filter((_, idx) => idx !== i) }))
  }

  async function handleSave() {
    if (!form.name.trim()) { setMsgErr(true); setMsg('Name is required'); return }
    setSaving(true); setMsg(''); setMsgErr(false)
    try {
      await upsertVirtualMCPServer(form)
      setMsg(`Virtual server "${form.name}" saved.`)
      setForm(blankVirtual()); setShowForm(false); await load()
    } catch (e) { setMsgErr(true); setMsg(e instanceof Error ? e.message : 'Save failed') }
    finally { setSaving(false) }
  }

  async function handleDelete(name: string) {
    setConfirmDialog({
      title: 'Delete Virtual Server',
      message: `Delete virtual server "${name}"?`,
      onConfirm: async () => {
        try { await deleteVirtualMCPServer(name); await load() }
        catch (e) { alert(e instanceof Error ? e.message : 'Delete failed') }
        setConfirmDialog(null)
      }
    })
  }

  const SOURCE_KIND_LABEL: Record<ToolSourceKind, string> = {
    api_tool: 'API Tool',
    mcp_tool: 'Specific tool from server',
    mcp_all:  'All tools from server',
  }

  return (
    <div>
      <SectionHeader
        title="Virtual MCP Servers"
        count={servers.length}
        hint="Composed tool sets that aggregate APIs and external MCP tools under one name"
        onAdd={() => { setForm(blankVirtual()); setShowForm(v => !v) }}
        addLabel={showForm ? 'Cancel' : '+ Create Virtual Server'}
      />
      {err && <p className="status-err" style={{ marginBottom: 12 }}>{err}</p>}

      {showForm && (
        <div className="panel" style={{ marginBottom: 20 }}>
          <div className="panel-header">{form.name || 'New Virtual Server'}</div>
          <div className="panel-body">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <Field label="Name *" hint="Unique server name">
                <input className="input" value={form.name} placeholder="e.g. research-tools"
                  onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
              </Field>
              <Field label="Description" hint="Optional description shown to LLM clients">
                <input className="input" value={form.description ?? ''}
                  placeholder="Tools for research and web search"
                  onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
              </Field>
            </div>

            {/* Sources */}
            <div style={{ marginTop: 16 }}>
              <div style={{ fontSize: 11, color: 'var(--muted)', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 8 }}>
                Tool Sources ({form.sources.length})
              </div>

              {form.sources.map((src, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, padding: '6px 10px', background: 'var(--step-bg)', borderRadius: 6 }}>
                  <SourceKindBadge kind={src.kind} />
                  <span style={{ fontFamily: 'monospace', fontSize: 12, flex: 1 }}>
                    {src.server_alias ?? ''}
                    {src.tool_name ? ` → ${src.tool_name}` : ''}
                  </span>
                  <button className="btn muted" style={{ width: 'auto', padding: '2px 8px', marginTop: 0, fontSize: 11 }}
                    onClick={() => removeSource(i)}>✕</button>
                </div>
              ))}

              {/* Add source row */}
              <div style={{ display: 'flex', gap: 8, marginTop: 8, flexWrap: 'wrap', alignItems: 'flex-end' }}>
                <select className="input" value={srcKind} onChange={e => setSrcKind(e.target.value as ToolSourceKind)}
                  style={{ width: 200, marginTop: 0 }}>
                  {(Object.entries(SOURCE_KIND_LABEL) as [ToolSourceKind, string][]).map(([k, l]) => (
                    <option key={k} value={k}>{l}</option>
                  ))}
                </select>
                {srcKind !== 'api_tool' && (
                  <input className="input" value={srcAlias} placeholder="server alias"
                    onChange={e => setSrcAlias(e.target.value)} style={{ flex: 1, minWidth: 140, marginTop: 0 }} />
                )}
                {srcKind === 'mcp_tool' && (
                  <input className="input" value={srcTool} placeholder="tool name"
                    onChange={e => setSrcTool(e.target.value)} style={{ flex: 1, minWidth: 120, marginTop: 0 }} />
                )}
                <button className="btn" style={{ width: 'auto', padding: '0 16px', marginTop: 0 }}
                  onClick={addSource}>
                  + Add Source
                </button>
              </div>
            </div>

            <div style={{ marginTop: 16, display: 'flex', gap: 10, alignItems: 'center' }}>
              <button className="btn" style={{ width: 'auto', padding: '0 24px' }}
                onClick={handleSave} disabled={saving}>
                {saving ? 'Saving…' : 'Save Virtual Server'}
              </button>
              {msg && <span className={msgErr ? 'status-err' : 'status-ok'}>{msg}</span>}
            </div>
          </div>
        </div>
      )}

      {loading ? <p className="hint">Loading…</p> : servers.length === 0 ? (
        <EmptyState icon="⬡" text="No virtual servers defined." />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {servers.map(s => (
            <div key={s.name} className="panel">
              <div style={{ padding: '10px 14px', display: 'flex', alignItems: 'center', gap: 10 }}>
                <span style={{ fontFamily: 'monospace', fontSize: 14, fontWeight: 600, flex: 1 }}>{s.name}</span>
                {s.description && <span style={{ color: 'var(--muted)', fontSize: 12 }}>{s.description}</span>}
                <span style={{ fontSize: 11, color: 'var(--muted)', background: 'var(--step-bg)', borderRadius: 10, padding: '1px 8px' }}>
                  {s.sources.length} source{s.sources.length !== 1 ? 's' : ''}
                </span>
                <button className="btn muted" style={{ width: 'auto', padding: '3px 12px', marginTop: 0, fontSize: 12 }}
                  onClick={() => { setForm({ ...s, sources: [...s.sources] }); setShowForm(true) }}>Edit</button>
                <button className="btn muted" style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 12, color: '#ef4444' }}
                  onClick={() => handleDelete(s.name)}>✕</button>
              </div>
              {s.sources.length > 0 && (
                <div style={{ borderTop: '1px solid var(--border)', padding: '8px 14px', display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  {s.sources.map((src, i) => (
                    <span key={i} style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 11, background: 'var(--step-bg)', padding: '2px 8px', borderRadius: 4 }}>
                      <SourceKindBadge kind={src.kind} />
                      <span style={{ fontFamily: 'monospace' }}>
                        {src.server_alias ?? ''}{src.tool_name ? ` / ${src.tool_name}` : ''}
                      </span>
                    </span>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
      {confirmDialog && (
        <ConfirmDialog
          title={confirmDialog.title}
          message={confirmDialog.message}
          onConfirm={confirmDialog.onConfirm}
          onCancel={() => setConfirmDialog(null)}
        />
      )}
    </div>
  )
}

// ── API Tools ────────────────────────────────────────────────────────

function APITools() {
  const [tools, setTools]       = useState<APIToolDef[]>([])
  const [loading, setLoading]   = useState(true)
  const [err, setErr]           = useState('')
  const [form, setForm]         = useState<APIToolDef>(blankTool())
  const [showForm, setShowForm] = useState(false)
  const [saving, setSaving]     = useState(false)
  const [msg, setMsg]           = useState('')
  const [msgErr, setMsgErr]     = useState(false)
  const [confirmDialog, setConfirmDialog] = useState<{ title: string; message: string; onConfirm: () => void } | null>(null)

  async function load() {
    setLoading(true); setErr('')
    try { setTools(await listAPITools()) }
    catch (e) { setErr(e instanceof Error ? e.message : 'Failed to load') }
    finally { setLoading(false) }
  }

  useEffect(() => { void load() }, [])

  function setF(k: keyof APIToolDef, v: string) {
    setForm(f => ({ ...f, [k]: v }))
  }

  async function handleSave() {
    if (!form.name.trim() || !form.path.trim()) { setMsgErr(true); setMsg('Name and path are required'); return }
    setSaving(true); setMsg(''); setMsgErr(false)
    try {
      await upsertAPITool(form)
      setMsg(`Tool "${form.name}" saved.`)
      setForm(blankTool()); setShowForm(false); await load()
    } catch (e) { setMsgErr(true); setMsg(e instanceof Error ? e.message : 'Save failed') }
    finally { setSaving(false) }
  }

  async function handleDelete(name: string) {
    setConfirmDialog({
      title: 'Delete API Tool',
      message: `Delete API tool "${name}"?`,
      onConfirm: async () => {
        try { await deleteAPITool(name); await load() }
        catch (e) { alert(e instanceof Error ? e.message : 'Delete failed') }
        setConfirmDialog(null)
      }
    })
  }

  return (
    <div>
      <SectionHeader
        title="API Tools"
        count={tools.length}
        hint="RAH API endpoints exposed as callable tools in MCP virtual servers"
        onAdd={() => { setForm(blankTool()); setShowForm(v => !v) }}
        addLabel={showForm ? 'Cancel' : '+ Register API Tool'}
      />
      {err && <p className="status-err" style={{ marginBottom: 12 }}>{err}</p>}

      {showForm && (
        <div className="panel" style={{ marginBottom: 20 }}>
          <div className="panel-header">{form.name || 'New API Tool'}</div>
          <div className="panel-body">
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <Field label="Tool Name *" hint="Name exposed to LLM clients">
                <input className="input" value={form.name} placeholder="e.g. search_knowledge_base"
                  onChange={e => setF('name', e.target.value)} />
              </Field>
              <Field label="Description" hint="What this tool does (shown to LLM)">
                <input className="input" value={form.description}
                  placeholder="Search the knowledge base for relevant documents"
                  onChange={e => setF('description', e.target.value)} />
              </Field>
              <Field label="Method" hint="HTTP method">
                <select className="input" value={form.method} onChange={e => setF('method', e.target.value)}>
                  {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => <option key={m}>{m}</option>)}
                </select>
              </Field>
              <Field label="Path *" hint="Gateway endpoint path">
                <input className="input" value={form.path} placeholder="/v1/kb/search"
                  onChange={e => setF('path', e.target.value)} />
              </Field>
              <Field label="Auth Kind" hint="How to authenticate calls to this endpoint">
                <select className="input" value={form.auth_kind ?? 'none'} onChange={e => setF('auth_kind', e.target.value)}>
                  <option value="none">none</option>
                  <option value="bearer">bearer token</option>
                  <option value="header">custom header</option>
                </select>
              </Field>
              {form.auth_kind && form.auth_kind !== 'none' && (
                <Field label={form.auth_kind === 'header' ? 'Header Name' : 'API Key Ref'} hint="Credential reference">
                  {form.auth_kind === 'header' && (
                    <input className="input" value={form.auth_header ?? ''} placeholder="X-API-Key"
                      onChange={e => setF('auth_header', e.target.value)} />
                  )}
                  <input className="input" value={form.auth_key_ref ?? ''} placeholder="mcp:my-tool-key"
                    onChange={e => setF('auth_key_ref', e.target.value)} style={{ marginTop: form.auth_kind === 'header' ? 8 : 0 }} />
                </Field>
              )}
            </div>

            <div style={{ marginTop: 16, display: 'flex', gap: 10, alignItems: 'center' }}>
              <button className="btn" style={{ width: 'auto', padding: '0 24px' }}
                onClick={handleSave} disabled={saving}>
                {saving ? 'Saving…' : 'Save Tool'}
              </button>
              {msg && <span className={msgErr ? 'status-err' : 'status-ok'}>{msg}</span>}
            </div>
          </div>
        </div>
      )}

      {loading ? <p className="hint">Loading…</p> : tools.length === 0 ? (
        <EmptyState icon="⬡" text="No API tools registered." />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {tools.map(t => (
            <div key={t.name} className="step" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 14px' }}>
              <span style={{ fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 4, color: '#fff',
                background: { GET: '#22c55e', POST: '#3b82f6', PUT: '#f97316', PATCH: '#eab308', DELETE: '#ef4444' }[t.method] ?? '#64748b', flexShrink: 0 }}>
                {t.method}
              </span>
              <span style={{ fontFamily: 'monospace', fontSize: 13, fontWeight: 600, minWidth: 160 }}>{t.name}</span>
              <span style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--muted)', flex: 1 }}>{t.path}</span>
              <span style={{ fontSize: 12, color: 'var(--muted)' }}>{t.description}</span>
              {t.auth_key_ref && <span style={{ fontSize: 11, color: '#22c55e' }} title={t.auth_key_ref}>🔑</span>}
              <button className="btn muted" style={{ width: 'auto', padding: '3px 12px', marginTop: 0, fontSize: 12 }}
                onClick={() => { setForm({ ...t }); setShowForm(true) }}>Edit</button>
              <button className="btn muted" style={{ width: 'auto', padding: '3px 10px', marginTop: 0, fontSize: 12, color: '#ef4444' }}
                onClick={() => handleDelete(t.name)}>✕</button>
            </div>
          ))}
        </div>
      )}
      {confirmDialog && (
        <ConfirmDialog
          title={confirmDialog.title}
          message={confirmDialog.message}
          onConfirm={confirmDialog.onConfirm}
          onCancel={() => setConfirmDialog(null)}
        />
      )}
    </div>
  )
}

// ── Shared helpers ────────────────────────────────────────────────────

function SectionHeader({ title, count, hint, onAdd, addLabel }: {
  title: string; count: number; hint: string; onAdd: () => void; addLabel: string
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 20 }}>
      <div>
        <span style={{ fontSize: 15, fontWeight: 700 }}>{title}</span>
        <span style={{ color: 'var(--muted)', fontSize: 13, marginLeft: 10 }}>{count} registered</span>
        <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 2 }}>{hint}</div>
      </div>
      <button className="btn" style={{ width: 'auto', padding: '0 18px', flexShrink: 0 }} onClick={onAdd}>
        {addLabel}
      </button>
    </div>
  )
}

function EmptyState({ icon, text }: { icon: string; text: string }) {
  return (
    <div style={{ textAlign: 'center', padding: '40px 0', color: 'var(--muted)' }}>
      <div style={{ fontSize: 32, opacity: 0.3, marginBottom: 12 }}>{icon}</div>
      <p>{text}</p>
    </div>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="field-label">{label}</label>
      {children}
      {hint && <span className="field-desc">{hint}</span>}
    </div>
  )
}

const SOURCE_KIND_COLOR: Record<ToolSourceKind, string> = {
  api_tool: '#22c55e',
  mcp_tool: '#3b82f6',
  mcp_all:  '#a78bfa',
}

function SourceKindBadge({ kind }: { kind: ToolSourceKind }) {
  const labels: Record<ToolSourceKind, string> = { api_tool: 'API', mcp_tool: 'TOOL', mcp_all: 'ALL' }
  return (
    <span style={{
      fontSize: 9, fontWeight: 700, padding: '1px 5px', borderRadius: 3,
      color: '#fff', background: SOURCE_KIND_COLOR[kind] ?? '#64748b',
    }}>
      {labels[kind]}
    </span>
  )
}
