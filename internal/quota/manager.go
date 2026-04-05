package quota

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// QuotaWindow defines a flexible time window for quota tracking
// Duration can be in hours (1h, 2h, 6h), days (1d, 7d), etc.
type QuotaWindow struct {
	Duration time.Duration `json:"duration"` // e.g., 1*time.Hour, 24*time.Hour, 7*24*time.Hour
	Name     string        `json:"name"`     // e.g., "1h", "daily", "weekly"
	Limit    float64       `json:"limit"`    // Cost limit for this window
}

// CostQuotaConfig holds flexible cost limits
// Supports multiple time windows: hours, days, weeks, months
type CostQuotaConfig struct {
	// Legacy fields (still supported for backward compatibility)
	DailyCostLimit   float64 `json:"daily_cost_limit,omitempty"`
	MonthlyCostLimit float64 `json:"monthly_cost_limit,omitempty"`

	// New flexible windows
	Windows []QuotaWindow `json:"windows,omitempty"`
}

// WindowUsage tracks usage for a specific quota window
type WindowUsage struct {
	WindowName   string
	WindowStart  time.Time    // When this window started
	CurrentUsed  float64      // How much cost used in this window
	WindowLimit  float64      // Cost limit for this window
	WindowDuration time.Duration // Duration of the window
}

// CostQuotaState tracks the current usage against limits
type CostQuotaState struct {
	TenantID         string
	Config           CostQuotaConfig
	CurrentDayUsed   float64      // Legacy field (for backward compatibility)
	CurrentMonthUsed float64      // Legacy field (for backward compatibility)
	LastReset        time.Time    // Legacy field (for backward compatibility)
	WindowUsages     map[string]*WindowUsage // Flexible windows tracking
}

// CostQuotaManager manages cost quotas for multiple tenants
type CostQuotaManager struct {
	mu     sync.RWMutex
	quotas map[string]CostQuotaState
}

// NewCostQuotaManager creates a new quota manager
func NewCostQuotaManager() *CostQuotaManager {
	return &CostQuotaManager{
		quotas: make(map[string]CostQuotaState),
	}
}

// RegisterQuota registers cost limits for a tenant
func (cqm *CostQuotaManager) RegisterQuota(tenantID string, cfg CostQuotaConfig) {
	cqm.mu.Lock()
	defer cqm.mu.Unlock()

	now := time.Now()
	state := CostQuotaState{
		TenantID:     tenantID,
		Config:       cfg,
		LastReset:    now,
		WindowUsages: make(map[string]*WindowUsage),
	}

	// Initialize window usages for flexible windows
	for _, window := range cfg.Windows {
		state.WindowUsages[window.Name] = &WindowUsage{
			WindowName:     window.Name,
			WindowStart:    now,
			CurrentUsed:    0,
			WindowLimit:    window.Limit,
			WindowDuration: window.Duration,
		}
	}

	cqm.quotas[tenantID] = state
}

// CanAfford checks if tenant can afford a request (estimated cost)
// Returns: (allowed bool, reason string, remainingBudget float64)
func (cqm *CostQuotaManager) CanAfford(tenantID string, estimatedCost float64) (bool, string, float64) {
	cqm.mu.RLock()
	state, ok := cqm.quotas[tenantID]
	cqm.mu.RUnlock()

	if !ok {
		// No quota configured for this tenant
		return true, "no_quota_configured", 0
	}

	now := time.Now()
	minRemaining := math.MaxFloat64
	blockedReason := ""

	// Check flexible windows
	if len(state.Config.Windows) > 0 {
		cqm.mu.Lock()
		defer cqm.mu.Unlock()

		// Refresh state after lock (could have changed)
		state = cqm.quotas[tenantID]

		for windowName, usage := range state.WindowUsages {
			if usage == nil {
				continue
			}

			// Reset window if its duration has elapsed
			if time.Since(usage.WindowStart) > usage.WindowDuration {
				usage.WindowStart = now
				usage.CurrentUsed = 0
			}

			// Check if request exceeds this window's limit
			if usage.CurrentUsed+estimatedCost > usage.WindowLimit {
				remaining := usage.WindowLimit - usage.CurrentUsed
				reason := fmt.Sprintf(
					"quota_exceeded (%s): $%.2f used + $%.2f request > $%.2f limit",
					windowName, usage.CurrentUsed, estimatedCost, usage.WindowLimit,
				)
				if blockedReason == "" {
					blockedReason = reason
				}
				minRemaining = min(minRemaining, remaining)
			} else {
				// Track the minimum remaining budget across all windows
				remaining := usage.WindowLimit - usage.CurrentUsed - estimatedCost
				minRemaining = min(minRemaining, remaining)
			}
		}

		cqm.quotas[tenantID] = state

		// If any window is exceeded, deny the request
		if blockedReason != "" {
			return false, blockedReason, max(0, minRemaining)
		}

		if minRemaining == math.MaxFloat64 {
			minRemaining = 0 // No windows defined, unlimited
		}
		return true, "quota_available", minRemaining
	}

	// Fall back to legacy daily/monthly limits if no windows defined
	cqm.mu.Lock()
	defer cqm.mu.Unlock()

	// Refresh state after lock
	state = cqm.quotas[tenantID]

	// Reset daily if past 24 hours
	if time.Since(state.LastReset) > 24*time.Hour {
		state.CurrentDayUsed = 0
		state.LastReset = now
	}

	// Check daily limit
	if state.Config.DailyCostLimit > 0 {
		if state.CurrentDayUsed+estimatedCost > state.Config.DailyCostLimit {
			remaining := state.Config.DailyCostLimit - state.CurrentDayUsed
			reason := fmt.Sprintf(
				"daily_quota_exceeded: $%.2f used + $%.2f request > $%.2f limit",
				state.CurrentDayUsed, estimatedCost, state.Config.DailyCostLimit,
			)
			cqm.quotas[tenantID] = state
			return false, reason, remaining
		}
	}

	// Check monthly limit
	if state.Config.MonthlyCostLimit > 0 {
		if state.CurrentMonthUsed+estimatedCost > state.Config.MonthlyCostLimit {
			remaining := state.Config.MonthlyCostLimit - state.CurrentMonthUsed
			reason := fmt.Sprintf(
				"monthly_quota_exceeded: $%.2f used + $%.2f request > $%.2f limit",
				state.CurrentMonthUsed, estimatedCost, state.Config.MonthlyCostLimit,
			)
			cqm.quotas[tenantID] = state
			return false, reason, remaining
		}
	}

	// Calculate remaining budget (minimum of daily and monthly)
	var remaining float64
	if state.Config.DailyCostLimit > 0 && state.Config.MonthlyCostLimit > 0 {
		dailyRemaining := state.Config.DailyCostLimit - state.CurrentDayUsed - estimatedCost
		monthlyRemaining := state.Config.MonthlyCostLimit - state.CurrentMonthUsed - estimatedCost
		remaining = min(dailyRemaining, monthlyRemaining)
	} else if state.Config.DailyCostLimit > 0 {
		remaining = state.Config.DailyCostLimit - state.CurrentDayUsed - estimatedCost
	} else if state.Config.MonthlyCostLimit > 0 {
		remaining = state.Config.MonthlyCostLimit - state.CurrentMonthUsed - estimatedCost
	} else {
		remaining = 0 // No limits configured
	}

	cqm.quotas[tenantID] = state
	return true, "quota_available", remaining
}

// RecordCost records actual cost after request completes
func (cqm *CostQuotaManager) RecordCost(tenantID string, actualCost float64) error {
	cqm.mu.Lock()
	defer cqm.mu.Unlock()

	state, ok := cqm.quotas[tenantID]
	if !ok {
		return nil  // No quota to track
	}

	now := time.Now()

	// Update flexible windows
	for _, usage := range state.WindowUsages {
		if usage == nil {
			continue
		}

		// Reset window if its duration has elapsed
		if time.Since(usage.WindowStart) > usage.WindowDuration {
			usage.WindowStart = now
			usage.CurrentUsed = 0
		}

		// Record the cost in this window
		usage.CurrentUsed += actualCost
	}

	// Update legacy fields for backward compatibility
	if time.Since(state.LastReset) > 24*time.Hour {
		state.CurrentDayUsed = 0
		state.LastReset = now
	}
	state.CurrentDayUsed += actualCost
	state.CurrentMonthUsed += actualCost

	cqm.quotas[tenantID] = state
	return nil
}

// WindowStatus represents the status of a single quota window
type WindowStatus struct {
	WindowName   string  `json:"window_name"`
	Limit        float64 `json:"limit"`
	Used         float64 `json:"used"`
	Remaining    float64 `json:"remaining"`
	ResetAt      string  `json:"reset_at"`
	Exceeded     bool    `json:"exceeded"`
}

// QuotaStatus represents the current quota status for a tenant
type QuotaStatus struct {
	TenantID         string          `json:"tenant_id"`
	// Legacy fields (for backward compatibility)
	DailyLimit       float64         `json:"daily_limit"`
	DailyUsed        float64         `json:"daily_used"`
	DailyRemaining   float64         `json:"daily_remaining"`
	MonthlyLimit     float64         `json:"monthly_limit"`
	MonthlyUsed      float64         `json:"monthly_used"`
	MonthlyRemaining float64         `json:"monthly_remaining"`
	DailyResetTime   string          `json:"daily_reset_time"`
	QuotaExceeded    bool            `json:"quota_exceeded"`
	// Flexible windows
	Windows          []WindowStatus  `json:"windows,omitempty"`
}

// GetQuotaStatus returns current quota status for a tenant
func (cqm *CostQuotaManager) GetQuotaStatus(tenantID string) QuotaStatus {
	cqm.mu.RLock()
	state, ok := cqm.quotas[tenantID]
	cqm.mu.RUnlock()

	if !ok {
		return QuotaStatus{TenantID: tenantID}  // No quota
	}

	status := QuotaStatus{
		TenantID: tenantID,
		// Legacy fields
		DailyLimit:       state.Config.DailyCostLimit,
		DailyUsed:        state.CurrentDayUsed,
		DailyRemaining:   max(0, state.Config.DailyCostLimit-state.CurrentDayUsed),
		MonthlyLimit:     state.Config.MonthlyCostLimit,
		MonthlyUsed:      state.CurrentMonthUsed,
		MonthlyRemaining: max(0, state.Config.MonthlyCostLimit-state.CurrentMonthUsed),
		DailyResetTime:   state.LastReset.Add(24 * time.Hour).Format("2006-01-02T15:04:05Z"),
		QuotaExceeded:    state.CurrentDayUsed > state.Config.DailyCostLimit,
	}

	// Add flexible window statuses
	if len(state.WindowUsages) > 0 {
		status.Windows = make([]WindowStatus, 0, len(state.WindowUsages))
		for _, usage := range state.WindowUsages {
			if usage == nil {
				continue
			}

			// Calculate reset time
			resetAt := usage.WindowStart.Add(usage.WindowDuration)

			// Check if window should be reset
			remaining := usage.WindowLimit - usage.CurrentUsed
			if remaining < 0 {
				remaining = 0
			}

			status.Windows = append(status.Windows, WindowStatus{
				WindowName: usage.WindowName,
				Limit:      usage.WindowLimit,
				Used:       usage.CurrentUsed,
				Remaining:  remaining,
				ResetAt:    resetAt.Format("2006-01-02T15:04:05Z"),
				Exceeded:   usage.CurrentUsed > usage.WindowLimit,
			})
		}
	}

	// Mark as exceeded if any window is exceeded
	if status.QuotaExceeded == false {
		for _, window := range status.Windows {
			if window.Exceeded {
				status.QuotaExceeded = true
				break
			}
		}
	}

	return status
}

// ListAllQuotaStatus returns all tenant quota statuses
func (cqm *CostQuotaManager) ListAllQuotaStatus() map[string]QuotaStatus {
	cqm.mu.RLock()
	tenants := make([]string, 0, len(cqm.quotas))
	for t := range cqm.quotas {
		tenants = append(tenants, t)
	}
	cqm.mu.RUnlock()

	result := make(map[string]QuotaStatus)
	for _, t := range tenants {
		result[t] = cqm.GetQuotaStatus(t)
	}
	return result
}

// Helper functions
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
