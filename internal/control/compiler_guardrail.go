package control

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileGuardrailConfig parses "guardrail.*" keys from step.Input and returns
// a GuardrailConfig ready to be embedded in LLMCallConfig.
func compileGuardrailConfig(c *Compiler, step StepConfig, resultSlot int) (steps.GuardrailConfig, error) {
	cfg := steps.GuardrailConfig{ResultSlot: resultSlot, FlagSlot: -1}

	hasAny := false
	for k := range step.Input {
		if strings.HasPrefix(k, "guardrail.") {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return cfg, nil
	}

	if v, ok := step.Input["guardrail.flag_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return cfg, fmt.Errorf("guardrail: flag_slot: %w", err)
		}
		cfg.FlagSlot = s
	}

	defaultAction := steps.GuardrailAction(step.Input["guardrail.on_violation"])
	if defaultAction == "" {
		defaultAction = steps.GuardrailBlock
	}

	if raw, ok := step.Input["guardrail.regex_rules"]; ok && raw != "" {
		var rules []steps.GuardrailRegexRule
		if err := json.Unmarshal([]byte(raw), &rules); err != nil {
			return cfg, fmt.Errorf("guardrail: regex_rules: invalid JSON: %w", err)
		}
		for i := range rules {
			if rules[i].Action == "" {
				rules[i].Action = defaultAction
			}
		}
		cfg.RegexRules = rules
	}

	if raw, ok := step.Input["guardrail.providers"]; ok && raw != "" {
		var providers []steps.GuardrailProviderConfig
		if err := json.Unmarshal([]byte(raw), &providers); err != nil {
			return cfg, fmt.Errorf("guardrail: providers: invalid JSON: %w", err)
		}
		for i := range providers {
			if providers[i].APIKey != "" {
				resolved, err := c.resolveAPIKey(providers[i].APIKey)
				if err != nil {
					return cfg, fmt.Errorf("guardrail: provider[%d] api_key: %w", i, err)
				}
				providers[i].APIKey = resolved
			}
			if providers[i].Action == "" {
				providers[i].Action = defaultAction
			}
		}
		cfg.Providers = providers
	}

	return cfg, nil
}
