package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/config"
)

// ReleaseItem is one API definition or flow included in a release.
type ReleaseItem struct {
	Type    string `json:"type"`             // "api" | "flow"
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ReleaseRecord describes a named, versioned bundle of APIs and flows.
type ReleaseRecord struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Items       []ReleaseItem `json:"items"`
	CreatedAt   int64         `json:"created_at"`
	Status      string        `json:"status"` // "draft" | "published"
}

// DeployRequest triggers deployment of a release to an environment.
type DeployRequest struct {
	ReleaseID     string          `json:"release_id"`
	EnvironmentID string          `json:"environment_id"`
	Payload       json.RawMessage `json:"payload"` // full UnifiedSyncRequest JSON to store
	Comment       string          `json:"comment,omitempty"`
}

// DeployResponse is returned after a successful deploy.
type DeployResponse struct {
	ConfigVersionID uint64 `json:"config_version_id"`
	ReleaseID       string `json:"release_id"`
	EnvironmentID   string `json:"environment_id"`
	AppliedLocally  bool   `json:"applied_locally"` // true if this instance is in the target env
}

// deployHistoryRecord is an abbreviated record stored in DomainDeployHistory.
type deployHistoryRecord struct {
	ConfigVersionID uint64 `json:"config_version_id"`
	ReleaseID       string `json:"release_id"`
	EnvironmentID   string `json:"environment_id"`
	Comment         string `json:"comment,omitempty"`
	DeployedAt      int64  `json:"deployed_at"`
}

// RegisterReleaseRoutes registers release CRUD and deploy routes.
// sync may be nil (routes remain fully functional, applied_locally will be false).
func RegisterReleaseRoutes(mux *http.ServeMux, dsm *DataStoreManager, sync *InstanceSync) {
	h := &releaseHandler{dsm: dsm, sync: sync}
	mux.HandleFunc("/releases", h.handleCollection)
	mux.HandleFunc("/releases/", h.handleItem)
	mux.HandleFunc("/deploy", h.handleDeploy)
	mux.HandleFunc("/deploy/history/", h.handleDeployHistory)
}

type releaseHandler struct {
	dsm  *DataStoreManager
	sync *InstanceSync
}

// â”€â”€ /releases â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func (h *releaseHandler) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.create(w, r)
	case http.MethodGet:
		h.list(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *releaseHandler) handleItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/releases/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "release id required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getOne(w, r, id)
	case http.MethodDelete:
		h.delete(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *releaseHandler) create(w http.ResponseWriter, r *http.Request) {
	if !h.dsm.IsConfigured(config.DomainReleases) {
		writeError(w, http.StatusServiceUnavailable, "releases domain not configured")
		return
	}
	var rec ReleaseRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if rec.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	now := time.Now()
	rec.ID = fmt.Sprintf("rel-%d", now.UnixNano())
	rec.CreatedAt = now.Unix()
	if rec.Status == "" {
		rec.Status = "draft"
	}
	data, err := json.Marshal(rec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.dsm.PutGlobal(context.Background(), config.DomainReleases, rec.ID, data); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (h *releaseHandler) list(w http.ResponseWriter, r *http.Request) {
	if !h.dsm.IsConfigured(config.DomainReleases) {
		writeJSON(w, http.StatusOK, []ReleaseRecord{})
		return
	}
	keys, err := h.dsm.ListGlobalKeys(r.Context(), config.DomainReleases, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]ReleaseRecord, 0, len(keys))
	for _, k := range keys {
		data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainReleases, k)
		if err != nil || !ok {
			continue
		}
		var rec ReleaseRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *releaseHandler) getOne(w http.ResponseWriter, r *http.Request, id string) {
	if !h.dsm.IsConfigured(config.DomainReleases) {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainReleases, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	var rec ReleaseRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		writeError(w, http.StatusInternalServerError, "corrupt record: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *releaseHandler) delete(w http.ResponseWriter, r *http.Request, id string) {
	if !h.dsm.IsConfigured(config.DomainReleases) {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	if err := h.dsm.DeleteGlobal(r.Context(), config.DomainReleases, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// â”€â”€ /deploy â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func (h *releaseHandler) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.ReleaseID == "" {
		writeError(w, http.StatusBadRequest, "release_id is required")
		return
	}
	if req.EnvironmentID == "" {
		writeError(w, http.StatusBadRequest, "environment_id is required")
		return
	}

	// 1. Validate the release exists.
	if h.dsm.IsConfigured(config.DomainReleases) {
		_, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainReleases, req.ReleaseID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "checking release: "+err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusBadRequest, "release not found: "+req.ReleaseID)
			return
		}
	}

	// 2. Generate next config version ID.
	var nextVersionID uint64 = 1
	if h.dsm.IsConfigured(config.DomainConfigVersions) {
		prefix := req.EnvironmentID + "/"
		keys, err := h.dsm.ListGlobalKeys(r.Context(), config.DomainConfigVersions, prefix)
		if err == nil {
			for _, k := range keys {
				suffix := strings.TrimPrefix(k, prefix)
				if n, err := strconv.ParseUint(suffix, 16, 64); err == nil {
					if n >= nextVersionID {
						nextVersionID = n + 1
					}
				}
			}
		}
	}

	// 3. Store ConfigVersionRecord.
	now := time.Now().Unix()
	versionKey := fmt.Sprintf("%s/%016x", req.EnvironmentID, nextVersionID)
	cvRec := ConfigVersionRecord{
		ID:            nextVersionID,
		EnvironmentID: req.EnvironmentID,
		ReleaseID:     req.ReleaseID,
		Payload:       req.Payload,
		Status:        "published",
		CreatedAt:     now,
		Comment:       req.Comment,
	}
	cvData, err := json.Marshal(cvRec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.dsm.IsConfigured(config.DomainConfigVersions) {
		if err := h.dsm.PutGlobal(r.Context(), config.DomainConfigVersions, versionKey, cvData); err != nil {
			writeError(w, http.StatusInternalServerError, "storing config version: "+err.Error())
			return
		}
	}

	// 4. Store deploy history record.
	if h.dsm.IsConfigured(config.DomainDeployHistory) {
		histKey := fmt.Sprintf("%s/%d", req.EnvironmentID, now)
		histRec := deployHistoryRecord{
			ConfigVersionID: nextVersionID,
			ReleaseID:       req.ReleaseID,
			EnvironmentID:   req.EnvironmentID,
			Comment:         req.Comment,
			DeployedAt:      now,
		}
		histData, err := json.Marshal(histRec)
		if err == nil {
			_ = h.dsm.PutGlobal(r.Context(), config.DomainDeployHistory, histKey, histData)
		}
	}

	// 5. Notify InstanceSync of the new version (if this instance is in the target env).
	appliedLocally := false
	if h.sync != nil && h.sync.cfg.EnvironmentID == req.EnvironmentID {
		h.sync.SetVersion(nextVersionID)
		appliedLocally = true
	}

	writeJSON(w, http.StatusOK, DeployResponse{
		ConfigVersionID: nextVersionID,
		ReleaseID:       req.ReleaseID,
		EnvironmentID:   req.EnvironmentID,
		AppliedLocally:  appliedLocally,
	})
}

// â”€â”€ /deploy/history/{environmentID} â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func (h *releaseHandler) handleDeployHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	envID := strings.TrimPrefix(r.URL.Path, "/deploy/history/")
	if envID == "" {
		writeError(w, http.StatusBadRequest, "environment_id required")
		return
	}

	if !h.dsm.IsConfigured(config.DomainDeployHistory) {
		writeJSON(w, http.StatusOK, []deployHistoryRecord{})
		return
	}

	prefix := envID + "/"
	keys, err := h.dsm.ListGlobalKeys(r.Context(), config.DomainDeployHistory, prefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Sort descending (most recent first) then take up to 50.
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	if len(keys) > 50 {
		keys = keys[:50]
	}

	out := make([]deployHistoryRecord, 0, len(keys))
	for _, k := range keys {
		data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainDeployHistory, k)
		if err != nil || !ok {
			continue
		}
		var rec deployHistoryRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	writeJSON(w, http.StatusOK, out)
}
