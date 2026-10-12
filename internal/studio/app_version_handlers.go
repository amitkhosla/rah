package studio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/appversion"
)

// --- Version handlers ---

// handleListVersions handles GET /api/apps/{appName}/versions
// Returns all versions of the logical app family.
func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	logicalApp := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	logicalApp = strings.TrimSuffix(logicalApp, "/versions")
	if logicalApp == "" {
		http.Error(w, "app name required", http.StatusBadRequest)
		return
	}
	versions, err := s.AppVersions.ListVersions(logicalApp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, versions)
}

// handleCreateVersion handles POST /api/apps/{appName}/versions
// Body: {"source_version": "v1", "target_version": "v2", "changelog": "..."}
func (s *Server) handleCreateVersion(w http.ResponseWriter, r *http.Request) {
	logicalApp := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	logicalApp = strings.TrimSuffix(logicalApp, "/versions")
	if logicalApp == "" {
		http.Error(w, "app name required", http.StatusBadRequest)
		return
	}

	var req struct {
		SourceVersion string `json:"source_version"`
		TargetVersion string `json:"target_version"`
		Changelog     string `json:"changelog"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.SourceVersion == "" || req.TargetVersion == "" {
		http.Error(w, "source_version and target_version are required", http.StatusBadRequest)
		return
	}

	sourceApp := logicalApp + "-" + req.SourceVersion
	targetApp := logicalApp + "-" + req.TargetVersion

	// Check target doesn't already exist
	if existing, _ := s.AppVersions.GetVersion(targetApp); existing != nil {
		http.Error(w, fmt.Sprintf("version %s already exists for %s", req.TargetVersion, logicalApp), http.StatusConflict)
		return
	}

	// Clone the draft
	if err := s.CloneAppVersion(r.Context(), sourceApp, targetApp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Register the new version as draft
	newVersion := appversion.AppVersion{
		AppName:    targetApp,
		LogicalApp: logicalApp,
		Version:    req.TargetVersion,
		Status:     "draft",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := s.AppVersions.UpsertVersion(newVersion); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, newVersion)
}

// handleUpdateVersionStatus handles PUT /api/apps/{appName}/versions/{ver}/status
// Body: {"status": "active" | "retired"}
func (s *Server) handleUpdateVersionStatus(w http.ResponseWriter, r *http.Request) {
	// Path: /api/apps/{logicalApp}/versions/{ver}/status
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	logicalApp := parts[0]
	ver := parts[2]
	appName := logicalApp + "-" + ver

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch req.Status {
	case "active":
		v, err := s.AppVersions.GetVersion(appName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		v.Status = "active"
		v.UpdatedAt = time.Now()
		if err := s.AppVersions.UpsertVersion(*v); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "retired":
		if err := s.AppVersions.RetireVersion(appName); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	default:
		http.Error(w, "status must be 'active' or 'retired'", http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}

// --- Routing handlers ---

// handleListRouting handles GET /api/apps/{appName}/routing
func (s *Server) handleListRouting(w http.ResponseWriter, r *http.Request) {
	logicalApp := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	logicalApp = strings.TrimSuffix(logicalApp, "/routing")
	rules, err := s.AppVersions.ListRoutingRules(logicalApp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, rules)
}

// handleUpsertRouting handles PUT /api/apps/{appName}/routing
// Body: {"rules": [{"tenant_id": 5, "active_app": "orders-v2", "environment": "production"}]}
func (s *Server) handleUpsertRouting(w http.ResponseWriter, r *http.Request) {
	logicalApp := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	logicalApp = strings.TrimSuffix(logicalApp, "/routing")

	var req struct {
		Rules []appversion.RoutingRule `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	for i, rule := range req.Rules {
		if rule.ActiveApp == "" {
			http.Error(w, fmt.Sprintf("rule %d: active_app is required", i), http.StatusBadRequest)
			return
		}
		if rule.Environment == "" {
			req.Rules[i].Environment = "production"
		}
		req.Rules[i].LogicalApp = logicalApp
		req.Rules[i].UpdatedAt = time.Now()
	}

	for _, rule := range req.Rules {
		if err := s.AppVersions.UpsertRoutingRule(rule); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]interface{}{"status": "ok", "applied": len(req.Rules)})
}

// handleDeleteRouting handles DELETE /api/apps/{appName}/routing
// Query params: tenant_id (optional), environment (default: production)
func (s *Server) handleDeleteRouting(w http.ResponseWriter, r *http.Request) {
	logicalApp := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	logicalApp = strings.TrimSuffix(logicalApp, "/routing")
	env := r.URL.Query().Get("environment")
	if env == "" {
		env = "production"
	}
	var tenantID *uint16
	if tidStr := r.URL.Query().Get("tenant_id"); tidStr != "" {
		var tid uint16
		if _, err := fmt.Sscanf(tidStr, "%d", &tid); err != nil {
			http.Error(w, "invalid tenant_id", http.StatusBadRequest)
			return
		}
		tenantID = &tid
	}
	if err := s.AppVersions.DeleteRoutingRule(tenantID, logicalApp, env); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// handlePromoteVersion handles POST /api/apps/{appName}/versions/{ver}/promote
// Sets this version as the default (nil-tenant rule) for the given environment.
func (s *Server) handlePromoteVersion(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/apps/"), "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	logicalApp := parts[0]
	ver := parts[2]
	appName := logicalApp + "-" + ver

	env := r.URL.Query().Get("environment")
	if env == "" {
		env = "production"
	}

	// Verify version exists
	if _, err := s.AppVersions.GetVersion(appName); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	rule := appversion.RoutingRule{
		TenantID:    nil, // default for all tenants
		LogicalApp:  logicalApp,
		ActiveApp:   appName,
		Environment: env,
		UpdatedAt:   time.Now(),
	}
	if err := s.AppVersions.UpsertRoutingRule(rule); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "promoted": appName, "environment": env})
}

// writeJSON is a helper to write JSON responses with proper content-type.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
