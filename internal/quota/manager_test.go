package quota

import (
	"testing"
	"time"
)

func TestQuotaManager(t *testing.T) {
	manager := NewCostQuotaManager()

	// Register quota for tenant
	cfg := CostQuotaConfig{
		DailyCostLimit:   100.00,
		MonthlyCostLimit: 2000.00,
	}
	manager.RegisterQuota("acme-corp", cfg)

	tests := []struct {
		name          string
		tenantID      string
		estimatedCost float64
		wantAllowed   bool
	}{
		{
			name:          "first request within limits",
			tenantID:      "acme-corp",
			estimatedCost: 10.00,
			wantAllowed:   true,
		},
		{
			name:          "multiple requests within limits",
			tenantID:      "acme-corp",
			estimatedCost: 20.00,
			wantAllowed:   true,
		},
		{
			name:          "request exceeding daily limit",
			tenantID:      "acme-corp",
			estimatedCost: 100.00,  // total would be 130.00 > 100.00 limit
			wantAllowed:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, _, _ := manager.CanAfford(tt.tenantID, tt.estimatedCost)
			if allowed != tt.wantAllowed {
				t.Errorf("CanAfford() allowed = %v, want %v", allowed, tt.wantAllowed)
			}
			// Record the cost if allowed to accumulate usage
			if allowed {
				manager.RecordCost(tt.tenantID, tt.estimatedCost)
			}
		})
	}
}

func TestRecordCost(t *testing.T) {
	manager := NewCostQuotaManager()

	cfg := CostQuotaConfig{
		DailyCostLimit:   100.00,
		MonthlyCostLimit: 2000.00,
	}
	manager.RegisterQuota("acme-corp", cfg)

	// Record a cost
	err := manager.RecordCost("acme-corp", 25.50)
	if err != nil {
		t.Fatalf("RecordCost() failed: %v", err)
	}

	// Check status
	status := manager.GetQuotaStatus("acme-corp")
	if status.DailyUsed != 25.50 {
		t.Errorf("GetQuotaStatus() DailyUsed = %v, want 25.50", status.DailyUsed)
	}
	if status.MonthlyUsed != 25.50 {
		t.Errorf("GetQuotaStatus() MonthlyUsed = %v, want 25.50", status.MonthlyUsed)
	}
}

func TestGetQuotaStatus(t *testing.T) {
	manager := NewCostQuotaManager()

	cfg := CostQuotaConfig{
		DailyCostLimit:   100.00,
		MonthlyCostLimit: 2000.00,
	}
	manager.RegisterQuota("startup-xyz", cfg)

	// Record some costs
	manager.RecordCost("startup-xyz", 30.00)
	manager.RecordCost("startup-xyz", 15.50)

	status := manager.GetQuotaStatus("startup-xyz")

	tests := []struct {
		name         string
		field        float64
		expectedVal  float64
	}{
		{
			name:        "DailyUsed",
			field:       status.DailyUsed,
			expectedVal: 45.50,
		},
		{
			name:        "DailyRemaining",
			field:       status.DailyRemaining,
			expectedVal: 54.50,
		},
		{
			name:        "MonthlyUsed",
			field:       status.MonthlyUsed,
			expectedVal: 45.50,
		},
		{
			name:        "MonthlyRemaining",
			field:       status.MonthlyRemaining,
			expectedVal: 1954.50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.field != tt.expectedVal {
				t.Errorf("%s = %v, want %v", tt.name, tt.field, tt.expectedVal)
			}
		})
	}
}

func TestNoQuotaTenant(t *testing.T) {
	manager := NewCostQuotaManager()

	// Tenant without quota should be allowed
	allowed, reason, _ := manager.CanAfford("no-quota-tenant", 100.00)
	if !allowed {
		t.Errorf("CanAfford() for unregistered tenant should be allowed")
	}
	if reason != "no_quota_configured" {
		t.Errorf("CanAfford() reason = %v, want 'no_quota_configured'", reason)
	}
}

func TestListAllQuotaStatus(t *testing.T) {
	manager := NewCostQuotaManager()

	// Register multiple tenants
	manager.RegisterQuota("tenant1", CostQuotaConfig{DailyCostLimit: 100.00, MonthlyCostLimit: 2000.00})
	manager.RegisterQuota("tenant2", CostQuotaConfig{DailyCostLimit: 50.00, MonthlyCostLimit: 1000.00})

	// Record costs
	manager.RecordCost("tenant1", 10.00)
	manager.RecordCost("tenant2", 5.00)

	statuses := manager.ListAllQuotaStatus()

	if len(statuses) != 2 {
		t.Errorf("ListAllQuotaStatus() returned %d tenants, want 2", len(statuses))
	}

	if statuses["tenant1"].DailyUsed != 10.00 {
		t.Errorf("tenant1 DailyUsed = %v, want 10.00", statuses["tenant1"].DailyUsed)
	}

	if statuses["tenant2"].DailyUsed != 5.00 {
		t.Errorf("tenant2 DailyUsed = %v, want 5.00", statuses["tenant2"].DailyUsed)
	}
}

func TestFlexibleWindows(t *testing.T) {
	manager := NewCostQuotaManager()

	// Register quota with flexible windows (hourly and daily)
	cfg := CostQuotaConfig{
		Windows: []QuotaWindow{
			{
				Duration: 1 * time.Hour,
				Name:     "hourly",
				Limit:    50.00,
			},
			{
				Duration: 24 * time.Hour,
				Name:     "daily",
				Limit:    500.00,
			},
			{
				Duration: 7 * 24 * time.Hour,
				Name:     "weekly",
				Limit:    2500.00,
			},
		},
	}
	manager.RegisterQuota("enterprise-customer", cfg)

	tests := []struct {
		name          string
		tenantID      string
		estimatedCost float64
		wantAllowed   bool
		description   string
	}{
		{
			name:          "first request within all windows",
			tenantID:      "enterprise-customer",
			estimatedCost: 10.00,
			wantAllowed:   true,
			description:   "Should allow first request",
		},
		{
			name:          "second request within all windows",
			tenantID:      "enterprise-customer",
			estimatedCost: 20.00,
			wantAllowed:   true,
			description:   "Should allow second request (30 total, within all limits)",
		},
		{
			name:          "request exceeding hourly limit",
			tenantID:      "enterprise-customer",
			estimatedCost: 25.00,
			wantAllowed:   false,
			description:   "Should deny request exceeding hourly limit (55 > 50)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, reason, _ := manager.CanAfford(tt.tenantID, tt.estimatedCost)
			if allowed != tt.wantAllowed {
				t.Errorf("CanAfford() allowed = %v, want %v. Reason: %s. Desc: %s",
					allowed, tt.wantAllowed, reason, tt.description)
			}
			// Record the cost if allowed to accumulate usage
			if allowed {
				manager.RecordCost(tt.tenantID, tt.estimatedCost)
			}
		})
	}
}

func TestFlexibleWindowsStatus(t *testing.T) {
	manager := NewCostQuotaManager()

	cfg := CostQuotaConfig{
		Windows: []QuotaWindow{
			{
				Duration: 1 * time.Hour,
				Name:     "hourly",
				Limit:    100.00,
			},
			{
				Duration: 24 * time.Hour,
				Name:     "daily",
				Limit:    500.00,
			},
		},
	}
	manager.RegisterQuota("test-tenant", cfg)

	// Record some costs
	manager.RecordCost("test-tenant", 25.00)
	manager.RecordCost("test-tenant", 15.00)

	status := manager.GetQuotaStatus("test-tenant")

	// Check that windows are populated
	if len(status.Windows) != 2 {
		t.Errorf("Expected 2 windows, got %d", len(status.Windows))
	}

	// Check hourly window
	for _, window := range status.Windows {
		if window.WindowName == "hourly" {
			if window.Used != 40.00 {
				t.Errorf("Hourly window used = %v, want 40.00", window.Used)
			}
			if window.Remaining != 60.00 {
				t.Errorf("Hourly window remaining = %v, want 60.00", window.Remaining)
			}
			if window.Exceeded {
				t.Errorf("Hourly window should not be exceeded")
			}
		}
		if window.WindowName == "daily" {
			if window.Used != 40.00 {
				t.Errorf("Daily window used = %v, want 40.00", window.Used)
			}
			if window.Remaining != 460.00 {
				t.Errorf("Daily window remaining = %v, want 460.00", window.Remaining)
			}
		}
	}
}

func TestBackwardCompatibilityWithLegacyConfig(t *testing.T) {
	manager := NewCostQuotaManager()

	// Register quota using legacy daily/monthly fields (no flexible windows)
	cfg := CostQuotaConfig{
		DailyCostLimit:   100.00,
		MonthlyCostLimit: 2000.00,
		// No Windows array
	}
	manager.RegisterQuota("legacy-tenant", cfg)

	// Should still work with legacy logic
	allowed, reason, _ := manager.CanAfford("legacy-tenant", 50.00)
	if !allowed {
		t.Errorf("Legacy config: CanAfford should allow 50.00, reason: %s", reason)
	}

	// Record cost
	manager.RecordCost("legacy-tenant", 50.00)

	// Should deny next request exceeding daily limit
	allowed, reason, _ = manager.CanAfford("legacy-tenant", 60.00)
	if allowed {
		t.Errorf("Legacy config: CanAfford should deny 60.00 when already used 50.00, reason: %s", reason)
	}

	// Check status
	status := manager.GetQuotaStatus("legacy-tenant")
	if status.DailyUsed != 50.00 {
		t.Errorf("Legacy config: DailyUsed = %v, want 50.00", status.DailyUsed)
	}
}

func TestMultipleTenantsWithDifferentConfigs(t *testing.T) {
	manager := NewCostQuotaManager()

	// Tenant 1: Legacy config (daily/monthly)
	manager.RegisterQuota("tenant-legacy", CostQuotaConfig{
		DailyCostLimit:   100.00,
		MonthlyCostLimit: 2000.00,
	})

	// Tenant 2: Flexible windows
	manager.RegisterQuota("tenant-flexible", CostQuotaConfig{
		Windows: []QuotaWindow{
			{Duration: 1 * time.Hour, Name: "hourly", Limit: 50.00},
			{Duration: 24 * time.Hour, Name: "daily", Limit: 500.00},
		},
	})

	// Test both tenants independently
	allowed1, _, _ := manager.CanAfford("tenant-legacy", 50.00)
	if !allowed1 {
		t.Errorf("Legacy tenant should allow 50.00")
	}

	allowed2, _, _ := manager.CanAfford("tenant-flexible", 30.00)
	if !allowed2 {
		t.Errorf("Flexible tenant should allow 30.00")
	}

	// Record costs
	manager.RecordCost("tenant-legacy", 50.00)
	manager.RecordCost("tenant-flexible", 30.00)

	// Check they maintain separate quotas
	status1 := manager.GetQuotaStatus("tenant-legacy")
	status2 := manager.GetQuotaStatus("tenant-flexible")

	if status1.DailyUsed != 50.00 {
		t.Errorf("tenant-legacy DailyUsed = %v, want 50.00", status1.DailyUsed)
	}

	if status2.Windows[0].Used != 30.00 {
		t.Errorf("tenant-flexible hourly window used = %v, want 30.00", status2.Windows[0].Used)
	}
}
