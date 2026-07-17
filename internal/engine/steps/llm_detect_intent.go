package steps

import (
	"strings"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// DetectIntentConfig holds the pattern matching rules for intent detection.
type DetectIntentConfig struct {
	HeaderRules map[string]map[string]string // Header Name -> Pattern -> Intent Tag
	BodyRules   map[string]string            // Pattern -> Intent Tag
	ResultSlot  int                          // Where to write the detected tag
}

// DetectIntent returns an engine.Instruction that tags the request based on patterns.
func DetectIntent(cfg DetectIntentConfig) engine.Instruction {
	return engine.Instruction{
		Name: "detect_intent",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			detected := ""

			// 1. Check Headers
			if ctx.Request != nil {
				for header, rules := range cfg.HeaderRules {
					val := ctx.Request.Header.Get(header)
					if val == "" { continue }
					for pattern, tag := range rules {
						if strings.Contains(strings.ToLower(val), strings.ToLower(pattern)) {
							detected = tag
							break
						}
					}
					if detected != "" { break }
				}
			}

			// 2. Write result
			if cfg.ResultSlot >= 0 {
				tag := []byte(detected)
				ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(tag))
				copy(ctx.ByteSlots[cfg.ResultSlot], tag)
			}

			return state.PC + 1
		},
	}
}
