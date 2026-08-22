package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

// NamedCircuit holds the atomic state for one named circuit.
// Fields are ordered for cache-line friendliness: all atomics first,
// then immutable config. The _pad field keeps the struct at 64 bytes
// to avoid false sharing between circuits in the registry.
type NamedCircuit struct {
	// Atomic mutable state (hot path).
	state        atomic.Int32 // CBStateClosed / CBStateOpen / CBStateHalfOpen
	failureCount atomic.Int64 // consecutive failures (Closed state)
	successCount atomic.Int64 // consecutive successes (HalfOpen state)
	lastTripNs   atomic.Int64 // UnixNano when circuit last opened
	openDurationNs atomic.Int64 // how long to stay Open before probing (can be updated)
	_pad         [32]byte     // pad struct to 64 bytes to avoid false sharing

	// Immutable after creation.
	name             string
	failureThreshold int64
	successThreshold int64
}

// namedCircuits is the global registry: string name → *NamedCircuit.
// Uses sync.Map for lock-free reads on the hot path.
var namedCircuits sync.Map

// GetOrCreateNamedCircuit returns the NamedCircuit for the given name,
// creating it if necessary with the specified thresholds and open duration.
// Uses sync.Map.LoadOrStore to avoid double-creation under concurrent access.
// The function is safe to call concurrently.
func GetOrCreateNamedCircuit(name string, failureThresh, successThresh int64, openDurationMs uint32) *NamedCircuit {
	nc := &NamedCircuit{
		name:             name,
		failureThreshold: failureThresh,
		successThreshold: successThresh,
	}
	nc.openDurationNs.Store(int64(openDurationMs) * int64(time.Millisecond))

	// LoadOrStore atomically returns existing or stores and returns new.
	actual, _ := namedCircuits.LoadOrStore(name, nc)
	return actual.(*NamedCircuit)
}

// IsNamedCircuitOpen checks whether the named circuit is currently open.
// This is a HOT PATH function with zero allocations:
// - Returns false if circuit not found (unknown circuit = closed).
// - Returns false if Closed or HalfOpen (requests allowed).
// - If Open and probe window elapsed, transitions to HalfOpen and returns false.
// - If Open and still waiting, returns true (circuit blocking).
func IsNamedCircuitOpen(name string) bool {
	val, ok := namedCircuits.Load(name)
	if !ok {
		return false // unknown circuit is closed
	}

	nc := val.(*NamedCircuit)
	s := nc.state.Load()

	switch s {
	case CBStateClosed:
		return false
	case CBStateHalfOpen:
		return false // probe allowed
	case CBStateOpen:
		now := time.Now().UnixNano()
		lastTrip := nc.lastTripNs.Load()
		openDur := nc.openDurationNs.Load()
		if now-lastTrip >= openDur {
			// Probe window elapsed; attempt transition to HalfOpen.
			// Only one goroutine will succeed; all will proceed (allowing probe).
			nc.state.CompareAndSwap(CBStateOpen, CBStateHalfOpen)
			return false
		}
		return true // still open
	}

	return false
}

// GetNamedCircuitState returns the raw circuit state constant for the named circuit.
// Returns CBStateClosed if the circuit is not registered.
// This is a hot-path helper: sync.Map.Load + one atomic.Load, zero allocations.
func GetNamedCircuitState(name string) int32 {
	val, ok := namedCircuits.Load(name)
	if !ok {
		return CBStateClosed
	}
	return val.(*NamedCircuit).state.Load()
}

// TripNamedCircuit immediately opens the named circuit and stores the trip time.
// If the circuit doesn't exist, creates it with defaults and the given override duration.
// The optional openDurationOverrideMs, if > 0, updates the circuit's open duration.
// Uses atomic operations to drive the state transition.
func TripNamedCircuit(name string, openDurationOverrideMs uint32) {
	val, _ := namedCircuits.Load(name)
	if val == nil {
		val = GetOrCreateNamedCircuit(name, 5, 2, openDurationOverrideMs)
	}
	circuit := val.(*NamedCircuit)

	if openDurationOverrideMs > 0 {
		circuit.openDurationNs.Store(int64(openDurationOverrideMs) * int64(time.Millisecond))
	}

	circuit.lastTripNs.Store(time.Now().UnixNano())
	circuit.failureCount.Store(0)
	circuit.successCount.Store(0)

	// Drive state to Open via CAS loop (may already be Open, but CAS handles it).
	for {
		old := circuit.state.Load()
		if circuit.state.CompareAndSwap(old, CBStateOpen) || old == CBStateOpen {
			break
		}
	}
}

// ResetNamedCircuit resets the named circuit to Closed state.
// Returns true if the circuit was found and reset, false if not found.
func ResetNamedCircuit(name string) bool {
	val, ok := namedCircuits.Load(name)
	if !ok {
		return false
	}

	nc := val.(*NamedCircuit)
	nc.state.Store(CBStateClosed)
	nc.failureCount.Store(0)
	nc.successCount.Store(0)
	return true
}

// RecordNamedCircuitOutcome records a success or failure for the named circuit
// and transitions state according to the circuit breaker state machine.
// Returns (stateChanged, newState) where stateChanged indicates if the state changed
// and newState is the resulting state. Returns (false, CBStateClosed) if circuit not found.
//
// State transitions:
//   - Closed + failure: increment failureCount; if >= threshold → Open.
//   - Closed + success: reset failureCount.
//   - HalfOpen + success: increment successCount; if >= threshold → Closed.
//   - HalfOpen + failure: re-Open.
//   - Open: no change from outcome recording.
func RecordNamedCircuitOutcome(name string, success bool) (bool, int32) {
	val, ok := namedCircuits.Load(name)
	if !ok {
		return false, CBStateClosed
	}

	nc := val.(*NamedCircuit)
	s := nc.state.Load()

	switch s {
	case CBStateClosed:
		if !success {
			fc := nc.failureCount.Add(1)
			if fc >= nc.failureThreshold {
				// Attempt to transition to Open.
				if nc.state.CompareAndSwap(CBStateClosed, CBStateOpen) {
					nc.lastTripNs.Store(time.Now().UnixNano())
					nc.failureCount.Store(0)
					return true, CBStateOpen
				}
			}
		} else {
			// Reset consecutive failure count on success.
			nc.failureCount.Store(0)
		}
		return false, CBStateClosed

	case CBStateHalfOpen:
		if success {
			sc := nc.successCount.Add(1)
			if sc >= nc.successThreshold {
				// CRITICAL: set state CLOSED first, then reset counters so
				// concurrent readers never see a partial reset.
				if nc.state.CompareAndSwap(CBStateHalfOpen, CBStateClosed) {
					nc.failureCount.Store(0)
					nc.successCount.Store(0)
					return true, CBStateClosed
				}
			}
		} else {
			// Probe failed — re-open the circuit and reset both counters so the
			// next HalfOpen cycle starts clean. successCount is reset here (not at
			// probe entry) to avoid resetting partial successes under concurrency.
			if nc.state.CompareAndSwap(CBStateHalfOpen, CBStateOpen) {
				nc.lastTripNs.Store(time.Now().UnixNano())
				nc.failureCount.Store(0)
				nc.successCount.Store(0)
				return true, CBStateOpen
			}
		}
		return false, CBStateHalfOpen

	case CBStateOpen:
		// No state change from outcome recording when Open.
		return false, CBStateOpen
	}

	return false, s
}
