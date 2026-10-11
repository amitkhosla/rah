package datacache

import (
	"sync"
	"testing"
)

// TestQuota_ReserveRelease tests basic Reserve and Release operations.
// Reserve(tenantID=1, app="a", 100, 1) succeeds; Release then Reserve again succeeds
func TestQuota_ReserveRelease(t *testing.T) {
	qr := NewQuotaRegistry(1000, 10)

	// Reserve should succeed
	err := qr.Reserve(1, "app-a", 100, 1)
	if err != nil {
		t.Fatalf("first Reserve failed: %v", err)
	}

	// Verify usage
	if qr.UsageBytes(1, "app-a") != 100 {
		t.Errorf("expected UsageBytes 100, got %d", qr.UsageBytes(1, "app-a"))
	}
	if qr.UsageSlots(1, "app-a") != 1 {
		t.Errorf("expected UsageSlots 1, got %d", qr.UsageSlots(1, "app-a"))
	}

	// Release
	qr.Release(1, "app-a", 100, 1)

	// Verify usage is back to 0
	if qr.UsageBytes(1, "app-a") != 0 {
		t.Errorf("expected UsageBytes 0 after Release, got %d", qr.UsageBytes(1, "app-a"))
	}
	if qr.UsageSlots(1, "app-a") != 0 {
		t.Errorf("expected UsageSlots 0 after Release, got %d", qr.UsageSlots(1, "app-a"))
	}

	// Reserve again should succeed
	err = qr.Reserve(1, "app-a", 100, 1)
	if err != nil {
		t.Fatalf("second Reserve after Release failed: %v", err)
	}
}

// TestQuota_ExceedsPlatformLimit tests that Reserve fails when exceeding platform limits.
// NewQuotaRegistry(maxBytes=100, maxSlots=10), Reserve(_, _, 200, 1) → ErrQuotaExceeded
func TestQuota_ExceedsPlatformLimit(t *testing.T) {
	qr := NewQuotaRegistry(100, 10)

	// Try to reserve more bytes than the platform limit
	err := qr.Reserve(1, "app-a", 200, 1)
	if err != ErrQuotaExceeded {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}

	// Verify no quota was reserved
	if qr.UsageBytes(1, "app-a") != 0 {
		t.Errorf("expected UsageBytes 0, got %d", qr.UsageBytes(1, "app-a"))
	}
}

// TestQuota_ExceedsSlotLimit tests slot limit enforcement.
func TestQuota_ExceedsSlotLimit(t *testing.T) {
	qr := NewQuotaRegistry(1000, 2) // max 2 slots

	// First reserve succeeds
	err := qr.Reserve(1, "app-a", 100, 1)
	if err != nil {
		t.Fatalf("first Reserve failed: %v", err)
	}

	// Second reserve succeeds
	err = qr.Reserve(1, "app-a", 100, 1)
	if err != nil {
		t.Fatalf("second Reserve failed: %v", err)
	}

	// Third reserve should fail (exceeds slot limit)
	err = qr.Reserve(1, "app-a", 100, 1)
	if err != ErrQuotaExceeded {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}

	// Verify only 2 slots are used
	if qr.UsageSlots(1, "app-a") != 2 {
		t.Errorf("expected UsageSlots 2, got %d", qr.UsageSlots(1, "app-a"))
	}
}

// TestQuota_ConcurrentReserve tests concurrent Reserve operations with slot limits.
// 10 goroutines each Reserve(_, _, 1, 0) → at most maxSlots succeed
func TestQuota_ConcurrentReserve(t *testing.T) {
	t.Parallel()
	qr := NewQuotaRegistry(1000, 5) // max 5 slots

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	// 10 goroutines try to reserve
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := qr.Reserve(1, "app-a", 1, 1)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// At most 5 should succeed
	if successCount > 5 {
		t.Errorf("expected at most 5 successes, got %d", successCount)
	}

	// Verify slot usage equals success count
	actualSlots := qr.UsageSlots(1, "app-a")
	if int(actualSlots) != successCount {
		t.Errorf("expected UsageSlots %d, got %d", successCount, actualSlots)
	}
}

// TestQuota_MultiTenantIsolation tests that quotas are isolated per tenant.
func TestQuota_MultiTenantIsolation(t *testing.T) {
	qr := NewQuotaRegistry(1000, 10)

	// Tenant 1 reserves
	err := qr.Reserve(1, "app-a", 100, 1)
	if err != nil {
		t.Fatalf("tenant 1 reserve failed: %v", err)
	}

	// Tenant 2 reserves independently
	err = qr.Reserve(2, "app-a", 200, 2)
	if err != nil {
		t.Fatalf("tenant 2 reserve failed: %v", err)
	}

	// Verify quotas are separate
	if qr.UsageBytes(1, "app-a") != 100 {
		t.Errorf("tenant 1: expected UsageBytes 100, got %d", qr.UsageBytes(1, "app-a"))
	}
	if qr.UsageBytes(2, "app-a") != 200 {
		t.Errorf("tenant 2: expected UsageBytes 200, got %d", qr.UsageBytes(2, "app-a"))
	}

	if qr.UsageSlots(1, "app-a") != 1 {
		t.Errorf("tenant 1: expected UsageSlots 1, got %d", qr.UsageSlots(1, "app-a"))
	}
	if qr.UsageSlots(2, "app-a") != 2 {
		t.Errorf("tenant 2: expected UsageSlots 2, got %d", qr.UsageSlots(2, "app-a"))
	}
}

// TestQuota_MultiAppIsolation tests that quotas are isolated per app.
func TestQuota_MultiAppIsolation(t *testing.T) {
	qr := NewQuotaRegistry(1000, 10)

	// Reserve for app-a
	err := qr.Reserve(1, "app-a", 100, 1)
	if err != nil {
		t.Fatalf("app-a reserve failed: %v", err)
	}

	// Reserve for app-b
	err = qr.Reserve(1, "app-b", 200, 2)
	if err != nil {
		t.Fatalf("app-b reserve failed: %v", err)
	}

	// Verify quotas are separate
	if qr.UsageBytes(1, "app-a") != 100 {
		t.Errorf("app-a: expected UsageBytes 100, got %d", qr.UsageBytes(1, "app-a"))
	}
	if qr.UsageBytes(1, "app-b") != 200 {
		t.Errorf("app-b: expected UsageBytes 200, got %d", qr.UsageBytes(1, "app-b"))
	}
}

// TestQuota_SetTenantLimit tests setting custom limits per tenant.
func TestQuota_SetTenantLimit(t *testing.T) {
	qr := NewQuotaRegistry(1000, 10)

	// Set custom limit for tenant 1
	qr.SetTenantLimit(1, "app-a", 50, 2)

	// Reserve up to the custom limit
	err := qr.Reserve(1, "app-a", 50, 2)
	if err != nil {
		t.Fatalf("reserve at custom limit failed: %v", err)
	}

	// Try to exceed custom limit
	err = qr.Reserve(1, "app-a", 1, 0)
	if err != ErrQuotaExceeded {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}
}

// TestQuota_ReleaseNonexistent tests that Release on non-existent entry is safe.
func TestQuota_ReleaseNonexistent(t *testing.T) {
	qr := NewQuotaRegistry(1000, 10)

	// Should not panic
	qr.Release(999, "nonexistent", 100, 1)

	// Usage should still be 0
	if qr.UsageBytes(999, "nonexistent") != 0 {
		t.Errorf("expected UsageBytes 0, got %d", qr.UsageBytes(999, "nonexistent"))
	}
}

// TestQuota_AtomicBytesAndSlotsRollback tests that partial failures rollback properly.
func TestQuota_AtomicBytesAndSlotsRollback(t *testing.T) {
	qr := NewQuotaRegistry(1000, 1) // only 1 slot

	// First reserve succeeds
	err := qr.Reserve(1, "app-a", 500, 1)
	if err != nil {
		t.Fatalf("first reserve failed: %v", err)
	}

	// Second reserve fails on slots but should rollback bytes too
	err = qr.Reserve(1, "app-a", 200, 1)
	if err != ErrQuotaExceeded {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}

	// Verify bytes didn't increase
	if qr.UsageBytes(1, "app-a") != 500 {
		t.Errorf("expected UsageBytes 500 (rollback), got %d", qr.UsageBytes(1, "app-a"))
	}
}
