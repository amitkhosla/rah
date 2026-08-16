package steps

import (
	"encoding/json"
	"net/url"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"strconv"
)

// SetStreamResponseBodyStep is emitted by the compiler as the FIRST instruction
// of every compiled flow. It sets ctx.StreamResponseBody once at flow start
// based on compile-time analysis: true = http_call will stream response body
// directly to the client socket (no slot capture), false = body is captured
// into a slot for downstream processing.
func SetStreamResponseBodyStep(stream bool) engine.Instruction {
	return engine.Instruction{
		Name: "SET_STREAM_RESPONSE_BODY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.StreamResponseBody = stream
			return state.PC + 1
		},
	}
}

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
			if !ctx.StreamResponseBody {
				ctx.ResponseBuffer = ctx.ByteSlots[src]
				ctx.IsBuffered = true
			}
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

// SetResponseStatusFromSlotStep sets the HTTP response status from IntSlots[slot].
func SetResponseStatusFromSlotStep(slot int) engine.Instruction {
	return engine.Instruction{
		Name: "SET_RESPONSE_STATUS_FROM_SLOT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			ctx.ResponseStatus = int(ctx.IntSlots[slot])
			return state.PC + 1
		},
	}
}

// MapStatusStep reads an HTTP status code from IntSlots[slot], looks it up in the
// mappings table, and writes the remapped status to the response. If the incoming
// status has no mapping, defaultStatus applies (use -1 to mean "pass"/unchanged).
func MapStatusStep(slot int, mappings map[int]int, defaultStatus int) engine.Instruction {
	return engine.Instruction{
		Name: "MAP_STATUS",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			sourceStatus := int(ctx.IntSlots[slot])
			if mapped, ok := mappings[sourceStatus]; ok {
				ctx.ResponseStatus = mapped
			} else if defaultStatus != -1 {
				ctx.ResponseStatus = defaultStatus
			}
			// If defaultStatus == -1 and no mapping found, leave ctx.ResponseStatus unchanged
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

// RespondTokenCountStep writes {"input_tokens": N} where N is IntSlots[intSlot].
// Implements the Anthropic /v1/messages/count_tokens endpoint so callers like
// Claude Code can check context size without getting a 404.
func RespondTokenCountStep(intSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "RESPOND_TOKEN_COUNT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			var n int64
			if intSlot >= 0 && intSlot < len(ctx.IntSlots) {
				n = ctx.IntSlots[intSlot]
			}
			buf := make([]byte, 0, 32)
			buf = append(buf, `{"input_tokens":`...)
			buf = strconv.AppendInt(buf, n, 10)
			buf = append(buf, '}')
			ctx.ResponseBuffer = buf
			ctx.IsBuffered = true
			return engine.StopPlan
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
			if !ctx.StreamResponseBody {
				ctx.ResponseBuffer = ctx.ByteSlots[src]
				ctx.IsBuffered = true
			}
			// if StreamResponseBody = true, http_call already piped body to client
			return engine.StopPlan
		},
	}
}

// RemoveResponseHeaderStep removes a response header by name.
// Appends a HeaderMutation with Op=1 to ctx.ResponseHeaders.
func RemoveResponseHeaderStep(name string) engine.Instruction {
	nameBytes := []byte(name)
	return engine.Instruction{
		Name: "REMOVE_RESPONSE_HEADER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if ctx.ResHeaderCount < len(ctx.ResponseHeaders) {
				ctx.ResponseHeaders[ctx.ResHeaderCount] = rctx.HeaderMutation{
					Key: nameBytes,
					Op:  1, // Remove
				}
				ctx.ResHeaderCount++
			}
			return state.PC + 1
		},
	}
}

