package scheduler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// stubStore implements Store for testing.
type stubStore struct {
	claimResult  ClaimResult
	claimErr     error
	recResult    error
	mu           sync.Mutex
	recCallCount int
	lastRec      ExecutionRecord
	lastNextRun  time.Time
}

func (s *stubStore) Claim(ctx context.Context, name string, instanceID string) (ClaimResult, error) {
	return s.claimResult, s.claimErr
}

func (s *stubStore) RecordExecution(ctx context.Context, rec ExecutionRecord, nextRun time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recCallCount++
	s.lastRec = rec
	s.lastNextRun = nextRun
	return s.recResult
}

func (s *stubStore) Upsert(ctx context.Context, sc *Schedule) error {
	return nil
}

func (s *stubStore) Delete(ctx context.Context, name string) error {
	return nil
}

func (s *stubStore) ListDueWithin(ctx context.Context, w int) ([]*ScheduledEvent, error) {
	return nil, nil
}

func (s *stubStore) ListAll(ctx context.Context) ([]*Schedule, error) {
	return nil, nil
}

func (s *stubStore) ListHistory(ctx context.Context, name string, limit int) ([]ExecutionRecord, error) {
	return nil, nil
}

// newTestExecutor creates an Executor for testing.
func newTestExecutor(store Store, runner FlowRunner) *Executor {
	dispatch := NewDispatchRing(64)
	var cancelled sync.Map
	return NewExecutor(store, "test-instance", runner, dispatch, &cancelled)
}

// makeTestEvent creates a ScheduledEvent for testing.
func makeTestEvent(name string) *ScheduledEvent {
	ev := globalEventPool.Get()
	ev.Name = name
	ev.FlowName = "test-flow"
	ev.TenantAlias = "test-tenant"
	ev.ScheduledAt = time.Now()
	ev.Epoch = uint32(time.Now().Unix() / 3600)
	return ev
}

// TestExecutorNextRunFromCron verifies that nextRun is computed from the cron expression.
func TestExecutorNextRunFromCron(t *testing.T) {
	store := &stubStore{claimResult: ClaimWon}
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		return nil // success
	}
	executor := newTestExecutor(store, runner)

	ev := makeTestEvent("cron-test")
	ev.Cron = "0 * * * * *" // fires at second 0 of every minute
	defer globalEventPool.Put(ev)

	now := time.Now()
	executor.handle(ev)

	if store.lastNextRun.Before(now) {
		t.Errorf("expected nextRun to be in the future, got %v (now=%v)", store.lastNextRun, now)
	}
	if store.lastNextRun.After(now.Add(2 * time.Minute)) {
		t.Errorf("expected nextRun to be within 2 minutes of now, got %v (now=%v)", store.lastNextRun, now)
	}
}

// TestExecutorNilRunner verifies that a nil runner returns an error.
func TestExecutorNilRunner(t *testing.T) {
	store := &stubStore{claimResult: ClaimWon}
	executor := newTestExecutor(store, nil)

	ev := makeTestEvent("nil-runner-test")
	defer globalEventPool.Put(ev)

	executor.handle(ev)

	if store.lastRec.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", store.lastRec.Status)
	}
	if store.lastRec.Error == "" {
		t.Error("expected non-empty error message")
	}
}

// TestExecutorTimeout verifies that TimeoutSec=0 defaults to 300s (runner is called).
func TestExecutorTimeout(t *testing.T) {
	store := &stubStore{claimResult: ClaimWon}
	runnerCalled := false
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		runnerCalled = true
		return nil
	}
	executor := newTestExecutor(store, runner)

	ev := makeTestEvent("timeout-test")
	ev.TimeoutSec = 0 // should default to 300s
	defer globalEventPool.Put(ev)

	executor.handle(ev)

	if !runnerCalled {
		t.Error("expected runner to be called; TimeoutSec=0 should default to 300s, not immediately cancel")
	}
}

// TestExecutorRetry verifies that retries work correctly.
func TestExecutorRetry(t *testing.T) {
	store := &stubStore{claimResult: ClaimWon}
	callCount := 0
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		callCount++
		if callCount < 3 {
			return fmt.Errorf("attempt %d failed", callCount)
		}
		return nil // succeeds on 3rd attempt
	}
	executor := newTestExecutor(store, runner)

	ev := makeTestEvent("retry-test")
	ev.RetryCount = 2       // 2 retries means 3 total attempts
	ev.RetryIntervalSec = 0 // no delay between retries
	defer globalEventPool.Put(ev)

	executor.handle(ev)

	if callCount != 3 {
		t.Errorf("expected runner to be called 3 times, got %d", callCount)
	}
	if store.lastRec.Status != "ok" {
		t.Errorf("expected final status 'ok', got %q", store.lastRec.Status)
	}
}

// TestExecutorClaimLost verifies that runner is not called when claim is lost.
func TestExecutorClaimLost(t *testing.T) {
	store := &stubStore{claimResult: ClaimLost}
	runnerCalled := false
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		runnerCalled = true
		return nil
	}
	executor := newTestExecutor(store, runner)

	ev := makeTestEvent("claim-lost-test")
	defer globalEventPool.Put(ev)

	executor.handle(ev)

	if runnerCalled {
		t.Error("expected runner to not be called when claim is lost")
	}
	if store.recCallCount > 0 {
		t.Error("expected RecordExecution to not be called when claim is lost")
	}
}

// TestExecutorClaimError verifies that runner is not called when claim returns error.
func TestExecutorClaimError(t *testing.T) {
	store := &stubStore{claimResult: ClaimError, claimErr: fmt.Errorf("claim failed")}
	runnerCalled := false
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		runnerCalled = true
		return nil
	}
	executor := newTestExecutor(store, runner)

	ev := makeTestEvent("claim-error-test")
	defer globalEventPool.Put(ev)

	executor.handle(ev)

	if runnerCalled {
		t.Error("expected runner to not be called when claim returns error")
	}
	if store.recCallCount > 0 {
		t.Error("expected RecordExecution to not be called when claim returns error")
	}
}

// TestExecutorCancelledSchedule verifies that cancelled schedules with non-expired TTL are skipped.
func TestExecutorCancelledSchedule(t *testing.T) {
	store := &stubStore{claimResult: ClaimWon}
	runnerCalled := false
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		runnerCalled = true
		return nil
	}
	dispatch := NewDispatchRing(64)
	var cancelled sync.Map
	executor := NewExecutor(store, "test-instance", runner, dispatch, &cancelled)

	ev := makeTestEvent("cancelled-test")
	defer globalEventPool.Put(ev)

	// Mark the event as cancelled with an expiry in the future (TTL not expired)
	futureExpiry := time.Now().Add(1 * time.Minute)
	cancelled.Store(ev.Name, futureExpiry)

	executor.handle(ev)

	if runnerCalled {
		t.Error("expected runner to not be called for cancelled schedule")
	}
	if store.recCallCount > 0 {
		t.Error("expected RecordExecution to not be called for cancelled schedule")
	}

	// Verify the cancelled entry was removed
	_, found := cancelled.Load(ev.Name)
	if found {
		t.Error("expected cancelled entry to be removed after skipping")
	}
}

// TestExecutorDeadLetter verifies that dead letter flow is called on final failure.
func TestExecutorDeadLetter(t *testing.T) {
	store := &stubStore{claimResult: ClaimWon}
	flowsCalled := make(map[string]int)
	var mu sync.Mutex
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		mu.Lock()
		flowsCalled[flowName]++
		mu.Unlock()
		if flowName != "dead-letter-flow" {
			return fmt.Errorf("execution failed")
		}
		return nil
	}
	executor := newTestExecutor(store, runner)

	ev := makeTestEvent("dead-letter-test")
	ev.DeadLetterFlow = "dead-letter-flow"
	ev.RetryCount = 0 // 1 attempt total
	defer globalEventPool.Put(ev)

	executor.handle(ev)

	if flowsCalled["test-flow"] != 1 {
		t.Errorf("expected test-flow to be called once, got %d", flowsCalled["test-flow"])
	}
	if flowsCalled["dead-letter-flow"] != 1 {
		t.Errorf("expected dead-letter-flow to be called once, got %d", flowsCalled["dead-letter-flow"])
	}
	if store.lastRec.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", store.lastRec.Status)
	}
}

// TestCancelledTTL_WithinExpiry_SkipsEvent verifies that an event is skipped
// if its cancellation expiry is in the future.
func TestCancelledTTL_WithinExpiry_SkipsEvent(t *testing.T) {
	store := &stubStore{claimResult: ClaimWon}
	runnerCalled := false
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		runnerCalled = true
		return nil
	}

	dispatch := NewDispatchRing(64)
	var cancelled sync.Map
	executor := NewExecutor(store, "test-instance", runner, dispatch, &cancelled)

	ev := makeTestEvent("ttl-within-test")
	defer globalEventPool.Put(ev)

	// Store an expiry in the future (30 seconds from now)
	futureExpiry := time.Now().Add(30 * time.Second)
	cancelled.Store(ev.Name, futureExpiry)

	executor.handle(ev)

	// Verify the event was NOT processed
	if runnerCalled {
		t.Error("expected runner to not be called for event within TTL expiry")
	}
	if store.recCallCount > 0 {
		t.Error("expected RecordExecution to not be called for event within TTL expiry")
	}

	// Verify the cancelled entry was cleaned up
	_, found := cancelled.Load(ev.Name)
	if found {
		t.Error("expected cancelled entry to be removed after skipping")
	}
}

// TestCancelledTTL_Expired_ProcessesEvent verifies that an event is processed normally
// if its cancellation expiry is in the past (TTL expired).
func TestCancelledTTL_Expired_ProcessesEvent(t *testing.T) {
	store := &stubStore{claimResult: ClaimWon}
	runnerCalled := false
	runner := func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error {
		runnerCalled = true
		return nil
	}

	dispatch := NewDispatchRing(64)
	var cancelled sync.Map
	executor := NewExecutor(store, "test-instance", runner, dispatch, &cancelled)

	ev := makeTestEvent("ttl-expired-test")
	defer globalEventPool.Put(ev)

	// Store an expiry in the PAST (already expired)
	pastExpiry := time.Now().Add(-5 * time.Second)
	cancelled.Store(ev.Name, pastExpiry)

	executor.handle(ev)

	// Verify the event WAS processed
	if !runnerCalled {
		t.Error("expected runner to be called for event with expired TTL")
	}
	if store.recCallCount == 0 {
		t.Error("expected RecordExecution to be called for event with expired TTL")
	}

	// Verify the cancelled entry was cleaned up
	_, found := cancelled.Load(ev.Name)
	if found {
		t.Error("expected cancelled entry to be removed after processing")
	}
}
