package control

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileServeA2A compiles an a2a_serve instruction.
//
// Step input fields:
//   - input["skills_json"]  → JSON object mapping skill ID to API path, e.g. {"pay":"/acme/pay"}
//   - input["gateway_base"] → base URL for skill HTTP dispatch (default: c.GatewayBase)
//   - input["timeout_ms"]   → int milliseconds (default 30000)
//
// The instruction always returns StopPlan — it writes the full A2A JSON-RPC
// response to the ResponseWriter and no further instructions should execute.
func (c *Compiler) compileServeA2A(step StepConfig) error {
	// Parse skill routes from JSON.
	skillsJSON := step.Input["skills_json"]
	var skillRoutes map[string]string
	if skillsJSON != "" {
		if err := json.Unmarshal([]byte(skillsJSON), &skillRoutes); err != nil {
			return fmt.Errorf("a2a_serve: invalid skills_json: %w", err)
		}
	}
	if skillRoutes == nil {
		skillRoutes = make(map[string]string)
	}

	// Resolve gateway base URL.
	gatewayBase := step.Input["gateway_base"]
	if gatewayBase == "" {
		gatewayBase = c.GatewayBase
	}

	// Resolve timeout.
	timeoutMs := 30_000
	if v := step.Input["timeout_ms"]; v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	cfg := steps.ServeA2AConfig{
		SkillRoutes: skillRoutes,
		GatewayBase: gatewayBase,
		TimeoutMs:   timeoutMs,
	}
	c.GlobalTable = append(c.GlobalTable, steps.ServeA2A(cfg))
	return nil
}
