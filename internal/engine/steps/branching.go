package steps

import "rah/internal/engine"

// TenantSwitch handles multi-option paths.
func TenantSwitch(ctx *engine.RequestContext) int16 {
	tenantType := ctx.Slots[0].(string) // Retrieve from optimized slot

	switch tenantType {
	case "GOLD":
		return 1 // Move to next (Gold-specific logic)
	case "SILVER":
		return 5 // Jump +5 steps to skip Gold logic and land on Silver logic
	default:
		return 10 // Jump +10 to skip all and land on the Finalize step
	}
}

// IfElseAuth handles a simple true/false branch.
func IfElseAuth(ctx *engine.RequestContext) int16 {
	isAuthenticated := ctx.Slots[1].(bool)

	if isAuthenticated {
		return 1 // Continue to next step
	}
	return 2 // Jump +2 to skip the "Success" step and land on "Unauthorized"
}
