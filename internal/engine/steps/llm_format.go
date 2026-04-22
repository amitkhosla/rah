package steps

import (
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// ParseMessageFormatConfig holds the bake-time slot assignments for ParseMessageFormat.
type ParseMessageFormatConfig struct {
	BodySlot        int // input: ByteSlots index with the raw JSON request body
	MessagesSlot    int // output: ByteSlots index for JSON-encoded []CanonicalMessage
	SystemSlot      int // output: ByteSlots index for extracted system prompt (-1 = don't extract)
	DetectedFmtSlot int // output: ByteSlots index for detected format string (-1 = don't store)
}

// ParseMessageFormat returns an Instruction that reads a provider-specific JSON
// body from cfg.BodySlot, detects the format (anthropic/openai/gemini), parses it
// into []CanonicalMessage, and writes results to the configured output slots.
func ParseMessageFormat(cfg ParseMessageFormatConfig) engine.Instruction {
	return engine.Instruction{
		Name: "PARSE_MESSAGE_FORMAT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			body := ctx.ByteSlots[cfg.BodySlot]
			if len(body) == 0 {
				return state.PC + 1
			}

			// Parse top-level keys only to detect format.
			var topLevel map[string]json.RawMessage
			if err := json.Unmarshal(body, &topLevel); err != nil {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				msg := []byte("invalid JSON body")
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			_, hasContents  := topLevel["contents"]
			_, hasMessages  := topLevel["messages"]
			_, hasSystem    := topLevel["system"]
			_, hasMaxTokens := topLevel["max_tokens"] // Anthropic requires max_tokens; OpenAI does not

			var format string
			switch {
			case hasContents:
				format = "gemini"
			case hasMessages && hasSystem:
				format = "anthropic"
			case hasMessages && hasMaxTokens:
				// Anthropic format with no system prompt — max_tokens is required in
				// the Anthropic API but optional in OpenAI, so its presence is a
				// reliable signal that the caller is using the Anthropic wire format.
				format = "anthropic"
			case hasMessages:
				format = "openai"
			default:
				// Not a recognised LLM request format — skip silently.
				// This allows parse_message_format to be placed in generic flows
				// where the request body may not be an LLM payload.
				return state.PC + 1
			}

			var messages []CanonicalMessage
			var systemText string

			switch format {
			case "anthropic":
				// Content field can be a plain string or an array of content blocks:
				// [{"type":"text","text":"..."},{"type":"image",...}]
				type anthropicContentBlock struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				type anthropicMsg struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				}
				// System can also be a string or array of content blocks.
				var req struct {
					System   json.RawMessage `json:"system"`
					Messages []anthropicMsg  `json:"messages"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					ctx.ResponseStatus = 400
					ctx.Failed = true
					msg := []byte("failed to parse anthropic request body")
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				// Resolve system — string or []content_block
				if len(req.System) > 0 && req.System[0] == '"' {
					_ = json.Unmarshal(req.System, &systemText)
				} else if len(req.System) > 0 && req.System[0] == '[' {
					var blocks []anthropicContentBlock
					if json.Unmarshal(req.System, &blocks) == nil {
						for _, b := range blocks {
							if b.Type == "text" {
								systemText += b.Text
							}
						}
					}
				}
				// Resolve each message — content can be string or []content_block
				extractAnthropicText := func(raw json.RawMessage) string {
					if len(raw) == 0 {
						return ""
					}
					if raw[0] == '"' {
						var s string
						json.Unmarshal(raw, &s) //nolint:errcheck
						return s
					}
					if raw[0] == '[' {
						var blocks []anthropicContentBlock
						if json.Unmarshal(raw, &blocks) == nil {
							var sb strings.Builder
							for _, b := range blocks {
								if b.Type == "text" {
									sb.WriteString(b.Text)
								}
							}
							return sb.String()
						}
					}
					return string(raw)
				}
				messages = make([]CanonicalMessage, 0, len(req.Messages))
				for _, m := range req.Messages {
					text := extractAnthropicText(m.Content)
					switch m.Role {
					case "user":
						messages = append(messages, CanonicalMessage{Role: RoleUser, Content: text})
					case "assistant":
						messages = append(messages, CanonicalMessage{Role: RoleAssistant, Content: text})
					}
				}

			case "openai":
				type openAIMsg struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				var req struct {
					Messages []openAIMsg `json:"messages"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					ctx.ResponseStatus = 400
					ctx.Failed = true
					msg := []byte("failed to parse openai request body")
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				systemExtracted := false
				messages = make([]CanonicalMessage, 0, len(req.Messages))
				for _, m := range req.Messages {
					switch m.Role {
					case "system":
						if !systemExtracted {
							systemText = m.Content
							systemExtracted = true
						}
					case "user":
						messages = append(messages, CanonicalMessage{Role: RoleUser, Content: m.Content})
					case "assistant":
						messages = append(messages, CanonicalMessage{Role: RoleAssistant, Content: m.Content})
					}
				}

			case "gemini":
				type geminiPart struct {
					Text string `json:"text"`
				}
				type geminiContent struct {
					Role  string       `json:"role"`
					Parts []geminiPart `json:"parts"`
				}
				var req struct {
					Contents          []geminiContent `json:"contents"`
					SystemInstruction *struct {
						Parts []geminiPart `json:"parts"`
					} `json:"systemInstruction"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					ctx.ResponseStatus = 400
					ctx.Failed = true
					msg := []byte("failed to parse gemini request body")
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				if req.SystemInstruction != nil && len(req.SystemInstruction.Parts) > 0 {
					systemText = req.SystemInstruction.Parts[0].Text
				}
				messages = make([]CanonicalMessage, 0, len(req.Contents))
				for _, c := range req.Contents {
					var role MessageRole
					switch c.Role {
					case "user":
						role = RoleUser
					case "model":
						role = RoleAssistant
					default:
						continue
					}
					var text string
					if len(c.Parts) > 0 {
						text = c.Parts[0].Text
					}
					messages = append(messages, CanonicalMessage{Role: role, Content: text})
				}
			}

			// JSON-encode canonical messages and write to MessagesSlot.
			encoded, err := json.Marshal(messages)
			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				msg := []byte("failed to encode canonical messages")
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}
			msgsSlice := ctx.Alloc(len(encoded))
			copy(msgsSlice, encoded)
			ctx.ByteSlots[cfg.MessagesSlot] = msgsSlice

			// Write system text to SystemSlot if configured.
			if cfg.SystemSlot >= 0 && systemText != "" {
				sysSlice := ctx.Alloc(len(systemText))
				copy(sysSlice, systemText)
				ctx.ByteSlots[cfg.SystemSlot] = sysSlice
			}

			// Write detected format to DetectedFmtSlot if configured.
			if cfg.DetectedFmtSlot >= 0 {
				fmtSlice := ctx.Alloc(len(format))
				copy(fmtSlice, format)
				ctx.ByteSlots[cfg.DetectedFmtSlot] = fmtSlice
			}

			return state.PC + 1
		},
	}
}

// FormatResponseConfig is resolved once at bake time and captured in the
// FormatResponse instruction closure.
type FormatResponseConfig struct {
	// Input slots
	ContentSlot      int // ByteSlot: LLM response content text (from llm_call ResultSlot)
	StopReasonSlot   int // ByteSlot: canonical stop reason (-1 = use per-format default)
	InputTokensSlot  int // IntSlot: input token count (-1 = 0)
	OutputTokensSlot int // IntSlot: output token count (-1 = 0)
	FormatSlot       int // ByteSlot: caller's expected format "anthropic"|"openai"|"gemini" (-1 = use Format field)
	ModelSlot        int // ByteSlot: runtime model slug override (-1 = use Model field)

	// StreamSlot: if >= 0, read from ByteSlots[StreamSlot]; when the value is
	// "true" or "1", assemble an SSE streaming response instead of plain JSON.
	// Set to -1 (default) to always return plain JSON.
	StreamSlot int

	// Bake-time static values (used when corresponding slot < 0)
	Model  string // model slug
	Format string // "anthropic" | "openai" | "gemini"

	// Output
	ResultSlot int // ByteSlot: write complete formatted JSON response here
}

// respBufPool recycles per-request response assembly buffers.
// Default capacity 4 KB covers most responses; large responses grow once.
var respBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 4096)
		return &b
	},
}

// jsonEscapeNeeded[c] is true when byte c requires escaping inside a JSON string.
var jsonEscapeNeeded [256]bool

// jsonEscapeSingle[c] is the single character written after the backslash for byte c.
// Zero means use \u00XX encoding.
var jsonEscapeSingle [256]byte

const fmtHexChars = "0123456789abcdef"

func init() {
	for i := 0; i < 0x20; i++ {
		jsonEscapeNeeded[i] = true
	}
	jsonEscapeNeeded['"'] = true
	jsonEscapeNeeded['\\'] = true
	jsonEscapeSingle['"'] = '"'
	jsonEscapeSingle['\\'] = '\\'
	jsonEscapeSingle['\n'] = 'n'
	jsonEscapeSingle['\r'] = 'r'
	jsonEscapeSingle['\t'] = 't'
}

// appendJSONString appends a JSON-encoded string (with surrounding quotes) to dst.
// Uses a 256-byte lookup table; no heap allocation.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !jsonEscapeNeeded[c] {
			continue
		}
		dst = append(dst, s[start:i]...)
		if sc := jsonEscapeSingle[c]; sc != 0 {
			dst = append(dst, '\\', sc)
		} else {
			dst = append(dst, '\\', 'u', '0', '0', fmtHexChars[c>>4], fmtHexChars[c&0xF])
		}
		start = i + 1
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// appendTxIDHex encodes 12 bytes of InternalTxID as 24 lowercase hex characters.
func appendTxIDHex(dst []byte, txid [2]uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], txid[0])
	for _, c := range b[:6] {
		dst = append(dst, fmtHexChars[c>>4], fmtHexChars[c&0xF])
	}
	binary.LittleEndian.PutUint64(b[:], txid[1])
	for _, c := range b[:6] {
		dst = append(dst, fmtHexChars[c>>4], fmtHexChars[c&0xF])
	}
	return dst
}

// mapStopReason maps a canonical stop reason to the target format's expected string.
// Switch compiles to a jump table (~2 ns).
func mapStopReason(reason, format string) string {
	switch format {
	case "anthropic":
		switch reason {
		case "end_turn":
			return "end_turn"
		case "max_tokens":
			return "max_tokens"
		case "tool_use":
			return "tool_use"
		case "stop", "STOP", "finish":
			return "end_turn"
		case "length", "MAX_TOKENS":
			return "max_tokens"
		case "tool_calls":
			return "tool_use"
		default:
			if reason == "" {
				return "end_turn"
			}
			return reason
		}
	case "gemini":
		switch reason {
		case "STOP":
			return "STOP"
		case "MAX_TOKENS":
			return "MAX_TOKENS"
		case "end_turn", "stop", "finish":
			return "STOP"
		case "max_tokens", "length":
			return "MAX_TOKENS"
		case "tool_use", "tool_calls":
			return "OTHER"
		default:
			if reason == "" {
				return "STOP"
			}
			return reason
		}
	default: // "openai" + anything unrecognised
		switch reason {
		case "stop":
			return "stop"
		case "length":
			return "length"
		case "tool_calls":
			return "tool_calls"
		case "end_turn", "STOP", "finish":
			return "stop"
		case "max_tokens", "MAX_TOKENS":
			return "length"
		case "tool_use":
			return "tool_calls"
		default:
			if reason == "" {
				return "stop"
			}
			return reason
		}
	}
}

// assembleAnthropicSSE writes a complete Anthropic streaming response as SSE events to dst.
// Claude Code (and other clients that send stream:true) expect this format.
// The entire response is assembled in one buffer — no goroutines needed.
func assembleAnthropicSSE(dst []byte, txid [2]uint64, content, stopReason, model string, in, out int64) []byte {
	// event: message_start
	dst = append(dst, "event: message_start\ndata: "...)
	dst = append(dst, `{"type":"message_start","message":{"id":"msg_`...)
	dst = appendTxIDHex(dst, txid)
	dst = append(dst, `","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":`...)
	dst = strconv.AppendInt(dst, in, 10)
	dst = append(dst, `,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`...)
	dst = append(dst, "\n\n"...)

	// event: content_block_start
	dst = append(dst, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"...)

	// event: ping
	dst = append(dst, "event: ping\ndata: {\"type\":\"ping\"}\n\n"...)

	// event: content_block_delta
	dst = append(dst, "event: content_block_delta\ndata: "...)
	dst = append(dst, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`...)
	dst = appendJSONString(dst, content)
	dst = append(dst, "}}\n\n"...)

	// event: content_block_stop
	dst = append(dst, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"...)

	// event: message_delta
	dst = append(dst, "event: message_delta\ndata: "...)
	dst = append(dst, `{"type":"message_delta","delta":{"stop_reason":`...)
	dst = appendJSONString(dst, stopReason)
	dst = append(dst, `,"stop_sequence":null},"usage":{"output_tokens":`...)
	dst = strconv.AppendInt(dst, out, 10)
	dst = append(dst, "}}\n\n"...)

	// event: message_stop
	dst = append(dst, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"...)

	return dst
}

// assembleOpenAISSE writes an OpenAI-compatible streaming response as SSE events to dst.
// Format: data: {...}\n\ndata: [DONE]\n\n
func assembleOpenAISSE(dst []byte, txid [2]uint64, content, finishReason, model string, in, out int64) []byte {
	// data: role delta (first chunk)
	dst = append(dst, "data: "...)
	dst = append(dst, `{"id":"chatcmpl-`...)
	dst = appendTxIDHex(dst, txid)
	dst = append(dst, `","object":"chat.completion.chunk","created":`...)
	dst = strconv.AppendInt(dst, time.Now().Unix(), 10)
	dst = append(dst, `,"model":`...)
	dst = appendJSONString(dst, model)
	dst = append(dst, `,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`...)
	dst = append(dst, "\n\n"...)

	// data: content delta
	dst = append(dst, "data: "...)
	dst = append(dst, `{"id":"chatcmpl-`...)
	dst = appendTxIDHex(dst, txid)
	dst = append(dst, `","object":"chat.completion.chunk","created":`...)
	dst = strconv.AppendInt(dst, time.Now().Unix(), 10)
	dst = append(dst, `,"model":`...)
	dst = appendJSONString(dst, model)
	dst = append(dst, `,"choices":[{"index":0,"delta":{"content":`...)
	dst = appendJSONString(dst, content)
	dst = append(dst, `},"finish_reason":null}]}`...)
	dst = append(dst, "\n\n"...)

	// data: finish chunk with usage
	dst = append(dst, "data: "...)
	dst = append(dst, `{"id":"chatcmpl-`...)
	dst = appendTxIDHex(dst, txid)
	dst = append(dst, `","object":"chat.completion.chunk","created":`...)
	dst = strconv.AppendInt(dst, time.Now().Unix(), 10)
	dst = append(dst, `,"model":`...)
	dst = appendJSONString(dst, model)
	dst = append(dst, `,"choices":[{"index":0,"delta":{},"finish_reason":`...)
	dst = appendJSONString(dst, finishReason)
	dst = append(dst, `}],"usage":{"prompt_tokens":`...)
	dst = strconv.AppendInt(dst, in, 10)
	dst = append(dst, `,"completion_tokens":`...)
	dst = strconv.AppendInt(dst, out, 10)
	dst = append(dst, `,"total_tokens":`...)
	dst = strconv.AppendInt(dst, in+out, 10)
	dst = append(dst, "}}"...)
	dst = append(dst, "\n\n"...)

	dst = append(dst, "data: [DONE]\n\n"...)
	return dst
}

// assembleAnthropic writes a complete Anthropic Messages API response JSON to dst.
func assembleAnthropic(dst []byte, txid [2]uint64, content, stopReason, model string, in, out int64) []byte {
	dst = append(dst, `{"id":"msg_`...)
	dst = appendTxIDHex(dst, txid)
	dst = append(dst, `","type":"message","role":"assistant","content":[{"type":"text","text":`...)
	dst = appendJSONString(dst, content)
	dst = append(dst, `}],"model":`...)
	dst = appendJSONString(dst, model)
	dst = append(dst, `,"stop_reason":`...)
	dst = appendJSONString(dst, stopReason)
	dst = append(dst, `,"stop_sequence":null,"usage":{"input_tokens":`...)
	dst = strconv.AppendInt(dst, in, 10)
	dst = append(dst, `,"output_tokens":`...)
	dst = strconv.AppendInt(dst, out, 10)
	return append(dst, `}}`...)
}

// assembleOpenAI writes a complete OpenAI Chat Completions API response JSON to dst.
func assembleOpenAI(dst []byte, txid [2]uint64, content, finishReason, model string, in, out int64) []byte {
	dst = append(dst, `{"id":"chatcmpl-`...)
	dst = appendTxIDHex(dst, txid)
	dst = append(dst, `","object":"chat.completion","created":`...)
	dst = strconv.AppendInt(dst, time.Now().Unix(), 10)
	dst = append(dst, `,"model":`...)
	dst = appendJSONString(dst, model)
	dst = append(dst, `,"choices":[{"index":0,"message":{"role":"assistant","content":`...)
	dst = appendJSONString(dst, content)
	dst = append(dst, `},"finish_reason":`...)
	dst = appendJSONString(dst, finishReason)
	dst = append(dst, `,"logprobs":null}],"usage":{"prompt_tokens":`...)
	dst = strconv.AppendInt(dst, in, 10)
	dst = append(dst, `,"completion_tokens":`...)
	dst = strconv.AppendInt(dst, out, 10)
	dst = append(dst, `,"total_tokens":`...)
	dst = strconv.AppendInt(dst, in+out, 10)
	return append(dst, `}}`...)
}

// assembleGemini writes a complete Gemini generateContent API response JSON to dst.
func assembleGemini(dst []byte, content, finishReason, model string, in, out int64) []byte {
	dst = append(dst, `{"candidates":[{"content":{"parts":[{"text":`...)
	dst = appendJSONString(dst, content)
	dst = append(dst, `}],"role":"model"},"finishReason":`...)
	dst = appendJSONString(dst, finishReason)
	dst = append(dst, `,"index":0}],"usageMetadata":{"promptTokenCount":`...)
	dst = strconv.AppendInt(dst, in, 10)
	dst = append(dst, `,"candidatesTokenCount":`...)
	dst = strconv.AppendInt(dst, out, 10)
	dst = append(dst, `,"totalTokenCount":`...)
	dst = strconv.AppendInt(dst, in+out, 10)
	dst = append(dst, `},"modelVersion":`...)
	dst = appendJSONString(dst, model)
	return append(dst, '}')
}

// FormatResponse returns an Instruction that converts a canonical LLM response
// into the wire-format JSON expected by the original caller.
//
// This enables transparent provider routing: a Claude CLI caller (Anthropic format)
// receives a properly-shaped Anthropic envelope even when the gateway routed the
// request to Gemini or OpenAI internally.
//
// Hot-path design:
//   - Template assembly via append — no encoding/json overhead (~10× faster)
//   - sync.Pool for the output buffer — zero allocation per request
//   - 256-byte lookup table for JSON string escaping
//   - Switch-based stop-reason mapping (~2 ns, jump table)
//   - strconv.AppendInt for integer fields — stack only
func FormatResponse(cfg FormatResponseConfig) engine.Instruction {
	return engine.Instruction{
		Name: "format_response",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read content text.
			var content string
			if cfg.ContentSlot >= 0 && cfg.ContentSlot < len(ctx.ByteSlots) {
				content = string(ctx.ByteSlots[cfg.ContentSlot])
			}

			// 2. Resolve target format (runtime slot overrides bake-time field).
			format := cfg.Format
			if cfg.FormatSlot >= 0 && cfg.FormatSlot < len(ctx.ByteSlots) {
				if f := ctx.ByteSlots[cfg.FormatSlot]; len(f) > 0 {
					format = string(f)
				}
			}
			if format == "" || format == "unknown" {
				format = "openai" // safest default — broadest client compatibility
			}

			// 3. Resolve model slug (runtime slot overrides bake-time field).
			model := cfg.Model
			if cfg.ModelSlot >= 0 && cfg.ModelSlot < len(ctx.ByteSlots) {
				if m := ctx.ByteSlots[cfg.ModelSlot]; len(m) > 0 {
					model = string(m)
				}
			}

			// 4. Map stop reason to target format's expected string.
			var rawStopReason string
			if cfg.StopReasonSlot >= 0 && cfg.StopReasonSlot < len(ctx.ByteSlots) {
				rawStopReason = string(ctx.ByteSlots[cfg.StopReasonSlot])
			}
			stopReason := mapStopReason(rawStopReason, format)

			// 5. Read token counts from IntSlots.
			var inputTokens, outputTokens int64
			if cfg.InputTokensSlot >= 0 && cfg.InputTokensSlot < len(ctx.IntSlots) {
				inputTokens = ctx.IntSlots[cfg.InputTokensSlot]
			}
			if cfg.OutputTokensSlot >= 0 && cfg.OutputTokensSlot < len(ctx.IntSlots) {
				outputTokens = ctx.IntSlots[cfg.OutputTokensSlot]
			}

			// 6. Detect streaming request.
			isStream := false
			if cfg.StreamSlot >= 0 && cfg.StreamSlot < len(ctx.ByteSlots) {
				v := ctx.ByteSlots[cfg.StreamSlot]
				isStream = string(v) == "true" || string(v) == "1"
			}

			// 7. Assemble format-specific response into a pooled buffer.
			bufPtr := respBufPool.Get().(*[]byte)
			buf := (*bufPtr)[:0]

			if isStream {
				// SSE streaming — emit format-specific events.
				// SSE is a transport wrapper; format selects which event schema to use.
				switch format {
				case "gemini":
					// Gemini streaming not yet implemented — fall back to plain JSON.
					buf = assembleGemini(buf, content, stopReason, model, inputTokens, outputTokens)
				case "openai":
					buf = assembleOpenAISSE(buf, ctx.InternalTxID, content, stopReason, model, inputTokens, outputTokens)
					ctx.SetResponseHeader([]byte("Content-Type"), []byte("text/event-stream"))
					ctx.SetResponseHeader([]byte("Cache-Control"), []byte("no-cache"))
					ctx.SetResponseHeader([]byte("X-Accel-Buffering"), []byte("no"))
				default: // "anthropic" + anything else
					buf = assembleAnthropicSSE(buf, ctx.InternalTxID, content, stopReason, model, inputTokens, outputTokens)
					ctx.SetResponseHeader([]byte("Content-Type"), []byte("text/event-stream"))
					ctx.SetResponseHeader([]byte("Cache-Control"), []byte("no-cache"))
					ctx.SetResponseHeader([]byte("X-Accel-Buffering"), []byte("no"))
				}
			} else {
				switch format {
				case "anthropic":
					buf = assembleAnthropic(buf, ctx.InternalTxID, content, stopReason, model, inputTokens, outputTokens)
				case "gemini":
					buf = assembleGemini(buf, content, stopReason, model, inputTokens, outputTokens)
				default: // "openai" + anything unrecognised
					buf = assembleOpenAI(buf, ctx.InternalTxID, content, stopReason, model, inputTokens, outputTokens)
				}
			}

			// 8. Copy assembled bytes into the arena (ctx owns the memory).
			if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				out := ctx.Alloc(len(buf))
				copy(out, buf)
				ctx.ByteSlots[cfg.ResultSlot] = out
			}

			// 9. Return buffer to pool immediately.
			*bufPtr = buf[:0]
			respBufPool.Put(bufPtr)

			return state.PC + 1
		},
	}
}
