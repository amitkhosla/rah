package sftp

import (
	"context"
	"fmt"
	"sort"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// SFTPConnectorManager holds named SFTP providers keyed by provider name.
// Initialized at gateway startup from []SFTPConnectorConfig.
type SFTPConnectorManager struct {
	providers map[string]*sftpProvider
}

// New creates a SFTPConnectorManager from configs.
// It constructs all sftpProvider instances but does not dial yet.
// Connections are established lazily on first use or by calling Start().
func New(ctx context.Context, configs []SFTPConnectorConfig, secrets SecretResolver) (*SFTPConnectorManager, error) {
	m := &SFTPConnectorManager{
		providers: make(map[string]*sftpProvider, len(configs)),
	}

	for _, cfg := range configs {
		if cfg.Name == "" {
			return nil, fmt.Errorf("sftp connector config missing name")
		}

		if cfg.Host == "" {
			return nil, fmt.Errorf("sftp connector %s: missing host", cfg.Name)
		}

		if cfg.Username == "" {
			return nil, fmt.Errorf("sftp connector %s: missing username", cfg.Name)
		}

		if cfg.PasswordRef == "" && cfg.PrivateKeyRef == "" {
			return nil, fmt.Errorf("sftp connector %s: must specify either password_ref or private_key_ref", cfg.Name)
		}

		if secrets == nil {
			return nil, fmt.Errorf("sftp connector %s: secretResolver is nil", cfg.Name)
		}

		provider := newSFTPProvider(cfg, secrets)
		m.providers[cfg.Name] = provider

		gatewaylog.Default.Info("[SFTPConnector] registered", gatewaylog.F("name", cfg.Name))
	}

	return m, nil
}

// Start initializes all SFTP providers by establishing connections.
// Called at gateway startup after wiring.
// If any provider fails to connect, logs the error and continues with the rest.
func (m *SFTPConnectorManager) Start(ctx context.Context) error {
	for name, provider := range m.providers {
		if err := provider.ensureConnected(ctx); err != nil {
			gatewaylog.Default.Error("[SFTPConnector] startup connect failed", gatewaylog.F("name", name), gatewaylog.F("error", err.Error()))
		}
	}
	return nil
}

// Stop closes all SFTP providers and releases resources.
func (m *SFTPConnectorManager) Stop() error {
	var lastErr error
	for name, provider := range m.providers {
		if provider == nil {
			continue
		}
		if err := provider.Close(); err != nil {
			gatewaylog.Default.Error("[SFTPConnector] close error", gatewaylog.F("name", name), gatewaylog.F("error", err.Error()))
			lastErr = err
		}
	}
	return lastErr
}

// Get retrieves an SFTPProvider by name. Returns (nil, false) if not found.
func (m *SFTPConnectorManager) Get(name string) (SFTPProvider, bool) {
	provider, ok := m.providers[name]
	if !ok {
		return nil, false
	}
	return provider, true
}

// Names returns a sorted list of all registered provider names.
func (m *SFTPConnectorManager) Names() []string {
	names := make([]string, 0, len(m.providers))
	for name := range m.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
