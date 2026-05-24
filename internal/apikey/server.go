package apikey

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ─── Request / Response shapes ────────────────────────────────────────────────

type createAppReq struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type updateAppReq struct {
	Name        *string           `json:"name,omitempty"`
	Description *string           `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type generateKeyReq struct {
	Alias          string   `json:"alias"`
	AllowedTenants []uint16 `json:"allowed_tenants,omitempty"`
}

type updateKeyReq struct {
	Alias          *string  `json:"alias,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	AllowedTenants []uint16 `json:"allowed_tenants,omitempty"`
}

// ─── Server ────────────────────────────────────────────────────────────────

// Server exposes Apps and API Keys over HTTP for management-plane operations.
// Store is optional; nil = memory-only mode, no persistence.
type Server struct {
	Store Store
}

// NewServer creates a Server backed by the optional Store.
func NewServer(store Store) *Server {
	return &Server{Store: store}
}

// RegisterHandlers wires all API key management endpoints onto the given mux.
func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/apps", s.appsRoot)
	mux.HandleFunc("/apps/", s.appsSub)
}

// ─── Root handlers ────────────────────────────────────────────────────────────

// appsRoot dispatches POST /apps (create app) and GET /apps (list apps).
func (s *Server) appsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createApp(w, r)
	case http.MethodGet:
		s.listApps(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// appsSub dispatches /apps/{id} and /apps/{id}/... sub-paths.
func (s *Server) appsSub(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path // e.g. /apps/123 or /apps/123/keys or /apps/123/keys/456
	rest := strings.TrimPrefix(path, "/apps/")
	if rest == path {
		// Should not happen if this handler is registered correctly.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Parse app ID (first path segment).
	seg := strings.SplitN(rest, "/", 2)
	appID, ok := parseID(seg[0])
	if !ok {
		http.Error(w, "invalid app ID", http.StatusBadRequest)
		return
	}

	// Dispatch based on remaining path and method.
	if len(seg) == 1 {
		// /apps/{id} only
		switch r.Method {
		case http.MethodGet:
			s.getApp(w, r, appID)
		case http.MethodPut:
			s.updateApp(w, r, appID)
		case http.MethodDelete:
			s.deleteApp(w, r, appID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Has sub-path: /apps/{id}/...
	subPath := "/" + seg[1]

	// Check for /apps/{id}/keys...
	if strings.HasPrefix(subPath, "/keys") {
		keyPart := strings.TrimPrefix(subPath, "/keys")
		if keyPart == "" || keyPart == "/" {
			// /apps/{id}/keys or /apps/{id}/keys/ (no key ID)
			switch r.Method {
			case http.MethodPost:
				s.generateKey(w, r, appID)
			case http.MethodGet:
				s.listKeys(w, r, appID)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		// /apps/{id}/keys/{kid}[/rotate]
		keyPath := strings.TrimPrefix(keyPart, "/")
		segs := strings.SplitN(keyPath, "/", 2)
		keyID, ok := parseID(segs[0])
		if !ok {
			http.Error(w, "invalid key ID", http.StatusBadRequest)
			return
		}

		if len(segs) == 1 {
			// /apps/{id}/keys/{kid}
			switch r.Method {
			case http.MethodPut:
				s.updateKey(w, r, appID, keyID)
			case http.MethodDelete:
				s.revokeKey(w, r, appID, keyID)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		// /apps/{id}/keys/{kid}/...
		if segs[1] == "rotate" && r.Method == http.MethodPost {
			s.rotateKey(w, r, appID, keyID)
			return
		}

		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Unknown sub-path
	http.Error(w, "not found", http.StatusNotFound)
}

// ─── App handlers ────────────────────────────────────────────────────────────

// createApp handles POST /apps
func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	var req createAppReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name must not be empty", http.StatusBadRequest)
		return
	}

	app := App{
		AppID:       NextAppID(),
		Name:        req.Name,
		Description: req.Description,
		Labels:      req.Labels,
		CreatedAt:   s.now(),
		UpdatedAt:   s.now(),
	}

	UpsertApp(app)

	// Persist to store (best-effort).
	if s.Store != nil {
		if raw, err := json.Marshal(app); err == nil {
			_ = s.Store.PutApp(r.Context(), app.AppID, raw)
		}
	}

	jsonOK(w, app)
}

// getApp handles GET /apps/{id}
func (s *Server) getApp(w http.ResponseWriter, r *http.Request, appID uint32) {
	app := GetApp(appID)
	if app == nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}
	jsonOK(w, app)
}

// updateApp handles PUT /apps/{id}
func (s *Server) updateApp(w http.ResponseWriter, r *http.Request, appID uint32) {
	app := GetApp(appID)
	if app == nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}

	var req updateAppReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Apply non-nil fields.
	if req.Name != nil {
		app.Name = *req.Name
	}
	if req.Description != nil {
		app.Description = *req.Description
	}
	if req.Labels != nil {
		app.Labels = req.Labels
	}

	UpsertApp(*app)

	// Persist to store (best-effort).
	if s.Store != nil {
		if raw, err := json.Marshal(app); err == nil {
			_ = s.Store.PutApp(r.Context(), app.AppID, raw)
		}
	}

	jsonOK(w, app)
}

// deleteApp handles DELETE /apps/{id}
// Deletes the app and all keys belonging to it.
func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request, appID uint32) {
	app := GetApp(appID)
	if app == nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}

	// Delete all keys belonging to this app.
	keys := ListKeysByApp(appID)
	for _, rec := range keys {
		DeleteKey(rec.KeyID)
		if s.Store != nil {
			_ = s.Store.DeleteKey(r.Context(), rec.KeyID)
		}
	}

	// Delete the app itself.
	DeleteApp(appID)
	if s.Store != nil {
		_ = s.Store.DeleteApp(r.Context(), appID)
	}

	w.WriteHeader(http.StatusOK)
}

// listApps handles GET /apps
func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	apps := ListApps()
	if apps == nil {
		apps = []App{}
	}
	type listResponse struct {
		Items []App `json:"items"`
		Count int   `json:"count"`
	}
	jsonOK(w, listResponse{Items: apps, Count: len(apps)})
}

// ─── Key handlers ────────────────────────────────────────────────────────────

// generateKey handles POST /apps/{id}/keys
func (s *Server) generateKey(w http.ResponseWriter, r *http.Request, appID uint32) {
	// Verify app exists.
	if GetApp(appID) == nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}

	var req generateKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Alias == "" {
		http.Error(w, "alias must not be empty", http.StatusBadRequest)
		return
	}

	// Generate new key.
	rawKey, rec, err := Generate(appID, req.Alias, req.AllowedTenants)
	if err != nil {
		http.Error(w, "failed to generate key: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Store in memory.
	UpsertKey(rec)

	// Persist to store (best-effort).
	if s.Store != nil {
		if raw, err := json.Marshal(rec); err == nil {
			_ = s.Store.PutKey(r.Context(), rec.KeyID, raw)
		}
	}

	jsonOK(w, CreateResponse{
		APIKeyView: rec.ToView(),
		RawKey:     rawKey,
	})
}

// listKeys handles GET /apps/{id}/keys
func (s *Server) listKeys(w http.ResponseWriter, r *http.Request, appID uint32) {
	// Verify app exists.
	if GetApp(appID) == nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}

	recs := ListKeysByApp(appID)
	views := make([]APIKeyView, len(recs))
	for i, rec := range recs {
		views[i] = rec.ToView()
	}

	type listResponse struct {
		Items []APIKeyView `json:"items"`
		Count int          `json:"count"`
	}
	jsonOK(w, listResponse{Items: views, Count: len(views)})
}

// updateKey handles PUT /apps/{id}/keys/{kid}
func (s *Server) updateKey(w http.ResponseWriter, r *http.Request, appID uint32, keyID uint32) {
	// Verify app exists.
	if GetApp(appID) == nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}

	// Get existing key record.
	rec := GetRecord(keyID)
	if rec == nil {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}

	// Verify key belongs to this app.
	if rec.AppID != appID {
		http.Error(w, "key does not belong to this app", http.StatusForbidden)
		return
	}

	var req updateKeyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Apply non-nil fields.
	if req.Alias != nil {
		rec.Alias = *req.Alias
	}
	if req.Enabled != nil {
		rec.Enabled = *req.Enabled
	}
	if req.AllowedTenants != nil {
		rec.AllowedTenants = req.AllowedTenants
	}

	UpsertKey(*rec)

	// Persist to store (best-effort).
	if s.Store != nil {
		if raw, err := json.Marshal(rec); err == nil {
			_ = s.Store.PutKey(r.Context(), rec.KeyID, raw)
		}
	}

	jsonOK(w, rec.ToView())
}

// revokeKey handles DELETE /apps/{id}/keys/{kid}
func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request, appID uint32, keyID uint32) {
	// Verify app exists.
	if GetApp(appID) == nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}

	// Get key and verify it belongs to this app.
	rec := GetRecord(keyID)
	if rec == nil {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}
	if rec.AppID != appID {
		http.Error(w, "key does not belong to this app", http.StatusForbidden)
		return
	}

	DeleteKey(keyID)

	// Delete from store (best-effort).
	if s.Store != nil {
		_ = s.Store.DeleteKey(r.Context(), keyID)
	}

	w.WriteHeader(http.StatusOK)
}

// rotateKey handles POST /apps/{id}/keys/{kid}/rotate
// Generates a new raw key for the same key record (same KeyID, Alias, AllowedTenants).
func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request, appID uint32, keyID uint32) {
	// Verify app exists.
	if GetApp(appID) == nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}

	// Get existing key record.
	rec := GetRecord(keyID)
	if rec == nil {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}

	// Verify key belongs to this app.
	if rec.AppID != appID {
		http.Error(w, "key does not belong to this app", http.StatusForbidden)
		return
	}

	// Generate a new raw key with same AppID, Alias, AllowedTenants.
	rawKey, newRec, err := Generate(rec.AppID, rec.Alias, rec.AllowedTenants)
	if err != nil {
		http.Error(w, "failed to generate key: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Keep the same KeyID, CreatedAt, but update the hash and UpdatedAt.
	newRec.KeyID = rec.KeyID
	newRec.CreatedAt = rec.CreatedAt

	UpsertKey(newRec)

	// Persist to store (best-effort).
	if s.Store != nil {
		if raw, err := json.Marshal(newRec); err == nil {
			_ = s.Store.PutKey(r.Context(), newRec.KeyID, raw)
		}
	}

	jsonOK(w, CreateResponse{
		APIKeyView: newRec.ToView(),
		RawKey:     rawKey,
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// jsonOK writes a 200 OK response with JSON-encoded body.
func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// parseID parses a path segment as uint32.
func parseID(s string) (uint32, bool) {
	n, err := strconv.ParseUint(s, 10, 32)
	return uint32(n), err == nil
}

// now returns the current Unix timestamp in seconds.
func (s *Server) now() int64 {
	// Using a method on Server for consistency with registry_server pattern.
	// Could also use time.Now().Unix() directly.
	return time.Now().Unix()
}
