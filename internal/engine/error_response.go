package engine

import (
	"encoding/json"
	"sync/atomic"

	"github.com/amitkhosla/rah/internal/rctx"
)

var (
	genericErrBodyPtr atomic.Pointer[[]byte]
	genericErrStatus  atomic.Int32
)

func init() {
	ConfigureGenericError(500, "request processing failed")
}

// ConfigureGenericError pre-builds the static JSON error body and status code.
// Call once at startup before serving traffic. Thread-safe: uses atomic swap.
func ConfigureGenericError(status int, message string) {
	b, _ := json.Marshal(map[string]string{"error": message})
	genericErrBodyPtr.Store(&b)
	genericErrStatus.Store(int32(status))
}

// applyGenericError writes the pre-built static error response to ctx.
// Called by the engine when ctx.Failed is true and no step set a >=400 status.
// The JSON body is built once at startup and copied here — no per-request marshaling.
func applyGenericError(ctx *rctx.Context) {
	body := *genericErrBodyPtr.Load()
	status := int(genericErrStatus.Load())
	// Honour a specific HTTP-range error code set by the failing step.
	if ctx.ErrorCode >= 400 && ctx.ErrorCode < 600 {
		status = int(ctx.ErrorCode)
	}
	ctx.ResponseStatus = status
	ctx.ResponseBuffer = append(ctx.ResponseBuffer[:0], body...)
	ctx.IsBuffered = true
	ctx.SetResponseHeader([]byte("Content-Type"), []byte("application/json"))
	txID := rctx.FormatTxID(ctx.InternalTxID)
	ctx.SetResponseHeader([]byte("X-Tx-Id"), []byte(txID))
}
