package datasource

import (
	"context"
	"fmt"
	"os"
	"testing"
)

type mockResolver struct {
	val []byte
	err error
}

func (m *mockResolver) Resolve(_ context.Context, _ string) ([]byte, error) {
	return m.val, m.err
}

func TestResolveDSN_EnvPrefix(t *testing.T) {
	os.Setenv("TEST_DSN_VAR", "postgres://localhost/testdb")
	defer os.Unsetenv("TEST_DSN_VAR")
	got := resolveDSN("env:TEST_DSN_VAR", nil)
	if got != "postgres://localhost/testdb" {
		t.Fatalf("expected resolved DSN, got %q", got)
	}
}

func TestResolveDSN_NilResolver_NonEnv(t *testing.T) {
	// nil resolver: non-env refs fall back to literal
	got := resolveDSN("gsm:projects/x/secrets/y", nil)
	if got != "gsm:projects/x/secrets/y" {
		t.Fatalf("expected literal fallback, got %q", got)
	}
}

func TestResolveDSN_SecretResolver_Called(t *testing.T) {
	r := &mockResolver{val: []byte("resolved-secret-dsn")}
	got := resolveDSN("gsm:projects/x/secrets/y", r)
	if got != "resolved-secret-dsn" {
		t.Fatalf("expected resolver result, got %q", got)
	}
}

func TestResolveDSN_SecretResolver_Error_FallsBackToLiteral(t *testing.T) {
	r := &mockResolver{err: fmt.Errorf("secret not found")}
	got := resolveDSN("gsm:projects/x/secrets/y", r)
	if got != "gsm:projects/x/secrets/y" {
		t.Fatalf("expected literal fallback on error, got %q", got)
	}
}

func TestResolveDSN_Literal_NoScheme(t *testing.T) {
	got := resolveDSN("postgres://user:pass@host/db", nil)
	if got != "postgres://user:pass@host/db" {
		t.Fatalf("expected literal passthrough, got %q", got)
	}
}

func TestResolveDSN_VaultScheme(t *testing.T) {
	r := &mockResolver{val: []byte("vault-resolved-dsn")}
	got := resolveDSN("vault:secret/data/db/dsn", r)
	if got != "vault-resolved-dsn" {
		t.Fatalf("expected resolver result for vault scheme, got %q", got)
	}
}

func TestResolveDSN_AWSSecretsManager(t *testing.T) {
	r := &mockResolver{val: []byte("asm-resolved-dsn")}
	got := resolveDSN("asm:us-east-1/db-secret", r)
	if got != "asm-resolved-dsn" {
		t.Fatalf("expected resolver result for ASM scheme, got %q", got)
	}
}
