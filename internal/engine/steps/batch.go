package steps

import (
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// BatchFlush flushes all storage ops queued in ctx.Ops into a single pipeline
// round-trip and blocks until results are written back to their dest slots.
// Place this instruction after a group of EmitGet/EmitPut calls and before
// any instruction that reads the results.
// No-op when OpCount == 0 or OnFlush is nil.
func BatchFlush() engine.Instruction {
	return engine.Instruction{
		Name: "batch_flush",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if ctx.OpCount > 0 && ctx.OnFlush != nil {
				ctx.OnFlush(ctx)
			}
			return s.PC + 1
		},
	}
}
