package engine

import (
	"fmt"
	"github.com/amitkhosla/rah/internal/rctx"
	"sync/atomic"
	"time"
)

// Circuit breaker states (stored atomically in CircuitState.state).
const (
	CBStateClosed   int32 = 0 // normal â€” all requests pass through
	CBStateOpen     int32 = 1 // tripped â€” requests are rejected
	CBStateHalfOpen int32 = 2 // probe â€” limited requests allowed to test recovery
)

// CircuitState is one named circuit breaker instance.
// All mutable fields are accessed via sync/atomic â€” no mutex on the hot path.
// Config fields (failureThreshold, successThreshold, openDurationNs) are
// written once at bake time and read-only thereafter.
type CircuitState struct {
	// Mutable state (atomic).
	state        int32 // CBStateClosed / CBStateOpen / CBStateHalfOpen
	failureCount int64 // consecutive failures while Closed
	successCount int64 // consecutive successes while HalfOpen
	lastTripNs   int64 // UnixNano when circuit last opened

	// Config â€” immutable after Alloc().
	failureThreshold int64
	successThreshold int64
	openDurationNs   int64 // how long to stay Open before probing
}

// CircuitBreakerArena is a fixed-size pre-allocated array of CircuitState
// slots indexed by a bake-time integer (0..255).  Using a plain array avoids
// any per-request heap allocation and keeps states cache-line-adjacent.
type CircuitBreakerArena struct {
	states [256]CircuitState
	count  int32 // atomic: number of allocated slots
}

// NewCircuitBreakerArena allocates a ready-to-use arena.
func NewCircuitBreakerArena() *CircuitBreakerArena { return &CircuitBreakerArena{} }

// Count returns the number of circuit breaker slots allocated so far.
// Safe to call concurrently; uses an atomic load.
func (a *CircuitBreakerArena) Count() int {
	return int(atomic.LoadInt32(&a.count))
}

// Alloc reserves the next CircuitState slot, initialises its config, and
// returns the index.  Returns an error when all 256 slots are exhausted.
// Must only be called at bake time (single-threaded compilation path).
func (a *CircuitBreakerArena) Alloc(failureThresh, successThresh int64, openDurationMs uint32) (int, error) {
	idx := int(atomic.AddInt32(&a.count, 1)) - 1
	if idx >= 256 {
		return 0, fmt.Errorf("circuit breaker arena full (max 256)")
	}
	cs := &a.states[idx]
	cs.failureThreshold = failureThresh
	cs.successThreshold = successThresh
	cs.openDurationNs = int64(openDurationMs) * int64(time.Millisecond)
	return idx, nil
}

// OutcomeFunc is the type for a runtime success/failure predicate.
// It is identical in shape to steps.ConditionFunc â€” the compiler casts between
// them without any wrapper to avoid an import cycle (engine â†› steps).
type OutcomeFunc = func(ctx *rctx.Context) bool

// CircuitBreakerGateStep returns an Instruction that checks whether requests
// may proceed according to the circuit breaker state machine.
//
//   - Closed  â†’ pass through.
//   - Open    â†’ if the open window has elapsed, transition to HalfOpen and allow
//     one probe request; otherwise: if fallbackFlowStart >= 0 jump to that flow;
//     else set ctx.ResponseStatus = 503 and stop.
//   - HalfOpen â†’ pass through (one probe at a time; CAS ensures only one thread
//     transitions the state).
//
// fallbackFlowStart is the absolute PC of the fallback flow's first instruction,
// or -1 when no fallback is configured (bare 503 behaviour is preserved).
//
// Use RecordCircuitOutcomeStep after the guarded work to update the state machine.
func CircuitBreakerGateStep(arena *CircuitBreakerArena, idx int, fallbackFlowStart int16) Instruction {
	return Instruction{
		Name: "CIRCUIT_BREAKER_GATE",
		Action: func(ctx *rctx.Context, state *ExecutionState) int16 {
			cs := &arena.states[idx]
			s := atomic.LoadInt32(&cs.state)
			switch s {
			case CBStateClosed:
				return state.PC + 1

			case CBStateOpen:
				now := time.Now().UnixNano()
				lastTrip := atomic.LoadInt64(&cs.lastTripNs)
				if now-lastTrip >= cs.openDurationNs {
					// Attempt to transition to HalfOpen so one probe request gets through.
					if atomic.CompareAndSwapInt32(&cs.state, CBStateOpen, CBStateHalfOpen) {
						atomic.StoreInt64(&cs.successCount, 0)
					}
					return state.PC + 1 // allow the probe (or the winner of the CAS race)
				}
				// Circuit is open and the probe window has not yet elapsed.
				if fallbackFlowStart >= 0 {
					return fallbackFlowStart // jump to the configured fallback flow
				}
				ctx.ResponseStatus = 503
				return StopPlan

			case CBStateHalfOpen:
				return state.PC + 1 // allow probe traffic

			default:
				return state.PC + 1
			}
		},
	}
}

// RecordCircuitOutcomeStep returns an Instruction that records the outcome of the
// guarded work for the circuit breaker state machine.
//
//   - successFn nil  â†’ always record as success.
//   - successFn non-nil â†’ call it; true = success, false = failure.
//
// State transitions:
//
//	Closed + failure count â‰¥ threshold  â†’ Open (reset failure counter, store trip time).
//	Closed + success                    â†’ reset failure counter.
//	HalfOpen + success count â‰¥ threshold â†’ Closed (reset both counters).
//	HalfOpen + failure                  â†’ re-Open (store new trip time).
func RecordCircuitOutcomeStep(arena *CircuitBreakerArena, idx int, successFn OutcomeFunc) Instruction {
	return Instruction{
		Name: "RECORD_CIRCUIT_OUTCOME",
		Action: func(ctx *rctx.Context, state *ExecutionState) int16 {
			cs := &arena.states[idx]
			s := atomic.LoadInt32(&cs.state)
			isSuccess := successFn == nil || successFn(ctx)

			switch s {
			case CBStateClosed:
				if !isSuccess {
					fc := atomic.AddInt64(&cs.failureCount, 1)
					if fc >= cs.failureThreshold {
						if atomic.CompareAndSwapInt32(&cs.state, CBStateClosed, CBStateOpen) {
							atomic.StoreInt64(&cs.lastTripNs, time.Now().UnixNano())
							atomic.StoreInt64(&cs.failureCount, 0)
						}
					}
				} else {
					// Reset consecutive failure count on any success.
					atomic.StoreInt64(&cs.failureCount, 0)
				}

			case CBStateHalfOpen:
				if isSuccess {
					sc := atomic.AddInt64(&cs.successCount, 1)
					if sc >= cs.successThreshold {
						// CRITICAL: set state CLOSED first, then reset counters so
						// concurrent readers never see a partial reset.
						if atomic.CompareAndSwapInt32(&cs.state, CBStateHalfOpen, CBStateClosed) {
							atomic.StoreInt64(&cs.failureCount, 0)
							atomic.StoreInt64(&cs.successCount, 0)
						}
					}
				} else {
					// Probe failed â€” re-open the circuit.
					if atomic.CompareAndSwapInt32(&cs.state, CBStateHalfOpen, CBStateOpen) {
						atomic.StoreInt64(&cs.lastTripNs, time.Now().UnixNano())
						atomic.StoreInt64(&cs.failureCount, 0)
					}
				}
			}
			return state.PC + 1
		},
	}
}
