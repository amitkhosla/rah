package secrets

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters for DeriveKey.
// Conservative defaults suitable for interactive use.
const (
	kdfTime    = 1
	kdfMemory  = 64 * 1024 // 64 MB
	kdfThreads = 4
	kdfKeyLen  = 32
)

// DeriveKey derives a 32-byte AES key from a passphrase using Argon2id.
// salt must be 16+ bytes; use a fixed per-deployment salt stored in config.
// The output is deterministic: same passphrase + same salt → same key.
func DeriveKey(passphrase string, salt []byte) [32]byte {
	raw := argon2.IDKey([]byte(passphrase), salt, kdfTime, kdfMemory, kdfThreads, kdfKeyLen)
	var key [32]byte
	copy(key[:], raw)
	return key
}

// GenerateSalt generates a cryptographically random 16-byte salt for use
// with DeriveKey. Call once at deployment time; store the result in config.
func GenerateSalt() ([16]byte, error) {
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return [16]byte{}, fmt.Errorf("secrets: generating salt: %w", err)
	}
	return salt, nil
}
