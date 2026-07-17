package engine

import "github.com/amitkhosla/rah/internal/rctx"

// WriteSlot stores data into slot idx using a 2-level strategy:
//
//  1. Data fits threshold (â‰¤ SlotValueThreshold) â†’ carved from arena (zero alloc)
//  2. Data larger than threshold                  â†’ heap alloc (no DataStore round-trip)
//
// Slot indices must be within ByteSlots capacity; out-of-range writes are no-ops.
func (s *ExecutionState) WriteSlot(ctx *rctx.Context, idx int, data []byte) {
	if idx < 0 || idx >= len(ctx.ByteSlots) {
		return
	}
	threshold := rctx.SlotValueThreshold
	if s.slotValueThreshold > 0 {
		threshold = s.slotValueThreshold
	}
	if len(data) <= threshold {
		dst := ctx.Alloc(len(data))
		copy(dst, data)
		ctx.ByteSlots[idx] = dst
		return
	}
	// Larger value â€” allocate on heap rather than spilling to an external store.
	heap := make([]byte, len(data))
	copy(heap, data)
	ctx.ByteSlots[idx] = heap
	ctx.ArenaOverflowed = true
}

// ReadSlot returns the slot value. Returns nil for out-of-range indices or
// unset slots. No sentinel check required â€” no DataStore tier exists.
func (s *ExecutionState) ReadSlot(ctx *rctx.Context, idx int) []byte {
	if idx < 0 || idx >= len(ctx.ByteSlots) {
		return nil
	}
	return ctx.ByteSlots[idx]
}
