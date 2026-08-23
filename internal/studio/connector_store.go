package studio

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/amitkhosla/rah/internal/storage"
)

// connectorStore holds storage provider configs in memory and optionally persists them
// to a JSON file so they survive studio restarts.
type connectorStore struct {
	mu       sync.RWMutex
	configs  map[string]storage.StorageProviderConfig
	filePath string // "" = memory-only
}

func newConnectorStore(initial []storage.StorageProviderConfig, filePath string) (*connectorStore, error) {
	s := &connectorStore{
		configs:  make(map[string]storage.StorageProviderConfig),
		filePath: filePath,
	}
	// Load previously saved connectors from file first.
	if filePath != "" {
		if data, err := os.ReadFile(filePath); err == nil {
			var list []storage.StorageProviderConfig
			if json.Unmarshal(data, &list) == nil {
				for _, cfg := range list {
					s.configs[cfg.Name] = cfg
				}
			}
		}
	}
	// Seed with connectors from static config — don't overwrite file-loaded ones.
	for _, cfg := range initial {
		if _, exists := s.configs[cfg.Name]; !exists {
			s.configs[cfg.Name] = cfg
		}
	}
	return s, nil
}

func (s *connectorStore) List() []storage.StorageProviderConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]storage.StorageProviderConfig, 0, len(s.configs))
	for _, v := range s.configs {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *connectorStore) Get(name string) (storage.StorageProviderConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.configs[name]
	return v, ok
}

func (s *connectorStore) Set(cfg storage.StorageProviderConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("connector name is required")
	}
	if cfg.Type == "" {
		return fmt.Errorf("connector type is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[cfg.Name] = cfg
	return s.persist()
}

func (s *connectorStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.configs[name]; !ok {
		return fmt.Errorf("connector %q not found", name)
	}
	delete(s.configs, name)
	return s.persist()
}

// persist writes the current configs to the file (caller must hold mu).
func (s *connectorStore) persist() error {
	if s.filePath == "" {
		return nil
	}
	list := make([]storage.StorageProviderConfig, 0, len(s.configs))
	for _, v := range s.configs {
		list = append(list, v)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0600)
}
