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
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RegistryStoreBackend is a minimal flat key-value interface, pre-scoped to
// the registry storage domain by the caller (main.go or control plane).
// Implementations must be safe for concurrent use.
type RegistryStoreBackend interface {
	Put(ctx context.Context, key string, value []byte) error
	MultiPut(ctx context.Context, kvs map[string][]byte) error
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Delete(ctx context.Context, key string) error
	ListKeys(ctx context.Context, prefix string) ([]string, error)
}

// pgTxStore is the transaction interface subset needed for audit writes.
// Implemented by an adapter in main.go that wraps the postgresqlStore and
// applies tenant/domain scoping to keys before they reach the DB.
type pgTxStore interface {
	ExecTx(ctx context.Context, fn func(pgx.Tx) error) error
	// MultiPutTx upserts registry-level key-value pairs inside the given transaction.
	// The implementor is responsible for mapping registry keys to fully-scoped DB keys.
	MultiPutTx(ctx context.Context, tx pgx.Tx, kvs map[string][]byte) error
	// DeleteScopedKeyTx deletes a registry-level key inside the given transaction.
	// The implementor is responsible for mapping the registry key to the fully-scoped DB key.
	DeleteScopedKeyTx(ctx context.Context, tx pgx.Tx, registryKey string) error
}

const (
	tenantKeyPrefix      = "tenant:"
	aliasSuffix          = ":aliases"
	urlPropPrefix        = ":url:"
	idPropPrefix         = ":id:"
	metaPropPrefix       = ":meta:"
	rateLimitPrefix      = "rl:"
	tenantIDSuffix       = ":tid"                    // used as: "tenant:{alias}:tid"
	v2OverridePropPrefix = ":v2override:"            // key: tenant:{alias}:v2override:{configName}
	modifierSuffix       = ":modifier"               // key: tenant:{alias}:modifier
)

// tenantTIDKey returns the datastore key used to persist a tenant's stable TenantID.
func tenantTIDKey(alias string) string {
	return tenantKeyPrefix + alias + tenantIDSuffix
}

// TenantRegistryStore is the default RegistryDatastore backed by any
// RegistryStoreBackend (disk, Redis, DragonflyDB, etc.).
// Each property category is stored under its own key so backends can map
// these to native structures (e.g. Redis hash per category, S3 object per
// tenant, PostgreSQL JSONB column) by replacing this adapter with a
// backend-specific implementation of RegistryDatastore.
type TenantRegistryStore struct {
	backend RegistryStoreBackend
	audit   *AuditWriter
	pgStore pgTxStore // nil when audit disabled
}

// NewTenantRegistryStore wraps any RegistryStoreBackend as a RegistryDatastore.
func NewTenantRegistryStore(backend RegistryStoreBackend) *TenantRegistryStore {
	return &TenantRegistryStore{backend: backend}
}

// EnableAudit wires audit into this store. Call after construction.
// When enabled, every write method wraps its KV mutation and audit row in a
// single Postgres transaction. If audit is nil, all existing behaviour is
// unchanged — zero new code paths are executed.
func (s *TenantRegistryStore) EnableAudit(audit *AuditWriter, pg pgTxStore) {
	s.audit = audit
	s.pgStore = pg
}

// ── Tenant: stable TenantID ──────────────────────────────────────────────────

// PutTenantID persists the stable TenantID for a primary alias so that restarts
// and peer instances can recover the same numeric ID.
func (s *TenantRegistryStore) PutTenantID(ctx context.Context, primaryAlias string, id uint16) error {
	return s.backend.Put(ctx, tenantTIDKey(primaryAlias), []byte(fmt.Sprintf("%d", id)))
}

// GetTenantID retrieves the persisted TenantID for a primary alias.
// Returns (0, false, nil) when the key does not exist.
func (s *TenantRegistryStore) GetTenantID(ctx context.Context, primaryAlias string) (uint16, bool, error) {
	data, ok, err := s.backend.Get(ctx, tenantTIDKey(primaryAlias))
	if err != nil || !ok {
		return 0, false, err
	}
	var id uint64
	for _, b := range data {
		if b < '0' || b > '9' {
			return 0, false, nil
		}
		id = id*10 + uint64(b-'0')
	}
	if id == 0 || id > 0xFFFF {
		return 0, false, nil
	}
	return uint16(id), true, nil
}

// ── Tenant: alias management ─────────────────────────────────────────────────

func (s *TenantRegistryStore) PutTenantAliases(ctx context.Context, primaryAlias string, aliases []string) error {
	data, err := json.Marshal(aliases)
	if err != nil {
		return err
	}
	k := tenantKeyPrefix + primaryAlias + aliasSuffix
	if s.audit == nil {
		return s.backend.Put(ctx, k, data)
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.MultiPutTx(ctx, tx, map[string][]byte{k: data}); err != nil {
			return err
		}
		return s.audit.WritePut(ctx, tx, k, data)
	})
}

// ── Tenant: service URLs ─────────────────────────────────────────────────────

func (s *TenantRegistryStore) PutServiceURL(ctx context.Context, primaryAlias, key, value string) error {
	k := tenantKeyPrefix + primaryAlias + urlPropPrefix + key
	if s.audit == nil {
		return s.backend.Put(ctx, k, []byte(value))
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.MultiPutTx(ctx, tx, map[string][]byte{k: []byte(value)}); err != nil {
			return err
		}
		return s.audit.WritePut(ctx, tx, k, []byte(value))
	})
}

// ── Tenant: identifiers ──────────────────────────────────────────────────────

func (s *TenantRegistryStore) PutIdentifier(ctx context.Context, primaryAlias, key, value string) error {
	k := tenantKeyPrefix + primaryAlias + idPropPrefix + key
	if s.audit == nil {
		return s.backend.Put(ctx, k, []byte(value))
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.MultiPutTx(ctx, tx, map[string][]byte{k: []byte(value)}); err != nil {
			return err
		}
		return s.audit.WritePut(ctx, tx, k, []byte(value))
	})
}

// ── Tenant: metadata ─────────────────────────────────────────────────────────

func (s *TenantRegistryStore) PutMetadata(ctx context.Context, primaryAlias, key, value string) error {
	k := tenantKeyPrefix + primaryAlias + metaPropPrefix + key
	if s.audit == nil {
		return s.backend.Put(ctx, k, []byte(value))
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.MultiPutTx(ctx, tx, map[string][]byte{k: []byte(value)}); err != nil {
			return err
		}
		return s.audit.WritePut(ctx, tx, k, []byte(value))
	})
}

// ── Tenant: batch write ──────────────────────────────────────────────────────

// PutBatch writes all URL, identifier, and metadata properties for a tenant
// in a single round-trip. More efficient than individual PutServiceURL /
// PutIdentifier / PutMetadata calls when writing many properties at once.
func (s *TenantRegistryStore) PutBatch(ctx context.Context, primaryAlias string, urls, ids, meta map[string]string) error {
	total := len(urls) + len(ids) + len(meta)
	if total == 0 {
		return nil
	}
	kvs := make(map[string][]byte, total)
	for k, v := range urls {
		kvs[tenantKeyPrefix+primaryAlias+urlPropPrefix+k] = []byte(v)
	}
	for k, v := range ids {
		kvs[tenantKeyPrefix+primaryAlias+idPropPrefix+k] = []byte(v)
	}
	for k, v := range meta {
		kvs[tenantKeyPrefix+primaryAlias+metaPropPrefix+k] = []byte(v)
	}
	if s.audit == nil {
		return s.backend.MultiPut(ctx, kvs)
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.MultiPutTx(ctx, tx, kvs); err != nil {
			return err
		}
		for k, v := range kvs {
			if err := s.audit.WritePut(ctx, tx, k, v); err != nil {
				return err
			}
		}
		return nil
	})
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

	if s.audit == nil {
		for _, k := range keys {
			if err := s.backend.Delete(ctx, k); err != nil {
				return err
			}
		}
		return nil
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		for _, k := range keys {
			if err := s.pgStore.DeleteScopedKeyTx(ctx, tx, k); err != nil {
				return err
			}
			if err := s.audit.WriteDelete(ctx, tx, k); err != nil {
				return err
			}
		}
		return nil
	})
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
	k := rateLimitPrefix + name
	if s.audit == nil {
		return s.backend.Put(ctx, k, data)
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.MultiPutTx(ctx, tx, map[string][]byte{k: data}); err != nil {
			return err
		}
		return s.audit.WritePut(ctx, tx, k, data)
	})
}

func (s *TenantRegistryStore) DeleteRateLimitConfig(ctx context.Context, name string) error {
	k := rateLimitPrefix + name
	if s.audit == nil {
		return s.backend.Delete(ctx, k)
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.DeleteScopedKeyTx(ctx, tx, k); err != nil {
			return err
		}
		return s.audit.WriteDelete(ctx, tx, k)
	})
}

// ── Tenant: rate limit modifiers ─────────────────────────────────────────────

func (s *TenantRegistryStore) PutModifier(ctx context.Context, alias string, rec TenantModifierRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	k := tenantKeyPrefix + alias + modifierSuffix
	if s.audit == nil {
		return s.backend.Put(ctx, k, data)
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.MultiPutTx(ctx, tx, map[string][]byte{k: data}); err != nil {
			return err
		}
		return s.audit.WritePut(ctx, tx, k, data)
	})
}

// ── Tenant: V2 rate limit overrides ──────────────────────────────────────────

func (s *TenantRegistryStore) PutV2Override(ctx context.Context, alias, configName string, rec TenantV2OverrideRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	k := tenantKeyPrefix + alias + v2OverridePropPrefix + configName
	if s.audit == nil {
		return s.backend.Put(ctx, k, data)
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.MultiPutTx(ctx, tx, map[string][]byte{k: data}); err != nil {
			return err
		}
		return s.audit.WritePut(ctx, tx, k, data)
	})
}

func (s *TenantRegistryStore) DeleteV2Override(ctx context.Context, alias, configName string) error {
	k := tenantKeyPrefix + alias + v2OverridePropPrefix + configName
	if s.audit == nil {
		return s.backend.Delete(ctx, k)
	}
	return s.pgStore.ExecTx(ctx, func(tx pgx.Tx) error {
		if err := s.pgStore.DeleteScopedKeyTx(ctx, tx, k); err != nil {
			return err
		}
		return s.audit.WriteDelete(ctx, tx, k)
	})
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

	// ── V2 overrides ─────────────────────────────────────────────────────────
	overrideKeys, err := s.backend.ListKeys(ctx, tenantKeyPrefix)
	if err == nil {
		for _, k := range overrideKeys {
			rest := strings.TrimPrefix(k, tenantKeyPrefix)
			idx := strings.Index(rest, v2OverridePropPrefix)
			if idx < 0 {
				continue
			}
			alias := rest[:idx]
			configName := rest[idx+len(v2OverridePropPrefix):]
			if alias == "" || configName == "" {
				continue
			}
			data, ok, err := s.backend.Get(ctx, k)
			if err != nil || !ok {
				continue
			}
			var rec TenantV2OverrideRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				continue
			}
			rec.Alias = alias
			rec.ConfigName = configName
			snap.V2Overrides = append(snap.V2Overrides, rec)
		}
	}

	// ── Tenant modifiers ──────────────────────────────────────────────────────
	modKeys, err := s.backend.ListKeys(ctx, tenantKeyPrefix)
	if err == nil {
		for _, k := range modKeys {
			if !strings.HasSuffix(k, modifierSuffix) {
				continue
			}
			rest := strings.TrimPrefix(k, tenantKeyPrefix)
			alias := strings.TrimSuffix(rest, modifierSuffix)
			if alias == "" {
				continue
			}
			data, ok, err := s.backend.Get(ctx, k)
			if err != nil || !ok {
				continue
			}
			var rec TenantModifierRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				continue
			}
			rec.Alias = alias
			snap.Modifiers = append(snap.Modifiers, rec)
		}
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

	// Persisted TenantID — zero if not found (graceful degradation).
	if id, ok, _ := s.GetTenantID(ctx, primaryAlias); ok {
		rec.PersistedID = id
	}

	// Scan all property keys for this tenant.
	propPrefix := tenantKeyPrefix + primaryAlias + ":"
	propKeys, err := s.backend.ListKeys(ctx, propPrefix)
	if err != nil {
		return rec, nil // return partial record; don't fail the whole restore
	}

	for _, k := range propKeys {
		// ListKeys strips the full scoped prefix (e.g. "tenant:__global__:tenant_data:tenant:freeco:")
		// so k is a bare suffix like "meta:tier", "url:primary", "id:api_key".
		// Re-attach propPrefix before calling Get so the backend can reconstruct the
		// correct full scoped key (e.g. "tenant:freeco:meta:tier" → the backend
		// prepends the domain scope to get the actual DB key).
		fullKey := propPrefix + k
		rest := k // k IS the rest — no propPrefix to strip
		switch {
		case strings.HasPrefix(rest, "url:"):
			key := strings.TrimPrefix(rest, "url:")
			if val, ok, _ := s.backend.Get(ctx, fullKey); ok {
				if rec.ServiceURLs == nil {
					rec.ServiceURLs = make(map[string]string)
				}
				rec.ServiceURLs[key] = string(val)
			}
		case strings.HasPrefix(rest, "id:"):
			key := strings.TrimPrefix(rest, "id:")
			if val, ok, _ := s.backend.Get(ctx, fullKey); ok {
				if rec.Identifiers == nil {
					rec.Identifiers = make(map[string]string)
				}
				rec.Identifiers[key] = string(val)
			}
		case strings.HasPrefix(rest, "meta:"):
			key := strings.TrimPrefix(rest, "meta:")
			if val, ok, _ := s.backend.Get(ctx, fullKey); ok {
				if rec.Metadata == nil {
					rec.Metadata = make(map[string]string)
				}
				rec.Metadata[key] = string(val)
			}
		}
	}

	return rec, nil
}
