package steps

import (
	"context"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// CredentialLookup resolves named credentials with optional tenant scoping.
// Defined locally to avoid pulling the secrets package (and its dependencies)
// into the engine binary unconditionally.
type CredentialLookup interface {
	Resolve(ctx context.Context, name, tenant string) ([]byte, error)
}

// LoadCredential resolves a named credential from the CredentialRegistry and
// writes the plaintext into ByteSlots[slot].
//
// The lookup uses ctx.TenantKey as the tenant scope, then falls back to the
// global credential with the same name if no tenant-specific override exists.
//
// On resolution failure the request is aborted with HTTP 500.
//
// Example flow YAML:
//
//	steps:
//	  - action: load_credential
//	    source: payment-key       # logical name registered via POST /credentials
//	    as: bearer_token
//	  - action: set_header
//	    key: "Authorization"
//	    as: bearer_token
func LoadCredential(lookup CredentialLookup, name string, slot int) engine.Instruction {
	return engine.Instruction{
		Name: "load_credential[" + name + "]",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			val, err := lookup.Resolve(context.Background(), name, ctx.TenantKey)
			if err != nil {
				ctx.ResponseStatus = 500
				return -1
			}
			ctx.ByteSlots[slot] = val
			return s.PC + 1
		},
	}
}
