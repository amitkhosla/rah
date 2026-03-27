package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
)

// EarlyReturn terminates flow execution immediately with a deliberate response.
// This is an INTENTIONAL stop (ctx.Failed remains false), not an error condition.
// status: HTTP response code to set (e.g. 200, 201, 400, 401, 403).
// bodySlot: index into ctx.ByteSlots to use as the response body. -1 = use staticBody.
// staticBody: used when bodySlot == -1; may be nil for an empty body.
func EarlyReturn(status int, bodySlot int, staticBody []byte) engine.Instruction {
	return engine.Instruction{
		Name: "early_return",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ResponseStatus = status
			if bodySlot >= 0 && bodySlot < len(ctx.ByteSlots) {
				ctx.ResponseBuffer = ctx.ByteSlots[bodySlot]
			} else if staticBody != nil {
				ctx.ResponseBuffer = staticBody
			}
			ctx.IsBuffered = true
			ctx.Failed = false // intentional stop, not an error
			return engine.StopPlan
		},
	}
}

// Fail marks the context as failed with an explicit code and message, then stops execution.
// This is for EXPLICIT error injection from flow definitions (e.g. returning a 403
// after a business-logic check). Steps that fail due to runtime errors set Failed
// themselves before returning StopPlan.
// code: application-level error code (mirrors HTTP or custom 1xxx codes).
// msgSlot: index into ctx.ByteSlots for the error message. -1 = use staticMsg.
// staticMsg: used when msgSlot == -1; may be nil.
func Fail(code int16, msgSlot int, staticMsg []byte) engine.Instruction {
	return engine.Instruction{
		Name: "fail",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.Failed = true
			ctx.ErrorCode = code
			if msgSlot >= 0 && msgSlot < len(ctx.ByteSlots) {
				ctx.ErrorMsg = ctx.ByteSlots[msgSlot]
			} else {
				ctx.ErrorMsg = staticMsg
			}
			return engine.StopPlan
		},
	}
}

// CaptureError copies the current error state (code + message) into ByteSlots
// so downstream steps can inspect or log it. Execution always continues.
// codeSlot: slot to write a decimal string of ctx.ErrorCode into. -1 = skip.
// msgSlot:  slot to write ctx.ErrorMsg into. -1 = skip.
// After capture, ctx.Failed and ctx.ErrorCode are cleared (error is "handled").
func CaptureError(codeSlot int, msgSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "capture_error",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if codeSlot >= 0 && codeSlot < len(ctx.ByteSlots) {
				code := ctx.ErrorCode
				// Write decimal digits of code into arena — max 5 chars for int16
				var buf [6]byte
				n := 0
				if code < 0 {
					buf[n] = '-'
					n++
					code = -code
				}
				if code == 0 {
					buf[n] = '0'
					n++
				} else {
					start := n
					for code > 0 {
						buf[n] = byte('0' + code%10)
						n++
						code /= 10
					}
					// reverse digits
					for i, j := start, n-1; i < j; i, j = i+1, j-1 {
						buf[i], buf[j] = buf[j], buf[i]
					}
				}
				s := ctx.Alloc(n)
				copy(s, buf[:n])
				ctx.ByteSlots[codeSlot] = s
			}
			if msgSlot >= 0 && msgSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[msgSlot] = ctx.ErrorMsg
			}
			ctx.Failed = false
			ctx.ErrorCode = 0
			ctx.ErrorMsg = nil
			return state.PC + 1
		},
	}
}

// WrapOnErrorContinue wraps an instruction so that if it signals failure
// (returns StopPlan AND ctx.Failed==true), the error is cleared and execution
// continues to the next instruction. If the step succeeds or returns any other
// PC, the result is passed through unchanged.
// Used by the compiler when a step has on_error: "continue".
func WrapOnErrorContinue(inner engine.Instruction) engine.Instruction {
	return engine.Instruction{
		Name: inner.Name + "|on_err:continue",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			savedPC := state.PC
			result := inner.Action(ctx, state)
			if result == engine.StopPlan && ctx.Failed {
				ctx.Failed = false
				ctx.ErrorCode = 0
				ctx.ErrorMsg = nil
				return savedPC + 1
			}
			return result
		},
	}
}

// WrapOnErrorJump wraps an instruction so that if it signals failure, execution
// jumps to errFlowPC (the absolute entry of an error-handler flow).
// ctx.Failed is cleared before the jump so the handler starts with a clean slate;
// ErrorCode and ErrorMsg are preserved so the handler can inspect them.
// Used by the compiler when a step has on_error: "jump:<flow_name>".
func WrapOnErrorJump(inner engine.Instruction, errFlowPC int16) engine.Instruction {
	return engine.Instruction{
		Name: inner.Name + "|on_err:jump",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			result := inner.Action(ctx, state)
			if result == engine.StopPlan && ctx.Failed {
				ctx.Failed = false // handler starts clean; code/msg preserved for inspection
				return errFlowPC
			}
			return result
		},
	}
}

// WrapOnErrorStatus wraps an instruction so that if it signals failure,
// the response is set to the given status + body and execution stops cleanly
// (not as an error — Failed is cleared so observability doesn't log it as unhandled).
// Used by the compiler when a step has on_error: "status:<code>".
func WrapOnErrorStatus(inner engine.Instruction, httpStatus int, body []byte) engine.Instruction {
	return engine.Instruction{
		Name: inner.Name + "|on_err:status",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			result := inner.Action(ctx, state)
			if result == engine.StopPlan && ctx.Failed {
				ctx.ResponseStatus = httpStatus
				if body != nil {
					ctx.ResponseBuffer = body
					ctx.IsBuffered = true
				}
				ctx.Failed = false
				ctx.ErrorCode = 0
				ctx.ErrorMsg = nil
				return engine.StopPlan
			}
			return result
		},
	}
}
