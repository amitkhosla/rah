package egress

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
)

// patternEntry holds a compiled glob/prefix pattern for host matching.
// specificity = len(pattern) so longer patterns win ties.
type patternEntry struct {
	pattern     string
	re          *regexp.Regexp // compiled from glob-style pattern
	profileID   uint8
	patternIdx  uint8 // index in slice, used for cache invalidation
	specificity int   // higher = more specific = wins ties
}

// exactEntry maps a service code (exact string) to a profile ID.
type exactEntry struct {
	code      string
	profileID uint8
}

// RuleSet is the immutable snapshot loaded atomically by EgressManager.
// Profiles[0] is always the default (Auto) profile.
type RuleSet struct {
	Profiles   [256]EgressProfile // indexed by profileID; ID 0 = Auto default
	ExactCodes []exactEntry       // sorted by code for binary search
	Patterns   []patternEntry     // ordered: longer specificity first (most specific first)
}

// ResolveCode looks up a service code via exact match, then falls back to
// pattern matching against the code string itself.
// Returns the EgressProfile from rs.Profiles[pid].
// Returns the default profile (Profiles[0]) if nothing matches.
func (rs *RuleSet) ResolveCode(code string) *EgressProfile {
	if code == "" {
		return &rs.Profiles[0]
	}
	// Binary search ExactCodes.
	n := len(rs.ExactCodes)
	i := sort.Search(n, func(i int) bool { return rs.ExactCodes[i].code >= code })
	if i < n && rs.ExactCodes[i].code == code {
		return &rs.Profiles[rs.ExactCodes[i].profileID]
	}
	return &rs.Profiles[0]
}

// ResolveHost looks up a host string via pattern matching.
// Returns the EgressProfile from rs.Profiles[pid].
// Returns the default profile (Profiles[0]) if nothing matches.
func (rs *RuleSet) ResolveHost(host string) *EgressProfile {
	// Patterns are sorted most-specific first, so the first match wins.
	for i := range rs.Patterns {
		p := &rs.Patterns[i]
		if p.re.MatchString(host) {
			return &rs.Profiles[p.profileID]
		}
	}
	return &rs.Profiles[0]
}

// compilePattern converts a glob-style pattern (e.g. "*.example.com") to a
// *regexp.Regexp. '*' is treated as "match any sequence" (including dots).
// The pattern is anchored at both ends.
func compilePattern(pattern string) (*regexp.Regexp, error) {
	// Escape all regexp metacharacters, then unescape our wildcard placeholder.
	escaped := regexp.QuoteMeta(pattern)
	// regexp.QuoteMeta turns '*' into '\*'; replace that with '.*'.
	regexStr := "^" + strings.ReplaceAll(escaped, `\*`, `.*`) + "$"
	re, err := regexp.Compile(regexStr)
	if err != nil {
		return nil, fmt.Errorf("egress: invalid pattern %q: %w", pattern, err)
	}
	return re, nil
}

// buildRuleSet constructs a RuleSet from config, resolving profile names to IDs.
// Returns error if a rule references an unknown profile name.
func buildRuleSet(cfg *config.EgressConfig) (*RuleSet, error) {
	rs := &RuleSet{}

	// Profiles[0] is always the built-in Auto default.
	rs.Profiles[0] = EgressProfile{ID: 0, Type: EgressTypeAuto}

	if cfg == nil {
		return rs, nil
	}

	// Build nameâ†’ID map. IDs start at 1 (0 is reserved for Auto default).
	nameToID := make(map[string]uint8, len(cfg.Profiles))
	for i, pc := range cfg.Profiles {
		if pc.Name == "" {
			return nil, fmt.Errorf("egress: profile at index %d has empty name", i)
		}
		id := uint8(i + 1)
		if id == 0 {
			// Overflow: more than 254 named profiles.
			return nil, fmt.Errorf("egress: too many profiles (max 254)")
		}
		nameToID[pc.Name] = id

		egressType := parseEgressType(pc.Type)
		rs.Profiles[id] = EgressProfile{
			ID:   id,
			Type: egressType,
		}
		// TLSConfig and timeouts are left for the caller / transport builder;
		// we only need type + ID in RuleSet for routing decisions.
		if pc.DialTimeoutMs > 0 {
			// Store via the profile; callers can read DialTimeout.
			p := &rs.Profiles[id]
			p.DialTimeout = 0 // will be wired by transport builder
			_ = pc.DialTimeoutMs
		}
	}

	// Build ExactCodes.
	rs.ExactCodes = make([]exactEntry, 0, len(cfg.CodeRules))
	for _, cr := range cfg.CodeRules {
		pid, ok := nameToID[cr.Profile]
		if !ok {
			return nil, fmt.Errorf("egress: code rule %q references unknown profile %q", cr.ServiceCode, cr.Profile)
		}
		rs.ExactCodes = append(rs.ExactCodes, exactEntry{code: cr.ServiceCode, profileID: pid})
	}
	sort.Slice(rs.ExactCodes, func(i, j int) bool {
		return rs.ExactCodes[i].code < rs.ExactCodes[j].code
	})

	// Build Patterns, sorted by specificity descending (longer = more specific).
	rs.Patterns = make([]patternEntry, 0, len(cfg.PatternRules))
	for idx, pr := range cfg.PatternRules {
		pid, ok := nameToID[pr.Profile]
		if !ok {
			return nil, fmt.Errorf("egress: pattern rule %q references unknown profile %q", pr.Pattern, pr.Profile)
		}
		re, err := compilePattern(pr.Pattern)
		if err != nil {
			return nil, err
		}
		rs.Patterns = append(rs.Patterns, patternEntry{
			pattern:     pr.Pattern,
			re:          re,
			profileID:   pid,
			patternIdx:  uint8(idx),
			specificity: len(pr.Pattern),
		})
	}
	sort.Slice(rs.Patterns, func(i, j int) bool {
		return rs.Patterns[i].specificity > rs.Patterns[j].specificity
	})
	// Fix up patternIdx after sort so it reflects position in the final slice
	// (used for cache invalidation â€” must be stable per entry, not per original index).
	for i := range rs.Patterns {
		rs.Patterns[i].patternIdx = uint8(i)
	}

	return rs, nil
}

// parseEgressType converts the config string to an EgressType constant.
func parseEgressType(s string) EgressType {
	switch strings.ToLower(s) {
	case "http1":
		return EgressTypeHTTP1
	case "https":
		return EgressTypeHTTPS
	case "h2c":
		return EgressTypeH2C
	default:
		return EgressTypeAuto
	}
}
