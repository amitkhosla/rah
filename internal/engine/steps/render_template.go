package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
)

// RenderSeg is one segment of a pre-compiled render_template instruction.
// Exactly one of Lit or SlotIdx is active per segment:
//   - Lit != nil  → emit these literal bytes verbatim
//   - SlotIdx >= 0 → emit ByteSlots[SlotIdx] at runtime
type RenderSeg struct {
	Lit     []byte // static literal bytes (nil when SlotIdx is used)
	SlotIdx int    // -1 when Lit is used; otherwise index into ByteSlots
}

// RenderTemplate builds a string at runtime by concatenating literal segments
// and slot values.  It is compiled from a template string with ${varname}
// markers (e.g. '{"userId":"${user_id}","plan":"${plan}"}').
//
// Hot-path cost: one pre-scan to total the length, one ctx.Alloc call, then
// len(segs) copy operations.  Zero heap allocations for results ≤ 1 KB.
func RenderTemplate(segs []RenderSeg, destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "RENDER_TEMPLATE",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Pass 1: calculate total length so we can Alloc once.
			total := 0
			for i := range segs {
				if segs[i].Lit != nil {
					total += len(segs[i].Lit)
				} else if segs[i].SlotIdx >= 0 && segs[i].SlotIdx < len(ctx.ByteSlots) {
					total += len(ctx.ByteSlots[segs[i].SlotIdx])
				}
			}

			// Pass 2: fill the buffer.
			buf := ctx.Alloc(total)
			n := 0
			for i := range segs {
				if segs[i].Lit != nil {
					n += copy(buf[n:], segs[i].Lit)
				} else if segs[i].SlotIdx >= 0 && segs[i].SlotIdx < len(ctx.ByteSlots) {
					n += copy(buf[n:], ctx.ByteSlots[segs[i].SlotIdx])
				}
			}
			ctx.ByteSlots[destSlot] = buf[:n]
			return s.PC + 1
		},
	}
}
