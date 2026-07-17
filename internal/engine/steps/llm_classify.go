package steps

import (
	"encoding/json"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/observability"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ClassifyLLMConfig configures the smart classification step.
type ClassifyLLMConfig struct {
	CallConfig LLMCallConfig  // The model to use for classification (e.g. gemini-flash)
	Mapping    map[string]int // JSON key in response -> ByteSlot index to write value to
}

// ClassifyLLM returns an engine.Instruction that calls an LLM and parses its
// JSON output into multiple metadata slots.
func ClassifyLLM(cfg ClassifyLLMConfig) engine.Instruction {
	// We reuse the core logic of LLMCall but wrap the result handling.
	base := LLMCall(cfg.CallConfig)

	return engine.Instruction{
		Name: "classify_llm",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Execute the LLM call using the base instruction logic.
			// The base logic will write the raw JSON to cfg.CallConfig.ResultSlot.
			pc := base.Action(ctx, state)
			if ctx.Failed || pc == engine.StopPlan {
				return pc
			}

			// 2. Parse the JSON result from the result slot.
			if cfg.CallConfig.ResultSlot >= 0 {
				raw := ctx.ByteSlots[cfg.CallConfig.ResultSlot]
				var parsed map[string]any
				if err := json.Unmarshal(raw, &parsed); err == nil {
					// 3. Map keys to slots.
					for key, slot := range cfg.Mapping {
						if val, ok := parsed[key]; ok {
							strVal := ""
							switch v := val.(type) {
							case string:
								strVal = v
							default:
								b, _ := json.Marshal(v)
								strVal = string(b)
							}

							if slot >= 0 && slot < len(ctx.ByteSlots) {
								out := ctx.Alloc(len(strVal))
								copy(out, strVal)
								ctx.ByteSlots[slot] = out
							}
						}
					}
				}
			}

			if cfg.CallConfig.ResultSlot >= 0 {
				classifyRaw := ctx.ByteSlots[cfg.CallConfig.ResultSlot]
				if len(classifyRaw) > 0 {
					full := string(classifyRaw)
					snip := string(classifyRaw)
					if len(snip) > 500 {
						snip = snip[:500] + "â€¦"
					}
					state.AddTraceAttr("classifier_output", snip)
					observability.WriteDetailLog(map[string]any{
						"type":         "classify_llm",
						"api_id":       ctx.ApiId,
						"tenant_id":    ctx.TenantID,
						"raw_response": full,
					})
				}
			}
			for key, slot := range cfg.Mapping {
				if slot >= 0 && slot < len(ctx.ByteSlots) && len(ctx.ByteSlots[slot]) > 0 {
					state.AddTraceAttr("cls_"+key, string(ctx.ByteSlots[slot]))
				}
			}

			return pc
		},
	}
}
