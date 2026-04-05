package main

import (
	"encoding/json"
	"net/http"
	"rah/internal/quota"
	"strings"
	"time"
)

// CostBreakdown represents cost information for a tenant
type CostBreakdown struct {
	TenantID       string                    `json:"tenant_id"`
	DailyUsed      float64                   `json:"daily_used"`
	DailyLimit     float64                   `json:"daily_limit"`
	DailyRemaining float64                   `json:"daily_remaining"`
	MonthlyUsed    float64                   `json:"monthly_used"`
	MonthlyLimit   float64                   `json:"monthly_limit"`
	MonthlyRemaining float64                 `json:"monthly_remaining"`
	Windows        []quota.WindowStatus      `json:"windows,omitempty"`
	QuotaExceeded  bool                      `json:"quota_exceeded"`
}

// CostSummary represents aggregated cost information
type CostSummary struct {
	TotalTenants     int            `json:"total_tenants"`
	TotalDailyUsed   float64        `json:"total_daily_used"`
	TotalMonthlyUsed float64        `json:"total_monthly_used"`
	Tenants          []CostBreakdown `json:"tenants"`
	Timestamp        string         `json:"timestamp"`
}

// RegisterCostRoutes registers cost tracking and quota status endpoints
func RegisterCostRoutes(mux *http.ServeMux, quotaManager *quota.CostQuotaManager, adminToken string) {
	// GET /api/v1/costs - Get cost status for all tenants (admin only)
	mux.HandleFunc("/api/v1/costs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check admin token
		if !verifyAdminToken(r, adminToken) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Get all quota statuses
		allStatuses := quotaManager.ListAllQuotaStatus()

		// Build response
		breakdown := make([]CostBreakdown, 0, len(allStatuses))
		for tenantID, status := range allStatuses {
			breakdown = append(breakdown, CostBreakdown{
				TenantID:         tenantID,
				DailyUsed:        status.DailyUsed,
				DailyLimit:       status.DailyLimit,
				DailyRemaining:   status.DailyRemaining,
				MonthlyUsed:      status.MonthlyUsed,
				MonthlyLimit:     status.MonthlyLimit,
				MonthlyRemaining: status.MonthlyRemaining,
				Windows:          status.Windows,
				QuotaExceeded:    status.QuotaExceeded,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tenants": breakdown,
		})
	})

	// GET /api/v1/costs/summary - Get aggregated cost summary (admin only)
	mux.HandleFunc("/api/v1/costs/summary", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check admin token
		if !verifyAdminToken(r, adminToken) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Get all quota statuses
		allStatuses := quotaManager.ListAllQuotaStatus()

		// Build summary
		totalDaily := 0.0
		totalMonthly := 0.0
		breakdown := make([]CostBreakdown, 0, len(allStatuses))

		for tenantID, status := range allStatuses {
			totalDaily += status.DailyUsed
			totalMonthly += status.MonthlyUsed
			breakdown = append(breakdown, CostBreakdown{
				TenantID:         tenantID,
				DailyUsed:        status.DailyUsed,
				DailyLimit:       status.DailyLimit,
				DailyRemaining:   status.DailyRemaining,
				MonthlyUsed:      status.MonthlyUsed,
				MonthlyLimit:     status.MonthlyLimit,
				MonthlyRemaining: status.MonthlyRemaining,
				Windows:          status.Windows,
				QuotaExceeded:    status.QuotaExceeded,
			})
		}

		summary := CostSummary{
			TotalTenants:     len(allStatuses),
			TotalDailyUsed:   totalDaily,
			TotalMonthlyUsed: totalMonthly,
			Tenants:          breakdown,
			Timestamp:        getCurrentTimestamp(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(summary)
	})

	// GET /api/v1/costs/{tenantId} - Get cost status for specific tenant (admin only)
	mux.HandleFunc("/api/v1/costs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check admin token
		if !verifyAdminToken(r, adminToken) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Extract tenant ID from path: /api/v1/costs/{tenantId}
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 5 {
			http.Error(w, "Invalid path", http.StatusBadRequest)
			return
		}

		tenantID := parts[4]
		if tenantID == "" {
			http.Error(w, "Tenant ID required", http.StatusBadRequest)
			return
		}

		// Get status for this tenant
		status := quotaManager.GetQuotaStatus(tenantID)

		breakdown := CostBreakdown{
			TenantID:         status.TenantID,
			DailyUsed:        status.DailyUsed,
			DailyLimit:       status.DailyLimit,
			DailyRemaining:   status.DailyRemaining,
			MonthlyUsed:      status.MonthlyUsed,
			MonthlyLimit:     status.MonthlyLimit,
			MonthlyRemaining: status.MonthlyRemaining,
			Windows:          status.Windows,
			QuotaExceeded:    status.QuotaExceeded,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(breakdown)
	})
}

// verifyAdminToken checks if the request has a valid admin token
func verifyAdminToken(r *http.Request, expectedToken string) bool {
	if expectedToken == "" {
		// No admin token configured, deny all admin requests
		return false
	}

	// Check X-Admin-Token header
	token := r.Header.Get("X-Admin-Token")
	if token == "" {
		// Check Authorization: Bearer token
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	return token == expectedToken
}

// getCurrentTimestamp returns the current time in RFC3339 format
func getCurrentTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
