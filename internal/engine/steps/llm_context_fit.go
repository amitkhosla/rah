package steps

import (
	"encoding/json"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// CheckContextFitConfig holds all bake-time parameters for the check_context_fit step.
type CheckContextFitConfig struct {
	// Input slots — each is optional (-1 = not used)
	PromptSlot  int // ByteSlots: current user message text or JSON messages array
	SystemSlot  int // ByteSlots: system prompt text
	HistorySlot int // ByteSlots: JSON-encoded []CanonicalMessage history
	ToolsSlot   int // ByteSlots: tools JSON (rough estimate of schema tokens)

	// Target model limits (baked at compile time from LLM catalog)
	MaxContextTokens int // from ModelCapabilities.MaxContextTokens; 0 = no limit check
	MaxOutputTokens  int // reserved for output; subtracted from budget before comparison

	// Output slots
	FitsSlot     int // BoolSlots index: true if total <= budget
	OverflowSlot int // IntSlots index: tokens over budget (0 if fits)
	TotalSlot    int // IntSlots index: total estimated tokens (-1 = not written)
}

// CheckContextFit returns an Instruction that estimates total token usage across
// the configured slots and compares it against the model's context budget.
//
// It never stops the flow — callers use the FitsSlot / OverflowSlot values in a
// subsequent if/switch step to decide what to do with an overflow.
func CheckContextFit(cfg CheckContextFitConfig) engine.Instruction {
	return engine.Instruction{
		Name: "CHECK_CONTEXT_FIT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			total := 0

			// ── PromptSlot ────────────────────────────────────────────────────────
			if cfg.PromptSlot >= 0 && cfg.PromptSlot < len(ctx.ByteSlots) {
				slot := ctx.ByteSlots[cfg.PromptSlot]
				if len(slot) > 0 {
					if slot[0] == '[' {
						// Looks like a JSON messages array — decode and sum per message.
						var msgs []CanonicalMessage
						if err := json.Unmarshal(slot, &msgs); err == nil {
							for _, m := range msgs {
								total += estimateTokens(m.Content) + 4
							}
						} else {
							// Malformed JSON — fall back to plain-text estimate.
							total += estimateTokens(string(slot))
						}
					} else {
						total += estimateTokens(string(slot))
					}
				}
			}

			// ── SystemSlot ────────────────────────────────────────────────────────
			if cfg.SystemSlot >= 0 && cfg.SystemSlot < len(ctx.ByteSlots) {
				slot := ctx.ByteSlots[cfg.SystemSlot]
				if len(slot) > 0 {
					total += estimateTokens(string(slot)) + 4
				}
			}

			// ── HistorySlot ───────────────────────────────────────────────────────
			if cfg.HistorySlot >= 0 && cfg.HistorySlot < len(ctx.ByteSlots) {
				slot := ctx.ByteSlots[cfg.HistorySlot]
				if len(slot) > 0 {
					var msgs []CanonicalMessage
					if err := json.Unmarshal(slot, &msgs); err == nil {
						for _, m := range msgs {
							total += estimateTokens(m.Content) + 4
						}
					} else {
						total += estimateTokens(string(slot))
					}
				}
			}

			// ── ToolsSlot ─────────────────────────────────────────────────────────
			if cfg.ToolsSlot >= 0 && cfg.ToolsSlot < len(ctx.ByteSlots) {
				slot := ctx.ByteSlots[cfg.ToolsSlot]
				if len(slot) > 0 {
					total += estimateTokens(string(slot))
				}
			}

			// ── Budget check ──────────────────────────────────────────────────────
			fits := true
			overflow := 0

			if cfg.MaxContextTokens > 0 {
				budget := cfg.MaxContextTokens - cfg.MaxOutputTokens
				if budget < 0 {
					budget = 0
				}
				if total > budget {
					overflow = total - budget
				}
				fits = overflow == 0
			}

			// ── Write outputs ─────────────────────────────────────────────────────
			if cfg.FitsSlot >= 0 && cfg.FitsSlot < len(ctx.BoolSlots) {
				ctx.BoolSlots[cfg.FitsSlot] = fits
			}
			if cfg.OverflowSlot >= 0 && cfg.OverflowSlot < len(ctx.IntSlots) {
				ctx.IntSlots[cfg.OverflowSlot] = int64(overflow)
			}
			if cfg.TotalSlot >= 0 && cfg.TotalSlot < len(ctx.IntSlots) {
				ctx.IntSlots[cfg.TotalSlot] = int64(total)
			}

			return state.PC + 1
		},
	}
}
