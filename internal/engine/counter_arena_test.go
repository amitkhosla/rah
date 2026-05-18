package engine

import (
	"testing"
)

// TestCounterArena_IsolationBetweenConfigs verifies that two different configIDs
// never share a counter arena — pointer inequality is the proof.
func TestCounterArena_IsolationBetweenConfigs(t *testing.T) {
	reg := NewConfigCounterRegistry(4)
	reg.RegisterTenantConfig(0, 2, 1000)
	reg.RegisterTenantConfig(1, 2, 1000)
	reg.RegisterSlotConfig(2, 2, 4096)
	reg.RegisterSlotConfig(3, 2, 4096)

	ta0 := reg.TenantArena(0)
	ta1 := reg.TenantArena(1)
	if ta0 == nil || ta1 == nil {
		t.Fatal("TenantArena returned nil")
	}
	if ta0 == ta1 {
		t.Error("configID 0 and configID 1 share the same TenantCounterArena pointer")
	}

	sa2 := reg.SlotArena(2)
	sa3 := reg.SlotArena(3)
	if sa2 == nil || sa3 == nil {
		t.Fatal("SlotArena returned nil")
	}
	if sa2 == sa3 {
		t.Error("configID 2 and configID 3 share the same SlotCounterArena pointer")
	}
}

// TestCounterArena_TenantBoundaries verifies that tenantID 0 and tenantID 65535
// both work correctly (boundary values for a uint16).
func TestCounterArena_TenantBoundaries(t *testing.T) {
	const limit = uint32(10)
	const epoch = uint32(999)

	arena := NewTenantCounterArena(65536, 1)

	// TenantID 0 — first slot in the arena.
	ok, rem := arena.Increment(0, 0, epoch, limit)
	if !ok {
		t.Error("tenantID 0: first increment should be allowed")
	}
	if rem != limit-1 {
		t.Errorf("tenantID 0: expected remaining=%d, got %d", limit-1, rem)
	}

	// TenantID 65535 — last addressable slot.
	ok, rem = arena.Increment(65535, 0, epoch, limit)
	if !ok {
		t.Error("tenantID 65535: first increment should be allowed")
	}
	if rem != limit-1 {
		t.Errorf("tenantID 65535: expected remaining=%d, got %d", limit-1, rem)
	}

	// Verify the two tenants have independent counters — exhaust tenantID 0.
	for i := 0; i < int(limit)-1; i++ {
		arena.Increment(0, 0, epoch, limit)
	}
	ok, _ = arena.Increment(0, 0, epoch, limit)
	if ok {
		t.Error("tenantID 0: should be denied after exhausting limit")
	}

	// tenantID 65535 should still have plenty of headroom.
	ok, _ = arena.Increment(65535, 0, epoch, limit)
	if !ok {
		t.Error("tenantID 65535: should still be allowed (independent counter)")
	}
}

// TestCounterArena_CollisionRate verifies CollisionRate returns a plausible
// value — specifically < 1.0 for realistic key counts relative to arena size.
func TestCounterArena_CollisionRate(t *testing.T) {
	arena := NewSlotCounterArena(4096)

	// Simulate 1000 active unique keys in a 4096-slot arena → ~24% occupancy.
	rate := arena.CollisionRate(1000)
	if rate < 0 {
		t.Errorf("CollisionRate returned negative value: %f", rate)
	}
	if rate >= 1.0 {
		t.Errorf("CollisionRate >= 1.0 for 1000 keys in 4096 slots: %f", rate)
	}

	// Overfull scenario should return > 1.0.
	rate = arena.CollisionRate(5000)
	if rate <= 1.0 {
		t.Errorf("CollisionRate should be > 1.0 for 5000 keys in 4096 slots: %f", rate)
	}
}

// TestCounterArena_LimitZeroAlwaysBlocked verifies that limit==0 always
// returns (false, 0) — "blocked", not "unlimited".
func TestCounterArena_LimitZeroAlwaysBlocked(t *testing.T) {
	epoch := uint32(42)

	// TenantCounterArena.
	ta := NewTenantCounterArena(100, 1)
	for _, tid := range []uint16{0, 1, 50, 99} {
		ok, rem := ta.Increment(tid, 0, epoch, 0)
		if ok {
			t.Errorf("TenantArena: tenantID %d with limit=0 should be blocked", tid)
		}
		if rem != 0 {
			t.Errorf("TenantArena: tenantID %d with limit=0 remaining should be 0, got %d", tid, rem)
		}
	}

	// SlotCounterArena.
	sa := NewSlotCounterArena(1024)
	keys := [][]byte{
		[]byte("user-123"),
		[]byte("192.168.1.1"),
		[]byte(""),
	}
	for _, k := range keys {
		ok, rem := sa.Increment(k, 0, epoch, 0)
		if ok {
			t.Errorf("SlotArena: key %q with limit=0 should be blocked", k)
		}
		if rem != 0 {
			t.Errorf("SlotArena: key %q with limit=0 remaining should be 0, got %d", k, rem)
		}
	}

	// counterEpochCAS directly.
	var slot uint64
	ok, rem := counterEpochCAS(&slot, epoch, 0)
	if ok {
		t.Error("counterEpochCAS: limit=0 should return false")
	}
	if rem != 0 {
		t.Errorf("counterEpochCAS: limit=0 remaining should be 0, got %d", rem)
	}
}

// TestCounterArena_EpochChangeResetsCounter verifies the self-resetting window
// behaviour: a new epoch resets the counter regardless of previous count.
func TestCounterArena_EpochChangeResetsCounter(t *testing.T) {
	const limit = uint32(3)

	// --- TenantCounterArena ---
	ta := NewTenantCounterArena(10, 1)

	// Fill to limit in epoch 1.
	epoch1 := uint32(100)
	for i := 0; i < int(limit); i++ {
		ok, _ := ta.Increment(0, 0, epoch1, limit)
		if !ok {
			t.Fatalf("TenantArena epoch1: increment %d should be allowed", i)
		}
	}
	// One more should be denied.
	ok, _ := ta.Increment(0, 0, epoch1, limit)
	if ok {
		t.Error("TenantArena epoch1: should be denied after limit reached")
	}

	// Advance epoch — counter must reset.
	epoch2 := uint32(101)
	ok, rem := ta.Increment(0, 0, epoch2, limit)
	if !ok {
		t.Error("TenantArena epoch2: first increment after epoch change should be allowed")
	}
	if rem != limit-1 {
		t.Errorf("TenantArena epoch2: expected remaining=%d, got %d", limit-1, rem)
	}

	// --- SlotCounterArena ---
	sa := NewSlotCounterArena(1024)
	key := []byte("test-key")

	for i := 0; i < int(limit); i++ {
		ok, _ := sa.Increment(key, 0, epoch1, limit)
		if !ok {
			t.Fatalf("SlotArena epoch1: increment %d should be allowed", i)
		}
	}
	ok, _ = sa.Increment(key, 0, epoch1, limit)
	if ok {
		t.Error("SlotArena epoch1: should be denied after limit reached")
	}

	ok, rem = sa.Increment(key, 0, epoch2, limit)
	if !ok {
		t.Error("SlotArena epoch2: first increment after epoch change should be allowed")
	}
	if rem != limit-1 {
		t.Errorf("SlotArena epoch2: expected remaining=%d, got %d", limit-1, rem)
	}
}

// TestCounterArena_OutOfRangeDegradation verifies safe degradation for
// out-of-range tenantID / windowIdx (returns true, 0 — allow).
func TestCounterArena_OutOfRangeDegradation(t *testing.T) {
	arena := NewTenantCounterArena(10, 2) // max 10 tenants, 2 windows

	// tenantID 10 is out of range for maxTenants=10.
	ok, _ := arena.Increment(10, 0, 1, 5)
	if !ok {
		t.Error("out-of-range tenantID should degrade to allow")
	}

	// windowIdx 2 is out of range for numWindows=2.
	ok, _ = arena.Increment(0, 2, 1, 5)
	if !ok {
		t.Error("out-of-range windowIdx should degrade to allow")
	}
}

// TestCounterArena_GlobalRegistry verifies that ActiveCounterRegistry is never
// nil and that InstallCounterRegistry replaces it atomically.
func TestCounterArena_GlobalRegistry(t *testing.T) {
	orig := ActiveCounterRegistry()
	if orig == nil {
		t.Fatal("ActiveCounterRegistry() should never be nil")
	}

	replacement := NewConfigCounterRegistry(8)
	InstallCounterRegistry(replacement)
	if ActiveCounterRegistry() != replacement {
		t.Error("InstallCounterRegistry did not replace the global registry")
	}

	// Restore original so other tests are unaffected.
	InstallCounterRegistry(orig)
}

// TestCounterArena_NextPow2 unit-tests the nextPow2 utility.
func TestCounterArena_NextPow2(t *testing.T) {
	cases := [][2]int{
		{0, 1}, {1, 1}, {2, 2}, {3, 4}, {4, 4},
		{5, 8}, {1023, 1024}, {1024, 1024}, {1025, 2048},
	}
	for _, c := range cases {
		got := nextPow2(c[0])
		if got != c[1] {
			t.Errorf("nextPow2(%d) = %d, want %d", c[0], got, c[1])
		}
	}
}
