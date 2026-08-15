package control

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileClassifyLLM resolves slots and appends the classify_llm instruction.
//
// Reserved input keys:
//   - model             : classifier model slug (baked; overridden at runtime by model_slot)
//   - model_slot        : slot name â†’ ByteSlots; if set, model is selected at runtime
//   - system_slot       : slot name â†’ ByteSlots; classifier system prompt (e.g. "return JSON {complexity:â€¦}")
//   - fallback_chain    : comma-separated aliases for baked fallback
//   - fallback_by_model : JSON map alias â†’ []alias for per-model fallback when model_slot is used
//   - prompt_slot / result_slot : legacy aliases (ignored; use key_identifier / as)
//
// All other input keys are treated as output-field mappings:
//
//	"complexity" â†’ "var.complexity"  means: parse resp["complexity"] â†’ ByteSlots[getSlot("var.complexity")]
func (c *Compiler) compileClassifyLLM(step StepConfig) error {
	// 1. Resolve baked model (required even when model_slot is used — serves as catalog default)
	modelSlug := step.Input["model"]
	if modelSlug == "" {
		modelSlug = c.LLMCfg.Default
	}
	var modelCfg config.LLMModelConfig
	found := false
	for _, m := range c.LLMCfg.Models {
		if m.Alias == modelSlug {
			modelCfg = m
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("classify_llm: model %q not found", modelSlug)
	}
	apiKey, err := c.resolveAPIKey(modelCfg.APIKeyRef)
	if err != nil {
		return fmt.Errorf("classify_llm: %w", err)
	}

	// 2. Resolve I/O slots
	promptSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("classify_llm: prompt slot: %w", err)
	}
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("classify_llm: result slot: %w", err)
	}

	// 3. Optional system slot (classifier instruction prompt, e.g. "return JSON {complexity:â€¦}")
	systemSlot := -1
	if ssName := step.Input["system_slot"]; ssName != "" {
		if s, slotErr := c.getSlot(ssName); slotErr == nil {
			systemSlot = s
		}
	}

	// 4. Optional dynamic model slot — selects classifier at runtime from a preceding route_llm step
	modelSlotIdx := -1
	var modelCatalog map[string]config.LLMModelConfig
	var catalogKeys map[string]string
	if msName := step.Input["model_slot"]; msName != "" {
		if s, slotErr := c.getSlot(msName); slotErr == nil {
			modelSlotIdx = s
			modelCatalog = make(map[string]config.LLMModelConfig, len(c.LLMCfg.Models))
			catalogKeys = make(map[string]string, len(c.LLMCfg.Models))
			for _, m := range c.LLMCfg.Models {
				modelCatalog[m.Alias] = m
				if resolvedKey, keyErr := c.resolveAPIKey(m.APIKeyRef); keyErr == nil {
					catalogKeys[m.Alias] = resolvedKey
				}
			}
		}
	}

	// 5. Baked fallback chain
	var classifyFallback []steps.FallbackEntry
	if chainStr, ok := step.Input["fallback_chain"]; ok && chainStr != "" {
		for _, alias := range strings.Split(chainStr, ",") {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			var mc config.LLMModelConfig
			found := false
			for _, m := range c.LLMCfg.Models {
				if m.Alias == alias {
					mc = m
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("classify_llm: fallback_chain entry %q not found in LLM catalog", alias)
			}
			fbKey, fbErr := c.resolveAPIKey(mc.APIKeyRef)
			if fbErr != nil {
				return fmt.Errorf("classify_llm: fallback_chain %q: %w", alias, fbErr)
			}
			classifyFallback = append(classifyFallback, steps.FallbackEntry{ModelConfig: mc, APIKey: fbKey})
		}
	}

	// 6. Per-model fallback map (used together with model_slot for tier-1 classifier routing)
	var classifyFBM map[string][]steps.FallbackEntry
	if fbmJSON, ok := step.Input["fallback_by_model"]; ok && fbmJSON != "" {
		var rawMap map[string][]string
		if jsonErr := json.Unmarshal([]byte(fbmJSON), &rawMap); jsonErr != nil {
			return fmt.Errorf("classify_llm: fallback_by_model: invalid JSON: %w", jsonErr)
		}
		classifyFBM = make(map[string][]steps.FallbackEntry, len(rawMap))
		for modelAlias, fallbacks := range rawMap {
			var entries []steps.FallbackEntry
			for _, fbAlias := range fallbacks {
				var mc config.LLMModelConfig
				found := false
				for _, m := range c.LLMCfg.Models {
					if m.Alias == fbAlias {
						mc = m
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("classify_llm: fallback_by_model[%q]: %q not found in LLM catalog", modelAlias, fbAlias)
				}
				fbKey, fbKeyErr := c.resolveAPIKey(mc.APIKeyRef)
				if fbKeyErr != nil {
					return fmt.Errorf("classify_llm: fallback_by_model[%q][%q]: %w", modelAlias, fbAlias, fbKeyErr)
				}
				entries = append(entries, steps.FallbackEntry{ModelConfig: mc, APIKey: fbKey})
			}
			classifyFBM[modelAlias] = entries
		}
	}

	// 7. Output field mapping — every non-reserved key maps a classifier JSON key â†’ ByteSlot
	reserved := map[string]bool{
		"model": true, "model_slot": true, "system_slot": true,
		"prompt_slot": true, "result_slot": true,
		"fallback_chain": true, "fallback_by_model": true,
	}
	mapping := make(map[string]int)
	for jsonKey, slotName := range step.Input {
		if reserved[jsonKey] {
			continue
		}
		if s, slotErr := c.getSlot(slotName); slotErr == nil {
			mapping[jsonKey] = s
		}
	}

	classifyCfg := steps.ClassifyLLMConfig{
		CallConfig: steps.LLMCallConfig{
			ModelConfig:     modelCfg,
			APIKey:          apiKey,
			PromptSlot:      promptSlot,
			ResultSlot:      resultSlot,
			SystemSlot:      systemSlot,
			MaxTokens:       512, // small — classification only
			Temperature:     0.0, // deterministic JSON output
			FallbackChain:   classifyFallback,
			FallbackByModel: classifyFBM,
			ModelSlot:       modelSlotIdx,
			ModelCatalog:    modelCatalog,
			CatalogKeys:     catalogKeys,
			APIKeySlot:      -1,
			InputTokensSlot: -1,
			OutputTokensSlot: -1,
			StopReasonSlot:  -1,
		},
		Mapping: mapping,
	}

	c.GlobalTable = append(c.GlobalTable, steps.ClassifyLLM(classifyCfg))

	// Register per-model upstream rate limits at bake time for all catalog models.
	for _, m := range c.LLMCfg.Models {
		if len(m.RateLimits) > 0 {
			windows := make([]*engine.UpstreamRateWindow, 0, len(m.RateLimits))
			for _, rl := range m.RateLimits {
				windows = append(windows, engine.UpstreamWindowFromWindow(rl.Window, uint32(rl.Limit)))
			}
			engine.RegisterUpstreamLimit(m.Alias, windows)
		}
	}

	return nil
}
