package cache

import (
	"bytes"
	"testing"
)

// TestHash128Deterministic verifies the same input always produces the same output.
func TestHash128Deterministic(t *testing.T) {
	fp1 := Hash128(1, []byte("token-abc"))
	fp2 := Hash128(1, []byte("token-abc"))
	if fp1 != fp2 {
		t.Fatal("Hash128 is not deterministic")
	}
}

// TestHash128TenantIsolation verifies different tenants with the same key get
// different fingerprints (no cross-tenant index collisions).
func TestHash128TenantIsolation(t *testing.T) {
	fpA := Hash128(1, []byte("shared-key"))
	fpB := Hash128(2, []byte("shared-key"))
	if fpA == fpB {
		t.Fatal("identical key for different tenants produced the same fingerprint")
	}
}

// TestHash128KeyDistinction verifies different keys for the same tenant differ.
func TestHash128KeyDistinction(t *testing.T) {
	fp1 := Hash128(1, []byte("key-one"))
	fp2 := Hash128(1, []byte("key-two"))
	if fp1 == fp2 {
		t.Fatal("different keys produced the same fingerprint")
	}
}

// TestHash128H1H2Independent verifies the routing hash (H1) and fingerprint (H2)
// differ for the same input — a necessary (not sufficient) condition for
// statistical independence between the two hash components.
func TestHash128H1H2Independent(t *testing.T) {
	fp := Hash128(1, []byte("some-token"))
	h1 := fp[0:8]
	h2 := fp[8:16]
	if bytes.Equal(h1, h2) {
		t.Fatal("H1 and H2 are identical; seeds may not be independent")
	}
}

// TestHash128EmptyKey verifies the empty-key edge case does not panic.
func TestHash128EmptyKey(t *testing.T) {
	fp := Hash128(0, []byte{})
	_ = fp // must not panic
}
