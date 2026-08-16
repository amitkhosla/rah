package steps

import (
	"context"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// LoadLLMKeyConfig configures the LoadLLMKey instruction.
type LoadLLMKeyConfig struct {
	// CredNameSlot is the ByteSlot index holding the credential name to look up
	// (e.g. "anthropic_key", "openai_key").
	CredNameSlot int

	// OutSlot is the ByteSlot index to write the resolved plaintext API key into.
	OutSlot int

	// Reg is the credential registry used to look up and resolve the credential.
	// Uses the same CredentialLookup interface as LoadCredential.
	// If nil the instruction is a no-op (baked key fallback).
	Reg CredentialLookup
}

// LoadLLMKey returns an instruction that fetches a per-tenant LLM API key from
// the CredentialRegistry and writes it into a ByteSlot for use by a subsequent
// llm_call instruction (via APIKeySlot).
//
// Failure modes are all silent no-ops so the flow can fall back to the baked
// key set on LLMCallConfig.APIKey:
//   - Reg == nil            â†’ no-op
//   - CredNameSlot empty    â†’ no-op
//   - credential not found  â†’ no-op
//   - resolution error      â†’ no-op
func LoadLLMKey(cfg LoadLLMKeyConfig) engine.Instruction {
	return engine.Instruction{
		Name: "load_llm_key",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Guard: registry must be configured.
			if cfg.Reg == nil {
				return state.PC + 1
			}

			// Guard: credential name slot must hold a non-empty value.
			if cfg.CredNameSlot < 0 || cfg.CredNameSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			credName := ctx.ByteSlots[cfg.CredNameSlot]
			if len(credName) == 0 {
				return state.PC + 1
			}

			// Resolve: tenant-specific first, then global fallback.
			plaintext, err := cfg.Reg.Resolve(context.Background(), string(credName), ctx.TenantKey)
			if err != nil || len(plaintext) == 0 {
				// Silent fallback — baked key will be used by llm_call.
				return state.PC + 1
			}

			// Write resolved key to output slot.
			if cfg.OutSlot >= 0 && cfg.OutSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.OutSlot] = append(ctx.ByteSlots[cfg.OutSlot][:0], plaintext...)
			}

			return state.PC + 1
		},
	}
}
