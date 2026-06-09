package engine

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"rah/internal/config"
)

// nBuckets is the number of latency histogram buckets.
//
// Layout (gateway overhead in ms):
//   0– 9 ms : 1 ms steps  → 10 buckets (indices  0– 9)
//  10–99 ms : 10 ms steps →  9 buckets (indices 10–18)
// 100–999 ms: 100 ms steps →  9 buckets (indices 19–27)
//    1000 ms+: overflow    →  1 bucket  (index   28)
const nBuckets = 29

// latencyHist is one generation of the rolling histogram.
// All fields are updated via atomics — no mutex on the hot path.
type latencyHist struct {
	buckets [nBuckets]atomic.Int64
	count   atomic.Int64
}

func (h *latencyHist) record(ms int64) {
	h.count.Add(1)
	h.buckets[bucketFor(ms)].Add(1)
}

// p99Ms returns the p99 latency in milliseconds from this generation's counts.
// Returns 0 if no samples have been recorded.
func (h *latencyHist) p99Ms() int64 {
	total := h.count.Load()
	if total == 0 {
		return 0
	}
	threshold := total - total/100 // cumulative count that reaches p99
	var cumulative int64
	for i := range h.buckets {
		cumulative += h.buckets[i].Load()
		if cumulative >= threshold {
			return bucketUpperMs(i)
		}
	}
	return 9999
}

func (h *latencyHist) reset() {
	for i := range h.buckets {
		h.buckets[i].Store(0)
	}
	h.count.Store(0)
}

// bucketFor maps a latency in ms to a bucket index.
func bucketFor(ms int64) int {
	if ms < 0 {
		return 0
	}
	if ms < 10 {
		return int(ms) // indices 0–9
	}
	if ms < 100 {
		return 10 + int((ms-10)/10) // indices 10–18
	}
	if ms < 1000 {
		return 19 + int((ms-100)/100) // indices 19–27
	}
	return 28 // overflow
}

// bucketUpperMs returns the upper bound in ms for bucket i.
func bucketUpperMs(i int) int64 {
	switch {
	case i < 10:
		return int64(i + 1)
	case i < 19:
		return int64(10 + (i-10+1)*10)
	case i < 28:
		return int64(100 + (i-19+1)*100)
	default:
		return 9999
	}
}

// LatencyRing is a two-generation rolling latency estimator.
//
// One generation is the current write target for request goroutines.
// The controller calls Rotate() each tick to freeze the current generation
// and start a fresh one, then reads p99 from the frozen generation.
//
// Hot path (Record): one atomic load + one atomic add — ~10 ns, no lock.
// Controller path (Rotate + p99Ms): 30 atomic stores + 29 atomic loads — ~1 µs, once per tick.
type LatencyRing struct {
	gens    [2]latencyHist
	current atomic.Uint32 // index of the generation currently being written to (0 or 1)
}

// Record adds one gateway-overhead sample in milliseconds.
// Called post-response on the request goroutine — never blocks.
func (r *LatencyRing) Record(ms int64) {
	r.gens[r.current.Load()].record(ms)
}

// Rotate freezes the current generation and starts a fresh one.
// Returns the frozen generation so the controller can read its p99.
// Must only be called by the controller goroutine (single writer).
func (r *LatencyRing) Rotate() *latencyHist {
	prev := r.current.Load()
	next := 1 - prev
	r.gens[next].reset()  // zero the incoming generation before exposing it
	r.current.Store(next) // new Record() calls now go to next; prev is frozen
	return &r.gens[prev]
}

// ConcurrencyLimiter is a lock-free semaphore with an atomically adjustable limit.
//
// TryAcquire: CAS loop, ~10–20 ns, no allocation, no blocking.
// Release:    single atomic add, ~5 ns.
// SetLimit:   single atomic store, visible to all goroutines immediately.
type ConcurrencyLimiter struct {
	active   atomic.Int64
	limit    atomic.Int64
	rejected atomic.Int64 // cumulative rejections since startup
}

// TryAcquire increments active and returns true if active < limit.
// Returns false immediately without blocking when at capacity.
func (l *ConcurrencyLimiter) TryAcquire() bool {
	for {
		cur := l.active.Load()
		if cur >= l.limit.Load() {
			l.rejected.Add(1)
			return false
		}
		if l.active.CompareAndSwap(cur, cur+1) {
			return true
		}
		// another goroutine won the CAS — retry with the updated value
	}
}

// Release decrements active. Must be called exactly once per successful TryAcquire.
func (l *ConcurrencyLimiter) Release() { l.active.Add(-1) }

// SetLimit updates the concurrency cap atomically. Safe to call from any goroutine.
func (l *ConcurrencyLimiter) SetLimit(n int64) { l.limit.Store(n) }

// Active returns the number of currently in-flight requests.
func (l *ConcurrencyLimiter) Active() int64 { return l.active.Load() }

// Limit returns the current concurrency cap.
func (l *ConcurrencyLimiter) Limit() int64 { return l.limit.Load() }

// Rejected returns the cumulative number of requests rejected since startup.
func (l *ConcurrencyLimiter) Rejected() int64 { return l.rejected.Load() }

// StartController initialises the limiter and, unless cfg.Disabled is true,
// starts the AIMD adaptive loop as a background goroutine.
// Call once from main after NewFlowManager; pass the gateway context so the
// controller stops cleanly on shutdown.
func (fm *FlowManager) StartController(ctx context.Context, cfg config.ConcurrencyConfig) {
	// Fill in zero-value fields with GOMAXPROCS-derived defaults.
	procs := int64(runtime.GOMAXPROCS(0))
	if cfg.TargetOverheadMs <= 0 {
		cfg.TargetOverheadMs = 50
	}
	if cfg.InitialLimit <= 0 {
		cfg.InitialLimit = procs * 1000
	}
	if cfg.MinLimit <= 0 {
		// Floor is set high enough that the controller never causes 429s under
		// normal load. At 1s upstream delay, 1000 concurrent = 1000 RPS — well
		// above what most gateways see as "low traffic."
		cfg.MinLimit = procs * 500
	}
	if cfg.MaxLimit <= 0 {
		cfg.MaxLimit = procs * 4000
	}
	if cfg.AddStep <= 0 {
		cfg.AddStep = 50
	}
	if cfg.CutFactor <= 0 {
		cfg.CutFactor = 0.85
	}
	if cfg.TickSec <= 0 {
		cfg.TickSec = 2
	}
	if cfg.MinSamples <= 0 {
		cfg.MinSamples = 50
	}
	if cfg.CooldownTicks <= 0 {
		cfg.CooldownTicks = 3
	}

	fm.Limiter.SetLimit(cfg.InitialLimit)

	if !cfg.Disabled {
		go fm.runConcurrencyController(ctx, cfg)
	}
}

// runConcurrencyController is the AIMD adaptive loop.
// Runs as a background goroutine; exits when ctx is cancelled.
//
// Each tick it:
//  1. Rotates the LatencyRing to get a frozen generation.
//  2. Computes p99 gateway overhead from that generation.
//  3. Adjusts the concurrency limit using AIMD rules.
//
// AIMD rules:
//   - Cut  (×CutFactor) when p99 > target AND utilisation > 60%.
//   - Grow (+AddStep)   when p99 ≤ target AND utilisation > 80%.
//   - Hold             in all other cases (low traffic, low samples, cooldown).
func (fm *FlowManager) runConcurrencyController(ctx context.Context, cfg config.ConcurrencyConfig) {
	ticker := time.NewTicker(time.Duration(cfg.TickSec) * time.Second)
	defer ticker.Stop()

	cooldown := 0 // ticks remaining in cut cooldown

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frozen := fm.LatencyRing.Rotate()
			samples := frozen.count.Load()

			if cooldown > 0 {
				cooldown--
			}

			// Don't act on statistically meaningless windows.
			if samples < cfg.MinSamples {
				continue
			}

			p99 := frozen.p99Ms()
			cur := fm.Limiter.limit.Load()
			active := fm.Limiter.active.Load()
			utilization := float64(active) / float64(cur)

			switch {
			case p99 > cfg.TargetOverheadMs && utilization > 0.6 && cooldown == 0:
				// Distressed and the limit is the likely cause — cut multiplicatively.
				next := int64(float64(cur) * cfg.CutFactor)
				if next < cfg.MinLimit {
					next = cfg.MinLimit
				}
				if next < active {
					next = active // never cut below currently in-flight requests
				}
				fm.Limiter.SetLimit(next)
				cooldown = cfg.CooldownTicks

			case p99 <= cfg.TargetOverheadMs && utilization > 0.8:
				// Healthy and we're actually using the capacity — probe higher.
				next := cur + cfg.AddStep
				if next > cfg.MaxLimit {
					next = cfg.MaxLimit
				}
				fm.Limiter.SetLimit(next)
			}
		}
	}
}

// ConcurrencyHandler handles the /admin/concurrency management endpoint.
//
//	GET  — returns current limit, active count, and cumulative rejections.
//	POST ?limit=N — overrides the limit; the adaptive controller continues
//	               running and will adjust from the new value on the next tick.
func (fm *FlowManager) ConcurrencyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var n int64
		if _, err := fmt.Sscan(r.URL.Query().Get("limit"), &n); err != nil || n <= 0 {
			http.Error(w, `{"error":"limit must be a positive integer"}`, http.StatusBadRequest)
			return
		}
		fm.Limiter.SetLimit(n)
	}
	fmt.Fprintf(w, `{"limit":%d,"active":%d,"rejected":%d}`,
		fm.Limiter.Limit(), fm.Limiter.Active(), fm.Limiter.Rejected())
}

