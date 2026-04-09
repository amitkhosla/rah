import { useState, useEffect, useCallback } from 'react'

// ── Data model ───────────────────────────────────────────────────────────────

interface SavedPrompt {
  id: string
  name: string
  content: string
  tags: string[]
  createdAt: string
  updatedAt: string
}

const STORAGE_KEY = 'rah_studio_prompts'

const STARTER_PROMPTS: SavedPrompt[] = [
  {
    id: 'starter-1',
    name: 'Helpful Assistant',
    content: "You are a helpful, accurate, and concise assistant. Answer questions clearly and acknowledge when you don't know something.",
    tags: ['general'],
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  },
  {
    id: 'starter-2',
    name: 'RAG Synthesis',
    content: "You are an expert at synthesizing information from retrieved documents. Given context passages, answer the user's question accurately. If the context doesn't contain enough information, say so clearly. Always cite which passage your answer comes from.",
    tags: ['rag', 'retrieval'],
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  },
  {
    id: 'starter-3',
    name: 'Tool-Use Agent',
    content: 'You are an intelligent agent with access to tools. When given a task:\n1. Break it into steps\n2. Use tools to gather information\n3. Synthesize results into a clear answer\nAlways explain what tools you used and why.',
    tags: ['agent', 'tools'],
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  },
]

// ── Helpers ──────────────────────────────────────────────────────────────────

function loadPrompts(): SavedPrompt[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed) && parsed.length > 0) return parsed
    }
  } catch {
    // ignore parse errors
  }
  savePrompts(STARTER_PROMPTS)
  return STARTER_PROMPTS
}

function savePrompts(prompts: SavedPrompt[]): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(prompts))
  } catch {
    // ignore storage errors
  }
}

function makeId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 6)
}

function parseTags(raw: string): string[] {
  return raw
    .split(',')
    .map(t => t.trim())
    .filter(t => t.length > 0)
}

// ── TagPill ───────────────────────────────────────────────────────────────────

function TagPill({ tag }: { tag: string }) {
  return (
    <span
      style={{
        display: 'inline-block',
        background: 'var(--step-bg)',
        color: 'var(--muted)',
        fontSize: 10,
        padding: '2px 7px',
        borderRadius: 10,
        border: '1px solid var(--border)',
        marginRight: 3,
        whiteSpace: 'nowrap',
      }}
    >
      {tag}
    </span>
  )
}

// ── Left panel ────────────────────────────────────────────────────────────────

interface LeftPanelProps {
  prompts: SavedPrompt[]
  selectedId: string | null
  onSelect: (id: string) => void
  onNew: () => void
  isNewMode: boolean
}

function LeftPanel({ prompts, selectedId, onSelect, onNew, isNewMode }: LeftPanelProps) {
  const [search, setSearch] = useState('')

  const filtered = search.trim()
    ? prompts.filter(p => {
        const q = search.toLowerCase()
        return (
          p.name.toLowerCase().includes(q) ||
          p.content.toLowerCase().includes(q) ||
          p.tags.some(t => t.toLowerCase().includes(q))
        )
      })
    : prompts

  return (
    <div
      style={{
        width: 280,
        flexShrink: 0,
        background: 'var(--panel)',
        borderRight: '1px solid var(--border)',
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
      }}
    >
      {/* Search */}
      <div style={{ padding: '10px 12px', borderBottom: '1px solid var(--border)' }}>
        <input
          className="input"
          placeholder="Search prompts..."
          value={search}
          onChange={e => setSearch(e.target.value)}
          style={{ fontSize: 12 }}
        />
      </div>

      {/* List */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {filtered.length === 0 && (
          <div style={{ padding: '16px 14px', color: 'var(--muted)', fontSize: 12 }}>
            No prompts found.
          </div>
        )}
        {filtered.map(p => {
          const isSelected = !isNewMode && selectedId === p.id
          return (
            <div
              key={p.id}
              onClick={() => onSelect(p.id)}
              style={{
                padding: '10px 14px',
                cursor: 'pointer',
                borderLeft: isSelected ? '3px solid var(--accent)' : '3px solid transparent',
                background: isSelected ? 'rgba(87,181,255,0.07)' : 'transparent',
                borderBottom: '1px solid var(--border)',
                transition: 'background 0.1s',
              }}
              onMouseEnter={e => {
                if (!isSelected) (e.currentTarget as HTMLDivElement).style.background = 'rgba(255,255,255,0.03)'
              }}
              onMouseLeave={e => {
                if (!isSelected) (e.currentTarget as HTMLDivElement).style.background = 'transparent'
              }}
            >
              <div style={{ fontWeight: 700, fontSize: 13, color: 'var(--text)', marginBottom: 3 }}>
                {p.name}
              </div>
              <div
                style={{
                  fontSize: 11,
                  color: 'var(--muted)',
                  marginBottom: 5,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  maxWidth: 220,
                }}
              >
                {p.content.slice(0, 80)}
              </div>
              {p.tags.length > 0 && (
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 2 }}>
                  {p.tags.map(t => <TagPill key={t} tag={t} />)}
                </div>
              )}
            </div>
          )
        })}
      </div>

      {/* New Prompt button */}
      <div style={{ padding: '10px 12px', borderTop: '1px solid var(--border)' }}>
        <button
          className={isNewMode ? 'btn' : 'btn muted'}
          onClick={onNew}
          style={{ fontSize: 13 }}
        >
          + New Prompt
        </button>
      </div>
    </div>
  )
}

// ── Detail / Edit panel ───────────────────────────────────────────────────────

interface DetailPanelProps {
  prompt: SavedPrompt
  onSave: (updated: SavedPrompt) => void
  onDelete: (id: string) => void
}

function DetailPanel({ prompt, onSave, onDelete }: DetailPanelProps) {
  const [name, setName] = useState(prompt.name)
  const [tagsRaw, setTagsRaw] = useState(prompt.tags.join(', '))
  const [content, setContent] = useState(prompt.content)
  const [copied, setCopied] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)

  // Reset local state when selected prompt changes
  useEffect(() => {
    setName(prompt.name)
    setTagsRaw(prompt.tags.join(', '))
    setContent(prompt.content)
    setCopied(false)
    setConfirmDelete(false)
  }, [prompt.id])

  const isDirty =
    name !== prompt.name ||
    content !== prompt.content ||
    parseTags(tagsRaw).join(',') !== prompt.tags.join(',')

  const handleSave = () => {
    if (!name.trim()) return
    onSave({
      ...prompt,
      name: name.trim(),
      content,
      tags: parseTags(tagsRaw),
      updatedAt: new Date().toISOString(),
    })
  }

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(content).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }, [content])

  const handleDelete = () => {
    if (confirmDelete) {
      onDelete(prompt.id)
    } else {
      setConfirmDelete(true)
      setTimeout(() => setConfirmDelete(false), 3000)
    }
  }

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', padding: 20, overflowY: 'auto' }}>
      {/* Name */}
      <div className="field-row">
        <label className="field-label">Name</label>
        <input
          className="input"
          value={name}
          onChange={e => setName(e.target.value)}
          style={{ fontSize: 15, fontWeight: 600 }}
          placeholder="Prompt name..."
        />
      </div>

      {/* Tags */}
      <div className="field-row">
        <label className="field-label">Tags</label>
        <input
          className="input"
          value={tagsRaw}
          onChange={e => setTagsRaw(e.target.value)}
          placeholder="e.g. customer-support, rag"
          style={{ fontSize: 12 }}
        />
        <span className="field-desc">Comma-separated tags for filtering</span>
      </div>

      {/* Content */}
      <div className="field-row" style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        <label className="field-label">Content</label>
        <textarea
          className="input"
          value={content}
          onChange={e => setContent(e.target.value)}
          placeholder="System prompt content..."
          style={{
            flex: 1,
            minHeight: 300,
            fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
            fontSize: 12,
            resize: 'vertical',
          }}
        />
      </div>

      {/* Action buttons */}
      <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
        <button
          className="btn"
          onClick={handleSave}
          disabled={!isDirty || !name.trim()}
          style={{ flex: 1, opacity: isDirty && name.trim() ? 1 : 0.45 }}
        >
          Save Changes
        </button>
        <button
          className="btn muted"
          onClick={handleCopy}
          style={{ flex: 1 }}
        >
          {copied ? 'Copied!' : 'Copy to Clipboard'}
        </button>
        <button
          onClick={handleDelete}
          style={{
            flex: 1,
            padding: 9,
            borderRadius: 8,
            border: 'none',
            background: confirmDelete ? '#b71c1c' : '#3a1a1a',
            color: confirmDelete ? '#fff' : '#ff7043',
            fontWeight: 700,
            fontSize: 13,
            cursor: 'pointer',
            transition: 'background 0.2s',
          }}
        >
          {confirmDelete ? 'Confirm Delete' : 'Delete'}
        </button>
      </div>

      {/* Metadata */}
      <div style={{ marginTop: 12, fontSize: 11, color: 'var(--muted)' }}>
        Created {new Date(prompt.createdAt).toLocaleString()} &middot; Updated {new Date(prompt.updatedAt).toLocaleString()}
      </div>
    </div>
  )
}

// ── New Prompt panel ──────────────────────────────────────────────────────────

interface NewPromptPanelProps {
  onSave: (prompt: SavedPrompt) => void
  onCancel: () => void
}

function NewPromptPanel({ onSave, onCancel }: NewPromptPanelProps) {
  const [name, setName] = useState('')
  const [tagsRaw, setTagsRaw] = useState('')
  const [content, setContent] = useState('')

  const handleSave = () => {
    if (!name.trim() || !content.trim()) return
    const now = new Date().toISOString()
    onSave({
      id: makeId(),
      name: name.trim(),
      content,
      tags: parseTags(tagsRaw),
      createdAt: now,
      updatedAt: now,
    })
  }

  const canSave = name.trim().length > 0 && content.trim().length > 0

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', padding: 20, overflowY: 'auto' }}>
      <div style={{ fontWeight: 700, fontSize: 15, marginBottom: 16, color: 'var(--accent)' }}>
        New Prompt
      </div>

      {/* Name */}
      <div className="field-row">
        <label className="field-label">Name</label>
        <input
          className="input"
          value={name}
          onChange={e => setName(e.target.value)}
          placeholder="Give this prompt a name..."
          autoFocus
        />
      </div>

      {/* Tags */}
      <div className="field-row">
        <label className="field-label">Tags</label>
        <input
          className="input"
          value={tagsRaw}
          onChange={e => setTagsRaw(e.target.value)}
          placeholder="e.g. customer-support, rag"
          style={{ fontSize: 12 }}
        />
        <span className="field-desc">Comma-separated, e.g. customer-support, rag</span>
      </div>

      {/* Content */}
      <div className="field-row" style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        <label className="field-label">Content</label>
        <textarea
          className="input"
          value={content}
          onChange={e => setContent(e.target.value)}
          placeholder="Enter the full system prompt text..."
          style={{
            flex: 1,
            minHeight: 300,
            fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
            fontSize: 12,
            resize: 'vertical',
          }}
        />
      </div>

      {/* Buttons */}
      <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
        <button
          className="btn"
          onClick={handleSave}
          disabled={!canSave}
          style={{ flex: 2, opacity: canSave ? 1 : 0.45 }}
        >
          Save Prompt
        </button>
        <button
          className="btn muted"
          onClick={onCancel}
          style={{ flex: 1 }}
        >
          Cancel
        </button>
      </div>
    </div>
  )
}

// ── Empty state ───────────────────────────────────────────────────────────────

function EmptyState({ onNew }: { onNew: () => void }) {
  return (
    <div
      style={{
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        color: 'var(--muted)',
        gap: 12,
      }}
    >
      <div style={{ fontSize: 32 }}>&#x1F4DD;</div>
      <div style={{ fontSize: 14, fontWeight: 600 }}>Select a prompt or create one</div>
      <button className="btn" onClick={onNew} style={{ width: 160 }}>
        + New Prompt
      </button>
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────────────

export default function PromptsLibrary() {
  const [prompts, setPrompts] = useState<SavedPrompt[]>(() => loadPrompts())
  const [selectedId, setSelectedId] = useState<string | null>(() => {
    const initial = loadPrompts()
    return initial.length > 0 ? initial[0].id : null
  })
  const [isNewMode, setIsNewMode] = useState(false)

  const selectedPrompt = prompts.find(p => p.id === selectedId) ?? null

  const persistAndSet = (updated: SavedPrompt[]) => {
    savePrompts(updated)
    setPrompts(updated)
  }

  const handleSelect = (id: string) => {
    setSelectedId(id)
    setIsNewMode(false)
  }

  const handleNew = () => {
    setIsNewMode(true)
    setSelectedId(null)
  }

  const handleSaveExisting = (updated: SavedPrompt) => {
    const next = prompts.map(p => (p.id === updated.id ? updated : p))
    persistAndSet(next)
  }

  const handleSaveNew = (prompt: SavedPrompt) => {
    const next = [prompt, ...prompts]
    persistAndSet(next)
    setSelectedId(prompt.id)
    setIsNewMode(false)
  }

  const handleDelete = (id: string) => {
    const next = prompts.filter(p => p.id !== id)
    persistAndSet(next)
    setSelectedId(next.length > 0 ? next[0].id : null)
    setIsNewMode(false)
  }

  const handleCancelNew = () => {
    setIsNewMode(false)
    if (prompts.length > 0) {
      setSelectedId(prompts[0].id)
    }
  }

  return (
    <div
      style={{
        display: 'flex',
        height: 'calc(100vh - 160px)',
        minHeight: 480,
        border: '1px solid var(--border)',
        borderRadius: 12,
        overflow: 'hidden',
        background: 'var(--panel)',
      }}
    >
      {/* Left sidebar */}
      <LeftPanel
        prompts={prompts}
        selectedId={selectedId}
        onSelect={handleSelect}
        onNew={handleNew}
        isNewMode={isNewMode}
      />

      {/* Right content */}
      {isNewMode ? (
        <NewPromptPanel onSave={handleSaveNew} onCancel={handleCancelNew} />
      ) : selectedPrompt ? (
        <DetailPanel
          key={selectedPrompt.id}
          prompt={selectedPrompt}
          onSave={handleSaveExisting}
          onDelete={handleDelete}
        />
      ) : (
        <EmptyState onNew={handleNew} />
      )}
    </div>
  )
}
