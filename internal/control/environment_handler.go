package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/config"
)

// EnvironmentRecord describes one deployment environment.
type EnvironmentRecord struct {
	ID            string `json:"id"`                        // e.g. "prod", "dev"
	Label         string `json:"label"`                     // display name
	GatewayLBURL  string `json:"gateway_lb_url,omitempty"`  // optional REST acceleration URL
	PollIntervalS int    `json:"poll_interval_s,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}

// InstanceHealth is an InstanceRecord annotated with an alive/dead status.
type InstanceHealth struct {
	InstanceRecord
	Alive bool `json:"alive"`
}

// EnvironmentDetail combines an EnvironmentRecord with its live instance list.
type EnvironmentDetail struct {
	EnvironmentRecord
	Instances []InstanceHealth `json:"instances"`
}

const defaultHeartbeatThresholdS = 30

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// RegisterEnvironmentRoutes registers environment CRUD and instance-health routes.
func RegisterEnvironmentRoutes(mux *http.ServeMux, dsm *DataStoreManager) {
	h := &environmentHandler{dsm: dsm}
	mux.HandleFunc("/environments", h.handleCollection)
	mux.HandleFunc("/environments/", h.handleItem)
}

type environmentHandler struct {
	dsm *DataStoreManager
}

func (h *environmentHandler) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.upsert(w, r)
	case http.MethodGet:
		h.list(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *environmentHandler) handleItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/environments/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "environment id required")
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

func (h *environmentHandler) upsert(w http.ResponseWriter, r *http.Request) {
	if !h.dsm.IsConfigured(config.DomainEnvironments) {
		writeError(w, http.StatusServiceUnavailable, "environments domain not configured")
		return
	}
	var rec EnvironmentRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if rec.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	data, err := json.Marshal(rec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.dsm.PutGlobal(context.Background(), config.DomainEnvironments, rec.ID, data); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *environmentHandler) list(w http.ResponseWriter, r *http.Request) {
	if !h.dsm.IsConfigured(config.DomainEnvironments) {
		writeJSON(w, http.StatusOK, []EnvironmentRecord{})
		return
	}
	keys, err := h.dsm.ListGlobalKeys(r.Context(), config.DomainEnvironments, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]EnvironmentRecord, 0, len(keys))
	for _, k := range keys {
		data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainEnvironments, k)
		if err != nil || !ok {
			continue
		}
		var rec EnvironmentRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *environmentHandler) getOne(w http.ResponseWriter, r *http.Request, id string) {
	if !h.dsm.IsConfigured(config.DomainEnvironments) {
		writeError(w, http.StatusNotFound, "environment not found")
		return
	}
	data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainEnvironments, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "environment not found")
		return
	}
	var rec EnvironmentRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		writeError(w, http.StatusInternalServerError, "corrupt record: "+err.Error())
		return
	}

	detail := EnvironmentDetail{EnvironmentRecord: rec, Instances: []InstanceHealth{}}

	// Populate live instance list from DomainGatewayInstances.
	if h.dsm.IsConfigured(config.DomainGatewayInstances) {
		instanceKeys, err := h.dsm.ListGlobalKeys(r.Context(), config.DomainGatewayInstances, "")
		if err == nil {
			now := time.Now().Unix()
			for _, ik := range instanceKeys {
				idata, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainGatewayInstances, ik)
				if err != nil || !ok {
					continue
				}
				var inst InstanceRecord
				if err := json.Unmarshal(idata, &inst); err != nil {
					continue
				}
				if inst.EnvironmentID != id {
					continue
				}
				alive := (now-inst.LastHeartbeat) <= defaultHeartbeatThresholdS
				detail.Instances = append(detail.Instances, InstanceHealth{
					InstanceRecord: inst,
					Alive:          alive,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, detail)
}

func (h *environmentHandler) delete(w http.ResponseWriter, r *http.Request, id string) {
	if !h.dsm.IsConfigured(config.DomainEnvironments) {
		writeError(w, http.StatusNotFound, "environment not found")
		return
	}
	if err := h.dsm.DeleteGlobal(r.Context(), config.DomainEnvironments, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
