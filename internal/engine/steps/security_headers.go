package steps

import (
	"rah/internal/engine"
	"rah/internal/rctx"
)

// Pre-baked security header name bytes — allocated once, zero cost at request time.
var (
	secHdrHSTS             = []byte("Strict-Transport-Security")
	secHdrFrameOptions     = []byte("X-Frame-Options")
	secHdrContentTypeOpts  = []byte("X-Content-Type-Options")
	secHdrReferrerPolicy   = []byte("Referrer-Policy")
	secHdrCSP              = []byte("Content-Security-Policy")
	secValNoSniff          = []byte("nosniff")
)

// SecurityHeadersStep sets optional HTTP security response headers.
//
// All configuration is captured at compile time as pre-baked []byte values.
// No allocations occur on the hot path — each enabled header is a single
// ctx.SetResponseHeader call with pre-computed name and value bytes.
//
// Every field is independently opt-in: omitting a field means that header is
// never set. There are no forced defaults.
//
// Parameters (all pre-baked at compile time, nil/zero = disabled):
//
//	hstsValue          — value for Strict-Transport-Security (e.g. "max-age=31536000; includeSubDomains")
//	frameOptions       — value for X-Frame-Options (e.g. "DENY" or "SAMEORIGIN")
//	contentTypeOptions — if true, sets X-Content-Type-Options: nosniff
//	referrerPolicy     — value for Referrer-Policy (e.g. "strict-origin-when-cross-origin")
//	csp                — value for Content-Security-Policy
func SecurityHeadersStep(
	hstsValue          []byte,
	frameOptions       []byte,
	contentTypeOptions bool,
	referrerPolicy     []byte,
	csp                []byte,
) engine.Instruction {
	return engine.Instruction{
		Name: "SET_SECURITY_HEADERS",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if len(hstsValue) > 0 {
				ctx.SetResponseHeader(secHdrHSTS, hstsValue)
			}
			if len(frameOptions) > 0 {
				ctx.SetResponseHeader(secHdrFrameOptions, frameOptions)
			}
			if contentTypeOptions {
				ctx.SetResponseHeader(secHdrContentTypeOpts, secValNoSniff)
			}
			if len(referrerPolicy) > 0 {
				ctx.SetResponseHeader(secHdrReferrerPolicy, referrerPolicy)
			}
			if len(csp) > 0 {
				ctx.SetResponseHeader(secHdrCSP, csp)
			}
			return s.PC + 1
		},
	}
}
