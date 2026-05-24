package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"rah/internal/config"
	"rah/internal/datastore"
	"rah/internal/secrets"
)

// credentialStoreAdapter wraps DataStoreManager scoped to DomainCredentials,
// implementing secrets.CredentialStore.
type credentialStoreAdapter struct {
	dsm *DataStoreManager
}

func (a *credentialStoreAdapter) Put(ctx context.Context, tenant, name string, value []byte) error {
	return a.dsm.Put(ctx, config.DomainCredentials, datastore.Tenant(tenant), name, value)
}

func (a *credentialStoreAdapter) Get(ctx context.Context, tenant, name string) ([]byte, bool, error) {
	return a.dsm.Get(ctx, config.DomainCredentials, datastore.Tenant(tenant), name)
}

func (a *credentialStoreAdapter) Delete(ctx context.Context, tenant, name string) error {
	return a.dsm.Delete(ctx, config.DomainCredentials, datastore.Tenant(tenant), name)
}

func (a *credentialStoreAdapter) List(ctx context.Context, tenant string) ([]string, error) {
	return a.dsm.ListKeysByDomain(ctx, config.DomainCredentials, datastore.Tenant(tenant), "")
}

// NewCredentialStore wraps dsm for use as a secrets.CredentialStore.
// Returns nil if the credentials domain is not configured — callers should
// check before passing to NewCredentialRegistry.
func NewCredentialStore(dsm *DataStoreManager) secrets.CredentialStore {
	if !dsm.IsConfigured(config.DomainCredentials) {
		return nil
	}
	return &credentialStoreAdapter{dsm: dsm}
}

// CredentialHandler exposes CRUD REST endpoints for the CredentialRegistry.
//
//	POST   /credentials            — create or update a credential
//	GET    /credentials            — list credentials (?tenant=alias)
//	GET    /credentials/{name}     — get a single credential (?tenant=alias)
//	DELETE /credentials/{name}     — delete a credential (?tenant=alias)
//
// All write operations that omit "tenant" operate on the global scope,
// which is the fallback for all tenants.
type CredentialHandler struct {
	reg *secrets.CredentialRegistry
}

// NewCredentialHandler creates a CredentialHandler backed by reg.
func NewCredentialHandler(reg *secrets.CredentialRegistry) *CredentialHandler {
	return &CredentialHandler{reg: reg}
}

// RegisterHandlers mounts the credential endpoints on mux.
func (h *CredentialHandler) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/credentials", h.collectionHandler)
	mux.HandleFunc("/credentials/", h.itemHandler)
}

func (h *CredentialHandler) collectionHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.upsertCredential(w, r)
	case http.MethodGet:
		h.listCredentials(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *CredentialHandler) itemHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/credentials/")
	if name == "" {
		http.Error(w, "credential name required in path", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getCredential(w, r, name)
	case http.MethodDelete:
		h.deleteCredential(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type upsertCredentialRequest struct {
	Name        string `json:"name"`
	Ref         string `json:"ref"`
	Tenant      string `json:"tenant,omitempty"`
	Description string `json:"description,omitempty"`
}

func (h *CredentialHandler) upsertCredential(w http.ResponseWriter, r *http.Request) {
	var req upsertCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, `"name" is required`, http.StatusBadRequest)
		return
	}
	if req.Ref == "" {
		http.Error(w, `"ref" is required`, http.StatusBadRequest)
		return
	}
	entry := secrets.CredentialEntry{Ref: req.Ref, Description: req.Description}
	if err := h.reg.Set(r.Context(), req.Name, req.Tenant, entry); err != nil {
		http.Error(w, "store failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": req.Name})
}

func (h *CredentialHandler) listCredentials(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	names, err := h.reg.List(r.Context(), tenant)
	if err != nil {
		http.Error(w, "list failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if names == nil {
		names = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"credentials": names, "tenant": tenant})
}

func (h *CredentialHandler) getCredential(w http.ResponseWriter, r *http.Request, name string) {
	tenant := r.URL.Query().Get("tenant")
	entry, ok, err := h.reg.GetEntry(r.Context(), name, tenant)
	if err != nil {
		http.Error(w, "lookup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entry)
}

func (h *CredentialHandler) deleteCredential(w http.ResponseWriter, r *http.Request, name string) {
	tenant := r.URL.Query().Get("tenant")
	if err := h.reg.Delete(r.Context(), name, tenant); err != nil {
		http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
