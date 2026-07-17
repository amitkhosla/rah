package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
)

// encryptedProvider resolves "enc:<base64url(nonce||ciphertext)>" references
// using AES-256-GCM. The master key is loaded once at startup via env/file.
//
// # Generating an encrypted value
//
//	key := make([]byte, 32)
//	rand.Read(key)
//	fmt.Println("key:", base64.StdEncoding.EncodeToString(key))  // store as env var
//
//	enc, _ := EncryptValue(key, []byte("mysecret"))
//	fmt.Println("enc:", enc)  // put in config as "enc:<enc>"
type encryptedProvider struct {
	gcm cipher.AEAD
}

func newEncryptedProvider(_ context.Context, cfg config.EncryptedConfig, bootstrap Resolver) (*encryptedProvider, error) {
	if cfg.Key == "" {
		return nil, fmt.Errorf("encrypted.key is required when encrypted provider is enabled")
	}

	// The master key ref is resolved via the bootstrap providers (env/file only).
	keyRef, err := bootstrap.Resolve(context.Background(), cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("resolving master key ref %q: %w", cfg.Key, err)
	}
	defer clear(keyRef)

	keyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyRef)))
	if err != nil {
		return nil, fmt.Errorf("decoding master key (expected base64): %w", err)
	}
	defer clear(keyBytes)

	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes (AES-256), got %d", len(keyBytes))
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}
	return &encryptedProvider{gcm: gcm}, nil
}

func (p *encryptedProvider) Scheme() string { return "enc" }

func (p *encryptedProvider) Resolve(_ context.Context, ref string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(ref, "enc:")
	if !ok {
		return nil, fmt.Errorf("secrets/enc: invalid reference %q", ref)
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("secrets/enc: base64 decode: %w", err)
	}

	nonceSize := p.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("secrets/enc: ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := p.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secrets/enc: decryption failed (wrong key or corrupted data): %w", err)
	}
	return plaintext, nil
}

// EncryptValue encrypts plaintext with the given 32-byte AES-256 key and
// returns the "enc:<base64>" string to put in config.
// This is a helper for operators generating encrypted config values.
func EncryptValue(key, plaintext []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return "enc:" + base64.StdEncoding.EncodeToString(sealed), nil
}
