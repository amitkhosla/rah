package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/amitkhosla/rah/internal/quota"
)

// RegisterCostRoutes registers HTTP endpoints for cost quota inspection.
// All endpoints require the admin token via X-Admin-Token or Authorization: Bearer header.
//
//	GET  /api/v1/costs              → list all tenant quota statuses
//	GET  /api/v1/costs/{tenantKey}  → quota status for a single tenant
func RegisterCostRoutes(mux *http.ServeMux, qm *quota.CostQuotaManager, adminToken string) {
	mux.HandleFunc("/api/v1/costs", func(w http.ResponseWriter, r *http.Request) {
		if !checkAdminToken(r, adminToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		all := qm.ListAllQuotaStatus()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(all)
	})

	mux.HandleFunc("/api/v1/costs/", func(w http.ResponseWriter, r *http.Request) {
		if !checkAdminToken(r, adminToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		tenantKey := strings.TrimPrefix(r.URL.Path, "/api/v1/costs/")
		if tenantKey == "" {
			http.Error(w, "tenant key required", http.StatusBadRequest)
			return
		}
		status := qm.GetQuotaStatus(tenantKey)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status)
	})
}

func checkAdminToken(r *http.Request, adminToken string) bool {
	if adminToken == "" {
		return false
	}
	if t := r.Header.Get("X-Admin-Token"); t == adminToken {
		return true
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ") == adminToken
	}
	return false
}
