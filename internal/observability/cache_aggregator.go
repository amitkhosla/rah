package observability

import (
	"sync"

	"github.com/amitkhosla/rah/internal/ingest"
)

// cacheAggregator accumulates cache hit/miss events from the ingest pipeline
// into per-instruction-name statistics (hits, misses, latency).
// Thread-safe via internal mutex.
type cacheAggregator struct {
	mu    sync.Mutex
	stats map[string]*cacheCounter // name -> {hits, misses, hitNs, missNs}
}

// newCacheAggregator creates a new cache event aggregator.
func newCacheAggregator() *cacheAggregator {
	return &cacheAggregator{
		stats: make(map[string]*cacheCounter),
	}
}

// recordHit increments hit count and cumulative latency for the given instruction name.
func (ca *cacheAggregator) recordHit(name string, durationNs int64) {
	ca.mu.Lock()
	c := ca.stats[name]
	if c == nil {
		c = &cacheCounter{}
		ca.stats[name] = c
	}
	c.hits++
	c.hitNs += uint64(durationNs)
	ca.mu.Unlock()
}

// recordMiss increments miss count and cumulative latency for the given instruction name.
func (ca *cacheAggregator) recordMiss(name string, durationNs int64) {
	ca.mu.Lock()
	c := ca.stats[name]
	if c == nil {
		c = &cacheCounter{}
		ca.stats[name] = c
	}
	c.misses++
	c.missNs += uint64(durationNs)
	ca.mu.Unlock()
}

// snapshot returns a copy of aggregated cache stats and clears the aggregator.
// Called periodically by Snapshot() to populate GatewayMetrics.CacheStats.
func (ca *cacheAggregator) snapshot() map[string]*cacheCounter {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if len(ca.stats) == 0 {
		return nil
	}
	out := ca.stats
	ca.stats = make(map[string]*cacheCounter)
	return out
}

// handleCacheEvent processes a single cache event from the ingest pipeline.
// Extracts the instruction name and duration from Model and DurationNs fields.
func (ca *cacheAggregator) handleCacheEvent(e ingest.Event) {
	// Event.Model contains the instruction name (e.g., "cache_get", "cache_get_global")
	// Event.DurationNs contains the store-call latency
	if e.Model == "" || e.DurationNs <= 0 {
		return
	}

	switch e.Kind {
	case ingest.KindCacheHit:
		ca.recordHit(e.Model, e.DurationNs)
	case ingest.KindCacheMiss:
		ca.recordMiss(e.Model, e.DurationNs)
	}
}
