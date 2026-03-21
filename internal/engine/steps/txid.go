package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
	"unsafe"
)

// StoreInternalTxID formats the gateway's internal transaction ID and stores
// it in ByteSlots[slotIdx]. Call this in an API flow to expose the TX ID
// (e.g. to forward as X-Rah-Request-ID or include in log steps).
func StoreInternalTxID(slotIdx int) engine.Instruction {
	return engine.Instruction{
		Name: "STORE_INTERNAL_TX_ID",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			s := rctx.FormatTxID(ctx.InternalTxID)
			dst := ctx.Alloc(len(s))
			copy(dst, s)
			ctx.ByteSlots[slotIdx] = dst
			return state.PC + 1
		},
	}
}

// BindCorrelationID reads an incoming header (headerKey) into ByteSlots[slotIdx].
// If the header is absent and generateIfMissing is true, a new transaction ID
// is generated and stored instead.
//
// This is the customer-driven correlation ID path — wired into an API flow at
// registration time, not globally. It lets the gateway echo back X-Request-ID
// (or similar) from the caller, falling back to a generated ID when absent.
func BindCorrelationID(headerKey string, generateIfMissing bool, gen *rctx.TxIDGenerator, slotIdx int) engine.Instruction {
	return engine.Instruction{
		Name: "BIND_CORRELATION_ID",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if ctx.Request != nil {
				if val := ctx.Request.Header.Get(headerKey); val != "" {
					// Zero-copy reference into http.Request.Header memory.
					ctx.ByteSlots[slotIdx] = unsafe.Slice(unsafe.StringData(val), len(val))
					return state.PC + 1
				}
			}
			if generateIfMissing && gen != nil {
				s := rctx.FormatTxID(gen.Generate(ctx.Timing.StartNs))
				dst := ctx.Alloc(len(s))
				copy(dst, s)
				ctx.ByteSlots[slotIdx] = dst
			}
			return state.PC + 1
		},
	}
}
