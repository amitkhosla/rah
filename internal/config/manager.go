package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manager holds the fully parsed gateway configuration and exposes typed
// accessors for each component. All components receive their config slice
// through the Manager rather than constructing it independently.
type Manager struct {
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
