package control

import (
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileDetectIntent resolves slots and appends the detect_intent instruction.
// Expects:
//   - as: slot name to write the detected intent tag into
//   - input: map where keys starting with "header:" define header-based rules,
//     e.g., "header:User-Agent": "claude-code/* -> claude-cli"
func (c *Compiler) compileDetectIntent(step StepConfig) error {
	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("detect_intent: result slot: %w", err)
	}

	headerRules := make(map[string]map[string]string)
	for k, v := range step.Input {
		if headerName, ok := strings.CutPrefix(k, "header:"); ok {
			// v format: "pattern -> tag"
			parts := strings.Split(v, "->")
			if len(parts) == 2 {
				pattern := strings.TrimSpace(parts[0])
				tag := strings.TrimSpace(parts[1])
				if headerRules[headerName] == nil {
					headerRules[headerName] = make(map[string]string)
				}
				headerRules[headerName][pattern] = tag
			}
		}
	}

	cfg := steps.DetectIntentConfig{
		HeaderRules: headerRules,
		ResultSlot:  resultSlot,
	}

	c.GlobalTable = append(c.GlobalTable, steps.DetectIntent(cfg))
	return nil
}
