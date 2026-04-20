package steps

import (
	"encoding/json"
	"net/url"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// EchoRequestStep writes all incoming request headers and query params as a
// JSON body. Useful for debugging and as a building block for custom responses.
func EchoRequestStep() engine.Instruction {
	return engine.Instruction{
		Name: "ECHO_REQUEST",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			type echoPayload struct {
				Headers map[string][]string `json:"headers"`
				Query   map[string][]string `json:"query"`
			}

			payload := echoPayload{}
			if ctx.Request != nil {
				payload.Headers = ctx.Request.Header
			}
			if len(ctx.RawQuery) > 0 {
				payload.Query, _ = url.ParseQuery(string(ctx.RawQuery))
			}

			ctx.ResponseBuffer, _ = json.Marshal(payload)
			ctx.IsBuffered = true
			return state.PC + 1
		},
	}
}

// SetResponseBodyStep sets the response body from ByteSlots[src].
// Customers can compose the body via concat/substring/etc. then emit it here.
func SetResponseBodyStep(src int) engine.Instruction {
	return engine.Instruction{
		Name: "SET_RESPONSE_BODY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ResponseBuffer = ctx.ByteSlots[src]
			ctx.IsBuffered = true
			return state.PC + 1
		},
	}
}

// SetResponseStatusStep sets the HTTP response status to a static code.
func SetResponseStatusStep(code int) engine.Instruction {
	return engine.Instruction{
		Name: "SET_RESPONSE_STATUS",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ResponseStatus = code
			return state.PC + 1
		},
	}
}

// SetConstStep writes a static string value (captured at bake time) into a ByteSlot.
// Use for injecting fixed system prompts, labels, or flags into the slot space.
func SetConstStep(value string, slot int) engine.Instruction {
	data := []byte(value) // captured once at bake time — zero runtime allocation
	return engine.Instruction{
		Name: "SET_CONST",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ByteSlots[slot] = data
			return state.PC + 1
		},
	}
}

// RespondStep writes ByteSlots[src] as the HTTP response body and stops the flow.
// It is a shorthand for set_response_body + return, commonly used as the last
// step in a proxy / LLM flow.
func RespondStep(src int) engine.Instruction {
	return engine.Instruction{
		Name: "RESPOND",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ResponseBuffer = ctx.ByteSlots[src]
			ctx.IsBuffered = true
			return engine.StopPlan
		},
	}
}

