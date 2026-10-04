package apikey

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
)

// Helper functions used by tests and benchmarks

// sha256sum computes SHA256 of a raw key string.
func sha256sum(rawKey string) [32]byte {
	return sha256.Sum256([]byte(rawKey))
}

// hexEncode converts a byte array to hex string.
func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}

// LookupByID returns the management-plane record for a key ID, or nil.
// This is an alias for GetRecord used in benchmarks.
func LookupByID(keyID uint32) *APIKeyRecord {
	return GetRecord(keyID)
}

// ============================================================================
// TIER 1: Functional Tests
// ============================================================================

// TestLookupByHash_Hit verifies that LookupByHash returns the correct entry
// after upserting a key.
func TestLookupByHash_Hit(t *testing.T) {
	appID := uint32(10001)
	keyID := uint32(10001)

	rec := APIKeyRecord{
		KeyID:     keyID,
		AppID:     appID,
		Alias:     "test-key-hit",
		Prefix:    "rah_test",
		Hash:      "testhash1234567890abcdef1234567890abcdef1234567890abcdef12345678",
		Enabled:   true,
		CreatedAt: 1000,
		UpdatedAt: 1000,
	}

	UpsertKey(rec)
	t.Cleanup(func() { DeleteKey(keyID) })

	entry := LookupByHash(rec.Hash)
	if entry == nil {
		t.Fatal("LookupByHash returned nil, expected non-nil")
	}

	if entry.KeyID != keyID {
		t.Errorf("KeyID mismatch: got %d, want %d", entry.KeyID, keyID)
	}

	if entry.AppID != appID {
		t.Errorf("AppID mismatch: got %d, want %d", entry.AppID, appID)
	}

	if entry.Alias != "test-key-hit" {
		t.Errorf("Alias mismatch: got %q, want %q", entry.Alias, "test-key-hit")
	}
}

// TestLookupByHash_Miss verifies that LookupByHash returns nil for non-existent hashes.
func TestLookupByHash_Miss(t *testing.T) {
	entry := LookupByHash("nonexistent1234567890abcdef1234567890abcdef1234567890abcdef")
	if entry != nil {
		t.Errorf("LookupByHash returned non-nil, expected nil")
	}
}

// TestListKeysByApp_ReturnsCorrectSubset verifies that ListKeysByApp returns
// exactly the keys belonging to a specific AppID.
func TestListKeysByApp_ReturnsCorrectSubset(t *testing.T) {
	app1 := uint32(10002)
	app2 := uint32(10003)

	// Create 3 keys for app1
	keys1 := []APIKeyRecord{
		{
			KeyID:     10101,
			AppID:     app1,
			Alias:     "app1-key1",
			Hash:      "hash_app1_key1_1234567890abcdef1234567890abcdef1234567890abc",
			Enabled:   true,
			CreatedAt: 1000,
			UpdatedAt: 1000,
		},
		{
			KeyID:     10102,
			AppID:     app1,
			Alias:     "app1-key2",
			Hash:      "hash_app1_key2_1234567890abcdef1234567890abcdef1234567890abc",
			Enabled:   true,
			CreatedAt: 1000,
			UpdatedAt: 1000,
		},
		{
			KeyID:     10103,
			AppID:     app1,
			Alias:     "app1-key3",
			Hash:      "hash_app1_key3_1234567890abcdef1234567890abcdef1234567890abc",
			Enabled:   true,
			CreatedAt: 1000,
			UpdatedAt: 1000,
		},
	}

	// Create 2 keys for app2
	keys2 := []APIKeyRecord{
		{
			KeyID:     10104,
			AppID:     app2,
			Alias:     "app2-key1",
			Hash:      "hash_app2_key1_1234567890abcdef1234567890abcdef1234567890abc",
			Enabled:   true,
			CreatedAt: 1000,
			UpdatedAt: 1000,
		},
		{
			KeyID:     10105,
			AppID:     app2,
			Alias:     "app2-key2",
			Hash:      "hash_app2_key2_1234567890abcdef1234567890abcdef1234567890abc",
			Enabled:   true,
			CreatedAt: 1000,
			UpdatedAt: 1000,
		},
	}

	// Insert all keys
	for _, k := range keys1 {
		UpsertKey(k)
	}
	for _, k := range keys2 {
		UpsertKey(k)
	}

	// Cleanup
	t.Cleanup(func() {
		for _, k := range keys1 {
			DeleteKey(k.KeyID)
		}
		for _, k := range keys2 {
			DeleteKey(k.KeyID)
		}
	})

	// Verify app1 has exactly 3 keys
	app1Keys := ListKeysByApp(app1)
	if len(app1Keys) != 3 {
		t.Errorf("ListKeysByApp(app1) returned %d keys, want 3", len(app1Keys))
	}

	// Verify app2 has exactly 2 keys
	app2Keys := ListKeysByApp(app2)
	if len(app2Keys) != 2 {
		t.Errorf("ListKeysByApp(app2) returned %d keys, want 2", len(app2Keys))
	}

	// Verify unknown app returns empty
	unknownKeys := ListKeysByApp(99999)
	if len(unknownKeys) != 0 {
		t.Errorf("ListKeysByApp(unknown) returned %d keys, want 0", len(unknownKeys))
	}
}

// ============================================================================
// TIER 2: Negative/Error Tests
// ============================================================================

// TestDeleteKey_RemovesFromAppIndex verifies that DeleteKey removes a key
// from both the hash and app indexes.
func TestDeleteKey_RemovesFromAppIndex(t *testing.T) {
	appID := uint32(10004)
	keyID := uint32(10106)
	hash := "hash_delete_test_1234567890abcdef1234567890abcdef1234567890abcdef"

	rec := APIKeyRecord{
		KeyID:     keyID,
		AppID:     appID,
		Alias:     "delete-test",
		Hash:      hash,
		Enabled:   true,
		CreatedAt: 1000,
		UpdatedAt: 1000,
	}

	UpsertKey(rec)

	// Verify key exists in all indexes
	if entry := LookupByHash(hash); entry == nil {
		t.Fatal("Key not found in hash index before delete")
	}
	if record := GetRecord(keyID); record == nil {
		t.Fatal("Key not found in ID index before delete")
	}

	appKeys := ListKeysByApp(appID)
	if len(appKeys) != 1 {
		t.Fatalf("Expected 1 key for app, got %d", len(appKeys))
	}

	// Delete the key
	DeleteKey(keyID)

	// Verify key is removed from hash index
	if entry := LookupByHash(hash); entry != nil {
		t.Error("Key still found in hash index after delete")
	}

	// Verify key is removed from ID index
	if record := GetRecord(keyID); record != nil {
		t.Error("Key still found in ID index after delete")
	}

	// Verify key is removed from app index
	appKeysAfter := ListKeysByApp(appID)
	if len(appKeysAfter) != 0 {
		t.Errorf("Expected 0 keys for app after delete, got %d", len(appKeysAfter))
	}
}

// TestLookupByHash_AfterRotation verifies that rotating a key (same KeyID,
// different hash) removes the old hash and adds the new one.
func TestLookupByHash_AfterRotation(t *testing.T) {
	keyID := uint32(10107)
	appID := uint32(10005)
	oldHash := "hash_rotation_old_1234567890abcdef1234567890abcdef1234567890abcdef"
	newHash := "hash_rotation_new_1234567890abcdef1234567890abcdef1234567890abcdef"

	// Insert with old hash
	oldRec := APIKeyRecord{
		KeyID:     keyID,
		AppID:     appID,
		Alias:     "rotation-key",
		Hash:      oldHash,
		Enabled:   true,
		CreatedAt: 1000,
		UpdatedAt: 1000,
	}
	UpsertKey(oldRec)

	// Verify old hash works
	if entry := LookupByHash(oldHash); entry == nil {
		t.Fatal("Old hash not found after initial insert")
	}

	// Rotate with new hash
	newRec := APIKeyRecord{
		KeyID:     keyID,
		AppID:     appID,
		Alias:     "rotation-key",
		Hash:      newHash,
		Enabled:   true,
		CreatedAt: 1000,
		UpdatedAt: 2000,
	}
	UpsertKey(newRec)

	t.Cleanup(func() { DeleteKey(keyID) })

	// Verify old hash is gone
	if entry := LookupByHash(oldHash); entry != nil {
		t.Error("Old hash still found after rotation")
	}

	// Verify new hash works
	if entry := LookupByHash(newHash); entry == nil {
		t.Fatal("New hash not found after rotation")
	}

	if entry := LookupByHash(newHash); entry.KeyID != keyID {
		t.Errorf("New hash points to wrong KeyID: got %d, want %d", entry.KeyID, keyID)
	}
}

// ============================================================================
// TIER 3: Non-Functional Tests
// ============================================================================

// TestUpsertKey_Concurrent verifies that concurrent upserting of keys to the
// same app maintains consistency (no panics, correct count, no races).
func TestUpsertKey_Concurrent(t *testing.T) {
	appID := uint32(10006)
	numGoroutines := 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(index int) {
			defer wg.Done()

			keyID := uint32(10200 + index)
			hash := fmt.Sprintf("hash_concurrent_%d_1234567890abcdef1234567890abcdef123456", index)

			rec := APIKeyRecord{
				KeyID:     keyID,
				AppID:     appID,
				Alias:     fmt.Sprintf("concurrent-key-%d", index),
				Hash:      hash,
				Enabled:   true,
				CreatedAt: 1000,
				UpdatedAt: 1000,
			}

			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("panic in goroutine %d: %v", index, r))
					mu.Unlock()
				}
			}()

			UpsertKey(rec)
		}(i)
	}

	wg.Wait()

	if len(errs) > 0 {
		for _, err := range errs {
			t.Error(err)
		}
		t.Fatal("Concurrent upserting panicked")
	}

	// Verify all 20 keys are present
	keys := ListKeysByApp(appID)
	if len(keys) != numGoroutines {
		t.Errorf("Expected %d keys, got %d", numGoroutines, len(keys))
	}

	// Cleanup
	for i := 0; i < numGoroutines; i++ {
		DeleteKey(uint32(10200 + i))
	}
}

// TestLookupByHash_PointerStability verifies that the pointer returned by
// LookupByHash points to stable data (immutability invariant). Upserting a
// different key must not affect the data of a previously retrieved pointer.
func TestLookupByHash_PointerStability(t *testing.T) {
	key1ID := uint32(10108)
	key2ID := uint32(10109)
	appID := uint32(10007)
	hash1 := "hash_stability_key1_1234567890abcdef1234567890abcdef1234567890abcdef"
	hash2 := "hash_stability_key2_1234567890abcdef1234567890abcdef1234567890abcdef"

	// Insert first key
	rec1 := APIKeyRecord{
		KeyID:     key1ID,
		AppID:     appID,
		Alias:     "stability-key1",
		Hash:      hash1,
		Enabled:   true,
		CreatedAt: 1000,
		UpdatedAt: 1000,
	}
	UpsertKey(rec1)

	// Get pointer to first key
	ptr1 := LookupByHash(hash1)
	if ptr1 == nil {
		t.Fatal("Failed to get pointer to first key")
	}

	// Capture the values from the first pointer
	origKeyID := ptr1.KeyID
	origAppID := ptr1.AppID
	origAlias := ptr1.Alias

	// Insert a different key
	rec2 := APIKeyRecord{
		KeyID:     key2ID,
		AppID:     appID,
		Alias:     "stability-key2",
		Hash:      hash2,
		Enabled:   true,
		CreatedAt: 2000,
		UpdatedAt: 2000,
	}
	UpsertKey(rec2)

	t.Cleanup(func() {
		DeleteKey(key1ID)
		DeleteKey(key2ID)
	})

	// Verify that ptr1 still has the original values (not modified)
	if ptr1.KeyID != origKeyID {
		t.Errorf("ptr1.KeyID changed: got %d, want %d", ptr1.KeyID, origKeyID)
	}

	if ptr1.AppID != origAppID {
		t.Errorf("ptr1.AppID changed: got %d, want %d", ptr1.AppID, origAppID)
	}

	if ptr1.Alias != origAlias {
		t.Errorf("ptr1.Alias changed: got %q, want %q", ptr1.Alias, origAlias)
	}
}
