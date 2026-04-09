package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"rah/internal/config"
	"rah/internal/datastore"
	"rah/internal/secrets"
	"sync"
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

// resolveStoreCredentials returns a copy of storeCfg with Username and Password
// resolved through the secrets manager. All other fields are unchanged.
func resolveStoreCredentials(ctx context.Context, storeCfg config.StoreConfig, resolver secrets.Resolver) (config.StoreConfig, error) {
	if storeCfg.Connection.Username != "" {
		val, err := resolver.Resolve(ctx, storeCfg.Connection.Username)
		if err != nil {
			return storeCfg, fmt.Errorf("resolving username for store %q: %w", storeCfg.Name, err)
		}
		storeCfg.Connection.Username = string(val)
		clear(val)
	}
	if storeCfg.Connection.Password != "" {
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

func (m *DataStoreManager) ListKeys(ctx context.Context, domain config.DataDomain, tenant datastore.Tenant, prefix string) ([]string, error) {
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
	return m.ListKeys(ctx, domain, GlobalTenant, prefix)
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
	keys, err := m.ListKeys(ctx, config.DomainTenantRegistry, tenant, "")
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
	return m.ListKeys(ctx, config.DomainInstances, SystemTenant, "")
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
