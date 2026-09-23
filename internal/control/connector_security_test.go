package control

import (
	"testing"
)

func TestMaskRef_EnvScheme(t *testing.T) {
	got := maskRef("env:DATABASE_URL")
	if got != "env:***" {
		t.Fatalf("expected env:***, got %q", got)
	}
}

func TestMaskRef_GSMScheme(t *testing.T) {
	got := maskRef("gsm:projects/x/secrets/y")
	if got != "gsm:***" {
		t.Fatalf("expected gsm:***, got %q", got)
	}
}

func TestMaskRef_PostgresDSN(t *testing.T) {
	got := maskRef("postgres://user:pass@host/db")
	if got != "postgres:***" {
		t.Fatalf("expected postgres:***, got %q", got)
	}
}

func TestMaskRef_Empty(t *testing.T) {
	got := maskRef("")
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestMaskRef_VaultScheme(t *testing.T) {
	got := maskRef("vault:secret/data/db/dsn")
	if got != "vault:***" {
		t.Fatalf("expected vault:***, got %q", got)
	}
}

func TestMaskRef_ASMScheme(t *testing.T) {
	got := maskRef("asm:us-east-1/db-secret")
	if got != "asm:***" {
		t.Fatalf("expected asm:***, got %q", got)
	}
}

func TestMaskRef_KeyValueWithPort(t *testing.T) {
	// A key=value connection string with a port number
	got := maskRef("host=localhost:5432")
	// First colon is at position after "host=localhost", so result is "host=localhost:***"
	if got != "host=localhost:***" {
		t.Fatalf("expected host=localhost:***, got %q", got)
	}
}
