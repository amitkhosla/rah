package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

// UpstreamCostWindow tracks accumulated USD cost in a fixed time window using
// a lock-free CAS loop. Packs [epoch:32 | cost_microUSD:32] in one atomic.Uint64.
// Maximum representable limit is ~$4295/window (uint32 max / 1e6).
type UpstreamCostWindow struct {
	epochDiv   uint32        // seconds per window: 1=second, 60=minute, 3600=hour, 86400=day
	limitMicro uint32        // spending limit in micro-USD (limitUSD * 1e6)
	slot       atomic.Uint64 // packed: [epoch:32 | accumulated_cost_micro:32]
}

// UpstreamCostLimiter holds up to 4 UpstreamCostWindows for one model,
// matching the capacity of UpstreamRateLimiter.
type UpstreamCostLimiter struct {
	Count   int
	Windows [4]*UpstreamCostWindow
}

// UpstreamCostWindowFromWindow converts a named window string and USD limit
// to an UpstreamCostWindow. Supported windows: "second", "minute", "hour", "day".
// Unknown window strings return nil (caller must check).
func UpstreamCostWindowFromWindow(window string, limitUSD float64) *UpstreamCostWindow {
	var div uint32
	switch window {
	case "second":
		div = 1
	case "minute":
		div = 60
	case "hour":
		div = 3600
	case "day":
		div = 86400
	default:
		return nil
	}
	return &UpstreamCostWindow{
		epochDiv:   div,
		limitMicro: uint32(limitUSD * 1e6),
	}
}

// Record atomically records a cost entry and returns true if the accumulated
// cost in the current window exceeds the limit. It is the hot path and performs
// zero allocations. The CAS loop follows the same pattern as upstreamEpochAllow.
func (w *UpstreamCostWindow) Record(costUSD float64) bool {
	now := uint32(time.Now().Unix()) / w.epochDiv
	costMicro := uint32(costUSD * 1e6)

	for {
		old := w.slot.Load()
		storedEpoch := uint32(old >> 32)

		var newVal uint64
		if storedEpoch != now {
			// New time window — reset to [now | costMicro].
			newVal = (uint64(now) << 32) | uint64(costMicro)
		} else {
			// Same epoch — accumulate cost.
			accumulatedCost := uint32(old & 0xFFFFFFFF)
			newCost := accumulatedCost + costMicro
			newVal = (uint64(now) << 32) | uint64(newCost)
		}

		if w.slot.CompareAndSwap(old, newVal) {
			// CAS succeeded — check if limit exceeded.
			return uint32(newVal&0xFFFFFFFF) > w.limitMicro
		}
		// CAS lost (concurrent update) — retry
	}
}

// Record records a cost against all windows in the limiter and returns true
// if any window's limit is exceeded. It records to all windows even if one
// exceeds, ensuring all windows are updated.
func (l *UpstreamCostLimiter) Record(costUSD float64) bool {
	limitExceeded := false
	for i := 0; i < l.Count; i++ {
		w := l.Windows[i]
		if w != nil && w.Record(costUSD) {
			limitExceeded = true
		}
	}
	return limitExceeded
}

// upstreamCostLimiters is the global registry of per-model cost limiters.
// Key: model alias → *UpstreamCostLimiter.
var upstreamCostLimiters sync.Map

// RegisterModelCostLimit registers a multi-window cost limiter for the given
// model alias. If a limiter for this key already exists, it is kept unchanged.
// windows slice is copied into the [4]*UpstreamCostWindow array (up to 4 entries);
// extra entries are silently ignored.
func RegisterModelCostLimit(alias string, windows []*UpstreamCostWindow) {
	if len(windows) == 0 {
		return
	}
	limiter := &UpstreamCostLimiter{}
	for i, w := range windows {
		if i >= 4 {
			break
		}
		limiter.Windows[i] = w
		limiter.Count++
	}
	upstreamCostLimiters.LoadOrStore(alias, limiter)
}

// GetModelCostLimiter retrieves the cost limiter for a model alias.
// Returns nil if not registered (zero allocation on miss).
func GetModelCostLimiter(alias string) *UpstreamCostLimiter {
	v, ok := upstreamCostLimiters.Load(alias)
	if !ok {
		return nil
	}
	return v.(*UpstreamCostLimiter)
}
