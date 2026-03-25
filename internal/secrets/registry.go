package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const globalTenant = "__global__"

// CredentialEntry is a named credential mapping persisted in the CredentialStore.
type CredentialEntry struct {
	// Ref is the secret reference resolved via the secrets manager
	// (e.g. "gsm://projects/p/secrets/s/versions/latest", "env:MY_KEY").
	Ref         string    `json:"ref"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CredentialStore is the storage backend for CredentialRegistry.
// Implementations pre-bind the DataDomain so the registry only passes
// tenant + name per call.
type CredentialStore interface {
	Put(ctx context.Context, tenant, name string, value []byte) error
	Get(ctx context.Context, tenant, name string) ([]byte, bool, error)
	Delete(ctx context.Context, tenant, name string) error
	List(ctx context.Context, tenant string) ([]string, error)
}

// CredentialRegistry maps logical names to secret references with optional
// per-tenant overrides.
//
// Resolution order: tenant-specific → global.
// A flow uses "load_credential" referencing a name; ops registers the mapping
// once via the /credentials REST endpoint — flow code never touches raw refs.
//
// Safe for concurrent use (thread-safety comes from the CredentialStore backend).
type CredentialRegistry struct {
	store   CredentialStore
	secrets Resolver
}

// NewCredentialRegistry creates a CredentialRegistry backed by store.
// resolver is used to fetch the actual secret value when Resolve is called.
func NewCredentialRegistry(store CredentialStore, resolver Resolver) *CredentialRegistry {
	return &CredentialRegistry{store: store, secrets: resolver}
}

// Set stores or updates a named credential.
// Use tenant="" to store a global credential available to all tenants.
func (r *CredentialRegistry) Set(ctx context.Context, name, tenant string, entry CredentialEntry) error {
	key := storageTenant(tenant)
	now := time.Now().UTC()
	entry.UpdatedAt = now
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("credentials: marshal %q: %w", name, err)
	}
	return r.store.Put(ctx, key, name, data)
}

// GetEntry returns the raw CredentialEntry for a named credential.
// Use tenant="" for global credentials.
func (r *CredentialRegistry) GetEntry(ctx context.Context, name, tenant string) (CredentialEntry, bool, error) {
	data, ok, err := r.store.Get(ctx, storageTenant(tenant), name)
	if err != nil || !ok {
		return CredentialEntry{}, ok, err
	}
	var entry CredentialEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return CredentialEntry{}, false, fmt.Errorf("credentials: unmarshal %q: %w", name, err)
	}
	return entry, true, nil
}

// Delete removes a named credential.
// Use tenant="" for global credentials.
func (r *CredentialRegistry) Delete(ctx context.Context, name, tenant string) error {
	return r.store.Delete(ctx, storageTenant(tenant), name)
}

// List returns all credential names for the given tenant scope.
// Use tenant="" to list global credentials.
func (r *CredentialRegistry) List(ctx context.Context, tenant string) ([]string, error) {
	return r.store.List(ctx, storageTenant(tenant))
}

// Resolve returns the plaintext secret for a named credential.
// Resolution order: tenant-specific → global.
// Use tenant="" to resolve global credentials only.
func (r *CredentialRegistry) Resolve(ctx context.Context, name, tenant string) ([]byte, error) {
	// 1. Tenant-specific override.
	if tenant != "" && tenant != globalTenant {
		entry, ok, err := r.GetEntry(ctx, name, tenant)
		if err != nil {
			return nil, fmt.Errorf("credentials: lookup %q tenant=%q: %w", name, tenant, err)
		}
		if ok {
			return r.secrets.Resolve(ctx, entry.Ref)
		}
	}

	// 2. Global fallback.
	entry, ok, err := r.GetEntry(ctx, name, "")
	if err != nil {
		return nil, fmt.Errorf("credentials: lookup %q (global): %w", name, err)
	}
	if !ok {
		return nil, fmt.Errorf("credentials: %q not found (tenant=%q, global)", name, tenant)
	}
	return r.secrets.Resolve(ctx, entry.Ref)
}

func storageTenant(tenant string) string {
	if tenant == "" {
		return globalTenant
	}
	return tenant
}
