package engine

import (
	"github.com/amitkhosla/rah/internal/rctx"
	"sync"
	"sync/atomic"
	"time"
)

// spikeArrestKey uniquely identifies a spike arrest bucket.
// All fields are value types so it is directly usable as a sync.Map key.
type spikeArrestKey struct {
	flowID   uint32
	tenantID uint16
	keyHash  uint64 // fnv64a hash of dynamic key slot value; 0 if key-less
}

// SpikeArrestStore holds the last-allowed timestamps for all spike arrest instances.
// sync.Map is used to avoid any global lock; per-key state is a single *int64.
type SpikeArrestStore struct {
	m sync.Map // spikeArrestKey â†’ *int64 (unix ns of last allowed request)
}

// NewSpikeArrestStore allocates a ready-to-use SpikeArrestStore.
func NewSpikeArrestStore() *SpikeArrestStore { return &SpikeArrestStore{} }

// Allow returns true if the request passes the spike arrest gate.
// intervalNs must equal interval_ms * time.Millisecond (pre-computed at bake time).
// The CAS loop is wait-free in the common (allow) case and retries only when
// two goroutines race to claim the same interval window.
func (s *SpikeArrestStore) Allow(key spikeArrestKey, intervalNs int64) bool {
	now := time.Now().UnixNano()
	v, _ := s.m.LoadOrStore(key, new(int64))
	ptr := v.(*int64)
	for {
		last := atomic.LoadInt64(ptr)
		if now-last < intervalNs {
			return false
		}
		if atomic.CompareAndSwapInt64(ptr, last, now) {
			return true
		}
	}
}

// fnv64a is a zero-allocation FNV-1a hash over a byte slice.
func fnv64a(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// SpikeArrestStep returns an Instruction that enforces a spike arrest (smoothed
// rate limit) policy.  At most one request per intervalNs is allowed through per
// unique (flowID, tenantID [, keySlot-value]) tuple.
//
//   - flowID    â€” unique integer allocated by the compiler at bake time.
//   - intervalNs â€” interval_ms * time.Millisecond, pre-computed at bake time.
//   - keySlot   â€” index into ctx.ByteSlots used as an extra key dimension;
//     -1 means "tenant only" (no per-value bucketing).
//
// On rejection the step sets ctx.ResponseStatus = 429 and returns StopPlan.
func SpikeArrestStep(store *SpikeArrestStore, flowID uint32, intervalNs int64, keySlot int) Instruction {
	return Instruction{
		Name: "SPIKE_ARREST",
		Action: func(ctx *rctx.Context, state *ExecutionState) int16 {
			key := spikeArrestKey{flowID: flowID, tenantID: ctx.TenantID}
			if keySlot >= 0 && keySlot < len(ctx.ByteSlots) {
				key.keyHash = fnv64a(ctx.ByteSlots[keySlot])
			}
			if !store.Allow(key, intervalNs) {
				ctx.ResponseStatus = 429
				return StopPlan
			}
			return state.PC + 1
		},
	}
}
