package egress

import (
	"sync/atomic"

	"github.com/amitkhosla/rah/internal/config"
)

const (
	// pidNoMatch is the sentinel pid stored in the negative cache.
	// Means "we already checked; there is no matching rule."
	pidNoMatch uint8 = 255

	// patternIdxExact is stored in the cache for exact/code matches
	// where no pattern index applies.
	patternIdxExact uint8 = 255
)

// EgressManager is the runtime object for egress profile resolution.
// All fields are safe for concurrent reads; Update() is the only writer.
type EgressManager struct {
	rules     atomic.Pointer[RuleSet]     // current immutable rule snapshot
	codeCache atomic.Pointer[egressCache] // service-code â†’ pid cache
	hostCache atomic.Pointer[egressCache] // host string â†’ pid cache
}

// NewEgressManager creates a ready-to-use EgressManager with an empty rule set.
// Resolve() immediately works and returns the Auto default profile.
func NewEgressManager() *EgressManager {
	m := &EgressManager{}

	// Store an empty (no-config) rule set so Resolve never sees nil.
	emptyRS, _ := buildRuleSet(nil)
	m.rules.Store(emptyRS)

	// Pre-allocate caches.
	m.codeCache.Store(newEgressCache(64))
	m.hostCache.Store(newEgressCache(256))

	return m
}

// Resolve is the hot path. Called per-request.
//
// Resolution order:
//  1. If serviceCode != "", check codeCache.
//  2. Miss â†’ binary-search ExactCodes â†’ populate codeCache.
//  3. If host != "", check hostCache.
//  4. Miss â†’ walk Patterns â†’ populate hostCache.
//  5. Default: return &rs.Profiles[0] (Auto).
//
// Never returns nil.
func (m *EgressManager) Resolve(serviceCode, host string) *EgressProfile {
	rs := m.rules.Load() // immutable snapshot — never modified

	// â"€â"€ Code resolution â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	if serviceCode != "" {
		cc := m.codeCache.Load()
		if pid, found := cc.get(serviceCode); found {
			if pid == pidNoMatch {
				// Negative cache hit — skip to host resolution.
				goto hostResolution
			}
			return &rs.Profiles[pid]
		}
		// Cache miss: walk ExactCodes.
		profile := rs.ResolveCode(serviceCode)
		if profile.ID != 0 {
			// Found a real match.
			cc.put(serviceCode, profile.ID, patternIdxExact)
			return profile
		}
		// No match: populate negative cache.
		cc.put(serviceCode, pidNoMatch, patternIdxExact)
	}

hostResolution:
	// â"€â"€ Host / pattern resolution â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	if host != "" {
		hc := m.hostCache.Load()
		if pid, found := hc.get(host); found {
			if pid == pidNoMatch {
				return &rs.Profiles[0]
			}
			return &rs.Profiles[pid]
		}
		// Cache miss: walk Patterns.
		for i := range rs.Patterns {
			p := &rs.Patterns[i]
			if p.re.MatchString(host) {
				hc.put(host, p.profileID, p.patternIdx)
				return &rs.Profiles[p.profileID]
			}
		}
		// No pattern matched: negative cache.
		hc.put(host, pidNoMatch, patternIdxExact)
	}

	// Default: Auto profile.
	return &rs.Profiles[0]
}

// Update replaces the rule snapshot and resets both caches.
// Called from the control plane on config reload (rare, not on the hot path).
func (m *EgressManager) Update(cfg *config.EgressConfig) error {
	newRS, err := buildRuleSet(cfg)
	if err != nil {
		return err
	}

	// Swap rule set atomically.
	m.rules.Store(newRS)

	// Replace caches entirely — the whole rule set changed, so any cached
	// pid values may map to stale profile IDs.
	m.codeCache.Store(newEgressCache(64))
	m.hostCache.Store(newEgressCache(256))

	return nil
}
