package engine

import (
	"testing"

	"rah/internal/registry"
)

// configMap is a simple lookup helper used across tests.
func makeConfigMap(m map[string]uint16) func(string) (uint16, bool) {
	return func(name string) (uint16, bool) {
		id, ok := m[name]
		return id, ok
	}
}

// TestUpstreamRegistry_LongerPatternWins verifies that a more specific (longer)
// prefix pattern beats a shorter one.
func TestUpstreamRegistry_LongerPatternWins(t *testing.T) {
	lookup := makeConfigMap(map[string]uint16{
		"short":  1,
		"long":   2,
	})

	patterns := []registry.UpstreamPattern{
		{Pattern: "https://api.example.com/*", ConfigName: "short"},
		{Pattern: "https://api.example.com/v1/*", ConfigName: "long"},
	}

	r := NewUpstreamRegistry(patterns, lookup, "fail_open", 0)

	id, found := r.Match("https://api.example.com/v1/users")
	if !found {
		t.Fatal("expected a match")
	}
	if id != 2 {
		t.Fatalf("expected configID=2 (long), got %d", id)
	}

	// Shorter path should still match the short pattern.
	id, found = r.Match("https://api.example.com/v2/users")
	if !found {
		t.Fatal("expected a match for shorter pattern")
	}
	if id != 1 {
		t.Fatalf("expected configID=1 (short), got %d", id)
	}
}

// TestUpstreamRegistry_ExactBeatsPrefix verifies that an exact match beats a
// prefix (wildcard) match of the same length.
func TestUpstreamRegistry_ExactBeatsPrefix(t *testing.T) {
	lookup := makeConfigMap(map[string]uint16{
		"prefix": 1,
		"exact":  2,
	})

	// Both patterns produce the same prefix length ("https://api.example.com/v1/chat").
	// The wildcard version strips the trailing '*', giving the same string length as
	// the exact version.
	patterns := []registry.UpstreamPattern{
		{Pattern: "https://api.example.com/v1/chat*", ConfigName: "prefix"},
		{Pattern: "https://api.example.com/v1/chat", ConfigName: "exact"},
	}

	r := NewUpstreamRegistry(patterns, lookup, "fail_open", 0)

	// Exact URL should match the exact pattern first.
	id, found := r.Match("https://api.example.com/v1/chat")
	if !found {
		t.Fatal("expected a match")
	}
	if id != 2 {
		t.Fatalf("expected configID=2 (exact), got %d", id)
	}

	// URL with suffix should only match the prefix pattern.
	id, found = r.Match("https://api.example.com/v1/chatcompletions")
	if !found {
		t.Fatal("expected a match for prefix pattern")
	}
	if id != 1 {
		t.Fatalf("expected configID=1 (prefix), got %d", id)
	}
}

// TestUpstreamRegistry_FailOpen verifies that an unmatched URL with fail_open
// policy results in allow=true and configID=0.
func TestUpstreamRegistry_FailOpen(t *testing.T) {
	lookup := makeConfigMap(map[string]uint16{"cfg": 5})
	patterns := []registry.UpstreamPattern{
		{Pattern: "https://api.example.com/*", ConfigName: "cfg"},
	}

	r := NewUpstreamRegistry(patterns, lookup, "fail_open", 0)

	id, allow := r.MatchWithPolicy("https://other.example.com/endpoint")
	if !allow {
		t.Fatal("expected allow=true for fail_open")
	}
	if id != 0 {
		t.Fatalf("expected configID=0 for fail_open unmatched, got %d", id)
	}
}

// TestUpstreamRegistry_FailClosed verifies that an unmatched URL with fail_closed
// policy results in allow=false.
func TestUpstreamRegistry_FailClosed(t *testing.T) {
	lookup := makeConfigMap(map[string]uint16{"cfg": 5})
	patterns := []registry.UpstreamPattern{
		{Pattern: "https://api.example.com/*", ConfigName: "cfg"},
	}

	r := NewUpstreamRegistry(patterns, lookup, "fail_closed", 0)

	id, allow := r.MatchWithPolicy("https://other.example.com/endpoint")
	if allow {
		t.Fatal("expected allow=false for fail_closed")
	}
	if id != 0 {
		t.Fatalf("expected configID=0 when denying, got %d", id)
	}
}

// TestUpstreamRegistry_DefaultConfig verifies that an unmatched URL with
// default_config policy results in allow=true and the configured defaultID.
func TestUpstreamRegistry_DefaultConfig(t *testing.T) {
	const defaultID uint16 = 42

	lookup := makeConfigMap(map[string]uint16{"cfg": 5})
	patterns := []registry.UpstreamPattern{
		{Pattern: "https://api.example.com/*", ConfigName: "cfg"},
	}

	r := NewUpstreamRegistry(patterns, lookup, "default_config", defaultID)

	id, allow := r.MatchWithPolicy("https://other.example.com/endpoint")
	if !allow {
		t.Fatal("expected allow=true for default_config")
	}
	if id != defaultID {
		t.Fatalf("expected configID=%d (defaultID), got %d", defaultID, id)
	}
}

// TestUpstreamRegistry_EmptyFailOpen verifies that an empty registry allows all
// requests (fail_open sentinel before InstallUpstreamRegistry is called).
func TestUpstreamRegistry_EmptyFailOpen(t *testing.T) {
	r := NewUpstreamRegistry(nil, makeConfigMap(nil), "fail_open", 0)

	id, allow := r.MatchWithPolicy("https://anything.example.com/path")
	if !allow {
		t.Fatal("empty registry should allow all (fail_open)")
	}
	if id != 0 {
		t.Fatalf("empty registry should return configID=0, got %d", id)
	}
}

// TestUpstreamRegistry_GlobalAtomic verifies that ActiveUpstreamRegistry never
// returns nil and that InstallUpstreamRegistry swaps the instance atomically.
func TestUpstreamRegistry_GlobalAtomic(t *testing.T) {
	// Reset to nil so we test the nil-guard.
	globalUpstreamRegistry.Store(nil)

	got := ActiveUpstreamRegistry()
	if got == nil {
		t.Fatal("ActiveUpstreamRegistry must never return nil")
	}

	// Install a new registry and verify it is returned.
	lookup := makeConfigMap(map[string]uint16{"cfg": 7})
	patterns := []registry.UpstreamPattern{
		{Pattern: "https://installed.example.com/*", ConfigName: "cfg"},
	}
	r := NewUpstreamRegistry(patterns, lookup, "fail_open", 0)
	InstallUpstreamRegistry(r)

	active := ActiveUpstreamRegistry()
	if active != r {
		t.Fatal("ActiveUpstreamRegistry should return the installed registry")
	}

	id, found := active.Match("https://installed.example.com/path")
	if !found || id != 7 {
		t.Fatalf("expected configID=7, got id=%d found=%v", id, found)
	}

	// Clean up — restore nil so other tests start fresh.
	globalUpstreamRegistry.Store(nil)
}
