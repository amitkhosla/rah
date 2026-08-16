package control

import (
	"fmt"
	"strconv"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine/steps"
	"github.com/amitkhosla/rah/internal/mcpreg"
)

// compileServeMCP resolves and appends the serve_mcp instruction.
//
// Step fields:
//   - step.KeyIdentifier       â†’ slot name holding the raw JSON-RPC request body
//   - step.Input["name"]       â†’ literal virtual MCP server name (mutually exclusive with name_slot)
//   - step.Input["name_slot"]  â†’ slot name whose value is the virtual MCP server name at runtime
//   - step.Input["timeout_ms"] â†’ int milliseconds (default 30000)
//
// The instruction always returns StopPlan — it writes the full JSON-RPC response
// to the ResponseWriter and no further instructions should execute.
func (c *Compiler) compileServeMCP(step StepConfig) error {
	// Resolve body slot.
	bodySlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("serve_mcp: body slot: %w", err)
	}

	// Resolve virtual server name — either from a slot or a literal.
	nameSlot := -1
	nameLiteral := step.Input["name"]
	if slotName := step.Input["name_slot"]; slotName != "" {
		s, slotErr := c.getSlotReadOnly(slotName)
		if slotErr != nil {
			return fmt.Errorf("serve_mcp: name slot: %w", slotErr)
		}
		nameSlot = s
		nameLiteral = ""
	}

	// Build MCPServerLookup that closes over compiler's LLM config.
	mcpLookup := steps.MCPServerLookup(func(alias string) (config.MCPServerConfig, string, bool) {
		for _, s := range c.LLMCfg.MCPServers {
			if s.Alias == alias {
				apiKey := s.APIKeyRef
				return s, apiKey, true
			}
		}
		return config.MCPServerConfig{}, "", false
	})

	// Build VirtualMCPLookup that closes over MCPRegistry (if available).
	var virtualLookup steps.VirtualMCPLookup
	if c.MCPRegistry != nil {
		reg := c.MCPRegistry
		virtualLookup = func(tenantID uint16, name string) (mcpreg.VirtualMCPServerDef, bool) {
			return reg.GetServer(tenantID, name)
		}
	} else {
		virtualLookup = func(_ uint16, _ string) (mcpreg.VirtualMCPServerDef, bool) {
			return mcpreg.VirtualMCPServerDef{}, false
		}
	}

	// Resolve timeout.
	timeoutMs := 30_000
	if v := step.Input["timeout_ms"]; v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil && n > 0 {
			timeoutMs = n
		}
	}

	cfg := steps.ServeMCPConfig{
		NameSlot:        nameSlot,
		NameLiteral:     nameLiteral,
		BodySlot:        bodySlot,
		LookupServer:    virtualLookup,
		LookupMCPServer: mcpLookup,
		GatewayBase:     c.GatewayBase,
		TimeoutMs:       timeoutMs,
	}
	c.GlobalTable = append(c.GlobalTable, steps.ServeMCP(cfg))
	return nil
}
