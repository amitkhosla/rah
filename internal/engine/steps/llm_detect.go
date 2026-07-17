package steps

import (
	"bytes"
	"encoding/json"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// DetectMessageFormatConfig holds the bake-time slot assignments for DetectMessageFormat.
type DetectMessageFormatConfig struct {
	BodySlot   int // ByteSlots index: raw JSON request body (read)
	FormatSlot int // ByteSlots index: write detected format string (write)
	// Detected values: "anthropic" | "openai" | "gemini" | "unknown"
}

// DetectMessageFormat returns an Instruction that reads a raw JSON body from
// cfg.BodySlot, auto-detects the LLM wire format (Anthropic/OpenAI/Gemini),
// and writes the format name string into cfg.FormatSlot.
func DetectMessageFormat(cfg DetectMessageFormatConfig) engine.Instruction {
	return engine.Instruction{
		Name: "DETECT_MESSAGE_FORMAT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			format := detectFormat(ctx.ByteSlots[cfg.BodySlot])
			out := ctx.Alloc(len(format))
			copy(out, format)
			ctx.ByteSlots[cfg.FormatSlot] = out
			return state.PC + 1
		},
	}
}

func detectFormat(body []byte) string {
	if len(body) == 0 { return "unknown" }
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil { return "unknown" }
	if _, ok := top["contents"]; ok { return "gemini" }
	_, hasMessages := top["messages"]
	systemRaw, hasSystem := top["system"]
	modelRaw, hasModel := top["model"]
	if hasModel {
		var modelStr string
		if err := json.Unmarshal(modelRaw, &modelStr); err == nil {
			switch {
			case bytes.HasPrefix([]byte(modelStr), []byte("claude-")): return "anthropic"
			case bytes.HasPrefix([]byte(modelStr), []byte("gpt-")),
				bytes.HasPrefix([]byte(modelStr), []byte("o1")),
				bytes.HasPrefix([]byte(modelStr), []byte("o3")): return "openai"
			}
		}
	}
	if hasMessages {
		if hasSystem && isJSONString(systemRaw) { return "anthropic" }
		return "openai"
	}
	return "unknown"
}

func isJSONString(raw json.RawMessage) bool {
	if len(raw) == 0 { return false }
	if bytes.Equal(raw, []byte("null")) { return false }
	return raw[0] == '"'
}
