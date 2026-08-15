package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/datastore"
)

// SlotRegistry tracks which gateway instance owns which event listener slot.
// Uses a KeyValueStore (typically PostgreSQL) with optimistic locking: an instance
// claims a slot by inserting/updating a row; ownership expires if the heartbeat TTL elapses.
type SlotRegistry struct {
	store         SlotStore // keyvalue store interface for persistence
	instanceID    string    // unique per gateway pod (e.g. hostname + pid)
	ttl           time.Duration // claim expiry (default 30s)
	renewInterval time.Duration // heartbeat interval (default 10s)

	mu      sync.RWMutex
	ownedBy map[string]bool // track which slots are owned by this instance
}

// SlotStore is a minimal interface for persisting slot claims.
// This can be backed by a datastore.KeyValueStore or a test mock.
type SlotStore interface {
	// Get retrieves a value by key. Returns the value and a bool indicating if it exists.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Put stores a value with optional expiry (0 = no expiry).
	Put(ctx context.Context, key string, value []byte, expiryDuration time.Duration) error
	// Delete removes a key.
	Delete(ctx context.Context, key string) error
}

// DataStoreWrapper wraps a datastore.KeyValueStore to implement SlotStore.
// Uses a fixed tenant for all operations.
type DataStoreWrapper struct {
	store  datastore.KeyValueStore
	tenant datastore.Tenant
}

// NewDataStoreWrapper creates a wrapper around a datastore.KeyValueStore.
func NewDataStoreWrapper(store datastore.KeyValueStore) *DataStoreWrapper {
	return &DataStoreWrapper{
		store:  store,
		tenant: "__event_slots__", // Use a reserved tenant for slot registry
	}
}

// Get implements SlotStore.Get.
func (w *DataStoreWrapper) Get(ctx context.Context, key string) ([]byte, bool, error) {
	return w.store.Get(ctx, w.tenant, key)
}

// Put implements SlotStore.Put using the ExpiringStore interface if available.
func (w *DataStoreWrapper) Put(ctx context.Context, key string, value []byte, expiryDuration time.Duration) error {
	// Try to use ExpiringStore if available
	if expStore, ok := w.store.(datastore.ExpiringStore); ok && expiryDuration > 0 {
		return expStore.PutWithTTL(ctx, w.tenant, key, value, expiryDuration)
	}
	// Fall back to regular Put (expiry not supported, but still works)
	return w.store.Put(ctx, w.tenant, key, value)
}

// Delete implements SlotStore.Delete.
func (w *DataStoreWrapper) Delete(ctx context.Context, key string) error {
	return w.store.Delete(ctx, w.tenant, key)
}

// SlotClaim represents the ownership record for a listener slot.
type SlotClaim struct {
	ListenerName string    `json:"listener_name"`
	InstanceID   string    `json:"instance_id"`
	ClaimedAt    time.Time `json:"claimed_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// NewSlotRegistry creates a SlotRegistry.
// store: any SlotStore implementation (e.g. wrapped datastore.KeyValueStore)
// instanceID: unique identifier for this gateway instance
func NewSlotRegistry(store SlotStore, instanceID string) *SlotRegistry {
	return &SlotRegistry{
		store:         store,
		instanceID:    instanceID,
		ttl:           30 * time.Second,
		renewInterval: 10 * time.Second,
		ownedBy:       make(map[string]bool),
	}
}

// SetTTL sets the claim TTL (default 30s).
func (r *SlotRegistry) SetTTL(ttl time.Duration) {
	r.ttl = ttl
}

// SetRenewInterval sets the heartbeat interval (default 10s).
func (r *SlotRegistry) SetRenewInterval(interval time.Duration) {
	r.renewInterval = interval
}

// TryClaim attempts to claim listenerName for this instance.
// Returns true if the claim succeeded (either new, already owned by us, or previous owner expired).
// Returns false if another instance currently holds the claim.
func (r *SlotRegistry) TryClaim(ctx context.Context, listenerName string) (bool, error) {
	key := fmt.Sprintf("slot:%s", listenerName)

	// Check if a claim already exists
	data, exists, err := r.store.Get(ctx, key)
	if err != nil {
		return false, fmt.Errorf("get slot %q: %w", listenerName, err)
	}

	now := time.Now()
	newClaim := SlotClaim{
		ListenerName: listenerName,
		InstanceID:   r.instanceID,
		ClaimedAt:    now,
		ExpiresAt:    now.Add(r.ttl),
	}

	if !exists {
		// Slot is unclaimed; claim it
		if err := r.persistClaim(ctx, key, newClaim); err != nil {
			return false, err
		}
		r.mu.Lock()
		r.ownedBy[listenerName] = true
		r.mu.Unlock()
		return true, nil
	}

	// Slot has a claim; check if we own it or if it's expired
	var existing SlotClaim
	if err := json.Unmarshal(data, &existing); err != nil {
		// Corrupt claim; take over
		if err := r.persistClaim(ctx, key, newClaim); err != nil {
			return false, err
		}
		r.mu.Lock()
		r.ownedBy[listenerName] = true
		r.mu.Unlock()
		return true, nil
	}

	if existing.InstanceID == r.instanceID {
		// We already own this slot; refresh the expiry
		if err := r.persistClaim(ctx, key, newClaim); err != nil {
			return false, err
		}
		r.mu.Lock()
		r.ownedBy[listenerName] = true
		r.mu.Unlock()
		return true, nil
	}

	if now.After(existing.ExpiresAt) {
		// Previous owner's claim expired; take over
		if err := r.persistClaim(ctx, key, newClaim); err != nil {
			return false, err
		}
		r.mu.Lock()
		r.ownedBy[listenerName] = true
		r.mu.Unlock()
		return true, nil
	}

	// Another instance owns this slot and it hasn't expired
	r.mu.Lock()
	delete(r.ownedBy, listenerName)
	r.mu.Unlock()
	return false, nil
}

// Release voluntarily releases a listener claim.
func (r *SlotRegistry) Release(ctx context.Context, listenerName string) error {
	key := fmt.Sprintf("slot:%s", listenerName)
	if err := r.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("release slot %q: %w", listenerName, err)
	}
	r.mu.Lock()
	delete(r.ownedBy, listenerName)
	r.mu.Unlock()
	return nil
}

// RenewAll renews TTL for all slots currently owned by this instance.
func (r *SlotRegistry) RenewAll(ctx context.Context) error {
	r.mu.RLock()
	owned := make([]string, 0, len(r.ownedBy))
	for name := range r.ownedBy {
		owned = append(owned, name)
	}
	r.mu.RUnlock()

	for _, name := range owned {
		key := fmt.Sprintf("slot:%s", name)
		data, exists, err := r.store.Get(ctx, key)
		if err != nil {
			log.Printf("[SlotRegistry] RenewAll: get %q failed: %v", name, err)
			continue
		}
		if !exists {
			// Slot was deleted elsewhere; update our tracking
			r.mu.Lock()
			delete(r.ownedBy, name)
			r.mu.Unlock()
			continue
		}

		var claim SlotClaim
		if err := json.Unmarshal(data, &claim); err != nil {
			log.Printf("[SlotRegistry] RenewAll: unmarshal %q failed: %v", name, err)
			continue
		}

		// Only renew if we still own it
		if claim.InstanceID != r.instanceID {
			r.mu.Lock()
			delete(r.ownedBy, name)
			r.mu.Unlock()
			continue
		}

		claim.ClaimedAt = time.Now()
		claim.ExpiresAt = time.Now().Add(r.ttl)
		if err := r.persistClaim(ctx, key, claim); err != nil {
			log.Printf("[SlotRegistry] RenewAll: persist %q failed: %v", name, err)
		}
	}

	return nil
}

// StartHeartbeat starts a background goroutine that calls RenewAll every renewInterval.
// Stops when ctx is cancelled.
func (r *SlotRegistry) StartHeartbeat(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(r.renewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.RenewAll(ctx); err != nil {
					log.Printf("[SlotRegistry] RenewAll error: %v", err)
				}
			}
		}
	}()
}

// persistClaim writes a claim to the store with TTL.
func (r *SlotRegistry) persistClaim(ctx context.Context, key string, claim SlotClaim) error {
	data, err := json.Marshal(claim)
	if err != nil {
		return fmt.Errorf("marshal claim: %w", err)
	}
	return r.store.Put(ctx, key, data, r.ttl)
}
