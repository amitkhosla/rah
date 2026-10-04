package scheduler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newTestScheduler(runner FlowRunner) *Scheduler {
	cfg := SchedulerConfig{LookaheadSec: 3600}
	sched := New(cfg, "test-instance", runner)
	return sched
}

func slotForNow() (slotIdx uint32, epoch uint32) {
	now := time.Now()
	return uint32(now.Unix()) % 3600, uint32(now.Unix() / 3600)
}

func TestSchedulerUpsertAndFire(t *testing.T) {
	called := make(chan string, 1)
	runner := func(flowName, tenantAlias string, constants map[string]string, timeoutSec int) error {
		called <- flowName
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := newTestScheduler(runner)
	sched.Executor.Start(ctx)

	sc := &Schedule{
		Name:       "fire-test",
		Cron:       "* * * * * *",
		FlowName:   "my-flow",
		Enabled:    true,
		TimeoutSec: 10,
	}
	if err := sched.UpsertSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}

	// Arm an event directly into the wheel at the current slot
	slot, epoch := slotForNow()
	ev := globalEventPool.Get()
	ev.Name = sc.Name
	ev.FlowName = sc.FlowName
	ev.TenantAlias = sc.TenantAlias
	ev.ScheduledAt = time.Now()
	ev.Epoch = epoch
	ev.Cron = sc.Cron
	sched.Wheel.Schedule(ev)

	// Tick the slot to dispatch the event
	sched.Wheel.tick(slot)

	select {
	case name := <-called:
		if name != "my-flow" {
			t.Errorf("expected my-flow, got %s", name)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout: runner was not called after tick")
	}
}

func TestSchedulerNoDoubleFire(t *testing.T) {
	callCount := 0
	var mu sync.Mutex
	done := make(chan struct{}, 2)

	runner := func(flowName, tenantAlias string, constants map[string]string, timeoutSec int) error {
		mu.Lock()
		callCount++
		mu.Unlock()
		done <- struct{}{}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := newTestScheduler(runner)
	sched.Executor.Start(ctx)

	now := time.Now()
	sc := &Schedule{
		Name:      "dedup-test",
		Cron:      "* * * * * *",
		FlowName:  "flow",
		Enabled:   true,
	}
	_ = sched.UpsertSchedule(ctx, sc)
	// Force NextRunAt to now so LoadUpcoming picks it up
	sched.Store.(*MemoryStore).mu.Lock()
	sched.Store.(*MemoryStore).schedules["dedup-test"].NextRunAt = now
	sched.Store.(*MemoryStore).mu.Unlock()

	// Load twice — second load should be a no-op due to armed dedup
	_ = sched.Loader.LoadUpcoming(ctx)
	_ = sched.Loader.LoadUpcoming(ctx)

	slot := uint32(now.Unix()) % 3600
	sched.Wheel.tick(slot)

	// Wait briefly and assert runner called at most once
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		// If it didn't fire at all, check if event was actually due
	}
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	count := callCount
	mu.Unlock()
	if count > 1 {
		t.Errorf("runner called %d times, want at most 1 (dedup failed)", count)
	}
}

func TestSchedulerDeletePreventsExecution(t *testing.T) {
	called := make(chan struct{}, 1)
	runner := func(flowName, tenantAlias string, constants map[string]string, timeoutSec int) error {
		called <- struct{}{}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := newTestScheduler(runner)
	sched.Executor.Start(ctx)

	sc := &Schedule{
		Name:      "delete-test",
		Cron:      "* * * * * *",
		FlowName:  "flow",
		Enabled:   true,
	}
	_ = sched.UpsertSchedule(ctx, sc)

	// Arm the event
	slot, epoch := slotForNow()
	ev := globalEventPool.Get()
	ev.Name = sc.Name
	ev.FlowName = sc.FlowName
	ev.ScheduledAt = time.Now()
	ev.Epoch = epoch
	ev.Cron = sc.Cron
	sched.Wheel.Schedule(ev)

	// Delete BEFORE ticking — marks cancelled
	_ = sched.DeleteSchedule(ctx, sc.Name)

	// Now tick — event goes into dispatch ring, but executor checks cancelled map
	sched.Wheel.tick(slot)

	select {
	case <-called:
		t.Error("runner was called for a deleted schedule")
	case <-time.After(300 * time.Millisecond):
		// correct: runner was not called
	}
}

func TestSchedulerRunnerErrorPropagated(t *testing.T) {
	runner := func(flowName, tenantAlias string, constants map[string]string, timeoutSec int) error {
		return fmt.Errorf("flow failed intentionally")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := newTestScheduler(runner)
	sched.Executor.Start(ctx)

	sc := &Schedule{
		Name:      "fail-test",
		Cron:      "* * * * * *",
		FlowName:  "failing-flow",
		Enabled:   true,
	}
	_ = sched.UpsertSchedule(ctx, sc)

	slot, epoch := slotForNow()
	ev := globalEventPool.Get()
	ev.Name = sc.Name
	ev.FlowName = sc.FlowName
	ev.ScheduledAt = time.Now()
	ev.Epoch = epoch
	ev.Cron = sc.Cron
	sched.Wheel.Schedule(ev)
	sched.Wheel.tick(slot)

	// Wait for execution to complete
	time.Sleep(200 * time.Millisecond)

	// Verify execution was recorded with "failed" status
	hist, err := sched.Store.ListHistory(ctx, sc.Name, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) == 0 {
		t.Fatal("expected execution history to be recorded")
	}
	if hist[0].Status != "failed" {
		t.Errorf("expected status 'failed', got '%s'", hist[0].Status)
	}
}
