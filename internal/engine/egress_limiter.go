package engine

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/rctx"
)

// EgressBehavior controls what the egress_rate_limit step does when the
// per-window call budget is exhausted.
type EgressBehavior int8

const (
	// EgressBehaviorFail returns an HTTP error immediately (default).
	EgressBehaviorFail EgressBehavior = 0
	// EgressBehaviorWait blocks until the current window resets, then retries.
	// All waiting goroutines share one timer — no per-goroutine sleep overhead.
	EgressBehaviorWait EgressBehavior = 1
	// EgressBehaviorFallback jumps to a compiled fallback flow.
	EgressBehaviorFallback EgressBehavior = 2
)

// egressBucket holds the live state for one named resource.
// Hot path: epoch load + atomic Add = ~10 ns, no allocation.
// Cold path (window reset): one CAS + channel close + one time.AfterFunc.
type egressBucket struct {
	epoch atomic.Int64                // which window this counter belongs to (now_ns / window_ns)
	count atomic.Int64                // calls consumed in the current window
	gate  atomic.Pointer[chan struct{}] // broadcast channel; closed when window resets
	mu    sync.Mutex                  // guards gate creation only, not the hot path
}

// countForEpoch atomically claims one call slot in the given epoch.
// If the bucket is in an older epoch it resets the counter and wakes all
// goroutines that were blocked waiting for the window to expire.
func (b *egressBucket) countForEpoch(epoch int64) int64 {
	cur := b.epoch.Load()
	if cur == epoch {
		return b.count.Add(1)
	}
	// We are in a new window. Race to be the resetter.
	if b.epoch.CompareAndSwap(cur, epoch) {
		b.count.Store(1)
		// Wake goroutines that were waiting for the old window to finish.
		if gatePtr := b.gate.Swap(nil); gatePtr != nil {
			close(*gatePtr)
		}
		return 1
	}
	// Another goroutine won the reset race; just increment.
	return b.count.Add(1)
}

// waitChannel returns the shared broadcast channel for the current window,
// creating it and scheduling its closure if this is the first waiter.
// One time.AfterFunc timer is created per (resource, window) pair regardless
// of how many goroutines are waiting — no per-goroutine timer overhead.
func (b *egressBucket) waitChannel(epoch, windowNs int64) <-chan struct{} {
	if gatePtr := b.gate.Load(); gatePtr != nil {
		return *gatePtr
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if gatePtr := b.gate.Load(); gatePtr != nil {
		return *gatePtr
	}
	ch := make(chan struct{})
	b.gate.Store(&ch)

	// Calculate exact ns until the next window boundary.
	nextBoundaryNs := (epoch+1)*windowNs - time.Now().UnixNano()
	if nextBoundaryNs <= 0 {
		nextBoundaryNs = 1 // already at boundary; fire immediately
	}
	// Close the channel when the window expires, waking all waiters.
	// CompareAndSwap ensures we close it only once even if countForEpoch
	// also triggers a wake (e.g. a new request arrives in the next epoch
	// before the timer fires).
	chPtr := &ch
	time.AfterFunc(time.Duration(nextBoundaryNs), func() {
		if b.gate.CompareAndSwap(chPtr, nil) {
			close(ch)
		}
	})

	return ch
}

// EgressLimiterStore holds per-resource egress rate limit state.
// Shared across all concurrent flows for a given FlowManager.
type EgressLimiterStore struct {
	buckets  sync.Map                 // string(resource) → *egressBucket
	remoteRL ExternalRateLimitProvider // nil = in-memory atomic counters
}

// NewEgressLimiterStore allocates a ready-to-use store.
func NewEgressLimiterStore() *EgressLimiterStore { return &EgressLimiterStore{} }

// WireRemoteRL configures a distributed counting backend on the store.
// Called at bake time before any traffic; not safe for concurrent use.
func (s *EgressLimiterStore) WireRemoteRL(rl ExternalRateLimitProvider) { s.remoteRL = rl }

// NewEgressLimiterStoreRedis creates an EgressLimiterStore that uses the given
// ExternalRateLimitProvider for distributed counting across pods. The in-memory
// gate channels for wait-behavior coordination are always process-local.
func NewEgressLimiterStoreRedis(rl ExternalRateLimitProvider) *EgressLimiterStore {
	return &EgressLimiterStore{remoteRL: rl}
}

// acquire tries to consume one call slot in the current window.
// Returns (true, nil) when the slot is granted.
// Returns (false, waitCh) when the budget is exhausted; waitCh is closed
// when the window resets so callers can block on it efficiently.
func (s *EgressLimiterStore) acquire(resource string, limit, windowNs int64) (bool, <-chan struct{}) {
	// Load-first to avoid allocating a new egressBucket on every hot-path call.
	// LoadOrStore is only reached on the first request for a given resource.
	v, ok := s.buckets.Load(resource)
	if !ok {
		v, _ = s.buckets.LoadOrStore(resource, &egressBucket{})
	}
	b := v.(*egressBucket)
	epoch := time.Now().UnixNano() / windowNs

	if s.remoteRL != nil {
		// Distributed path: Redis counter for cross-pod accuracy.
		// Key: egress:{resource}:{epoch} — unique per resource and window.
		windowSec := int(windowNs / int64(time.Second))
		if windowSec < 1 {
			windowSec = 1
		}
		redisKey := "egress:" + resource + ":" + strconv.FormatInt(epoch, 10)
		allowed, _ := s.remoteRL.Check(redisKey, uint32(limit), windowSec)
		if allowed {
			return true, nil
		}
		// Gate channel stays in-memory; time.AfterFunc wakes waiters at window boundary.
		return false, b.waitChannel(epoch, windowNs)
	}

	// In-memory path: CAS-based atomic counter, zero allocation on hot path.
	n := b.countForEpoch(epoch)
	if n <= limit {
		return true, nil
	}
	return false, b.waitChannel(epoch, windowNs)
}

// EgressRateLimitStep returns an Instruction that gates outbound calls to a
// named resource (external REST API, database, LLM provider, Redis cluster,
// etc.) to at most limit calls per window.
//
// Unlike ingress rate limiting (which protects Rah from callers), egress rate
// limiting protects external resources from being overrun by Rah — regardless
// of how many tenant flows or parallel branches are calling simultaneously.
//
// Parameters (all resolved at bake time):
//   - store       — shared EgressLimiterStore (one per FlowManager).
//   - resource    — stable name for the external resource ("breeze_api", "openai", "pg_primary").
//   - limit       — max calls allowed per window across all goroutines.
//   - windowNs    — window size in nanoseconds (pre-computed from "1s"/"1m"/"1h" at bake time).
//   - behavior    — what to do when budget exhausted: wait, fail, or fallback.
//   - fallbackPC  — absolute PC to jump to (behavior==fallback); -1 otherwise.
//   - failStatus  — HTTP status returned when behavior==fail.
//   - maxWaitNs   — upper bound for how long a goroutine waits (behavior==wait).
//
// Waiting goroutines: all share one broadcast channel per window per resource.
// When the window resets, one timer fires and closes the channel, waking all
// blocked goroutines simultaneously. No thundering herd — each goroutine then
// retries acquire() and the first `limit` succeed; the rest wait for the next window.
func EgressRateLimitStep(
	store *EgressLimiterStore,
	resource string,
	limit, windowNs, maxWaitNs int64,
	behavior EgressBehavior,
	fallbackPC int16,
	failStatus int,
) Instruction {
	return Instruction{
		Name: "EGRESS_RATE_LIMIT",
		Action: func(ctx *rctx.Context, state *ExecutionState) int16 {
			// deadline is computed lazily — only on first wait — so the hot path
			// (acquire succeeds immediately) never pays the time.Now() cost.
			var deadline time.Time

			for {
				allowed, waitCh := store.acquire(resource, limit, windowNs)
				if allowed {
					return state.PC + 1
				}

				switch behavior {

				case EgressBehaviorFail:
					ctx.ResponseStatus = failStatus
					return StopPlan

				case EgressBehaviorFallback:
					if fallbackPC >= 0 {
						return fallbackPC
					}
					ctx.ResponseStatus = 503
					return StopPlan

				case EgressBehaviorWait:
					if deadline.IsZero() {
						deadline = time.Now().Add(time.Duration(maxWaitNs))
					}
					remaining := time.Until(deadline)
					if remaining <= 0 {
						// Gave up waiting — surface as 503.
						ctx.ResponseStatus = 503
						return StopPlan
					}

					// Bail before blocking if the client already disconnected.
					if atomic.LoadInt32(&ctx.Cancelled) != 0 {
						return StopCancelled
					}
					if ctx.Request != nil {
						select {
						case <-ctx.Request.Context().Done():
							atomic.StoreInt32(&ctx.Cancelled, 1)
							return StopCancelled
						default:
						}
					}

					// Block until the window resets, deadline, or disconnect.
					// waitCh is shared across all goroutines waiting on this
					// resource+window — one close() wakes them all.
					timer := time.NewTimer(remaining)
					if ctx.Request != nil {
						select {
						case <-waitCh:
							timer.Stop()
						case <-timer.C:
							ctx.ResponseStatus = 503
							return StopPlan
						case <-ctx.Request.Context().Done():
							timer.Stop()
							atomic.StoreInt32(&ctx.Cancelled, 1)
							return StopCancelled
						}
					} else {
						// Scheduled / background flow — no HTTP request context.
						select {
						case <-waitCh:
							timer.Stop()
						case <-timer.C:
							ctx.ResponseStatus = 503
							return StopPlan
						}
					}
					// Window reset — loop back and retry acquire.
				}
			}
		},
	}
}
