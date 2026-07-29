package studio

import (
	"encoding/json"
	"net/http"
	"strings"
)

// environmentVersionsHandler handles all version-related endpoints under /api/environments/
// URL patterns:
//   GET  /api/environments/{env}/versions           → list versions
//   GET  /api/environments/{env}/versions/{id}      → get single version
//   PATCH /api/environments/{env}/versions/{id}     → void version
//   POST /api/environments/{env}/versions/{id}/rollback → rollback (redeploy)
func (s *Server) environmentVersionsHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/environments/")
	parts := strings.Split(path, "/")

	if len(parts) < 1 || strings.TrimSpace(parts[0]) == "" {
		http.Error(w, "environment id required", http.StatusBadRequest)
		return
	}

	env := parts[0]

	// Must have at least 2 parts: env and "versions"
	if len(parts) < 2 || parts[1] != "versions" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// List versions: GET /api/environments/{env}/versions
	if len(parts) == 2 {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.listVersions(w, r, env)
		return
	}

	// Must have at least 3 parts for specific version operations
	if len(parts) < 3 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	versionID := parts[2]

	// Get or void version: GET/PATCH /api/environments/{env}/versions/{id}
	if len(parts) == 3 {
		switch r.Method {
		case http.MethodGet:
			s.getVersion(w, r, env, versionID)
		case http.MethodPatch:
			s.voidVersion(w, r, env, versionID)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Rollback: POST /api/environments/{env}/versions/{id}/rollback
	if len(parts) == 4 && parts[3] == "rollback" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.rollbackVersion(w, r, env, versionID)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

// listVersions retrieves all versions for an environment.
func (s *Server) listVersions(w http.ResponseWriter, r *http.Request, env string) {
	versions, err := s.versionStore.ListVersions(env)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Return empty array if nil
	if versions == nil {
		versions = make([]*VersionRecord, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(versions)
}

// getVersion retrieves a single version by ID.
func (s *Server) getVersion(w http.ResponseWriter, r *http.Request, env, versionID string) {
	rec, err := s.versionStore.GetVersion(env, versionID)
	if err != nil {
		http.Error(w, "version not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec)
}

// voidVersion marks a version as voided.
func (s *Server) voidVersion(w http.ResponseWriter, r *http.Request, env, versionID string) {
	var body struct {
		Reason string `json:"reason"`
		By     string `json:"by"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if err := s.versionStore.VoidVersion(env, versionID, body.By, body.Reason); err != nil {
		http.Error(w, "version not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "voided"})
}

// rollbackVersion redeploys a previous version to an environment.
func (s *Server) rollbackVersion(w http.ResponseWriter, r *http.Request, env, versionID string) {
	// 1. Get the version record
	rec, err := s.versionStore.GetVersion(env, versionID)
	if err != nil {
		http.Error(w, "version not found", http.StatusNotFound)
		return
	}

	// 2. Load the release from the store using PayloadRef (release ID)
	release, err := s.store.Get(r.Context(), rec.PayloadRef)
	if err != nil {
		http.Error(w, "release payload not found", http.StatusNotFound)
		return
	}

	// 3. Re-deploy: select all targets matching env and POST payload to each
	targets := s.selectTargets(nil, nil)
	if len(targets) == 0 {
		http.Error(w, "no targets available", http.StatusInternalServerError)
		return
	}

	results := make([]map[string]interface{}, 0, len(targets))
	for _, target := range targets {
		for _, url := range target.URLs {
			syncURL, err := buildTargetURL(url, "/sync", "")
			if err != nil {
				results = append(results, map[string]interface{}{
					"target": target.Name,
					"url":    url,
					"error":  err.Error(),
				})
				continue
			}

			_, status, err := s.gatewayCall(r.Context(), "POST", syncURL, release.Payload)
			if err != nil {
				results = append(results, map[string]interface{}{
					"target": target.Name,
					"url":    url,
					"status": status,
					"error":  err.Error(),
				})
			} else {
				results = append(results, map[string]interface{}{
					"target": target.Name,
					"url":    url,
					"status": status,
					"error":  nil,
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"rolled_back_to": versionID,
		"results":        results,
	})
}
