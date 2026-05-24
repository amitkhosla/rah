package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// PrefixLen is the number of leading characters of the raw key stored for
	// display purposes so customers can identify which key they are using.
	PrefixLen = 12
)

// APIKeyRecord is the full management-plane record. It is persisted to the
// backing store. The Hash field is never returned in API responses.
type APIKeyRecord struct {
	KeyID          uint32   `json:"key_id"`
	AppID          uint32   `json:"app_id"`
	Alias          string   `json:"alias"`                     // e.g. "prod-key-1"
	Prefix         string   `json:"prefix"`                    // first PrefixLen chars of raw key
	Hash           string   `json:"hash"`                      // hex(SHA256(rawKey)) — persisted, never in responses
	AllowedTenants []uint16 `json:"allowed_tenants,omitempty"` // nil = unrestricted
	Enabled        bool     `json:"enabled"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
}

// APIKeyEntry is the minimal runtime struct kept in gateway memory.
// The hash is the map key — it is never stored inside the value.
type APIKeyEntry struct {
	KeyID          uint32
	AppID          uint32   // copied into ctx.CallerID
	Alias          string   // copied into ctx.CallerKey for logging
	Enabled        bool
	AllowedTenants []uint16 // nil = unrestricted
}

// APIKeyView is the API-safe projection of APIKeyRecord. Hash is omitted.
type APIKeyView struct {
	KeyID          uint32   `json:"key_id"`
	AppID          uint32   `json:"app_id"`
	Alias          string   `json:"alias"`
	Prefix         string   `json:"prefix"`
	AllowedTenants []uint16 `json:"allowed_tenants,omitempty"`
	Enabled        bool     `json:"enabled"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
}

// CreateResponse is the one-time response returned on key creation or rotation.
// RawKey is shown once and never stored.
type CreateResponse struct {
	APIKeyView
	RawKey string `json:"key"`
}

var (
	globalKeyIDCounter atomic.Uint32
	globalKeysByHash   sync.Map // SHA256-hex → APIKeyEntry  (hot path, O(1))
	globalKeysByID     sync.Map // uint32 KeyID → APIKeyRecord (management plane)
)

// NextKeyID returns the next monotonically increasing Key ID.
func NextKeyID() uint32 {
	return globalKeyIDCounter.Add(1)
}

// BumpKeyIDCounterIfNeeded ensures the counter is at least id so that future
// calls to NextKeyID do not collide with restored records.
func BumpKeyIDCounterIfNeeded(id uint32) {
	for {
		cur := globalKeyIDCounter.Load()
		if id <= cur {
			return
		}
		if globalKeyIDCounter.CompareAndSwap(cur, id) {
			return
		}
	}
}

// HashKey returns the hex-encoded SHA256 of a raw API key string.
func HashKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

// Generate creates a new raw API key and returns the raw key alongside the
// full APIKeyRecord ready for storage. The raw key follows the format:
//
//	"rah_" + base64url(32 random bytes)  ≈ 47 chars
//
// The prefix (first PrefixLen chars) and SHA256 hash are stored; the raw key
// is returned to the caller once and never persisted by this package.
func Generate(appID uint32, alias string, allowedTenants []uint16) (rawKey string, rec APIKeyRecord, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		err = fmt.Errorf("apikey: rand read: %w", err)
		return
	}
	rawKey = "rah_" + base64.RawURLEncoding.EncodeToString(buf)

	now := time.Now().Unix()
	rec = APIKeyRecord{
		KeyID:          NextKeyID(),
		AppID:          appID,
		Alias:          alias,
		Prefix:         rawKey[:PrefixLen],
		Hash:           HashKey(rawKey),
		AllowedTenants: allowedTenants,
		Enabled:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return
}

// UpsertKey adds or replaces a key record in both the hash and ID indexes.
// On rotation the old hash entry is removed using the existing record's hash.
func UpsertKey(rec APIKeyRecord) {
	rec.UpdatedAt = time.Now().Unix()

	// If there is an existing record with a different hash, remove the stale
	// hash entry so the old raw key no longer validates.
	if existing, ok := globalKeysByID.Load(rec.KeyID); ok {
		old := existing.(APIKeyRecord)
		if old.Hash != rec.Hash {
			globalKeysByHash.Delete(old.Hash)
		}
	}

	entry := APIKeyEntry{
		KeyID:          rec.KeyID,
		AppID:          rec.AppID,
		Alias:          rec.Alias,
		Enabled:        rec.Enabled,
		AllowedTenants: rec.AllowedTenants,
	}
	globalKeysByHash.Store(rec.Hash, entry)
	globalKeysByID.Store(rec.KeyID, rec)
}

// DeleteKey removes a key from both indexes.
func DeleteKey(keyID uint32) {
	if v, ok := globalKeysByID.Load(keyID); ok {
		rec := v.(APIKeyRecord)
		globalKeysByHash.Delete(rec.Hash)
	}
	globalKeysByID.Delete(keyID)
}

// LookupByHash is the hot-path O(1) lookup used by the validate_api_key step.
// Returns nil on cache miss.
func LookupByHash(hash string) *APIKeyEntry {
	v, ok := globalKeysByHash.Load(hash)
	if !ok {
		return nil
	}
	e := v.(APIKeyEntry)
	return &e
}

// GetRecord returns the management-plane record for a key ID, or nil.
func GetRecord(keyID uint32) *APIKeyRecord {
	v, ok := globalKeysByID.Load(keyID)
	if !ok {
		return nil
	}
	r := v.(APIKeyRecord)
	return &r
}

// ListKeys returns all APIKeyRecord values in arbitrary order.
func ListKeys() []APIKeyRecord {
	var out []APIKeyRecord
	globalKeysByID.Range(func(_, v any) bool {
		out = append(out, v.(APIKeyRecord))
		return true
	})
	return out
}

// ListKeysByApp returns all keys belonging to a specific AppID.
func ListKeysByApp(appID uint32) []APIKeyRecord {
	var out []APIKeyRecord
	globalKeysByID.Range(func(_, v any) bool {
		r := v.(APIKeyRecord)
		if r.AppID == appID {
			out = append(out, r)
		}
		return true
	})
	return out
}

// ToView returns an API-safe projection of the record (hash omitted).
func (r APIKeyRecord) ToView() APIKeyView {
	return APIKeyView{
		KeyID:          r.KeyID,
		AppID:          r.AppID,
		Alias:          r.Alias,
		Prefix:         r.Prefix,
		AllowedTenants: r.AllowedTenants,
		Enabled:        r.Enabled,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}
