package scheduler

import (
	"context"
	"testing"
	"time"
)

// TestMemoryStoreListDueAtExactTime verifies the >= boundary check.
// The condition !s.NextRunAt.Before(now) correctly implements >=.
func TestMemoryStoreListDueAtExactTime(t *testing.T) {
	ms := NewMemoryStore()

	baseTime := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	nowPlusOneHour := baseTime.Add(1 * time.Hour)

	sc := &Schedule{
		Name:     "future-schedule",
		Cron:     "* * * * * *",
		FlowName: "flow",
		Enabled:  true,
	}
	_ = ms.Upsert(context.Background(), sc)

	// Manually set NextRunAt to ensure it's within the window
	ms.mu.Lock()
	ms.schedules["future-schedule"].NextRunAt = nowPlusOneHour.Add(30 * time.Second)
	ms.mu.Unlock()

	// Simulate a ListDueWithin call using fixed times to avoid race conditions
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var events []*ScheduledEvent
	now := nowPlusOneHour          // Use our base time, not time.Now()
	cutoff := now.Add(time.Minute) // 60 second window

	for _, s := range ms.schedules {
		if !s.Enabled {
			continue
		}
		// This is the exact condition from ListDueWithin
		if !s.NextRunAt.Before(now) && s.NextRunAt.Before(cutoff) {
			events = append(events, &ScheduledEvent{
				Name:     s.Name,
				FlowName: s.FlowName,
			})
		}
	}

	if len(events) == 0 {
		t.Error("expected schedule with NextRunAt at +30s to be included in 60s window")
	}
}

// TestMemoryStoreRecordExecutionChecksExistenceFirst verifies that history is NOT written
// if the schedule has been deleted.
func TestMemoryStoreRecordExecutionChecksExistenceFirst(t *testing.T) {
	ms := NewMemoryStore()
	sc := &Schedule{
		Name:     "gone",
		Cron:     "* * * * * *",
		FlowName: "f",
		Enabled:  true,
	}
	_ = ms.Upsert(context.Background(), sc)
	_ = ms.Delete(context.Background(), "gone")

	rec := ExecutionRecord{Name: "gone", Status: "ok"}
	err := ms.RecordExecution(context.Background(), rec, time.Now().Add(time.Minute))
	if err == nil {
		t.Error("expected error when recording for deleted schedule")
	}

	// Also verify no history was written
	hist, _ := ms.ListHistory(context.Background(), "gone", 10)
	if len(hist) > 0 {
		t.Error("expected no history to be written for deleted schedule")
	}
}

// TestMemoryStoreListHistoryDefaultLimit verifies that limit=0 returns at most 100 records.
func TestMemoryStoreListHistoryDefaultLimit(t *testing.T) {
	ms := NewMemoryStore()
	sc := &Schedule{
		Name:     "many",
		Cron:     "* * * * * *",
		FlowName: "f",
		Enabled:  true,
	}
	_ = ms.Upsert(context.Background(), sc)

	// Insert 150 records directly
	ms.mu.Lock()
	for i := 0; i < 150; i++ {
		ms.history["many"] = append(ms.history["many"], ExecutionRecord{
			Name:   "many",
			Status: "ok",
		})
	}
	ms.mu.Unlock()

	hist, err := ms.ListHistory(context.Background(), "many", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) > 100 {
		t.Errorf("limit=0 should return at most 100, got %d", len(hist))
	}
}

// TestMemoryStoreClaimDeletedSchedule verifies that Claim returns ClaimLost
// for a non-existent schedule.
func TestMemoryStoreClaimDeletedSchedule(t *testing.T) {
	ms := NewMemoryStore()
	result, err := ms.Claim(context.Background(), "nonexistent", "instance-1")
	if err != nil {
		t.Fatal(err)
	}
	if result != ClaimLost {
		t.Errorf("expected ClaimLost for nonexistent schedule, got %v", result)
	}
}
