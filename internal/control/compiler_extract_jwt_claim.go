package control

import (
	"fmt"
	"strings"

	"rah/internal/engine/steps"
)

// compileExtractJWTClaim resolves slots and appends the extract_jwt_claim instruction.
//
// Slot mapping:
//   - key_identifier → token source:
//       "header.Authorization" → TokenHeader="Authorization", StripBearer=true
//       "header.X-Token"       → TokenHeader="X-Token", StripBearer=false
//       "var.mytoken"          → TokenSlot=<resolved slot>
//   - as             → outputSlot
//   - input["claim_name"]    → ClaimName (default "sub")
//   - input["default_value"] → DefaultValue (default "")
func (c *Compiler) compileExtractJWTClaim(step StepConfig) error {
	outputSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("extract_jwt_claim: output slot %q: %w", step.As, err)
	}

	cfg := steps.ExtractJWTClaimConfig{
		TokenSlot:    -1,
		OutputSlot:   outputSlot,
		ClaimName:    step.Input["claim_name"],
		DefaultValue: step.Input["default_value"],
	}

	if cfg.ClaimName == "" {
		cfg.ClaimName = "sub"
	}

	// Parse token source from key_identifier.
	src := strings.TrimSpace(step.KeyIdentifier)
	switch {
	case strings.HasPrefix(src, "header."):
		cfg.TokenHeader = strings.TrimPrefix(src, "header.")
		cfg.StripBearer = strings.EqualFold(cfg.TokenHeader, "Authorization")
	case strings.HasPrefix(src, "var."):
		s, slotErr := c.getSlot(src)
		if slotErr != nil {
			return fmt.Errorf("extract_jwt_claim: token slot %q: %w", src, slotErr)
		}
		cfg.TokenSlot = s
	case src == "":
		// Default to Authorization header.
		cfg.TokenHeader = "Authorization"
		cfg.StripBearer = true
	default:
		// Treat as a raw header name.
		cfg.TokenHeader = src
	}

	c.GlobalTable = append(c.GlobalTable, steps.ExtractJWTClaim(cfg))
	return nil
}
