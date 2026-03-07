package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"rah/internal/config"
	"rah/internal/datastore"
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

// DataStoreManager keeps the active data-store topology selected per customer.
type DataStoreManager struct {
	mu            sync.RWMutex
	active        config.DataStoreConfig
	registryStore map[config.DataDomain]datastore.KeyValueStore
}

func NewDataStoreManager(initial config.DataStoreConfig) (*DataStoreManager, error) {
	if err := initial.Validate(); err != nil {
		return nil, err
	}

	stores, err := buildDomainStores(initial)
	if err != nil {
		return nil, err
	}

	return &DataStoreManager{active: initial, registryStore: stores}, nil
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

func (m *DataStoreManager) Update(cfg config.DataStoreConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	m.mu.RLock()
	oldCfg := m.active
	m.mu.RUnlock()
	if err := validateImmutableStartupDomains(oldCfg, cfg); err != nil {
		return err
	}

	stores, err := buildDomainStores(cfg)
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

func buildDomainStores(cfg config.DataStoreConfig) (map[config.DataDomain]datastore.KeyValueStore, error) {
	stores := make(map[config.DataDomain]datastore.KeyValueStore, len(cfg.Bindings))
	for domain := range cfg.Bindings {
		storeCfg, err := cfg.ResolveStore(domain)
		if err != nil {
			return nil, err
		}
		store, err := datastore.NewStore(storeCfg, domain)
		if err != nil {
			return nil, err
		}
		stores[domain] = store
	}
	return stores, nil
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

func (m *DataStoreManager) GetGlobal(ctx context.Context, domain config.DataDomain, key string) ([]byte, bool, error) {
	return m.Get(ctx, domain, GlobalTenant, key)
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
	if !m.IsConfigured(config.DomainRegistryStore) {
		return map[string][]byte{}, nil
	}
	keys, err := m.ListKeys(ctx, config.DomainRegistryStore, tenant, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		v, ok, err := m.Get(ctx, config.DomainRegistryStore, tenant, k)
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
		if err := m.Update(cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
