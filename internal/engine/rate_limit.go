package engine

import (
	"rah/internal/rctx"
	"sync/atomic"
	"time"
	"unsafe"
)

/*
RateLimitRule (32-bit uint) Detailed Specification:

[BITS 00-02] MODE / WINDOW TYPE:
  - 0: SECOND  -> Key includes current Unix Second. Resets every 1s.
  - 1: MINUTE  -> Key includes current Unix Minute. Resets every 60s.
  - 2: HOUR    -> Key includes current Unix Hour. Resets every 3600s.
  - 3: DAY     -> Key includes current Unix Day (Calendar day).
  - 4: WEEK    -> Key includes current ISO Week number.
  - 5: MONTH   -> Key includes current Month (1-12).
  - 6: YEAR    -> Key includes current Year.
  - 7: GAP     -> Special "Enforcement" Mode. Instead of a window, it ensures
    a minimum duration (BaseLimit) has passed since the LAST
    successful request for this key.

[BITS 03-05] SCOPE:
  - 0: Tenant  -> Rate limit shared by all requests belonging to the TenantID.
  - 1: API     -> Rate limit shared by all requests for a specific Endpoint.
  - 2: IP      -> Rate limit specific to the Source IP.
  - 3: SLOT    -> The "Flex" mode. It hashes the value found in TargetSlotID
    (e.g., ClientID, UserID, DeviceID).

[BITS 06-07] SYNC POLICY:
- 0: LOCAL   -> Check only this Pod's memory. Fastest (~50ns).
- 1: ASYNC   -> Check local, but sync deltas to Redis/DB in background.
- 2: STRICT  -> Network call to Redis/DB before allowing. Slowest (~1ms).

[BITS 08-13] TARGET SLOT ID:
  - Integer (0-63). Only used if Scope is 3 (SLOT). Tells the engine
    which index in ctx.Slots holds the key string.

[BITS 14-15] MULTIPLIER:
- 0: x1, 1: x10, 2: x100, 3: x1000. Multiplies the BaseLimit.

[BITS 16-31] BASE LIMIT:
- The raw count or seconds gap value.
*/
type RateLimitRule uint32

// --- Constants for Bit-Parsing ---

const (
	ModeMask   = 0x07   // Bits 0-2
	ScopeMask  = 0x07   // Bits 3-5
	PolicyMask = 0x03   // Bits 6-7
	SlotMask   = 0x3F   // Bits 8-13
	MultMask   = 0x03   // Bits 14-15
	LimitMask  = 0xFFFF // Bits 16-31
)

// --- The Core Rule Type ---

func (r RateLimitRule) Mode() uint8   { return uint8(r & ModeMask) }
func (r RateLimitRule) Scope() uint8  { return uint8((r >> 3) & ScopeMask) }
func (r RateLimitRule) Policy() uint8 { return uint8((r >> 6) & PolicyMask) }
func (r RateLimitRule) Slot() uint8   { return uint8((r >> 8) & SlotMask) }

func (r RateLimitRule) FinalLimit() uint32 {
	base := uint32((r >> 16) & 0xFFFF)
	multi := (r >> 14) & 0x03
	multipliers := [4]uint32{1, 10, 100, 1000}
	return base * multipliers[multi]
}

// --- CounterStore: The "Big Block" of Memory ---

// CounterStore is a massive flat array of 64-bit slots.
// For 25k tenants, we pre-allocate this to avoid runtime growth.
type CounterStore struct {
	// Each slot is 8 bytes. 1 million slots = 8MB.
	Arena []uint64
}

func NewCounterStore(size int) *CounterStore {
	return &CounterStore{
		Arena: make([]uint64, size),
	}
}

// ResolvedLimit is the effective rate limit after all 4 resolution layers.
// Returned by ResolveRateLimit. Fits in two CPU registers.
type ResolvedLimit struct {
	PerSec      uint32
	PerMin      uint32
	BurstFactor uint16
	_           uint16 // pad to 12 bytes
}

// --- The 3 High-Performance Algorithms ---

// 1. FixedWindow: Uses the slot as a simple uint32 counter (in the lower 32 bits).
func (cs *CounterStore) FixedWindow(idx uint32, limit uint32) bool {
	// Increment only the lower 32 bits atomically
	// We use 64-bit atomic because the whole Arena is uint64 to support Token Bucket
	val := atomic.AddUint64(&cs.Arena[idx], 1)
	count := uint32(val & 0xFFFFFFFF)

	if count > limit {
		return false
	}
	return true
}

// FixedWindowEpoch is a lock-free, self-resetting fixed-window counter.
//
// Slot layout: [epoch:32 | count:32]
//   - epoch == currentEpoch: CAS-increment the lower 32 bits.
//   - epoch != currentEpoch: CAS-reset to [currentEpoch | 1] (new window).
//
// No background goroutine required — slots self-reset on first access in a
// new time bucket. Collisions between (tenant, config, bucket) triples are
// benign: worst case is a marginally tighter limit for one request.
//
// Returns true if the request is within the limit, false if it exceeds it.
func (cs *CounterStore) FixedWindowEpoch(idx uint32, epoch uint32, limit uint32) bool {
	for {
		old := atomic.LoadUint64(&cs.Arena[idx])
		storedEpoch := uint32(old >> 32)

		if storedEpoch != epoch {
			// New time window — reset slot to [epoch | 1].
			newSlot := (uint64(epoch) << 32) | 1
			if atomic.CompareAndSwapUint64(&cs.Arena[idx], old, newSlot) {
				return true // first request in this window
			}
			continue // another goroutine won the CAS — retry
		}

		// Same window — check current count before incrementing.
		count := uint32(old & 0xFFFFFFFF)
		if count >= limit {
			return false // limit already reached
		}

		if atomic.CompareAndSwapUint64(&cs.Arena[idx], old, old+1) {
			return true
		}
		// CAS failed (concurrent increment) — retry
	}
}

// 2. TokenBucket: Uses the slot as [LastUpdate(32bit) | Tokens(32bit)].
func (cs *CounterStore) TokenBucket(idx uint32, rate uint32, burst uint32, now uint32) bool {
	for {
		oldState := atomic.LoadUint64(&cs.Arena[idx])
		lastUpdate := uint32(oldState >> 32)
		oldTokens := uint32(oldState & 0xFFFFFFFF)

		// Refill tokens based on time passed
		elapsed := now - lastUpdate
		newTokens := oldTokens + (elapsed * rate)
		if newTokens > burst {
			newTokens = burst
		}

		if newTokens < 1 {
			return false // Bucket empty
		}

		// Pack new state: [CurrentTime | Tokens-1]
		newState := (uint64(now) << 32) | uint64(newTokens-1)

		// CAS ensures thread-safety without locks
		if atomic.CompareAndSwapUint64(&cs.Arena[idx], oldState, newState) {
			return true
		}
		// If CAS fails, another thread updated the bucket; loop and retry (rare)
	}
}

// 3. GapInterval: Uses the slot as a LastSeen timestamp.
func (cs *CounterStore) GapInterval(idx uint32, gap uint32, now uint32) bool {
	lastSeen := uint32(atomic.LoadUint64(&cs.Arena[idx]))

	if now-lastSeen < gap {
		return false
	}

	// Update timestamp
	atomic.StoreUint64(&cs.Arena[idx], uint64(now))
	return true
}

// --- The Instructions (Lego Blocks) ---

// FixedWindowStep is used when the "Bake" process sees a standard Sec/Min/Day window.
type FixedWindowStep struct {
	Rule   RateLimitRule
	Store  *CounterStore
	RuleID uint32 // Offset into the Arena
}

func (s *FixedWindowStep) Execute(ctx *rctx.Context) int16 {
	now := time.Now()
	elapsed := uint32(now.Unix())

	// Calculate bucket ID based on mode
	var bucketID uint32
	switch s.Rule.Mode() {
	case 1: // MINUTE
		bucketID = elapsed / 60
	case 2: // HOUR
		bucketID = elapsed / 3600
	case 3: // DAY
		bucketID = elapsed / 86400
	default: // SECOND
		bucketID = elapsed
	}

	idx := s.calculateIndex(ctx, bucketID)
	if !s.Store.FixedWindow(idx, s.Rule.FinalLimit()) {
		ctx.ResponseStatus = 429
		return -1 // Stop
	}
	return 1
}

// TokenBucketStep is used when the "Bake" process sees a Burst requirement.
type TokenBucketStep struct {
	Rule  RateLimitRule
	Store *CounterStore
	Burst uint32
	Rate  uint32
}

func (s *TokenBucketStep) Execute(ctx *rctx.Context) int16 {
	idx := s.calculateIndex(ctx, 0) // Token bucket doesn't need bucket IDs

	if !s.Store.TokenBucket(idx, s.Rate, s.Burst, uint32(time.Now().Unix())) {
		ctx.ResponseStatus = 429
		return -1
	}
	return 1
}

// RemoteRateLimitStep is the "De-coupled" handler for Redis/External.
type RemoteRateLimitStep struct {
	Rule     RateLimitRule
	Provider ExternalRateLimitProvider // Interface/Reference to Redis
}

func (s *RemoteRateLimitStep) resolveKey(ctx *rctx.Context) string {
	slotIdx := int(s.Rule.Slot())
	if slotIdx < len(ctx.ByteSlots) {
		return ByteToString(ctx.ByteSlots[slotIdx])
	}
	return ""
}
func ByteToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

func (s *RemoteRateLimitStep) Execute(ctx *rctx.Context) int16 {
	key := s.resolveKey(ctx)
	if !s.Provider.Check(key, s.Rule) {
		ctx.ResponseStatus = 429
		return -1
	}
	return 1
}

// We will use a helper to extract the TenantID from a known slot (usually slot 0)
func (s *FixedWindowStep) calculateIndex(ctx *rctx.Context, bucketID uint32) uint32 {
	// Assuming TenantID is an integer in slot 0 or derived from string
	// If your context has a specific GetTenantID() method, use that.
	tenantID := uint32(0) // Default or ctx.GetTenantID()

	h := tenantID ^ s.RuleID ^ bucketID
	return h % uint32(len(s.Store.Arena))
}

func (s *TokenBucketStep) calculateIndex(ctx *rctx.Context, bucketID uint32) uint32 {
	// Logic for Token Bucket index
	tenantID := uint32(0)
	h := tenantID ^ uint32(s.Rule)
	return h % uint32(len(s.Store.Arena))
}

// Add to engine\rate_limit.go
type ExternalRateLimitProvider interface {
	Check(key string, rule RateLimitRule) bool
}
