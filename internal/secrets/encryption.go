package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// Encryptor encrypts and decrypts credential values stored at rest.
// All implementations must be safe for concurrent use.
type Encryptor interface {
	// Encrypt encrypts plaintext and returns a versioned ciphertext string.
	// Format: "enc:<keyVersion>:<base64(nonce||ciphertext||tag)>"
	Encrypt(plaintext []byte) (string, error)

	// Decrypt decrypts a stored value. If the value does not start with "enc:",
	// it is returned as-is (plaintext passthrough for migration compatibility).
	Decrypt(stored string) ([]byte, error)

	// Version returns the key version identifier (e.g. "k1") for rotation tracking.
	Version() string
}

// aesEncryptor implements Encryptor using AES-256-GCM.
// It is immutable after construction and safe for concurrent use.
type aesEncryptor struct {
	gcm        cipher.AEAD
	keyVersion string
}

// NewAESEncryptor creates an AES-256-GCM Encryptor from a 32-byte key.
// The keyVersion string is embedded in encrypted values for rotation tracking.
func NewAESEncryptor(key [32]byte, keyVersion string) Encryptor {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		// aes.NewCipher only errors on invalid key length; [32]byte is always valid.
		panic(fmt.Sprintf("secrets: NewAESEncryptor: aes.NewCipher: %v", err))
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(fmt.Sprintf("secrets: NewAESEncryptor: cipher.NewGCM: %v", err))
	}
	return &aesEncryptor{gcm: gcm, keyVersion: keyVersion}
}

// Version returns the key version identifier embedded in ciphertexts.
func (e *aesEncryptor) Version() string { return e.keyVersion }

// Encrypt encrypts plaintext with AES-256-GCM and returns a versioned string.
// Format: "enc:<keyVersion>:<base64(nonce||ciphertext||tag)>"
func (e *aesEncryptor) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets/enc: generating nonce: %w", err)
	}
	// Seal appends ciphertext+tag after nonce in the output slice.
	sealed := e.gcm.Seal(nonce, nonce, plaintext, nil)
	encoded := base64.StdEncoding.EncodeToString(sealed)
	return "enc:" + e.keyVersion + ":" + encoded, nil
}

// Decrypt decrypts a stored value produced by Encrypt.
// If stored does not start with "enc:", it is returned as-is (plaintext passthrough).
func (e *aesEncryptor) Decrypt(stored string) ([]byte, error) {
	if !strings.HasPrefix(stored, "enc:") {
		return []byte(stored), nil
	}

	// Format: "enc:<version>:<base64payload>"
	parts := strings.SplitN(stored, ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("secrets/enc: invalid encrypted format %q", stored)
	}
	// parts[0]="enc", parts[1]=version, parts[2]=base64payload
	payload, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("secrets/enc: base64 decode: %w", err)
	}

	nonceSize := e.gcm.NonceSize()
	if len(payload) < nonceSize {
		return nil, fmt.Errorf("secrets/enc: payload too short")
	}

	nonce, ciphertext := payload[:nonceSize], payload[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secrets/enc: decryption failed (wrong key or corrupted data): %w", err)
	}
	return plaintext, nil
}

// noopEncryptor is a passthrough Encryptor that does not encrypt.
// Used when no master key is configured (plaintext mode).
type noopEncryptor struct{}

// NoopEncryptor is a passthrough Encryptor that does not encrypt.
// Used when no master key is configured (plaintext mode).
var NoopEncryptor Encryptor = noopEncryptor{}

func (noopEncryptor) Version() string { return "noop" }

// Encrypt returns the plaintext as a plain string (no encryption).
func (noopEncryptor) Encrypt(plaintext []byte) (string, error) {
	return string(plaintext), nil
}

// Decrypt returns the stored value as-is (passthrough).
func (noopEncryptor) Decrypt(stored string) ([]byte, error) {
	return []byte(stored), nil
}
