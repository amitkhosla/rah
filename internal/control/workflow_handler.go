package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/datastore"
)

const workflowNamePattern = `^[a-zA-Z0-9_-]+$`
const workflowIndexKey = "__index__"
const workflowGlobalTenant = "__workflows__"

var workflowNameRegex = regexp.MustCompile(workflowNamePattern)

// workflowStore is the minimal datastore interface required by WorkflowHandler.
type workflowStore interface {
	IsConfigured(domain config.DataDomain) bool
	Get(ctx context.Context, domain config.DataDomain, tenant datastore.Tenant, key string) ([]byte, bool, error)
	Put(ctx context.Context, domain config.DataDomain, tenant datastore.Tenant, key string, value []byte) error
	Delete(ctx context.Context, domain config.DataDomain, tenant datastore.Tenant, key string) error
}

// WorkflowHandler exposes CRUD REST endpoints for workflow definitions.
//
//	GET    /workflows            — list all workflow names
//	GET    /workflows/{name}     — get a single workflow (JSON)
//	POST   /workflows            — create/upsert workflow (validate, cycle-check, store)
//	DELETE /workflows/{name}     — delete workflow
type WorkflowHandler struct {
	dsm workflowStore // nil = storage not configured
}

// NewWorkflowHandler creates a WorkflowHandler. dsm may be nil (returns 503 on all requests).
func NewWorkflowHandler(dsm workflowStore) *WorkflowHandler {
	return &WorkflowHandler{dsm: dsm}
}

// RegisterHandlers mounts the workflow endpoints on mux.
func (h *WorkflowHandler) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/workflows", h.collectionHandler)
	mux.HandleFunc("/workflows/", h.itemHandler)
}

func (h *WorkflowHandler) collectionHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listWorkflows(w, r)
	case http.MethodPost:
		h.upsertWorkflow(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *WorkflowHandler) itemHandler(w http.ResponseWriter, r *http.Request) {
	// Path is /workflows/{name}
	rest := strings.TrimPrefix(r.URL.Path, "/workflows/")
	if rest == "" {
		http.Error(w, "workflow name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getWorkflow(w, r, rest)
	case http.MethodDelete:
		h.deleteWorkflow(w, r, rest)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// listWorkflows handles GET /workflows — returns list of workflow names.
func (h *WorkflowHandler) listWorkflows(w http.ResponseWriter, r *http.Request) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainWorkflows) {
		http.Error(w, "workflow storage not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	indexData, found, err := h.dsm.Get(ctx, config.DomainWorkflows, datastore.Tenant(workflowGlobalTenant), workflowIndexKey)
	if err != nil {
		http.Error(w, "failed to read index: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var names []string
	if found {
		if err := json.Unmarshal(indexData, &names); err != nil {
			http.Error(w, "corrupted index: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"workflows": names})
}

// getWorkflow handles GET /workflows/{name} — returns a single workflow.
func (h *WorkflowHandler) getWorkflow(w http.ResponseWriter, r *http.Request, name string) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainWorkflows) {
		http.Error(w, "workflow storage not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	data, found, err := h.dsm.Get(ctx, config.DomainWorkflows, datastore.Tenant(workflowGlobalTenant), name)
	if err != nil {
		http.Error(w, "failed to read workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}

	var wf config.WorkflowDefinition
	if err := json.Unmarshal(data, &wf); err != nil {
		http.Error(w, "corrupted workflow data: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wf)
}

// upsertWorkflow handles POST /workflows — creates or updates a workflow.
func (h *WorkflowHandler) upsertWorkflow(w http.ResponseWriter, r *http.Request) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainWorkflows) {
		http.Error(w, "workflow storage not configured", http.StatusServiceUnavailable)
		return
	}

	var wf config.WorkflowDefinition
	if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate name
	if wf.Name == "" {
		http.Error(w, "workflow name required", http.StatusBadRequest)
		return
	}
	if !workflowNameRegex.MatchString(wf.Name) {
		http.Error(w, fmt.Sprintf("workflow name must match %s", workflowNamePattern), http.StatusBadRequest)
		return
	}

	// Validate edges reference existing nodes
	nodeIDs := make(map[string]struct{})
	for _, n := range wf.Nodes {
		nodeIDs[n.ID] = struct{}{}
	}
	for _, e := range wf.Edges {
		if e.ListenerName == "" {
			http.Error(w, "listener_name required on all edges", http.StatusBadRequest)
			return
		}
		if _, ok := nodeIDs[e.SourceNodeID]; !ok {
			http.Error(w, fmt.Sprintf("edge references non-existent source node %q", e.SourceNodeID), http.StatusBadRequest)
			return
		}
		if _, ok := nodeIDs[e.TargetNodeID]; !ok {
			http.Error(w, fmt.Sprintf("edge references non-existent target node %q", e.TargetNodeID), http.StatusBadRequest)
			return
		}
	}

	// Check for cycles
	if hasCycle(wf) {
		http.Error(w, `{"error": "workflow contains a cycle"}`, http.StatusUnprocessableEntity)
		return
	}

	// Update timestamps
	now := time.Now().Unix()
	if wf.CreatedAt == 0 {
		wf.CreatedAt = now
	}
	wf.UpdatedAt = now

	// Serialize and store
	data, err := json.Marshal(wf)
	if err != nil {
		http.Error(w, "failed to serialize workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	if err := h.dsm.Put(ctx, config.DomainWorkflows, datastore.Tenant(workflowGlobalTenant), wf.Name, data); err != nil {
		http.Error(w, "failed to store workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Update index
	if err := h.updateIndex(ctx, wf.Name, true); err != nil {
		http.Error(w, "failed to update index: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wf)
}

// deleteWorkflow handles DELETE /workflows/{name} — deletes a workflow.
func (h *WorkflowHandler) deleteWorkflow(w http.ResponseWriter, r *http.Request, name string) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainWorkflows) {
		http.Error(w, "workflow storage not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	if err := h.dsm.Delete(ctx, config.DomainWorkflows, datastore.Tenant(workflowGlobalTenant), name); err != nil {
		http.Error(w, "failed to delete workflow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Update index
	if err := h.updateIndex(ctx, name, false); err != nil {
		http.Error(w, "failed to update index: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// updateIndex adds or removes a workflow name from the index.
func (h *WorkflowHandler) updateIndex(ctx context.Context, name string, add bool) error {
	indexData, _, err := h.dsm.Get(ctx, config.DomainWorkflows, datastore.Tenant(workflowGlobalTenant), workflowIndexKey)
	if err != nil {
		return err
	}

	var names []string
	if len(indexData) > 0 {
		if err := json.Unmarshal(indexData, &names); err != nil {
			return err
		}
	}

	if add {
		// Add if not present
		found := false
		for _, n := range names {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			names = append(names, name)
		}
	} else {
		// Remove if present
		filtered := make([]string, 0, len(names))
		for _, n := range names {
			if n != name {
				filtered = append(filtered, n)
			}
		}
		names = filtered
	}

	updatedData, err := json.Marshal(names)
	if err != nil {
		return err
	}

	return h.dsm.Put(ctx, config.DomainWorkflows, datastore.Tenant(workflowGlobalTenant), workflowIndexKey, updatedData)
}

// hasCycle performs DFS to detect cycles in the workflow DAG.
func hasCycle(wf config.WorkflowDefinition) bool {
	// Build adjacency list
	adj := make(map[string][]string)
	for _, e := range wf.Edges {
		adj[e.SourceNodeID] = append(adj[e.SourceNodeID], e.TargetNodeID)
	}

	// Track visited states: 0 = unvisited, 1 = visiting, 2 = visited
	state := make(map[string]int)

	var dfs func(nodeID string) bool
	dfs = func(nodeID string) bool {
		if state[nodeID] == 1 {
			return true // Back edge found — cycle detected
		}
		if state[nodeID] == 2 {
			return false // Already fully processed
		}

		state[nodeID] = 1
		for _, neighbor := range adj[nodeID] {
			if dfs(neighbor) {
				return true
			}
		}
		state[nodeID] = 2
		return false
	}

	for _, node := range wf.Nodes {
		if state[node.ID] == 0 {
			if dfs(node.ID) {
				return true
			}
		}
	}

	return false
}
