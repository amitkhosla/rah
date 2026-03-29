package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RegisterManagementRoutes wires the virtual MCP tool management REST endpoints
// into the provided mux:
//
//	POST   /ai/mcp/virtual/register  — register (or update) a tool
//	DELETE /ai/mcp/virtual/{name}    — unregister a tool by name
//	GET    /ai/mcp/virtual/tools     — list all registered virtual tools
//
// All responses use Content-Type: application/json.
func RegisterManagementRoutes(mux *http.ServeMux, registry *Registry) {
	mux.HandleFunc("/ai/mcp/virtual/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Strip the prefix to get the sub-path.
		sub := strings.TrimPrefix(r.URL.Path, "/ai/mcp/virtual/")
		sub = strings.TrimSuffix(sub, "/")

		switch {
		case sub == "register" && r.Method == http.MethodPost:
			handleRegisterTool(w, r, registry)

		case sub == "tools" && r.Method == http.MethodGet:
			handleListTools(w, registry)

		case sub != "" && sub != "register" && sub != "tools" && r.Method == http.MethodDelete:
			// sub is the tool name to delete
			handleUnregisterTool(w, sub, registry)

		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		}
	})
}

// handleRegisterTool decodes a ToolEntry from the request body and registers it.
func handleRegisterTool(w http.ResponseWriter, r *http.Request, registry *Registry) {
	var entry ToolEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if entry.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "name is required"})
		return
	}
	registry.Register(entry)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"registered": entry.Name})
}

// handleListTools returns all registered tool entries as JSON.
// Internal routing fields (GatewayURL, Method) are included so operators can
// inspect the full registration.
func handleListTools(w http.ResponseWriter, registry *Registry) {
	type wireEntry struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema,omitempty"`
		GatewayURL  string          `json:"gatewayUrl"`
		Method      string          `json:"method"`
	}
	entries := registry.List()
	out := make([]wireEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, wireEntry{
			Name:        e.Name,
			Description: e.Description,
			InputSchema: e.InputSchema,
			GatewayURL:  e.GatewayURL,
			Method:      e.Method,
		})
	}
	_ = json.NewEncoder(w).Encode(out)
}

// handleUnregisterTool removes a tool by name.
func handleUnregisterTool(w http.ResponseWriter, name string, registry *Registry) {
	if _, ok := registry.Get(name); !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "tool not found: " + name})
		return
	}
	registry.Unregister(name)
	_ = json.NewEncoder(w).Encode(map[string]string{"unregistered": name})
}
