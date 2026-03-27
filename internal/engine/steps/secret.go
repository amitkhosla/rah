package steps

import (
	"context"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// SecretLoader is the subset of secrets.Resolver used by this package.
// Defined locally to avoid importing the secrets package and pulling in
// its (potentially large) dependency tree.
type SecretLoader interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// LoadSecret resolves a credential reference and writes the plaintext into
// ByteSlots[slot] on every request.
//
// The resolved value is served from the secrets manager's in-memory cache
// after the first fetch, so per-request overhead is a cache lookup (~RLock +
// map read + slice copy) — not a network call.
//
// On resolution failure the request is aborted with HTTP 500. Use this step
// only for secrets that must be present for the flow to function correctly
// (e.g. upstream API keys, signing keys). If the secret is optional, handle
// the empty-slot case downstream.
//
// Example flow YAML:
//
//	steps:
//	  - action: load_secret
//	    source: "gsm://projects/my-proj/secrets/stripe-key/versions/latest"
//	    as: stripe_key
//	  - action: set_header
//	    key: "Authorization"
//	    as: stripe_key
func LoadSecret(loader SecretLoader, ref string, slot int) engine.Instruction {
	return engine.Instruction{
		Name: "load_secret[" + ref + "]",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			val, err := loader.Resolve(context.Background(), ref)
			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				ctx.ErrorMsg = ctx.Alloc(len("secret resolution failed"))
				copy(ctx.ErrorMsg, "secret resolution failed")
				return engine.StopPlan
			}
			ctx.ByteSlots[slot] = val
			return s.PC + 1
		},
	}
}
