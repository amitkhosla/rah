package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Manager holds the fully parsed gateway configuration and exposes typed
// accessors for each component. All components receive their config slice
// through the Manager rather than constructing it independently.
type Manager struct {
	mu      sync.RWMutex
	gateway GatewayConfig
}

// Load reads a JSON (.json) or YAML (.yaml / .yml) config file and returns a
// validated Manager. The file format is detected from the file extension.
func Load(path string) (*Manager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}

	var cfg GatewayConfig
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err = json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing JSON config %q: %w", path, err)
		}
	case ".yaml", ".yml":
		if err = yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing YAML config %q: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unsupported config format %q — use .json, .yaml, or .yml", ext)
	}

	if err = cfg.DataStore.Validate(); err != nil {
		return nil, fmt.Errorf("invalid datastore config: %w", err)
	}

	return &Manager{gateway: cfg}, nil
}

// Default returns a Manager with hardcoded development defaults (local disk store).
// Used when no config file is provided.
func Default() *Manager {
	return &Manager{
		gateway: GatewayConfig{
			Layout: GlobalLayout{
				MaxBytesSlots: 32,
				MaxIntsSlots:  16,
				DefaultLimits: ResourceLimit{
					MaxBodySize: 1024 * 1024, // 1MB
				},
			},
			DataStore: DataStoreConfig{
				Stores: map[string]StoreConfig{
					"local_disk": {
						Name:    "local_disk",
						Kind:    StoreDisk,
						Enabled: true,
						Connection: StoreConnection{
							Path: "/var/lib/rah",
						},
					},
				},
				Bindings: map[DataDomain]string{
					DomainAPIDefinitions: "local_disk",
					DomainFlows:          "local_disk",
					DomainTenantRegistry: "local_disk",
					DomainCache:          "local_disk",
					DomainRateLimit:      "local_disk",
					DomainCustomerData:   "local_disk",
					DomainInstances:      "local_disk",
				},
			},
		},
	}
}

// Layout returns the global slot/resource layout config.
func (m *Manager) Layout() GlobalLayout { return m.gateway.Layout }

// DataStore returns the datastore bindings and store configs.
func (m *Manager) DataStore() DataStoreConfig { return m.gateway.DataStore }

// Gateway returns the full top-level config (useful for serialisation/debug).
func (m *Manager) Gateway() GatewayConfig { return m.gateway }

// Secrets returns the credential provider configuration.
func (m *Manager) Secrets() SecretsConfig { return m.gateway.Secrets }

// Cache returns the cache configuration.
func (m *Manager) Cache() CacheConfig { return m.gateway.Cache }

// Async returns the async job execution subsystem configuration.
func (m *Manager) Async() AsyncConfig { return m.gateway.Async }

// LLM returns a snapshot of the LLM catalog (models + MCP servers).
func (m *Manager) LLM() LLMConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a shallow copy so the caller cannot mutate in-place.
	cfg := m.gateway.LLM
	models := make([]LLMModelConfig, len(cfg.Models))
	copy(models, cfg.Models)
	cfg.Models = models
	servers := make([]MCPServerConfig, len(cfg.MCPServers))
	copy(servers, cfg.MCPServers)
	cfg.MCPServers = servers
	return cfg
}

// UpsertLLMModel adds or updates an LLM model in the live catalog.
// The model is matched by Alias; if no existing entry has the same Alias
// the model is appended.
func (m *Manager) UpsertLLMModel(model LLMModelConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.gateway.LLM.Models {
		if existing.Alias == model.Alias {
			m.gateway.LLM.Models[i] = model
			return
		}
	}
	m.gateway.LLM.Models = append(m.gateway.LLM.Models, model)
}

// DeleteLLMModel removes a model by alias. Returns false if not found.
func (m *Manager) DeleteLLMModel(slug string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.gateway.LLM.Models {
		if existing.Alias == slug {
			m.gateway.LLM.Models = append(m.gateway.LLM.Models[:i], m.gateway.LLM.Models[i+1:]...)
			return true
		}
	}
	return false
}

// UpsertMCPServer adds or updates an MCP server config.
// The server is matched by Alias; if no existing entry has the same Alias
// the server is appended.
func (m *Manager) UpsertMCPServer(srv MCPServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.gateway.LLM.MCPServers {
		if existing.Alias == srv.Alias {
			m.gateway.LLM.MCPServers[i] = srv
			return
		}
	}
	m.gateway.LLM.MCPServers = append(m.gateway.LLM.MCPServers, srv)
}

// DeleteMCPServer removes an MCP server by alias. Returns false if not found.
func (m *Manager) DeleteMCPServer(alias string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.gateway.LLM.MCPServers {
		if existing.Alias == alias {
			m.gateway.LLM.MCPServers = append(m.gateway.LLM.MCPServers[:i], m.gateway.LLM.MCPServers[i+1:]...)
			return true
		}
	}
	return false
}

// Quotas returns the current quota configuration snapshot.
func (m *Manager) Quotas() QuotasConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := m.gateway.Quotas
	tenants := make([]TenantQuotaConfig, len(q.Tenants))
	copy(tenants, q.Tenants)
	q.Tenants = tenants
	return q
}

// UpsertTenantQuota adds or updates the quota entry for a tenant.
func (m *Manager) UpsertTenantQuota(q TenantQuotaConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.gateway.Quotas.Tenants {
		if t.TenantID == q.TenantID {
			m.gateway.Quotas.Tenants[i] = q
			return
		}
	}
	m.gateway.Quotas.Tenants = append(m.gateway.Quotas.Tenants, q)
}

// DeleteTenantQuota removes the quota entry for a tenant. Returns false if not found.
func (m *Manager) DeleteTenantQuota(tenantID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.gateway.Quotas.Tenants {
		if t.TenantID == tenantID {
			m.gateway.Quotas.Tenants = append(m.gateway.Quotas.Tenants[:i], m.gateway.Quotas.Tenants[i+1:]...)
			return true
		}
	}
	return false
}
