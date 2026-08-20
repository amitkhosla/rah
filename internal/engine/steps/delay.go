package steps

import (
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// DelayStep pauses the executing flow for a fixed or dynamic number of milliseconds.
//
// Parameters (resolved at compile/bake time):
//   - staticMs: duration in ms; used when msSlot < 0.
//   - msSlot:   IntSlots index; when >= 0, the slot value overrides staticMs.
//
// Cancellation: if the underlying HTTP request is cancelled (client disconnect,
// upstream timeout) before the timer fires, the flow stops with StopCancelled
// rather than waiting out the full duration. Scheduled/background flows have no
// request context and sleep unconditionally.
//
// A resolved duration ≤ 0 is a no-op.
//
// Intended use: rate-limiting outbound API calls. To wait until the next N-second
// window boundary, compute the remaining ms via mod/sub on the current unix_ms
// timestamp and pass the result via msSlot.
func DelayStep(staticMs int64, msSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "DELAY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ms := staticMs
			if msSlot >= 0 && msSlot < len(ctx.IntSlots) {
				ms = ctx.IntSlots[msSlot]
			}
			if ms <= 0 {
				return state.PC + 1
			}

			// Bail early if the client already disconnected before we start sleeping.
			if stop, halt := StopIfCancelled(ctx); halt {
				return stop
			}

			timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
			defer timer.Stop()

			if ctx.Request != nil {
				// HTTP request path: race the timer against client disconnect.
				select {
				case <-timer.C:
				case <-ctx.Request.Context().Done():
					atomic.StoreInt32(&ctx.Cancelled, 1)
					return engine.StopCancelled
				}
			} else {
				// Scheduled or background flow: no HTTP context, sleep unconditionally.
				<-timer.C
			}

			return state.PC + 1
		},
	}
}
