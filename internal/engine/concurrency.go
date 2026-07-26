package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/config"
)

// nBuckets is the number of latency histogram buckets.
//
// Layout (gateway overhead in ms):
//   0â€“ 9 ms : 1 ms steps  â†’ 10 buckets (indices  0â€“ 9)
//  10â€“99 ms : 10 ms steps â†’  9 buckets (indices 10â€“18)
// 100â€“999 ms: 100 ms steps â†’  9 buckets (indices 19â€“27)
//    1000 ms+: overflow    â†’  1 bucket  (index   28)
const nBuckets = 29

// latencyHist is one generation of the rolling histogram.
// All fields are updated via atomics â€” no mutex on the hot path.
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
		return int(ms) // indices 0â€“9
	}
	if ms < 100 {
		return 10 + int((ms-10)/10) // indices 10â€“18
	}
	if ms < 1000 {
		return 19 + int((ms-100)/100) // indices 19â€“27
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
// Hot path (Record): one atomic load + one atomic add â€” ~10 ns, no lock.
// Controller path (Rotate + p99Ms): 30 atomic stores + 29 atomic loads â€” ~1 Âµs, once per tick.
type LatencyRing struct {
	gens    [2]latencyHist
	current atomic.Uint32 // index of the generation currently being written to (0 or 1)
}

// Record adds one gateway-overhead sample in milliseconds.
// Called post-response on the request goroutine â€” never blocks.
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
// TryAcquire: CAS loop, ~10â€“20 ns, no allocation, no blocking.
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
		// another goroutine won the CAS â€” retry with the updated value
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

// StartController initialises the limiter and, if cfg.Enabled is true,
// starts the AIMD adaptive loop as a background goroutine.
// Call once from main after NewFlowManager; pass the gateway context so the
// controller stops cleanly on shutdown.
func (fm *FlowManager) StartController(ctx context.Context, cfg config.ConcurrencyConfig) {
	// Always store a typed zero so liveConfig.Load() is always type-safe,
	// even if the feature is disabled and the AIMD goroutine never starts.
	fm.liveConfig.Store(cfg)

	if !cfg.Enabled {
		fm.limiterEnabled.Store(false)
		log.Printf("[concurrency] disabled â€” gate inactive, no 429s")
		return
	}

	// Fill zero-value fields with GOMAXPROCS-derived defaults.
	procs := int64(runtime.GOMAXPROCS(0))
	if cfg.TargetOverheadMs <= 0 {
		cfg.TargetOverheadMs = 50
	}
	if cfg.InitialLimit <= 0 {
		cfg.InitialLimit = procs * 1000
	}
	if cfg.MinLimit <= 0 {
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

	// Store filled config before enabling the gate â€” goroutine reads liveConfig.
	fm.liveConfig.Store(cfg)
	fm.Limiter.SetLimit(cfg.InitialLimit)
	fm.limiterEnabled.Store(true) // gate is now live; must be last

	go fm.runConcurrencyController(ctx)
	log.Printf("[concurrency] enabled limit=%d adaptive=%v target_ms=%d",
		cfg.InitialLimit, !cfg.Disabled, cfg.TargetOverheadMs)
}

// runConcurrencyController is the AIMD adaptive loop.
// Runs as a background goroutine; exits when ctx is cancelled.
//
// Each tick it reads liveConfig (enabling runtime PATCH updates) and checks
// limiterEnabled to idle when disabled at runtime.
//
// AIMD rules:
//   - Cut  (Ã—CutFactor) when p99 > target AND utilisation > 60%.
//   - Grow (+AddStep)   when p99 â‰¤ target AND utilisation > 80%.
//   - Hold             in all other cases (low traffic, low samples, cooldown).
func (fm *FlowManager) runConcurrencyController(ctx context.Context) {
	initCfg := fm.liveConfig.Load().(config.ConcurrencyConfig)
	ticker := time.NewTicker(time.Duration(initCfg.TickSec) * time.Second)
	defer ticker.Stop()

	cooldown := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg := fm.liveConfig.Load().(config.ConcurrencyConfig)

			// Gate disabled at runtime â€” drain ring to prevent stale samples,
			// then idle. No limit adjustment.
			if !fm.limiterEnabled.Load() {
				fm.LatencyRing.Rotate()
				cooldown = 0
				continue
			}

			// Fixed-limit mode â€” gate is active but AIMD is off.
			if cfg.Disabled {
				fm.LatencyRing.Rotate()
				continue
			}

			frozen := fm.LatencyRing.Rotate()
			samples := frozen.count.Load()

			if cooldown > 0 {
				cooldown--
			}

			if samples < cfg.MinSamples {
				continue
			}

			p99 := frozen.p99Ms()
			cur := fm.Limiter.limit.Load()
			active := fm.Limiter.active.Load()
			utilization := float64(active) / float64(cur)

			switch {
			case p99 > cfg.TargetOverheadMs && utilization > 0.6 && cooldown == 0:
				next := int64(float64(cur) * cfg.CutFactor)
				if next < cfg.MinLimit {
					next = cfg.MinLimit
				}
				if next < active {
					next = active
				}
				fm.Limiter.SetLimit(next)
				cooldown = cfg.CooldownTicks

			case p99 <= cfg.TargetOverheadMs && utilization > 0.8:
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
//	GET         â€” returns extended JSON: enabled, limit, active, rejected, adaptive params.
//	POST ?limit=N â€” legacy manual limit override.
//	PATCH       â€” JSON body runtime update of any concurrency config fields.
func (fm *FlowManager) ConcurrencyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodPatch:
		var patch struct {
			Enabled          *bool    `json:"enabled"`
			Disabled         *bool    `json:"disabled"`
			Limit            *int64   `json:"limit"`
			TargetOverheadMs *int64   `json:"target_overhead_ms"`
			MinLimit         *int64   `json:"min_limit"`
			MaxLimit         *int64   `json:"max_limit"`
			AddStep          *int64   `json:"add_step"`
			CutFactor        *float64 `json:"cut_factor"`
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		cur := fm.liveConfig.Load().(config.ConcurrencyConfig)
		if patch.Enabled != nil {
			cur.Enabled = *patch.Enabled
		}
		if patch.Disabled != nil {
			cur.Disabled = *patch.Disabled
		}
		if patch.TargetOverheadMs != nil {
			cur.TargetOverheadMs = *patch.TargetOverheadMs
		}
		if patch.MinLimit != nil {
			cur.MinLimit = *patch.MinLimit
		}
		if patch.MaxLimit != nil {
			cur.MaxLimit = *patch.MaxLimit
		}
		if patch.AddStep != nil {
			cur.AddStep = *patch.AddStep
		}
		if patch.CutFactor != nil {
			cur.CutFactor = *patch.CutFactor
		}
		// Store updated config before toggling the gate.
		fm.liveConfig.Store(cur)
		fm.limiterEnabled.Store(cur.Enabled)
		if patch.Limit != nil && *patch.Limit > 0 {
			fm.Limiter.SetLimit(*patch.Limit)
		}

	case http.MethodPost:
		// Legacy: POST ?limit=N manual override.
		var n int64
		if _, err := fmt.Sscan(r.URL.Query().Get("limit"), &n); err != nil || n <= 0 {
			http.Error(w, `{"error":"limit must be a positive integer"}`, http.StatusBadRequest)
			return
		}
		fm.Limiter.SetLimit(n)
	}

	cfg := fm.liveConfig.Load().(config.ConcurrencyConfig)
	if _, err := fmt.Fprintf(w,
		`{"enabled":%v,"limit":%d,"active":%d,"rejected":%d,"adaptive":%v,`+
			`"target_overhead_ms":%d,"min_limit":%d,"max_limit":%d,"add_step":%d,"cut_factor":%.3f}`,
		fm.limiterEnabled.Load(),
		fm.Limiter.Limit(), fm.Limiter.Active(), fm.Limiter.Rejected(),
		!cfg.Disabled,
		cfg.TargetOverheadMs, cfg.MinLimit, cfg.MaxLimit, cfg.AddStep, cfg.CutFactor,
	); err != nil {
		log.Printf("[concurrency] handler write error: %v", err)
	}
}

