package secrets

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
)

// TestEncryptedValueProvider_DecryptsVersionedFormat verifies that a value
// encrypted with AESEncryptor (versioned format "enc:<version>:<base64>") is
// correctly decrypted by EncryptedValueProvider.Resolve.
func TestEncryptedValueProvider_DecryptsVersionedFormat(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("generating key: %v", err)
	}
	enc := NewAESEncryptor(key, "k1")
	provider := NewEncryptedValueProvider(enc)

	plaintext := []byte("super-secret-value-123")
	ref, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Verify the encrypted ref has the versioned format.
	if !isVersionedEncRef(ref) {
		t.Errorf("expected versioned ref, got %q", ref)
	}

	got, err := provider.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("Resolve: got %q, want %q", got, plaintext)
	}
}

// TestEncryptedValueProvider_IgnoresOldFormat verifies that refs in the old
// "enc:<base64>" format (no version segment) are rejected with ErrNotHandled.
func TestEncryptedValueProvider_IgnoresOldFormat(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("generating key: %v", err)
	}
	enc := NewAESEncryptor(key, "k1")
	provider := NewEncryptedValueProvider(enc)

	// Simulate an old-format ref: "enc:<base64>" with no version segment.
	// Base64 characters never include ':', so this has no second colon.
	oldRef := "enc:AAECBAUGB"

	_, err := provider.Resolve(context.Background(), oldRef)
	if err == nil {
		t.Fatal("expected ErrNotHandled for old-format ref, got nil")
	}
	if !errors.Is(err, ErrNotHandled) {
		t.Errorf("expected ErrNotHandled, got %v", err)
	}
}

// TestEncryptedValueProvider_IgnoresNonEncRef verifies that non-enc refs
// (e.g. "gsm://..." or "env:VAR") are rejected with ErrNotHandled.
func TestEncryptedValueProvider_IgnoresNonEncRef(t *testing.T) {
	provider := NewEncryptedValueProvider(NoopEncryptor)

	nonEncRefs := []string{
		"gsm://my-project/secrets/my-secret/versions/latest",
		"env:MY_VAR",
		"$MY_VAR",
		"file:///run/secrets/key",
		"plaintext-value",
		"vault://mount/path#field",
	}

	for _, ref := range nonEncRefs {
		_, err := provider.Resolve(context.Background(), ref)
		if err == nil {
			t.Errorf("ref %q: expected ErrNotHandled, got nil", ref)
			continue
		}
		if !errors.Is(err, ErrNotHandled) {
			t.Errorf("ref %q: expected ErrNotHandled, got %v", ref, err)
		}
	}
}

// TestEncryptedValueProvider_NoopEncryptor verifies that with NoopEncryptor,
// a plaintext value stored and retrieved via the provider round-trips correctly.
//
// Note: NoopEncryptor.Encrypt returns the plaintext as-is (no "enc:" prefix),
// so stored values will NOT be in versioned enc format. Therefore, when the
// noop-encrypted ref is passed to Resolve it returns ErrNotHandled (correct:
// noop values are plain strings, not versioned enc refs).
//
// This test verifies that a manually-constructed versioned-format ref can be
// decrypted by NoopEncryptor via the provider (since Decrypt is a passthrough).
func TestEncryptedValueProvider_NoopEncryptor(t *testing.T) {
	provider := NewEncryptedValueProvider(NoopEncryptor)

	// NoopEncryptor.Encrypt returns the plaintext as-is.
	// That means the stored ref won't have "enc:<version>:" prefix, so
	// Resolve will return ErrNotHandled — that's correct behaviour.
	plainRef, err := NoopEncryptor.Encrypt([]byte("my-secret"))
	if err != nil {
		t.Fatalf("NoopEncryptor.Encrypt: %v", err)
	}
	// plainRef == "my-secret" — not a versioned ref → ErrNotHandled.
	_, err = provider.Resolve(context.Background(), plainRef)
	if !errors.Is(err, ErrNotHandled) {
		t.Errorf("noop plaintext ref: expected ErrNotHandled, got %v", err)
	}

	// Simulate a versioned ref that NoopEncryptor.Decrypt passes through.
	// NoopEncryptor.Decrypt returns the stored string as []byte regardless of format.
	versionedRef := "enc:noop:aGVsbG8="
	got, err := provider.Resolve(context.Background(), versionedRef)
	if err != nil {
		t.Fatalf("Resolve versioned ref via NoopEncryptor: %v", err)
	}
	// NoopEncryptor.Decrypt is a passthrough, so it returns the full ref as bytes.
	if string(got) != versionedRef {
		t.Errorf("NoopEncryptor passthrough: got %q, want %q", got, versionedRef)
	}
}

// TestIsVersionedEncRef verifies the detection logic for versioned enc refs.
func TestIsVersionedEncRef(t *testing.T) {
	cases := []struct {
		ref     string
		versioned bool
	}{
		{"enc:k1:AAABBB==", true},
		{"enc:k2:somepayload", true},
		{"enc:AAABBB==", false},    // old format, no version
		{"enc:noop:payload", true}, // noop version
		{"gsm://project/s", false},
		{"env:MY_VAR", false},
		{"plaintext", false},
		{"enc:", false},            // incomplete
		{"enc:k1:", true},          // version + empty payload (still has second colon)
	}

	for _, tc := range cases {
		got := isVersionedEncRef(tc.ref)
		if got != tc.versioned {
			t.Errorf("isVersionedEncRef(%q) = %v, want %v", tc.ref, got, tc.versioned)
		}
	}
}
