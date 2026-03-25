package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// envProvider resolves "env:VAR_NAME", "$VAR_NAME", and "${VAR_NAME}" references.
type envProvider struct{}

func newEnvProvider() *envProvider { return &envProvider{} }

func (p *envProvider) Scheme() string { return "env" }

func (p *envProvider) Resolve(_ context.Context, ref string) ([]byte, error) {
	name := envVarName(ref)
	val, ok := os.LookupEnv(name)
	if !ok {
		return nil, fmt.Errorf("secrets/env: environment variable %q is not set", name)
	}
	return []byte(val), nil
}

// envVarName extracts the variable name from the three supported syntaxes:
//
//	"env:VAR"   → "VAR"
//	"$VAR"      → "VAR"
//	"${VAR}"    → "VAR"
func envVarName(ref string) string {
	switch {
	case strings.HasPrefix(ref, "env:"):
		return ref[4:]
	case strings.HasPrefix(ref, "${") && strings.HasSuffix(ref, "}"):
		return ref[2 : len(ref)-1]
	case strings.HasPrefix(ref, "$"):
		return ref[1:]
	}
	return ref
}
