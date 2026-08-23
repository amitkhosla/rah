package engine

import (
	"sync"
	"testing"
	"time"
)

// clearNamedCircuits removes all entries from the global namedCircuits registry.
// Call this before each test that creates named circuits.
func clearNamedCircuits() {
	namedCircuits.Range(func(k, _ any) bool {
		namedCircuits.Delete(k)
		return true
	})
}

// Tier 1 — Functional tests

// TestNamedCircuit_TripAndOpen verifies that tripping a circuit makes it open.
func TestNamedCircuit_TripAndOpen(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	circuit := GetOrCreateNamedCircuit("trip-test", 5, 2, 100)
	if circuit == nil {
		t.Fatal("GetOrCreateNamedCircuit returned nil")
	}

	// Initially closed.
	if IsNamedCircuitOpen("trip-test") {
		t.Error("circuit should not be open initially")
	}

	// Trip it.
	TripNamedCircuit("trip-test", 0)

	// Now it should be open.
	if !IsNamedCircuitOpen("trip-test") {
		t.Error("circuit should be open after trip")
	}
}

// TestNamedCircuit_ResetAfterTrip verifies that reset closes an open circuit.
func TestNamedCircuit_ResetAfterTrip(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	GetOrCreateNamedCircuit("reset-test", 5, 2, 100)
	TripNamedCircuit("reset-test", 0)

	if !IsNamedCircuitOpen("reset-test") {
		t.Fatal("circuit should be open")
	}

	// Reset it.
	ok := ResetNamedCircuit("reset-test")
	if !ok {
		t.Error("ResetNamedCircuit should return true for existing circuit")
	}

	// Now should be closed.
	if IsNamedCircuitOpen("reset-test") {
		t.Error("circuit should be closed after reset")
	}
}

// TestNamedCircuit_AutoProbeAfterDuration verifies auto-transition to HalfOpen.
func TestNamedCircuit_AutoProbeAfterDuration(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	// 1ms open duration.
	GetOrCreateNamedCircuit("probe-test", 5, 2, 1)
	TripNamedCircuit("probe-test", 0)

	if !IsNamedCircuitOpen("probe-test") {
		t.Fatal("circuit should be open immediately after trip")
	}

	// Wait for probe window to elapse.
	time.Sleep(2 * time.Millisecond)

	// Circuit should now allow probe (IsOpen returns false).
	if IsNamedCircuitOpen("probe-test") {
		t.Error("circuit should allow probe after duration elapsed")
	}
}

// TestNamedCircuit_RecordOutcome_FullCycle verifies full state machine cycle.
func TestNamedCircuit_RecordOutcome_FullCycle(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	// Create circuit: 5 failures to open, 2 successes to close.
	GetOrCreateNamedCircuit("cycle-test", 5, 2, 100)

	// Record 4 failures (threshold not yet met).
	for i := 0; i < 4; i++ {
		changed, state := RecordNamedCircuitOutcome("cycle-test", false)
		if changed {
			t.Errorf("state should not change at failure %d", i+1)
		}
		if state != CBStateClosed {
			t.Errorf("state should be Closed at failure %d", i+1)
		}
	}

	// 5th failure should trip.
	changed, state := RecordNamedCircuitOutcome("cycle-test", false)
	if !changed {
		t.Error("state should change at 5th failure")
	}
	if state != CBStateOpen {
		t.Errorf("state should be Open after trip, got %d", state)
	}

	// Now we're in HalfOpen after probe window (simulate by resetting state manually for testing).
	// Instead, let's manually set state to HalfOpen to test the success count logic.
	val, _ := namedCircuits.Load("cycle-test")
	circuit := val.(*NamedCircuit)
	circuit.state.Store(CBStateHalfOpen)
	circuit.successCount.Store(0)

	// Record 1 success (threshold not yet met).
	changed, state = RecordNamedCircuitOutcome("cycle-test", true)
	if changed {
		t.Error("state should not change at 1st success in HalfOpen")
	}
	if state != CBStateHalfOpen {
		t.Errorf("state should stay HalfOpen at 1st success, got %d", state)
	}

	// 2nd success should close.
	changed, state = RecordNamedCircuitOutcome("cycle-test", true)
	if !changed {
		t.Error("state should change at 2nd success in HalfOpen")
	}
	if state != CBStateClosed {
		t.Errorf("state should be Closed after recovery, got %d", state)
	}
}

// TestNamedCircuit_GetOrCreate_Idempotent verifies LoadOrStore behavior.
func TestNamedCircuit_GetOrCreate_Idempotent(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	c1 := GetOrCreateNamedCircuit("idempotent-test", 5, 2, 100)
	c2 := GetOrCreateNamedCircuit("idempotent-test", 5, 2, 100)

	if c1 != c2 {
		t.Error("GetOrCreateNamedCircuit should return same pointer for same name")
	}
}

// Tier 2 — Negative tests

// TestNamedCircuit_UnknownName_NotOpen verifies unknown circuits report not open.
func TestNamedCircuit_UnknownName_NotOpen(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	if IsNamedCircuitOpen("nonexistent") {
		t.Error("unknown circuit should not be open")
	}
}

// TestNamedCircuit_Reset_UnknownName verifies reset returns false for unknown circuit.
func TestNamedCircuit_Reset_UnknownName(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	ok := ResetNamedCircuit("nonexistent")
	if ok {
		t.Error("ResetNamedCircuit should return false for unknown circuit")
	}
}

// TestNamedCircuit_RecordOutcome_UnknownName verifies outcome recording for unknown circuit.
func TestNamedCircuit_RecordOutcome_UnknownName(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	changed, state := RecordNamedCircuitOutcome("nonexistent", true)
	if changed {
		t.Error("state should not change for unknown circuit")
	}
	if state != CBStateClosed {
		t.Error("unknown circuit should report Closed state")
	}
}

// Tier 3 — Non-functional tests

// TestNamedCircuit_ConcurrentOutcomes verifies thread-safety under concurrent recording.
func TestNamedCircuit_ConcurrentOutcomes(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	GetOrCreateNamedCircuit("race-circuit", 100, 50, 100)

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				RecordNamedCircuitOutcome("race-circuit", false)
			}
		}()
	}

	wg.Wait()

	// Verify we can still read state without panic.
	IsNamedCircuitOpen("race-circuit")
}

// TestNamedCircuit_BlastRadius verifies that one circuit doesn't affect another.
func TestNamedCircuit_BlastRadius(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	GetOrCreateNamedCircuit("circuit-a", 5, 2, 100)
	GetOrCreateNamedCircuit("circuit-b", 5, 2, 100)

	// Trip circuit-a.
	TripNamedCircuit("circuit-a", 0)

	if !IsNamedCircuitOpen("circuit-a") {
		t.Fatal("circuit-a should be open")
	}

	// circuit-b should be unaffected.
	if IsNamedCircuitOpen("circuit-b") {
		t.Error("circuit-b should not be affected by tripping circuit-a")
	}
}

// TestNamedCircuit_IsOpen_ZeroAllocs verifies IsNamedCircuitOpen has zero allocations.
func TestNamedCircuit_IsOpen_ZeroAllocs(t *testing.T) {
	t.Helper()
	clearNamedCircuits()

	// Create circuit first so Map.Load hits it.
	GetOrCreateNamedCircuit("alloc-test", 5, 2, 100)

	// Measure allocations per run.
	allocs := testing.AllocsPerRun(100, func() {
		IsNamedCircuitOpen("alloc-test")
	})

	if allocs > 0 {
		t.Errorf("IsNamedCircuitOpen should allocate 0 times, got %v", allocs)
	}
}
