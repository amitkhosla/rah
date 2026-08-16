package control

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/apikey"
	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/datastore"
	"github.com/amitkhosla/rah/internal/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

const (
	SystemTenant datastore.Tenant = "__system__"
	GlobalTenant datastore.Tenant = "__global__"
)

var immutableStartupDomains = map[config.DataDomain]struct{}{
	config.DomainAPIDefinitions: {},
	config.DomainFlows:          {},
}

// StoreWrapFn is an optional transform applied to every store after creation.
// Typically used to wrap stores with EventingStore for ingest event emission.
type StoreWrapFn func(domain config.DataDomain, store datastore.KeyValueStore) datastore.KeyValueStore

// DataStoreManager keeps the active data-store topology selected per customer.
type DataStoreManager struct {
	mu            sync.RWMutex
	active        config.DataStoreConfig
	registryStore map[config.DataDomain]datastore.KeyValueStore
	resolver      secrets.Resolver // nil when no secrets manager is configured
	wrapFn        StoreWrapFn      // optional; applied to each store at creation and on SetStoreWrapper
}

// NewDataStoreManager builds a DataStoreManager from the given config.
// resolver is optional (pass nil to skip credential resolution — suitable for
// tests and deployments where credentials are already in the config as literals).
func NewDataStoreManager(ctx context.Context, initial config.DataStoreConfig, resolver secrets.Resolver) (*DataStoreManager, error) {
	if err := initial.Validate(); err != nil {
		return nil, err
	}

	stores, err := buildDomainStores(ctx, initial, resolver, nil)
	if err != nil {
		return nil, err
	}

	return &DataStoreManager{active: initial, registryStore: stores, resolver: resolver}, nil
}

func validateImmutableStartupDomains(oldCfg, newCfg config.DataStoreConfig) error {
	for domain := range immutableStartupDomains {
		oldStore, oldExists := oldCfg.Bindings[domain]
		newStore, newExists := newCfg.Bindings[domain]
		if oldExists != newExists || oldStore != newStore {
			return fmt.Errorf("domain %q is startup-managed and cannot be changed via /config/datastores", domain)
		}
	}
	return nil
}

func (m *DataStoreManager) Update(ctx context.Context, cfg config.DataStoreConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	m.mu.RLock()
	oldCfg := m.active
	m.mu.RUnlock()
	if err := validateImmutableStartupDomains(oldCfg, cfg); err != nil {
		return err
	}

	stores, err := buildDomainStores(ctx, cfg, m.resolver, m.wrapFn)
	if err != nil {
		return err
	}

	m.mu.Lock()
	oldStores := m.registryStore
	m.active = cfg
	m.registryStore = stores
	m.mu.Unlock()

	for _, store := range oldStores {
		_ = store.Close()
	}

	return nil
}

func (m *DataStoreManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, store := range m.registryStore {
		_ = store.Close()
	}
}

func buildDomainStores(ctx context.Context, cfg config.DataStoreConfig, resolver secrets.Resolver, wrapFn StoreWrapFn) (map[config.DataDomain]datastore.KeyValueStore, error) {
	stores := make(map[config.DataDomain]datastore.KeyValueStore, len(cfg.Bindings))
	for domain := range cfg.Bindings {
		storeCfg, err := cfg.ResolveStore(domain)
		if err != nil {
			return nil, err
		}
		if resolver != nil {
			if storeCfg, err = resolveStoreCredentials(ctx, storeCfg, resolver); err != nil {
				return nil, fmt.Errorf("domain %q: %w", domain, err)
			}
		}
		store, err := datastore.NewStore(ctx, storeCfg, domain)
		if err != nil {
			return nil, err
		}
		if storeCfg.Encryption.ShouldEncryptDomain(domain) {
			encStore, encErr := buildEncryptingStore(ctx, store, storeCfg.Encryption, string(domain), resolver)
			if encErr != nil {
				return nil, fmt.Errorf("domain %q: %w", domain, encErr)
			}
			store = encStore
		}
		if wrapFn != nil {
			store = wrapFn(domain, store)
		}
		stores[domain] = store
	}
	return stores, nil
}

// SetStoreWrapper installs a wrapper applied to every store on creation.
// It also retroactively wraps all currently-active stores.
// Safe to call after NewDataStoreManager (e.g. once the ingest pipeline is ready).
func (m *DataStoreManager) SetStoreWrapper(fn StoreWrapFn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wrapFn = fn
	for domain, store := range m.registryStore {
		m.registryStore[domain] = fn(domain, store)
	}
}

// buildEncryptingStore wraps inner with AES-256-GCM encryption based on EncryptionConfig.
// Supports single-key (enc.KeyRef), multi-key rotation (enc.Keys), and per-tenant HKDF
// (enc.HKDF) modes. HKDF mode can be combined with either key mode.
func buildEncryptingStore(ctx context.Context, inner datastore.KeyValueStore, enc config.EncryptionConfig, domain string, resolver secrets.Resolver) (datastore.KeyValueStore, error) {
	primaryVersion := enc.PrimaryVersion
	if primaryVersion == 0 {
		primaryVersion = 1
	}

	if len(enc.Keys) > 0 {
		// Multi-key rotation mode.
		if enc.HKDF {
			// HKDF + multi-key: pass raw master keys to NewEncryptingStoreHKDF.
			masterKeys := make(map[byte][]byte, len(enc.Keys))
			for _, vkr := range enc.Keys {
				if vkr.Version == 0 {
					return nil, fmt.Errorf("encrypting store: version 0 is reserved; use version 1 or higher")
				}
				key, err := resolveEncryptionKey(ctx, vkr.KeyRef, resolver)
				if err != nil {
					return nil, fmt.Errorf("encrypting store: version %d: %w", vkr.Version, err)
				}
				masterKeys[vkr.Version] = key
			}
			return datastore.NewEncryptingStoreHKDF(inner, masterKeys, primaryVersion, domain)
		}

		// Non-HKDF multi-key: pre-build AEADs.
		aeads := make(map[byte]cipher.AEAD, len(enc.Keys))
		for _, vkr := range enc.Keys {
			if vkr.Version == 0 {
				return nil, fmt.Errorf("encrypting store: version 0 is reserved; use version 1 or higher")
			}
			key, err := resolveEncryptionKey(ctx, vkr.KeyRef, resolver)
			if err != nil {
				return nil, fmt.Errorf("encrypting store: version %d: %w", vkr.Version, err)
			}
			block, err := aes.NewCipher(key)
			if err != nil {
				return nil, fmt.Errorf("encrypting store: version %d: create cipher: %w", vkr.Version, err)
			}
			aead, err := cipher.NewGCM(block)
			if err != nil {
				return nil, fmt.Errorf("encrypting store: version %d: create GCM: %w", vkr.Version, err)
			}
			aeads[vkr.Version] = aead
		}
		return datastore.NewEncryptingStoreMulti(inner, aeads, primaryVersion, domain)
	}

	// Single-key mode.
	encKey, err := resolveEncryptionKey(ctx, enc.KeyRef, resolver)
	if err != nil {
		return nil, fmt.Errorf("encrypting store: %w", err)
	}
	if enc.HKDF {
		// HKDF + single-key: use version 1 master key.
		return datastore.NewEncryptingStoreHKDF(inner, map[byte][]byte{1: encKey}, 1, domain)
	}
	return datastore.NewEncryptingStore(inner, encKey, domain)
}

// resolveEncryptionKey resolves an encryption key reference to raw 32-byte key material.
// Supported schemes:
//   - hex:<64 hex chars> — inline key, no resolver needed (dev/test)
//   - anything else      — delegated to the secrets resolver (env:, vault://, gsm://, etc.)
//
// Returns an error if the resolved value is not exactly 32 bytes.
func resolveEncryptionKey(ctx context.Context, keyRef string, resolver secrets.Resolver) ([]byte, error) {
	if keyRef == "" {
		return nil, fmt.Errorf("encryption is enabled but key_ref is empty")
	}
	var hexStr string
	if strings.HasPrefix(keyRef, "hex:") {
		hexStr = strings.TrimPrefix(keyRef, "hex:")
	} else if resolver != nil {
		raw, err := resolver.Resolve(ctx, keyRef)
		if err != nil {
			return nil, fmt.Errorf("resolve key_ref %q: %w", keyRef, err)
		}
		hexStr = strings.TrimSpace(string(raw))
	} else {
		return nil, fmt.Errorf("encryption key_ref %q requires a secrets resolver (none configured); use hex: prefix for inline keys", keyRef)
	}
	key, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("decode hex encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes (AES-256), got %d bytes", len(key))
	}
	return key, nil
}

// resolveStoreCredentials returns a copy of storeCfg with credentials resolved
// through the secrets manager. All other fields are unchanged.
//
// Resolution order (highest precedence first):
//  1. UsernameRef / PasswordRef — explicit secret references (env:, enc:, vault:, etc.)
//     Resolved value is written into Username / Password.
//  2. Username / Password — may themselves be secret references (same scheme support)
//     or inline plaintext (no scheme prefix â†’ passed through unchanged).
func resolveStoreCredentials(ctx context.Context, storeCfg config.StoreConfig, resolver secrets.Resolver) (config.StoreConfig, error) {
	// Resolve UsernameRef first — overrides Username if set.
	if storeCfg.Connection.UsernameRef != "" {
		val, err := resolver.Resolve(ctx, storeCfg.Connection.UsernameRef)
		if err != nil {
			return storeCfg, fmt.Errorf("resolving username_ref for store %q: %w", storeCfg.Name, err)
		}
		storeCfg.Connection.Username = string(val)
		clear(val)
	} else if storeCfg.Connection.Username != "" {
		val, err := resolver.Resolve(ctx, storeCfg.Connection.Username)
		if err != nil {
			return storeCfg, fmt.Errorf("resolving username for store %q: %w", storeCfg.Name, err)
		}
		storeCfg.Connection.Username = string(val)
		clear(val)
	}

	// Resolve PasswordRef first — overrides Password if set.
	if storeCfg.Connection.PasswordRef != "" {
		val, err := resolver.Resolve(ctx, storeCfg.Connection.PasswordRef)
		if err != nil {
			return storeCfg, fmt.Errorf("resolving password_ref for store %q: %w", storeCfg.Name, err)
		}
		storeCfg.Connection.Password = string(val)
		clear(val)
	} else if storeCfg.Connection.Password != "" {
		val, err := resolver.Resolve(ctx, storeCfg.Connection.Password)
		if err != nil {
			return storeCfg, fmt.Errorf("resolving password for store %q: %w", storeCfg.Name, err)
		}
		storeCfg.Connection.Password = string(val)
		clear(val)
	}

	return storeCfg, nil
}

func (m *DataStoreManager) Snapshot() config.DataStoreConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	copyCfg := config.DataStoreConfig{
		Stores:   make(map[string]config.StoreConfig, len(m.active.Stores)),
		Bindings: make(map[config.DataDomain]string, len(m.active.Bindings)),
	}
	for k, v := range m.active.Stores {
		copyCfg.Stores[k] = v
	}
	for k, v := range m.active.Bindings {
		copyCfg.Bindings[k] = v
	}
	return copyCfg
}

func (m *DataStoreManager) Resolve(domain config.DataDomain) (config.StoreConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active.ResolveStore(domain)
}

func (m *DataStoreManager) IsConfigured(domain config.DataDomain) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.registryStore[domain]
	return ok
}

func (m *DataStoreManager) MutableDomains() []config.DataDomain {
	mutable := make([]config.DataDomain, 0)
	for _, domain := range []config.DataDomain{
		config.DomainTenantRegistry,
		config.DomainCache,
		config.DomainRateLimit,
		config.DomainCustomerData,
		config.DomainInstances,
	} {
		mutable = append(mutable, domain)
	}
	return mutable
}

func (m *DataStoreManager) PoolStats() map[config.DataDomain]datastore.PoolStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := make(map[config.DataDomain]datastore.PoolStats, len(m.registryStore))
	for domain, store := range m.registryStore {
		stats[domain] = store.PoolStats()
	}
	return stats
}

func (m *DataStoreManager) Put(ctx context.Context, domain config.DataDomain, tenant datastore.Tenant, key string, value []byte) error {
	store, err := m.resolveDomainStore(domain)
	if err != nil {
		return err
	}
	return store.Put(ctx, tenant, key, value)
}

func (m *DataStoreManager) Get(ctx context.Context, domain config.DataDomain, tenant datastore.Tenant, key string) ([]byte, bool, error) {
	store, err := m.resolveDomainStore(domain)
	if err != nil {
		return nil, false, err
	}
	return store.Get(ctx, tenant, key)
}

func (m *DataStoreManager) Delete(ctx context.Context, domain config.DataDomain, tenant datastore.Tenant, key string) error {
	store, err := m.resolveDomainStore(domain)
	if err != nil {
		return err
	}
	return store.Delete(ctx, tenant, key)
}

func (m *DataStoreManager) ListKeysByDomain(ctx context.Context, domain config.DataDomain, tenant datastore.Tenant, prefix string) ([]string, error) {
	store, err := m.resolveDomainStore(domain)
	if err != nil {
		return nil, err
	}
	return store.ListKeys(ctx, tenant, prefix)
}

func (m *DataStoreManager) PutGlobal(ctx context.Context, domain config.DataDomain, key string, value []byte) error {
	return m.Put(ctx, domain, GlobalTenant, key, value)
}

// MultiPutGlobal writes multiple key-value pairs to the given domain in a
// single round-trip when the backend supports BatchStore. Falls back to
// individual puts for backends that do not implement BatchStore.
func (m *DataStoreManager) MultiPutGlobal(ctx context.Context, domain config.DataDomain, kvs map[string][]byte) error {
	store, err := m.resolveDomainStore(domain)
	if err != nil {
		return err
	}
	if b, ok := store.(datastore.BatchStore); ok {
		return b.MultiPut(ctx, GlobalTenant, kvs)
	}
	for k, v := range kvs {
		if err := store.Put(ctx, GlobalTenant, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (m *DataStoreManager) GetGlobal(ctx context.Context, domain config.DataDomain, key string) ([]byte, bool, error) {
	return m.Get(ctx, domain, GlobalTenant, key)
}

func (m *DataStoreManager) DeleteGlobal(ctx context.Context, domain config.DataDomain, key string) error {
	return m.Delete(ctx, domain, GlobalTenant, key)
}

func (m *DataStoreManager) ListGlobalKeys(ctx context.Context, domain config.DataDomain, prefix string) ([]string, error) {
	return m.ListKeysByDomain(ctx, domain, GlobalTenant, prefix)
}

func (m *DataStoreManager) ReadGlobalDomainSnapshot(ctx context.Context, domain config.DataDomain) (map[string][]byte, error) {
	if !m.IsConfigured(domain) {
		return map[string][]byte{}, nil
	}
	keys, err := m.ListGlobalKeys(ctx, domain, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		v, ok, err := m.GetGlobal(ctx, domain, k)
		if err != nil {
			return nil, err
		}
		if ok {
			out[k] = v
		}
	}
	return out, nil
}

// ReadRegistryStoreSnapshot reads all tenant registry records for the given tenant when configured.
func (m *DataStoreManager) ReadRegistryStoreSnapshot(ctx context.Context, tenant datastore.Tenant) (map[string][]byte, error) {
	if !m.IsConfigured(config.DomainTenantRegistry) {
		return map[string][]byte{}, nil
	}
	keys, err := m.ListKeysByDomain(ctx, config.DomainTenantRegistry, tenant, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		v, ok, err := m.Get(ctx, config.DomainTenantRegistry, tenant, k)
		if err != nil {
			return nil, err
		}
		if ok {
			out[k] = v
		}
	}
	return out, nil
}

func (m *DataStoreManager) ReadAPIDefinitionsSnapshot(ctx context.Context) (map[string][]byte, error) {
	return m.ReadGlobalDomainSnapshot(ctx, config.DomainAPIDefinitions)
}

func (m *DataStoreManager) ReadFlowsSnapshot(ctx context.Context) (map[string][]byte, error) {
	return m.ReadGlobalDomainSnapshot(ctx, config.DomainFlows)
}

func (m *DataStoreManager) ReadRateLimitConfigsV2Snapshot(ctx context.Context) (map[string][]byte, error) {
	return m.ReadGlobalDomainSnapshot(ctx, config.DomainRateLimitConfigsV2)
}

func (m *DataStoreManager) ReadTiersSnapshot(ctx context.Context) (map[string][]byte, error) {
	return m.ReadGlobalDomainSnapshot(ctx, config.DomainTiers)
}

func (m *DataStoreManager) ReadUpstreamServicesSnapshot(ctx context.Context) (map[string][]byte, error) {
	return m.ReadGlobalDomainSnapshot(ctx, config.DomainUpstreamServices)
}

// â"€â"€â"€ RateLimitV2Datastore implementation (satisfies registry.RateLimitV2Datastore) â"€â"€

func (m *DataStoreManager) PutRateLimitConfigV2(ctx context.Context, name string, raw []byte) error {
	if !m.IsConfigured(config.DomainRateLimitConfigsV2) {
		return nil
	}
	return m.PutGlobal(ctx, config.DomainRateLimitConfigsV2, name, raw)
}

func (m *DataStoreManager) DeleteRateLimitConfigV2(ctx context.Context, name string) error {
	if !m.IsConfigured(config.DomainRateLimitConfigsV2) {
		return nil
	}
	return m.DeleteGlobal(ctx, config.DomainRateLimitConfigsV2, name)
}

func (m *DataStoreManager) PutTier(ctx context.Context, name string, raw []byte) error {
	if !m.IsConfigured(config.DomainTiers) {
		return nil
	}
	return m.PutGlobal(ctx, config.DomainTiers, name, raw)
}

func (m *DataStoreManager) DeleteTier(ctx context.Context, name string) error {
	if !m.IsConfigured(config.DomainTiers) {
		return nil
	}
	return m.DeleteGlobal(ctx, config.DomainTiers, name)
}

func (m *DataStoreManager) PutUpstreamService(ctx context.Context, name string, raw []byte) error {
	if !m.IsConfigured(config.DomainUpstreamServices) {
		return nil
	}
	return m.PutGlobal(ctx, config.DomainUpstreamServices, name, raw)
}

func (m *DataStoreManager) DeleteUpstreamService(ctx context.Context, name string) error {
	if !m.IsConfigured(config.DomainUpstreamServices) {
		return nil
	}
	return m.DeleteGlobal(ctx, config.DomainUpstreamServices, name)
}

func (m *DataStoreManager) RegisterInstance(ctx context.Context, instanceID string, payload []byte) error {
	if !m.IsConfigured(config.DomainInstances) {
		return nil
	}
	return m.Put(ctx, config.DomainInstances, SystemTenant, instanceID, payload)
}

func (m *DataStoreManager) ListInstances(ctx context.Context) ([]string, error) {
	if !m.IsConfigured(config.DomainInstances) {
		return []string{}, nil
	}
	return m.ListKeysByDomain(ctx, config.DomainInstances, SystemTenant, "")
}

// StoreFor returns the KeyValueStore for the given domain name, or nil if not configured.
// Used by the compiler to resolve stores at bake time for instruction capture.
func (m *DataStoreManager) StoreFor(domain string) datastore.KeyValueStore {
	store, err := m.resolveDomainStore(config.DataDomain(domain))
	if err != nil {
		return nil
	}
	return store
}

func (m *DataStoreManager) resolveDomainStore(domain config.DataDomain) (datastore.KeyValueStore, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	store, ok := m.registryStore[domain]
	if !ok {
		return nil, config.ErrDomainNotConfigured(domain)
	}
	return store, nil
}

// DataStoreConfigHandler allows customer runtime updates when infra changes.
func (m *DataStoreManager) DataStoreConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch {
		m.dataStorePatchHandler(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		m.dataStoreDeleteHandler(w, r)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/test") {
		m.dataStoreTestHandler(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		instances, _ := m.ListInstances(r.Context())
		_ = json.NewEncoder(w).Encode(struct {
			SupportedKinds []config.StoreKind                        `json:"supported_kinds"`
			Config         config.DataStoreConfig                    `json:"config"`
			Pools          map[config.DataDomain]datastore.PoolStats `json:"pools"`
			Instances      []string                                  `json:"instances,omitempty"`
			MutableDomains []config.DataDomain                       `json:"mutable_domains"`
		}{
			SupportedKinds: config.SupportedStoreKinds(),
			Config:         m.Snapshot(),
			Pools:          m.PoolStats(),
			Instances:      instances,
			MutableDomains: m.MutableDomains(),
		})
	case http.MethodPost:
		var cfg config.DataStoreConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := m.Update(r.Context(), cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *DataStoreManager) dataStorePatchHandler(w http.ResponseWriter, r *http.Request) {
	var storeCfg config.StoreConfig
	if err := json.NewDecoder(r.Body).Decode(&storeCfg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if storeCfg.Name == "" {
		http.Error(w, "name field required", http.StatusBadRequest)
		return
	}
	supported := map[config.StoreKind]struct{}{
		config.StoreDisk: {}, config.StoreRedis: {}, config.StoreMongoDB: {},
		config.StoreDragonFly: {}, config.StorePostgreSQL: {}, config.StoreCassandra: {},
	}
	if _, ok := supported[storeCfg.Kind]; !ok {
		http.Error(w, fmt.Sprintf("unsupported store kind: %s", storeCfg.Kind), http.StatusBadRequest)
		return
	}
	snapshot := m.Snapshot()
	snapshot.Stores[storeCfg.Name] = storeCfg
	if err := m.Update(r.Context(), snapshot); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(storeCfg)
}

func (m *DataStoreManager) dataStoreDeleteHandler(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	storeName := pathParts[len(pathParts)-1]
	if storeName == "" || storeName == "datastores" {
		http.Error(w, "store name required in path", http.StatusBadRequest)
		return
	}
	snapshot := m.Snapshot()
	var refs []string
	for domain, name := range snapshot.Bindings {
		if name == storeName {
			refs = append(refs, string(domain))
		}
	}
	if len(refs) > 0 {
		http.Error(w, fmt.Sprintf("store %q referenced by bindings: %v", storeName, refs), http.StatusConflict)
		return
	}
	delete(snapshot.Stores, storeName)
	if err := m.Update(r.Context(), snapshot); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *DataStoreManager) dataStoreTestHandler(w http.ResponseWriter, r *http.Request) {
	var storeCfg config.StoreConfig
	if err := json.NewDecoder(r.Body).Decode(&storeCfg); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	latencyMs, err := m.testStoreConnection(r.Context(), storeCfg)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "latency_ms": latencyMs})
}

func (m *DataStoreManager) testStoreConnection(ctx context.Context, cfg config.StoreConfig) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch cfg.Kind {
	case config.StoreRedis:
		return m.testRedisConnection(ctx, cfg.Connection)
	case config.StorePostgreSQL:
		return m.testPostgresConnection(ctx, cfg.Connection)
	case config.StoreDisk:
		return m.testDiskConnection(ctx, cfg.Connection)
	default:
		return 0, fmt.Errorf("test not implemented for store kind %q", cfg.Kind)
	}
}

func (m *DataStoreManager) testRedisConnection(ctx context.Context, conn config.StoreConnection) (int64, error) {
	start := time.Now()
	client := goredis.NewClient(&goredis.Options{Addr: conn.EffectiveAddress(), Password: conn.Password})
	defer func() { _ = client.Close() }()
	if err := client.Ping(ctx).Err(); err != nil {
		return 0, fmt.Errorf("redis ping: %w", err)
	}
	return time.Since(start).Milliseconds(), nil
}

func buildStoreDSN(c config.StoreConnection) string {
	if c.Address != "" {
		return c.Address
	}
	if c.Host == "" {
		return ""
	}
	if c.Port > 0 {
		return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			c.Host, c.Port, c.Username, c.Password, c.Database)
	}
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
		c.Host, c.Username, c.Password, c.Database)
}

func (m *DataStoreManager) testPostgresConnection(ctx context.Context, conn config.StoreConnection) (int64, error) {
	start := time.Now()
	cfg, err := pgxpool.ParseConfig(buildStoreDSN(conn))
	if err != nil {
		return 0, fmt.Errorf("postgres config: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("postgres connect: %w", err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres ping: %w", err)
	}
	return time.Since(start).Milliseconds(), nil
}

func (m *DataStoreManager) testDiskConnection(_ context.Context, conn config.StoreConnection) (int64, error) {
	start := time.Now()
	if conn.Path == "" {
		return 0, fmt.Errorf("disk path required")
	}
	tmp := filepath.Join(conn.Path, fmt.Sprintf(".health-%d", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, []byte("ok"), 0644); err != nil {
		return 0, fmt.Errorf("disk write: %w", err)
	}
	if _, err := os.ReadFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("disk read: %w", err)
	}
	_ = os.Remove(tmp)
	return time.Since(start).Milliseconds(), nil
}

// â"€â"€â"€ apikey.Store implementation â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func (m *DataStoreManager) PutApp(ctx context.Context, appID uint32, raw []byte) error {
	if !m.IsConfigured(config.DomainApps) {
		return nil
	}
	return m.PutGlobal(ctx, config.DomainApps, strconv.FormatUint(uint64(appID), 10), raw)
}

func (m *DataStoreManager) DeleteApp(ctx context.Context, appID uint32) error {
	if !m.IsConfigured(config.DomainApps) {
		return nil
	}
	return m.DeleteGlobal(ctx, config.DomainApps, strconv.FormatUint(uint64(appID), 10))
}

func (m *DataStoreManager) ListApps(ctx context.Context) ([][]byte, error) {
	if !m.IsConfigured(config.DomainApps) {
		return [][]byte{}, nil
	}
	keys, err := m.ListGlobalKeys(ctx, config.DomainApps, "")
	if err != nil {
		return nil, err
	}
	result := make([][]byte, 0, len(keys))
	for _, k := range keys {
		v, ok, err := m.GetGlobal(ctx, config.DomainApps, k)
		if err != nil {
			return nil, err
		}
		if ok {
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *DataStoreManager) PutKey(ctx context.Context, keyID uint32, raw []byte) error {
	if !m.IsConfigured(config.DomainAPIKeys) {
		return nil
	}
	return m.PutGlobal(ctx, config.DomainAPIKeys, strconv.FormatUint(uint64(keyID), 10), raw)
}

func (m *DataStoreManager) DeleteKey(ctx context.Context, keyID uint32) error {
	if !m.IsConfigured(config.DomainAPIKeys) {
		return nil
	}
	return m.DeleteGlobal(ctx, config.DomainAPIKeys, strconv.FormatUint(uint64(keyID), 10))
}

func (m *DataStoreManager) ListKeys(ctx context.Context) ([][]byte, error) {
	if !m.IsConfigured(config.DomainAPIKeys) {
		return [][]byte{}, nil
	}
	keys, err := m.ListGlobalKeys(ctx, config.DomainAPIKeys, "")
	if err != nil {
		return nil, err
	}
	result := make([][]byte, 0, len(keys))
	for _, k := range keys {
		v, ok, err := m.GetGlobal(ctx, config.DomainAPIKeys, k)
		if err != nil {
			return nil, err
		}
		if ok {
			result = append(result, v)
		}
	}
	return result, nil
}

// compile-time interface check
var _ apikey.Store = (*DataStoreManager)(nil)
