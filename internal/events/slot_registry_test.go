package events

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// mockStore is an in-memory implementation of SlotStore for testing.
type mockStore struct {
	mu    sync.RWMutex
	data  map[string][]byte
	expiry map[string]time.Time
}

func newMockStore() *mockStore {
	return &mockStore{
		data: make(map[string][]byte),
		expiry: make(map[string]time.Time),
	}
}

func (m *mockStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check if expired
	if exp, exists := m.expiry[key]; exists && time.Now().After(exp) {
		return nil, false, nil
	}

	data, ok := m.data[key]
	return data, ok, nil
}

func (m *mockStore) Put(ctx context.Context, key string, value []byte, expiryDuration time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data[key] = value
	if expiryDuration > 0 {
		m.expiry[key] = time.Now().Add(expiryDuration)
	}
	return nil
}

func (m *mockStore) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.data, key)
	delete(m.expiry, key)
	return nil
}

func TestTryClaimNew(t *testing.T) {
	store := newMockStore()
	registry := NewSlotRegistry(store, "instance1")
	ctx := context.Background()

	// First claim should succeed
	claimed, err := registry.TryClaim(ctx, "listener1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !claimed {
		t.Fatalf("expected claim to succeed")
	}

	// Verify the claim is stored
	data, exists, err := store.Get(ctx, "slot:listener1")
	if err != nil || !exists {
		t.Fatalf("claim not found in store")
	}

	var claim SlotClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		t.Fatalf("failed to unmarshal claim: %v", err)
	}
	if claim.InstanceID != "instance1" || claim.ListenerName != "listener1" {
		t.Fatalf("claim mismatch: got %+v", claim)
	}
}

func TestTryClaimAlreadyOwned(t *testing.T) {
	store := newMockStore()
	registry := NewSlotRegistry(store, "instance1")
	ctx := context.Background()

	// First claim
	claimed, err := registry.TryClaim(ctx, "listener1")
	if err != nil || !claimed {
		t.Fatalf("first claim failed")
	}

	// Second claim by same instance should succeed
	claimed, err = registry.TryClaim(ctx, "listener1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !claimed {
		t.Fatalf("expected second claim to succeed")
	}
}

func TestTryClaimContestedFails(t *testing.T) {
	store := newMockStore()
	registry1 := NewSlotRegistry(store, "instance1")
	registry2 := NewSlotRegistry(store, "instance2")
	ctx := context.Background()

	// Instance 1 claims
	claimed, err := registry1.TryClaim(ctx, "listener1")
	if err != nil || !claimed {
		t.Fatalf("instance1 claim failed")
	}

	// Instance 2 tries to claim (should fail)
	claimed, err = registry2.TryClaim(ctx, "listener1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimed {
		t.Fatalf("expected claim to fail for instance2")
	}
}

func TestTryClaimExpiredTakeover(t *testing.T) {
	store := newMockStore()
	registry1 := NewSlotRegistry(store, "instance1")
	registry1.SetTTL(100 * time.Millisecond)
	registry2 := NewSlotRegistry(store, "instance2")
	registry2.SetTTL(100 * time.Millisecond)

	ctx := context.Background()

	// Instance 1 claims
	claimed, err := registry1.TryClaim(ctx, "listener1")
	if err != nil || !claimed {
		t.Fatalf("instance1 claim failed")
	}

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	// Instance 2 tries to claim (should succeed because instance1's claim expired)
	claimed, err = registry2.TryClaim(ctx, "listener1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !claimed {
		t.Fatalf("expected claim to succeed for instance2 after expiry")
	}

	// Verify instance2 now owns it
	data, exists, err := store.Get(ctx, "slot:listener1")
	if err != nil || !exists {
		t.Fatalf("claim not found")
	}

	var claim SlotClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if claim.InstanceID != "instance2" {
		t.Fatalf("expected instance2 to own the slot, got %s", claim.InstanceID)
	}
}

func TestRelease(t *testing.T) {
	store := newMockStore()
	registry := NewSlotRegistry(store, "instance1")
	ctx := context.Background()

	// Claim
	claimed, err := registry.TryClaim(ctx, "listener1")
	if err != nil || !claimed {
		t.Fatalf("claim failed")
	}

	// Release
	if err := registry.Release(ctx, "listener1"); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// Verify it's gone
	_, exists, err := store.Get(ctx, "slot:listener1")
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}
	if exists {
		t.Fatalf("slot should not exist after release")
	}
}

func TestRenewAll(t *testing.T) {
	store := newMockStore()
	registry := NewSlotRegistry(store, "instance1")
	registry.SetTTL(1 * time.Second)
	ctx := context.Background()

	// Claim a listener
	claimed, err := registry.TryClaim(ctx, "listener1")
	if err != nil || !claimed {
		t.Fatalf("claim failed")
	}

	// Get the original expiry
	data1, _, _ := store.Get(ctx, "slot:listener1")
	var claim1 SlotClaim
	if err := json.Unmarshal(data1, &claim1); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	originalExpiry := claim1.ExpiresAt

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Renew
	if err := registry.RenewAll(ctx); err != nil {
		t.Fatalf("RenewAll failed: %v", err)
	}

	// Get the new expiry
	data2, _, _ := store.Get(ctx, "slot:listener1")
	var claim2 SlotClaim
	if err := json.Unmarshal(data2, &claim2); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	newExpiry := claim2.ExpiresAt

	// New expiry should be later than original
	if !newExpiry.After(originalExpiry) {
		t.Fatalf("expiry not renewed: original=%v, new=%v", originalExpiry, newExpiry)
	}
}

func TestStartHeartbeat(t *testing.T) {
	store := newMockStore()
	registry := NewSlotRegistry(store, "instance1")
	registry.SetTTL(500 * time.Millisecond)
	registry.SetRenewInterval(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Claim a listener
	claimed, err := registry.TryClaim(ctx, "listener1")
	if err != nil || !claimed {
		t.Fatalf("claim failed")
	}

	// Start heartbeat
	registry.StartHeartbeat(ctx)

	// Wait for at least one renewal
	time.Sleep(150 * time.Millisecond)

	// Get expiry after heartbeat
	data, _, _ := store.Get(ctx, "slot:listener1")
	var claim SlotClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	expiryAfter := claim.ExpiresAt

	// The claim should still exist and be valid
	if time.Now().After(expiryAfter) {
		t.Fatalf("claim expired before heartbeat could renew it")
	}
}

func TestMultipleSlots(t *testing.T) {
	store := newMockStore()
	registry := NewSlotRegistry(store, "instance1")
	ctx := context.Background()

	// Claim multiple listeners
	for i := 1; i <= 3; i++ {
		name := "listener" + string(rune(48+i))
		claimed, err := registry.TryClaim(ctx, name)
		if err != nil || !claimed {
			t.Fatalf("claim for %s failed", name)
		}
	}

	// RenewAll should renew all of them
	if err := registry.RenewAll(ctx); err != nil {
		t.Fatalf("RenewAll failed: %v", err)
	}

	// Verify all still exist
	for i := 1; i <= 3; i++ {
		name := "listener" + string(rune(48+i))
		_, exists, _ := store.Get(ctx, "slot:"+name)
		if !exists {
			t.Fatalf("slot %s should exist after RenewAll", name)
		}
	}
}
