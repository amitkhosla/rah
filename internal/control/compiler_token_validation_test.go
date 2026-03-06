package control

import (
	"testing"

	"rah/internal/config"
	"rah/internal/engine"
)

func TestCompileExecutableAddsTokenValidationInstruction(t *testing.T) {
	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	compiler := NewCompiler(fm)

	plan := compiler.CompileExecutable([]StepConfig{
		{Action: "token_validation", KeyIdentifier: "header.Authorization", Input: map[string]string{"jwt.jwks_url": "https://issuer/.well-known/jwks.json"}},
	}, nil)

	if len(plan) != 2 {
		t.Fatalf("expected 2 instructions (bind + validation), got %d", len(plan))
	}
	if got := plan[0].Name; got != "BIND_HEADER" {
		t.Fatalf("expected first instruction BIND_HEADER, got %s", got)
	}
	if got := plan[1].Name; got != "TOKEN_VALIDATE" {
		t.Fatalf("expected second instruction TOKEN_VALIDATE, got %s", got)
	}
}
