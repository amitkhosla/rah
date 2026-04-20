package steps

import (
	"strconv"
	"strings"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// RoutingRule is a single conditional rule that maps a condition to a model slug.
type RoutingRule struct {
	Condition     string   `json:"condition"`          // condition expression (see evaluateRoutingCondition)
	Model         string   `json:"model"`              // model slug to use when condition matches
	FallbackChain []string `json:"fallback,omitempty"` // fallback models if primary fails; consumed by llm_call FallbackByModel
}

// RouteLLMConfig configures the RouteLLM instruction.
type RouteLLMConfig struct {
	Rules        []RoutingRule
	Default      string                           // fallback model slug if no rule matches
	ResultSlot   int                              // ByteSlots index to write resolved model slug
	TokenSlot    int                              // IntSlots index for token estimate (-1 if unused)
	MetaSlot     int                              // ByteSlots index for tenant meta value (-1 if unused
	ByteSlotMap  map[string]int                   // slot name → ByteSlots index for slot-value conditions
	ModelCatalog map[string]config.LLMModelConfig // for validation only (write slug even if not in catalog)
}

// RouteLLM returns an engine.Instruction that evaluates routing rules in order
// and writes the resolved model slug to ctx.ByteSlots[cfg.ResultSlot].
//
// Condition syntax (simple parser, no external deps):
//   - "" or "true"           → always matches
//   - "token_count > N"      → ctx.IntSlots[TokenSlot] > N  (requires TokenSlot >= 0)
//   - "token_count < N"      → ctx.IntSlots[TokenSlot] < N
//   - "token_count >= N"     → ctx.IntSlots[TokenSlot] >= N
//   - "token_count <= N"     → ctx.IntSlots[TokenSlot] <= N
//   - "meta == VALUE"        → string(ctx.ByteSlots[MetaSlot]) == VALUE  (requires MetaSlot >= 0)
//   - "meta != VALUE"        → not equal
//   - Unrecognized           → skip rule (no match)
func RouteLLM(cfg RouteLLMConfig) engine.Instruction {
	return engine.Instruction{
		Name: "route_llm",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			resolved := ""

			for _, rule := range cfg.Rules {
				if evaluateRoutingCondition(rule.Condition, ctx, cfg.TokenSlot, cfg.MetaSlot, cfg.ByteSlotMap) {
					resolved = rule.Model
					break
				}
			}

			// If no rule matched, use default
			if resolved == "" {
				resolved = cfg.Default
			}

			// Write result to slot (even if empty, let caller handle empty slug)
			if resolved != "" && cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				dst := ctx.Alloc(len(resolved))
				copy(dst, resolved)
				ctx.ByteSlots[cfg.ResultSlot] = dst
			}

			state.AddTraceAttr("selected_model", resolved)
			if cfg.TokenSlot >= 0 && cfg.TokenSlot < len(ctx.IntSlots) {
				state.AddTraceAttr("token_count", strconv.FormatInt(ctx.IntSlots[cfg.TokenSlot], 10))
			}

			return state.PC + 1
		},
	}
}

// evaluateRoutingCondition evaluates a single routing condition string.
// Returns true if the condition matches, false otherwise (including parse errors).
//
// Supported LHS tokens:
//   - "token_count" → IntSlots[tokenSlot]  (requires tokenSlot >= 0)
//   - "meta"        → ByteSlots[metaSlot]  (requires metaSlot >= 0)
//   - any other     → looked up in byteSlotMap; uses that ByteSlot value for == / !=
func evaluateRoutingCondition(cond string, ctx *rctx.Context, tokenSlot, metaSlot int, byteSlotMap map[string]int) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" || cond == "true" {
		return true
	}

	// Split into at most 3 parts: lhs operator rhs
	// Using SplitN with limit allows values containing spaces (e.g. meta == hello world)
	parts := strings.SplitN(cond, " ", 3)
	if len(parts) < 3 {
		return false
	}

	lhs := strings.TrimSpace(parts[0])
	op := strings.TrimSpace(parts[1])
	rhs := strings.TrimSpace(parts[2])

	switch lhs {
	case "token_count":
		if tokenSlot < 0 || tokenSlot >= len(ctx.IntSlots) {
			return false
		}
		n, err := strconv.ParseInt(rhs, 10, 64)
		if err != nil {
			return false
		}
		val := ctx.IntSlots[tokenSlot]
		switch op {
		case ">":
			return val > n
		case "<":
			return val < n
		case ">=":
			return val >= n
		case "<=":
			return val <= n
		default:
			return false
		}

	case "meta":
		if metaSlot < 0 || metaSlot >= len(ctx.ByteSlots) {
			return false
		}
		metaVal := string(ctx.ByteSlots[metaSlot])
		switch op {
		case "==":
			return metaVal == rhs
		case "!=":
			return metaVal != rhs
		default:
			return false
		}

	default:
		// Slot-based condition: lhs is a named slot resolved at bake time.
		if byteSlotMap != nil {
			if idx, ok := byteSlotMap[lhs]; ok && idx >= 0 && idx < len(ctx.ByteSlots) {
				slotVal := string(ctx.ByteSlots[idx])
				switch op {
				case "==":
					return slotVal == rhs
				case "!=":
					return slotVal != rhs
				}
			}
		}
		return false
	}
}
