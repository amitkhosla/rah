package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
)

// TraceCaptureStep emits the value of slotIdx into the instruction trace output
// under the given field name. It is a no-op when tracing is not active.
// Inserted by the compiler whenever a step has trace_capture: true; the instruction
// immediately follows the step it annotates so it appears as a sibling event in
// the trace timeline with the captured value visible in the Output field.
func TraceCaptureStep(slotIdx int, fieldName string) engine.Instruction {
	return engine.Instruction{
		Name: "trace_capture[" + fieldName + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if slotIdx >= 0 && slotIdx < len(ctx.ByteSlots) {
				val := ctx.ByteSlots[slotIdx]
				if len(val) > 0 {
					state.AddTraceAttr(fieldName, string(val))
				}
			}
			return state.PC + 1
		},
	}
}
