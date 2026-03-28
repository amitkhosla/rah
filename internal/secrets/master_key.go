package secrets

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// MasterKeyConfig describes how to load the master encryption key.
// Priority: KeyEnv > KeyPath > (PassphraseEnv + Salt) > none (noop mode).
type MasterKeyConfig struct {
	// KeyEnv: name of env var containing base64-encoded 32-byte key.
	// e.g. RAH_MASTER_KEY
	KeyEnv string

	// KeyPath: path to file containing base64-encoded 32-byte key (or raw 32 bytes).
	KeyPath string

	// PassphraseEnv: name of env var containing a UTF-8 passphrase.
	// Requires Salt to be set.
	PassphraseEnv string

	// Salt: hex-encoded 16+ byte salt for passphrase KDF.
	Salt string

	// KeyVersion: version tag embedded in ciphertext (default "k1").
	KeyVersion string
}

// LoadMasterKey loads the master key according to cfg and returns an Encryptor.
// If no key source is configured, returns NoopEncryptor and no error.
// Returns an error only if a source is configured but fails to load.
func LoadMasterKey(cfg MasterKeyConfig) (Encryptor, error) {
	version := cfg.KeyVersion
	if version == "" {
		version = "k1"
	}

	// Priority 1: KeyEnv
	if cfg.KeyEnv != "" {
		val, ok := os.LookupEnv(cfg.KeyEnv)
		if !ok {
			return nil, fmt.Errorf("secrets: master key env var %q is not set", cfg.KeyEnv)
		}
		key, err := decodeKey(val)
		if err != nil {
			return nil, fmt.Errorf("secrets: master key from env %q: %w", cfg.KeyEnv, err)
		}
		return NewAESEncryptor(key, version), nil
	}

	// Priority 2: KeyPath
	if cfg.KeyPath != "" {
		data, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("secrets: reading master key file %q: %w", cfg.KeyPath, err)
		}
		key, err := decodeKey(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, fmt.Errorf("secrets: master key from file %q: %w", cfg.KeyPath, err)
		}
		return NewAESEncryptor(key, version), nil
	}

	// Priority 3: PassphraseEnv + Salt
	if cfg.PassphraseEnv != "" && cfg.Salt != "" {
		passphrase, ok := os.LookupEnv(cfg.PassphraseEnv)
		if !ok {
			return nil, fmt.Errorf("secrets: passphrase env var %q is not set", cfg.PassphraseEnv)
		}
		salt, err := hex.DecodeString(cfg.Salt)
		if err != nil {
			return nil, fmt.Errorf("secrets: decoding salt (expected hex): %w", err)
		}
		if len(salt) < 16 {
			return nil, fmt.Errorf("secrets: salt must be 16+ bytes, got %d", len(salt))
		}
		key := DeriveKey(passphrase, salt)
		return NewAESEncryptor(key, version), nil
	}

	// No key source configured — noop mode.
	return NoopEncryptor, nil
}

// decodeKey decodes a base64-encoded 32-byte key string.
// If the input is not valid base64, it checks whether it is raw 32 bytes.
func decodeKey(s string) ([32]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// Try raw bytes (e.g. if the file contains the raw 32-byte key).
		if len(s) == 32 {
			var key [32]byte
			copy(key[:], s)
			return key, nil
		}
		return [32]byte{}, fmt.Errorf("base64 decode failed and not raw 32 bytes: %w", err)
	}
	if len(decoded) != 32 {
		return [32]byte{}, fmt.Errorf("key must be 32 bytes (AES-256), got %d", len(decoded))
	}
	var key [32]byte
	copy(key[:], decoded)
	return key, nil
}
