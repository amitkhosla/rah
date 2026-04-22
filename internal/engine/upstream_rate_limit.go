package engine

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// UpstreamRateWindow defines a single fixed-window rate limit for a named resource.
// EpochDiv converts Unix seconds to epoch buckets: 1=second, 60=minute, 3600=hour, 86400=day.
type UpstreamRateWindow struct {
	EpochDiv uint32
	Limit    uint32
	slot     atomic.Uint64 // packed: [epoch:32 | count:32] — same layout as CounterStore.FixedWindowEpoch
}

// UpstreamRateLimiter is a per-named-resource multi-window rate limiter.
// Keys can be LLM model aliases, HTTP upstream hostnames, or any stable string.
// Up to 4 concurrent windows (second / minute / hour / day) are supported.
// All operations are lock-free; the only contention is on atomic CAS.
type UpstreamRateLimiter struct {
	Windows [4]*UpstreamRateWindow
	Count   int
}

// Allow returns true if the current request is within all configured windows.
// It atomically increments the counter for each active window.
// If any window is exceeded the request is denied and NO counters are incremented
// for subsequent windows (fast-path short-circuit).
func (u *UpstreamRateLimiter) Allow() bool {
	now := uint32(time.Now().Unix())
	for i := 0; i < u.Count; i++ {
		w := u.Windows[i]
		epoch := now / w.EpochDiv
		if !upstreamEpochAllow(&w.slot, epoch, w.Limit) {
			return false
		}
	}
	return true
}

// upstreamEpochAllow is a lock-free fixed-window check on a standalone atomic.Uint64.
// Uses the same epoch|count CAS loop as CounterStore.FixedWindowEpoch.
func upstreamEpochAllow(slot *atomic.Uint64, epoch, limit uint32) bool {
	for {
		old := slot.Load()
		storedEpoch := uint32(old >> 32)

		if storedEpoch != epoch {
			// New time window — reset to [epoch | 1].
			newVal := (uint64(epoch) << 32) | 1
			if slot.CompareAndSwap(old, newVal) {
				return true // first request in this window
			}
			continue // another goroutine won the CAS — retry
		}

		count := uint32(old & 0xFFFFFFFF)
		if count >= limit {
			return false // window exhausted
		}

		if slot.CompareAndSwap(old, old+1) {
			return true
		}
		// CAS lost (concurrent increment) — retry
	}
}

// upstreamLimits is the global registry of per-resource rate limiters.
// Key: resource name (model alias, hostname, etc.) → *UpstreamRateLimiter.
var upstreamLimits sync.Map

// UpstreamWindowFromWindow converts a named window string and limit to an UpstreamRateWindow.
// Supported windows: "second", "minute", "hour", "day". Unknown strings default to "minute".
func UpstreamWindowFromWindow(window string, limit uint32) *UpstreamRateWindow {
	var div uint32
	switch window {
	case "second":
		div = 1
	case "hour":
		div = 3600
	case "day":
		div = 86400
	default: // "minute" and anything unrecognised
		div = 60
	}
	return &UpstreamRateWindow{EpochDiv: div, Limit: limit}
}

// RegisterUpstreamLimit registers a multi-window rate limiter for the given key.
// If a limiter for this key already exists (e.g. from a previous bake), it is kept
// unchanged — the existing counters continue running so we don't drop state on hot reload.
// windows must have at most 4 entries; extra entries are silently ignored.
func RegisterUpstreamLimit(key string, windows []*UpstreamRateWindow) {
	if len(windows) == 0 {
		return
	}
	rl := &UpstreamRateLimiter{}
	for i, w := range windows {
		if i >= 4 {
			break
		}
		rl.Windows[i] = w
		rl.Count++
	}
	upstreamLimits.LoadOrStore(key, rl)
}

// CheckUpstreamLimit returns true if the request is within the rate limits for key.
// Returns true (allowed) when no limiter is registered for the key.
func CheckUpstreamLimit(key string) bool {
	v, ok := upstreamLimits.Load(key)
	if !ok {
		return true // no limit configured
	}
	return v.(*UpstreamRateLimiter).Allow()
}

// windowName converts an EpochDiv back to a human-readable window label.
func windowName(div uint32) string {
	switch div {
	case 1:
		return "second"
	case 3600:
		return "hour"
	case 86400:
		return "day"
	default:
		return "minute"
	}
}

// CheckUpstreamLimitWithDetail is like CheckUpstreamLimit but returns a detail
// string describing which window was exhausted (e.g. "minute:60/60").
// Returns ("", true) when allowed, ("<window>:<count>/<limit>", false) when blocked.
func CheckUpstreamLimitWithDetail(key string) (detail string, allowed bool) {
	v, ok := upstreamLimits.Load(key)
	if !ok {
		return "", true
	}
	u := v.(*UpstreamRateLimiter)
	now := uint32(time.Now().Unix())
	for i := 0; i < u.Count; i++ {
		w := u.Windows[i]
		epoch := now / w.EpochDiv
		old := w.slot.Load()
		storedEpoch := uint32(old >> 32)
		var count uint32
		if storedEpoch == epoch {
			count = uint32(old & 0xFFFFFFFF)
		}
		if count >= w.Limit {
			win := windowName(w.EpochDiv)
			return win + ":" + fmt.Sprintf("%d/%d", count, w.Limit), false
		}
	}
	// All windows within limits — run the real Allow() to increment counters.
	return "", u.Allow()
}
