package engine

import "rah/internal/rctx"

// WriteSlot stores data into slot idx, choosing the appropriate tier:
//
//  1. Slot index in range AND data fits arena → arena (zero alloc, fastest)
//  2. Slot index in range AND data too large  → SlotOverflowStore (data overflow)
//  3. Slot index out of range                 → SlotOverflowStore (index overflow)
//
// When SlotOverflowStore is nil and the data cannot fit in the arena,
// the value is stored on the heap as a fallback (no data loss).
func (s *ExecutionState) WriteSlot(ctx *rctx.Context, idx int, data []byte) {
	// ── Case 3: slot index beyond in-memory capacity ──────────────────────────
	if ctx.IsSlotIndexOverflow(idx) {
		if s.SlotOverflow == nil {
			// No store configured — index overflow is a no-op.
			return
		}
		key := ctx.SlotIndexStoreKey(idx)
		if err := s.SlotOverflow.SlotPut(key, data); err == nil {
			ctx.TrackSlotKey(key)
		}
		return
	}

	// ── Case 1: data fits in the arena ────────────────────────────────────────
	if len(data) <= rctx.ArenaBlockSize {
		s := ctx.Alloc(len(data))
		copy(s, data)
		ctx.ByteSlots[idx] = s
		return
	}

	// ── Case 2: data too large for a single arena block ───────────────────────
	if s.SlotOverflow == nil {
		// No store — keep on heap rather than lose the value.
		heap := make([]byte, len(data))
		copy(heap, data)
		ctx.ByteSlots[idx] = heap
		ctx.ArenaOverflowed = true
		return
	}
	key := ctx.NextSlotDataKey()
	if err := s.SlotOverflow.SlotPut(key, data); err != nil {
		// Store write failed — fall back to heap so execution continues.
		heap := make([]byte, len(data))
		copy(heap, data)
		ctx.ByteSlots[idx] = heap
		return
	}
	ctx.TrackSlotKey(key)
	ctx.ByteSlots[idx] = ctx.EncodeSlotRef(key)
	ctx.ArenaOverflowed = true
}

// ReadSlot returns the value for slot idx, transparently fetching from
// SlotOverflowStore when the slot holds a store-reference sentinel or when
// the slot index itself exceeds in-memory capacity.
//
// Returns nil when the slot is empty or the store is unreachable.
func (s *ExecutionState) ReadSlot(ctx *rctx.Context, idx int) []byte {
	// ── Case 3: slot index beyond in-memory capacity ──────────────────────────
	if ctx.IsSlotIndexOverflow(idx) {
		if s.SlotOverflow == nil {
			return nil
		}
		key := ctx.SlotIndexStoreKey(idx)
		data, _ := s.SlotOverflow.SlotGet(key)
		return data
	}

	// ── Case 2: sentinel — value was previously spilled to store ──────────────
	if ctx.IsSlotInStore(idx) {
		if s.SlotOverflow == nil {
			return nil
		}
		data, _ := s.SlotOverflow.SlotGet(ctx.SlotStoreKey(idx))
		return data
	}

	// ── Case 1: inline arena bytes ────────────────────────────────────────────
	return ctx.ByteSlots[idx]
}
