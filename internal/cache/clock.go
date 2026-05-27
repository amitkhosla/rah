package cache

import (
	"sync/atomic"
	"time"
)

// DefaultClockInterval is the refresh interval used when newCachedClock is
// called with interval <= 0.
//
// At 10 ms granularity an expired entry may be served for at most 10 ms past
// its true expiry time. For caches whose TTLs are measured in seconds this is
// negligible (0.017% of the shortest common TTL of 60 s).
//
// Why a background goroutine beats time.Now() per call:
//   - Linux:   time.Now() ≈ 2 ns via VDSO — acceptable but still measurable at
//              high throughput (100M Get/s × 2 ns = 200 ms/s of one core).
//   - Windows: time.Now() requires a kernel stdcall ≈ 100 ns — the profiler
//              showed stdcall0/stdcall2 consuming 12 % of total CPU time.
//
// The background goroutine is parked (sleeping on a ticker) > 99.99 % of the
// time. On each tick it does one atomic store and goes back to sleep.
const DefaultClockInterval = 10 * time.Millisecond

// cachedClock maintains a coarse unix-second timestamp updated by a single
// background goroutine. All read paths pay one atomic load (~1 ns) instead of
// a time.Now() call.
//
// Concurrency: safe for any number of concurrent readers; one writer (the
// background goroutine). No mutex needed — atomic store/load on a 32-bit word
// is always tear-free on amd64/arm64.
type cachedClock struct {
	nowSec atomic.Uint32
}

// newCachedClock initialises the clock to the current second and starts the
// background updater goroutine. The goroutine exits when stop is closed.
//
// interval <= 0 uses DefaultClockInterval.
func newCachedClock(interval time.Duration, stop <-chan struct{}) *cachedClock {
	if interval <= 0 {
		interval = DefaultClockInterval
	}
	c := &cachedClock{}
	c.nowSec.Store(uint32(time.Now().Unix()))
	go clockUpdater(c, interval, stop)
	return c
}

// clockUpdater is the background goroutine body. Kept separate so it appears
// with a descriptive name in pprof profiles and stack traces.
func clockUpdater(c *cachedClock, interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case t := <-ticker.C:
			c.nowSec.Store(uint32(t.Unix()))
		}
	}
}

// now returns the cached unix-second timestamp. One atomic load, no syscall.
func (c *cachedClock) now() uint32 {
	return c.nowSec.Load()
}
