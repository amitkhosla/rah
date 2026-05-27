package datastore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/hkdf"
)

// encMagic is the two-byte prefix identifying AES-256-GCM encrypted values.
// Values without this prefix are treated as plaintext (migration safety).
var encMagic = [2]byte{0xE5, 0xCE}

// EncryptingStore wraps any KeyValueStore and transparently encrypts Put values
// and decrypts Get values using AES-256-GCM.
//
// Wire format: [magic:2][version:1][nonce:12][ciphertext:N][gcm_tag:16]
// AAD = full scoped key "tenant:{t}:{domain}:{key}" — binds ciphertext to its location.
//
// Values without the magic prefix are passed through as plaintext (migration safety).
// ZSetStore and DistributedStore operations bypass encryption entirely.
//
// Key rotation: populate aeads with multiple version→AEAD entries. New values are
// encrypted with the AEAD at primaryVersion; reads look up the version byte embedded
// in the wire format, enabling zero-downtime rotation without re-encrypting old data.
//
// Per-tenant isolation (HKDF mode): when masterKeys is non-nil, each encrypt/decrypt
// call derives a tenant-specific 32-byte subkey via HKDF-SHA256 before building the
// AEAD. This ensures that master key material alone cannot decrypt a single tenant's
// data without performing the tenant-specific derivation step. masterKeys entries take
// priority over the pre-built aeads entries for the same version.
type EncryptingStore struct {
	inner          KeyValueStore
	aeads          map[byte]cipher.AEAD // version byte → pre-built AEAD (non-HKDF mode)
	masterKeys     map[byte][]byte      // version byte → 32-byte master key (HKDF mode)
	primaryVersion byte
	domain         string
}

// NewEncryptingStore wraps inner with AES-256-GCM encryption using a single key at version 1.
// key must be exactly 32 bytes (AES-256). Use NewEncryptingStoreMulti for key rotation.
func NewEncryptingStore(inner KeyValueStore, key []byte, domain string) (*EncryptingStore, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes (AES-256), got %d bytes", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM cipher: %w", err)
	}

	return &EncryptingStore{
		inner:          inner,
		aeads:          map[byte]cipher.AEAD{1: aead},
		primaryVersion: 1,
		domain:         domain,
	}, nil
}

// NewEncryptingStoreMulti wraps inner with AES-256-GCM encryption using multiple versioned keys.
// aeads maps version byte (1–255) to a pre-built cipher.AEAD.
// primaryVersion selects which AEAD encrypts new values; it must be present in aeads.
// Values encrypted under any version in aeads are decrypted transparently on read.
func NewEncryptingStoreMulti(inner KeyValueStore, aeads map[byte]cipher.AEAD, primaryVersion byte, domain string) (*EncryptingStore, error) {
	if len(aeads) == 0 {
		return nil, fmt.Errorf("at least one AEAD is required")
	}
	if _, ok := aeads[primaryVersion]; !ok {
		return nil, fmt.Errorf("primary version %d not found in provided AEADs", primaryVersion)
	}
	// defensive copy
	cp := make(map[byte]cipher.AEAD, len(aeads))
	for v, a := range aeads {
		cp[v] = a
	}
	return &EncryptingStore{
		inner:          inner,
		aeads:          cp,
		primaryVersion: primaryVersion,
		domain:         domain,
	}, nil
}

// NewEncryptingStoreHKDF wraps inner with per-tenant AES-256-GCM encryption using HKDF-SHA256
// key derivation. masterKeys maps version byte (1–255) to a 32-byte master key; a per-tenant
// subkey is derived from the master key on every encrypt/decrypt call:
//
//	subkey = HKDF-SHA256(masterKey, salt=tenant, info=domain)[:32]
//
// This ensures that the master key alone cannot decrypt data for a specific tenant without
// performing the tenant-specific HKDF step.
// primaryVersion selects which master key encrypts new values; it must be present in masterKeys.
// Values encrypted under any version in masterKeys are decrypted transparently on read.
// Each master key must be exactly 32 bytes (AES-256).
func NewEncryptingStoreHKDF(inner KeyValueStore, masterKeys map[byte][]byte, primaryVersion byte, domain string) (*EncryptingStore, error) {
	if len(masterKeys) == 0 {
		return nil, fmt.Errorf("at least one master key is required")
	}
	if _, ok := masterKeys[primaryVersion]; !ok {
		return nil, fmt.Errorf("primary version %d not found in provided master keys", primaryVersion)
	}
	// Validate key lengths and make defensive copies.
	cp := make(map[byte][]byte, len(masterKeys))
	for v, k := range masterKeys {
		if len(k) != 32 {
			return nil, fmt.Errorf("master key version %d must be 32 bytes (AES-256), got %d bytes", v, len(k))
		}
		kc := make([]byte, 32)
		copy(kc, k)
		cp[v] = kc
	}
	return &EncryptingStore{
		inner:          inner,
		masterKeys:     cp,
		primaryVersion: primaryVersion,
		domain:         domain,
	}, nil
}

// deriveAEAD derives a per-tenant AES-256-GCM AEAD from masterKey using HKDF-SHA256.
// salt = []byte(tenant), info = []byte(domain). Returns an error only on I/O failure
// (HKDF reader is memory-only and never fails in practice).
func deriveAEAD(masterKey []byte, tenant Tenant, domain string) (cipher.AEAD, error) {
	r := hkdf.New(sha256.New, masterKey, []byte(tenant), []byte(domain))
	subkey := make([]byte, 32)
	if _, err := io.ReadFull(r, subkey); err != nil {
		return nil, fmt.Errorf("hkdf derive subkey: %w", err)
	}
	block, err := aes.NewCipher(subkey)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher from derived key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM from derived key: %w", err)
	}
	return aead, nil
}

// ── KeyValueStore interface implementation ──────────────────────────────────

func (s *EncryptingStore) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
	encrypted, err := s.encrypt(tenant, key, value)
	if err != nil {
		return err
	}
	return s.inner.Put(ctx, tenant, key, encrypted)
}

func (s *EncryptingStore) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
	encrypted, found, err := s.inner.Get(ctx, tenant, key)
	if err != nil || !found {
		return encrypted, found, err
	}

	// Check if value is encrypted (has magic prefix).
	if !isEncrypted(encrypted) {
		// Plaintext — return as-is (migration safety).
		return encrypted, true, nil
	}

	// Decrypt.
	decrypted, err := s.decrypt(tenant, key, encrypted)
	if err != nil {
		return nil, false, err
	}

	return decrypted, true, nil
}

func (s *EncryptingStore) Delete(ctx context.Context, tenant Tenant, key string) error {
	return s.inner.Delete(ctx, tenant, key)
}

func (s *EncryptingStore) ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error) {
	return s.inner.ListKeys(ctx, tenant, prefix)
}

func (s *EncryptingStore) Kind() string {
	return s.inner.Kind()
}

func (s *EncryptingStore) Name() string {
	return s.inner.Name()
}

func (s *EncryptingStore) PoolStats() PoolStats {
	return s.inner.PoolStats()
}

func (s *EncryptingStore) Close() error {
	return s.inner.Close()
}

// ── BatchStore interface implementation ──────────────────────────────────────

// MultiGet decrypts each value returned from inner, or falls back to sequential
// Gets if inner does not implement BatchStore.
func (s *EncryptingStore) MultiGet(ctx context.Context, tenant Tenant, keys []string) (map[string][]byte, error) {
	var encryptedResults map[string][]byte
	var err error

	if b, ok := s.inner.(BatchStore); ok {
		encryptedResults, err = b.MultiGet(ctx, tenant, keys)
	} else {
		// Fall back to sequential Gets.
		encryptedResults = make(map[string][]byte, len(keys))
		for _, k := range keys {
			v, found, getErr := s.inner.Get(ctx, tenant, k)
			if getErr != nil {
				return nil, getErr
			}
			if found {
				encryptedResults[k] = v
			}
		}
	}
	if err != nil {
		return nil, err
	}

	// Decrypt each result.
	result := make(map[string][]byte, len(encryptedResults))
	for k, encrypted := range encryptedResults {
		// Check if value is encrypted.
		if !isEncrypted(encrypted) {
			// Plaintext — keep as-is.
			result[k] = encrypted
			continue
		}

		// Decrypt.
		decrypted, decErr := s.decrypt(tenant, k, encrypted)
		if decErr != nil {
			return nil, decErr
		}
		result[k] = decrypted
	}

	return result, nil
}

// MultiPut encrypts each value, then delegates to inner BatchStore if available,
// or falls back to sequential Puts.
func (s *EncryptingStore) MultiPut(ctx context.Context, tenant Tenant, kvs map[string][]byte) error {
	// Encrypt all values.
	encryptedKVs := make(map[string][]byte, len(kvs))
	for k, v := range kvs {
		encrypted, err := s.encrypt(tenant, k, v)
		if err != nil {
			return err
		}
		encryptedKVs[k] = encrypted
	}

	// Write using BatchStore if available, else sequential.
	if b, ok := s.inner.(BatchStore); ok {
		return b.MultiPut(ctx, tenant, encryptedKVs)
	}

	for k, encrypted := range encryptedKVs {
		if err := s.inner.Put(ctx, tenant, k, encrypted); err != nil {
			return err
		}
	}
	return nil
}

// ── ExpiringStore interface implementation ──────────────────────────────────

// PutWithTTL encrypts the value, then delegates to inner ExpiringStore if
// available, or falls back to plain Put.
func (s *EncryptingStore) PutWithTTL(ctx context.Context, tenant Tenant, key string, value []byte, ttl time.Duration) error {
	encrypted, err := s.encrypt(tenant, key, value)
	if err != nil {
		return err
	}

	if e, ok := s.inner.(ExpiringStore); ok {
		return e.PutWithTTL(ctx, tenant, key, encrypted, ttl)
	}

	return s.inner.Put(ctx, tenant, key, encrypted)
}

// ── Encryption/Decryption helpers ─────────────────────────────────────────────

// isEncrypted returns true if data starts with encMagic.
func isEncrypted(data []byte) bool {
	if len(data) < len(encMagic) {
		return false
	}
	return data[0] == encMagic[0] && data[1] == encMagic[1]
}

// encrypt encrypts plaintext value and returns wire format:
// [magic:2][version:1][nonce:12][ciphertext:N][gcm_tag:16]
// The version byte written is s.primaryVersion, enabling key rotation via NewEncryptingStoreMulti.
// When the primary version has a masterKeys entry, a per-tenant subkey is derived via HKDF-SHA256.
func (s *EncryptingStore) encrypt(tenant Tenant, key string, plaintext []byte) ([]byte, error) {
	var aead cipher.AEAD
	if masterKey, ok := s.masterKeys[s.primaryVersion]; ok {
		// HKDF mode: derive a tenant-specific subkey.
		derived, err := deriveAEAD(masterKey, tenant, s.domain)
		if err != nil {
			return nil, err
		}
		aead = derived
	} else {
		aead = s.aeads[s.primaryVersion]
	}

	// Build AAD from scoped key.
	aad := []byte(fmt.Sprintf("tenant:%s:%s:%s", tenant, s.domain, key))

	// Generate random 12-byte nonce.
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Encrypt plaintext with GCM (tag appended by Seal).
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)

	// Wire format: [magic:2][version:1][nonce:12][ciphertext+tag]
	result := make([]byte, 0, 2+1+12+len(ciphertext))
	result = append(result, encMagic[:]...)
	result = append(result, s.primaryVersion)
	result = append(result, nonce...)
	result = append(result, ciphertext...)

	return result, nil
}

// decrypt extracts wire format and decrypts:
// [magic:2][version:1][nonce:12][ciphertext:N][gcm_tag:16]
// The version byte is used to look up the correct key for decryption. In HKDF mode,
// a tenant-specific subkey is derived from the master key before opening the AEAD.
func (s *EncryptingStore) decrypt(tenant Tenant, key string, encrypted []byte) ([]byte, error) {
	// Validate format.
	if len(encrypted) < 2+1+12 {
		return nil, fmt.Errorf("encrypted value too short (minimum 15 bytes, got %d)", len(encrypted))
	}

	// Check magic.
	if encrypted[0] != encMagic[0] || encrypted[1] != encMagic[1] {
		return nil, fmt.Errorf("invalid magic bytes in encrypted value")
	}

	// Look up the key by the version byte embedded in the wire format.
	version := encrypted[2]

	var aead cipher.AEAD
	if masterKey, ok := s.masterKeys[version]; ok {
		// HKDF mode: derive a tenant-specific subkey for this version.
		derived, err := deriveAEAD(masterKey, tenant, s.domain)
		if err != nil {
			return nil, err
		}
		aead = derived
	} else if a, ok := s.aeads[version]; ok {
		aead = a
	} else {
		return nil, fmt.Errorf("unknown encryption key version: %d (not in active key set)", version)
	}

	// Extract nonce and ciphertext.
	nonce := encrypted[3 : 3+12]
	ciphertext := encrypted[3+12:]

	// Build AAD from scoped key.
	aad := []byte(fmt.Sprintf("tenant:%s:%s:%s", tenant, s.domain, key))

	// Decrypt.
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	return plaintext, nil
}
