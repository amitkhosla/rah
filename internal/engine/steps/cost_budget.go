package steps

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/ingest"
	"github.com/amitkhosla/rah/internal/quota"
	"github.com/amitkhosla/rah/internal/rctx"
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
	ModelSlot      int // -1 = not configured; reads model name from ByteSlots[ModelSlot] if >= 0
	KeySlot        int // -1 = use ctx.TenantKey; >= 0 = read quota key from ByteSlots[KeySlot]
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
// keySlot: ByteSlot index containing a custom quota key (-1 = use ctx.TenantKey)
func EnforceCostBudget(quotaManager *quota.CostQuotaManager, costSlot int, keySlot int) engine.Instruction {
	return engine.Instruction{
		Name: "ENFORCE_COST_BUDGET",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Validate slot index
			if costSlot < 0 || costSlot >= len(ctx.IntSlots) {
				// Invalid slot, but don't fail the request â€” just continue
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

			// Get quota key (use custom key if provided, otherwise fall back to tenant key)
			quotaKey := ctx.TenantKey
			if keySlot >= 0 && keySlot < len(ctx.ByteSlots) {
				if k := strings.TrimSpace(string(ctx.ByteSlots[keySlot])); k != "" {
					quotaKey = k
				}
			}
			if quotaKey == "" {
				// Fallback: if no key available, skip quota check
				return s.PC + 1
			}

			// Check if tenant can afford this request
			allowed, reason, _ := quotaManager.CanAfford(quotaKey, estimatedCost)

			if !allowed {
				// Budget exceeded â€” return 429 (Payment Required)
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
				// Invalid slot, but don't fail â€” just skip recording
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

			// Get quota key (use custom key if provided, otherwise fall back to tenant key)
			quotaKey := ctx.TenantKey
			if cfg.KeySlot >= 0 && cfg.KeySlot < len(ctx.ByteSlots) {
				if k := strings.TrimSpace(string(ctx.ByteSlots[cfg.KeySlot])); k != "" {
					quotaKey = k
				}
			}
			if quotaKey == "" {
				// Fallback: skip recording if no quota key available
				return s.PC + 1
			}

			// 1. Update local quota manager (fast, in-memory)
			err := cfg.QuotaManager.RecordCost(quotaKey, actualCost)
			if err != nil {
				// Log error but don't fail the request (cost recording is best-effort)
				ctx.ErrorMsg = []byte(err.Error())
				// Continue anyway â€” don't stop the request
			}

			// 2. Schedule cost event emission (deferred, after response sent)
			// Capture values for closure
			if cfg.IngestPipeline != nil {
				capturedCost := actualCost
				var capturedModel string
				if cfg.ModelSlot >= 0 && cfg.ModelSlot < len(ctx.ByteSlots) {
					capturedModel = string(ctx.ByteSlots[cfg.ModelSlot])
				}
				capturedQuotaKey := quotaKey
				capturedTenantID := ctx.TenantID
				capturedTimestamp := time.Now().UnixNano()

				ctx.AfterResponse = append(ctx.AfterResponse, func() {
					// Build cost event payload
					payload := CostEventPayload{
						TenantKey: capturedQuotaKey,
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

// CalculateCostConfig holds configuration for the CalculateCost instruction.
type CalculateCostConfig struct {
	PricingManager  PricingLookup // nil = zero-cost fallback
	CostSlot        int           // IntSlot to write result into (fixed-point: value = cost * 1e9)
	InputTokensSlot int           // IntSlot containing input token count
	OutputTokensSlot int          // IntSlot containing output token count
	ModelSlot       int           // ByteSlot containing model ID string; -1 = not configured
}

// PricingLookup is a narrow interface satisfied by *pricing.PricingManager.
// Using an interface avoids a direct import of the pricing package from steps
// (keeps the dependency graph clean; control â†’ steps â†’ interface â† pricing).
type PricingLookup interface {
	// GetPriceByModelID returns the per-1M-token rates for a model.
	// Returns (0, 0, false) when the model is not in the catalog.
	GetPriceByModelID(modelID string) (inputPer1M, outputPer1M float64, ok bool)
}

// CalculateCost computes the cost from token counts and a pricing manager lookup.
// This step:
// 1. Reads input/output token counts from IntSlots
// 2. Reads the model ID from ByteSlots[ModelSlot] (if configured)
// 3. Looks up pricing via PricingManager.GetPriceByModelID (cache-only, ~50ns)
// 4. Calculates: cost = (inputTokens*inputRate + outputTokens*outputRate) / 1M
// 5. Stores result as fixed-point int64 in CostSlot: stored = int64(cost * 1e9)
//
// Graceful degradation: if PricingManager is nil, model slot is missing, or the
// model is not in the pricing catalog, cost is set to 0 and execution continues.
//
// Usage in a flow:
//
//	{"action": "calculate_cost",
//	 "key":                "cost_slot",
//	 "input_tokens_slot":  "in_tokens",
//	 "output_tokens_slot": "out_tokens",
//	 "model_slot":         "model_name"}
func CalculateCost(cfg CalculateCostConfig) engine.Instruction {
	return engine.Instruction{
		Name: "CALCULATE_COST",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			// Validate IntSlot indices
			nInt := len(ctx.IntSlots)
			if cfg.CostSlot < 0 || cfg.CostSlot >= nInt {
				return s.PC + 1
			}
			if cfg.InputTokensSlot < 0 || cfg.InputTokensSlot >= nInt {
				return s.PC + 1
			}
			if cfg.OutputTokensSlot < 0 || cfg.OutputTokensSlot >= nInt {
				return s.PC + 1
			}

			// Read token counts
			inputTokens := ctx.IntSlots[cfg.InputTokensSlot]
			outputTokens := ctx.IntSlots[cfg.OutputTokensSlot]

			if inputTokens == 0 && outputTokens == 0 {
				ctx.IntSlots[cfg.CostSlot] = 0
				return s.PC + 1
			}

			// Zero-cost fallback when no pricing manager is wired
			if cfg.PricingManager == nil {
				ctx.IntSlots[cfg.CostSlot] = 0
				return s.PC + 1
			}

			// Resolve model ID from ByteSlot
			var modelID string
			if cfg.ModelSlot >= 0 && cfg.ModelSlot < len(ctx.ByteSlots) {
				modelID = string(ctx.ByteSlots[cfg.ModelSlot])
			}
			if modelID == "" {
				ctx.IntSlots[cfg.CostSlot] = 0
				return s.PC + 1
			}

			// Look up pricing (cache-only, never blocks)
			inputRate, outputRate, ok := cfg.PricingManager.GetPriceByModelID(modelID)
			if !ok {
				// Model not in catalog â€” zero-cost, don't fail the request
				ctx.IntSlots[cfg.CostSlot] = 0
				return s.PC + 1
			}

			// cost = (inputTokens * inputRate + outputTokens * outputRate) / 1_000_000
			cost := (float64(inputTokens)*inputRate + float64(outputTokens)*outputRate) / 1e6

			// Store as fixed-point: int64(cost * 1e9) so downstream steps can work in integers
			ctx.IntSlots[cfg.CostSlot] = int64(cost * 1e9)
			return s.PC + 1
		},
	}
}
