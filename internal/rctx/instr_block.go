package rctx

// InstrBlock is one node in the linked chain of instruction overflow slots.
// Used when a flow exceeds 64 instructions (the inline InstrPC/InstrDurNs capacity).
// The common case (≤64 instructions) has zero overhead — InstrOverflow stays nil.
type InstrBlock struct {
	PCs  [64]int16
	Durs [64]int32
	N    uint8
	Next *InstrBlock
}

// AppendInstr records one (pc, durNs) pair into the instruction chain.
// Uses the inline arrays for the first 64; spills into InstrOverflow blocks after that.
func (ctx *Context) AppendInstr(pc int16, durNs int32) {
	if ctx.InstrCount < 64 {
		ctx.InstrPC[ctx.InstrCount] = pc
		ctx.InstrDurNs[ctx.InstrCount] = durNs
		ctx.InstrCount++
		return
	}
	// Spill into overflow chain.
	if ctx.InstrOverflow == nil {
		ctx.InstrOverflow = &InstrBlock{}
	}
	b := ctx.InstrOverflow
	for b.Next != nil {
		b = b.Next
	}
	if int(b.N) < len(b.PCs) {
		b.PCs[b.N] = pc
		b.Durs[b.N] = durNs
		b.N++
		return
	}
	nb := &InstrBlock{}
	nb.PCs[0] = pc
	nb.Durs[0] = durNs
	nb.N = 1
	b.Next = nb
}

// IterInstructions calls fn(pc, durNs) for every recorded instruction in order:
// first the 64 inline slots, then overflow blocks in chain order.
func (ctx *Context) IterInstructions(fn func(pc int16, durNs int32)) {
	for i := uint8(0); i < ctx.InstrCount; i++ {
		fn(ctx.InstrPC[i], ctx.InstrDurNs[i])
	}
	for b := ctx.InstrOverflow; b != nil; b = b.Next {
		for i := uint8(0); i < b.N; i++ {
			fn(b.PCs[i], b.Durs[i])
		}
	}
}

// TotalInstrCount returns the total number of recorded instructions across
// inline slots and all overflow blocks. Called after Execute() returns,
// before ctx.Reset(), to size a flat snapshot when needed.
func (ctx *Context) TotalInstrCount() int {
	total := int(ctx.InstrCount)
	for b := ctx.InstrOverflow; b != nil; b = b.Next {
		total += int(b.N)
	}
	return total
}
