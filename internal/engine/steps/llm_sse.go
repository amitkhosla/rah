package steps

import (
	"fmt"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// SSEEventConfig configures a send_sse_event instruction.
type SSEEventConfig struct {
	EventSlot int  // ByteSlots index for the event name (may be -1 for default "message")
	DataSlot  int  // ByteSlots index for the data payload
	IDSlot    int  // ByteSlots index for the optional event id (-1 = omit)
}

// SendSSEEvent writes a single SSE frame to ctx.Writer and flushes immediately.
// SSE frame format:
//   event: <name>\n     (only if EventSlot >= 0)
//   id: <id>\n          (only if IDSlot >= 0 and slot non-empty)
//   data: <payload>\n\n
//
// If ctx.Writer is nil or does not implement http.Flusher, the instruction is a no-op.
// ResponseStatus is NOT changed by this instruction (the caller sets it before the loop).
func SendSSEEvent(cfg SSEEventConfig) engine.Instruction {
	return engine.Instruction{
		Name: "SEND_SSE_EVENT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			w := ctx.GetWriter()
			if w == nil {
				return state.PC + 1
			}

			type flusher interface{ Flush() }

			// Build SSE frame into stack buffer — no heap alloc for typical payloads
			var buf [4096]byte
			n := 0

			write := func(s string) {
				n += copy(buf[n:], s)
			}

			// event: line
			if cfg.EventSlot >= 0 && cfg.EventSlot < len(ctx.ByteSlots) {
				if ev := ctx.ByteSlots[cfg.EventSlot]; len(ev) > 0 {
					write("event: ")
					n += copy(buf[n:], ev)
					write("\n")
				}
			}

			// id: line
			if cfg.IDSlot >= 0 && cfg.IDSlot < len(ctx.ByteSlots) {
				if id := ctx.ByteSlots[cfg.IDSlot]; len(id) > 0 {
					write("id: ")
					n += copy(buf[n:], id)
					write("\n")
				}
			}

			// data: line  (required)
			write("data: ")
			if cfg.DataSlot >= 0 && cfg.DataSlot < len(ctx.ByteSlots) {
				n += copy(buf[n:], ctx.ByteSlots[cfg.DataSlot])
			}
			write("\n\n")

			// If buf was too small fall back to fmt (rare, large payloads)
			if n >= len(buf) {
				data := ""
				if cfg.DataSlot >= 0 && cfg.DataSlot < len(ctx.ByteSlots) {
					data = string(ctx.ByteSlots[cfg.DataSlot])
				}
				eventName := "message"
				if cfg.EventSlot >= 0 && cfg.EventSlot < len(ctx.ByteSlots) {
					if ev := ctx.ByteSlots[cfg.EventSlot]; len(ev) > 0 {
						eventName = string(ev)
					}
				}
				frame := fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, data)
				_, _ = fmt.Fprint(w, frame)
			} else {
				_, _ = w.Write(buf[:n])
			}

			// Flush so the client receives the event immediately
			if f, ok := w.(flusher); ok {
				f.Flush()
			}

			return state.PC + 1
		},
	}
}
