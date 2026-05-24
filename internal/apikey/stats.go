package apikey

import "sync/atomic"

// Stats holds always-on atomic counters for the validate_api_key step.
// There is a single global instance (Global) — zero lock contention.
type Stats struct {
	Attempts     atomic.Int64
	Hits         atomic.Int64
	Misses       atomic.Int64
	Disabled     atomic.Int64
	TenantDenied atomic.Int64
	// TimingBuckets is a 6-bucket latency histogram (nanoseconds):
	//   [0] <100ns  [1] <250ns  [2] <500ns  [3] <1µs  [4] <2µs  [5] ≥2µs
	TimingBuckets [6]atomic.Int64
}

// Global is the singleton Stats instance updated by the validate_api_key step.
var Global Stats

// RecordTiming increments the appropriate latency bucket for a single
// validate_api_key execution. ns is the elapsed time in nanoseconds.
func (s *Stats) RecordTiming(ns int64) {
	switch {
	case ns < 100:
		s.TimingBuckets[0].Add(1)
	case ns < 250:
		s.TimingBuckets[1].Add(1)
	case ns < 500:
		s.TimingBuckets[2].Add(1)
	case ns < 1_000:
		s.TimingBuckets[3].Add(1)
	case ns < 2_000:
		s.TimingBuckets[4].Add(1)
	default:
		s.TimingBuckets[5].Add(1)
	}
}

// Snapshot returns a JSON-serialisable map of current counters. It is called
// by the observability package to include api_key_stats in the telemetry
// snapshot.
func (s *Stats) Snapshot() map[string]any {
	attempts := s.Attempts.Load()
	hits := s.Hits.Load()

	var hitRatePct float64
	if attempts > 0 {
		hitRatePct = float64(hits) / float64(attempts) * 100
	}

	buckets := map[string]int64{
		"<100ns":  s.TimingBuckets[0].Load(),
		"<250ns":  s.TimingBuckets[1].Load(),
		"<500ns":  s.TimingBuckets[2].Load(),
		"<1us":    s.TimingBuckets[3].Load(),
		"<2us":    s.TimingBuckets[4].Load(),
		">=2us":   s.TimingBuckets[5].Load(),
	}

	return map[string]any{
		"attempts":       attempts,
		"hits":           hits,
		"misses":         s.Misses.Load(),
		"disabled":       s.Disabled.Load(),
		"tenant_denied":  s.TenantDenied.Load(),
		"hit_rate_pct":   hitRatePct,
		"timing_buckets": buckets,
	}
}
