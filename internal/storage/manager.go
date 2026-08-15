package storage

import (
	"context"
	"fmt"
	"sort"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

type StorageManager struct {
	providers map[string]storageProvider // read-only after New(), no mutex needed
}

func New(configs []StorageProviderConfig) (*StorageManager, error) {
	m := &StorageManager{providers: make(map[string]storageProvider, len(configs))}
	for _, cfg := range configs {
		var p storageProvider
		var err error
		switch cfg.Type {
		case "s3", "":
			p, err = newS3Provider(cfg)
		case "gcs":
			p, err = newGCSProvider(cfg)
		case "local":
			p, err = newLocalProvider(cfg)
		default:
			return nil, fmt.Errorf("storage %q: unknown type %q", cfg.Name, cfg.Type)
		}
		if err != nil {
			return nil, err
		}
		m.providers[cfg.Name] = p
		gatewaylog.Default.Info("[Storage] registered", gatewaylog.F("name", cfg.Name), gatewaylog.F("type", cfg.Type))
	}
	return m, nil
}

func (m *StorageManager) Get(ctx context.Context, providerName, key string) ([]byte, error) {
	p, ok := m.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("storage provider %q not found", providerName)
	}
	return p.Get(ctx, key)
}

func (m *StorageManager) Put(ctx context.Context, providerName, key string, content []byte, contentType string) error {
	p, ok := m.providers[providerName]
	if !ok {
		return fmt.Errorf("storage provider %q not found", providerName)
	}
	return p.Put(ctx, key, content, contentType)
}

func (m *StorageManager) Delete(ctx context.Context, providerName, key string) error {
	p, ok := m.providers[providerName]
	if !ok {
		return fmt.Errorf("storage provider %q not found", providerName)
	}
	return p.Delete(ctx, key)
}

// Names returns a sorted slice of all provider names.
// Returns an empty slice (not nil) when providers is empty.
func (m *StorageManager) Names() []string {
	names := make([]string, 0, len(m.providers))
	for name := range m.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
