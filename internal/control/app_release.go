package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/config"
)

// AppRelease version-snapshots the APIs and flows belonging to an app.
type AppRelease struct {
	AppName   string    `json:"app_name"`
	Version   string    `json:"version"`    // semver string e.g. "1.0.0"
	Channel   string    `json:"channel"`    // "staging" | "production"
	APINames  []string  `json:"api_names"`  // names of gateway APIs included in this release
	FlowNames []string  `json:"flow_names"` // names of flows included in this release
	Notes     string    `json:"notes,omitempty"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
}

// AppReleasesHandler dispatches app release CRUD operations.
// Paths:
//
//	GET    /apps/{name}/releases           — list all releases for app
//	POST   /apps/{name}/releases           — create new release
//	POST   /apps/{name}/releases/{ver}/promote   — promote version to active
//	POST   /apps/{name}/releases/{ver}/rollback  — rollback to previous version
func (s *ManagementServer) AppReleasesHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/apps/"), "/")

	if len(parts) < 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	appName := parts[0]
	if appName == "" {
		http.Error(w, "app name required", http.StatusBadRequest)
		return
	}

	// /apps/{name}/releases
	if len(parts) == 2 && parts[1] == "releases" {
		switch r.Method {
		case http.MethodGet:
			s.listAppReleases(w, r, appName)
		case http.MethodPost:
			s.createAppRelease(w, r, appName)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// /apps/{name}/releases/{version}/promote or /rollback
	if len(parts) >= 4 && parts[1] == "releases" {
		version := parts[2]
		action := parts[3]
		switch action {
		case "promote":
			if r.Method == http.MethodPost {
				s.promoteAppRelease(w, r, appName, version)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		case "rollback":
			if r.Method == http.MethodPost {
				s.rollbackAppRelease(w, r, appName, version)
			} else {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		default:
			http.Error(w, "unknown action", http.StatusBadRequest)
		}
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

// listAppReleases returns all releases for an app.
func (s *ManagementServer) listAppReleases(w http.ResponseWriter, r *http.Request, appName string) {
	ctx := r.Context()

	if !s.dataStore.IsConfigured(config.DomainApps) {
		http.Error(w, "app store not configured", http.StatusServiceUnavailable)
		return
	}

	// List all keys matching release:{appname}:*
	prefix := fmt.Sprintf("release:%s:", appName)
	keys, err := s.dataStore.ListGlobalKeys(ctx, config.DomainApps, prefix)
	if err != nil {
		http.Error(w, fmt.Sprintf("list failed: %v", err), http.StatusInternalServerError)
		return
	}

	var releases []AppRelease
	for _, key := range keys {
		// Skip active pointers (release:{appname}:active:{channel})
		if strings.Contains(key, ":active:") {
			continue
		}

		data, ok, err := s.dataStore.GetGlobal(ctx, config.DomainApps, key)
		if err != nil || !ok {
			continue
		}

		var rel AppRelease
		if err := json.Unmarshal(data, &rel); err != nil {
			continue
		}
		releases = append(releases, rel)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"app_name": appName,
		"releases": releases,
	})
}

// createAppRelease creates a new release version.
func (s *ManagementServer) createAppRelease(w http.ResponseWriter, r *http.Request, appName string) {
	ctx := r.Context()

	if !s.dataStore.IsConfigured(config.DomainApps) {
		http.Error(w, "app store not configured", http.StatusServiceUnavailable)
		return
	}

	var rel AppRelease
	if err := json.NewDecoder(r.Body).Decode(&rel); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if rel.Version == "" {
		http.Error(w, "version field required", http.StatusBadRequest)
		return
	}

	if rel.Channel == "" {
		rel.Channel = "production"
	}

	rel.AppName = appName
	rel.CreatedAt = time.Now()
	rel.Active = false // new releases default to inactive

	// Store under key release:{appname}:{version}
	key := fmt.Sprintf("release:%s:%s", appName, rel.Version)
	data, err := json.Marshal(rel)
	if err != nil {
		http.Error(w, fmt.Sprintf("marshal failed: %v", err), http.StatusInternalServerError)
		return
	}

	if err := s.dataStore.PutGlobal(ctx, config.DomainApps, key, data); err != nil {
		http.Error(w, fmt.Sprintf("store failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(rel)
}

// promoteAppRelease sets the given version as active and deactivates the current active version.
func (s *ManagementServer) promoteAppRelease(w http.ResponseWriter, r *http.Request, appName, version string) {
	ctx := r.Context()

	if !s.dataStore.IsConfigured(config.DomainApps) {
		http.Error(w, "app store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Channel string `json:"channel,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Channel == "" {
		req.Channel = "production"
	}

	// Read the release to promote
	releaseKey := fmt.Sprintf("release:%s:%s", appName, version)
	relData, ok, err := s.dataStore.GetGlobal(ctx, config.DomainApps, releaseKey)
	if err != nil || !ok {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}

	var rel AppRelease
	if err := json.Unmarshal(relData, &rel); err != nil {
		http.Error(w, "corrupt release data", http.StatusInternalServerError)
		return
	}

	rel.Active = true
	rel.Channel = req.Channel

	// Read current active version (if any) and deactivate
	activeKey := fmt.Sprintf("release:%s:active:%s", appName, req.Channel)
	if currentActiveData, ok, _ := s.dataStore.GetGlobal(ctx, config.DomainApps, activeKey); ok {
		var activeRel AppRelease
		if err := json.Unmarshal(currentActiveData, &activeRel); err == nil {
			activeRel.Active = false
			if oldData, err := json.Marshal(activeRel); err == nil {
				oldKey := fmt.Sprintf("release:%s:%s", appName, activeRel.Version)
				_ = s.dataStore.PutGlobal(ctx, config.DomainApps, oldKey, oldData)
			}
		}
	}

	// Store promoted version as active
	if newData, err := json.Marshal(rel); err == nil {
		_ = s.dataStore.PutGlobal(ctx, config.DomainApps, releaseKey, newData)
	}

	// Store pointer to active version
	if ptrData, err := json.Marshal(rel); err == nil {
		_ = s.dataStore.PutGlobal(ctx, config.DomainApps, activeKey, ptrData)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rel)
}

// rollbackAppRelease promotes the previous active version.
func (s *ManagementServer) rollbackAppRelease(w http.ResponseWriter, r *http.Request, appName, version string) {
	ctx := r.Context()

	if !s.dataStore.IsConfigured(config.DomainApps) {
		http.Error(w, "app store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Channel string `json:"channel,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Channel == "" {
		req.Channel = "production"
	}

	releaseKey := fmt.Sprintf("release:%s:%s", appName, version)
	relData, ok, err := s.dataStore.GetGlobal(ctx, config.DomainApps, releaseKey)
	if err != nil || !ok {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}

	var rel AppRelease
	if err := json.Unmarshal(relData, &rel); err != nil {
		http.Error(w, "corrupt release data", http.StatusInternalServerError)
		return
	}

	rel.Active = true
	rel.Channel = req.Channel

	// Read current active version (if any) and deactivate
	activeKey := fmt.Sprintf("release:%s:active:%s", appName, req.Channel)
	if currentActiveData, ok, _ := s.dataStore.GetGlobal(ctx, config.DomainApps, activeKey); ok {
		var activeRel AppRelease
		if err := json.Unmarshal(currentActiveData, &activeRel); err == nil {
			activeRel.Active = false
			if oldData, err := json.Marshal(activeRel); err == nil {
				oldKey := fmt.Sprintf("release:%s:%s", appName, activeRel.Version)
				_ = s.dataStore.PutGlobal(ctx, config.DomainApps, oldKey, oldData)
			}
		}
	}

	// Store rollback version as active
	if newData, err := json.Marshal(rel); err == nil {
		_ = s.dataStore.PutGlobal(ctx, config.DomainApps, releaseKey, newData)
	}

	// Store pointer to active version
	if ptrData, err := json.Marshal(rel); err == nil {
		_ = s.dataStore.PutGlobal(ctx, config.DomainApps, activeKey, ptrData)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rel)
}
