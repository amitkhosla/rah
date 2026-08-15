package document

import (
	"context"
	"fmt"
	"sort"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// DocumentConnectorManager holds named DocumentProviders keyed by connector name.
// Initialized at gateway startup from []DocumentConnectorConfig.
type DocumentConnectorManager struct {
	providers map[string]DocumentProvider
	kinds     map[string]string
}

// New creates a DocumentConnectorManager from configs.
// It initializes the providers but does not connect them; connections are
// established lazily on first use or by calling Start().
func New(ctx context.Context, configs []config.DocumentConnectorConfig, secretResolver SecretResolver) (*DocumentConnectorManager, error) {
	m := &DocumentConnectorManager{
		providers: make(map[string]DocumentProvider, len(configs)),
		kinds:     make(map[string]string, len(configs)),
	}

	for _, cfg := range configs {
		provider, err := NewDocumentProvider(ctx, cfg, secretResolver)
		if err != nil {
			return nil, fmt.Errorf("document connector %s: %w", cfg.Name, err)
		}
		batching := newBatchingDocumentProvider(provider, cfg)
		m.providers[cfg.Name] = batching
		m.kinds[cfg.Name] = cfg.Kind
		gatewaylog.Default.Info("[DocumentConnector] registered", gatewaylog.F("name", cfg.Name), gatewaylog.F("kind", cfg.Kind))
	}

	return m, nil
}

// Get retrieves a DocumentProvider by name. Returns (nil, false) if not found.
func (m *DocumentConnectorManager) Get(name string) (DocumentProvider, bool) {
	provider, ok := m.providers[name]
	return provider, ok
}

// Start initializes all document providers (connects, pings, etc.).
// Called at gateway startup after wiring.
func (m *DocumentConnectorManager) Start(ctx context.Context) error {
	for name, provider := range m.providers {
		if err := provider.Ping(ctx); err != nil {
			return fmt.Errorf("document connector %s: startup ping failed: %w", name, err)
		}
	}
	for _, p := range m.providers {
		if bp, ok := p.(*BatchingDocumentProvider); ok {
			bp.start(ctx)
		}
	}
	return nil
}

// Stop closes all document providers and releases resources.
func (m *DocumentConnectorManager) Stop() error {
	var lastErr error
	for name, provider := range m.providers {
		if err := provider.Close(); err != nil {
			gatewaylog.Default.Error("[DocumentConnector] close error", gatewaylog.F("name", name), gatewaylog.F("error", err.Error()))
			lastErr = err
		}
	}
	return lastErr
}

// Names returns the names of all registered providers.
func (m *DocumentConnectorManager) Names() []string {
	names := make([]string, 0, len(m.providers))
	for name := range m.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Kinds returns a map of connector name → kind string.
func (m *DocumentConnectorManager) Kinds() map[string]string {
	out := make(map[string]string, len(m.kinds))
	for k, v := range m.kinds {
		out[k] = v
	}
	return out
}
