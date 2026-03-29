package mcp

import "sync"

// Registry holds the live set of MCP tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]ToolEntry
}

// NewRegistry creates an empty, ready-to-use tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]ToolEntry)}
}

// Register adds or replaces a tool by name.
func (r *Registry) Register(t ToolEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name] = t
}

// Unregister removes a tool by name (no-op if not found).
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// List returns a snapshot of all registered tools.
func (r *Registry) List() []ToolEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolEntry, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// Get looks up a single tool by name.
func (r *Registry) Get(name string) (ToolEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}
