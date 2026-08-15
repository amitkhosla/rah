package control

// rate_limit_integration_test.go — integration tests for per-tenant dynamic
// rate limiting via switch + check_rate_limit_v2.
//
// Tests validate:
//   1. check_rate_limit_v2 inside a switch case stops the flow on denial (DeniedPC=-1).
//   2. Per-tenant plan dispatch: enterprise=1000/min, pro=200/min, free=3/min (low for testing).
//   3. Tenants are isolated — hitting free-plan limit doesn't affect enterprise requests.
//   4. Rate limit configs must be sent via /sync (not direct REST) to allocate counter arenas.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	tenantregistry "github.com/amitkhosla/rah/internal/registry" //nolint:typecheck
)

// setupRLTestStack builds the full gateway stack + tenants + RL configs needed
// for rate limit tests.
func setupRLTestStack(t *testing.T) (*engine.FlowManager, *ManagementServer) {
	t.Helper()
	fm, _, server, regMgr := newTestStack(t)

	// Register three tenants with different plans.
	for _, tc := range []struct {
		alias string
		plan  string
	}{
		{"tenant-enterprise", "enterprise"},
		{"tenant-pro", "pro"},
		{"tenant-free", "free"},
	} {
		regMgr.UpsertTenantState(
			[]string{tc.alias},
			nil,
			nil,
			map[string]string{"plan": tc.plan},
		)
	}

	// Step 1: register RL configs via sync so EnsureRateLimitV2ID + counter arenas
	// are allocated. The direct /rate-limit-configs-v2 REST endpoint does NOT call
	// EnsureRateLimitV2ID so configID stays 0 and the compiler emits a no-op step.
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "rl-configs",
		RateLimitConfigsV2: []tenantregistry.RateLimitConfigV2{
			{
				Name:        "rl-enterprise",
				Enforcement: "approximate",
				Windows:     []tenantregistry.RateLimitWindow{{Period: "1m", PeriodSecs: 60, Limit: 1000}},
			},
			{
				Name:        "rl-pro",
				Enforcement: "approximate",
				Windows:     []tenantregistry.RateLimitWindow{{Period: "1m", PeriodSecs: 60, Limit: 200}},
			},
			{
				// Use limit=3 so the test can trigger it with 4 requests.
				Name:        "rl-free",
				Enforcement: "approximate",
				Windows:     []tenantregistry.RateLimitWindow{{Period: "1m", PeriodSecs: 60, Limit: 3}},
			},
		},
	})

	// Step 2: deploy the flow + API. All flows in one sync bundle so switch-case
	// references resolve from the same fragments map.
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "scenario-rl",
		Flows: []FlowUpdate{
			{Name: "flow-rl-enterprise", Action: "upsert", Instructions: []StepConfig{
				{Action: "check_rate_limit_v2", Input: map[string]string{
					"config":   "rl-enterprise",
					"count_by": "tenant",
				}},
			}},
			{Name: "flow-rl-pro", Action: "upsert", Instructions: []StepConfig{
				{Action: "check_rate_limit_v2", Input: map[string]string{
					"config":   "rl-pro",
					"count_by": "tenant",
				}},
			}},
			{Name: "flow-rl-free", Action: "upsert", Instructions: []StepConfig{
				{Action: "check_rate_limit_v2", Input: map[string]string{
					"config":   "rl-free",
					"count_by": "tenant",
				}},
			}},
			{Name: "flow-plan-dispatch", Action: "upsert", Instructions: []StepConfig{
				// Resolve tenant from X-Tenant-ID header.
				{Action: "registry_lookup", KeyIdentifier: "header.X-Tenant-ID"},
				// Load the tenant's plan from registry metadata.
				{Action: "load_meta", Key: "plan", As: "tenant_plan"},
				// Dispatch to the per-plan RL check.
				{Action: "switch", As: "tenant_plan", Cases: map[string]string{
					"enterprise": "flow-rl-enterprise",
					"pro":        "flow-rl-pro",
					"free":       "flow-rl-free",
				}},
				// If we reach here, RL passed. Set 200.
				{Action: "set_response_status", Value: "200"},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "api-rl-test", Path: "/v1/rl-test", FlowName: "flow-plan-dispatch", Action: "upsert"},
		},
	})

	return fm, server
}

// sendRLRequest sends a request to /v1/rl-test with the given tenant alias
// and returns the HTTP status code from ctx.ResponseStatus.
func sendRLRequest(t *testing.T, fm *engine.FlowManager, tenantAlias string) int {
	t.Helper()
	ctx := runRequest(t, fm, http.MethodGet, "/v1/rl-test", map[string]string{
		"X-Tenant-ID": tenantAlias,
	})
	return int(ctx.ResponseStatus)
}

// TestRateLimitStopsFlowOnDenial verifies that check_rate_limit_v2 embedded in
// a switch case correctly halts the flow (DeniedPC=-1/StopPlan) when the limit
// is exceeded, returning 429 instead of continuing to proxy steps.
func TestRateLimitStopsFlowOnDenial(t *testing.T) {
	fm, _ := setupRLTestStack(t)

	// The free plan has limit=3/min. First 3 requests must succeed.
	for i := 1; i <= 3; i++ {
		status := sendRLRequest(t, fm, "tenant-free")
		if status != http.StatusOK {
			t.Errorf("request %d: expected 200 (under limit), got %d", i, status)
		}
	}

	// The 4th request must be rate-limited (429).
	status := sendRLRequest(t, fm, "tenant-free")
	if status != http.StatusTooManyRequests {
		t.Errorf("request 4 (over limit): expected 429, got %d", status)
	}

	// A 5th request also denied.
	status = sendRLRequest(t, fm, "tenant-free")
	if status != http.StatusTooManyRequests {
		t.Errorf("request 5 (over limit): expected 429, got %d", status)
	}
}

// TestPerTenantIsolation verifies that hitting the free-plan limit doesn't
// affect enterprise or pro tenants, which have much higher limits.
func TestPerTenantIsolation(t *testing.T) {
	fm, _ := setupRLTestStack(t)

	// Exhaust the free-plan limit.
	for i := 0; i < 3; i++ {
		sendRLRequest(t, fm, "tenant-free") // drain the 3-request budget
	}
	if status := sendRLRequest(t, fm, "tenant-free"); status != http.StatusTooManyRequests {
		t.Errorf("free tenant: expected 429 after limit exhausted, got %d", status)
	}

	// Enterprise and pro tenants must still get through (their limits are 1000 and 200/min).
	for i, tenant := range []string{"tenant-enterprise", "tenant-pro"} {
		status := sendRLRequest(t, fm, tenant)
		if status != http.StatusOK {
			t.Errorf("tenant %s request %d: expected 200 (should not be rate-limited), got %d", tenant, i, status)
		}
	}
}

// TestRateLimitWindowReset verifies that the counter resets in a new time window.
// This test manipulates the epoch by sending requests and verifying the window
// logic — not a real-time test (epoch = now/60, so windows are real minutes;
// this test just confirms the counter behaves correctly within a single window).
func TestRateLimitCountPerTenant(t *testing.T) {
	fm, _ := setupRLTestStack(t)

	// Each tenant has a separate counter. Sending N requests to enterprise
	// should not consume any budget from free.
	for i := 0; i < 5; i++ {
		status := sendRLRequest(t, fm, "tenant-enterprise")
		if status != http.StatusOK {
			t.Errorf("enterprise request %d: expected 200, got %d", i+1, status)
		}
	}

	// Free tenant should still have its full budget of 3.
	for i := 1; i <= 3; i++ {
		status := sendRLRequest(t, fm, "tenant-free")
		if status != http.StatusOK {
			t.Errorf("free request %d: expected 200 (own budget), got %d", i, status)
		}
	}
	if status := sendRLRequest(t, fm, "tenant-free"); status != http.StatusTooManyRequests {
		t.Errorf("free request 4: expected 429, got %d", status)
	}
}

// TestRateLimitAllPlansCorrect sends enough requests to verify each plan's
// limit is applied independently.
func TestRateLimitAllPlansCorrect(t *testing.T) {
	fm, _ := setupRLTestStack(t)

	cases := []struct {
		tenant string
		limit  int
	}{
		{"tenant-free", 3},       // rl-free: 3/min
		{"tenant-enterprise", 20}, // rl-enterprise: 1000/min (well below limit)
		{"tenant-pro", 20},        // rl-pro: 200/min (well below limit)
	}

	for _, tc := range cases {
		for i := 1; i <= tc.limit; i++ {
			status := sendRLRequest(t, fm, tc.tenant)
			if status != http.StatusOK {
				t.Errorf("tenant %s, request %d/%d: expected 200, got %d", tc.tenant, i, tc.limit, status)
			}
		}

		// One more request for free tenant should be denied.
		if tc.limit == 3 {
			status := sendRLRequest(t, fm, tc.tenant)
			if status != http.StatusTooManyRequests {
				t.Errorf("tenant %s: expected 429 after %d requests, got %d", tc.tenant, tc.limit, status)
			}
			fmt.Printf("  * %s: allowed %d, then 429\n", tc.tenant, tc.limit)
		} else {
			fmt.Printf("  * %s: %d requests all 200 (limit=%d/min)\n", tc.tenant, tc.limit, []int{1000, 200}[func() int {
				if tc.tenant == "tenant-enterprise" {
					return 0
				}
				return 1
			}()])
		}
	}
}
