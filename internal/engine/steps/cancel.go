package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
	"sync/atomic"
)

// detectAndMarkCancelled checks whether the client request context has been
// cancelled (disconnect or upstream timeout) and, if so, atomically marks the
// rctx.Context as cancelled. Returns true if the step should stop processing.
//
// Call this at IO boundaries (before upstream calls, before cache hits) to avoid
// wasting CPU and hitting backends on behalf of a disconnected client.
func detectAndMarkCancelled(ctx *rctx.Context) bool {
	if ctx.Request == nil {
		return false
	}
	select {
	case <-ctx.Request.Context().Done():
		atomic.StoreInt32(&ctx.Cancelled, 1)
		return true
	default:
		return false
	}
}

// checkCancelled returns true if ctx.Cancelled has already been set (by
// detectAndMarkCancelled or by a previous step that detected the disconnect).
// Cheap: single atomic load, no context check.
func checkCancelled(ctx *rctx.Context) bool {
	return atomic.LoadInt32(&ctx.Cancelled) != 0
}

// StopIfCancelled checks for a client disconnect and returns StopCancelled if
// detected. Intended as a one-liner guard at the top of IO-bound step Actions.
func StopIfCancelled(ctx *rctx.Context) (int16, bool) {
	if checkCancelled(ctx) || detectAndMarkCancelled(ctx) {
		return engine.StopCancelled, true
	}
	return 0, false
}
