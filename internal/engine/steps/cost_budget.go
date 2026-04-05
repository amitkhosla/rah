package steps

import (
	"rah/internal/engine"
	"rah/internal/quota"
	"rah/internal/rctx"
)

// EnforceCostBudget checks if a tenant can afford the estimated LLM cost before proceeding.
// This is a pre-check that prevents expensive LLM calls from proceeding if the tenant
// has exceeded their cost quota.
//
// Usage in a flow:
//
//	{"action": "estimate_tokens", "slot": "estimated_tokens_slot"}
//	{"action": "calculate_cost", "input_tokens_slot": "estimated_tokens_slot",
//	                              "output_tokens_slot": "estimated_output_tokens",
//	                              "cost_slot": "estimated_cost_slot"}
//	{"action": "enforce_cost_budget", "cost_slot": "estimated_cost_slot"}
//	{"action": "llm_call", ...}
//	{"action": "record_cost", "actual_cost_slot": "actual_cost_slot"}
//
// The cost_slot should contain the estimated cost in dollars (float64 stored as int64 via fixed-point encoding).
// If the tenant cannot afford the request, returns 429 (Payment Required).
// If no quota is configured for the tenant, allows the request (no-op).
//
// quotaManager: Central quota manager for all tenants (injected at bake time)
// costSlot: IntSlot index containing estimated cost (interpreted as fixed-point: value/1e9 = cost in dollars)
func EnforceCostBudget(quotaManager *quota.CostQuotaManager, costSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "ENFORCE_COST_BUDGET",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Validate slot index
			if costSlot < 0 || costSlot >= len(ctx.IntSlots) {
				// Invalid slot, but don't fail the request — just continue
				return s.PC + 1
			}

			// Read estimated cost from slot (stored as fixed-point: divide by 1e9)
			estimatedCostFixed := ctx.IntSlots[costSlot]
			if estimatedCostFixed == 0 {
				// No cost to check, proceed
				return s.PC + 1
			}

			// Convert from fixed-point to float64 (1e9 = base unit)
			estimatedCost := float64(estimatedCostFixed) / 1e9

			// Get tenant ID as string (for quota manager)
			// TenantID is uint16, but we need the string key (TenantKey)
			tenantKey := ctx.TenantKey
			if tenantKey == "" {
				// Fallback: use numeric tenant ID (shouldn't happen in normal flow)
				// The quota manager expects the tenant key as registered
				return s.PC + 1
			}

			// Check if tenant can afford this request
			allowed, reason, _ := quotaManager.CanAfford(tenantKey, estimatedCost)

			if !allowed {
				// Budget exceeded — return 429 (Payment Required)
				ctx.ResponseStatus = 429
				// Optionally store the reason in the context for logging
				ctx.ErrorMsg = []byte(reason)
				ctx.ErrorCode = 429
				return -1 // Stop execution
			}

			// Budget available, proceed
			return s.PC + 1
		},
	}
}

// RecordCost records the actual cost after an LLM call completes.
// This must be called after the LLM response is received to update the quota manager
// with the actual tokens used (not estimated).
//
// Usage in a flow:
//
//	{"action": "llm_call", "output_tokens_slot": "actual_output_tokens_slot", ...}
//	{"action": "record_cost", "cost_slot": "actual_cost_slot"}
//
// actualCostSlot: IntSlot index containing actual cost (fixed-point: value/1e9 = cost in dollars)
func RecordCost(quotaManager *quota.CostQuotaManager, costSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "RECORD_COST",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Validate slot index
			if costSlot < 0 || costSlot >= len(ctx.IntSlots) {
				// Invalid slot, but don't fail — just skip recording
				return s.PC + 1
			}

			// Read actual cost from slot (stored as fixed-point: divide by 1e9)
			actualCostFixed := ctx.IntSlots[costSlot]
			if actualCostFixed == 0 {
				// No cost to record, proceed
				return s.PC + 1
			}

			// Convert from fixed-point to float64 (1e9 = base unit)
			actualCost := float64(actualCostFixed) / 1e9

			// Get tenant key
			tenantKey := ctx.TenantKey
			if tenantKey == "" {
				// Fallback: skip recording if tenant key not set
				return s.PC + 1
			}

			// Record the actual cost in quota manager
			err := quotaManager.RecordCost(tenantKey, actualCost)
			if err != nil {
				// Log error but don't fail the request (cost recording is best-effort)
				ctx.ErrorMsg = []byte(err.Error())
				// Continue anyway — don't stop the request
			}

			return s.PC + 1
		},
	}
}

// CalculateCost computes the cost from token counts and pricing information.
// This is a helper step that:
// 1. Reads input/output token counts from slots
// 2. Looks up pricing for the model
// 3. Calculates: cost = (inputTokens * inputRate + outputTokens * outputRate) / 1M
// 4. Stores result in cost_slot as fixed-point (multiply by 1e9 to store)
//
// Usage in a flow:
//
//	{"action": "calculate_cost",
//	 "input_tokens_slot": "in_tokens",
//	 "output_tokens_slot": "out_tokens",
//	 "model_slot": "model_name",
//	 "cost_slot": "result_slot"}
//
// Note: This step requires the pricing manager to be injected at compile time.
// For now, this is a placeholder — pricing lookup will be added in a future commit.
func CalculateCost(costSlot, inputTokensSlot, outputTokensSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "CALCULATE_COST",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Validate slot indices
			if costSlot < 0 || costSlot >= len(ctx.IntSlots) {
				return s.PC + 1
			}
			if inputTokensSlot < 0 || inputTokensSlot >= len(ctx.IntSlots) {
				return s.PC + 1
			}
			if outputTokensSlot < 0 || outputTokensSlot >= len(ctx.IntSlots) {
				return s.PC + 1
			}

			// Read token counts
			inputTokens := ctx.IntSlots[inputTokensSlot]
			outputTokens := ctx.IntSlots[outputTokensSlot]

			if inputTokens == 0 && outputTokens == 0 {
				// No tokens, no cost
				ctx.IntSlots[costSlot] = 0
				return s.PC + 1
			}

			// TODO: Integrate with pricing manager to look up rates
			// For now, just return 0 (placeholder)
			// The actual implementation will:
			// 1. Get model from context
			// 2. Look up pricing for model
			// 3. Calculate: cost = (inputTokens * inputRate + outputTokens * outputRate) / 1M
			// 4. Store as fixed-point: cost * 1e9

			ctx.IntSlots[costSlot] = 0
			return s.PC + 1
		},
	}
}
