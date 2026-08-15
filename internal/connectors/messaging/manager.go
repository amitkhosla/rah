package messaging

import (
	"context"
	"fmt"
	"sort"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// MessagePublisherManager holds named MessagePublishers keyed by publisher name.
// Initialized at gateway startup from []PublisherConfig.
type MessagePublisherManager struct {
	publishers map[string]MessagePublisher
	kinds      map[string]string
}

// New creates a MessagePublisherManager from configs.
// It initializes the publishers but does not connect them; connections are
// established lazily on first use or by calling Start().
func New(ctx context.Context, configs []config.PublisherConfig, secretResolver SecretResolver) (*MessagePublisherManager, error) {
	m := &MessagePublisherManager{
		publishers: make(map[string]MessagePublisher, len(configs)),
		kinds:      make(map[string]string, len(configs)),
	}

	for _, cfg := range configs {
		publisher, err := NewMessagePublisher(ctx, cfg, secretResolver)
		if err != nil {
			return nil, fmt.Errorf("publisher[%s]: %w", cfg.Name, err)
		}
		m.publishers[cfg.Name] = publisher
		m.kinds[cfg.Name] = cfg.Kind
		gatewaylog.Default.Info("[MessagePublisher] registered", gatewaylog.F("name", cfg.Name), gatewaylog.F("kind", cfg.Kind))
	}

	return m, nil
}

// Get retrieves a MessagePublisher by name. Returns (nil, false) if not found.
func (m *MessagePublisherManager) Get(name string) (MessagePublisher, bool) {
	publisher, ok := m.publishers[name]
	return publisher, ok
}

// Start initializes all message publishers (connects, pings, etc.).
// Called at gateway startup after wiring.
// If any publisher fails to connect, logs the error and continues with the rest.
func (m *MessagePublisherManager) Start(ctx context.Context) error {
	for name, publisher := range m.publishers {
		if err := publisher.Connect(ctx); err != nil {
			gatewaylog.Default.Error("[MessagePublisher] startup connect failed", gatewaylog.F("name", name), gatewaylog.F("error", err.Error()))
		}
	}
	return nil
}

// Stop closes all message publishers and releases resources.
// Nil-safe — publishers that never connected must not panic on Close.
func (m *MessagePublisherManager) Stop() error {
	var lastErr error
	for name, publisher := range m.publishers {
		if publisher == nil {
			continue
		}
		if err := publisher.Close(); err != nil {
			gatewaylog.Default.Error("[MessagePublisher] close error", gatewaylog.F("name", name), gatewaylog.F("error", err.Error()))
			lastErr = err
		}
	}
	return lastErr
}

// Names returns the names of all registered publishers.
func (m *MessagePublisherManager) Names() []string {
	names := make([]string, 0, len(m.publishers))
	for name := range m.publishers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Kinds returns a map of publisher name → kind string.
func (m *MessagePublisherManager) Kinds() map[string]string {
	out := make(map[string]string, len(m.kinds))
	for k, v := range m.kinds {
		out[k] = v
	}
	return out
}
