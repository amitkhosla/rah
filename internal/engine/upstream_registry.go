package engine

import (
	"sort"
	"strings"
	"sync/atomic"

	"github.com/amitkhosla/rah/internal/registry"
)

// unmatchedPolicy controls behaviour when no URL pattern matches.
type unmatchedPolicy uint8

const (
	policyFailOpen   unmatchedPolicy = 0 // no config â€” allow unlimited
	policyFailClosed unmatchedPolicy = 1 // deny the request
	policyDefault    unmatchedPolicy = 2 // apply defaultID config
)

// upstreamEntry is one compiled pattern entry.
type upstreamEntry struct {
	prefix   string // the pattern prefix to match against (wildcard suffix stripped)
	exact    bool   // true if the original pattern had no wildcard suffix
	configID uint16
}

// UpstreamRegistry maps URL patterns to configIDs.
// Patterns are matched longest-prefix-first (most specific wins).
// The registry is immutable after construction; swapped atomically on update.
type UpstreamRegistry struct {
	entries   []upstreamEntry // sorted by len(prefix) descending (longest first)
	unmatched unmatchedPolicy
	defaultID uint16 // used when unmatched == policyDefault
}

// NewUpstreamRegistry builds a registry from a list of (pattern, configNameâ†’configID) pairs.
//
// unmatchedStr must be one of:
//
//	"fail_open"      â€” allow all unmatched URLs with no rate limiting (configID=0)
//	"fail_closed"    â€” deny all unmatched URLs
//	"default_config" â€” apply defaultConfigID to all unmatched URLs
//
// defaultConfigID is ignored unless unmatchedStr == "default_config".
func NewUpstreamRegistry(
	patterns []registry.UpstreamPattern,
	configNameToID func(name string) (uint16, bool),
	unmatchedStr string,
	defaultConfigID uint16,
) *UpstreamRegistry {
	r := &UpstreamRegistry{
		defaultID: defaultConfigID,
	}

	switch unmatchedStr {
	case "fail_closed":
		r.unmatched = policyFailClosed
	case "default_config":
		r.unmatched = policyDefault
	default: // "fail_open" and anything unrecognised
		r.unmatched = policyFailOpen
	}

	for _, p := range patterns {
		id, ok := configNameToID(p.ConfigName)
		if !ok {
			// Unknown config name â€” skip silently; the caller is responsible for
			// ensuring config names are valid before building the registry.
			continue
		}

		prefix := p.Pattern
		exact := true
		if strings.HasSuffix(prefix, "*") {
			prefix = prefix[:len(prefix)-1] // strip the trailing '*'
			exact = false
		}

		r.entries = append(r.entries, upstreamEntry{
			prefix:   prefix,
			exact:    exact,
			configID: id,
		})
	}

	// Sort longest prefix first so the first match is always the most specific.
	// For equal-length prefixes, exact matches sort before prefix matches so that
	// a full-URL exact pattern beats a same-length wildcard prefix.
	sort.SliceStable(r.entries, func(i, j int) bool {
		li, lj := len(r.entries[i].prefix), len(r.entries[j].prefix)
		if li != lj {
			return li > lj // longer prefix first
		}
		// Same length: exact beats prefix
		if r.entries[i].exact != r.entries[j].exact {
			return r.entries[i].exact // exact=true sorts earlier
		}
		return false
	})

	return r
}

// Match returns the configID for the given URL.
// The second return value is true when a matching pattern was found.
func (r *UpstreamRegistry) Match(url string) (configID uint16, found bool) {
	for i := range r.entries {
		e := &r.entries[i]
		if e.exact {
			if url == e.prefix {
				return e.configID, true
			}
		} else {
			if strings.HasPrefix(url, e.prefix) {
				return e.configID, true
			}
		}
	}
	return 0, false
}

// MatchWithPolicy applies the unmatched policy when no pattern matches.
//
// Return values:
//
//	configID â€” the rate-limit config to apply (0 means no limiting)
//	allow    â€” false means deny the request immediately (fail_closed)
func (r *UpstreamRegistry) MatchWithPolicy(url string) (configID uint16, allow bool) {
	id, found := r.Match(url)
	if found {
		return id, true
	}

	switch r.unmatched {
	case policyFailClosed:
		return 0, false
	case policyDefault:
		return r.defaultID, true
	default: // policyFailOpen
		return 0, true
	}
}

// â”€â”€ Global atomic state â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

var globalUpstreamRegistry atomic.Pointer[UpstreamRegistry]

// ActiveUpstreamRegistry returns the currently installed registry.
// Always returns a non-nil value; before InstallUpstreamRegistry is called it
// returns an empty fail-open registry.
func ActiveUpstreamRegistry() *UpstreamRegistry {
	r := globalUpstreamRegistry.Load()
	if r == nil {
		return emptyUpstreamRegistry
	}
	return r
}

// InstallUpstreamRegistry atomically replaces the global registry.
func InstallUpstreamRegistry(r *UpstreamRegistry) {
	globalUpstreamRegistry.Store(r)
}

// emptyUpstreamRegistry is the sentinel returned before any registry is installed.
var emptyUpstreamRegistry = &UpstreamRegistry{unmatched: policyFailOpen}
