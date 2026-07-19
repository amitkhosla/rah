package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"testing"
)

// randomKey generates a random 32-byte key for testing.
func randomKey(t *testing.T) [32]byte {
	t.Helper()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	return key
}

// TestAESEncryptor_RoundTrip verifies that encrypt→decrypt returns the original plaintext.
func TestAESEncryptor_RoundTrip(t *testing.T) {
	key := randomKey(t)
	enc := NewAESEncryptor(key, "k1")

	plaintext := []byte("super-secret-api-key-1234567890")
	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

// TestAESEncryptor_DifferentNonces verifies that two encryptions of the same plaintext
// produce different ciphertexts (probabilistic nonce ensures semantic security).
func TestAESEncryptor_DifferentNonces(t *testing.T) {
	key := randomKey(t)
	enc := NewAESEncryptor(key, "k1")

	plaintext := []byte("same plaintext")
	ct1, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt (1): %v", err)
	}
	ct2, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt (2): %v", err)
	}
	if ct1 == ct2 {
		t.Error("two encryptions of the same plaintext produced identical ciphertexts (nonce reuse!)")
	}
}

// TestAESEncryptor_Passthrough verifies that Decrypt returns a non-"enc:" value as-is.
func TestAESEncryptor_Passthrough(t *testing.T) {
	key := randomKey(t)
	enc := NewAESEncryptor(key, "k1")

	stored := "not-encrypted"
	got, err := enc.Decrypt(stored)
	if err != nil {
		t.Fatalf("Decrypt passthrough: %v", err)
	}
	if string(got) != stored {
		t.Errorf("passthrough: got %q, want %q", got, stored)
	}
}

// TestAESEncryptor_TamperedCiphertext verifies that a corrupted ciphertext returns an error.
func TestAESEncryptor_TamperedCiphertext(t *testing.T) {
	key := randomKey(t)
	enc := NewAESEncryptor(key, "k1")

	ct, err := enc.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Corrupt the base64 payload by appending garbage bytes before re-encoding.
	// The format is "enc:k1:<base64>"; decode, flip a byte, re-encode.
	parts := splitN(ct, ":", 3)
	payload, _ := base64.StdEncoding.DecodeString(parts[2])
	if len(payload) > 0 {
		payload[len(payload)-1] ^= 0xFF // flip last byte (tag area)
	}
	tampered := "enc:" + parts[1] + ":" + base64.StdEncoding.EncodeToString(payload)

	_, err = enc.Decrypt(tampered)
	if err == nil {
		t.Error("expected error for tampered ciphertext, got nil")
	}
}

// splitN is a helper to avoid importing strings in the test.
func splitN(s, sep string, n int) []string {
	result := make([]string, 0, n)
	for i := 0; i < n-1; i++ {
		idx := indexOf(s, sep)
		if idx < 0 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	result = append(result, s)
	return result
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestDeriveKey_Deterministic verifies that the same passphrase+salt produces the same key.
func TestDeriveKey_Deterministic(t *testing.T) {
	passphrase := "my-secure-passphrase"
	salt := []byte("fixed-16-byte-sa") // exactly 16 bytes

	k1 := DeriveKey(passphrase, salt)
	k2 := DeriveKey(passphrase, salt)

	if k1 != k2 {
		t.Error("DeriveKey is not deterministic: same inputs produced different keys")
	}
}

// TestDeriveKey_DifferentSalt verifies that different salts produce different keys.
func TestDeriveKey_DifferentSalt(t *testing.T) {
	passphrase := "my-secure-passphrase"
	salt1 := []byte("fixed-16-byte-sa")
	salt2 := []byte("other-16-byte-sa")

	k1 := DeriveKey(passphrase, salt1)
	k2 := DeriveKey(passphrase, salt2)

	if k1 == k2 {
		t.Error("DeriveKey: different salts produced the same key")
	}
}

// TestLoadMasterKey_EnvVar verifies that LoadMasterKey reads a base64 key from an env var.
func TestLoadMasterKey_EnvVar(t *testing.T) {
	key := randomKey(t)
	encoded := base64.StdEncoding.EncodeToString(key[:])

	envVar := "RAH_TEST_MASTER_KEY_" + t.Name()
	t.Setenv(envVar, encoded)

	enc, err := LoadMasterKey(MasterKeyConfig{
		KeyEnv:     envVar,
		KeyVersion: "k1",
	})
	if err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	if enc == nil {
		t.Fatal("LoadMasterKey returned nil encryptor")
	}
	if enc.Version() != "k1" {
		t.Errorf("Version: got %q, want %q", enc.Version(), "k1")
	}

	// Verify it can round-trip.
	pt := []byte("test-payload")
	ct, err := enc.Encrypt(pt)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(pt) {
		t.Errorf("round-trip: got %q, want %q", got, pt)
	}
}

// TestLoadMasterKey_NoConfig verifies that LoadMasterKey returns NoopEncryptor when unconfigured.
func TestLoadMasterKey_NoConfig(t *testing.T) {
	enc, err := LoadMasterKey(MasterKeyConfig{})
	if err != nil {
		t.Fatalf("LoadMasterKey (no config): unexpected error: %v", err)
	}
	if enc != NoopEncryptor {
		t.Errorf("expected NoopEncryptor, got %T", enc)
	}
}

// TestNoopEncryptor_RoundTrip verifies that the noop encryptor returns the original value.
func TestNoopEncryptor_RoundTrip(t *testing.T) {
	plaintext := []byte("plaintext-value")
	ct, err := NoopEncryptor.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("NoopEncryptor.Encrypt: %v", err)
	}
	got, err := NoopEncryptor.Decrypt(ct)
	if err != nil {
		t.Fatalf("NoopEncryptor.Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("noop round-trip: got %q, want %q", got, plaintext)
	}
}

// TestLoadMasterKey_KeyPath verifies that LoadMasterKey reads a key from a file.
func TestLoadMasterKey_KeyPath(t *testing.T) {
	key := randomKey(t)
	encoded := base64.StdEncoding.EncodeToString(key[:])

	f, err := os.CreateTemp(t.TempDir(), "rahkey-*.txt")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	if _, err := f.WriteString(encoded); err != nil {
		t.Fatalf("writing key file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing key file: %v", err)
	}

	enc, err := LoadMasterKey(MasterKeyConfig{
		KeyPath:    f.Name(),
		KeyVersion: "k2",
	})
	if err != nil {
		t.Fatalf("LoadMasterKey (file): %v", err)
	}
	if enc.Version() != "k2" {
		t.Errorf("Version: got %q, want %q", enc.Version(), "k2")
	}
}

// TestLoadMasterKey_Passphrase verifies that LoadMasterKey derives a key from a passphrase.
func TestLoadMasterKey_Passphrase(t *testing.T) {
	envVar := "RAH_TEST_PASSPHRASE_" + t.Name()
	t.Setenv(envVar, "my-test-passphrase")

	// 16 bytes of hex = 32 hex chars
	saltHex := "0102030405060708090a0b0c0d0e0f10"

	enc, err := LoadMasterKey(MasterKeyConfig{
		PassphraseEnv: envVar,
		Salt:          saltHex,
		KeyVersion:    "k1",
	})
	if err != nil {
		t.Fatalf("LoadMasterKey (passphrase): %v", err)
	}

	// Round-trip smoke test.
	pt := []byte("passphrase-derived")
	ct, err := enc.Encrypt(pt)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(pt) {
		t.Errorf("passphrase round-trip: got %q, want %q", got, pt)
	}
}
