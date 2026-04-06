package steps

import (
	"encoding/json"
	"time"

	"rah/internal/engine"
	"rah/internal/ingest"
	"rah/internal/quota"
	"rah/internal/rctx"
)

// CostEventPayload is the structured data emitted to ingest pipeline when cost is recorded.
// It's serialized as JSON and sent to configured sinks (HTTP, Redis, S3, etc.) for analytics.
type CostEventPayload struct {
	TenantKey string  `json:"tenant_key"`        // Tenant identifier
	APIKey    string  `json:"api_key,omitempty"` // The actual API key used (if tracked)
	Cost      float64 `json:"cost"`              // Total cost in USD
	Model     string  `json:"model,omitempty"`   // LLM model name
	Timestamp int64   `json:"timestamp_ns"`      // Unix nanoseconds
}

// RecordCostConfig holds configuration for the RecordCost instruction.
// Includes both quota manager (local in-memory) and ingest pipeline (external events).
type RecordCostConfig struct {
	QuotaManager   *quota.CostQuotaManager
	IngestPipeline *ingest.Pipeline // nil = no-op for cost event emission
	CostSlot       int
}

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
// This instruction:
// 1. Updates the local quota manager with actual cost (fast, in-memory)
// 2. Schedules a cost event emission via AfterResponse hook (deferred, to ingest pipeline)
//
// Cost events are emitted after the response is sent, allowing external analytics
// services to consume and store cost data without blocking the gateway.
//
// Usage in a flow:
//
//	{"action": "llm_call", "output_tokens_slot": "actual_output_tokens_slot", ...}
//	{"action": "record_cost", "cost_slot": "actual_cost_slot"}
//
// cfg: RecordCostConfig containing quota manager and ingest pipeline
func RecordCost(cfg RecordCostConfig) engine.Instruction {
	return engine.Instruction{
		Name: "RECORD_COST",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Validate slot index
			if cfg.CostSlot < 0 || cfg.CostSlot >= len(ctx.IntSlots) {
				// Invalid slot, but don't fail — just skip recording
				return s.PC + 1
			}

			// Read actual cost from slot (stored as fixed-point: divide by 1e9)
			actualCostFixed := ctx.IntSlots[cfg.CostSlot]
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

			// 1. Update local quota manager (fast, in-memory)
			err := cfg.QuotaManager.RecordCost(tenantKey, actualCost)
			if err != nil {
				// Log error but don't fail the request (cost recording is best-effort)
				ctx.ErrorMsg = []byte(err.Error())
				// Continue anyway — don't stop the request
			}

			// 2. Schedule cost event emission (deferred, after response sent)
			// Capture values for closure
			if cfg.IngestPipeline != nil {
				capturedCost := actualCost
				capturedModel := ctx.Model
				capturedTenantKey := tenantKey
				capturedTenantID := ctx.TenantID
				capturedTimestamp := time.Now().UnixNano()

				ctx.AfterResponse = append(ctx.AfterResponse, func() {
					// Build cost event payload
					payload := CostEventPayload{
						TenantKey: capturedTenantKey,
						Cost:      capturedCost,
						Model:     capturedModel,
						Timestamp: capturedTimestamp,
					}

					// Serialize to JSON
					payloadJSON, err := json.Marshal(payload)
					if err != nil {
						// Silently skip on marshal error (observability shouldn't fail)
						return
					}

					// Create ingest event
					evt := ingest.Event{
						TenantID:    capturedTenantID,
						Kind:        ingest.KindCostRecord,
						Model:       capturedModel,
						TimestampNs: capturedTimestamp,
					}

					// Determine number of sinks
					numSinks := cfg.IngestPipeline.NumSinksForKind(ingest.KindCostRecord)
					if numSinks == 0 {
						numSinks = 1 // safety fallback
					}

					// Set payload and emit
					evt.SetPayload(payloadJSON, numSinks)
					cfg.IngestPipeline.Emit(evt)
				})
			}

			return s.PC + 1
		},
	}
}

// CalculateCost computes the cost from token counts and pricing information.
// This is a helper step that:
// 1. Reads input/output token counts from slots
// 2. Looks up pricing for the model (optional; continues if pricing not found)
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
// If pricing manager is nil or pricing is missing for the model:
// - Sets cost to 0 (zero-cost fallback)
// - Logs a warning if configured to do so
// - Continues execution (doesn't fail)
//
// Graceful degradation: gateway continues even without pricing information.
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
			// The pricing manager will be available in the compiler/flowmanager
			//
			// For now: Return 0 (zero-cost fallback)
			// This allows the gateway to work even without pricing configured.
			//
			// The actual implementation will:
			// 1. Get model from context or slot
			// 2. Look up pricing: pricingManager.GetPrice(provider, modelID)
			// 3. If not found: log warning and use zero-cost
			// 4. Calculate: cost = (inputTokens * inputRate + outputTokens * outputRate) / 1M
			// 5. Store as fixed-point: int64(cost * 1e9)

			ctx.IntSlots[costSlot] = 0
			return s.PC + 1
		},
	}
}
