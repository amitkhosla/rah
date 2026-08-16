package steps

import (
	"bytes"
	"encoding/json"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// CanonicalToolCall is the provider-agnostic representation of a tool call
// request from an LLM response.
type CanonicalToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	InputJSON json.RawMessage `json:"input"` // always a JSON object
}

// emptyToolCalls is the canonical JSON encoding of an empty tool call slice.
var emptyToolCalls = []byte("[]")

// â"€â"€â"€ parse_tool_calls â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// ParseToolCallsConfig holds the bake-time slot assignments for ParseToolCalls.
type ParseToolCallsConfig struct {
	// ResponseSlot: ByteSlot index holding the raw LLM response content string
	// (as written by llm_call — plain text or JSON content array)
	ResponseSlot int
	// ToolCallsSlot: ByteSlot index to write []CanonicalToolCall JSON (or [] if none)
	ToolCallsSlot int
	// CountSlot: IntSlot index to write the number of tool calls found (-1 = don't write)
	CountSlot int
	// HasToolCallsSlot: BoolSlot index to write true if any tool calls found (-1 = don't write)
	HasToolCallsSlot int
	// SourceFormat: "anthropic", "openai", or "" (auto-detect from structure)
	SourceFormat string
}

// ParseToolCalls returns an Instruction that extracts tool call requests from
// an LLM response stored in ByteSlots[cfg.ResponseSlot].
//
// For Anthropic responses, the content is expected to be a JSON array like:
//
//	[{"type":"text","text":"..."}, {"type":"tool_use","id":"...","name":"...","input":{...}}]
//
// The instruction updates ResponseSlot to contain only the concatenated text
// blocks so downstream steps receive clean text.
//
// For OpenAI responses, the instruction looks for a JSON object with a
// "tool_calls" key containing the function call array.
//
// Extracted tool calls are written as []CanonicalToolCall JSON to ToolCallsSlot.
// An empty array is written when no tool calls are found.
func ParseToolCalls(cfg ParseToolCallsConfig) engine.Instruction {
	return engine.Instruction{
		Name: "PARSE_TOOL_CALLS",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			raw := ctx.ByteSlots[cfg.ResponseSlot]
			if len(raw) == 0 {
				state.WriteSlot(ctx, cfg.ToolCallsSlot, emptyToolCalls)
				writeIntSlot(ctx, cfg.CountSlot, 0)
				writeBoolSlot(ctx, cfg.HasToolCallsSlot, false)
				return state.PC + 1
			}

			format := cfg.SourceFormat
			if format == "" {
				format = autoDetectToolFormat(raw)
			}

			var calls []CanonicalToolCall
			var textContent []byte

			switch format {
			case "anthropic":
				calls, textContent = parseAnthropicToolCalls(raw)
				// Update ResponseSlot to contain only the extracted text content.
				if textContent != nil {
					state.WriteSlot(ctx, cfg.ResponseSlot, textContent)
				}
			case "openai":
				calls = parseOpenAIToolCalls(raw)
			default:
				// Plain text or unrecognised format — no tool calls.
				calls = nil
			}

			if len(calls) == 0 {
				state.WriteSlot(ctx, cfg.ToolCallsSlot, emptyToolCalls)
				writeIntSlot(ctx, cfg.CountSlot, 0)
				writeBoolSlot(ctx, cfg.HasToolCallsSlot, false)
				return state.PC + 1
			}

			encoded, err := json.Marshal(calls)
			if err != nil {
				state.WriteSlot(ctx, cfg.ToolCallsSlot, emptyToolCalls)
				writeIntSlot(ctx, cfg.CountSlot, 0)
				writeBoolSlot(ctx, cfg.HasToolCallsSlot, false)
				return state.PC + 1
			}

			state.WriteSlot(ctx, cfg.ToolCallsSlot, encoded)
			writeIntSlot(ctx, cfg.CountSlot, int64(len(calls)))
			writeBoolSlot(ctx, cfg.HasToolCallsSlot, true)
			return state.PC + 1
		},
	}
}

// autoDetectToolFormat inspects raw bytes to determine the likely provider format.
// Returns "anthropic", "openai", or "" (unknown/plain text).
func autoDetectToolFormat(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}

	// Anthropic content array: starts with '[' and contains "type":"tool_use"
	if trimmed[0] == '[' {
		if bytes.Contains(trimmed, []byte(`"type":"tool_use"`)) ||
			bytes.Contains(trimmed, []byte(`"type": "tool_use"`)) {
			return "anthropic"
		}
		// Also check for any "type":"text" which hints at Anthropic content array
		if bytes.Contains(trimmed, []byte(`"type":"text"`)) ||
			bytes.Contains(trimmed, []byte(`"type": "text"`)) {
			return "anthropic"
		}
	}

	// OpenAI tool calls: JSON object with "tool_calls" key, or array with "function" key
	if trimmed[0] == '{' {
		if bytes.Contains(trimmed, []byte(`"tool_calls"`)) {
			return "openai"
		}
	}
	if trimmed[0] == '[' {
		if bytes.Contains(trimmed, []byte(`"function":{`) ) ||
			bytes.Contains(trimmed, []byte(`"function": {`)) {
			return "openai"
		}
	}

	return ""
}

// anthropicContentBlock is used for JSON decoding of Anthropic content array items.
type anthropicContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
}

// parseAnthropicToolCalls parses an Anthropic content array and returns
// (toolCalls, textContent). textContent is the concatenation of all text blocks.
// Returns (nil, nil) if parsing fails or there are no tool_use blocks.
func parseAnthropicToolCalls(raw []byte) ([]CanonicalToolCall, []byte) {
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, nil
	}

	var calls []CanonicalToolCall
	var textParts [][]byte

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, []byte(block.Text))
			}
		case "tool_use":
			input := block.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			calls = append(calls, CanonicalToolCall{
				ID:        block.ID,
				Name:      block.Name,
				InputJSON: input,
			})
		}
	}

	var textContent []byte
	if len(textParts) > 0 {
		textContent = bytes.Join(textParts, nil)
	} else {
		textContent = []byte{}
	}

	return calls, textContent
}

// openAIToolCallItem is used for JSON decoding of an OpenAI tool_calls array item.
type openAIToolCallItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAIResponseWithToolCalls is used to decode an OpenAI response object that
// may contain a "tool_calls" field.
type openAIResponseWithToolCalls struct {
	ToolCalls []openAIToolCallItem `json:"tool_calls"`
}

// parseOpenAIToolCalls parses OpenAI tool call format from raw bytes.
// raw may be a JSON object with a "tool_calls" key, or a raw tool_calls array.
func parseOpenAIToolCalls(raw []byte) []CanonicalToolCall {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}

	var items []openAIToolCallItem

	if trimmed[0] == '{' {
		// Full response object — extract tool_calls field.
		var resp openAIResponseWithToolCalls
		if err := json.Unmarshal(trimmed, &resp); err != nil {
			return nil
		}
		items = resp.ToolCalls
	} else if trimmed[0] == '[' {
		// Direct tool_calls array.
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil
		}
	}

	if len(items) == 0 {
		return nil
	}

	calls := make([]CanonicalToolCall, 0, len(items))
	for _, item := range items {
		// Validate the arguments string is valid JSON; fall back to "{}" if not.
		args := json.RawMessage(item.Function.Arguments)
		if len(args) == 0 || !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		calls = append(calls, CanonicalToolCall{
			ID:        item.ID,
			Name:      item.Function.Name,
			InputJSON: args,
		})
	}
	return calls
}

// writeIntSlot writes val to ctx.IntSlots[slot] when slot >= 0 and in bounds.
func writeIntSlot(ctx *rctx.Context, slot int, val int64) {
	if slot >= 0 && slot < len(ctx.IntSlots) {
		ctx.IntSlots[slot] = val
	}
}

// writeBoolSlot writes val to ctx.BoolSlots[slot] when slot >= 0 and in bounds.
func writeBoolSlot(ctx *rctx.Context, slot int, val bool) {
	if slot >= 0 && slot < len(ctx.BoolSlots) {
		ctx.BoolSlots[slot] = val
	}
}

// â"€â"€â"€ append_tool_result â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// AppendToolResultConfig holds the bake-time slot assignments for AppendToolResult.
type AppendToolResultConfig struct {
	// HistorySlot: ByteSlot index holding []CanonicalMessage JSON
	HistorySlot int
	// ToolCallIDSlot: ByteSlot index holding the tool call ID string
	ToolCallIDSlot int
	// ResultSlot: ByteSlot index holding the tool result content (plain text or JSON)
	ResultSlot int
	// TargetFormat: "anthropic" or "openai" — determines how to encode the tool result
	TargetFormat string
}

// AppendToolResult returns an Instruction that appends a tool execution result
// back into the conversation history stored in ByteSlots[cfg.HistorySlot].
//
// For Anthropic format, the tool result is encoded as a provider-native message
// with Role "tool_result". For OpenAI format, Role "tool" is used with the
// ToolCallID set so the provider can correlate the result.
func AppendToolResult(cfg AppendToolResultConfig) engine.Instruction {
	return engine.Instruction{
		Name: "APPEND_TOOL_RESULT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			msgs := decodeHistory(ctx.ByteSlots[cfg.HistorySlot])

			toolCallID := string(ctx.ByteSlots[cfg.ToolCallIDSlot])
			resultContent := string(ctx.ByteSlots[cfg.ResultSlot])

			var msg CanonicalMessage
			switch cfg.TargetFormat {
			case "anthropic":
				// Encode the result as an Anthropic tool_result content block
				// wrapped in the canonical message format understood by the
				// Anthropic adapter.
				contentJSON, err := json.Marshal([]map[string]string{
					{
						"type":        "tool_result",
						"tool_use_id": toolCallID,
						"content":     resultContent,
					},
				})
				if err != nil {
					// Fallback to plain content on marshal error (should never happen).
					contentJSON = []byte(resultContent)
				}
				msg = CanonicalMessage{
					Role:       RoleToolResult,
					Content:    string(contentJSON),
					ToolCallID: toolCallID,
				}
			case "openai":
				msg = CanonicalMessage{
					Role:       "tool",
					Content:    resultContent,
					ToolCallID: toolCallID,
				}
			default:
				msg = CanonicalMessage{
					Role:       "tool",
					Content:    resultContent,
					ToolCallID: toolCallID,
				}
			}

			msgs = append(msgs, msg)
			writeHistorySlot(ctx, state, cfg.HistorySlot, msgs)
			return state.PC + 1
		},
	}
}
