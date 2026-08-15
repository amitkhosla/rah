package control

import (
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
)

func TestCompileExecutableAddsTokenValidationInstruction(t *testing.T) {
	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	compiler := NewCompiler(fm)

	plan, err := compiler.CompileExecutable([]StepConfig{
		{Action: "token_validation", KeyIdentifier: "header.Authorization", Input: map[string]string{"jwt.jwks_url": "https://issuer/.well-known/jwks.json"}},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected compile error: %v", err)
	}

	// [0] SET_STREAM_RESPONSE_BODY — always first; flow has no http_call so streaming=true
	// [1] BIND_HEADER — auto-bind preamble for header.Authorization
	// [2] TOKEN_VALIDATE
	if len(plan) != 3 {
		t.Fatalf("expected 3 instructions (stream-flag + bind + validation), got %d", len(plan))
	}
	if got := plan[1].Name; got != "BIND_HEADER" {
		t.Fatalf("expected instruction[1] BIND_HEADER, got %s", got)
	}
	if got := plan[2].Name; got != "TOKEN_VALIDATE" {
		t.Fatalf("expected instruction[2] TOKEN_VALIDATE, got %s", got)
	}
}
