package mcpreg

import (
	"fmt"
	"sync"
)

// Registry stores VirtualMCPServerDef entries indexed by name.
// Global entries (TenantID=0) are stored under their plain name.
// Tenant-scoped entries are stored under "tenantID:name".
type Registry struct {
	mu      sync.RWMutex
	servers map[string]VirtualMCPServerDef
	// Separately track all registered APIToolDefs (global catalog)
	apiTools map[string]APIToolDef
}

func NewRegistry() *Registry {
	return &Registry{
		servers:  make(map[string]VirtualMCPServerDef),
		apiTools: make(map[string]APIToolDef),
	}
}

func serverKey(tenantID uint16, name string) string {
	if tenantID == 0 {
		return name
	}
	return fmt.Sprintf("%d:%s", tenantID, name)
}

// UpsertServer adds or replaces a VirtualMCPServerDef.
func (r *Registry) UpsertServer(def VirtualMCPServerDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[serverKey(def.TenantID, def.Name)] = def
}

// DeleteServer removes a virtual MCP server. Returns false if not found.
func (r *Registry) DeleteServer(tenantID uint16, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := serverKey(tenantID, name)
	if _, ok := r.servers[key]; !ok {
		return false
	}
	delete(r.servers, key)
	return true
}

// GetServer looks up a virtual MCP server. Tenant-scoped takes priority over global.
func (r *Registry) GetServer(tenantID uint16, name string) (VirtualMCPServerDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Try tenant-scoped first
	if tenantID != 0 {
		if def, ok := r.servers[serverKey(tenantID, name)]; ok {
			return def, true
		}
	}
	// Fall back to global
	def, ok := r.servers[name]
	return def, ok
}

// ListServers returns all definitions, optionally filtered by tenantID (0 = all).
func (r *Registry) ListServers(tenantID uint16) []VirtualMCPServerDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []VirtualMCPServerDef
	for _, def := range r.servers {
		if tenantID == 0 || def.TenantID == 0 || def.TenantID == tenantID {
			out = append(out, def)
		}
	}
	return out
}

// UpsertAPITool adds or replaces an APIToolDef in the global catalog.
func (r *Registry) UpsertAPITool(t APIToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apiTools[t.Name] = t
}

// DeleteAPITool removes an APIToolDef. Returns false if not found.
func (r *Registry) DeleteAPITool(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.apiTools[name]; !ok {
		return false
	}
	delete(r.apiTools, name)
	return true
}

// GetAPITool looks up an APIToolDef by name.
func (r *Registry) GetAPITool(name string) (APIToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.apiTools[name]
	return t, ok
}

// ListAPITools returns all API tool definitions.
func (r *Registry) ListAPITools() []APIToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]APIToolDef, 0, len(r.apiTools))
	for _, t := range r.apiTools {
		out = append(out, t)
	}
	return out
}
