package control

import (
	"encoding/json"
	"net/http"
	"strings"

	"rah/internal/secrets"
)

// TenantCredentialHandler handles per-tenant credential CRUD via tenant-path URLs.
//
//	GET    /tenants/{alias}/credentials            — list credential names (no values)
//	PUT    /tenants/{alias}/credentials/{name}     — set/replace a credential value (encrypted at rest)
//	DELETE /tenants/{alias}/credentials/{name}     — delete a credential
//
// Values are encrypted at rest using the Encryptor; the UI never receives plaintext.
// The alias in the path is used directly as the tenant key in the CredentialStore
// (same convention as the existing CredentialHandler which uses ?tenant=alias).
//
// This handler is designed to be set as registry.TenantServer.ExtraSubHandler so it
// participates in the existing /tenants/ routing without registering a duplicate mux pattern.
type TenantCredentialHandler struct {
	reg *secrets.CredentialRegistry
	enc secrets.Encryptor
}

// NewTenantCredentialHandler creates a TenantCredentialHandler.
func NewTenantCredentialHandler(reg *secrets.CredentialRegistry, enc secrets.Encryptor) *TenantCredentialHandler {
	return &TenantCredentialHandler{reg: reg, enc: enc}
}

// ServeHTTP implements http.Handler. It handles /tenants/{alias}/credentials[/{name}].
// This is called by registry.TenantServer.tenantSubHandler via ExtraSubHandler.
func (h *TenantCredentialHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.dispatch(w, r)
}

// dispatch routes /tenants/{alias}/credentials[/{name}] to the correct handler.
func (h *TenantCredentialHandler) dispatch(w http.ResponseWriter, r *http.Request) {
	// Path shape: /tenants/{alias}/credentials[/{name}]
	// Strip leading /tenants/
	rest := strings.TrimPrefix(r.URL.Path, "/tenants/")
	// rest = "{alias}/credentials[/{name}]"

	// Find the /credentials segment
	credIdx := strings.Index(rest, "/credentials")
	if credIdx < 0 {
		http.NotFound(w, r)
		return
	}

	alias := rest[:credIdx]
	if alias == "" {
		http.Error(w, "tenant alias required in path", http.StatusBadRequest)
		return
	}

	afterCred := rest[credIdx+len("/credentials"):]
	// afterCred is "" or "/{name}"

	if afterCred == "" || afterCred == "/" {
		// Collection endpoint: GET /tenants/{alias}/credentials
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.listCredentials(w, r, alias)
		return
	}

	// Item endpoint: /tenants/{alias}/credentials/{name}
	name := strings.TrimPrefix(afterCred, "/")
	if name == "" {
		http.Error(w, "credential name required in path", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putCredential(w, r, alias, name)
	case http.MethodDelete:
		h.deleteCredential(w, r, alias, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// listCredentials handles GET /tenants/{alias}/credentials.
// Returns credential names only — never plaintext values.
func (h *TenantCredentialHandler) listCredentials(w http.ResponseWriter, r *http.Request, alias string) {
	names, err := h.reg.List(r.Context(), alias)
	if err != nil {
		http.Error(w, "list failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if names == nil {
		names = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"credentials": names})
}

type putCredentialRequest struct {
	Value string `json:"value"`
}

// putCredential handles PUT /tenants/{alias}/credentials/{name}.
// Encrypts the value before storing it.
func (h *TenantCredentialHandler) putCredential(w http.ResponseWriter, r *http.Request, alias, name string) {
	var req putCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Value == "" {
		http.Error(w, `"value" is required`, http.StatusBadRequest)
		return
	}

	encrypted, err := h.enc.Encrypt([]byte(req.Value))
	if err != nil {
		http.Error(w, "encryption failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	entry := secrets.CredentialEntry{Ref: encrypted}
	if err := h.reg.Set(r.Context(), name, alias, entry); err != nil {
		http.Error(w, "store failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "name": name})
}

// deleteCredential handles DELETE /tenants/{alias}/credentials/{name}.
func (h *TenantCredentialHandler) deleteCredential(w http.ResponseWriter, r *http.Request, alias, name string) {
	if err := h.reg.Delete(r.Context(), name, alias); err != nil {
		http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
