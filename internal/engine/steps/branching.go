package steps

import (
	"rah/internal/rctx"
)

// TenantSwitch handles multi-option paths.
func TenantSwitch(ctx *rctx.Context) int16 {
	tenantType := string(ctx.ByteSlots[0]) // Retrieve from optimized slot

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
func IfElseAuth(ctx *rctx.Context) int16 {
	isAuthenticated := ctx.BoolSlots[1]

	if isAuthenticated {
		return 1 // Continue to next step
	}
	return 2 // Jump +2 to skip the "Success" step and land on "Unauthorized"
}
