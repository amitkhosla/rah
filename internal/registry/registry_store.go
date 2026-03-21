package registry

// registry_store.go — default RegistryDatastore implementation backed by any
// key-value store. Serialisation (JSON) lives here so backends store opaque
// bytes and never need to understand the registry data model.
//
// Key layout (all stored under the registry domain, global tenant scope):
//
//	tenant:{primaryAlias}:aliases    → JSON []string
//	tenant:{primaryAlias}:url:{key}  → raw value bytes
//	tenant:{primaryAlias}:id:{key}   → raw value bytes
//	tenant:{primaryAlias}:meta:{key} → raw value bytes
//	rl:{name}                        → JSON { per_sec, per_min, burst_factor }
//
// A Redis backend could implement RegistryDatastore directly using HSET/HGETALL
// instead of this adapter. A PostgreSQL backend could use a tenants table.
// The interface, not this file, is the contract.

import (
	"context"
	"encoding/json"
	"strings"
)

// RegistryStoreBackend is a minimal flat key-value interface, pre-scoped to
// the registry storage domain by the caller (main.go or control plane).
// Implementations must be safe for concurrent use.
type RegistryStoreBackend interface {
	Put(ctx context.Context, key string, value []byte) error
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Delete(ctx context.Context, key string) error
	ListKeys(ctx context.Context, prefix string) ([]string, error)
}

const (
	tenantKeyPrefix  = "tenant:"
	aliasSuffix      = ":aliases"
	urlPropPrefix    = ":url:"
	idPropPrefix     = ":id:"
	metaPropPrefix   = ":meta:"
	rateLimitPrefix  = "rl:"
)

// TenantRegistryStore is the default RegistryDatastore backed by any
// RegistryStoreBackend (disk, Redis, DragonflyDB, etc.).
// Each property category is stored under its own key so backends can map
// these to native structures (e.g. Redis hash per category, S3 object per
// tenant, PostgreSQL JSONB column) by replacing this adapter with a
// backend-specific implementation of RegistryDatastore.
type TenantRegistryStore struct {
	backend RegistryStoreBackend
}

// NewTenantRegistryStore wraps any RegistryStoreBackend as a RegistryDatastore.
func NewTenantRegistryStore(backend RegistryStoreBackend) *TenantRegistryStore {
	return &TenantRegistryStore{backend: backend}
}

// ── Tenant: alias management ─────────────────────────────────────────────────

func (s *TenantRegistryStore) PutTenantAliases(ctx context.Context, primaryAlias string, aliases []string) error {
	data, err := json.Marshal(aliases)
	if err != nil {
		return err
	}
	return s.backend.Put(ctx, tenantKeyPrefix+primaryAlias+aliasSuffix, data)
}

// ── Tenant: service URLs ─────────────────────────────────────────────────────

func (s *TenantRegistryStore) PutServiceURL(ctx context.Context, primaryAlias, key, value string) error {
	return s.backend.Put(ctx, tenantKeyPrefix+primaryAlias+urlPropPrefix+key, []byte(value))
}

// ── Tenant: identifiers ──────────────────────────────────────────────────────

func (s *TenantRegistryStore) PutIdentifier(ctx context.Context, primaryAlias, key, value string) error {
	return s.backend.Put(ctx, tenantKeyPrefix+primaryAlias+idPropPrefix+key, []byte(value))
}

// ── Tenant: metadata ─────────────────────────────────────────────────────────

func (s *TenantRegistryStore) PutMetadata(ctx context.Context, primaryAlias, key, value string) error {
	return s.backend.Put(ctx, tenantKeyPrefix+primaryAlias+metaPropPrefix+key, []byte(value))
}

// ── Tenant: lifecycle ────────────────────────────────────────────────────────

// DeleteTenant removes all keys for the given primary alias: aliases, all URL,
// identifier, and metadata entries stored under that alias prefix.
func (s *TenantRegistryStore) DeleteTenant(ctx context.Context, primaryAlias string) error {
	prefix := tenantKeyPrefix + primaryAlias + ":"
	keys, err := s.backend.ListKeys(ctx, prefix)
	if err != nil {
		return err
	}
	// Also delete the aliases key which uses the same prefix pattern.
	keys = append(keys, tenantKeyPrefix+primaryAlias+aliasSuffix)
	for _, k := range keys {
		if err := s.backend.Delete(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

// ── Rate limit configurations ────────────────────────────────────────────────

type storedRateLimitConfig struct {
	PerSec      uint32 `json:"per_sec"`
	PerMin      uint32 `json:"per_min"`
	BurstFactor uint16 `json:"burst_factor"`
}

func (s *TenantRegistryStore) PutRateLimitConfig(ctx context.Context, name string, cfg RateLimitConfig) error {
	stored := storedRateLimitConfig{
		PerSec:      cfg.PerSec,
		PerMin:      cfg.PerMin,
		BurstFactor: cfg.BurstFactor,
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return s.backend.Put(ctx, rateLimitPrefix+name, data)
}

func (s *TenantRegistryStore) DeleteRateLimitConfig(ctx context.Context, name string) error {
	return s.backend.Delete(ctx, rateLimitPrefix+name)
}

// ── Startup restore ──────────────────────────────────────────────────────────

// LoadAll reconstructs a RegistrySnapshot by scanning all stored keys.
// Tenant records are assembled from their individual property keys.
// Called once at startup — not performance-critical.
func (s *TenantRegistryStore) LoadAll(ctx context.Context) (RegistrySnapshot, error) {
	var snap RegistrySnapshot

	// ── Rate limit configs ────────────────────────────────────────────────────
	rlKeys, err := s.backend.ListKeys(ctx, rateLimitPrefix)
	if err != nil {
		return snap, err
	}
	for _, k := range rlKeys {
		if !strings.HasPrefix(k, rateLimitPrefix) {
			continue
		}
		data, ok, err := s.backend.Get(ctx, k)
		if err != nil || !ok {
			continue
		}
		var stored storedRateLimitConfig
		if err := json.Unmarshal(data, &stored); err != nil {
			continue
		}
		snap.RateLimits = append(snap.RateLimits, RateLimitRecord{
			Name: strings.TrimPrefix(k, rateLimitPrefix),
			Config: RateLimitConfig{
				PerSec:      stored.PerSec,
				PerMin:      stored.PerMin,
				BurstFactor: stored.BurstFactor,
			},
		})
	}

	// ── Tenants ───────────────────────────────────────────────────────────────
	// Discover all primary aliases by listing alias keys.
	aliasKeys, err := s.backend.ListKeys(ctx, tenantKeyPrefix)
	if err != nil {
		return snap, err
	}

	seen := make(map[string]bool)
	for _, k := range aliasKeys {
		if !strings.HasSuffix(k, aliasSuffix) {
			continue
		}
		// Extract primaryAlias from "tenant:{primaryAlias}:aliases"
		withoutPrefix := strings.TrimPrefix(k, tenantKeyPrefix)
		primaryAlias := strings.TrimSuffix(withoutPrefix, aliasSuffix)
		if seen[primaryAlias] {
			continue
		}
		seen[primaryAlias] = true

		rec, err := s.loadTenantRecord(ctx, primaryAlias)
		if err != nil {
			continue
		}
		snap.Tenants = append(snap.Tenants, rec)
	}

	return snap, nil
}

// loadTenantRecord assembles a TenantRecord for the given primary alias by
// reading all its individual property keys from the backend.
func (s *TenantRegistryStore) loadTenantRecord(ctx context.Context, primaryAlias string) (TenantRecord, error) {
	rec := TenantRecord{}

	// Aliases
	if data, ok, err := s.backend.Get(ctx, tenantKeyPrefix+primaryAlias+aliasSuffix); err == nil && ok {
		_ = json.Unmarshal(data, &rec.Aliases)
	}
	if len(rec.Aliases) == 0 {
		rec.Aliases = []string{primaryAlias}
	}

	// Scan all property keys for this tenant.
	propPrefix := tenantKeyPrefix + primaryAlias + ":"
	propKeys, err := s.backend.ListKeys(ctx, propPrefix)
	if err != nil {
		return rec, nil // return partial record; don't fail the whole restore
	}

	for _, k := range propKeys {
		rest := strings.TrimPrefix(k, propPrefix)
		switch {
		case strings.HasPrefix(rest, "url:"):
			key := strings.TrimPrefix(rest, "url:")
			if val, ok, _ := s.backend.Get(ctx, k); ok {
				if rec.ServiceURLs == nil {
					rec.ServiceURLs = make(map[string]string)
				}
				rec.ServiceURLs[key] = string(val)
			}
		case strings.HasPrefix(rest, "id:"):
			key := strings.TrimPrefix(rest, "id:")
			if val, ok, _ := s.backend.Get(ctx, k); ok {
				if rec.Identifiers == nil {
					rec.Identifiers = make(map[string]string)
				}
				rec.Identifiers[key] = string(val)
			}
		case strings.HasPrefix(rest, "meta:"):
			key := strings.TrimPrefix(rest, "meta:")
			if val, ok, _ := s.backend.Get(ctx, k); ok {
				if rec.Metadata == nil {
					rec.Metadata = make(map[string]string)
				}
				rec.Metadata[key] = string(val)
			}
		}
	}

	return rec, nil
}
