package steps

import (
	"bytes"
	"encoding/json"

	"rah/internal/engine"
	"rah/internal/rctx"
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
//
// Detection priority:
//  1. "gemini"    — top-level "contents" key present
//  2. "anthropic" — top-level "messages" present AND "system" is a JSON string,
//     OR "model" value starts with "claude-"
//  3. "openai"    — top-level "messages" present, OR "model" starts with "gpt-"/"o1"/"o3"
//  4. "unknown"   — body empty, invalid JSON, or no recognisable keys
//
// On empty body the instruction silently writes "unknown" and returns PC+1.
// On invalid JSON the instruction silently writes "unknown" and returns PC+1.
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

// detectFormat is the pure detection logic, extracted for testability.
func detectFormat(body []byte) string {
	if len(body) == 0 {
		return "unknown"
	}

	// Parse only the top-level keys to avoid full-document allocation.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return "unknown"
	}

	// ── 1. Gemini: "contents" array present ───────────────────────────────────
	if _, ok := top["contents"]; ok {
		return "gemini"
	}

	// ── 2 & 3. Anthropic vs OpenAI ────────────────────────────────────────────
	_, hasMessages := top["messages"]
	systemRaw, hasSystem := top["system"]
	modelRaw, hasModel := top["model"]

	// Model-name heuristic: check before structural key logic so a lone
	// "model" field (without "messages") can still produce a useful result.
	if hasModel {
		var modelStr string
		if err := json.Unmarshal(modelRaw, &modelStr); err == nil {
			switch {
			case bytes.HasPrefix([]byte(modelStr), []byte("claude-")):
				return "anthropic"
			case bytes.HasPrefix([]byte(modelStr), []byte("gpt-")),
				bytes.HasPrefix([]byte(modelStr), []byte("o1")),
				bytes.HasPrefix([]byte(modelStr), []byte("o3")):
				return "openai"
			}
		}
	}

	if hasMessages {
		// Anthropic: "system" is a top-level JSON string (not null, not array, not object).
		if hasSystem && isJSONString(systemRaw) {
			return "anthropic"
		}
		// OpenAI: "messages" present; "system" absent, null, array, or object.
		return "openai"
	}

	return "unknown"
}

// isJSONString returns true if raw is a valid, non-null JSON string literal.
func isJSONString(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	// null → treat as absent → false
	if bytes.Equal(raw, []byte("null")) {
		return false
	}
	// JSON strings start and end with '"'
	return raw[0] == '"'
}
