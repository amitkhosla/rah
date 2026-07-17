package steps

import (
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/ingest"
	"github.com/amitkhosla/rah/internal/rctx"
)

// EmitEventConfig is baked at compile time and captured in the instruction closure.
type EmitEventConfig struct {
	Pipeline    *ingest.Pipeline  // nil = no-op (pipeline disabled)
	Kind        ingest.EventKind  // event kind label
	PayloadSlot int               // ByteSlots index for event payload; -1 = no payload
	ModelSlot        int // ByteSlots index for model name annotation; -1 = skip
	SessionSlot      int // ByteSlots index for session ID string; -1 = skip
	InputTokensSlot  int // IntSlots index for input token count;  -1 = skip
	OutputTokensSlot int // IntSlots index for output token count; -1 = skip
	// Deferred=true emits after the HTTP response is committed (AfterResponse hook).
	// Deferred=false emits immediately (non-blocking channel write).
	Deferred bool
}

// EmitEvent returns an engine.Instruction that fires an ingest event.
// The instruction always returns state.PC+1 â€” it never stops the plan.
// The actual channel write is non-blocking; dropped events are silently discarded.
func EmitEvent(cfg EmitEventConfig) engine.Instruction {
	return engine.Instruction{
		Name: "emit_event[" + string(cfg.Kind) + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Pipeline == nil {
				return state.PC + 1
			}
			if ctx.TestMode {
				return state.PC + 1 // suppress ingest emission during test execution
			}

			// Snapshot event fields from the current context.
			// Snapshots are cheap value copies; no arena allocation needed.
			e := ingest.Event{
				TenantID:    ctx.TenantID,
				APIID:       ctx.ApiId,
				Kind:        cfg.Kind,
				TimestampNs: time.Now().UnixNano(),
			}

			// Session ID from slot.
			if cfg.SessionSlot >= 0 && cfg.SessionSlot < len(ctx.ByteSlots) {
				if raw := ctx.ByteSlots[cfg.SessionSlot]; len(raw) > 0 {
					e.SessionID = string(raw)
				}
			}

			// Model name from slot.
			if cfg.ModelSlot >= 0 && cfg.ModelSlot < len(ctx.ByteSlots) {
				if raw := ctx.ByteSlots[cfg.ModelSlot]; len(raw) > 0 {
					e.Model = string(raw)
				}
			}

			// Caller identity â€” always copy directly from context.
			e.CallerID = ctx.CallerID
			e.CallerKey = ctx.CallerKey

			// Token counts written to IntSlots by the llm_call step.
			if cfg.InputTokensSlot >= 0 && cfg.InputTokensSlot < len(ctx.IntSlots) {
				if v := ctx.IntSlots[cfg.InputTokensSlot]; v > 0 {
					e.InputTokens = int32(v)
				}
			}
			if cfg.OutputTokensSlot >= 0 && cfg.OutputTokensSlot < len(ctx.IntSlots) {
				if v := ctx.IntSlots[cfg.OutputTokensSlot]; v > 0 {
					e.OutputTokens = int32(v)
				}
			}

			// TxID from InternalTxID.
			if ctx.InternalTxID != ([2]uint64{}) {
				e.TxID = rctx.FormatTxID(ctx.InternalTxID)
			}

			// Payload from slot.
			if cfg.PayloadSlot >= 0 && cfg.PayloadSlot < len(ctx.ByteSlots) {
				if raw := ctx.ByteSlots[cfg.PayloadSlot]; len(raw) > 0 {
					// Determine number of sinks that will consume this event.
					numSinks := 1
					if cfg.Pipeline != nil {
						numSinks = cfg.Pipeline.NumSinksForKind(cfg.Kind)
					}
					if numSinks == 0 {
						numSinks = 1 // safety fallback
					}
					// SetPayload handles inline (â‰¤128B) vs heap storage automatically.
					// For heap storage, ref count is pre-set to numSinks.
					e.SetPayload(raw, numSinks)
				}
			}

			if cfg.Deferred {
				// Schedule emission after the HTTP response is committed.
				// Capture e by value so the closure owns its own copy.
				captured := e
				pipeline := cfg.Pipeline
				ctx.AfterResponse = append(ctx.AfterResponse, func() {
					pipeline.Emit(captured)
				})
			} else {
				cfg.Pipeline.Emit(e)
			}

			return state.PC + 1
		},
	}
}
