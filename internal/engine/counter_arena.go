package engine

import (
	"math/bits"
	"sync/atomic"
)

// ---------------------------------------------------------------------------
// Shared CAS primitive
// ---------------------------------------------------------------------------

// counterEpochCAS atomically updates a slot using the [epoch:32 | count:32] layout.
//
// Same epoch  → CAS-increment the count. Returns (allowed, remaining).
// New epoch   → CAS-reset to [epoch|1], starting a fresh window.
// limit == 0  → always blocked (returns false, 0).
// limit reached → returns (false, 0).
func counterEpochCAS(slot *uint64, epoch uint32, limit uint32) (bool, uint32) {
	if limit == 0 {
		return false, 0
	}
	for {
		old := atomic.LoadUint64(slot)
		storedEpoch := uint32(old >> 32)

		if storedEpoch != epoch {
			// New time window — reset to [epoch | 1].
			newVal := (uint64(epoch) << 32) | 1
			if atomic.CompareAndSwapUint64(slot, old, newVal) {
				remaining := uint32(0)
				if limit > 1 {
					remaining = limit - 1
				}
				return true, remaining
			}
			continue // another goroutine won, retry
		}

		// Same window.
		count := uint32(old & 0xFFFFFFFF)
		if count >= limit {
			return false, 0
		}
		if atomic.CompareAndSwapUint64(slot, old, old+1) {
			newCount := count + 1
			remaining := uint32(0)
			if limit > newCount {
				remaining = limit - newCount
			}
			return true, remaining
		}
		// Concurrent write — retry.
	}
}

// counterEpochCASBy is like counterEpochCAS but increments the counter by delta
// instead of 1. delta == 0 is treated as 1 to prevent no-op increments.
// If delta would exceed the limit, the request is denied and the counter is not modified.
func counterEpochCASBy(slot *uint64, epoch uint32, limit uint32, delta uint32) (bool, uint32) {
	if limit == 0 {
		return false, 0
	}
	if delta == 0 {
		delta = 1
	}
	for {
		old := atomic.LoadUint64(slot)
		storedEpoch := uint32(old >> 32)

		if storedEpoch != epoch {
			// New time window — reset to [epoch | delta].
			if delta > limit {
				// Delta alone exceeds limit — deny without modifying.
				return false, 0
			}
			newVal := (uint64(epoch) << 32) | uint64(delta)
			if atomic.CompareAndSwapUint64(slot, old, newVal) {
				remaining := uint32(0)
				if limit > delta {
					remaining = limit - delta
				}
				return true, remaining
			}
			continue
		}

		// Same window.
		count := uint32(old & 0xFFFFFFFF)
		newCount := count + delta
		if newCount > limit {
			// Adding delta would exceed limit — deny.
			return false, 0
		}
		newVal := (uint64(epoch) << 32) | uint64(newCount)
		if atomic.CompareAndSwapUint64(slot, old, newVal) {
			remaining := uint32(0)
			if limit > newCount {
				remaining = limit - newCount
			}
			return true, remaining
		}
	}
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

// arenaFNV32a is an inline FNV-1a 32-bit hash. Zero allocations.
func arenaFNV32a(b []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}

// nextPow2 returns the smallest power of 2 >= n. If n <= 1, returns 1.
func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	// bits.Len returns the number of bits needed to represent n.
	// 1 << bits.Len(n-1) is the next power of 2 >= n.
	return 1 << bits.Len(uint(n-1))
}

// ---------------------------------------------------------------------------
// Part A — TenantCounterArena
// ---------------------------------------------------------------------------

// TenantCounterArena provides O(1) per-tenant rate limit counters using direct
// TenantID indexing. No hash collisions possible.
//
// Slot layout: slots[tenantID*numWindows + windowIdx]  = [epoch:32 | count:32]
type TenantCounterArena struct {
	slots      []uint64
	numWindows int
	maxTenants int
}

// NewTenantCounterArena allocates a counter arena for up to maxTenants tenants,
// each with numWindows independent counters (one per rate-limit window type).
func NewTenantCounterArena(maxTenants, numWindows int) *TenantCounterArena {
	if maxTenants <= 0 {
		maxTenants = 65536
	}
	if numWindows <= 0 {
		numWindows = 1
	}
	return &TenantCounterArena{
		slots:      make([]uint64, maxTenants*numWindows),
		numWindows: numWindows,
		maxTenants: maxTenants,
	}
}

// Increment checks and increments the counter for (tenantID, windowIdx) in the
// given epoch. Returns (allowed, remaining).
//
//   - limit == 0  → always (false, 0)  — blocked, not unlimited
//   - out-of-range tenantID / windowIdx → (true, 0)  — safe degradation
func (a *TenantCounterArena) Increment(tenantID uint16, windowIdx int, epoch uint32, limit uint32) (bool, uint32) {
	if limit == 0 {
		return false, 0
	}
	if int(tenantID) >= a.maxTenants || windowIdx < 0 || windowIdx >= a.numWindows {
		return true, 0 // out-of-range — allow safely
	}
	idx := int(tenantID)*a.numWindows + windowIdx
	return counterEpochCAS(&a.slots[idx], epoch, limit)
}

// IncrementBy is like Increment but adds delta to the counter instead of 1.
// Use this for token-weighted rate limiting where delta = tokens consumed.
func (a *TenantCounterArena) IncrementBy(tenantID uint16, windowIdx int, epoch, limit, delta uint32) (bool, uint32) {
	if limit == 0 {
		return false, 0
	}
	if int(tenantID) >= a.maxTenants || windowIdx < 0 || windowIdx >= a.numWindows {
		return true, 0
	}
	idx := int(tenantID)*a.numWindows + windowIdx
	return counterEpochCASBy(&a.slots[idx], epoch, limit, delta)
}

// ReadCurrent returns the counter value for (tenantID, windowIdx) in the given
// epoch without modifying any state. Returns 0 if the window has reset or the
// indices are out of range. Safe to call concurrently with Increment/IncrementBy.
func (a *TenantCounterArena) ReadCurrent(tenantID uint16, windowIdx int, epoch uint32) uint32 {
	if int(tenantID) >= a.maxTenants || windowIdx < 0 || windowIdx >= a.numWindows {
		return 0
	}
	packed := atomic.LoadUint64(&a.slots[int(tenantID)*a.numWindows+windowIdx])
	if uint32(packed>>32) != epoch {
		return 0 // window has reset — counter is 0 for this epoch
	}
	return uint32(packed)
}

// ---------------------------------------------------------------------------
// Part B — SlotCounterArena
// ---------------------------------------------------------------------------

// SlotCounterArena provides hash-based rate limit counters for arbitrary byte
// keys (IP addresses, user IDs, API keys, etc.). The arena size is a power of 2
// so the slot index is a cheap bitmask operation.
//
// Slot layout: slots[hash & mask] = [epoch:32 | count:32]
type SlotCounterArena struct {
	slots []uint64 // power-of-2 length
	mask  uint32   // len(slots) - 1
}

// NewSlotCounterArena allocates a hash-based counter arena.
// size is rounded up to the next power of 2; minimum is 1024.
func NewSlotCounterArena(size int) *SlotCounterArena {
	if size < 1024 {
		size = 1024
	}
	size = nextPow2(size)
	return &SlotCounterArena{
		slots: make([]uint64, size),
		mask:  uint32(size - 1),
	}
}

// Increment checks and increments the counter for the given key bytes and
// windowIdx in the given epoch. Returns (allowed, remaining).
//
//   - limit == 0 → always (false, 0)
func (a *SlotCounterArena) Increment(keyBytes []byte, windowIdx int, epoch uint32, limit uint32) (bool, uint32) {
	if limit == 0 {
		return false, 0
	}
	h := arenaFNV32a(keyBytes)
	// Mix in windowIdx to keep different window types from colliding on the same slot.
	h ^= uint32(windowIdx) * 2654435761
	idx := h & a.mask
	return counterEpochCAS(&a.slots[idx], epoch, limit)
}

// IncrementBy is like Increment but adds delta to the counter instead of 1.
func (a *SlotCounterArena) IncrementBy(keyBytes []byte, windowIdx int, epoch, limit, delta uint32) (bool, uint32) {
	if limit == 0 {
		return false, 0
	}
	h := arenaFNV32a(keyBytes)
	h ^= uint32(windowIdx) * 2654435761
	idx := h & a.mask
	return counterEpochCASBy(&a.slots[idx], epoch, limit, delta)
}

// ReadCurrent returns the counter value for keyBytes+windowIdx in the given
// epoch without modifying any state. Returns 0 if the window has reset.
// Uses the same hash as Increment/IncrementBy so results are consistent.
func (a *SlotCounterArena) ReadCurrent(keyBytes []byte, windowIdx int, epoch uint32) uint32 {
	h := arenaFNV32a(keyBytes)
	h ^= uint32(windowIdx) * 2654435761
	packed := atomic.LoadUint64(&a.slots[h&a.mask])
	if uint32(packed>>32) != epoch {
		return 0
	}
	return uint32(packed)
}

// CollisionRate returns an estimate of hash-table occupancy: activeKeys / arenaSize.
// A value < 0.5 is healthy; > 0.75 means consider enlarging the arena.
// This is a monitoring-only helper — it does not read slot state.
func (a *SlotCounterArena) CollisionRate(activeKeys uint64) float64 {
	sz := uint64(len(a.slots))
	if sz == 0 {
		return 0
	}
	return float64(activeKeys) / float64(sz)
}

// ---------------------------------------------------------------------------
// Part C — ConfigCounterRegistry
// ---------------------------------------------------------------------------

// ConfigCounterRegistry maps configIDs to their isolated counter arenas.
// Each configID owns exactly one arena (either tenant-direct or slot-hash).
// Registrations happen at bake time; hot-path reads are pointer loads only.
type ConfigCounterRegistry struct {
	tenantArenas []*TenantCounterArena
	slotArenas   []*SlotCounterArena
	cap          int
}

// NewConfigCounterRegistry allocates a registry that can hold up to maxConfigs
// distinct rate-limit configurations.
func NewConfigCounterRegistry(maxConfigs int) *ConfigCounterRegistry {
	if maxConfigs <= 0 {
		maxConfigs = 256
	}
	return &ConfigCounterRegistry{
		tenantArenas: make([]*TenantCounterArena, maxConfigs),
		slotArenas:   make([]*SlotCounterArena, maxConfigs),
		cap:          maxConfigs,
	}
}

// RegisterTenantConfig wires configID to a TenantCounterArena.
// maxTenants == 0 → default 65536.
// If an arena is already registered for this configID, it is preserved to
// avoid resetting in-flight counters on re-sync / cross-instance bootstrap.
func (r *ConfigCounterRegistry) RegisterTenantConfig(configID uint16, numWindows, maxTenants int) {
	if int(configID) >= r.cap {
		return
	}
	if r.tenantArenas[configID] != nil {
		return // preserve existing arena; counters survive re-bootstrap
	}
	if maxTenants == 0 {
		maxTenants = 65536
	}
	r.tenantArenas[configID] = NewTenantCounterArena(maxTenants, numWindows)
}

// RegisterSlotConfig wires configID to a SlotCounterArena.
// arenaSize == 0 → default 65536.
// If an arena is already registered for this configID, it is preserved to
// avoid resetting in-flight counters on re-sync / cross-instance bootstrap.
func (r *ConfigCounterRegistry) RegisterSlotConfig(configID uint16, numWindows, arenaSize int) {
	if int(configID) >= r.cap {
		return
	}
	if r.slotArenas[configID] != nil {
		return // preserve existing arena; counters survive re-bootstrap
	}
	if arenaSize == 0 {
		arenaSize = 65536
	}
	r.slotArenas[configID] = NewSlotCounterArena(arenaSize)
}

// TenantArena returns the TenantCounterArena for configID, or nil if none registered.
func (r *ConfigCounterRegistry) TenantArena(configID uint16) *TenantCounterArena {
	if r == nil || int(configID) >= r.cap {
		return nil
	}
	return r.tenantArenas[configID]
}

// SlotArena returns the SlotCounterArena for configID, or nil if none registered.
func (r *ConfigCounterRegistry) SlotArena(configID uint16) *SlotCounterArena {
	if r == nil || int(configID) >= r.cap {
		return nil
	}
	return r.slotArenas[configID]
}

// ---------------------------------------------------------------------------
// Global state
// ---------------------------------------------------------------------------

var globalCounterRegistry atomic.Pointer[ConfigCounterRegistry]

func init() {
	// Install a non-nil default so ActiveCounterRegistry() is always safe.
	globalCounterRegistry.Store(NewConfigCounterRegistry(256))
}

// ActiveCounterRegistry returns the current global registry. Never nil.
func ActiveCounterRegistry() *ConfigCounterRegistry {
	return globalCounterRegistry.Load()
}

// InstallCounterRegistry atomically replaces the global registry.
// Typically called once at startup after all bake-time registrations.
func InstallCounterRegistry(r *ConfigCounterRegistry) {
	if r != nil {
		globalCounterRegistry.Store(r)
	}
}
